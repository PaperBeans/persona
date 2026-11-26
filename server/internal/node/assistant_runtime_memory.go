package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	toolMemoryRead  = "memory_read"
	toolMemoryWrite = "memory_write"
	memoryToolCap   = 64 * 1024
)

func isMemoryTool(name string) bool {
	return name == toolMemoryRead || name == toolMemoryWrite
}

func (n *Node) recordedRuntimeSessionCwd(session string) string {
	if n == nil || session == "" {
		return ""
	}
	if n.state != nil {
		if st, ok := n.state.get(session); ok && st.Cwd != "" {
			return st.Cwd
		}
	}
	if n.runtimes != nil {
		if row, _, ok := n.runtimes.rowOf(session); ok && row.Cwd != "" {
			return row.Cwd
		}
	}
	return ""
}

func (n *Node) sessionAllowsMemoryTools(session string) (string, bool) {
	if n == nil || session == "" || n.guestOf(session) != nil {
		return "", false
	}
	cwd := n.recordedRuntimeSessionCwd(session)
	if cwd == "" || !identity.IsAssistantDir(filepath.Clean(cwd)) {
		return "", false
	}
	return cwd, true
}

func (n *Node) memoryTools() []proto.Tool {
	return []proto.Tool{
		{
			Name:        toolMemoryRead,
			Description: "Read a markdown note from the assistant's shared memory directory (for example MEMORY.md). Returns empty content when the memory directory or note does not exist yet.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Filename in the memory directory, ending in .md (no path separators)."}},"required":["name"],"additionalProperties":false}`),
		},
		{
			Name:        toolMemoryWrite,
			Description: "Create or overwrite a markdown note in the assistant's shared memory directory. Refused in plan mode.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Filename in the memory directory, ending in .md (no path separators)."},"content":{"type":"string","description":"Markdown content to write (at most 64 KiB)."}},"required":["name","content"],"additionalProperties":false}`),
		},
	}
}

func (n *Node) assistantMemoryTool(_ context.Context, tool, session string, raw json.RawMessage) proto.ToolResult {
	if n == nil || session == "" {
		return proto.TextResult("Memory tools require an assistant session", true)
	}
	if n.guestOf(session) != nil {
		return proto.TextResult("Memory tools are not available in guest sessions", true)
	}
	cwd, ok := n.sessionAllowsMemoryTools(session)
	if !ok {
		return proto.TextResult("Memory tools are available only in the assistant workspace", true)
	}

	var args struct {
		Name    string  `json:"name"`
		Content *string `json:"content"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return proto.TextResult("Invalid memory tool arguments: "+err.Error(), true)
	}
	if dec.More() {
		return proto.TextResult("Invalid memory tool arguments: unexpected trailing data", true)
	}
	if !memoryNameRe.MatchString(args.Name) {
		return proto.TextResult("Invalid memory filename: must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,100}\\.md$", true)
	}

	if tool == toolMemoryWrite {
		if args.Content == nil {
			return proto.TextResult("memory_write requires content", true)
		}
		if len(*args.Content) > memoryToolCap {
			return proto.TextResult(fmt.Sprintf("Memory note exceeds %d bytes", memoryToolCap), true)
		}
		mode, _ := n.modesOf(session)
		if mode == proto.ModePlan {
			return proto.TextResult("memory_write is refused in plan permission mode", true)
		}
	} else if args.Content != nil {
		return proto.TextResult("memory_read does not accept content", true)
	}

	n.memoryMu.Lock()
	defer n.memoryMu.Unlock()

	dir := n.mergedMemoryDir(cwd)
	if dir == "" {
		return proto.TextResult("Assistant memory directory is unavailable", true)
	}

	if tool == toolMemoryRead {
		resolvedDir, _, lstatInfo, err := resolveMemoryTarget(dir, args.Name, false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return proto.TextResult("", false)
			}
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		root, err := os.OpenRoot(resolvedDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return proto.TextResult("", false)
			}
			return proto.TextResult("Cannot open memory directory: "+err.Error(), true)
		}
		defer root.Close()
		f, err := openKnowledgeFile(root, args.Name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return proto.TextResult("", false)
			}
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		if !opened.Mode().IsRegular() {
			return proto.TextResult("Cannot read memory file: memory note target must be a regular file", true)
		}
		if lstatInfo != nil && !os.SameFile(lstatInfo, opened) {
			return proto.TextResult("Cannot read memory file: memory note target changed during open", true)
		}
		data, err := io.ReadAll(io.LimitReader(f, memoryToolCap+1))
		if err != nil {
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		if len(data) > memoryToolCap {
			cut := memoryToolCap
			for cut > 0 && !utf8.RuneStart(data[cut]) {
				cut--
			}
			return proto.TextResult(string(data[:cut])+"\n...(truncated)", false)
		}
		return proto.TextResult(string(data), false)
	}

	resolvedDir, target, _, err := resolveMemoryTarget(dir, args.Name, true)
	if err != nil {
		return proto.TextResult("Cannot write memory file: "+err.Error(), true)
	}

	tmp, err := os.CreateTemp(resolvedDir, ".mem-*.tmp")
	if err != nil {
		return proto.TextResult("Cannot stage memory file: "+err.Error(), true)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return proto.TextResult("Cannot set memory file permissions: "+err.Error(), true)
	}
	if _, err := tmp.WriteString(*args.Content); err != nil {
		_ = tmp.Close()
		return proto.TextResult("Cannot write memory file: "+err.Error(), true)
	}
	if err := tmp.Close(); err != nil {
		return proto.TextResult("Cannot flush memory file: "+err.Error(), true)
	}
	if st, err := os.Lstat(target); err == nil && !st.Mode().IsRegular() {
		return proto.TextResult("Cannot write memory file: memory note target must be a regular file", true)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return proto.TextResult("Cannot commit memory file: "+err.Error(), true)
	}
	keep = true
	return proto.TextResult(fmt.Sprintf("Wrote %s (%d bytes).", args.Name, len(*args.Content)), false)
}

// resolveMemoryTarget resolves the memory directory through any user-configured symlink and ensures
// the note file itself is a regular file (or absent when creating) inside it.
func resolveMemoryTarget(dir, name string, create bool) (string, string, os.FileInfo, error) {
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", nil, err
		}
	} else if _, err := os.Lstat(dir); err != nil {
		return "", "", nil, err
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if !create && errors.Is(err, os.ErrNotExist) {
			return "", "", nil, errors.New("memory directory symlink target does not exist")
		}
		return "", "", nil, err
	}
	dinfo, err := os.Stat(resolvedDir)
	if err != nil {
		return "", "", nil, err
	}
	if !dinfo.IsDir() {
		return "", "", nil, errors.New("memory directory path is not a directory")
	}
	target := filepath.Join(resolvedDir, name)
	rel, err := filepath.Rel(resolvedDir, filepath.Clean(target))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) || strings.ContainsRune(rel, filepath.Separator) {
		return "", "", nil, errors.New("invalid memory file path")
	}
	info, err := os.Lstat(target)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", "", nil, errors.New("memory note target must be a regular file")
		}
	} else if !create || !errors.Is(err, os.ErrNotExist) {
		return "", "", nil, err
	}
	return resolvedDir, target, info, nil
}
