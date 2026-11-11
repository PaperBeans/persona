package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Launcher keys that are references (docs/extensions/secret-providers.md "Launcher keys", #3962): a model provider's
// key, or one computer's key for it, may be a reference such as op://vault/item/field in place of the key. It is
// sealed in the same field behind the extensions' marker (extension.SealedReference), so nothing else about a stored
// provider changes, and the provider extension that claims its scheme (internal/extmgr, in this process) gives the
// value wherever the key goes out: a computer's launchers.pull, the manager's own calls (main chat, quota, probe).
// Saving the reference in Settings is the owner's standing approval for that launcher; the manager keeps a resolved
// value in memory for a while (extmgr secret_kept.go), never on disk.

// launcherSecrets is what the launchers ask of the provider extensions (extmgr.Manager).
type launcherSecrets interface {
	// LauncherSecret is reference's value for launcher on machine ("" for the manager's own calls).
	LauncherSecret(ctx context.Context, reference, launcher, machine string) (string, error)
	// ForgetLauncherSecrets drops what is kept of launcher's keys.
	ForgetLauncherSecrets(launcher string)
	SecretProviderOf(reference string) (extension.SecretProvider, bool)
	// SecretReference is reference as a settings field shows it.
	SecretReference(ctx context.Context, reference string) *extmgr.SecretReferenceView
}

var _ launcherSecrets = (*extmgr.Manager)(nil)

// keyReference is the reference a sealed key holds; false for a key, no key or one that does not open.
func (s *Service) keyReference(sealed string) (string, bool) {
	if sealed == "" {
		return "", false
	}
	v, err := s.openKey(sealed)
	if err != nil {
		return "", false
	}
	return extension.ReferenceOf(v)
}

// openLauncherKey is a sealed key of launcher as it is used on machine ("" for the manager's own calls): the key, or
// the value its reference resolves to. A reference that does not resolve is the provider's error (*extmgr.SecretError,
// in the asker's language), so a start it refuses says why.
func (s *Service) openLauncherKey(ctx context.Context, sealed, launcher, machine string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	v, err := s.openKey(sealed)
	if err != nil {
		return "", i18n.Err("assistant-models.err_key_decrypt")
	}
	ref, ok := extension.ReferenceOf(v)
	if !ok {
		return v, nil
	}
	if s.refs == nil {
		return "", noSecretProvider(ref)
	}
	return s.refs.LauncherSecret(ctx, ref, launcher, machine)
}

func noSecretProvider(ref string) error {
	scheme, _ := extension.ReferenceScheme(ref)
	return i18n.Err("hub-extension_secrets.no_provider", i18n.Args{"scheme": scheme + "://"})
}

// sealKeyReference checks a reference for launcher's key (machine's own when set) and seals it: one of a scheme an
// enabled provider extension claims, that resolves now. Called before models.mu is taken: it asks the provider.
func (s *Service) sealKeyReference(ctx context.Context, ref, launcher, machine string) (string, error) {
	ref = strings.TrimSpace(ref)
	if _, ok := extension.ReferenceScheme(ref); !ok {
		return "", i18n.Err("hub-extension_secrets.not_reference")
	}
	if s.refs == nil {
		return "", noSecretProvider(ref)
	}
	if _, ok := s.refs.SecretProviderOf(ref); !ok {
		return "", noSecretProvider(ref)
	}
	if _, err := s.refs.LauncherSecret(ctx, ref, launcher, machine); err != nil {
		return "", err
	}
	return s.sealKey(extension.SealedReference(ref))
}

// sealKeyReferences seals in's references (api_key_reference, machine_key_references) into it. An update first drops
// what is kept of the launcher's keys, so a reference saved again resolves afresh (the secret may have been rotated).
func (s *Service) sealKeyReferences(ctx context.Context, in *launcherInput, update bool) error {
	if update {
		s.forgetLauncherSecrets(in.Name)
	}
	if in.APIKeyReference != nil && strings.TrimSpace(*in.APIKeyReference) != "" {
		sealed, err := s.sealKeyReference(ctx, *in.APIKeyReference, in.Name, "")
		if err != nil {
			return err
		}
		in.keyRefSealed = sealed
	}
	for machine, ref := range in.MachineKeyReferences {
		if ref == nil || strings.TrimSpace(*ref) == "" {
			continue
		}
		sealed, err := s.sealKeyReference(ctx, *ref, in.Name, machine)
		if err != nil {
			return err
		}
		if in.machineRefsSealed == nil {
			in.machineRefsSealed = map[string]string{}
		}
		in.machineRefsSealed[machine] = sealed
	}
	return nil
}

func (s *Service) forgetLauncherSecrets(launcher string) {
	if s.refs != nil {
		s.refs.ForgetLauncherSecrets(launcher)
	}
}

// withKeyReferences fills the managed views' key_reference and machine_key_references: where a key is a reference,
// the reference and what it names, never a value.
func (s *Service) withKeyReferences(ctx context.Context, views []LauncherView, keys []machineKey) {
	view := func(ref string) *extmgr.SecretReferenceView {
		if s.refs == nil {
			return &extmgr.SecretReferenceView{Reference: ref, Title: ref}
		}
		return s.refs.SecretReference(ctx, ref)
	}
	for i := range views {
		if views[i].Source != "persona" {
			continue
		}
		if ref, ok := s.keyReference(views[i].keySealed); ok {
			views[i].KeyReference = view(ref)
		}
		for _, k := range keys {
			if k.Launcher != views[i].Name {
				continue
			}
			if ref, ok := s.keyReference(k.KeySealed); ok {
				if views[i].MachineKeyReferences == nil {
					views[i].MachineKeyReferences = map[string]*extmgr.SecretReferenceView{}
				}
				views[i].MachineKeyReferences[k.Machine] = view(ref)
			}
		}
	}
}

// launcherReferences are the stored providers' keys that hold references, for the provider extensions' details.
func (s *Service) launcherReferences(ctx context.Context) []extmgr.LauncherReference {
	var out []extmgr.LauncherReference
	stored := s.storedLaunchers(ctx)
	name := func(launcher string) string {
		for _, l := range stored {
			if l.Name == launcher && l.DisplayName != "" {
				return l.DisplayName
			}
		}
		return launcher
	}
	for _, l := range stored {
		if ref, ok := s.keyReference(l.KeySealed); ok {
			out = append(out, extmgr.LauncherReference{Launcher: l.Name, LauncherName: name(l.Name), Reference: ref})
		}
	}
	var keys []machineKey
	if st := s.store(); st != nil {
		_, _ = memoSetting(ctx, st, launchersMachineKeysKey, &keys)
	}
	for _, k := range keys {
		if !slices.ContainsFunc(stored, func(l storedLauncher) bool { return l.Name == k.Launcher }) {
			continue
		}
		if ref, ok := s.keyReference(k.KeySealed); ok {
			out = append(out, extmgr.LauncherReference{Launcher: k.Launcher, LauncherName: name(k.Launcher), Machine: k.Machine, Reference: ref})
		}
	}
	return out
}
