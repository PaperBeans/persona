// Package recovery owns the read-only, whole-install Recovery inventory. The
// hub's routes only authenticate and audit calls; bundle discovery and result
// redaction stay here with the operations/backup implementation.
package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/operations/backup"
)

var (
	ErrInvalidBundleID = errors.New("invalid recovery bundle id")
	ErrBundleNotFound  = errors.New("recovery bundle not found")
	ErrBusy            = errors.New("another recovery check is running")
)

// checkSlot admits one whole-bundle check per process. The hub builds a Service per request, and a verification
// extracts the full bundle into temporary space and restores two temporary databases on the live PostgreSQL server,
// so concurrent requests (another tab, a retry after a reload, another device) are refused rather than stacked.
var checkSlot = make(chan struct{}, 1)

const maxBundles = 50

type Capability struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type Capabilities struct {
	Inspect  Capability `json:"inspect"`
	Verify   Capability `json:"verify"`
	Create   Capability `json:"create"`
	Restore  Capability `json:"restore"`
	Download Capability `json:"download"`
}

type Overview struct {
	Capabilities Capabilities `json:"capabilities"`
	Discovery    Capability   `json:"discovery"`
	Bundles      []Bundle     `json:"bundles"`
	History      Capability   `json:"history"`
}

type Bundle struct {
	ID           string    `json:"id"`
	Revision     string    `json:"revision"`
	Name         string    `json:"name"`
	ArchiveBytes int64     `json:"archive_bytes"`
	ModifiedAt   time.Time `json:"modified_at"`
}

type Check struct {
	Kind      string                   `json:"kind"`
	State     string                   `json:"state"`
	Reason    string                   `json:"reason,omitempty"`
	CheckedAt time.Time                `json:"checked_at"`
	Bundle    Bundle                   `json:"bundle"`
	Report    *backup.InspectionReport `json:"report,omitempty"`
}

type Service struct {
	home          string
	inspect       func(context.Context, string) (backup.InspectionReport, error)
	verify        func(context.Context, backup.Options) (backup.Result, error)
	support       func(backup.Options) backup.Support
	createSupport func(backup.Options) backup.Support
	now           func() time.Time
	slot          chan struct{}
}

func NewService(home string) *Service {
	operator := backup.NewOperator()
	return &Service{
		home: home, inspect: backup.InspectBundle, verify: operator.Verify,
		support: backup.VerificationSupport, createSupport: backup.CreationSupport,
		now: time.Now, slot: checkSlot,
	}
}

// acquire takes the check slot without waiting; release must be called when ok.
func (s *Service) acquire() (release func(), ok bool) {
	select {
	case s.slot <- struct{}{}:
		return func() { <-s.slot }, true
	default:
		return nil, false
	}
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	discovery, entries := s.list(ctx)
	bundles := make([]Bundle, 0, len(entries))
	for _, entry := range entries {
		bundles = append(bundles, entry.Bundle)
	}
	create := Capability{State: "guided", Reason: "writes_quiesced"}
	if support := s.createSupport(s.options("")); support.State != "supported" {
		create = Capability{State: "unsupported", Reason: support.Reason}
	}
	return Overview{
		Capabilities: Capabilities{
			Inspect: Capability{State: "supported"}, Verify: capabilityOf(s.support(s.options(""))), Create: create,
			Restore:  Capability{State: "unsupported", Reason: "terminal_only"},
			Download: Capability{State: "unsupported", Reason: "private_keys"},
		},
		Discovery: discovery, Bundles: bundles,
		History: Capability{State: "unavailable", Reason: "no_authoritative_source"},
	}, nil
}

func (s *Service) Inspect(ctx context.Context, id string) (Check, error) {
	entry, err := s.resolve(ctx, id)
	if err != nil {
		return Check{}, err
	}
	release, ok := s.acquire()
	if !ok {
		return Check{}, ErrBusy
	}
	defer release()
	check := Check{Kind: "offline_integrity", CheckedAt: s.now().UTC(), Bundle: entry.Bundle}
	report, err := s.inspect(ctx, entry.path)
	if err != nil {
		if ctx.Err() != nil {
			return Check{}, ctx.Err()
		}
		check.State, check.Reason = "failed", "invalid_bundle"
		return check, nil
	}
	unchanged, err := s.unchanged(entry)
	if err != nil {
		return Check{}, err
	}
	if !unchanged {
		check.State, check.Reason = "failed", "bundle_changed"
		return check, nil
	}
	check.State, check.Report = "passed", &report
	return check, nil
}

