package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// pastedPathStart is where Claude Code cuts a pasted line into pieces: a space before something that starts a path.
	pastedPathStart = regexp.MustCompile(`^["']?(?:/|[A-Za-z]:\\)`)
	pastedImageExt  = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)$`)
)

// claudePaste is what Claude Code (2.1.316, its paste handler) makes of pasted text. Each line is cut at every space
// before a path; a line of several pieces that are not all paths stays as it is. Otherwise each piece that is a path
// with an image extension, to a file that exists, becomes an attached image and leaves the text; a blank line goes.
// A paste that attaches nothing is pasted as it was, blank lines included.
func claudePaste(text string) (kept string, attached []string) {
	notPath := func(p string) bool { return !pastedPathStart.MatchString(strings.TrimSpace(p)) }
	var out []string
	for _, line := range strings.Split(text, "\n") {
		var pieces []string
		start := 0
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' && pastedPathStart.MatchString(line[i+1:]) {
				if strings.TrimSpace(line[start:i]) != "" {
					pieces = append(pieces, line[start:i])
				}
				start = i + 1
			}
		}
		if strings.TrimSpace(line[start:]) != "" {
			pieces = append(pieces, line[start:])
		}
		if len(pieces) > 1 && slices.ContainsFunc(pieces, notPath) {
			out = append(out, line)
			continue
		}
		for _, p := range pieces {
			path := strings.Trim(strings.TrimSpace(p), `"'`)
			if _, err := os.Stat(path); err == nil && pastedImageExt.MatchString(path) {
				attached = append(attached, path)
				continue
			}
			out = append(out, p)
		}
	}
	if len(attached) == 0 {
		return text, nil
	}
	return strings.Join(out, "\n"), attached
}

// claudePasteBefore316 is what older Claude Code (2.1.296, its paste handler) makes of pasted text. Each line is cut
// at every space before a path, with no check of the other pieces. Each piece that ends in an image extension (quotes
// around it allowed) is a picture: one to a file that exists is attached, the others are dropped. The other pieces are
// pasted one per line. A paste that attaches nothing is pasted as it was.
func claudePasteBefore316(text string) (kept string, attached []string) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		var pieces []string
		start := 0
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' && (strings.HasPrefix(line[i+1:], "/") || regexp.MustCompile(`^[A-Za-z]:\\`).MatchString(line[i+1:])) {
				pieces = append(pieces, line[start:i])
				start = i + 1
			}
		}
		pieces = append(pieces, line[start:])
		for _, p := range pieces {
			if strings.TrimSpace(p) == "" {
				continue
			}
			path := strings.TrimSpace(p)
			if len(path) > 1 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
				path = path[1 : len(path)-1]
			}
			if !pastedImageExt.MatchString(path) {
				out = append(out, p)
			} else if _, err := os.Stat(path); err == nil {
				attached = append(attached, path)
			}
		}
	}
	if len(attached) == 0 {
		return text, nil
	}
	return strings.Join(out, "\n"), attached
}

// claudePastes are the Claude Code paste rules a message to Claude has to come through: each picture attached once,
// and its path kept as text where the version keeps it.
var claudePastes = []struct {
	version   string
	paste     func(string) (string, []string)
	keepsPath bool
}{
	{"2.1.316", claudePaste, true},
	{"2.1.296", claudePasteBefore316, false},
}

// appPhotos is the photos the apps show for a message: the lines of its <attached_images> block that start with "/" or
// "~" (iOS UserBubble.imagePaths, Android imagePaths, web parseUserText).
func appPhotos(text string) (photos []string) {
	for _, l := range strings.Split(attachedNote.FindString(text), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") || strings.HasPrefix(l, "~") {
			photos = append(photos, l)
		}
	}
	return photos
}

