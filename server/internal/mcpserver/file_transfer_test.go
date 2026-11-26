package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// A path on this computer may be relative to the agent's directory: send's path and fetch's dest are made absolute
// here, the other computer's path is left alone, and anything else passes unchanged.
func TestFileTransferArgsResolveThisComputersRelativePath(t *testing.T) {
	cwd := t.TempDir() // absolute on every system
	abs := filepath.Join(cwd, "elsewhere", "a")
	absJSON, _ := json.Marshal(abs)
	cases := []struct {
		name, in, key, want string
	}{
		{"send relative", `{"direction":"send","machine":"b","path":"out/report.pdf"}`, "path", filepath.Join(cwd, "out/report.pdf")},
		{"send absolute", `{"direction":"send","machine":"b","path":` + string(absJSON) + `}`, "path", abs},
		{"send home", `{"direction":"send","machine":"b","path":"~/a"}`, "path", "~/a"},
		{"fetch dest relative", `{"direction":"fetch","machine":"b","path":"docs/x","dest":"in"}`, "dest", filepath.Join(cwd, "in")},
		{"fetch path is the other computer's", `{"direction":"fetch","machine":"b","path":"docs/x"}`, "path", "docs/x"},
		{"fetch without dest", `{"direction":"fetch","machine":"b","path":"~/x"}`, "dest", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got map[string]any
			if err := json.Unmarshal(fileTransferArgs(json.RawMessage(c.in), cwd), &got); err != nil {
				t.Fatal(err)
			}
			v, _ := got[c.key].(string)
			if v != c.want {
				t.Fatalf("%s = %q, want %q", c.key, v, c.want)
			}
		})
	}
	for _, in := range []string{`{"direction":"list"}`, `not json`, `{"direction":"send","path":"a"}`} {
		cwdArg := cwd
		if in == `{"direction":"send","path":"a"}` {
			cwdArg = "" // no working directory known: unchanged
		}
		if got := string(fileTransferArgs(json.RawMessage(in), cwdArg)); got != in {
			t.Errorf("%s changed to %s", in, got)
		}
	}
}
