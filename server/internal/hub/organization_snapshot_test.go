package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestOrganizationSnapshotRouteIsFullCredentialsOnly(t *testing.T) {
	pattern := assistantwire.OrganizationSnapshotRoute
	if !slices.Contains(routePatterns(t), pattern) {
		t.Fatalf("%s is not relayed", pattern)
	}
	if scope := extwire.RouteScopes[pattern]; scope != "" {
		t.Fatalf("organization snapshot gained extension scope %q", scope)
	}
	data, err := os.ReadFile("testdata/route_scopes.txt")
	if err != nil {
		t.Fatal(err)
	}
	line := "-                    " + pattern
	if !strings.Contains(string(data), line) {
		t.Errorf("route_scopes.txt lacks %q", line)
	}
	_, path, _ := strings.Cut(pattern, " ")
	for _, role := range []string{roleAnswer, roleLink, roleLinkPair, rolePeer, rolePeerPair} {
		if allowed(principal{Role: role}, http.MethodGet, path) {
			t.Errorf("%s may read the organization snapshot", role)
		}
	}
}

func TestOrganizationSnapshotRelaysFullRoleAndRejectsWrites(t *testing.T) {
	h, srv, _ := newBusHub(t)
	got := make(chan assistantwire.HTTPRequest, 2)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return map[string]any{"tools": []any{}}, nil
		}
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected " + method)
		}
		got <- req
		return assistantwire.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"relayed":true}`)}, nil
	})
	request, err := http.NewRequest(http.MethodGet, srv.URL+assistantwire.OrganizationSnapshotPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	// A client cannot elevate itself by forging the manager's role header.
	request.Header.Set(assistantwire.RoleHeader, "extension")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != `{"relayed":true}` {
		t.Fatalf("GET: %d %s", response.StatusCode, body)
	}
	forwarded := <-got
	if forwarded.Method != http.MethodGet || forwarded.Path != assistantwire.OrganizationSnapshotPath ||
		len(forwarded.Header[assistantwire.RoleHeader]) != 1 || forwarded.Header[assistantwire.RoleHeader][0] != roleFull {
		t.Fatalf("manager received %+v", forwarded)
	}

	request, err = http.NewRequest(http.MethodPost, srv.URL+assistantwire.OrganizationSnapshotPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", response.StatusCode)
	}
	select {
	case unexpected := <-got:
		t.Fatalf("write reached the manager: %+v", unexpected)
	default:
	}
}
