package extmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	larkcliconnector "github.com/nwverse/persona/server/internal/connector/larkcli"
	larkcliadapter "github.com/nwverse/persona/server/internal/larkcli"
)

const managedLarkCLIProfile = "persona"

type larkCLIBotView struct {
	Profile  string               `json:"profile"`
	AppID    string               `json:"app_id,omitempty"`
	SameApp  bool                 `json:"same_app"`
	Mode     string               `json:"mode"`
	Choice   string               `json:"choice"`
	Profiles []larkCLIProfileView `json:"profiles"`
}

type larkCLIProfileView struct {
	Name        string `json:"name"`
	AppID       string `json:"app_id"`
	User        string `json:"user"`
	TokenStatus string `json:"token_status"`
}

// effectiveLarkCLIProfile is the one profile the manager gives to both the
// connector and assistant.lark. An explicit profile remains an escape hatch;
// otherwise a bot app gets the managed profile and no bot falls back to the
// CLI's default profile.
func (h *Manager) effectiveLarkCLIProfile(ctx context.Context) string {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return ""
	}
	if profile := strings.TrimSpace(h.cfg.Lark.CLI.Profile); profile != "" {
		return profile
	}
	if profile := h.larkCLIProfileChoice(ctx); profile != "" {
		return profile
	}
	if h.botAppCredentialsID(ctx) != "" {
		return managedLarkCLIProfile
	}
	return ""
}

// larkCLIProfileChoice is the app's setting. It lives with the bot settings
// in the manager store, not in manager.json, and is intentionally separate
// from the effective profile selected by the precedence rules.
func (h *Manager) larkCLIProfileChoice(ctx context.Context) string {
	if h == nil {
		return ""
	}
	b := &h.bots
	b.mu.Lock()
	h.loadBotsLocked(ctx)
	choice := strings.TrimSpace(b.set.LarkCLIProfile)
	b.mu.Unlock()
	return choice
}

func (h *Manager) configuredLarkCLIProfile() string {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Lark.CLI.Profile)
}

func (h *Manager) larkCLIMode(ctx context.Context) string {
	switch {
	case h.configuredLarkCLIProfile() != "":
		return "config"
	case h.larkCLIProfileChoice(ctx) != "":
		return "chosen"
	case h.botAppCredentialsID(ctx) != "":
		return "bot"
	default:
		return "default"
	}
}

func (h *Manager) botAppCredentials(ctx context.Context) (appID, secret, domain string) {
	if h == nil || h.cfg == nil {
		return "", "", ""
	}
	b := &h.bots
	b.mu.Lock()
	defer b.mu.Unlock()
	h.loadBotsLocked(ctx)
	appID, secret, domain, _, err := h.botCredentialsLocked()
	if err != nil {
		// The app id is still safe and authoritative for profile selection even
		// when the sealed secret cannot be opened; sync will report the failure
		// without exposing it.
		return appID, "", domain
	}
	return appID, secret, domain
}

func (h *Manager) botAppCredentialsID(ctx context.Context) string {
	id, _, _ := h.botAppCredentials(ctx)
	return id
}

// syncLarkCLIProfile binds the managed profile to the bot app without ever
// putting the app secret in argv or a returned error. It is intentionally a
// no-op for an explicit profile and for a manager without bot credentials.
func (h *Manager) syncLarkCLIProfile(ctx context.Context) error {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil || h.configuredLarkCLIProfile() != "" || h.larkCLIProfileChoice(ctx) != "" {
		return nil
	}
	appID, secret, domain := h.botAppCredentials(ctx)
	if appID == "" || secret == "" {
		return nil
	}
	cli := h.cfg.Lark.CLI
	show := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Profile: managedLarkCLIProfile, Timeout: 15 * time.Second})
	showCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	current, err := show.ConfigShow(showCtx)
	cancel()
	if err == nil && current.AppID == appID {
		return nil
	}
	brand := larkCLIBrand(domain)
	initRunner := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Timeout: 15 * time.Second})
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = initRunner.InitProfile(initCtx, managedLarkCLIProfile, appID, secret, brand)
	cancel()
	if err != nil {
		return err
	}
	return nil
}

