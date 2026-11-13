package extmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An extension's sign-in (manifest.auth, docs/extensions/extensions.md "Sign-in (auth)", #4507): its program answers auth.status,
// auth.step and auth.logout; the hub keeps the last status it heard, shows it on the extension, tells the apps when
// it changes (extension_auth) and passes the apps' sign-in steps on. What signing in yields stays with the program, in
// its data directory: the values of a step go to it only, never into a log, an audit or an answer.

// The states of ExtensionAuth.
const (
	authUnknown   = "unknown" // the program does not run (switched off, waiting for its settings), or was not asked yet
	authSignedOut = "signed_out"
	authPending   = "pending"
	authSignedIn  = "signed_in"
	authExpired   = "expired"
	authError     = "error"
)

const (
	authStatusWait = 10 * time.Second
	authStepWait   = time.Minute
)

// ExtensionAuth is an extension's sign-in as the apps see it.
type ExtensionAuth struct {
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Page          string     `json:"page,omitempty"`
	State         string     `json:"state"`
	Account       string     `json:"account,omitempty"`
	Message       string     `json:"message,omitempty"`
	MessageDetail string     `json:"message_detail,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CheckedAt     *time.Time `json:"checked_at,omitempty"`
}

// authStatus is the program's answer to auth.status and auth.logout.
type authStatus struct {
	// hubMessage distinguishes hub-owned failures from the program's authored message.
	hubMessage bool
	State      string     `json:"state"`
	Account    string     `json:"account,omitempty"`
	Message    string     `json:"message,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// authPrompt is a sign-in step: the connectors' Prompt, and when its code or link stops working.
type authPrompt struct {
	connector.Prompt
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// authCache is the last status of each extension that signs in.
type authCache struct {
	mu         sync.Mutex
	byID       map[string]authStatus
	at         map[string]time.Time
	refreshing map[string]bool
}

func (c *authCache) get(id string) (authStatus, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.byID[id]
	return st, c.at[id], ok
}

// put keeps st for id and says whether it changed.
func (c *authCache) put(id string, st authStatus) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID == nil {
		c.byID, c.at = map[string]authStatus{}, map[string]time.Time{}
	}
	old, had := c.byID[id]
	c.byID[id], c.at[id] = st, time.Now()
	return !had || old.State != st.State || old.Account != st.Account || old.Message != st.Message || old.hubMessage != st.hubMessage || !sameTime(old.ExpiresAt, st.ExpiresAt)
}

func (c *authCache) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, id)
	delete(c.at, id)
}

