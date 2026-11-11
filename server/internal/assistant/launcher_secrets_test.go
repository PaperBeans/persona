package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// fakeLauncherSecrets is a provider extension for exv:// references: values by reference, every resolution noted.
type fakeLauncherSecrets struct {
	mu       sync.Mutex
	values   map[string]string
	resolved []string // reference|launcher|machine
	forgot   []string
}

func (f *fakeLauncherSecrets) LauncherSecret(_ context.Context, ref, launcher, machine string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, ref+"|"+launcher+"|"+machine)
	if v, ok := f.values[ref]; ok {
		return v, nil
	}
	return "", i18n.Err("hub-extension_secrets.not_found", i18n.Args{"provider": "Example Vault"})
}

func (f *fakeLauncherSecrets) ForgetLauncherSecrets(launcher string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot = append(f.forgot, launcher)
}

func (f *fakeLauncherSecrets) SecretProviderOf(ref string) (extension.SecretProvider, bool) {
	if !strings.HasPrefix(ref, "exv://") {
		return extension.SecretProvider{}, false
	}
	return extension.SecretProvider{Extension: "example-vault", Contribution: "vault", Name: "Example Vault"}, true
}

func (f *fakeLauncherSecrets) SecretReference(_ context.Context, ref string) *extmgr.SecretReferenceView {
	return &extmgr.SecretReferenceView{Provider: "example-vault", ProviderName: "Example Vault", Reference: ref, Title: "Item › " + ref[strings.LastIndex(ref, "/")+1:]}
}

func (f *fakeLauncherSecrets) noted() (resolved, forgot []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.resolved), slices.Clone(f.forgot)
}

const (
	refMainValue = "sk-test-from-vault-main"
	refMacValue  = "sk-test-from-vault-mac"
)

