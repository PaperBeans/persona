package herdr

import (
	"context"
	"sync"
	"testing"
)

// An Agent's terminal starts with its marker variables: tab.create and workspace.create carry them as herdr's `env`
// object (#10111).
func TestCreateCarriesEnv(t *testing.T) {
	var mu sync.Mutex
	got := map[string]map[string]any{}
	sock := fakeServer(t, func(method string, params map[string]any) string {
		mu.Lock()
		got[method] = params
		mu.Unlock()
		return `"result":{"type":"tab_created","tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}`
	})
	c := &Client{Bin: "/nonexistent/herdr", Socket: sock}
	if _, err := c.TabCreate(context.Background(), "w1", "/tmp", "agent", "PERSONA_AGENT=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WorkspaceCreate(context.Background(), "/tmp", "tmp", "PERSONA_AGENT=1"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"tab.create", "workspace.create"} {
		env, _ := got[m]["env"].(map[string]any)
		if env["PERSONA_AGENT"] != "1" {
			t.Fatalf("%s params %v: no PERSONA_AGENT in env", m, got[m])
		}
	}
	// Without variables nothing changes in the request.
	if _, err := c.TabCreate(context.Background(), "w1", "/tmp", "shell"); err != nil {
		t.Fatal(err)
	}
	if _, has := got["tab.create"]["env"]; has {
		t.Fatalf("a plain tab got env: %v", got["tab.create"])
	}
}

// A herdr without environment support refuses the field; the tab still opens, without the variables.
func TestCreateRetriesWithoutEnvWhenRefused(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	sock := fakeServer(t, func(method string, params map[string]any) string {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, params)
		if _, has := params["env"]; has {
			return `"error":{"code":"invalid_request","message":"unknown field ` + "`env`" + `"}`
		}
		return `"result":{"type":"tab_created","tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}`
	})
	c := &Client{Bin: "/nonexistent/herdr", Socket: sock}
	created, err := c.TabCreate(context.Background(), "w1", "/tmp", "agent", "PERSONA_AGENT=1")
	if err != nil || created.RootPane.ID != "w1:p2" || len(calls) != 2 {
		t.Fatalf("created %+v err %v after %d calls", created, err, len(calls))
	}
}
