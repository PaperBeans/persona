package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type taskTrustResponse struct {
	Kinds []struct {
		Key       string `json:"key"`
		Machine   string `json:"machine"`
		Cwd       string `json:"cwd"`
		Readonly  bool   `json:"readonly"`
		Ambiguous bool   `json:"ambiguous"`
	} `json:"kinds"`
}

func taskTrustRequest(s *Service, method, key, role string) *httptest.ResponseRecorder {
	path := "/api/v1/task-trust"
	if key != "" {
		path += "?key=" + url.QueryEscape(key)
	}
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set(RoleHeader, role)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func taskTrustList(t *testing.T, s *Service) taskTrustResponse {
	t.Helper()
	w := taskTrustRequest(s, http.MethodGet, "", roleFull)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var result taskTrustResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTaskTrustHTTPPersistentKeysAndLegacyClient(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	dir := filepath.Join(t.TempDir(), "a|b")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ordinary := PlanTask{Machine: "example-node", Cwd: "/example/work"}
	read := ordinary
	read.Readonly = true
	pipeDir := PlanTask{Machine: "example-node", Cwd: dir}
	pipeMachine := PlanTask{Machine: "example|node", Cwd: "/example/work"}
	otherMachine := PlanTask{Machine: "example|other", Cwd: "/example/work", Readonly: true}
	escaped := PlanTask{Machine: "example|node", Cwd: "/example/#?+%|work"}
	tasks := []PlanTask{ordinary, read, pipeDir, pipeMachine, otherMachine, escaped}
	s.trustKinds(ctx, tasks)

	// Reconstruct the service on the same persistent store, as after a restart.
	s = newServiceOn(t, newFakeHub(), st)
	for _, task := range []PlanTask{ordinary, pipeDir, pipeMachine} {
		if !s.planStartsAtOnce(ctx, []PlanTask{task}) {
			t.Fatalf("saved edit rule no longer matches: %q", trustKey(task))
		}
	}
	w := taskTrustRequest(s, http.MethodGet, "", roleFull)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var result taskTrustResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Kinds) != len(tasks) {
		t.Fatalf("got %d kinds, want %d: %s", len(result.Kinds), len(tasks), w.Body.String())
	}
	var raw struct {
		Kinds []map[string]any `json:"kinds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	// This is the old Apple decoder: unknown fields are ignored, and all four fields remain present.
	var legacy struct {
		Kinds []struct {
			Key      string `json:"key"`
			Machine  string `json:"machine"`
			Cwd      string `json:"cwd"`
			Readonly bool   `json:"readonly"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]int{}
	for i, kind := range result.Kinds {
		for _, field := range []string{"key", "machine", "cwd", "readonly"} {
			if _, ok := raw.Kinds[i][field]; !ok {
				t.Errorf("legacy decoder requires %q: %v", field, raw.Kinds[i])
			}
		}
		if i > 0 && result.Kinds[i-1].Key >= kind.Key {
			t.Fatalf("list is not sorted or contains duplicates: %+v", result.Kinds)
		}
		byKey[kind.Key] = i
	}
	for _, task := range tasks {
		key := trustKey(task)
		i, ok := byKey[key]
		if !ok {
			t.Errorf("active key %q is absent: %s", key, w.Body.String())
			continue
		}
		kind := result.Kinds[i]
		if kind.Readonly != task.Readonly {
			t.Errorf("mode for %q: %+v", key, kind)
		}
		ambiguous := strings.Count(key, "|") > 2
		if ambiguous {
			identifier := url.PathEscape(key)
			if !kind.Ambiguous || kind.Machine != "" || kind.Cwd != identifier {
				t.Errorf("ambiguous key %q misrepresented: %+v", key, kind)
			}
			if legacy.Kinds[i].Machine != "" || legacy.Kinds[i].Cwd != identifier {
				t.Errorf("legacy decoder cannot distinguish %q: %+v", key, legacy.Kinds[i])
			}
		} else if kind.Ambiguous || kind.Machine != task.Machine || kind.Cwd != task.Cwd || raw.Kinds[i]["ambiguous"] != nil {
			t.Errorf("ordinary key changed shape: %+v, raw %v", kind, raw.Kinds[i])
		}
	}

	// A read-only device may inspect but not revoke a remembered key.
	if got := taskTrustRequest(s, http.MethodGet, "", "answer").Code; got != http.StatusOK {
		t.Errorf("read-only GET: %d", got)
	}
	if got := taskTrustRequest(s, http.MethodDelete, trustKey(pipeDir), "answer").Code; got != http.StatusForbidden {
		t.Errorf("read-only DELETE: %d", got)
	}
	if !s.planStartsAtOnce(ctx, []PlanTask{pipeDir}) {
		t.Fatal("forbidden delete changed the edit rule")
	}

	// Delete a read rule without deleting the matching edit rule.
	if w := taskTrustRequest(s, http.MethodDelete, trustKey(read), roleFull); w.Code != http.StatusOK {
		t.Fatalf("delete read: %d %s", w.Code, w.Body.String())
	}
	if !s.planStartsAtOnce(ctx, []PlanTask{ordinary}) || slices.Contains(storedTrust(t, st), trustKey(read)) {
		t.Fatal("deleting read did not preserve the edit rule or remove the read key")
	}
	for _, task := range []PlanTask{pipeDir, pipeMachine, otherMachine, escaped, ordinary} {
		key := trustKey(task)
		if w := taskTrustRequest(s, http.MethodDelete, key, roleFull); w.Code != http.StatusOK {
			t.Fatalf("delete %q: %d %s", key, w.Code, w.Body.String())
		}
		if slices.Contains(storedTrust(t, st), key) {
			t.Fatalf("key survived DELETE: %q", key)
		}
		if !task.Readonly && s.planStartsAtOnce(ctx, []PlanTask{task}) {
			t.Fatalf("deleted edit key still admits %q", key)
		}
	}
	if got := taskTrustRequest(s, http.MethodDelete, trustKey(pipeDir), roleFull).Code; got != http.StatusNotFound {
		t.Errorf("repeat DELETE: %d", got)
	}
	if len(taskTrustList(t, s).Kinds) != 0 {
		t.Fatal("deleted rules still listed")
	}
}

func TestTaskTrustHTTPAmbiguousCollisionKeepsMatching(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	machinePipe := PlanTask{Machine: "example|node", Cwd: "/work"}
	directoryPipe := PlanTask{Machine: "example", Cwd: "node|/work"}
	if trustKey(machinePipe) != trustKey(directoryPipe) {
		t.Fatal("fixture must demonstrate the legacy collision")
	}
	s.trustKinds(ctx, []PlanTask{machinePipe})
	s = newServiceOn(t, newFakeHub(), st)
	for _, task := range []PlanTask{machinePipe, directoryPipe} {
		if !s.planStartsAtOnce(ctx, []PlanTask{task}) {
			t.Fatal("legacy key matching changed")
		}
	}
	list := taskTrustList(t, s)
	if len(list.Kinds) != 1 || !list.Kinds[0].Ambiguous || list.Kinds[0].Machine != "" || list.Kinds[0].Cwd != url.PathEscape(trustKey(machinePipe)) {
		t.Fatalf("ambiguous collision must not choose a machine/directory pair: %+v", list)
	}
	if w := taskTrustRequest(s, http.MethodDelete, list.Kinds[0].Key, roleFull); w.Code != http.StatusOK {
		t.Fatalf("delete collision: %d %s", w.Code, w.Body.String())
	}
	for _, task := range []PlanTask{machinePipe, directoryPipe} {
		if s.planStartsAtOnce(ctx, []PlanTask{task}) {
			t.Fatal("deleted collision still admits a matching edit task")
		}
	}
}

// Apple clients predating ambiguous use NSString.lastPathComponent(cwd) as the row title. A single escaped path
// component preserves the entire distinct identifier there, even when the original keys share a last path component.
func TestTaskTrustHTTPLegacyTitleDistinguishesOpaqueKeys(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	tasks := []PlanTask{
		{Machine: "example|node", Cwd: "/same"},
		{Machine: "different|node", Cwd: "/same"},
		{Machine: "example|node", Cwd: "/%2F|é🚀"},
		{Machine: "example|node", Cwd: "//|é🚀"},
	}
	s.trustKinds(ctx, tasks)
	list := taskTrustList(t, s)
	if len(list.Kinds) != len(tasks) {
		t.Fatalf("legacy client received %d rows, want %d", len(list.Kinds), len(tasks))
	}
	titles := make(map[string]bool)
	for _, kind := range list.Kinds {
		title := path.Base(kind.Cwd) // no slash: the same projection as NSString.lastPathComponent
		decoded, err := url.PathUnescape(kind.Cwd)
		if !kind.Ambiguous || kind.Machine != "" || strings.Contains(kind.Cwd, "/") || title != kind.Cwd || err != nil || decoded != kind.Key {
			t.Errorf("key lost in legacy title: %+v, title %q, decoded %q, error %v", kind, title, decoded, err)
		}
		if titles[title] {
			t.Errorf("duplicate legacy title %q", title)
		}
		titles[title] = true
		if w := taskTrustRequest(s, http.MethodDelete, kind.Key, roleFull); w.Code != http.StatusOK {
			t.Errorf("delete exact key %q: %d %s", kind.Key, w.Code, w.Body.String())
		}
	}
	for _, task := range tasks {
		if s.planStartsAtOnce(ctx, []PlanTask{task}) {
			t.Errorf("deleted key still admits %q", trustKey(task))
		}
	}
}

func TestTaskTrustHTTPFailedReadAndRecovery(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	task := PlanTask{Machine: "example", Cwd: "/work"}
	s.trustKinds(ctx, []PlanTask{task})
	before := storedTrust(t, st)
	st.failKey(trustSetting, errors.New("synthetic storage failure"))
	w := taskTrustRequest(s, http.MethodGet, "", roleFull)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "synthetic storage failure") {
		t.Errorf("GET on failed read: %d %s", w.Code, w.Body.String())
	}
	if s.planStartsAtOnce(ctx, []PlanTask{task}) {
		t.Error("failed trust read admitted an edit task")
	}
	read := task
	read.Readonly = true
	if !s.planStartsAtOnce(ctx, []PlanTask{read}) {
		t.Error("failed trust read changed read-only auto-admission")
	}
	s.trustKinds(ctx, []PlanTask{{Machine: "example", Cwd: "/new"}})
	if w := taskTrustRequest(s, http.MethodDelete, trustKey(task), roleFull); w.Code != http.StatusServiceUnavailable {
		t.Errorf("DELETE on failed read: %d %s", w.Code, w.Body.String())
	}
	// The error reaches apierr intact, so its timeout classification is not lost behind a generic wrapper.
	st.failKey(trustSetting, context.DeadlineExceeded)
	w = taskTrustRequest(s, http.MethodGet, "", roleFull)
	var failure struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || w.Code != http.StatusServiceUnavailable || failure.Code != "timeout" || failure.Error != context.DeadlineExceeded.Error() {
		t.Errorf("timeout semantics: %d %s (decode: %v)", w.Code, w.Body.String(), err)
	}
	st.failKey(trustSetting, nil)
	if after := storedTrust(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed read overwrote rules: before %q after %q", before, after)
	}
	if got := taskTrustList(t, s); len(got.Kinds) != 1 || got.Kinds[0].Key != trustKey(task) {
		t.Fatalf("retry did not recover rule: %+v", got)
	}
	if !s.planStartsAtOnce(ctx, []PlanTask{task}) {
		t.Fatal("recovered rule no longer admits edit task")
	}
}

