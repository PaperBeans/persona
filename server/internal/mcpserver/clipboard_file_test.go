package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The secret the tests copy: long enough to show its last 4 characters, and none of it may come back.
const testSecret = "sk-test-0123456789abcdefghijklmnopqrstuvWXYZ"

// clipboardHome makes a home directory for the call (HOME) and a directory outside it.
func clipboardHome(t *testing.T) (home, outside string) {
	t.Helper()
	root := t.TempDir()
	home, outside = filepath.Join(root, "home"), filepath.Join(root, "outside")
	for _, dir := range []string{home, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PERSONA_GUEST", "")
	return home, outside
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// phoneNode is a node whose phone sets the clipboard: it keeps the arguments each call sent and answers with
// answer (the apps' {"ok":true,…} when empty).
func phoneNode(t *testing.T, answer string) *[]json.RawMessage {
	t.Helper()
	var sent []json.RawMessage
	if answer == "" {
		answer = `{"content":[{"type":"text","text":"{\n  \"characters\" : 1,\n  \"ok\" : true\n}"}]}`
	}
	fakeNode(t, func(int) ([]byte, error) { return []byte(answer), nil })
	inner := dialNode
	dialNode = func(ctx context.Context, req any) ([]byte, error) {
		if m, ok := req.(map[string]any); ok && m["kind"] == "mcp.call" {
			sent = append(sent, m["args"].(json.RawMessage))
		}
		return inner(ctx, req)
	}
	return &sent
}

// clipboardCall calls phone_clipboard_set with args and returns the result's text and whether it is an error.
func clipboardCall(t *testing.T, args any) (string, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": clipboardSetTool, "arguments": args})
	res, rerr := handle(context.Background(), "test", request{JSONRPC: "2.0", ID: json.RawMessage(`7`), Method: "tools/call", Params: params})
	if rerr != nil {
		t.Fatalf("rpc error %+v", rerr)
	}
	m := res.(map[string]any)
	var text strings.Builder
	for _, c := range m["content"].([]map[string]any) {
		text.WriteString(fmt.Sprint(c["text"]))
	}
	return text.String(), m["isError"].(bool)
}

func sentText(t *testing.T, args json.RawMessage) string {
	t.Helper()
	var a map[string]any
	if err := json.Unmarshal(args, &a); err != nil {
		t.Fatal(err)
	}
	if _, ok := a["file"]; ok {
		t.Errorf("file went on to the node: %s", args)
	}
	if _, ok := a["trim"]; ok {
		t.Errorf("trim went on to the node: %s", args)
	}
	text, ok := a["text"].(string)
	if !ok {
		t.Fatalf("no text sent: %s", args)
	}
	return text
}

func TestClipboardFileSendsTheTextAndAnswersWithoutIt(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "key.txt"), []byte(testSecret+"\n"))
	sent := phoneNode(t, "")
	text, isErr := clipboardCall(t, map[string]any{"file": path})
	if isErr || len(*sent) != 1 {
		t.Fatalf("copy failed: %q (sent %d)", text, len(*sent))
	}
	if got := sentText(t, (*sent)[0]); got != testSecret {
		t.Fatalf("the phone got %q, want the file's text without its line break", got)
	}
	want := fmt.Sprintf("Copied the text of %s to the phone's clipboard: %d bytes, %d characters, ending in \"WXYZ\".", path, len(testSecret), len(testSecret))
	if text != want {
		t.Fatalf("result %q, want %q", text, want)
	}
	if strings.Contains(text, testSecret[:len(testSecret)-4]) {
		t.Fatal("the result holds the text")
	}
}

func TestClipboardFileTrimFalseKeepsTheLineBreaks(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "key.txt"), []byte("\n"+testSecret+"\r\n"))
	sent := phoneNode(t, "")
	if text, isErr := clipboardCall(t, map[string]any{"file": path, "trim": false}); isErr {
		t.Fatal(text)
	}
	if got := sentText(t, (*sent)[0]); got != "\n"+testSecret+"\r\n" {
		t.Fatalf("trim false: the phone got %q", got)
	}
}

