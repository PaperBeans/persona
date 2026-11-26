package node

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// secretRunDeliveryCall keeps delivery as raw JSON so these acceptance tests also compile against a node from before
// explicit environment delivery existed. The request is the same secret.run request the manager sends.
func secretRunDeliveryCall(t *testing.T, delivery string, commands []string) (proto.SecretRunAnswer, error, json.RawMessage, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("secret.run refuses Windows")
	}
	home := t.TempDir()
	request := map[string]any{
		"commands":   commands,
		"password":   []byte(testSecret),
		"timeout_ms": (30 * time.Second).Milliseconds(),
	}
	if delivery != "" {
		request["delivery"] = delivery
	}
	params, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{home: home}
	out, callErr := n.handle(context.Background(), proto.MethodSecretRun, params)
	var ans proto.SecretRunAnswer
	if callErr == nil {
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), testSecret) {
			t.Fatalf("the answer carries the password: %s", b)
		}
		if err := json.Unmarshal(b, &ans); err != nil {
			t.Fatalf("answer %s: %v", b, err)
		}
	} else if strings.Contains(callErr.Error(), testSecret) {
		t.Fatalf("the error carries the password: %v", callErr)
	}
	return ans, callErr, params, home
}

// Omitted delivery and explicit stdin retain the strict contract: only sudo -S reads the typed password, another
// command reads empty stdin, and neither gets that password in PERSONA_SECRET.
func TestSecretRunStdinDeliveryNeverPopulatesSecretEnv(t *testing.T) {
	t.Setenv("PERSONA_SECRET", "")
	sudo := fakeSudo(t, `read p; printf 'stdin=%d env=%d\n' "${#p}" "${#PERSONA_SECRET}"`)
	for _, delivery := range []string{"", "stdin"} {
		name := delivery
		if name == "" {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			commands := []string{
				sudo + " -S -k -p '' whoami",
				`read p; printf 'stdin=%d env=%d argc=%d\n' "${#p}" "${#PERSONA_SECRET}" "$#"`,
			}
			ans, err, params, _ := secretRunDeliveryCall(t, delivery, commands)
			if err != nil {
				t.Fatal(err)
			}
			if len(ans.Results) != 2 {
				t.Fatalf("results = %+v, want both commands", ans.Results)
			}
			if got, want := ans.Results[0].Output, fmt.Sprintf("stdin=%d env=0\n", len(testSecret)); got != want {
				t.Fatalf("sudo -S output %q, want %q", got, want)
			}
			if got, want := ans.Results[1].Output, "stdin=0 env=0 argc=0\n"; got != want {
				t.Fatalf("non-sudo output %q, want %q", got, want)
			}
			if strings.Trim(string(params), "\x00") != "" {
				t.Fatal("the request's bytes were not zeroed")
			}
		})
	}
}

// Explicit env delivery gives every command exactly the typed password in its own PERSONA_SECRET, never as an
// argument or stdin. The node's own environment is untouched and the run leaves no credential file behind.
func TestSecretRunEnvDeliversOnlyThroughChildEnvironment(t *testing.T) {
	const nodeEnvironment = "node-original-not-the-typed-password"
	t.Setenv("PERSONA_SECRET", nodeEnvironment)
	hashCommand := "/usr/bin/shasum -a 256"
	if runtime.GOOS == "linux" {
		hashCommand = "/usr/bin/sha256sum"
	}
	command := `read p; printf 'stdin=%d argc=%d env=%d sha=' "${#p}" "$#" "${#PERSONA_SECRET}"; ` +
		`printf %s "$PERSONA_SECRET" | ` + hashCommand + ` | /usr/bin/cut -d ' ' -f 1`
	ans, err, params, home := secretRunDeliveryCall(t, "env", []string{command, command})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("stdin=0 argc=0 env=%d sha=%x\n", len(testSecret), sha256.Sum256([]byte(testSecret)))
	if len(ans.Results) != 2 {
		t.Fatalf("results = %+v, want every approved command", ans.Results)
	}
	for i, result := range ans.Results {
		if result.ExitCode != 0 || result.Output != want || result.OutputWithheld {
			t.Errorf("command %d = %+v, want exact child env with empty stdin/argv and output %q", i+1, result, want)
		}
	}
	if got := os.Getenv("PERSONA_SECRET"); got != nodeEnvironment {
		t.Errorf("the node process environment changed to %q", got)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the run left files or reusable credentials in its home: %v", names)
	}
	if strings.Trim(string(params), "\x00") != "" {
		t.Fatal("the one-use request's bytes were not zeroed")
	}
}

// Environment delivery uses the same streaming literal redaction and bounded output as stdin delivery.
func TestSecretRunEnvRetainsOutputWithholdingAndBounds(t *testing.T) {
	t.Setenv("PERSONA_SECRET", "")
	commands := []string{
		`printf 'before:%s:after' "$PERSONA_SECRET"`,
		`yes x | head -n 20000`,
	}
	ans, err, params, _ := secretRunDeliveryCall(t, "env", commands)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 2 {
		t.Fatalf("results = %+v, want both commands", ans.Results)
	}
	if got := ans.Results[0]; got.Output != "" || !got.OutputWithheld || got.Truncated {
		t.Errorf("output containing PERSONA_SECRET = %+v, want output_withheld with no output", got)
	}
	if got := ans.Results[1]; !got.Truncated || len(got.Output) != secretRunClip || strings.Trim(got.Output, "x\n") != "" || got.OutputWithheld {
		t.Errorf("bounded clean output = %d bytes, truncated %v, withheld %v", len(got.Output), got.Truncated, got.OutputWithheld)
	}
	if strings.Trim(string(params), "\x00") != "" {
		t.Fatal("the request's bytes were not zeroed")
	}
}

// An unknown mode is not silently treated as either stdin or env, and its command never starts.
func TestSecretRunRefusesUnknownDeliveryBeforeRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "unknown-delivery-ran")
	ans, err, params, _ := secretRunDeliveryCall(t, "environment", []string{"touch " + shellQuote(marker)})
	if err == nil {
		t.Errorf("unknown delivery was accepted: %+v", ans.Results)
	}
	if fileExists(marker) {
		t.Error("the command for an unknown delivery ran")
	}
	if strings.Trim(string(params), "\x00") != "" {
		t.Fatal("the refused request's bytes were not zeroed")
	}
}