// claim marks a background refresh of id as running; false when one already is.
func (c *authCache) claim(id string, on bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refreshing == nil {
		c.refreshing = map[string]bool{}
	}
	if on && c.refreshing[id] {
		return false
	}
	c.refreshing[id] = on
	return true
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// validAuthState is a state a program may answer.
func validAuthState(s string) bool {
	switch s {
	case authSignedOut, authPending, authSignedIn, authExpired, authError:
		return true
	}
	return false
}

// authView is p's sign-in from the cache (nil when it does not sign in): unknown while its program does not run or
// was not asked yet. It asks in the background when it was not.
func (ps *extensions) authView(p *extension.Extension, langs ...i18n.Lang) *ExtensionAuth {
	spec := p.Full.Auth
	if spec == nil {
		return nil
	}
	v := &ExtensionAuth{Title: spec.Title, Description: spec.Description, Page: spec.Page, State: authUnknown}
	if !ps.authRuns(p) {
		return v
	}
	st, at, ok := ps.auth.get(p.Record.ID)
	if !ok {
		go ps.refreshAuth(context.Background(), p.Record.ID)
		return v
	}
	v.State, v.Account, v.Message, v.ExpiresAt, v.CheckedAt = st.State, st.Account, st.Message, st.ExpiresAt, &at
	if st.hubMessage && len(langs) > 0 {
		v.Message, v.MessageDetail = i18n.ExtensionFailureText(langs[0], v.Message, "extension-status.auth_failed")
	}
	return v
}

// authRuns says whether p's program can be asked about its sign-in now: on, set up, and started (or started by
// being called).
func (ps *extensions) authRuns(p *extension.Extension) bool {
	if !p.Record.Enabled || p.Problem != "" {
		return false
	}
	hp := ps.get(p.Record.ID)
	return hp != nil && hp.proc != nil
}

// refreshAuth asks extension id for its status, keeps it and tells the apps when it changed. Background callers
// (an auth.changed event, a view that had none) run one at a time per extension.
func (ps *extensions) refreshAuth(ctx context.Context, id string) {
	if !ps.auth.claim(id, true) {
		return
	}
	defer ps.auth.claim(id, false)
	_, _ = ps.askAuth(ctx, id)
}

// askAuth asks extension id's program for its sign-in status now.
func (ps *extensions) askAuth(ctx context.Context, id string) (*ExtensionAuth, error) {
	p := ps.reg.Get(id)
	if p == nil || p.Full.Auth == nil {
		return nil, errNoAuth
	}
	if !ps.authRuns(p) {
		return ps.authView(p), nil
	}
	hp := ps.get(id)
	ctx, cancel := context.WithTimeout(ctx, authStatusWait)
	defer cancel()
	var st authStatus
	if err := hp.proc.Call(ctx, "auth.status", map[string]any{}, &st); err != nil {
		var remote *rpc.Error
		st = authStatus{State: authError, Message: err.Error(), hubMessage: !errors.As(err, &remote) || errors.As(err, new(*i18n.Message))}
	} else if !validAuthState(st.State) {
		st = authStatus{State: authError, Message: i18n.T(i18n.En, "extension-status.auth_unknown_state", i18n.Args{"state": st.State}), hubMessage: true}
	}
	ps.keepAuth(p, st)
	return ps.authView(p, ps.h.contextLang(ctx)), nil
}

// keepAuth keeps st as p's status and tells the apps when it changed.
func (ps *extensions) keepAuth(p *extension.Extension, st authStatus) {
	if !ps.auth.put(p.Record.ID, st) || ps.h == nil {
		return
	}
	ps.h.broadcast(map[string]any{"type": "extension_auth", "extension": p.Record.ID, "auth": ps.authView(p), "hub_auth_message": st.hubMessage})
}

// forgetAuth drops what is known of id's sign-in (its program stopped, it was removed) and tells the apps.
func (ps *extensions) forgetAuth(id string) {
	if _, _, had := ps.auth.get(id); !had {
		return
	}
	ps.auth.drop(id)
	if ps.h == nil {
		return
	}
	if p := ps.reg.Get(id); p != nil && p.Full.Auth != nil {
		ps.h.broadcast(map[string]any{"type": "extension_auth", "extension": id, "auth": ps.authView(p)})
	}
}

// needsSignIn says whether extension id signs in and is not signed in (an install's next step is to).
func (h *Manager) needsSignIn(ctx context.Context, id string) bool {
	ps := h.extensions
	v, err := ps.askAuth(ctx, id)
	if err != nil || v == nil {
		return false
	}
	return v.State == authSignedOut || v.State == authExpired
}

// authWarning is the line old apps show for an extension that is not signed in.
func authWarning(l i18n.Lang, v *ExtensionAuth) string {
	switch {
	case v == nil:
		return ""
	case v.State == authSignedOut:
		return i18n.T(l, "hub-extension_auth.warn_signed_out", i18n.Args{"title": v.Title})
	case v.State == authExpired:
		return i18n.T(l, "hub-extension_auth.warn_expired", i18n.Args{"title": v.Title})
	}
	return ""
}

var errNoAuth = i18n.ErrEnglish("extension-status.no_auth")

// authQR draws the QR code of a step's URL: a qr step's (to scan with another app) and a device code's (its link,
// for another phone).
func authQR(p *connector.Prompt) error {
	p.QR = nil
	if p.Kind != connector.PromptQRCode && p.Kind != connector.PromptDeviceCode {
		return nil
	}
	if p.URL == "" {
		if p.Kind == connector.PromptQRCode {
			return i18n.ErrEnglish("extension-status.qr_missing_url")
		}
		return nil
	}
	rows, size, err := qrRows(p.URL)
	if err != nil {
		return err
	}
	p.QR = &connector.QRCode{Size: size, Rows: rows}
	return nil
}

func (h *Manager) authRoutes(api *http.ServeMux, full func(f func(w http.ResponseWriter, r *http.Request, p *extension.Extension)) http.HandlerFunc) {
	ps := func() *extensions { return h.extensions }
	noAuth := func(w http.ResponseWriter, r *http.Request, p *extension.Extension) bool {
		if p.Full.Auth != nil {
			return false
		}
		writeExtensionResponseError(w, r, http.StatusNotFound, "no_auth", i18n.Err("hub-extension_auth.no_auth"))
		return true
	}
	// running is p's program to talk to about signing in, or writes why there is none.
	running := func(w http.ResponseWriter, r *http.Request, p *extension.Extension) bool {
		if noAuth(w, r, p) {
			return false
		}
		if !p.Record.Enabled {
			writeExtensionResponseError(w, r, http.StatusConflict, "disabled", i18n.Err("hub-extension_auth.disabled"))
			return false
		}
		if !ps().authRuns(p) {
			writeExtensionResponseError(w, r, http.StatusConflict, "not_running", i18n.Err("hub-extension_auth.not_running"))
			return false
		}
		return true
	}
	api.HandleFunc("GET /api/v1/extensions/{id}/-/auth", full(func(w http.ResponseWriter, r *http.Request, p *extension.Extension) {
		if noAuth(w, r, p) {
			return
		}
		v, err := ps().askAuth(r.Context(), p.Record.ID)
		if err != nil {
			writeExtensionResponseErr(w, r, http.StatusBadGateway, err)
			return
		}
		writeExtensionJSON(w, r, v)
	}))
	api.HandleFunc("POST /api/v1/extensions/{id}/-/auth/step", full(func(w http.ResponseWriter, r *http.Request, p *extension.Extension) {
		if !running(w, r, p) {
			return
		}
		var in struct {
			Step   string            `json:"step"`
			Values map[string]string `json:"values"`
		}
		if err := decodeJSONBody(w, r, &in, 16<<10); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if in.Values == nil {
			in.Values = map[string]string{}
		}
		ctx, cancel := context.WithTimeout(r.Context(), authStepWait)
		defer cancel()
		var pr authPrompt
		if err := ps().get(p.Record.ID).proc.Call(ctx, "auth.step", map[string]any{"step": in.Step, "values": in.Values}, &pr); err != nil {
			writeExtensionResponseErr(w, r, http.StatusBadGateway, err)
			return
		}
		if err := authQR(&pr.Prompt); err != nil {
			writeExtensionResponseErr(w, r, http.StatusBadGateway, err)
			return
		}
		if in.Step == "" {
			ps().reg.Audit(r.Context(), p.Record.ID, "auth_start", actor(r), nil)
		}
		if pr.Done {
			ps().reg.Audit(r.Context(), p.Record.ID, "auth_signed_in", actor(r), nil)
			_, _ = ps().askAuth(context.WithoutCancel(r.Context()), p.Record.ID)
		} else if in.Step == "" {
			_, _ = ps().askAuth(context.WithoutCancel(r.Context()), p.Record.ID) // now pending
		}
		writeExtensionJSON(w, r, pr)
	}))
	api.HandleFunc("POST /api/v1/extensions/{id}/-/auth/logout", full(func(w http.ResponseWriter, r *http.Request, p *extension.Extension) {
		if !running(w, r, p) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), authStepWait)
		defer cancel()
		var st authStatus
		if err := ps().get(p.Record.ID).proc.Call(ctx, "auth.logout", map[string]any{}, &st); err != nil {
			writeExtensionResponseErr(w, r, http.StatusBadGateway, err)
			return
		}
		ps().reg.Audit(r.Context(), p.Record.ID, "auth_logout", actor(r), nil)
		if validAuthState(st.State) {
			ps().keepAuth(p, st)
		} else {
			_, _ = ps().askAuth(context.WithoutCancel(r.Context()), p.Record.ID)
		}
		writeExtensionJSON(w, r, ps().authView(ps().reg.Get(p.Record.ID), h.requestLang(r)))
	}))
}