// Text shorter than clipboardTailMin shows no characters at all; a multi-line text keeps its inner line breaks and
// tabs, a byte order mark is dropped, and multi-byte characters are counted as characters.
func TestClipboardFileShortAndMultiLineText(t *testing.T) {
	home, _ := clipboardHome(t)
	sent := phoneNode(t, "")
	short := writeFile(t, filepath.Join(home, "pin"), []byte("hunter2\n"))
	text, isErr := clipboardCall(t, map[string]any{"file": short})
	if isErr || strings.Contains(text, "ending") || strings.Contains(text, "ter2") ||
		!strings.HasSuffix(text, ": 7 bytes, 7 characters.") {
		t.Fatalf("short text: %q", text)
	}
	pem := "\uFEFF-----BEGIN KEY-----\n\tAAAA密钥BBBB\n-----END KEY-----\n"
	long := writeFile(t, filepath.Join(home, "key.pem"), []byte(pem))
	text, isErr = clipboardCall(t, map[string]any{"file": long, "trim": true})
	wantText := "-----BEGIN KEY-----\n\tAAAA密钥BBBB\n-----END KEY-----"
	if isErr || sentText(t, (*sent)[1]) != wantText {
		t.Fatalf("pem: %q, sent %s", text, (*sent)[1])
	}
	if !strings.Contains(text, fmt.Sprintf("%d bytes, %d characters", len(wantText), len([]rune(wantText)))) {
		t.Fatalf("pem counts: %q", text)
	}
}

// A symlink inside the home directory to a file inside it is followed.
func TestClipboardFileFollowsASymlinkInsideHome(t *testing.T) {
	home, _ := clipboardHome(t)
	if err := os.Mkdir(filepath.Join(home, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := writeFile(t, filepath.Join(home, "secrets", "key"), []byte(testSecret))
	link := filepath.Join(home, "key-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	sent := phoneNode(t, "")
	if text, isErr := clipboardCall(t, map[string]any{"file": link}); isErr || sentText(t, (*sent)[0]) != testSecret {
		t.Fatalf("symlink inside home: %q", text)
	}
}

// Every refusal: the node is never called, the result is an error naming the rule, and it never holds the text.
func TestClipboardFileRefusals(t *testing.T) {
	home, outside := clipboardHome(t)
	good := writeFile(t, filepath.Join(home, "key.txt"), []byte(testSecret))
	outsideFile := writeFile(t, filepath.Join(outside, "key.txt"), []byte(testSecret))
	escape := filepath.Join(home, "escape")
	if err := os.Symlink(outsideFile, escape); err != nil {
		t.Fatal(err)
	}
	escapeDir := filepath.Join(home, "linked-dir")
	if err := os.Symlink(outside, escapeDir); err != nil {
		t.Fatal(err)
	}
	big := writeFile(t, filepath.Join(home, "big.txt"), []byte(strings.Repeat("a", clipboardFileMax+1)))
	binary := writeFile(t, filepath.Join(home, "image.png"), []byte("\x89PNG\r\n\x1a\n\xff\xfe"+testSecret))
	nul := writeFile(t, filepath.Join(home, "nul.txt"), []byte(testSecret+"\x00"))
	escSeq := writeFile(t, filepath.Join(home, "esc.txt"), []byte("\x1b[31m"+testSecret))
	empty := writeFile(t, filepath.Join(home, "empty.txt"), nil)
	breaks := writeFile(t, filepath.Join(home, "breaks.txt"), []byte("\r\n\n"))
	dir := filepath.Join(home, "folder")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args any
		want string
	}{
		{"missing", map[string]any{"file": filepath.Join(home, "nope.txt")}, "there is no file at"},
		{"relative", map[string]any{"file": "key.txt"}, "must be an absolute path"},
		{"tilde", map[string]any{"file": "~/key.txt"}, "~ is not expanded"},
		{"outside home", map[string]any{"file": outsideFile}, "is outside your home directory"},
		{"symlink escaping home", map[string]any{"file": escape}, "leads to"},
		{"symlinked folder escaping home", map[string]any{"file": filepath.Join(escapeDir, "key.txt")}, "outside your home directory"},
		{"dot-dot out of home", map[string]any{"file": filepath.Join(home, "..", "outside", "key.txt")}, "outside your home directory"},
		{"home itself", map[string]any{"file": home}, "outside your home directory"},
		{"directory", map[string]any{"file": dir}, "is a directory"},
		{"too big", map[string]any{"file": big}, "at most 64 KiB"},
		{"binary", map[string]any{"file": binary}, "is not UTF-8 text"},
		{"NUL", map[string]any{"file": nul}, "control characters"},
		{"escape sequence", map[string]any{"file": escSeq}, "control characters"},
		{"empty", map[string]any{"file": empty}, "is empty"},
		{"only line breaks", map[string]any{"file": breaks}, "nothing but line breaks"},
		{"text and file", map[string]any{"file": good, "text": "x"}, "either text or file"},
		{"trim without file", map[string]any{"text": "x", "trim": true}, "trim goes with file"},
		{"file not a string", map[string]any{"file": 42}, "must be the absolute path"},
		{"blank file", map[string]any{"file": "  "}, "must be the absolute path"},
		{"trim not a boolean", map[string]any{"file": good, "trim": "yes"}, "trim must be true or false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sent := phoneNode(t, "")
			text, isErr := clipboardCall(t, c.args)
			if !isErr || !strings.Contains(text, c.want) {
				t.Fatalf("got %q (error %v), want an error with %q", text, isErr, c.want)
			}
			if len(*sent) != 0 {
				t.Fatalf("refused, but sent %s", (*sent)[0])
			}
			if strings.Contains(text, testSecret[4:20]) {
				t.Fatalf("the refusal holds the file's text: %q", text)
			}
		})
	}
}

// Exactly the limit is still copied.
func TestClipboardFileAtTheLimit(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "max.txt"), []byte(strings.Repeat("b", clipboardFileMax)))
	sent := phoneNode(t, "")
	if text, isErr := clipboardCall(t, map[string]any{"file": path}); isErr || len(sentText(t, (*sent)[0])) != clipboardFileMax {
		t.Fatalf("%d bytes: %q", clipboardFileMax, text)
	}
}