func (s *Service) Verify(ctx context.Context, id string) (Check, error) {
	entry, err := s.resolve(ctx, id)
	if err != nil {
		return Check{}, err
	}
	release, ok := s.acquire()
	if !ok {
		return Check{}, ErrBusy
	}
	defer release()
	check := Check{Kind: "database_restore", CheckedAt: s.now().UTC(), Bundle: entry.Bundle}
	if support := s.support(s.options("")); support.State != "supported" {
		check.State, check.Reason = "unsupported", support.Reason
		return check, nil
	}
	result, err := s.verify(ctx, s.options(entry.path))
	if err != nil || !result.Verified {
		if ctx.Err() != nil {
			return Check{}, ctx.Err()
		}
		check.State, check.Reason = "failed", "verification_failed"
		return check, nil
	}
	unchanged, err := s.unchanged(entry)
	if err != nil {
		return Check{}, err
	}
	if !unchanged {
		check.State, check.Reason = "failed", "bundle_changed"
		return check, nil
	}
	check.State = "passed"
	return check, nil
}

func capabilityOf(support backup.Support) Capability {
	return Capability{State: support.State, Reason: support.Reason}
}

func (s *Service) options(bundle string) backup.Options {
	return backup.Options{Home: s.home, Bundle: bundle}
}

type listedBundle struct {
	Bundle
	path string
}

func (s *Service) resolve(ctx context.Context, id string) (listedBundle, error) {
	if len(id) != sha256.Size*2 {
		return listedBundle{}, ErrInvalidBundleID
	}
	if _, err := hex.DecodeString(id); err != nil || strings.ToLower(id) != id {
		return listedBundle{}, ErrInvalidBundleID
	}
	discovery, entries := s.list(ctx)
	if discovery.State != "supported" {
		return listedBundle{}, ErrBundleNotFound
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return listedBundle{}, ErrBundleNotFound
}

func (s *Service) unchanged(was listedBundle) (bool, error) {
	info, err := os.Lstat(was.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	return bundleIdentity(was.path, info).Revision == was.Revision, nil
}

func (s *Service) list(ctx context.Context) (Capability, []listedBundle) {
	if ctx.Err() != nil {
		return Capability{State: "unsupported", Reason: "unreadable_directory"}, nil
	}
	directory := filepath.Join(s.home, "backups")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return Capability{State: "supported"}, []listedBundle{}
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return Capability{State: "unsupported", Reason: "unsafe_directory"}, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Capability{State: "unsupported", Reason: "unreadable_directory"}, nil
	}
	out := make([]listedBundle, 0, min(len(entries), maxBundles))
	truncated := false
	for _, entry := range entries {
		if ctx.Err() != nil {
			return Capability{State: "unsupported", Reason: "unreadable_directory"}, nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if len(out) == maxBundles {
			truncated = true
		}
		out = append(out, bundleIdentity(name, info))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModifiedAt.Equal(out[j].ModifiedAt) {
			return out[i].Name < out[j].Name
		}
		return out[i].ModifiedAt.After(out[j].ModifiedAt)
	})
	if len(out) > maxBundles {
		out = out[:maxBundles]
		truncated = true
	}
	discovery := Capability{State: "supported"}
	if truncated {
		discovery.Reason = "limited"
	}
	return discovery, out
}

func bundleIdentity(name string, info os.FileInfo) listedBundle {
	base := filepath.Base(name)
	id := sha256.Sum256([]byte(base))
	revision := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", base, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())))
	return listedBundle{
		Bundle: Bundle{
			ID: hex.EncodeToString(id[:]), Revision: hex.EncodeToString(revision[:]), Name: base,
			ArchiveBytes: info.Size(), ModifiedAt: info.ModTime().UTC(),
		},
		path: name,
	}
}
