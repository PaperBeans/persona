//go:build sdklive

package node

// Live end-to-end checks of the assistant on the Agent SDK (#2455): the node, the bridge, the SDK and the machine's
// claude against a real gateway. Never run by CI: it needs the sdklive build tag and SDKLIVE=1, plus the gateway in
// SDKLIVE_BASE_URL / SDKLIVE_TOKEN / SDKLIVE_MODEL (SDKLIVE_MODEL2: another model of the same provider). The token
// only goes through the environment; nothing here prints it. Scratch files go under /tmp/e2e-node and are removed.
//
//	cd server && go generate ./internal/runtimepkg && SDKLIVE=1 SDKLIVE_BASE_URL=… SDKLIVE_TOKEN=… SDKLIVE_MODEL=… \
//	  go test -tags sdklive -run TestSDKLive -v -count=1 ./internal/node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
	"github.com/nwverse/persona/server/internal/transcript"
)

const liveRoot = "/tmp/e2e-node"

type liveFix struct {
	*sdkFixture        // n, fh, log, proc, session; its dir is unused
	scratch     string // this test's directory under liveRoot
	cfg         string // CLAUDE_CONFIG_DIR
	tmp         string // TMPDIR: where the bridge writes its settings file
	cwd         string
	token       string
	base        string
	model       string // without [1m]: the launcher's Models say the window, sdkModel adds it
	model2      string
}

func trimWindow(m string) string { return strings.TrimSuffix(m, "[1m]") }

func liveSetup(t *testing.T) *liveFix {
	t.Helper()
	if os.Getenv("SDKLIVE") != "1" {
		t.Skip("set SDKLIVE=1 and SDKLIVE_BASE_URL / SDKLIVE_TOKEN / SDKLIVE_MODEL to run against a real gateway")
	}
	f := &liveFix{base: os.Getenv("SDKLIVE_BASE_URL"), token: os.Getenv("SDKLIVE_TOKEN"), model: trimWindow(os.Getenv("SDKLIVE_MODEL")),
		model2: trimWindow(os.Getenv("SDKLIVE_MODEL2"))}
	if f.base == "" || f.token == "" || f.model == "" {
		t.Skip("SDKLIVE_BASE_URL, SDKLIVE_TOKEN and SDKLIVE_MODEL are needed")
	}
	claude, err := claudeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	js, err := findJSRuntime(context.Background(), userHome())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(liveRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	f.scratch, err = os.MkdirTemp(liveRoot, strings.NewReplacer("/", "-").Replace(t.Name())+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// a process stopped mid-test (a restart) may still be writing its transcript for a moment: remove until gone
		for i := 0; i < 40; i++ {
			_ = os.RemoveAll(f.scratch)
			if time.Sleep(250 * time.Millisecond); !fileExists(f.scratch) {
				return
			}
		}
		t.Errorf("%s not removed", f.scratch)
	})
	home := filepath.Join(f.scratch, "home")
	f.cfg, f.tmp, f.cwd = filepath.Join(f.scratch, "claude-config"), filepath.Join(f.scratch, "tmp"), filepath.Join(f.scratch, "cwd")
	for _, d := range []string{home, f.cfg, f.tmp, f.cwd} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(home, ".zshrc"), nil, 0o600)
	// Nothing reaches the user's ~/.claude or ~/.zshrc: a home, config dir and temp dir of the test's own.
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("CLAUDE_CONFIG_DIR", f.cfg)
	t.Setenv("TMPDIR", f.tmp)
	t.Setenv("PATH", filepath.Dir(claude)+":"+filepath.Dir(js.Path)+":/usr/bin:/bin")
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "PERSONA_NODEJS", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
		t.Setenv(k, "") // restored after the test
		_ = os.Unsetenv(k)
	}
	load, find, cl := sdkLoadPackage, sdkFindJS, sdkClaude
	t.Cleanup(func() { sdkLoadPackage, sdkFindJS, sdkClaude = load, find, cl })
	sdkClaude = func() (string, error) { return claude, nil }

	f.sdkFixture = &sdkFixture{n: codexTestNode(t), log: &lockedBuf{}}
	f.n.dir = filepath.Join(f.scratch, "node")
	f.n.bin = "/usr/bin/true"                       // the persona MCP server and hooks: present, doing nothing
	if err := f.n.writeClaudeConfig(); err != nil { // the hooks and settings the node writes when it starts
		t.Fatal(err)
	}
	f.n.logger = log.New(f.log, "", 0)
	f.fh = withFakeHub(t, f.n)
	t.Cleanup(func() {
		f.n.mu.Lock()
		var ps []*assistantProc
		for _, p := range f.n.procs {
			ps = append(ps, p)
		}
		f.n.mu.Unlock()
		for _, p := range ps {
			f.n.stopAssistant(p.id)
			select {
			case <-p.exited:
			case <-time.After(10 * time.Second):
				t.Errorf("process %d still running", p.cmd.Process.Pid)
			}
		}
		if strings.Contains(f.log.String(), f.token) {
			t.Error("FAIL security: the key is in the node's log")
		}
	})
	return f
}