func TestTaskTrustHTTPBadAndMissingValues(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	if got := taskTrustList(t, s); got.Kinds == nil || len(got.Kinds) != 0 {
		t.Fatalf("unset setting should be an empty list: %+v", got)
	}
	// The ordinary unknown-mode record was historically displayed as edit; retain that legacy behavior.
	keys := []string{"example|/work|read", "example|/work|old", "missing-separators", "example|/bad|extra|unknown"}
	if err := st.PutSetting(ctx, trustSetting, keys); err != nil {
		t.Fatal(err)
	}
	got := taskTrustList(t, s)
	if len(got.Kinds) != 2 || got.Kinds[0].Key != keys[1] || got.Kinds[0].Readonly || got.Kinds[1].Key != keys[0] || !got.Kinds[1].Readonly {
		t.Fatalf("legacy/malformed keys: %+v", got)
	}
	st.mu.Lock()
	st.vals[trustSetting] = []byte(`{"not":"a list"}`)
	st.mu.Unlock()
	if w := taskTrustRequest(s, http.MethodGet, "", roleFull); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("bad stored value: %d %s", w.Code, w.Body.String())
	}
	if w := taskTrustRequest(s, http.MethodDelete, keys[0], roleFull); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete bad stored value: %d %s", w.Code, w.Body.String())
	}
	st.mu.Lock()
	raw := string(st.vals[trustSetting])
	st.mu.Unlock()
	if raw != `{"not":"a list"}` {
		t.Fatalf("failed operations overwrote the stored value: %s", raw)
	}
	if err := st.PutSetting(ctx, trustSetting, keys); err != nil {
		t.Fatal(err)
	}
	if len(taskTrustList(t, s).Kinds) != 2 {
		t.Fatal("valid data did not recover after a bad stored value")
	}
}

