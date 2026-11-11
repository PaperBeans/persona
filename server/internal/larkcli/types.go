package larkcli

import (
	"encoding/json"
	"strings"
	"time"
)

// MinimumVersion is the first lark-cli release whose shortcut JSON contracts this
// adapter accepts. Keep this pinned and raise it only with fixture updates.
const MinimumVersion = "1.0.72"

// AuthStatus is the user identity reported by `lark-cli auth status --json`.
// GrantedScopes and MissingScopes are best-effort because older CLI releases did
// not include both fields.
type AuthStatus struct {
	AppID            string    `json:"app_id,omitempty"`
	Brand            string    `json:"brand,omitempty"`
	LoggedIn         bool      `json:"logged_in"`
	Expired          bool      `json:"expired"`
	UserName         string    `json:"user_name,omitempty"`
	OpenID           string    `json:"open_id,omitempty"`
	TokenStatus      string    `json:"token_status,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
	GrantedScopes    []string  `json:"granted_scopes,omitempty"`
	MissingScopes    []string  `json:"missing_scopes,omitempty"`
}

// Profile is the non-secret summary printed by `lark-cli profile list`.
// Keep this deliberately smaller than the CLI's output: profile credentials
// and token material must never cross the manager API boundary.
type Profile struct {
	Name        string `json:"name"`
	AppID       string `json:"app_id,omitempty"`
	Brand       string `json:"brand,omitempty"`
	User        string `json:"user,omitempty"`
	TokenStatus string `json:"token_status,omitempty"`
}

// DaysUntilExpiry returns the rounded-up number of calendar days remaining for
// the refresh token. The access token can be renewed automatically before this
// deadline, so its expires_at value is not a login expiry.
func (a AuthStatus) DaysUntilExpiry(now time.Time) int {
	if a.RefreshExpiresAt.IsZero() {
		return 0
	}
	d := a.RefreshExpiresAt.Sub(now)
	if d <= 0 {
		return 0
	}
	days := int((d + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}

// RefreshWindowNeedsReminder reports when the rolling refresh-token window is
// nearly exhausted. A healthy login refreshes that window on user API calls,
// so its ordinary seven-day deadline is not an expiry warning; only a window
// with less than one day remaining indicates that refreshes may have stopped.
func (a AuthStatus) RefreshWindowNeedsReminder(now time.Time) bool {
	if a.RefreshExpiresAt.IsZero() {
		return false
	}
	remaining := a.RefreshExpiresAt.Sub(now)
	return remaining > 0 && remaining < 24*time.Hour
}

// Chat is the stable subset of a shortcut chat item used by Persona.
type Chat struct {
	ChatID        string `json:"chat_id"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	OwnerID       string `json:"owner_id,omitempty"`
	ChatType      string `json:"chat_type,omitempty"`
	ChatMode      string `json:"chat_mode,omitempty"`
	P2PTargetType string `json:"p2p_target_type,omitempty"`
	P2PTargetID   string `json:"p2p_target_id,omitempty"`
	External      bool   `json:"external,omitempty"`
	ChatStatus    string `json:"chat_status,omitempty"`
}

// ChatList is the normalized result of +chat-list or +chat-search.
type ChatList struct {
	Chats     []Chat `json:"chats"`
	Total     int    `json:"total,omitempty"`
	HasMore   bool   `json:"has_more,omitempty"`
	PageToken string `json:"page_token,omitempty"`
}

// Mention is one mention in a message.
type Mention struct {
	ID     string `json:"id,omitempty"`
	IDType string `json:"id_type,omitempty"`
	Key    string `json:"key,omitempty"`
	Name   string `json:"name,omitempty"`
}

// Sender is the sender data surfaced by the CLI shortcuts.
type Sender struct {
	ID              string            `json:"id,omitempty"`
	IDType          string            `json:"id_type,omitempty"`
	SenderType      string            `json:"sender_type,omitempty"`
	Name            string            `json:"name,omitempty"`
	OpenBotID       string            `json:"open_bot_id,omitempty"`
	SenderI18nNames map[string]string `json:"sender_i18n_names,omitempty"`
}

// Message is the common message shape returned by chat reads and searches.
// Content is retained as JSON because the CLI returns either a JSON string or a
// structured value depending on msg_type.
type Message struct {
	MessageID   string          `json:"message_id"`
	MsgType     string          `json:"msg_type,omitempty"`
	CreateTime  string          `json:"create_time,omitempty"`
	UpdateTime  string          `json:"update_time,omitempty"`
	Sender      Sender          `json:"sender,omitempty"`
	Content     json.RawMessage `json:"content,omitempty"`
	ChatID      string          `json:"chat_id,omitempty"`
	ChatName    string          `json:"chat_name,omitempty"`
	ChatType    string          `json:"chat_type,omitempty"`
	ChatPartner json.RawMessage `json:"chat_partner,omitempty"`
	Deleted     bool            `json:"deleted,omitempty"`
	Updated     bool            `json:"updated,omitempty"`
	Mentions    []Mention       `json:"mentions,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	ParentID    string          `json:"parent_id,omitempty"`
	RootID      string          `json:"root_id,omitempty"`
	Reactions   json.RawMessage `json:"reactions,omitempty"`
}

// MessagePage is the normalized paginated result of a message shortcut.
type MessagePage struct {
	Messages  []Message `json:"messages"`
	Total     int       `json:"total,omitempty"`
	HasMore   bool      `json:"has_more,omitempty"`
	PageToken string    `json:"page_token,omitempty"`
}

// SearchOptions are the read-only filters supported by +messages-search.
type SearchOptions struct {
	ChatID    string
	Sender    string
	ChatType  string
	Start     string
	End       string
	AtMe      bool
	Limit     int
	PageToken string
}

func (o SearchOptions) normalizedLimit() int {
	if o.Limit <= 0 {
		return 20
	}
	if o.Limit > 50 {
		return 50
	}
	return o.Limit
}

func splitScopes(s string) []string { return strings.Fields(s) }