// launcher is the managed launcher of the gateway.
func (f *liveFix) launcher(timeout int) proto.ManagedLauncher {
	models := []proto.ProviderModel{{ID: f.model, ContextWindow: 1_000_000}}
	if f.model2 != "" {
		models = append(models, proto.ProviderModel{ID: f.model2, ContextWindow: 1_000_000})
	}
	return proto.ManagedLauncher{Name: "gw", Kind: "api", Auth: "bearer", BaseURL: f.base, APIKey: f.token, Model: f.model,
		Models: models, TimeoutMS: timeout}
}

func liveExec(model, runtime string) modelExecution {
	return modelExecution{Model: model, Fingerprint: "live", Runtime: runtime}
}

// send sends text and waits for the turn to end.
func (f *liveFix) send(t *testing.T, id, launcher, text, effort string, ex modelExecution) string {
	t.Helper()
	id = f.sendAsync(t, id, launcher, text, effort, ex)
	f.waitIdle(t, id)
	return id
}

func (f *liveFix) sendAsync(t *testing.T, id, launcher, text, effort string, ex modelExecution) string {
	t.Helper()
	ex.ConfigDir = f.cfg // this fixture's profile is explicit, never selected by the node service's environment
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	got, err := f.n.assistantSend(ctx, id, f.cwd, launcher, text, nil, "", effort, ex)
	if err != nil {
		t.Fatalf("send %q: %v\nlog:\n%s", text, err, f.redacted())
	}
	return got
}

func (f *liveFix) redacted() string { return strings.ReplaceAll(f.log.String(), f.token, "<key>") }