// A guest conversation's server neither offers file nor reads one.
func TestClipboardFileNotForGuests(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "key.txt"), []byte(testSecret))
	t.Setenv("PERSONA_GUEST", "1")
	sent := phoneNode(t, "")
	if text, isErr := clipboardCall(t, map[string]any{"file": path}); !isErr || len(*sent) != 0 {
		t.Fatalf("guest: %q, sent %d", text, len(*sent))
	}
	fakeNode(t, func(int) ([]byte, error) { return []byte(phoneToolsLine), nil })
	if schema := clipboardSchema(t); strings.Contains(schema, `"file"`) {
		t.Fatalf("guest sees file: %s", schema)
	}
}

// A call without file goes to the node exactly as it came; one with "file": null or "text": "" is a text call or a
// file call as the other argument says.
func TestClipboardTextCallsAreUnchanged(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "key.txt"), []byte(testSecret))
	sent := phoneNode(t, "")
	params := json.RawMessage(`{"name":"phone_clipboard_set","arguments":{"text":"a link",  "extra":1}}`)
	if _, rerr := handle(context.Background(), "test", request{JSONRPC: "2.0", ID: json.RawMessage(`8`), Method: "tools/call", Params: params}); rerr != nil {
		t.Fatal(rerr)
	}
	if string((*sent)[0]) != `{"text":"a link",  "extra":1}` {
		t.Fatalf("a text call was rewritten: %s", (*sent)[0])
	}
	if text, isErr := clipboardCall(t, map[string]any{"text": "a link", "file": nil, "trim": nil}); isErr || string((*sent)[1]) != `{"text":"a link"}` {
		t.Fatalf("file null: %q, sent %s", text, (*sent)[1])
	}
	if text, isErr := clipboardCall(t, map[string]any{"text": "", "file": path}); isErr || sentText(t, (*sent)[2]) != testSecret {
		t.Fatalf("text \"\" and file: %q", text)
	}
	if text, isErr := clipboardCall(t, map[string]any{"text": nil, "file": path}); isErr || sentText(t, (*sent)[3]) != testSecret {
		t.Fatalf("text null and file: %q", text)
	}
}