// validateLarkCLIProfile checks a user-selected profile against the CLI's
// read-only profile list. Empty means automatic selection.
func (h *Manager) validateLarkCLIProfile(ctx context.Context, choice string) error {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return nil
	}
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return fmt.Errorf("lark-cli is not configured")
	}
	cli := h.cfg.Lark.CLI
	r := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Profile: h.effectiveLarkCLIProfile(ctx), Timeout: 5 * time.Second})
	profiles, err := r.Profiles(ctx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Name == choice && choice != managedLarkCLIProfile {
			return nil
		}
	}
	return fmt.Errorf("lark-cli profile %q does not exist", choice)
}

// setLarkCLIProfile persists the app choice and rebuilds the connector. The
// registry keeps enabled/feed switches by connector id, so Inbox wiring stays
// attached to the replacement.
func (h *Manager) setLarkCLIProfile(ctx context.Context, choice string) error {
	choice = strings.TrimSpace(choice)
	b := &h.bots
	b.mu.Lock()
	h.loadBotsLocked(ctx)
	if b.set.LarkCLIProfile == choice {
		b.mu.Unlock()
		return nil
	}
	b.set.LarkCLIProfile = choice
	h.saveBotSettingsLocked()
	b.mu.Unlock()
	if err := h.syncLarkCLIProfile(ctx); err != nil {
		h.logf("larkcli profile sync: %v", err)
	}
	h.refreshLarkCLIConnector(ctx)
	h.kickInbox()
	return nil
}

func larkCLIBrand(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "lark" || d == "larksuite" || strings.Contains(d, "larksuite.com") {
		return "lark"
	}
	return "feishu"
}

// refreshLarkCLIConnector applies a profile change to the live registry. The
// registry's Remove/Add pair preserves the user's enabled switch and cancels a
// pending login through the connector lifecycle.
func (h *Manager) refreshLarkCLIConnector(ctx context.Context) {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil || h.connectors == nil {
		return
	}
	profile := h.effectiveLarkCLIProfile(ctx)
	if existing := h.connectors.Get(larkcliconnector.ID); existing != nil {
		if c, ok := existing.(*larkcliconnector.Connector); ok && c.Profile() == profile {
			return
		}
		h.connectors.Remove(larkcliconnector.ID)
	}
	cli := h.cfg.Lark.CLI
	if err := h.addConnector(larkcliconnector.New(larkcliadapter.Config{Bin: cli.Bin, Profile: profile})); err != nil {
		h.logf("larkcli connector: %v", err)
	}
}

// larkCLIView is the small binding summary consumed by the bot settings page.
// The app id is read from the selected profile's non-secret auth status, then
// compared with the bot app id held by the manager.
func (h *Manager) larkCLIView(ctx context.Context, botAppID string) *larkCLIBotView {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return nil
	}
	profile := h.effectiveLarkCLIProfile(ctx)
	choice := h.larkCLIProfileChoice(ctx)
	appID := ""
	cli := h.cfg.Lark.CLI
	r := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Profile: profile, Timeout: 5 * time.Second})
	if auth, err := r.AuthStatus(ctx); err == nil {
		appID = auth.AppID
	}
	profiles, err := r.Profiles(ctx)
	if err != nil {
		profiles = nil
	}
	viewProfiles := make([]larkCLIProfileView, 0, len(profiles))
	for _, p := range profiles {
		// The managed profile is what "follow the bot's app" (choice "") uses: listing it again would offer the
		// same thing twice under an internal name.
		if p.Name == managedLarkCLIProfile {
			continue
		}
		viewProfiles = append(viewProfiles, larkCLIProfileView{Name: p.Name, AppID: p.AppID, User: p.User, TokenStatus: p.TokenStatus})
	}
	return &larkCLIBotView{
		Profile:  profile,
		AppID:    appID,
		SameApp:  botAppID != "" && appID != "" && botAppID == appID,
		Mode:     h.larkCLIMode(ctx),
		Choice:   choice,
		Profiles: viewProfiles,
	}
}