func liveWait(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (f *liveFix) waitIdle(t *testing.T, id string) {
	t.Helper()
	liveWait(t, "the turn's end", 3*time.Minute, func() bool {
		p := f.proc(id)
		if p == nil || !p.alive() {
			t.Fatalf("the process of %s is gone mid-turn\nlog:\n%s", short(id), f.redacted())
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.busy
	})
}

func (f *liveFix) deltas() []string {
	f.fh.mu.Lock()
	defer f.fh.mu.Unlock()
	return slices.Clone(f.fh.deltas)
}

// transcriptFile is the conversation's transcript under the test's config dir.
func (f *liveFix) transcriptFile(t *testing.T, id string) string {
	t.Helper()
	var path string
	liveWait(t, "the transcript", 20*time.Second, func() bool {
		m, _ := filepath.Glob(filepath.Join(f.cfg, "projects", "*", id+".jsonl"))
		if len(m) > 0 {
			path = m[0]
		}
		return path != ""
	})
	return path
}

// items reads the transcript with Persona's parser once it holds an answer after the last user message.
func (f *liveFix) items(t *testing.T, id string) []proto.Item {
	t.Helper()
	path := f.transcriptFile(t, id)
	var items []proto.Item
	liveWait(t, "an answer in the transcript", 20*time.Second, func() bool {
		var err error
		if items, err = transcript.NewFile(path).Refresh(); err != nil {
			t.Fatal(err)
		}
		return lastAnswer(items) != ""
	})
	return items
}

// lastAnswer is the assistant's text after the last user message, "" while there is none.
func lastAnswer(items []proto.Item) string {
	var b strings.Builder
	for _, it := range items {
		switch it.Role {
		case "user":
			b.Reset()
		case "assistant":
			if it.Text != "" && it.ToolName == "" {
				b.WriteString(it.Text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// lastModel is the model of the transcript's last assistant line.
func lastModel(t *testing.T, path string) string {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var model string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Type == "assistant" && l.Message.Model != "" {
			model = l.Message.Model
		}
	}
	return model
}

// tree is pid and its descendants.
func tree(pid int) []int {
	out := []int{pid}
	b, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	for _, f := range strings.Fields(string(b)) {
		if c, err := strconv.Atoi(f); err == nil {
			out = append(out, tree(c)...)
		}
	}
	return out
}

func argv(pid int) string {
	b, _ := exec.Command("ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(b))
}

func liveOK(t *testing.T, item, what string) { t.Logf("RESULT %s ok: %s", item, what) }

const (
	codeWord = "PINEAPPLE42"
	remember = "Remember the code word " + codeWord + ". Reply with only: OK"
	recall   = "What was the code word? Reply with only the word."
)

// recalled sends recall and says the answer, and whether it holds the code word.
func (f *liveFix) recalled(t *testing.T, id string, ex modelExecution) (string, bool) {
	t.Helper()
	f.send(t, id, "gw", recall, "", ex)
	ans := lastAnswer(f.items(t, id))
	return ans, strings.Contains(ans, codeWord)
}

// stop stops the conversation's process and waits for it to be gone.
func (f *liveFix) stop(t *testing.T, id string) {
	t.Helper()
	if p := f.proc(id); p != nil {
		f.n.stopAssistant(id)
		<-p.exited
	}
}

// gone waits for every pid to have exited.
func awaitGone(t *testing.T, what string, pids []int) {
	t.Helper()
	liveWait(t, what, 10*time.Second, func() bool { return !slices.ContainsFunc(pids, alive) })
}

// cliEnv is the gateway in the environment, for the plain claude launcher: the CLI path through a managed launcher
// needs `personad launch` and its node socket.
func (f *liveFix) cliEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", f.base)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", f.token)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_MODEL", f.model+"[1m]")
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
}

// 1. A message on agentsdk: the bridge runs it, deltas stream, the turn ends, the transcript reads.
func TestSDKLive1Send(t *testing.T) {
	f := liveSetup(t)
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(0)})
	start := time.Now()
	id := f.send(t, "", "gw", "Reply with exactly the word: hello", "", liveExec(f.model, proto.AssistantRuntimeAgentSDK))
	t.Logf("turn took %s", time.Since(start).Round(time.Millisecond))
	s := f.session(t, id)
	if s.Runtime != proto.AssistantRuntimeAgentSDK || s.RuntimeFallback != "" {
		t.Fatalf("FAIL 1: runtime %q fallback %q\nlog:\n%s", s.Runtime, s.RuntimeFallback, f.redacted())
	}
	p := f.proc(id)
	if !strings.HasSuffix(p.cmd.Args[len(p.cmd.Args)-1], filepath.Join("agent-sdk", "dist", "bridge.mjs")) || p.sdkVersion == "" {
		t.Fatalf("FAIL 1: not the bridge: %v (sdk %q)", p.cmd.Args, p.sdkVersion)
	}
	if len(f.deltas()) == 0 {
		t.Fatal("FAIL 1: no session.delta")
	}
	items := f.items(t, id)
	var user bool
	for _, it := range items {
		user = user || (it.Role == "user" && strings.Contains(it.Text, "Reply with exactly the word: hello"))
	}
	if !user || !strings.Contains(strings.ToLower(lastAnswer(items)), "hello") {
		t.Fatalf("FAIL 1: transcript user %v answer %q", user, lastAnswer(items))
	}
	if tp := f.n.transcriptPath(id); tp != f.transcriptFile(t, id) {
		t.Errorf("FAIL 1: node's transcriptPath %q", tp)
	}
	liveOK(t, "1", fmt.Sprintf("runtime=agentsdk via bridge (sdk %s), %d deltas, turn ended, %d transcript items, answer %q",
		p.sdkVersion, len(f.deltas()), len(items), lastAnswer(items)))
}

// 2. Effort switched while it runs stays in the process; a model, an effort with the message or a provider field
// change restarts it with --resume, like the CLI (they change the env the provider's tiers and windows derive from),
// and the conversation goes on.
func TestSDKLive2Switch(t *testing.T) {
	f := liveSetup(t)
	if f.model2 == "" {
		t.Skip("SDKLIVE_MODEL2 is needed")
	}
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(0)})
	ex := liveExec(f.model, proto.AssistantRuntimeAgentSDK)
	id := f.send(t, "", "gw", remember, "", ex)
	prev := f.proc(id)
	restarted := func(item, what string) {
		t.Helper()
		if p := f.proc(id); p != prev {
			t.Errorf("FAIL %s: %s restarted the bridge (pid %d → %d)", item, what, prev.cmd.Process.Pid, p.cmd.Process.Pid)
			prev = p
		} else {
			liveOK(t, item, what+": same process")
		}
	}

	// effort as the app switches it (session.modes): a control request to the running bridge
	if err := f.n.setModes(context.Background(), id, "", "low"); err != nil {
		t.Fatalf("FAIL 2a: session.modes: %v", err)
	}
	f.send(t, id, "gw", "Reply with only: OK", "", ex)
	restarted("2a", "session.modes effort low, then a send")

	// effort that comes with the message (session.send's effort)
	f.send(t, id, "gw", "Reply with only: OK", "medium", ex)
	if p := f.proc(id); p != prev {
		liveOK(t, "2b", fmt.Sprintf("session.send effort medium restarted (pid %d → %d), as the CLI does", prev.cmd.Process.Pid, p.cmd.Process.Pid))
		prev = p
	} else {
		liveOK(t, "2b", "session.send effort medium: same process")
	}
	if strings.Contains(f.log.String(), "control request failed") {
		t.Errorf("FAIL 2: %s", f.redacted())
	}

	// another model of the provider
	ex2 := liveExec(f.model2, proto.AssistantRuntimeAgentSDK)
	ans, knows := f.recalled(t, id, ex2)
	if m := lastModel(t, f.transcriptFile(t, id)); m != f.model2 || !knows {
		t.Errorf("FAIL 2c: after the model switch the model is %q, the answer %q", m, ans)
	} else {
		liveOK(t, "2c", fmt.Sprintf("model switch: now %s, the conversation still knows the word", m))
	}
	prev = f.proc(id)

	// provider field: next message restarts with resume and still knows the word
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(600000)})
	ans, knows = f.recalled(t, id, ex2)
	switch p := f.proc(id); {
	case p == prev:
		t.Errorf("FAIL 2d: TimeoutMS change did not restart the process")
	case !knows:
		t.Errorf("FAIL 2d: after the provider change the answer is %q", ans)
	default:
		liveOK(t, "2d", fmt.Sprintf("TimeoutMS change restarted (pid %d → %d) with resume, answer %q", prev.cmd.Process.Pid, p.cmd.Process.Pid, ans))
	}
}

