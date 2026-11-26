package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestExternalSourceRoutesRequireFullCredentials(t *testing.T) {
	registered := routePatterns(t)
	for _, pattern := range []string{
		assistantwire.ExternalBindingsListRoute, assistantwire.ExternalBindingsCreateRoute,
		assistantwire.ExternalBindingsUpdateRoute, assistantwire.ExternalBindingsDeleteRoute,
		assistantwire.ExternalBindingsLookupRoute, assistantwire.ExternalBindingsSummaryRoute,
		assistantwire.ExternalPoliciesReadRoute, assistantwire.ExternalPoliciesWriteRoute, assistantwire.ExternalPolicyEffectiveRoute,
	} {
		if !slices.Contains(registered, pattern) {
			t.Errorf("%s is not relayed", pattern)
		}
		if scope := extwire.RouteScopes[pattern]; scope != "" {
			t.Errorf("%s grants extension scope %q", pattern, scope)
		}
		method, path, _ := strings.Cut(pattern, " ")
		for _, role := range []string{roleAnswer, roleLink, rolePeer, roleLinkPair, rolePeerPair} {
			if allowed(principal{Role: role}, method, strings.ReplaceAll(path, "{id}", "fixture")) {
				t.Errorf("%s can call %s", role, pattern)
			}
		}
	}
}

func TestExternalSourcePolicyRelaysToManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	got := make(chan assistantwire.HTTPRequest, 1)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return struct {
				Tools []string `json:"tools"`
			}{Tools: []string{}}, nil
		}
		var r assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &r) != nil {
			return nil, errors.New("unexpected route dispatch")
		}
		got <- r
		return assistantwire.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"policy":{}}`)}, nil
	})
	query := url.Values{"external_ref": {`{"provider":"lark"}`}}.Encode()
	body := `{"mode":"off"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+assistantwire.ExternalPoliciesPath+"?"+query, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("policy update: %d", res.StatusCode)
	}
	select {
	case r := <-got:
		if r.Method != http.MethodPut || r.Path != assistantwire.ExternalPoliciesPath || r.Query != query || string(r.Body) != body ||
			!slices.Equal(r.Header[assistantwire.RoleHeader], []string{roleFull}) {
			t.Fatalf("unexpected request relayed to the manager: %+v", r)
		}
	default:
		t.Fatal("no request reached the manager")
	}
}
