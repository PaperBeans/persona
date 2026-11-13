package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeMigratedCodexApp writes the App's global state as the migrated App leaves it: the migration markers, but no
// projectless-thread-ids, no thread-project-assignments and no local-projects — its sidebar lives only in the state
// database (#6388).
func writeMigratedCodexApp(t *testing.T, home, dbSQL string) {
	t.Helper()
	sqlite := sqlite3WithJSON(t)
	state := `{"app-server-projects-migration-by-host": {"local": 1}}`
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sqlite, filepath.Join(home, "state_5.sqlite"), dbSQL).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	codexAppDBs.Lock()
	clear(codexAppDBs.byHome) // read the database again now, not after codexAppDBRefresh
	codexAppDBs.Unlock()
}

// A chat the migrated App writes only to the state database (no legacy sidebar keys) is still an App sidebar chat:
// listed as one, not left a read-only history fallback. An exec run in the same database is not a sidebar chat.
func TestCodexAppMigratedChatsAreListed(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	updated := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	writeMigratedCodexApp(t, home, codexAppSchema+
		codexAppRow(appMigrated, "", filepath.Join(project, "migrated"), "Migrated chat", updated, 0)+
		codexAppRow(appExec, "", project, "A run", updated, 0, "codex_exec"))

	rows := rowsByThread(n.codexSessions(map[string]bool{}))
	m, ok := rows[appMigrated]
	if !ok {
		t.Fatalf("the migrated chat is missing from Agents: %v", rows)
	}
	if m.AgentName != "Codex App" || m.SpaceKind != "codex_app" {
		t.Fatalf("the migrated chat is not an App row: %+v", m)
	}
	if m.Title != "Migrated chat" || m.Cwd != filepath.Join(project, "migrated") {
		t.Fatalf("the migrated chat = %+v", m)
	}
	if _, ok := rows[appExec]; ok {
		t.Fatal("an exec run is not an App sidebar chat")
	}
}

// A migrated App's project lives only in the database: a chat's project is found by its cwd there (the json's
// local-projects is gone), and a cwd outside every root falls back to the ungrouped row.
func TestCodexAppProjectFromMigratedDatabase(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeMigratedCodexApp(t, home, codexAppSchema+
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT);
		 CREATE TABLE project_roots (project_id TEXT, root TEXT);
		 INSERT INTO projects VALUES ('dbp1', 'Beta');
		 INSERT INTO project_roots VALUES ('dbp1', '`+root+`');`)
	app := loadCodexAppState(home)
	pid, name := app.project(appMigrated, filepath.Join(root, "migrated"))
	if pid != "dbp1" || name != "Beta" {
		t.Fatalf("project = %q %q, want the database project dbp1 Beta", pid, name)
	}
	if pid, _ := app.project(appMigrated, t.TempDir()); pid != "" {
		t.Fatalf("a cwd outside every root fell into %q, want the ungrouped fallback", pid)
	}
}
