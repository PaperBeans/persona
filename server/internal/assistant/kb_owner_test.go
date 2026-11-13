package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
)

func ownerTestService(t *testing.T, dir, keyDir string) *Service {
	t.Helper()
	f := newFakeHub().withMachine()
	f.hello.Owner = "示例用户"
	f.hello.KB = &KBSettings{}
	f.answerApp(AppConnectors, assistantwire.ConnectorsOwner, func(json.RawMessage) (any, error) {
		return assistantwire.KBOwnerSources{Sources: []assistantwire.KBOwnerSource{}}, nil
	})
	s := New(Options{Hub: f, KB: &KBOptions{Dir: dir, KeyDir: keyDir}, Logf: testLogf(t)})
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	t.Cleanup(s.kbEnd)
	return s
}

func TestKBOwnerEncryptedIdentitiesAndForgetting(t *testing.T) {
	dir, keyDir := t.TempDir(), t.TempDir()
	s := ownerTestService(t, dir, keyDir)
	e := kbEng(t, s)
	shared := kb.Handle{Kind: kb.HandleEmail, Value: "OWNER@EXAMPLE.COM"}
	phone := kb.Handle{Kind: kb.HandlePhone, Value: "+1 (202) 555-0100"}
	for _, src := range []string{"alpha", "beta"} {
		handles := []kb.Handle{shared}
		if src == "alpha" {
			handles = append(handles, phone)
		}
		_, err := e.Ingest(context.Background(), kbConnectorSource{id: src}, kb.SourceChat{ID: "chat", Name: "示例会话"}, []kb.SourceMessage{{ID: "mine", SenderID: "self-" + src, SenderName: "自动发现的名字", Mine: true, SenderHandles: handles, Text: "hello", At: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
	}
	setSettings(t, s, func(v *Settings) { v.OwnerAliases = []string{"  手工别名  ", "手工别名"} })
	owner, err := s.kbOwner(context.Background())
	if err != nil || owner.Name != "示例用户" || !slices.Equal(owner.Aliases, []string{"手工别名"}) || !slices.Contains(owner.Handles, kb.Handle{Kind: kb.HandleEmail, Value: "owner@example.com"}) || !slices.Contains(owner.Handles, kb.Handle{Kind: kb.HandlePhone, Value: "+12025550100"}) {
		t.Fatalf("owner: %+v %v", owner, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ownerIdentitiesFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"owner@example.com", "OWNER@EXAMPLE.COM", "self-alpha", "+12025550100"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("plaintext identity on disk: %q", private)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, ownerIdentitiesFile))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %o", info.Mode().Perm())
	}
	if len(e.Store.People()) != 0 {
		t.Fatal("the owner became an ordinary person")
	}
	detail, err := e.Get(kb.KindChat, "alpha-chat", 2000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(detail)
	if strings.Contains(string(b), "owner@example.com") || strings.Contains(string(b), "self-alpha") {
		t.Fatalf("raw owner identity leaked through ordinary get: %s", b)
	}
	if hits := e.Search("owner@example.com", true, 10); len(hits) != 0 {
		t.Fatalf("encrypted identities entered search: %+v", hits)
	}
	s.kbClose()
	reopened := ownerTestService(t, dir, keyDir)
	before, err := reopened.kbOwner(context.Background())
	if err != nil || len(before.Handles) != len(owner.Handles) {
		t.Fatalf("restart: %+v %v", before, err)
	}
	if _, err := reopened.kbForget(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.kbOwner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(after.Handles, kb.Handle{Kind: kb.HandleEmail, Value: "owner@example.com"}) || slices.Contains(after.Handles, kb.Handle{Kind: kb.HandlePhone, Value: "+12025550100"}) || slices.Contains(after.Handles, kb.Handle{Kind: "alpha", Value: "self-alpha"}) {
		t.Fatalf("source provenance: %+v", after)
	}
	if _, err := reopened.kbForget(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}
	final, err := reopened.kbOwner(context.Background())
	if err != nil || len(final.Handles) != 0 {
		t.Fatalf("all forgotten: %+v %v", final, err)
	}
}

func TestKBOwnerWithoutKeyIsMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	s := ownerTestService(t, dir, "")
	_ = kbEng(t, s)
	s.kbObserveOwner("example", "", []kb.Handle{{Kind: kb.HandleGitHub, Value: "example-user"}})
	owner, err := s.kbOwner(context.Background())
	if err != nil || len(owner.Handles) != 1 {
		t.Fatalf("memory: %+v %v", owner, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ownerIdentitiesFile)); !os.IsNotExist(err) {
		t.Fatalf("identity file without key: %v", err)
	}
	s.kbClose()
	fresh := ownerTestService(t, dir, "")
	owner, err = fresh.kbOwner(context.Background())
	if err != nil || len(owner.Handles) != 0 {
		t.Fatalf("restart without key: %+v %v", owner, err)
	}
}

func TestKBOwnerRejectsCorruptCiphertext(t *testing.T) {
	for _, mode := range []string{"corrupt", "wrong-key", "missing-key"} {
		t.Run(mode, func(t *testing.T) {
			dir, keyDir := t.TempDir(), t.TempDir()
			s := ownerTestService(t, dir, keyDir)
			_ = kbEng(t, s)
			s.kbObserveOwner("example", "", []kb.Handle{{Kind: "example", Value: "CaseSensitive-ID"}})
			first, err := s.kbOwner(context.Background())
			if err != nil || !slices.Contains(first.Handles, kb.Handle{Kind: "example", Value: "CaseSensitive-ID"}) {
				t.Fatalf("opaque id changed: %+v %v", first, err)
			}
			s.kbClose()
			path := filepath.Join(dir, ownerIdentitiesFile)
			raw, _ := os.ReadFile(path)
			if mode == "corrupt" {
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "wrong-key" {
				if err := os.WriteFile(filepath.Join(keyDir, "handles.key"), bytes.Repeat([]byte{42}, 32), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-key" {
				keyDir = ""
			}
			fresh := ownerTestService(t, dir, keyDir)
			e := kbEng(t, fresh)
			if _, err := e.Apply(kb.KindProject, "ordinary-project", kb.Update{Goal: "ordinary KB remains available"}); err != nil {
				t.Fatalf("identity error blocked KB writes: %v", err)
			}
			if _, err := e.Get(kb.KindProject, "ordinary-project", 0); err != nil {
				t.Fatalf("identity error blocked KB reads: %v", err)
			}
			if _, err := fresh.kbOwner(context.Background()); err == nil {
				t.Fatal("invalid ciphertext silently accepted")
			}
			fresh.kbObserveOwner("example", "", []kb.Handle{{Kind: "example", Value: "new-id"}})
			if err := fresh.kbForgetOwner("example"); err == nil {
				t.Fatal("invalid identities silently forgotten")
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, raw) {
				t.Fatal("invalid ciphertext was overwritten")
			}
		})
	}
}

func TestKBOwnerLiveMailAndEditableAliases(t *testing.T) {
	s := ownerTestService(t, t.TempDir(), "")
	f := s.hub.(*fakeHub)
	f.answerApp(AppConnectors, assistantwire.ConnectorsOwner, func(json.RawMessage) (any, error) {
		return assistantwire.KBOwnerSources{Sources: []assistantwire.KBOwnerSource{{Source: "mail", Handles: []kb.Handle{{Kind: kb.HandleEmail, Value: "MAIL@EXAMPLE.COM"}}}}}, nil
	})
	put := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/assistant", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
		}
	}
	put(`{"owner_aliases":[" Alias ","Alias",""]}`)
	put(`{"notes":"updated"}`)
	owner, err := s.kbOwner(context.Background())
	if err != nil || !slices.Equal(owner.Aliases, []string{"Alias"}) || !slices.Equal(owner.Handles, []kb.Handle{{Kind: kb.HandleEmail, Value: "mail@example.com"}}) {
		t.Fatalf("live owner: %+v %v", owner, err)
	}
	put(`{"owner_aliases":[]}`)
	owner, err = s.kbOwner(context.Background())
	if err != nil || len(owner.Aliases) != 0 {
		t.Fatalf("aliases not cleared: %+v %v", owner, err)
	}
	p, _ := json.Marshal(s.profile(context.Background(), []TaskView{}, &TodoReport{}))
	if bytes.Contains(p, []byte("mail@example.com")) {
		t.Fatal("profile exposes raw identity")
	}
	out, ok, err := s.kbRequest(context.Background(), assistantwire.MethodKBOwner, json.RawMessage(`{}`))
	if !ok || err != nil {
		t.Fatalf("rpc: %+v %v %v", out, ok, err)
	}
	if _, _, err := s.kbRequest(context.Background(), assistantwire.MethodKBOwner, json.RawMessage(`{"aliases":["other"]}`)); err == nil {
		t.Fatal("owner read accepted a mutation")
	}
}
