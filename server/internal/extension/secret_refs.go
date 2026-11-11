package extension

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
)

// References in an extension's secrets (docs/extensions/secret-providers.md "References in an extension's secrets"):
// the owner saves op://vault/item/field instead of a value, and the manager resolves it through the provider whose
// scheme it is each time it hands the extension its secrets. The reference is kept sealed in the same row a value would
// be, behind refMarker, which no value the settings take can start with (a secret is typed text: no NUL).

const refMarker = "\x00persona-ref\x00"

// SealedReference is what is stored for a reference.
func SealedReference(ref string) string { return refMarker + ref }

// ReferenceOf is the reference a stored secret holds; false for a value.
func ReferenceOf(stored string) (string, bool) {
	return strings.CutPrefix(stored, refMarker)
}

// SplitSecrets parts an extension's stored secrets into values and references (by name).
func SplitSecrets(stored map[string]string) (values, refs map[string]string) {
	values, refs = map[string]string{}, map[string]string{}
	for name, s := range stored {
		if ref, ok := ReferenceOf(s); ok {
			refs[name] = ref
		} else {
			values[name] = s
		}
	}
	return values, refs
}

// noteReferences keeps which schemes extension id's secrets reference (an index: who to apply again when a provider
// changes, who a provider lists as using its references). Called with every read of its secrets.
func (r *Registry) noteReferences(id string, stored map[string]string) {
	var schemes []string
	for _, s := range stored {
		if ref, ok := ReferenceOf(s); ok {
			if scheme, ok := ReferenceScheme(ref); ok && !slices.Contains(schemes, scheme) {
				schemes = append(schemes, scheme)
			}
		}
	}
	sort.Strings(schemes)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refSchemes == nil {
		r.refSchemes = map[string][]string{}
	}
	if len(schemes) == 0 {
		delete(r.refSchemes, id)
	} else {
		r.refSchemes[id] = schemes
	}
}

// ReferenceHolders are the extensions whose secrets reference any of schemes, by id, sorted.
func (r *Registry) ReferenceHolders(schemes []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id, held := range r.refSchemes {
		for _, s := range held {
			if slices.Contains(schemes, s) {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// SecretReference is one of an extension's secrets that holds a reference, as its provider lists it.
type SecretReference struct {
	Extension, Name, Reference string
}

// ReferencesTo are the references to schemes held in the installed extensions' secrets: what a provider's details
// list as its users.
func (r *Registry) ReferencesTo(ctx context.Context, schemes []string) []SecretReference {
	var out []SecretReference
	for _, id := range r.ReferenceHolders(schemes) {
		stored, err := r.Secrets(ctx, id)
		if err != nil {
			continue
		}
		_, refs := SplitSecrets(stored)
		for _, name := range sortedKeys(refs) {
			if scheme, ok := ReferenceScheme(refs[name]); ok && slices.Contains(schemes, scheme) {
				out = append(out, SecretReference{Extension: id, Name: name, Reference: refs[name]})
			}
		}
	}
	return out
}

// SetSecretReference keeps a reference as one of a plugin's declared secrets: the owner's standing approval for this
// extension to be handed that secret's value whenever it starts. Refused for text that is not a reference, a scheme no
// enabled extension supplies, and the extension's own scheme (it cannot supply its own secrets).
func (r *Registry) SetSecretReference(ctx context.Context, id, name, ref, actor string) error {
	scheme, ok := ReferenceScheme(ref)
	if !ok {
		return i18n.Err("extension-status.secret_not_reference")
	}
	p := r.Get(id)
	if p != nil && slices.Contains(p.Full.Schemes(), scheme) {
		return i18n.Err("extension-status.secret_own_scheme", i18n.Args{"scheme": scheme + "://"})
	}
	if _, ok := r.SecretProviderFor(scheme); !ok {
		return i18n.Err("extension-status.secret_no_provider", i18n.Args{"scheme": scheme + "://"})
	}
	return r.setSecret(ctx, id, name, SealedReference(ref), actor, map[string]any{"name": name, "reference": ref})
}