// sendToClaude sends text (and images) to an idle Claude through session.send and returns what was typed into it. The
// transcript then records the message as Claude does: the pictures it attached, then its text.
func sendToClaude(t *testing.T, text string, images []map[string]any) string {
	t.Helper()
	n, f, home, cwd := guideNode(t, true)
	guidePane(t, f, home, cwd, func(int) string { return "" })
	var mu sync.Mutex
	typed := ""
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		pasted := str(c.Params["text"])
		mu.Lock()
		typed = pasted
		mu.Unlock()
		kept, attached := claudePaste(pasted)
		var content []any
		chips := ""
		for i := range attached {
			content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}})
			chips += "[Image #" + strconv.Itoa(i+1) + "]"
		}
		content = append(content, map[string]any{"type": "text", "text": chips + kept})
		line, _ := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": cwd,
			"message": map[string]any{"role": "user", "content": content}})
		appendTranscript(t, home, cwd, sidA, string(line))
	})
	params := map[string]any{"session_id": sidA, "text": text}
	if images != nil {
		params["images"] = images
	}
	if _, err := request(t, n, "session.send", params); err != nil {
		t.Fatalf("send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return typed
}

// Claude Code turns each bare image path in <attached_images> into an `[Image #n]` and drops the line (#32). Each
// picture is attached once, its path reaches the agent as text, and the apps show each photo once.
func TestSendKeepsTheImagePathForClaude(t *testing.T) {
	png := map[string]any{"mime": "image/png", "data": "iVBORw0KGgo="}
	jpg := map[string]any{"mime": "image/jpeg", "data": "/9j/4AAQ"}
	typed := sendToClaude(t, "  copy this into the docs ", []map[string]any{png, jpg})

	photos := appPhotos(typed)
	if len(photos) != 2 || !strings.HasPrefix(typed, "copy this into the docs\n\n<attached_images>\n") {
		t.Fatalf("typed %q: want the text, then a block with the two saved photos", typed)
	}
	for _, c := range claudePastes {
		kept, attached := c.paste(typed)
		if !slices.Equal(attached, photos) {
			t.Errorf("Claude Code %s attaches %v, want each photo once: %v", c.version, attached, photos)
		}
		for _, p := range photos {
			if c.keepsPath && !strings.Contains(kept, p) {
				t.Errorf("on Claude Code %s the agent does not get the path %s; its text is %q", c.version, p, kept)
			}
		}
		// What Claude's transcript records (the bare lines gone) gives the apps no photo besides the attached pictures.
		if got := appPhotos(kept); len(got) != 0 {
			t.Errorf("on Claude Code %s the apps show %v from the transcript's text as well as the pictures", c.version, got)
		}
	}
}

// The line nodes wrote before #4361, "path: <path>", is cut at its space by older Claude Code and attaches the
// picture a second time; the line written now is not.
func TestOlderClaudeCodeAttachesEachPictureOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(p, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, attached := claudePasteBefore316("look\n\n<attached_images>\n" + p + "\npath: " + p + "\n</attached_images>"); len(attached) != 2 {
		t.Fatalf("the paste model no longer shows the bug: attached %v", attached)
	}
	if _, attached := claudePasteBefore316(keepImagePaths("look\n\n<attached_images>\n" + p + "\n</attached_images>")); !slices.Equal(attached, []string{p}) {
		t.Fatalf("Claude Code 2.1.296 attaches %v, want the picture once", attached)
	}
}

// Android's composer uploads the photos to the computer first and writes their paths in the block itself: those paths
// reach the agent too.
func TestSendKeepsPathsOfPhotosAClientUploaded(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.jpg")
	if err := os.WriteFile(p, []byte{0xff, 0xd8, 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	typed := sendToClaude(t, "the button is off\n\n<attached_images>\n"+p+"\n</attached_images>", nil)
	for _, c := range claudePastes {
		kept, attached := c.paste(typed)
		if !slices.Equal(attached, []string{p}) || c.keepsPath && !strings.Contains(kept, p) {
			t.Fatalf("Claude Code %s attaches %v and keeps %q: want the photo once and its path as text", c.version, attached, kept)
		}
	}
	if got := appPhotos(typed); !slices.Equal(got, []string{p}) {
		t.Errorf("the apps show %v, want the photo once", got)
	}
}

func TestKeepImagePathsLeavesOtherTextAlone(t *testing.T) {
	block := "look\n\n<attached_images>\n/u/a.jpg\n/u/b.png\n</attached_images>"
	done := "look\n\n<attached_images>\n/u/a.jpg\npath:/u/a.jpg\n/u/b.png\npath:/u/b.png\n</attached_images>"
	older := "look\n\n<attached_images>\n/u/a.jpg\npath: /u/a.jpg\n</attached_images>" // as nodes before #4361 wrote it
	for _, c := range []struct{ name, in, want string }{
		{"a path per photo", block, done},
		{"already has them", done, done},
		{"already has them, the older way", older, older},
		{"no block", "look at /u/a.jpg", "look at /u/a.jpg"},
		{"an empty block", "look\n<attached_images>\n</attached_images>", "look\n<attached_images>\n</attached_images>"},
		// a message that quotes the tag in prose is the user's words, not a list of photos
		{"prose in the tag", "the node writes <attached_images>\nhere\n</attached_images>", "the node writes <attached_images>\nhere\n</attached_images>"},
		{"block not at the end", "<attached_images>\n/u/a.jpg\n</attached_images>\nwhat is it?", "<attached_images>\n/u/a.jpg\n</attached_images>\nwhat is it?"},
	} {
		if got := keepImagePaths(c.in); got != c.want {
			t.Errorf("%s: keepImagePaths(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A message with words and a picture, its path: lines added, is more lines than Claude Code (2.1.316) shows in its
// box: the box shows the picture's chip and a `[Pasted text #n …]` placeholder. Left there after the grace, it is the
// message still waiting and gets one Enter; the same placeholder after a short message without pictures is someone
// else's paste and gets none.
func TestEnsureSubmittedKnowsAPictureMessageBehindItsPlaceholder(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	placeholder := strings.Replace(screenFixture(t, "claude-2.1.312-typed-162.ansi"), "fix the flaky test", "[Image #1][Pasted text #2 +3 lines]", 1)
	empty := screenFixture(t, "claude-2.1.312-empty-162.ansi")
	text := keepImagePaths("fix the flaky test\n\n<attached_images>\n/tmp/a.jpg\n</attached_images>")

	stuck := &fakePane{screens: []string{placeholder, empty}}
	(&Node{hosts: herdrHosts(stuck.serve(t))}).ensureSubmitted(context.Background(), "w1:p1", text, time.Now(), nil)
	if len(stuck.keys) != 1 || stuck.keys[0][0] != "enter" {
		t.Fatalf("a picture message behind its placeholder: keys %v", stuck.keys)
	}

	other := &fakePane{screens: []string{placeholder}}
	(&Node{hosts: herdrHosts(other.serve(t))}).ensureSubmitted(context.Background(), "w1:p1", "fix the flaky test", time.Now(), nil)
	if len(other.keys) != 0 {
		t.Fatalf("pressed enter on someone else's paste: %v", other.keys)
	}
}
