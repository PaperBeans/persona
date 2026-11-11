package assistant

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project's soft delete over the apps' routes (#4412): delete, list the trash, restore, and the 404 / 409 answers.
func TestKBProjectTrashRoutes(t *testing.T) {
	dir := t.TempDir()
	writeProject := func(name string) {
		t.Helper()
		path := filepath.Join(dir, "projects", "example")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "meta.json"), []byte(`{"slug":"example","name":"`+name+`","status":"active"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProject("Example")
	s := kbServiceOn(t, newFakeHub(), newKBStore(), dir, nil)
	do := func(method, path string) (int, string) {
		r := httptest.NewRecorder()
		s.ServeHTTP(r, httptest.NewRequest(method, path, nil))
		return r.Code, r.Body.String()
	}
	code, body := do("DELETE", "/api/v1/kb/projects/example")
	if code != 200 {
		t.Fatalf("delete: %d %s", code, body)
	}
	var entry struct{ ID, Slug, Name string }
	if err := json.Unmarshal([]byte(body), &entry); err != nil || entry.Slug != "example" || entry.Name != "Example" || entry.ID == "" {
		t.Fatalf("delete answer %s (%v)", body, err)
	}
	if code, body := do("GET", "/api/v1/kb/projects"); code != 200 || strings.Contains(body, `"example"`) {
		t.Fatalf("list after delete: %d %s", code, body)
	}
	if code, body := do("GET", "/api/v1/kb-trash"); code != 200 || !strings.Contains(body, entry.ID) {
		t.Fatalf("trash: %d %s", code, body)
	}
	if code, body := do("DELETE", "/api/v1/kb/projects/example"); code != 404 {
		t.Fatalf("second delete: %d %s", code, body)
	}
	if code, body := do("POST", "/api/v1/kb-trash/"+entry.ID+"/restore"); code != 200 || !strings.Contains(body, `"slug":"example"`) {
		t.Fatalf("restore: %d %s", code, body)
	}
	if code, body := do("GET", "/api/v1/kb/projects"); code != 200 || !strings.Contains(body, `"example"`) {
		t.Fatalf("list after restore: %d %s", code, body)
	}
	if code, body := do("POST", "/api/v1/kb-trash/"+entry.ID+"/restore"); code != 404 {
		t.Fatalf("second restore: %d %s", code, body)
	}
	// Deleted again, then a project with the same slug appears: the restore is refused.
	code, body = do("DELETE", "/api/v1/kb/projects/example")
	if code != 200 {
		t.Fatalf("delete again: %d %s", code, body)
	}
	_ = json.Unmarshal([]byte(body), &entry)
	writeProject("Newer")
	if code, body := do("POST", "/api/v1/kb-trash/"+entry.ID+"/restore"); code != 409 {
		t.Fatalf("restore over a newer project: %d %s", code, body)
	}
}