// A provider's key, and one computer's key for it, saved as references: checked on save, resolved for the computer
// that pulls it, shown as the reference and what it names, never handed to a node from before the catalog.
func TestLauncherKeyReferences(t *testing.T) {
	s, _ := scopeService(t)
	vault := &fakeLauncherSecrets{values: map[string]string{"exv://Example/item/ok": refMainValue, "exv://Example/item/mac": refMacValue}}
	s.refs = vault
	addProvider(t, s, `{"name":"anchor","base_url":"https://gw.example.com","model":"m1","api_key":"sk-test-anchor-000"}`)

	words := func(key string, args i18n.Args) string { return i18n.T(i18n.Default, key, args) }
	for body, want := range map[string]string{
		`{"name":"gw","base_url":"https://gw.example.com","model":"m1","api_key":"sk-test-x","api_key_reference":"exv://Example/item/ok"}`: words("assistant-models_launchers.err_key_value_or_reference", nil),
		`{"name":"gw","base_url":"https://gw.example.com","model":"m1","api_key_reference":"nop://Example/item/ok"}`:                       words("hub-extension_secrets.no_provider", i18n.Args{"scheme": "nop://"}),
		`{"name":"gw","base_url":"https://gw.example.com","model":"m1","api_key_reference":"exv://Example/item/missing"}`:                  words("hub-extension_secrets.not_found", i18n.Args{"provider": "Example Vault"}),
		`{"name":"gw","base_url":"https://gw.example.com","model":"m1","api_key_reference":"https://example.com/key"}`:                     words("hub-extension_secrets.not_reference", nil),
	} {
		if r := request(s, "POST", "/api/v1/launchers", body); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), want) {
			t.Fatalf("%s: want 400 with %q, got %d %s", body, want, r.Code, r.Body)
		}
	}

	v := addProvider(t, s, `{"name":"gw","base_url":"https://gw.example.com","model":"m1","api_key_reference":"exv://Example/item/ok","own_service":"use"}`)
	if !v.HasKey || v.KeyReference == nil || v.KeyReference.Reference != "exv://Example/item/ok" || v.KeyReference.ProviderName != "Example Vault" || v.KeyReference.Title != "Item › ok" {
		t.Fatalf("the view says where the key comes from: %+v %+v", v, v.KeyReference)
	}
	if resolved, _ := vault.noted(); !slices.Contains(resolved, "exv://Example/item/ok|gw|") {
		t.Fatalf("checked on save: %v", resolved)
	}

	if l, err := pull(t, s, "mac", "gw", nil); err != nil || l.APIKey != refMainValue {
		t.Fatalf("mac pulls the value: %q %v", l.APIKey, err)
	}
	if resolved, _ := vault.noted(); !slices.Contains(resolved, "exv://Example/item/ok|gw|mac") {
		t.Fatalf("resolved for the computer that pulled it: %v", resolved)
	}

	// One computer's key as a reference; both maps for one computer are refused.
	if r := request(s, "PUT", "/api/v1/launchers/gw", `{"base_url":"https://gw.example.com","model":"m1","machine_keys":{"mac":"k"},"machine_key_references":{"mac":"exv://Example/item/mac"}}`); r.Code != http.StatusBadRequest {
		t.Fatalf("a key and a reference for mac: %d %s", r.Code, r.Body)
	}
	r := request(s, "PUT", "/api/v1/launchers/gw", `{"base_url":"https://gw.example.com","model":"m1","machine_key_references":{"mac":"exv://Example/item/mac"}}`)
	if r.Code != 200 {
		t.Fatalf("mac's key as a reference: %d %s", r.Code, r.Body)
	}
	if body := r.Body.String(); strings.Contains(body, "sk-test-from-vault") || !strings.Contains(body, `"key_machines":["mac"]`) ||
		!strings.Contains(body, `"machine_key_references":{"mac":{"provider":"example-vault"`) || !strings.Contains(body, `"key_reference":{`) {
		t.Fatalf("view: %s", body)
	}
	if _, forgot := vault.noted(); !slices.Contains(forgot, "gw") {
		t.Fatalf("a save drops what was kept of its keys: %v", forgot)
	}
	if l, _ := pull(t, s, "mac", "gw", nil); l.APIKey != refMacValue {
		t.Fatalf("mac's own: %q", l.APIKey)
	}
	if l, _ := pull(t, s, "server", "gw", nil); l.APIKey != refMainValue {
		t.Fatalf("server's: %q", l.APIKey)
	}
	for _, path := range []string{"/api/v1/launchers", "/api/v1/launchers/gw"} {
		if r := request(s, "GET", path, ""); strings.Contains(r.Body.String(), "sk-test-from-vault") || !strings.Contains(r.Body.String(), "exv://Example/item/mac") {
			t.Fatalf("%s: never a value, always the reference: %s", path, r.Body)
		}
	}

	// Kept sealed as the reference, never the value.
	stored, err := s.readStoredLaunchers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := s.readMachineKeys(context.Background())
	raw, _ := json.Marshal(map[string]any{"stored": stored, "keys": keys})
	if strings.Contains(string(raw), "sk-test-from-vault") || strings.Contains(string(raw), "exv://") {
		t.Fatalf("stored in the clear: %s", raw)
	}
	i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == "gw" })
	if ref, ok := s.keyReference(stored[i].KeySealed); !ok || ref != "exv://Example/item/ok" {
		t.Fatalf("sealed reference: %q %v", ref, ok)
	}
	if m, err := s.openStoredLauncher(stored[i]); err != nil || m.APIKey != "" {
		t.Fatalf("the stored provider opens without the reference in place of a key: %q %v", m.APIKey, err)
	}

	// A node from before the catalog is not handed it; one that pulls is.
	catalog, legacy, _ := s.nodeLaunchersFor(context.Background(), stored, keys, "mac")
	if !slices.ContainsFunc(catalog, func(l proto.ManagedLauncher) bool { return l.Name == "gw" }) ||
		slices.ContainsFunc(legacy, func(l proto.ManagedLauncher) bool { return l.Name == "gw" }) {
		t.Fatalf("catalog %v legacy %v", catalog, legacy)
	}

	// The provider extension's details list both.
	refs := s.launcherReferences(context.Background())
	if len(refs) != 2 || refs[0] != (extmgr.LauncherReference{Launcher: "gw", LauncherName: "gw", Reference: "exv://Example/item/ok"}) ||
		refs[1] != (extmgr.LauncherReference{Launcher: "gw", LauncherName: "gw", Machine: "mac", Reference: "exv://Example/item/mac"}) {
		t.Fatalf("launcher references: %+v", refs)
	}

	// The reference stops resolving: the pull refuses with the provider's words.
	vault.mu.Lock()
	delete(vault.values, "exv://Example/item/ok")
	vault.mu.Unlock()
	if _, err := pull(t, s, "server", "gw", nil); codeOf(err) != proto.CodeLauncherKeyUnresolved || !strings.Contains(err.Error(), words("hub-extension_secrets.not_found", i18n.Args{"provider": "Example Vault"})) {
		t.Fatalf("unresolved: %v", err)
	}

	// A key given replaces the reference.
	if r := request(s, "PUT", "/api/v1/launchers/gw", `{"base_url":"https://gw.example.com","model":"m1","api_key":"sk-test-plain-0000"}`); r.Code != 200 || strings.Contains(r.Body.String(), `"key_reference"`) {
		t.Fatalf("a plain key again: %d %s", r.Code, r.Body)
	}
	if l, _ := pull(t, s, "server", "gw", nil); l.APIKey != "sk-test-plain-0000" {
		t.Fatalf("plain: %q", l.APIKey)
	}

	if r := request(s, "DELETE", "/api/v1/launchers/gw", ""); r.Code != 200 {
		t.Fatalf("delete: %d %s", r.Code, r.Body)
	}
	if _, forgot := vault.noted(); len(forgot) < 3 {
		t.Fatalf("removed: its kept keys dropped: %v", forgot)
	}
	if refs := s.launcherReferences(context.Background()); len(refs) != 0 {
		t.Fatalf("a removed provider's references: %+v", refs)
	}
}

// The manager's own calls with a provider whose key is a reference resolve it for no computer.
func TestLauncherKeyReferenceForTheManager(t *testing.T) {
	s, _ := scopeService(t)
	vault := &fakeLauncherSecrets{values: map[string]string{"exv://Example/item/ok": refMainValue}}
	s.refs = vault
	addProvider(t, s, `{"kind":"api","api":"messages","name":"direct","base_url":"https://gw.example.com","model":"m1","api_key_reference":"exv://Example/item/ok"}`)
	c, err := s.apiConfig(context.Background(), ModelBinding{Provider: "launcher.direct", Model: "m1"})
	if err != nil || c.APIKey != refMainValue {
		t.Fatalf("the manager's call: %q %v", c.APIKey, err)
	}
	if resolved, _ := vault.noted(); resolved[len(resolved)-1] != "exv://Example/item/ok|direct|" {
		t.Fatalf("resolved for the manager: %v", resolved)
	}
	s.refs = nil // no extensions in this process: the reference does not open as a key
	if _, err := s.apiConfig(context.Background(), ModelBinding{Provider: "launcher.direct", Model: "m1"}); err == nil || !strings.Contains(err.Error(), "exv://") {
		t.Fatalf("no provider: %v", err)
	}
}
