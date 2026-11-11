package tmux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTmux writes a tmux stand-in running body as a shell script and returns its path and the file it logs calls to.
func fakeTmux(t *testing.T, body string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nCALLS=" + calls + "\nDIR=" + dir + "\necho \"$@\" >> \"$CALLS\"\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func readCalls(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// bufferOf is the -b argument of a logged call.
func bufferOf(call string) string {
	f := strings.Fields(call)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "-b" {
			return f[i+1]
		}
	}
	return ""
}

// A paste that fails (the pane closed after the load) must not leave the typed text in the user's tmux server.
func TestTmuxSendTextDeletesTheBufferWhenThePasteFails(t *testing.T) {
	bin, calls := fakeTmux(t, `case "$1" in
load-buffer) cat > /dev/null ;;
paste-buffer) echo "can't find pane: %9" >&2; exit 1 ;;
esac
`)
	err := Client{Bin: bin}.SendText(context.Background(), "%9", "my secret answer")
	if err == nil || !strings.Contains(err.Error(), "paste-buffer") {
		t.Fatalf("SendText = %v, want the paste error", err)
	}
	log := readCalls(t, calls)
	if len(log) != 3 || !strings.HasPrefix(log[0], "load-buffer") || !strings.HasPrefix(log[1], "paste-buffer") ||
		!strings.HasPrefix(log[2], "delete-buffer") {
		t.Fatalf("calls = %q, want load-buffer, paste-buffer, delete-buffer", log)
	}
	if name := bufferOf(log[0]); name == "" || bufferOf(log[2]) != name {
		t.Fatalf("delete-buffer %q does not name the loaded buffer %q", bufferOf(log[2]), name)
	}
}