// 3. Interrupting a long answer ends the turn; the conversation goes on.
func TestSDKLive3Interrupt(t *testing.T) {
	f := liveSetup(t)
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(0)})
	ex := liveExec(f.model, proto.AssistantRuntimeAgentSDK)
	id := f.send(t, "", "gw", remember, "", ex)
	before := len(f.deltas())
	f.sendAsync(t, id, "gw", "Count from 1 to 500, one number per line, each followed by its English word.", "", ex)
	liveWait(t, "the long answer streaming", 2*time.Minute, func() bool { return len(f.deltas()) > before+5 })
	p := f.proc(id)
	if err := f.n.assistantInterrupt(id); err != nil {
		t.Fatalf("FAIL 3: interrupt: %v", err)
	}
	liveWait(t, "the turn to stop", 30*time.Second, func() bool {
		select {
		case <-p.exited:
			return true
		default:
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.busy
	})
	// an interrupted process is stopped (as the CLI): the conversation is out of assistantSessions, its state stays
	st, _ := f.n.state.get(id)
	t.Logf("after interrupt: state runtime %q, process alive %v, listed %v", st.Runtime, p.alive(), f.proc(id) != nil)
	if st.Runtime != proto.AssistantRuntimeAgentSDK {
		t.Errorf("FAIL 3: runtime after interrupt %q", st.Runtime)
	}
	if ans, knows := f.recalled(t, id, ex); !knows {
		t.Fatalf("FAIL 3: after the interrupt the answer is %q", ans)
	} else {
		liveOK(t, "3", fmt.Sprintf("interrupted (process alive after: %v), next message answered %q", p.alive(), ans))
	}
}

