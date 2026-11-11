package extmgr

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Binding the Lark bot in one authorization (#4347, docs/extensions/bots.md "Pairing and permissions"): the bind step
// starts the accounts site's app registration, the user opens its page in Lark (or scans it from another device) and
// confirms, and the answer carries the new app's credentials and the confirming person's open id. The credentials are
// saved as if typed in, the app configured as after typing them, and that person becomes the owner: no pairing code.
// The registration is kept in the settings (bots.feishu), so a manager restart between two polls loses nothing; its
// sealed parts are top-level fields there (FeishuBotSettings.RegistrationDeviceEnc / RegistrationSecretEnc), where the
// key rotation re-seals them (db.sealedSettings).

// botRegistration is a registration under way.
type botRegistration struct {
	Region        string    `json:"region"` // "feishu" | "lark": whose accounts site is asked
	DeviceCodeEnc string    `json:"-"`      // sealed: polling with it yields the app's secret (kept in RegistrationDeviceEnc)
	URL           string    `json:"url"`    // the page to open or scan
	Expires       time.Time `json:"expires"`
	Interval      int       `json:"interval"` // seconds between polls
	// Once confirmed, what the answer carried, kept until the bind is finished (a failed finish is tried again on
	// the next poll instead of creating another app).
	AppID     string `json:"app_id,omitempty"`
	SecretEnc string `json:"-"` // sealed (kept in RegistrationSecretEnc)
	OpenID    string `json:"open_id,omitempty"`
}

const (
	stepRegister        = "register"
	botRegisterFor      = 10 * time.Minute // when the platform does not say
	botRegisterInterval = 5
	botRegisterName     = "Persona"
)

// registrar is the accounts site's client (replaced in tests).
func (h *Manager) registrar() *feishubot.Registrar {
	b := &h.bots
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.registrar == nil {
		b.registrar = feishubot.NewRegistrar()
	}
	return b.registrar
}

// registeredDomain is the domain a registered app's calls go to: the region itself (tests point it elsewhere).
func (h *Manager) registeredDomain(region string) string {
	if h.bots.regDomain != nil {
		return h.bots.regDomain(region)
	}
	return region
}

// beginFeishuRegistration starts a registration, or goes on with the one under way. ok is false when the accounts
// site cannot be reached: the bind falls back to typing the credentials.
func (h *Manager) beginFeishuRegistration(ctx context.Context, errText string) (connector.Prompt, bool) {
	b := &h.bots
	b.mu.Lock()
	h.loadBotsLocked(ctx)
	reg := b.set.registration()
	now := b.clock()
	b.mu.Unlock()
	if reg != nil && now.Before(reg.Expires) && errText == "" {
		return h.registerPrompt(ctx, *reg, ""), true
	}
	region := "feishu"
	ch, err := h.registrar().Begin(ctx, region, botRegisterName)
	if err != nil {
		h.logf("bots: registration: %v", err)
		return connector.Prompt{}, false
	}
	sealed, err := h.sealKey(ch.DeviceCode)
	if err != nil {
		h.logf("bots: registration: %v", err)
		return connector.Prompt{}, false
	}
	expires := time.Duration(ch.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = botRegisterFor
	}
	next := botRegistration{Region: region, DeviceCodeEnc: sealed, URL: ch.URL, Expires: now.Add(expires), Interval: max(ch.Interval, botRegisterInterval)}
	h.putRegistration(&next)
	return h.registerPrompt(ctx, next, errText), true
}

// registration is the registration under way with its sealed parts, nil when there is none.
func (s FeishuBotSettings) registration() *botRegistration {
	if s.Registration == nil {
		return nil
	}
	r := *s.Registration
	r.DeviceCodeEnc, r.SecretEnc = s.RegistrationDeviceEnc, s.RegistrationSecretEnc
	return &r
}

// putRegistration keeps reg (nil: drops it), its sealed parts in the settings' top-level fields.
func (h *Manager) putRegistration(reg *botRegistration) {
	b := &h.bots
	b.mu.Lock()
	b.set.Registration, b.set.RegistrationDeviceEnc, b.set.RegistrationSecretEnc = nil, "", ""
	if reg != nil {
		r := *reg
		b.set.RegistrationDeviceEnc, b.set.RegistrationSecretEnc = r.DeviceCodeEnc, r.SecretEnc
		r.DeviceCodeEnc, r.SecretEnc = "", ""
		b.set.Registration = &r
	}
	h.saveBotSettingsLocked()
	b.mu.Unlock()
}

func (h *Manager) registerPrompt(ctx context.Context, reg botRegistration, errText string) connector.Prompt {
	l := h.botLang(ctx)
	return connector.Prompt{Step: stepRegister, Kind: connector.PromptQRCode, URL: reg.URL, Interval: reg.Interval,
		Hint: i18n.T(l, "hub-channels.register_hint"), Link: reg.URL, LinkText: i18n.T(l, "hub-channels.register_open"), Error: errText}
}

// pollFeishuRegistration is the register step asked again: still waiting, or confirmed and bound.
func (h *Manager) pollFeishuRegistration(ctx context.Context, credentials func(string) connector.Prompt) (connector.Prompt, error) {
	l := h.botLang(ctx)
	b := &h.bots
	b.mu.Lock()
	h.loadBotsLocked(ctx)
	var reg botRegistration
	cur := b.set.registration()
	live := cur != nil
	if live {
		reg = *cur
	}
	now := b.clock()
	b.mu.Unlock()
	if !live {
		return h.restartRegistration(ctx, "", credentials), nil
	}
	if reg.AppID == "" {
		if !now.Before(reg.Expires) {
			return h.restartRegistration(ctx, i18n.T(l, "hub-channels.register_expired"), credentials), nil
		}
		code, err := h.openKey(reg.DeviceCodeEnc)
		if err != nil {
			return h.restartRegistration(ctx, "", credentials), nil
		}
		t, err := h.registrar().Poll(ctx, reg.Region, code)
		if err != nil { // the network: asked again on the next poll
			h.logf("bots: registration poll: %v", err)
			return h.registerPrompt(ctx, reg, ""), nil
		}
		switch t.Status {
		case feishubot.RegisterDone:
			sealed, err := h.sealKey(t.AppSecret)
			if err != nil {
				return connector.Prompt{}, err
			}
			reg.AppID, reg.SecretEnc, reg.OpenID = t.AppID, sealed, t.OpenID
			if t.Brand == "lark" || t.Brand == "feishu" {
				reg.Region = t.Brand // the app lives on the tenant's cloud
			}
			h.putRegistration(&reg)
		case feishubot.RegisterPending, feishubot.RegisterSlowDown, feishubot.RegisterFailed:
			// A tenant on the other cloud is answered there: ask that one from now on.
			if other := t.Brand; (other == "lark" || other == "feishu") && other != reg.Region {
				reg.Region = other
				h.putRegistration(&reg)
				return h.registerPrompt(ctx, reg, ""), nil
			}
			if t.Status == feishubot.RegisterFailed {
				h.logf("bots: registration: %s", t.ErrorText)
				h.putRegistration(nil)
				return credentials(i18n.T(l, "hub-channels.register_failed")), nil
			}
			if t.Status == feishubot.RegisterSlowDown {
				reg.Interval += 5
				h.putRegistration(&reg)
			}
			return h.registerPrompt(ctx, reg, ""), nil
		case feishubot.RegisterExpired:
			return h.restartRegistration(ctx, i18n.T(l, "hub-channels.register_expired"), credentials), nil
		case feishubot.RegisterDenied:
			return h.restartRegistration(ctx, i18n.T(l, "hub-channels.register_denied"), credentials), nil
		}
	}
	name, err := h.finishFeishuRegistration(ctx, reg)
	if err != nil {
		h.logf("bots: registration finish: %v", err)
		return h.registerPrompt(ctx, reg, i18n.Localize(err, l)), nil
	}
	h.putRegistration(nil)
	return connector.Prompt{Done: true, Account: firstNonEmpty(name, i18n.T(l, "hub-channels.paired"))}, nil
}

// restartRegistration drops the registration and starts another, saying why; the credentials step when the accounts
// site cannot be reached.
func (h *Manager) restartRegistration(ctx context.Context, errText string, credentials func(string) connector.Prompt) connector.Prompt {
	h.putRegistration(nil)
	if p, ok := h.beginFeishuRegistration(ctx, errText); ok {
		return p
	}
	return credentials(errText)
}

// finishFeishuRegistration binds the registered app: its credentials saved and checked like typed ones, the app
// configured, the confirming person made the owner and greeted in the private chat (which gives its chat id). It
// gives the owner's name.
func (h *Manager) finishFeishuRegistration(ctx context.Context, reg botRegistration) (string, error) {
	secret, err := h.openKey(reg.SecretEnc)
	if err != nil {
		return "", err
	}
	domain := h.registeredDomain(reg.Region)
	on := true
	if err := h.updateBot(ctx, &on, &reg.AppID, &secret, &domain, nil); err != nil {
		return "", err
	}
	// Best-effort, as after typed credentials: the template opened the scopes and the callback; this subscribes the
	// events over the long connection and submits the publish.
	if rep := feishubot.New(reg.AppID, secret, domain).AutoConfigure(ctx); rep.Detail != "" {
		h.logf("bots: auto-configure: %s", rep.Detail)
	}
	b := &h.bots
	b.mu.Lock()
	api := b.api
	b.mu.Unlock()
	if api == nil {
		return "", errBotOff
	}
	l := h.userLang()
	body := i18n.T(l, "hub-bots_chat.paired_body")
	card := feishubot.NewCard(i18n.T(l, "hub-bots_chat.paired"), feishubot.Green, i18n.T(l, "hub-bots_chat.paired")).Markdown(body)
	key := "registered-" + reg.AppID // the greeting lands once however often a failed finish is tried again
	msgID, chatID, err := api.SendUser(ctx, reg.OpenID, "interactive", card.JSON(), cutUTF8(key+":c", feishuMaxUUID))
	if feishubot.CardRejected(err) {
		msgID, chatID, err = api.SendUser(ctx, reg.OpenID, "text", feishubot.TextContent(body), cutUTF8(key+":t", feishuMaxUUID))
	}
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.set.Owner = &BotOwner{OpenID: reg.OpenID, ChatID: chatID, At: b.clock().UTC()}
	b.set.Pairing = nil
	h.saveBotSettingsLocked()
	b.mu.Unlock()
	h.rememberMessageChat(msgID, chatID)
	h.auditBot(ctx, botRecord{auditBotPaired, map[string]any{"open_id": reg.OpenID, "registered": true}})
	name := h.botName(ctx, api, reg.OpenID)
	b.mu.Lock()
	if b.set.Owner != nil && b.set.Owner.OpenID == reg.OpenID {
		b.set.Owner.Name = name
		h.saveBotSettingsLocked()
	}
	b.mu.Unlock()
	// The chat starts afresh; the greeting is already there.
	_ = h.notifyAssistant(assistantwire.EventFeishuOwner, assistantwire.FeishuOwner{Paired: true, Greeted: true})
	return name, nil
}