func TestTaskTrustHTTPWithoutStore(t *testing.T) {
	ctx := context.Background()
	s := newStorelessService(t, newFakeHub())
	edit := PlanTask{Machine: "example", Cwd: "/work"}
	s.trustKinds(ctx, []PlanTask{edit})
	if got := taskTrustList(t, s); got.Kinds == nil || len(got.Kinds) != 0 {
		t.Fatalf("no-store GET: %+v", got)
	}
	if s.planStartsAtOnce(ctx, []PlanTask{edit}) {
		t.Fatal("no-store edit rule unexpectedly admitted")
	}
	if got := taskTrustRequest(s, http.MethodDelete, trustKey(edit), roleFull).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("no-store DELETE: %d", got)
	}
}

func TestTaskTrustHTTPConcurrentListAndWrites(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := newServiceOn(t, newFakeHub(), st)
	old := PlanTask{Machine: "example", Cwd: "/old|dir"}
	s.trustKinds(ctx, []PlanTask{old})
	const count = 12
	start := make(chan struct{})
	done := make(chan error, count+2)
	for i := range count {
		go func() {
			<-start
			s.trustKinds(ctx, []PlanTask{{Machine: "example", Cwd: fmt.Sprintf("/work|%02d", i)}})
			done <- nil
		}()
	}
	go func() {
		<-start
		w := taskTrustRequest(s, http.MethodDelete, trustKey(old), roleFull)
		if w.Code != http.StatusOK {
			done <- fmt.Errorf("delete: %d %s", w.Code, w.Body.String())
			return
		}
		done <- nil
	}()
	go func() {
		<-start
		for range count {
			w := taskTrustRequest(s, http.MethodGet, "", roleFull)
			var response taskTrustResponse
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				done <- fmt.Errorf("list: %d %s", w.Code, w.Body.String())
				return
			}
			for _, row := range response.Kinds {
				if !row.Ambiguous || row.Cwd != url.PathEscape(row.Key) || row.Machine != "" {
					done <- fmt.Errorf("bad concurrent row: %+v", row)
					return
				}
			}
		}
		done <- nil
	}()
	close(start)
	for range count + 2 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	got := taskTrustList(t, s)
	if len(got.Kinds) != count {
		t.Fatalf("concurrent writes lost rules: %+v", got)
	}
	for i := range count {
		key := trustKey(PlanTask{Machine: "example", Cwd: fmt.Sprintf("/work|%02d", i)})
		if !slices.Contains(storedTrust(t, st), key) {
			t.Errorf("missing %q", key)
		}
	}
}
