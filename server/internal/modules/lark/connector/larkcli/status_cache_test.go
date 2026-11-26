package larkcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/connectorwire"
)

func statusSpawns(t *testing.T, log string) (version, auth int) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case strings.Contains(line, "--version"):
			version++
		case strings.Contains(line, "auth status"):
			auth++
		}
	}
	return version, auth
}

func TestRepeatedStatusSpawnsLarkCLIOnce(t *testing.T) {
	log := filepath.Join(t.TempDir(), "spawns.log")
	t.Setenv("LARKCLI_FAKE_LOG", log)
	c := testConnector(t, "success")
	for range 10 {
		if st := c.Status(context.Background()); st.State != connectorwire.StateOK {
			t.Fatalf("status = %+v", st)
		}
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if st := c.Status(context.Background()); st.State != connectorwire.StateOK {
				t.Errorf("status = %+v", st)
			}
		})
	}
	wg.Wait()
	if version, auth := statusSpawns(t, log); version != 1 || auth != 1 {
		t.Fatalf("20 status reads spawned --version %d and auth status %d times, want 1 and 1", version, auth)
	}
}

func TestStatusShowsSignInRightAfterLogin(t *testing.T) {
	c := testConnector(t, "not_logged_in")
	ctx := context.Background()
	if st := c.Status(ctx); st.State != connectorwire.StateNotSignedIn {
		t.Fatalf("before login: status = %+v", st)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	if _, err := c.LoginStep(ctx, "", nil); err != nil {
		t.Fatal(err)
	}
	if p := waitForLogin(t, c); !p.Done || p.Error != "" {
		t.Fatalf("login prompt = %+v", p)
	}
	if st := c.Status(ctx); st.State != connectorwire.StateOK {
		t.Fatalf("after login: status = %+v", st)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "not_logged_in")
	if st := c.Status(ctx); st.State != connectorwire.StateNotSignedIn {
		t.Fatalf("after logout: status = %+v", st)
	}
}