// 4. Reaped (stopAssistant, as reapAssistants does) or killed: the next message resumes with the context.
func TestSDKLive4Reap(t *testing.T) {
	f := liveSetup(t)
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(0)})
	ex := liveExec(f.model, proto.AssistantRuntimeAgentSDK)
	id := f.send(t, "", "gw", remember, "", ex)

	f.stop(t, id)
	if ans, knows := f.recalled(t, id, ex); !knows {
		t.Errorf("FAIL 4a: after reaping the answer is %q", ans)
	} else {
		liveOK(t, "4a", "reaped, resumed: "+ans)
	}

	// SIGKILL the bridge alone: its claude must not stay behind, and the next message resumes.
	p := f.proc(id)
	pids := tree(p.cmd.Process.Pid)
	settings := settingsPath(pids)
	_ = syscall.Kill(p.cmd.Process.Pid, syscall.SIGKILL)
	<-p.exited
	awaitGone(t, "claude to go with its killed bridge", pids)
	if _, err := os.Stat(settings); settings != "" && err == nil {
		t.Logf("note 4b: a SIGKILLed bridge leaves its settings file %s (no exit handler can run)", settings)
	}
	if ans, knows := f.recalled(t, id, ex); !knows {
		t.Errorf("FAIL 4b: after a kill the answer is %q", ans)
	} else {
		liveOK(t, "4b", "killed, resumed: "+ans)
	}
}