// authChanged is a program's auth.changed event: ask it again.
func (ps *extensions) authChanged(id string, _ json.RawMessage) {
	go ps.refreshAuth(context.Background(), id)
}

// extensionAuthCommand is `personad extension auth <id> [login|logout]` (extension_cli.go).
func extensionAuthCommand(ctx context.Context, c *extensionClient, args []string, stdin io.Reader, stdout io.Writer) error {
	base := "/api/v1/extensions/" + url.PathEscape(args[0]) + "/-/auth"
	l := c.l()
	say := func(v ExtensionAuth) {
		key := map[string]string{authUnknown: "extmgr-cli.auth_unknown", authSignedOut: "extmgr-cli.auth_signed_out",
			authPending: "extmgr-cli.auth_pending", authSignedIn: "extmgr-cli.auth_signed_in",
			authExpired: "extmgr-cli.auth_expired", authError: "extmgr-cli.auth_error"}[v.State]
		line := v.State
		if key != "" {
			line = i18n.T(l, key)
		}
		if v.Account != "" {
			line = i18n.T(l, "extmgr-cli.auth_account", i18n.Args{"state": line, "account": v.Account})
		}
		if v.Message != "" {
			line += i18n.T(l, "extmgr-cli.paren", i18n.Args{"text": v.Message})
		}
		fmt.Fprintln(stdout, i18n.T(l, "extmgr-cli.label_value", i18n.Args{"label": v.Title, "value": line}))
	}
	what := ""
	if len(args) > 1 {
		what = args[1]
	}
	switch what {
	case "":
		var v ExtensionAuth
		if err := c.do(ctx, "GET", base, nil, &v); err != nil {
			return err
		}
		say(v)
		return nil
	case "logout":
		var v ExtensionAuth
		if err := c.do(ctx, "POST", base+"/logout", map[string]string{}, &v); err != nil {
			return err
		}
		say(v)
		return nil
	case "login":
	default:
		return errors.New("personad extension auth <id> [login|logout]")
	}
	in := bufio.NewReader(stdin)
	step, values := "", map[string]string{}
	shown := ""
	for {
		var p authPrompt
		if err := c.do(ctx, "POST", base+"/step", map[string]any{"step": step, "values": values}, &p); err != nil {
			return err
		}
		if p.Done {
			if p.Account != "" {
				fmt.Fprintln(stdout, i18n.T(l, "extmgr-cli.auth_done_account", i18n.Args{"account": p.Account}))
			} else {
				fmt.Fprintln(stdout, i18n.T(l, "extmgr-cli.auth_done"))
			}
			return nil
		}
		if p.Error != "" {
			fmt.Fprintln(stdout, p.Error)
		}
		step, values = p.Step, map[string]string{}
		switch p.Kind {
		case connector.PromptDeviceCode, connector.PromptQRCode:
			if key := p.URL + "\n" + p.UserCode; key != shown {
				shown = key
				if p.QR != nil {
					printQR(stdout, p.QR.Rows)
				}
				if p.UserCode != "" {
					fmt.Fprintln(stdout, i18n.T(l, "extmgr-cli.auth_open_code", i18n.Args{"url": p.URL, "code": p.UserCode}))
				} else {
					fmt.Fprintln(stdout, i18n.T(l, "extmgr-cli.auth_open", i18n.Args{"url": p.URL}))
				}
				if p.Hint != "" {
					fmt.Fprintln(stdout, p.Hint)
				}
			}
			wait := time.Duration(max(p.Interval, 2)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		default:
			if p.Hint != "" {
				fmt.Fprintln(stdout, p.Hint)
			}
			for _, f := range p.Fields {
				fmt.Fprint(stdout, i18n.T(l, "extmgr-cli.field_prompt", i18n.Args{"label": f.Label})+" ")
				var line string
				if f.Secret {
					if file, ok := stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
						b, err := term.ReadPassword(int(file.Fd()))
						fmt.Fprintln(stdout)
						if err != nil {
							return err
						}
						line = string(b)
					}
				}
				if line == "" {
					read, err := in.ReadString('\n')
					if err != nil && read == "" {
						return err
					}
					line = read
				}
				values[f.Name] = strings.TrimSpace(line)
			}
		}
	}
}

// printQR draws a QR code's rows ("0" / "1") in a terminal, two rows a line, with its quiet zone.
func printQR(w io.Writer, rows []string) {
	if len(rows) == 0 {
		return
	}
	size := len(rows[0])
	at := func(y, x int) bool { return y >= 0 && y < len(rows) && x >= 0 && x < size && rows[y][x] == '1' }
	for y := -2; y < len(rows)+2; y += 2 {
		var b strings.Builder
		for x := -2; x < size+2; x++ {
			top, bottom := at(y, x), at(y+1, x)
			switch { // light on dark terminals: a dark module is a space
			case top && bottom:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bottom:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		fmt.Fprintln(w, b.String())
	}
}