// The phone's answer reaches the model as it is unless it quotes the text: a refusal (Android in the background)
// is passed on, an answer quoting the text is replaced, an unexpected success is passed on.
func TestClipboardFilePhoneAnswers(t *testing.T) {
	home, _ := clipboardHome(t)
	path := writeFile(t, filepath.Join(home, "key.txt"), []byte(testSecret))
	quoted, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": "cannot set " + testSecret}}, "is_error": true})
	quotedOK, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": `{"set":"` + testSecret + `"}`}}})
	for _, c := range []struct {
		name, answer, want string
		isErr              bool
	}{
		{"refused", `{"content":[{"type":"text","text":"Android only allows this while Persona is open"}],"is_error":true}`, "Android only allows", true},
		{"error quoting the text", string(quoted), "did not take the text", true},
		{"answer quoting the text", string(quotedOK), "quoted the copied text", false},
		{"no ok", `{"content":[{"type":"text","text":"{\"ok\":false}"}]}`, `{"ok":false}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			phoneNode(t, c.answer)
			text, isErr := clipboardCall(t, map[string]any{"file": path})
			if isErr != c.isErr || !strings.Contains(text, c.want) || strings.Contains(text, testSecret) {
				t.Fatalf("got %q (error %v)", text, isErr)
			}
		})
	}
}

const phoneToolsLine = `{"tools":[` +
	`{"name":"phone_clipboard_set","description":"Replace the phone's clipboard with the given text.","input_schema":{"type":"object","properties":{"text":{"type":"string","description":"Text to place on the clipboard."}},"required":["text"]}},` +
	`{"name":"phone_clipboard_get","description":"Read the clipboard.","input_schema":{"type":"object","properties":{}}}]}`

// clipboardSchema is phone_clipboard_set's input schema and description as tools/list gives them.
func clipboardSchema(t *testing.T) string {
	t.Helper()
	res, rerr := handle(context.Background(), "test", listRequest())
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, tool := range res.(map[string]any)["tools"].([]map[string]any) {
		if tool["name"] == clipboardSetTool {
			b, _ := json.Marshal(tool)
			return string(b)
		}
	}
	t.Fatal("no phone_clipboard_set")
	return ""
}

func TestClipboardSetListsFile(t *testing.T) {
	clipboardHome(t)
	fakeNode(t, func(int) ([]byte, error) { return []byte(phoneToolsLine), nil })
	var tool struct {
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
				Default     any    `json:"default"`
			} `json:"properties"`
			Required []string `json:"required"`
		} `json:"inputSchema"`
	}
	if err := json.Unmarshal([]byte(clipboardSchema(t)), &tool); err != nil {
		t.Fatal(err)
	}
	props := tool.InputSchema.Properties
	if props["text"].Type != "string" || props["file"].Type != "string" || props["trim"].Type != "boolean" || props["trim"].Default != true {
		t.Fatalf("properties %+v", props)
	}
	if len(tool.InputSchema.Required) != 0 {
		t.Fatalf("still required: %v", tool.InputSchema.Required)
	}
	if !strings.HasPrefix(tool.Description, "Replace the phone's clipboard with the given text.\n\n") ||
		!strings.Contains(tool.Description, "pass file instead of text") || !strings.Contains(tool.Description, "64 KiB") ||
		!strings.Contains(tool.Description, "at least 16 characters") {
		t.Fatalf("description %q", tool.Description)
	}
	// The other tools are as the node listed them.
	res, _ := handle(context.Background(), "test", listRequest())
	for _, tool := range res.(map[string]any)["tools"].([]map[string]any) {
		if tool["name"] == "phone_clipboard_get" && (tool["description"] != "Read the clipboard." || string(tool["inputSchema"].(json.RawMessage)) != `{"type":"object","properties":{}}`) {
			t.Fatalf("phone_clipboard_get changed: %+v", tool)
		}
	}
}

// A schema the tool's phone sent in a shape it does not know is left alone (and file is still read on a call).
func TestWithClipboardFileLeavesOddSchemas(t *testing.T) {
	for _, schema := range []string{`[]`, `"x"`, `{"type":"object","properties":[]}`} {
		got, desc := withClipboardFile(json.RawMessage(schema), "d")
		if string(got) != schema || desc != "d" {
			t.Errorf("%s became %s / %q", schema, got, desc)
		}
	}
	got, _ := withClipboardFile(json.RawMessage(`{"type":"object"}`), "d")
	if !strings.Contains(string(got), `"file"`) {
		t.Errorf("a schema without properties gets file: %s", got)
	}
}
