package extmgr

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Backup before an automatic upgrade (docs/extensions/extensions.md "Auto-upgrade", "Backup before an upgrade"): before
// the hub upgrades a market extension by itself it asks the installed program to get ready (upgrade.prepare, when its
// manifest says so), stops it and snapshots its data directory (Registry.BackupData, the newest three kept). When the
// new version then does not come up, or fails its upgrade.health, watchUpgrade stops it, puts the snapshot back and
// rolls the package back: the old version starts on the data it left. A snapshot that fails stops the upgrade only
// when either version's manifest says upgrade.backup "required"; otherwise the upgrade goes ahead without one.

const (
	// upgradePrepareWithin and upgradeHealthWithin bound the two calls to the extension's program.
	upgradePrepareWithin = 2 * time.Minute
	upgradeHealthWithin  = 30 * time.Second
)

// pendingBackup is the snapshot an automatic upgrade of one extension took, held until watchUpgrade knows whether the
// new version stays.
type pendingBackup struct {
	from, to string
	backup   string // the archive, "" when there was no data directory
}

// pendingBackups are the snapshots held for a rollback, by storage key.
type pendingBackups struct {
	mu sync.Mutex
	m  map[string]pendingBackup
}

func (b *pendingBackups) hold(key string, p pendingBackup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]pendingBackup{}
	}
	b.m[key] = p
}

// take hands back (and lets go of) the snapshot held for key's upgrade to version to.
func (b *pendingBackups) take(key, to string) (pendingBackup, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.m[key]
	if !ok || p.to != to {
		return pendingBackup{}, false
	}
	delete(b.m, key)
	return p, true
}

func (b *pendingBackups) drop(key, to string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if to != "" {
		if p, ok := b.m[key]; !ok || p.to != to {
			return
		}
	}
	delete(b.m, key)
}

// backupBeforeUpgrade gets extension p ready for its automatic upgrade to next (version to): upgrade.prepare when p
// declares it, then its program stopped and its data snapshot. On an error the upgrade must not go ahead, and p's
// program runs again. On success the program stays stopped (the upgrade starts the new one): the caller runs
// restartAfterUpgradeFailed when the upgrade fails after all.
func (ps *extensions) backupBeforeUpgrade(ctx context.Context, p *extension.Extension, next *extension.Manifest, to string) error {
	id, key, from := p.Record.ID, p.Record.StorageKey(), p.Record.Version
	hp := ps.get(id)
	if p.Manifest.UpgradePrepare() {
		if hp == nil || hp.proc == nil {
			return i18n.Err("hub-extension_autoupgrade.prepare_failed", i18n.Args{"why": i18n.T(i18n.From(ctx), "hub-extension_autoupgrade.not_running")})
		}
		cctx, cancel := context.WithTimeout(ctx, upgradePrepareWithin)
		var out struct {
			OK    *bool  `json:"ok"`
			Error string `json:"error"`
		}
		err := hp.proc.Call(cctx, "upgrade.prepare", map[string]any{"from": from, "to": to, "data_dir": ps.reg.DataDir(id)}, &out)
		cancel()
		if err == nil && out.OK != nil && !*out.OK {
			err = errors.New(out.Error)
		}
		if err != nil {
			ps.logf("plugin %s: upgrade.prepare for %s: %v (not upgraded)", id, to, err)
			return i18n.Err("hub-extension_autoupgrade.prepare_failed", i18n.Args{"why": err.Error()})
		}
	}
	if hp != nil && hp.proc != nil {
		hp.proc.Stop() // nothing writes its data while it is copied
	}
	path, files, err := ps.reg.BackupData(id, from)
	if err != nil {
		if p.Manifest.BackupRequired() || next.BackupRequired() {
			ps.logf("plugin %s: backing up its data before %s: %v (not upgraded: it requires a backup)", id, to, err)
			ps.apply(ps.storageRuntimeContext(), id)
			return i18n.Err("hub-extension_autoupgrade.backup_failed", i18n.Args{"why": err.Error()})
		}
		ps.logf("plugin %s: backing up its data before %s: %v (upgraded without a backup)", id, to, err)
		return nil
	}
	if path != "" {
		ps.logf("plugin %s: its data (%d files) backed up to %s before upgrading to %s", id, files, path, to)
	}
	ps.upgradeBackups.hold(key, pendingBackup{from: from, to: to, backup: path})
	return nil
}

// restartAfterUpgradeFailed lets go of the snapshot taken for an upgrade of id that did not happen, and runs the
// installed version again (backupBeforeUpgrade stopped it).
func (ps *extensions) restartAfterUpgradeFailed(id string) {
	ps.upgradeBackups.drop(ps.reg.StorageKeyOf(id), "")
	ps.apply(ps.storageRuntimeContext(), id)
}

// upgradeHealth asks the new version's program whether it works (upgrade.health), "" when it does; why not otherwise.
func (ps *extensions) upgradeHealth(ctx context.Context, id string, hp *hubExtension) string {
	cctx, cancel := context.WithTimeout(ctx, upgradeHealthWithin)
	defer cancel()
	var out struct {
		OK    *bool  `json:"ok"`
		Error string `json:"error"`
	}
	err := hp.proc.Call(cctx, "upgrade.health", map[string]any{}, &out)
	switch {
	case err != nil:
		return err.Error()
	case out.OK != nil && !*out.OK:
		if out.Error == "" {
			return "upgrade.health: not ok"
		}
		return out.Error
	}
	return ""
}

// rollBackUpgrade stops the new version of id, puts back the snapshot taken before its automatic upgrade (if one was)
// and rolls the package back to the version before.
func (ps *extensions) rollBackUpgrade(ctx context.Context, id string, hp *hubExtension, why string) (bool, string) {
	key := ps.reg.StorageKeyOf(id)
	to := ""
	if p := ps.reg.Get(id); p != nil {
		to = p.Record.Version
	}
	if held, ok := ps.upgradeBackups.take(key, to); ok {
		hp.proc.Stop() // the snapshot goes back while nothing writes
		if err := ps.reg.RestoreData(id, held.backup); err != nil {
			ps.logf("plugin %s: putting back its data from before %s: %v (its data stays as %s left it)", id, to, err, to)
		} else {
			ps.logf("plugin %s: its data is back as %s left it", id, held.from)
		}
	}
	if _, err := ps.reg.Rollback(ctx, id, "hub"); err != nil {
		ps.logf("plugin %s: rollback: %v", id, err)
		ps.apply(ps.storageRuntimeContext(), id) // whatever is installed runs again
		return false, ""
	}
	return true, why
}
