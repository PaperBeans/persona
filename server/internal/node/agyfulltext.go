package node

import (
	"bytes"
	"crypto/sha256"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// Keep at most two decoded exports between reads, with a size limit per entry.
const agyFullCacheFileLimit = 128 << 20

type agyFullExport struct {
	file  os.FileInfo
	hash  [32]byte
	steps []agyStep
}

var agyTruncation = lazyre.New(`\n<truncated (\d+) bytes>\n`)

func (s *agyStep) truncatedText(field string) *string {
	switch field {
	case "content":
		return &s.Content
	case "thinking":
		return &s.Thinking
	case "error":
		return &s.Error
	default:
		return nil
	}
}

func agyNeedsFullText(step agyStep) bool {
	switch step.Type {
	case "PLANNER_RESPONSE", "GENERIC", "ERROR_MESSAGE":
	default:
		return false
	}
	for _, field := range step.TruncatedFields {
		if text := step.truncatedText(field); text != nil && agyTruncation.MatchString(*text) {
			return true
		}
	}
	return false
}

func agyMatchingFullText(preview, full string) bool {
	for _, m := range agyTruncation.FindAllStringSubmatchIndex(preview, -1) {
		omitted, err := strconv.Atoi(preview[m[2]:m[3]])
		prefix, suffix := preview[:m[0]], preview[m[1]:]
		if err == nil && omitted > 0 && len(full)-len(prefix)-len(suffix) == omitted &&
			strings.HasPrefix(full, prefix) && strings.HasSuffix(full, suffix) {
			return true
		}
	}
	return false
}

func (n *Node) agyFullSteps(root *os.Root, key, conversation string, file os.FileInfo, unsettled bool) ([]agyStep, error) {
	var cached agyFullExport
	n.mu.Lock()
	if n.agyFullCache != nil {
		cached, _ = n.agyFullCache.get(key)
	}
	n.mu.Unlock()
	sameSnapshot := sameAgyFile(file, cached.file)
	if sameSnapshot && !unsettled {
		return cached.steps, nil
	}
	raw, err := agyReadFile(root, agyFullTranscriptPath)
	if err != nil {
		return nil, err
	}
	// Rapid same-size rewrites can share a timestamp. Check recent bytes before
	// reusing a parse, and ignore incomplete records at the writer's tail.
	fileSize := len(raw)
	raw = raw[:bytes.LastIndexByte(raw, '\n')+1]
	if sameSnapshot && sha256.Sum256(raw) == cached.hash {
		return cached.steps, nil
	}
	steps, rev := parseAgySteps(raw, conversation)
	if fileSize <= agyFullCacheFileLimit {
		latest, err := root.Lstat(agyFullTranscriptPath)
		if err == nil && sameAgyFile(file, latest) {
			n.mu.Lock()
			if n.agyFullCache == nil {
				n.agyFullCache = newLRU[string, agyFullExport](2)
			}
			n.agyFullCache.put(key, agyFullExport{file: latest, hash: rev.hash, steps: steps})
			n.mu.Unlock()
		}
	}
	return steps, nil
}

// The compact export owns the sequence, identities, and branch. The full export
// can lag behind a rewind, so verify the record identity, both retained ends,
// and the omitted byte count before replacing text.
func restoreAgyFullText(steps, full []agyStep, memos []agyStepMemo) bool {
	restored := false
	for i, j := 0, 0; i < len(steps) && j < len(full); i++ {
		step := &steps[i]
		if !agyNeedsFullText(*step) {
			continue
		}
		for j < len(full) && full[j].seq < step.seq {
			j++
		}
		if j == len(full) {
			break
		}
		record := &full[j]
		if step.seq != record.seq || step.CreatedAt == "" || step.CreatedAt != record.CreatedAt ||
			step.Type != record.Type || step.Source != record.Source || step.Status != record.Status {
			continue
		}
		count := len(step.TruncatedFields)
		step.TruncatedFields = slices.DeleteFunc(step.TruncatedFields, func(field string) bool {
			preview, text := step.truncatedText(field), record.truncatedText(field)
			if preview == nil || slices.Contains(record.TruncatedFields, field) || !agyMatchingFullText(*preview, *text) {
				return false
			}
			*preview = *text
			return true
		})
		if len(step.TruncatedFields) < count {
			restored = true
			step.rendered = step.renderedHash()
			memos[i].rendered = step.rendered
		}
	}
	return restored
}
