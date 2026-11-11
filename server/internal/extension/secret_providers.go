package extension

import (
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Secret providers (docs/extensions/secret-providers.md): an extension's secret_providers contribution supplies the
// values of references of its schemes (op://vault/item/field). The registry only says which enabled extension claims
// a scheme; resolving is the manager's (extmgr/secret_providers.go).

// ReferenceMost is the longest reference a setting may hold.
const ReferenceMost = 1024

// SecretProvider is the contribution that resolves references of one scheme.
type SecretProvider struct {
	Extension, Contribution string
	// Name is the contribution's name (1Password), for "From 1Password: op://…".
	Name string
}

// ReferenceScheme is the scheme of a reference ("op" for op://vault/item/field); false for text that cannot be one
// (no scheme, a reserved one such as https, nothing after ://, too long, a control character).
func ReferenceScheme(ref string) (string, bool) {
	i := strings.Index(ref, "://")
	if i <= 0 || i+3 == len(ref) || len(ref) > ReferenceMost || strings.ContainsFunc(ref, unicode.IsControl) {
		return "", false
	}
	scheme := ref[:i]
	if !schemePattern.MatchString(scheme) || slices.Contains(reservedSchemes, scheme) {
		return "", false
	}
	return scheme, true
}

// SecretProviderFor is the enabled extension (without a problem, the contribution switched on) that claims scheme.
// The install refuses a second claim, so there is at most one.
func (r *Registry) SecretProviderFor(scheme string) (SecretProvider, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.extensions))
	for id := range r.extensions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := r.extensions[id]
		if !p.Record.Enabled || p.Problem != "" || p.Manifest == nil {
			continue
		}
		for _, it := range p.Manifest.SecretProviders() {
			if slices.Contains(it.Schemes, scheme) {
				return SecretProvider{Extension: id, Contribution: it.ID, Name: it.Name}, true
			}
		}
	}
	return SecretProvider{}, false
}
