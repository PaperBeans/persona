package projection

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/jsonsubset"
)

var waitSchema = sync.OnceValues(func() (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "schema", "wire", "todo-wait.schema.json"))
	if err != nil {
		return nil, err
	}
	return jsonsubset.Compile(data)
})

func holdsToWait(t *testing.T, raw []byte) string {
	t.Helper()
	root, err := waitSchema()
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonsubset.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return jsonsubset.ValidateAt(root, root["$defs"].(map[string]any)["TodoWait"].(map[string]any), v, "$", 0)
}

func TestProjectionFollowsTheWaitCases(t *testing.T) {
	data, err := os.ReadFile(fixtures + "todo-wait/todo_wait.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int `json:"version"`
		Cases   []struct {
			Name     string    `json:"name"`
			Input    viewInput `json:"input"`
			Expected struct {
				Wait json.RawMessage `json:"wait"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Version != 1 || len(doc.Cases) == 0 {
		t.Fatalf("todo-wait/todo_wait.json: %v (version %d, %d cases)", err, doc.Version, len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			list := viewList(t, c.Input)
			related := relate(list)
			got := Wait(related[0], c.Input.Now)
			views, waits := ViewsAndWaits(list, c.Input.Now)
			if !reflect.DeepEqual(waits[0], got) {
				t.Fatalf("ViewsAndWaits[0] = %+v, Wait = %+v", waits[0], got)
			}
			wantNull := len(c.Expected.Wait) == 0 || string(bytes.TrimSpace(c.Expected.Wait)) == "null"
			if wantNull {
				if got != nil {
					t.Fatalf("got wait %+v, want nil", got)
				}
				if views[0].Phase == assistantwire.TodoPhaseWaiting {
					t.Fatalf("phase is waiting but wait is nil")
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil wait, want %s", c.Expected.Wait)
			}
			if views[0].Phase != assistantwire.TodoPhaseWaiting {
				t.Fatalf("wait is non-nil but phase is %q", views[0].Phase)
			}
			out, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var g, w any
			if err := json.Unmarshal(out, &g); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(c.Expected.Wait, &w); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g, w) {
				t.Errorf("TodoWait:\n got  %s\n want %s", out, compact(t, c.Expected.Wait))
			}
			if p := holdsToWait(t, out); p != "" {
				t.Errorf("TodoWait does not hold to schema: %s", p)
			}
		})
	}
}

// Invariant: across every shared todo-view case, Wait is non-nil if and only if Phase is waiting, and every
// emitted Wait holds to schema/wire/todo-wait.schema.json.
func TestProjectionWaitMatchesWaitingPhaseAcrossAllViewCases(t *testing.T) {
	for _, c := range readCases[viewInput](t, "todo-view/todo_view.json") {
		t.Run(c.Name, func(t *testing.T) {
			list := viewList(t, c.Input)
			views, waits := ViewsAndWaits(list, c.Input.Now)
			v, w := views[0], waits[0]
			if v.Phase == assistantwire.TodoPhaseWaiting {
				if w == nil {
					t.Fatalf("phase is waiting (reason %q) but Wait is nil", v.Reason)
				}
				if !w.Kind.Valid() {
					t.Fatalf("invalid wait kind %q", w.Kind)
				}
				raw, err := json.Marshal(w)
				if err != nil {
					t.Fatal(err)
				}
				if p := holdsToWait(t, raw); p != "" {
					t.Fatalf("TodoWait schema violation: %s", p)
				}
			} else if w != nil {
				t.Fatalf("phase is %q but Wait is non-nil: %+v", v.Phase, w)
			}
		})
	}
}
