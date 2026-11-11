package extension

import (
	"slices"
	"strings"
	"testing"
)

// secret_providers: the line on the install page and in an upgrade that adds one, the digest, and what an item and a
// reference may be.
func TestSecretProvidersPoint(t *testing.T) {
	contrib := func(items ...map[string]any) func(map[string]any) {
		return func(m map[string]any) { m["contributes"] = map[string]any{"secret_providers": items} }
	}
	item := map[string]any{"id": "vault", "name": "Example Vault", "schemes": []string{"exv"}}
	old, err := Parse(with(t, func(m map[string]any) {}))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(with(t, contrib(item)))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.SecretProviders(); len(got) != 1 || got[0].Name != "Example Vault" || !slices.Equal(m.Schemes(), []string{"exv"}) {
		t.Fatalf("%+v %v", got, m.Schemes())
	}
	has := func(lines []PermissionLine) bool {
		return slices.ContainsFunc(lines, func(l PermissionLine) bool {
			return l.Key == "secret_provider" && l.Title != "" && strings.Contains(l.Detail, "exv://")
		})
	}
	if !has(Explain(m)) || has(Explain(old)) || !has(Added(old, m)) || has(Added(m, m)) {
		t.Errorf("explain %+v added %+v", Explain(m), Added(old, m))
	}
	if Digest(old) == Digest(m) {
		t.Error("supplying secrets is agreed to: the digest changes")
	}
	plain, _ := Parse(with(t, func(m map[string]any) {}))
	if Digest(plain) != Digest(old) {
		t.Error("an extension without a provider keeps its digest")
	}

	for name, edit := range map[string]func(map[string]any){
		"no schemes":       func(it map[string]any) { it["schemes"] = []string{} },
		"five schemes":     func(it map[string]any) { it["schemes"] = []string{"a", "b", "c", "d", "e"} },
		"https":            func(it map[string]any) { it["schemes"] = []string{"https"} },
		"persona":          func(it map[string]any) { it["schemes"] = []string{"persona"} },
		"upper case":       func(it map[string]any) { it["schemes"] = []string{"OP"} },
		"with ://":         func(it map[string]any) { it["schemes"] = []string{"op://"} },
		"twice":            func(it map[string]any) { it["schemes"] = []string{"op", "op"} },
		"no name":          func(it map[string]any) { delete(it, "name") },
		"a field it lacks": func(it map[string]any) { it["hosts"] = []string{"example.com"} },
		"a bad id":         func(it map[string]any) { it["id"] = "Vault!" },
	} {
		it := map[string]any{}
		for k, v := range item {
			it[k] = v
		}
		edit(it)
		if _, err := Parse(with(t, contrib(it))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	noProgram, err := Parse(with(t, func(m map[string]any) { delete(m, "runtime"); contrib(item)(m) }))
	if err == nil && len(noProgram.SecretProviders()) > 0 {
		t.Error("a provider needs a program that speaks the protocol")
	}
}

func TestReferenceScheme(t *testing.T) {
	for ref, want := range map[string]string{
		"op://vault/item/field":  "op",
		"exv://Example/item/ok":  "exv",
		"op://":                  "",
		"https://example.com/x":  "",
		"persona://settings":     "",
		"vault/item/field":       "",
		"://x":                   "",
		"op://vault/item\nfield": "",
		"OP://vault/item/field":  "",
		"op://" + strings.Repeat("x", ReferenceMost): "",
	} {
		got, ok := ReferenceScheme(ref)
		if got != want || ok != (want != "") {
			t.Errorf("ReferenceScheme(%q) = %q %v, want %q", clipForTest(ref), got, ok, want)
		}
	}
}

func clipForTest(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
