package extmgr

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Launcher keys that are references (docs/extensions/secret-providers.md, "Launcher keys"): a launcher's key is
// opened every time a computer pulls it and every time the manager calls the provider's service itself, so a resolved
// value is kept here for a while instead of asking the provider each time. Only in this process's memory, never on
// disk, and only for launchers: an extension's secrets resolve at each start, an Agent's hand-off after each card.

// secretKeep is how long a resolved launcher key is kept.
var secretKeep = 10 * time.Minute

// keptSecrets are resolved launcher keys, by reference and the use they were resolved for.
type keptSecrets struct {
	mu   sync.Mutex
	kept map[keptKey]keptSecret
	// gen counts the forgets: a value resolved while one happened is not kept (its provider may have just gone).
	gen uint64
}

type keptKey struct{ reference, kind, id, machine string }

type keptSecret struct {
	value, provider string
	at              time.Time
}

// get is key's kept value, or the generation to put a fresh one with.
func (k *keptSecrets) get(key keptKey) (string, uint64, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.kept[key]
	if !ok || time.Since(v.at) >= secretKeep {
		delete(k.kept, key)
		return "", k.gen, false
	}
	return v.value, k.gen, true
}

// put keeps value unless something was forgotten since gen.
func (k *keptSecrets) put(key keptKey, value, provider string, gen uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if gen != k.gen {
		return
	}
	if k.kept == nil {
		k.kept = map[keptKey]keptSecret{}
	}
	k.kept[key] = keptSecret{value: value, provider: provider, at: time.Now()}
}

// forget drops the kept values that match.
func (k *keptSecrets) forget(match func(keptKey, keptSecret) bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gen++
	for key, v := range k.kept {
		if match(key, v) {
			delete(k.kept, key)
		}
	}
}

// forgetReference: a resolution of reference failed, so no kept copy of it is trusted any more.
func (k *keptSecrets) forgetReference(reference string) {
	k.forget(func(key keptKey, _ keptSecret) bool { return key.reference == reference })
}

// forgetProvider: provider was switched off, upgraded or removed.
func (k *keptSecrets) forgetProvider(provider string) {
	k.forget(func(_ keptKey, v keptSecret) bool { return v.provider == provider })
}

// LauncherSecret is launcher's key for machine ("" for the manager's own calls) when the key is reference: kept for
// secretKeep after its provider gave it. Saving the reference in Settings approved it (approved by "settings").
// Errors are *SecretError.
func (h *Manager) LauncherSecret(ctx context.Context, reference, launcher, machine string) (string, error) {
	if h.extensions == nil {
		return h.ResolveSecret(ctx, reference, wire.SecretPurpose{}, "settings") // says no provider is on
	}
	key := keptKey{reference: reference, kind: "launcher", id: launcher, machine: machine}
	v, gen, ok := h.extensions.kept.get(key)
	if ok {
		return v, nil
	}
	v, err := h.ResolveSecret(ctx, reference, wire.SecretPurpose{Kind: "launcher", ID: launcher, Machine: machine}, "settings")
	if err != nil {
		return "", err
	}
	if prov, ok := h.SecretProviderOf(reference); ok {
		h.extensions.kept.put(key, v, prov.Extension, gen)
	}
	return v, nil
}

// ForgetLauncherSecrets drops launcher's kept keys: its settings changed, or it is gone.
func (h *Manager) ForgetLauncherSecrets(launcher string) {
	if h.extensions == nil {
		return
	}
	h.extensions.kept.forget(func(key keptKey, _ keptSecret) bool { return key.kind == "launcher" && key.id == launcher })
}