// A paste the context ends still gets the buffer deleted: the cleanup has a context of its own.
func TestTmuxSendTextDeletesTheBufferAfterTheContextEnds(t *testing.T) {
	bin, calls := fakeTmux(t, `case "$1" in
load-buffer) cat > /dev/null ;;
paste-buffer) exec sleep 30 ;;
esac
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (Client{Bin: bin}).SendText(ctx, "%1", "hello"); err == nil {
		t.Fatal("SendText with a paste outliving its context = nil error")
	}
	log := readCalls(t, calls)
	if len(log) != 3 || !strings.HasPrefix(log[2], "delete-buffer") || bufferOf(log[2]) != bufferOf(log[0]) {
		t.Fatalf("calls = %q, want the loaded buffer deleted after the context ended", log)
	}
}

// A load the client gives up on (ctx ended, or a tmux wrapper that hangs after the server took the text) may still have
// left the buffer on the server: it is deleted too, and nothing is pasted.
func TestTmuxSendTextDeletesTheBufferWhenTheLoadFails(t *testing.T) {
	for _, c := range []struct{ name, load string }{
		{"load outlives its context", "cat > /dev/null; exec sleep 30"},
		{"load fails", "cat > /dev/null; echo 'server exited unexpectedly' >&2; exit 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, calls := fakeTmux(t, "case \"$1\" in\nload-buffer) "+c.load+" ;;\nesac\n")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := (Client{Bin: bin}).SendText(ctx, "%1", "my secret answer"); err == nil {
				t.Fatal("SendText with a failed load = nil error")
			}
			log := readCalls(t, calls)
			if len(log) != 2 || !strings.HasPrefix(log[0], "load-buffer") || !strings.HasPrefix(log[1], "delete-buffer") ||
				bufferOf(log[0]) == "" || bufferOf(log[1]) != bufferOf(log[0]) {
				t.Fatalf("calls = %q, want load-buffer then delete-buffer of the same buffer", log)
			}
		})
	}
}

// Two Persona processes on one tmux server must not share buffer names: one's load would overwrite the other's text.
func TestTmuxSendTextBufferNamesAreUniqueAndCarryThePid(t *testing.T) {
	bin, calls := fakeTmux(t, `[ "$1" = load-buffer ] && cat > /dev/null
exit 0
`)
	c := Client{Bin: bin}
	for range 2 {
		if err := c.SendText(context.Background(), "%1", "hello"); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, call := range readCalls(t, calls) {
		if strings.HasPrefix(call, "load-buffer") {
			names = append(names, bufferOf(call))
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("buffer names = %q, want two distinct ones", names)
	}
	pid := "-" + strconv.Itoa(os.Getpid()) + "-"
	for _, n := range names {
		if !strings.HasPrefix(n, "persona-") || !strings.Contains(n, pid) {
			t.Fatalf("buffer name %q does not carry the pid %d", n, os.Getpid())
		}
	}
}

// Empty text types nothing: no tmux runs at all.
func TestTmuxSendTextEmptyIsANoOp(t *testing.T) {
	bin, calls := fakeTmux(t, "exit 1\n")
	if err := (Client{Bin: bin}).SendText(context.Background(), "%1", ""); err != nil {
		t.Fatalf("SendText(\"\") = %v", err)
	}
	if log := readCalls(t, calls); len(log) != 0 {
		t.Fatalf("calls = %q, want none", log)
	}
}

// killHolder ends the background child a fake left holding the output pipe.
func killHolder(t *testing.T, pidFile string) {
	t.Cleanup(func() {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
	})
}

// A tmux wrapper whose child keeps stdout open must not hold Capture past its context.
func TestTmuxCaptureIsBoundedWhenAChildHoldsStdout(t *testing.T) {
	bin, _ := fakeTmux(t, `sleep 30 &
echo $! > "$DIR/holder"
wait
`)
	killHolder(t, filepath.Join(filepath.Dir(bin), "holder"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Client{Bin: bin}.Capture(ctx, "%1", "", 0, false)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Capture took %v, want it bounded by the context plus the wait delay", elapsed)
	}
	if err == nil {
		t.Fatal("Capture after the context ended = nil error")
	}
}

// A tmux that exits 0 while a child it left behind still holds stdout succeeded: its output is the answer.
func TestTmuxCaptureSucceedsWhenAChildOutlivesTheExit(t *testing.T) {
	bin, _ := fakeTmux(t, `echo 'the screen'
sleep 30 &
echo $! > "$DIR/holder"
`)
	killHolder(t, filepath.Join(filepath.Dir(bin), "holder"))
	start := time.Now()
	out, err := Client{Bin: bin}.Capture(context.Background(), "%1", "", 0, false)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Capture took %v, want it bounded by the wait delay", elapsed)
	}
	if err != nil || out != "the screen\n" {
		t.Fatalf("Capture = %q, %v, want the screen and no error", out, err)
	}
}

// A node started inside a pane of another server (tmux -L work) carries that server in its own TMUX, and tmux without
// -S runs against the server TMUX names. A pane on the default server must still be reached on the default server, so
// the node's TMUX is not passed on; a pane on a named server is reached through -S.
func TestTmuxDefaultServerIgnoresTheNodesOwnTMUX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	t.Setenv("TMUX", "/tmp/other/work,1,0")
	t.Setenv("TMUX_PANE", "%7")
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
echo "TMUX=$TMUX TMUX_PANE=$TMUX_PANE args=$*" >> ` + calls + `
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := (Client{Bin: bin}).Capture(ctx, "%11", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := (Client{Bin: bin, Socket: "/s"}).Capture(ctx, "%11", "", 0, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("calls = %q, want 2", b)
	}
	if want := "TMUX= TMUX_PANE= args=capture-pane -p -t %11"; lines[0] != want {
		t.Errorf("default server call = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "args=-S /s capture-pane -p -t %11") {
		t.Errorf("named server call = %q, want -S /s", lines[1])
	}
}

// captureTmux is a tmux that records each argv as one line of calls and, for capture-pane, prints what a pane with 100
// rows of history and a 50-row screen gives: the history ("h1".."h100") only when -S asks for it, then the screen
// ("s1".."s50"), each row ended in \n as tmux ends them.
func captureTmux(t *testing.T) (Client, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$1" in
capture-pane)
	case " $* " in *" -S "*) i=1; while [ $i -le 100 ]; do echo "h$i"; i=$((i+1)); done ;; esac
	i=1; while [ $i -le 50 ]; do echo "s$i"; i=$((i+1)); done ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: bin}, calls
}

func lastCall(t *testing.T, calls string) string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-1]
}

// rows is "<prefix><from>".."<prefix><to>", each ended in end.
func rows(prefix string, from, to int, end string) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%s%d%s", prefix, i, end)
	}
	return b.String()
}

func TestCaptureHonorsSourceAndLines(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, source string
		lines        int
		ansi         bool
		argv, want   string
	}{
		// The screen only, never the scrollback, cut to its last rows (a dialog read: 40 rows).
		{"visible", "visible", 40, false, "capture-pane -p -t %3", rows("s", 11, 50, "\n")},
		{"default source is the screen", "", 40, false, "capture-pane -p -t %3", rows("s", 11, 50, "\n")},
		{"visible whole", "visible", 0, false, "capture-pane -p -t %3", rows("s", 1, 50, "\n")},
		// tmux gives 60 rows of history and the whole screen: at most 60 come back, the newest.
		{"recent", "recent", 60, false, "capture-pane -p -t %3 -S -60", rows("h", 91, 100, "\n") + rows("s", 1, 50, "\n")},
		{"recent-unwrapped joins wrapped lines", "recent-unwrapped", 30, false, "capture-pane -p -t %3 -S -30 -J", rows("s", 21, 50, "\n")},
		// Colored rows end in \r\n, as the host contract says.
		{"ansi", "visible", 5, true, "capture-pane -p -t %3 -e", rows("s", 46, 50, "\r\n")},
	} {
		t.Run(c.name, func(t *testing.T) {
			client, calls := captureTmux(t)
			got, err := client.Capture(ctx, "%3", c.source, c.lines, c.ansi)
			if err != nil {
				t.Fatal(err)
			}
			if argv := lastCall(t, calls); argv != c.argv {
				t.Errorf("argv = %q, want %q", argv, c.argv)
			}
			if got != c.want {
				t.Errorf("Capture = %q, want %q", got, c.want)
			}
		})
	}
}

// The phone pages "recent" until fewer rows come back than it asked for: past the start of the history it must.
func TestCaptureRecentEndsAtTheHistorysStart(t *testing.T) {
	client, _ := captureTmux(t)
	got, err := client.Capture(context.Background(), "%3", "recent", 400, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "\n"); n != 150 {
		t.Fatalf("recent 400 = %d rows, want the 150 the pane has", n)
	}
}

func TestCaptureRefusesAnUnknownSource(t *testing.T) {
	client, calls := captureTmux(t)
	if _, err := client.Capture(context.Background(), "%3", "scrollback", 10, false); err == nil {
		t.Fatal("Capture with an unknown source succeeded")
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("tmux ran for an unknown source: %v", err)
	}
}

// Blank rows at the bottom of a tall pane are not rows of the screen's content, and an empty pane reads as nothing.
func TestLastRows(t *testing.T) {
	for _, c := range []struct {
		out  string
		n    int
		ansi bool
		want string
	}{
		{"a\nb\n\n\n", 1, false, "b\n"},
		{"a\nb\n\n\n", 5, true, "a\r\nb\r\n"},
		{"\n\n", 3, false, ""},
		// tmux -e keeps a colored blank row that the plain read drops: both end at the same row, so row i of one is
		// row i of the other.
		{"a\n\x1b[41m   \x1b[49m\n", 0, true, "a\r\n"},
		{"a\n\x1b[41m   \x1b[49m\n\x1b[0m\n", 1, true, "a\r\n"},
		{"\x1b]8;;https://example.com\x1b\\ \x1b]8;;\x1b\\\n", 0, true, ""},
		{"a\n   \n", 0, false, "a\n"},
		// A colored row with text, or a blank row above the content, stays.
		{"\x1b[41m x \x1b[49m\n", 0, true, "\x1b[41m x \x1b[49m\r\n"},
		{"\x1b[41m   \x1b[49m\na\n", 0, true, "\x1b[41m   \x1b[49m\r\na\r\n"},
		{"", 0, true, ""},
	} {
		if got := lastRows(c.out, c.n, c.ansi); got != c.want {
			t.Errorf("lastRows(%q, %d, %v) = %q, want %q", c.out, c.n, c.ansi, got, c.want)
		}
	}
}

// keysTmux is a tmux that records each run's argv, one line per run with every argument followed by '|'.
func keysTmux(t *testing.T) (Client, func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s|' \"$a\"; done >> " + calls + "\necho >> " + calls + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: bin}, func() []string {
		b, err := os.ReadFile(calls)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
}

// Persona's key names become tmux's: tmux types a name it does not know as text, so none goes through as is.
func TestKeysTranslatesPersonaNames(t *testing.T) {
	for _, c := range []struct{ key, want string }{
		{"enter", "Enter"},
		{"return", "Enter"},
		{"esc", "Escape"},
		{"Escape", "Escape"},
		{"shift+tab", "BTab"},
		{"backtab", "BTab"},
		{"shift+right", "S-Right"},
		{"alt+left", "M-Left"},
		{"option+up", "M-Up"},
		{"ctrl+left", "C-Left"},
		{"control+shift+home", "C-S-Home"},
		{"f5", "F5"},
		{"F1", "F1"},
		{"ctrl+shift+f5", "C-S-F5"},
		{"meta+pageup", "M-PageUp"},
		{"shift+delete", "S-DC"},
		{"alt+enter", "M-Enter"},
		{"shift+enter", "M-Enter"}, // ESC CR, as on Orca
		{"alt+backspace", "M-BSpace"},
		{"ctrl+backspace", "C-h"},
		{"ctrl+space", "C-Space"},
		{"alt+esc", "M-Escape"},
		{"ctrl+c", "C-c"},
		{"ctrl+shift+u", "C-u"},
		{"ctrl+]", "C-]"},
		{"ctrl+2", "C-@"},
		{"alt+b", "M-b"},
		{"alt+shift+b", "M-B"},
		{"y", "y"},
		{"A", "A"},
		{"+", "+"},
		{"-", "-"},
		{";", `\;`}, // a bare ';' would end the tmux command
		{"alt+;", `M-\;`},
	} {
		client, calls := keysTmux(t)
		if err := client.Keys(context.Background(), "%3", c.key); err != nil {
			t.Fatalf("Keys(%q): %v", c.key, err)
		}
		want := "send-keys|-t|%3|" + c.want + "|"
		if got := calls(); len(got) != 1 || got[0] != want {
			t.Errorf("Keys(%q) ran %q, want %q", c.key, got, want)
		}
	}
}

// Keys in a row go in one send-keys; ctrl+enter, which has no tmux name, goes as its CSI u bytes in a send-keys -H.
func TestKeysSendsCtrlEnterAsBytes(t *testing.T) {
	client, calls := keysTmux(t)
	if err := client.Keys(context.Background(), "%3", "ctrl+u", "backspace", "ctrl+enter", "enter"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"send-keys|-t|%3|C-u|BSpace|",
		"send-keys|-H|-t|%3|1b|5b|31|33|3b|35|75|",
		"send-keys|-t|%3|Enter|",
	}
	if got := calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// A name Persona does not know is refused before tmux runs at all, even after known ones.
func TestKeysRefusesUnknownNamesBeforePressing(t *testing.T) {
	for _, keys := range [][]string{
		{"bogus name"},
		{"enter", "shift+frob"},
		{"hyper+a"},
		{"ctrl+1"},
		{"shift+space"},
		{"ctrl+tab"},
		{"ctrl+"},
		{""},
	} {
		client, calls := keysTmux(t)
		err := client.Keys(context.Background(), "%3", keys...)
		bad := keys[len(keys)-1]
		if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), `"`+bad+`"`) {
			t.Errorf("Keys(%q) = %v, want an unknown key error naming %q", keys, err, bad)
		}
		if got := calls(); len(got) != 0 {
			t.Errorf("Keys(%q) ran tmux: %q", keys, got)
		}
	}
}