// settingsPath is the --settings file on a claude command line among pids.
func settingsPath(pids []int) string {
	for _, pid := range pids {
		args := strings.Fields(argv(pid))
		if i := slices.Index(args, "--settings"); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// 5. Falling back to the CLI: the SDK can't run, the conversation says why and still answers.
func TestSDKLive5Fallback(t *testing.T) {
	f := liveSetup(t)
	f.cliEnv(t)
	ex := modelExecution{Fingerprint: "live", Runtime: proto.AssistantRuntimeAgentSDK}

	// PERSONA_NODEJS that is not there: findJSRuntime goes on to the node on PATH
	t.Setenv("PERSONA_NODEJS", filepath.Join(f.scratch, "no-such-node"))
	id := f.send(t, "", "claude", "Reply with exactly the word: hello", "", ex)
	s := f.session(t, id)
	t.Logf("5a PERSONA_NODEJS missing: runtime %q fallback %q answer %q", s.Runtime, s.RuntimeFallback, lastAnswer(f.items(t, id)))
	f.stop(t, id)

	// the bridge's package unavailable
	sdkLoadPackage = func() (*runtimepkg.Package, error) { return nil, nil }
	id = f.send(t, "", "claude", "Reply with exactly the word: hello", "", ex)
	s = f.session(t, id)
	ans := lastAnswer(f.items(t, id))
	switch p := f.proc(id); {
	case s.Runtime != "" || s.RuntimeFallback == "":
		t.Errorf("FAIL 5: runtime %q fallback %q", s.Runtime, s.RuntimeFallback)
	case strings.Contains(strings.Join(p.cmd.Args, " "), "bridge.mjs"):
		t.Errorf("FAIL 5: still the bridge: %v", p.cmd.Args)
	case !strings.Contains(strings.ToLower(ans), "hello"):
		t.Errorf("FAIL 5: answer %q", ans)
	default:
		liveOK(t, "5", fmt.Sprintf("package missing → CLI, fallback %q, answer %q", s.RuntimeFallback, ans))
	}
}

// 6. No key on the bridge's or claude's command line; the settings file is 0600 and goes with the process.
func TestSDKLive6Security(t *testing.T) {
	f := liveSetup(t)
	f.n.setManaged([]proto.ManagedLauncher{f.launcher(0)})
	id := f.send(t, "", "gw", "Reply with only: OK", "", liveExec(f.model, proto.AssistantRuntimeAgentSDK))
	pids := tree(f.proc(id).cmd.Process.Pid)
	var claudeSeen bool
	for _, pid := range pids {
		a := argv(pid)
		if strings.Contains(a, f.token) {
			t.Errorf("FAIL 6: the key on pid %d's command line", pid)
		}
		claudeSeen = claudeSeen || strings.Contains(a, "claude")
	}
	if !claudeSeen || len(pids) < 2 {
		t.Errorf("FAIL 6: no claude under the bridge (%d processes)", len(pids))
	}
	settings := settingsPath(pids)
	if settings == "" {
		t.Fatalf("FAIL 6: no --settings file on claude's command line")
	}
	st, err := os.Stat(settings)
	if err != nil {
		t.Fatalf("FAIL 6: %v", err)
	}
	dst, _ := os.Stat(filepath.Dir(settings))
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 || !strings.HasPrefix(settings, f.tmp) {
		t.Errorf("FAIL 6: settings %s %v, dir %v", settings, st.Mode().Perm(), dst.Mode().Perm())
	}
	f.stop(t, id)
	liveWait(t, "the settings file removed", 10*time.Second, func() bool {
		_, err := os.Stat(filepath.Dir(settings))
		return os.IsNotExist(err)
	})
	awaitGone(t, "the bridge and claude to exit", pids)
	liveOK(t, "6", fmt.Sprintf("%d processes, no key in argv; settings 0600 in a 0700 dir under TMPDIR, removed on exit", len(pids)))
}

// 7. The same message on the CLI and on the SDK: the transcripts have the same shape.
func TestSDKLive7CompareCLI(t *testing.T) {
	f := liveSetup(t)
	f.cliEnv(t)
	shape := func(runtime string) ([]string, []string) {
		id := f.send(t, "", "claude", "Reply with exactly the word: hello", "", modelExecution{Fingerprint: "live", Runtime: runtime})
		if s := f.session(t, id); s.Runtime != runtime {
			t.Fatalf("FAIL 7: asked %q, runs %q (%s)", runtime, s.Runtime, s.RuntimeFallback)
		}
		var items []string
		for _, it := range f.items(t, id) {
			if it.Kind == "thinking" { // the model thinks on some turns and not others, on either runtime
				continue
			}
			items = append(items, it.Role+"/"+it.Kind)
		}
		raw := rawTypes(t, f.transcriptFile(t, id))
		f.stop(t, id)
		return items, raw
	}
	cliItems, cliRaw := shape("")
	sdkItems, sdkRaw := shape(proto.AssistantRuntimeAgentSDK)
	t.Logf("7 cli items %v raw %v", cliItems, cliRaw)
	t.Logf("7 sdk items %v raw %v", sdkItems, sdkRaw)
	if !slices.Equal(cliItems, sdkItems) {
		t.Errorf("FAIL 7: parsed items differ: cli %v sdk %v", cliItems, sdkItems)
	} else {
		liveOK(t, "7", fmt.Sprintf("parsed items equal %v; raw line types cli %v sdk %v", cliItems, cliRaw, sdkRaw))
	}
}

// rawTypes is the distinct type[/subtype] of a transcript's lines, sorted.
func rawTypes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		var l struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal([]byte(line), &l) == nil && l.Type != "" {
			seen[strings.TrimSuffix(l.Type+"/"+l.Subtype, "/")] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
