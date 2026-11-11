package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// phone_clipboard_set's file argument (#3947, docs/apple/phone-tools.md "Copying a file to the clipboard"): this
// server reads a text file on the machine the agent runs on and sends its text to the phone as the call's text, so
// a secret (an API key, a token, a password) reaches the phone's clipboard without the model reading it. The model
// sends only the path and gets back the size and at most the last 4 characters. The phone apps get an ordinary
// {text} call; nothing on the way (node, manager, hub) logs a call's arguments.
//
// The file is read here, in the process Claude Code starts for the agent, and nowhere else. This process runs as the
// agent's user and inside the agent's sandbox when it has one (docs/server/agent-sandbox.md), so it can read only
// what the agent could read itself. The node is not sandboxed: a sandboxed agent could have it copy hub.json to the
// clipboard and read it back with phone_clipboard_get. The hub may run in a container, or on another machine than
// the agent.
//
// The checks keep the argument to what it is for: the user's own text files in their home directory. They are not
// a wall against the agent, which runs as the same user (and in the same sandbox); so a directory swapped for a
// symlink between the check and the open would give it nothing it could not read anyway. The last part of the path
// is opened without following a symlink, and the open file itself is checked.

const (
	clipboardSetTool = "phone_clipboard_set"
	// clipboardFileMax is the largest file: ample for keys, tokens and certificates, and it refuses a log or an
	// image picked by mistake.
	clipboardFileMax = 64 << 10
	// clipboardTailMin is the shortest text whose last 4 characters the result shows: in a shorter one they would
	// give away too much of it.
	clipboardTailMin = 16
	clipboardTailLen = 4
)

// clipboardCopy is what a phone_clipboard_set with file sent: the path the model gave and the text read from it.
type clipboardCopy struct {
	path, text string
}

// clipboardFileArgs reads the file of a phone_clipboard_set call that has one and returns the arguments to send
// instead (its text as text, without file and trim) and what was copied. A call without file is returned as it is,
// with a nil copy. The error is the refusal the model gets; it never holds any of the file's text.
func clipboardFileArgs(args json.RawMessage) (json.RawMessage, *clipboardCopy, error) {
	var a map[string]json.RawMessage
	if json.Unmarshal(args, &a) != nil || a == nil {
		return args, nil, nil // checkCallParams lets only objects through; the phone answers anything else
	}
	file, hasFile := a["file"]
	trimArg, hasTrim := a["trim"]
	if hasFile && isNull(file) {
		hasFile = false
	}
	if !hasFile {
		if hasTrim && !isNull(trimArg) {
			return nil, nil, errors.New("trim goes with file only: text is sent as it is")
		}
		if _, nullFile := a["file"]; !nullFile && !hasTrim {
			return args, nil, nil // an ordinary call goes as it came
		}
		delete(a, "file")
		delete(a, "trim")
		out, err := json.Marshal(a)
		return out, nil, err
	}
	if isGuest() {
		return nil, nil, errors.New("file is not available in this conversation: pass the text itself")
	}
	var path string
	if json.Unmarshal(file, &path) != nil || strings.TrimSpace(path) == "" {
		return nil, nil, errors.New("file must be the absolute path of a text file, as a string")
	}
	// Models fill optional arguments with null or "": text that says nothing does not compete with file.
	if text, ok := a["text"]; ok && !isNull(text) && string(text) != `""` {
		return nil, nil, errors.New("give either text or file, not both")
	}
	trim := true
	if hasTrim && !isNull(trimArg) && json.Unmarshal(trimArg, &trim) != nil {
		return nil, nil, errors.New("trim must be true or false")
	}
	text, err := readClipboardFile(path, trim)
	if err != nil {
		return nil, nil, err
	}
	delete(a, "file")
	delete(a, "trim")
	a["text"], err = json.Marshal(text)
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(a)
	return out, &clipboardCopy{path: path, text: text}, err
}

func isNull(v json.RawMessage) bool { return strings.TrimSpace(string(v)) == "null" }

// readClipboardFile is the text of the file at path, after the checks above, without line breaks at both ends when
// trim is set. Its errors name the path and the rule, never the content.
func readClipboardFile(path string, trim bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("file must be an absolute path on this computer; %q is not (~ is not expanded)", path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot tell your home directory, so no file can be copied: %v", err)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("cannot resolve your home directory %s: %v", home, err)
	}
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("there is no file at %s", path)
	}
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %v", path, err)
	}
	if !insideDir(realHome, real) {
		// A path that looks inside home but leads out of it went through a symlink.
		if clean := filepath.Clean(path); insideDir(filepath.Clean(home), clean) || insideDir(realHome, clean) {
			return "", fmt.Errorf("%s leads to %s, outside your home directory %s: only files in your home directory can be copied", path, real, home)
		}
		return "", fmt.Errorf("%s is outside your home directory %s: only files in your home directory can be copied", path, home)
	}
	// Said before opening it: a socket or a device cannot be opened, and the open's error would not say why.
	if st, err := os.Lstat(real); err == nil && !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is %s, not a text file", path, fileKind(st.Mode()))
	}
	f, err := openClipboardFile(real)
	if err != nil {
		return "", fmt.Errorf("cannot open %s: %v", path, err)
	}
	defer f.Close()
	st, err := f.Stat() // the file opened, whatever happened to the path since
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %v", path, err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is %s, not a text file", path, fileKind(st.Mode()))
	}
	if !ownedByCaller(st) {
		return "", fmt.Errorf("%s belongs to another user: only your own files can be copied", path)
	}
	tooBig := func(size int64) error {
		return fmt.Errorf("%s is %d bytes; at most %d KiB can be copied to the clipboard", path, size, clipboardFileMax>>10)
	}
	if st.Size() > clipboardFileMax {
		return "", tooBig(st.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, clipboardFileMax+1)) // it may have grown since the stat
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %v", path, err)
	}
	if len(data) > clipboardFileMax {
		return "", tooBig(int64(len(data)))
	}
	text, err := clipboardText(data, trim)
	if err != nil {
		return "", fmt.Errorf("%s %v", path, err)
	}
	return text, nil
}

// clipboardText is data as clipboard text: UTF-8 without NUL or other control characters but tab and line breaks,
// a leading byte order mark dropped, and with trim no line breaks at both ends. The error completes a sentence
// that starts with the path.
func clipboardText(data []byte, trim bool) (string, error) {
	if !utf8.Valid(data) {
		return "", errors.New("is not UTF-8 text")
	}
	text := strings.TrimPrefix(string(data), "\uFEFF")
	if strings.ContainsFunc(text, func(r rune) bool { return r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f }) {
		return "", errors.New("is not plain text: it has NUL or other control characters")
	}
	if trim {
		text = strings.Trim(text, "\r\n")
	}
	if text == "" {
		if trim && len(data) > 0 {
			return "", errors.New("holds nothing but line breaks")
		}
		return "", errors.New("is empty")
	}
	return text, nil
}

// fileKind names what a file that is not a regular one is.
func fileKind(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	case mode&fs.ModeSymlink != 0:
		return "a symlink"
	}
	return "a special file"
}

// insideDir says whether path is inside dir (both real paths).
func insideDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// answer is the phone's result as the model gets it: a success becomes the size and the last characters, and an
// answer that quotes the text (no phone does today) is replaced, so the file's text never comes back.
func (c *clipboardCopy) answer(res proto.ToolResult) proto.ToolResult {
	if !res.IsError && phoneSaidOK(res) {
		return proto.TextResult(c.summary(), false)
	}
	for _, part := range res.Content {
		if strings.Contains(part.Text, c.text) || strings.Contains(part.Data, c.text) {
			text := "The phone answered, but its answer quoted the copied text, so it is not shown here."
			if res.IsError {
				text = "The phone did not take the text; its answer quoted the text, so it is not shown here."
			}
			return proto.TextResult(text, res.IsError)
		}
	}
	return res
}

// phoneSaidOK: the phone answered {"ok": true, …}, as the iOS and Android apps do once the clipboard is set.
func phoneSaidOK(res proto.ToolResult) bool {
	for _, part := range res.Content {
		var v struct {
			OK bool `json:"ok"`
		}
		if part.Type == "text" && json.Unmarshal([]byte(part.Text), &v) == nil && v.OK {
			return true
		}
	}
	return false
}

// summary confirms the copy without the text: its size, and its last characters when it is long enough.
func (c *clipboardCopy) summary() string {
	runes := []rune(c.text)
	tail := "."
	if len(runes) >= clipboardTailMin {
		tail = fmt.Sprintf(", ending in %q.", string(runes[len(runes)-clipboardTailLen:]))
	}
	return fmt.Sprintf("Copied the text of %s to the phone's clipboard: %d bytes, %d characters%s", c.path, len(c.text), len(runes), tail)
}

// withClipboardFile is phone_clipboard_set's schema with file and trim, text no longer required, and its
// description saying when to use file. A schema that is not an object is left as it is.
func withClipboardFile(schema json.RawMessage, description string) (json.RawMessage, string) {
	var s map[string]json.RawMessage
	if json.Unmarshal(schema, &s) != nil || s == nil {
		return schema, description
	}
	var properties map[string]json.RawMessage
	if raw, ok := s["properties"]; ok && json.Unmarshal(raw, &properties) != nil {
		return schema, description
	}
	if properties == nil {
		properties = map[string]json.RawMessage{}
	}
	properties["file"], _ = json.Marshal(map[string]any{"type": "string", "description": prompts.RenderPhoneClipboardFileOption()})
	properties["trim"], _ = json.Marshal(map[string]any{"type": "boolean", "default": true,
		"description": prompts.RenderPhoneClipboardTrimOption()})
	s["properties"], _ = json.Marshal(properties)
	var required []string
	if raw, ok := s["required"]; ok && json.Unmarshal(raw, &required) == nil {
		required = slices.DeleteFunc(required, func(name string) bool { return name == "text" })
		if len(required) == 0 {
			delete(s, "required")
		} else {
			s["required"], _ = json.Marshal(required)
		}
	}
	out, err := json.Marshal(s)
	if err != nil {
		return schema, description
	}
	return out, prompts.RenderPhoneClipboardFile(prompts.PhoneClipboardFile{Description: description,
		MaxKiB: clipboardFileMax >> 10, TailMin: clipboardTailMin})
}
