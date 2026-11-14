package github

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// The account the connector reads as, and what was read as it (#7832).
//
// A token does not say whose it is: GitHub does (/user). So a token new to the connector — the first one, one a
// computer hands over after GitHub refused the last, one from another source — reads or answers nothing that
// belongs to an account until GitHub has named its account (credential), and the connector counts the accounts it
// has read as: the epoch. What is read as one account (the inbox's things, the notifications cursor, the answers
// kept for If-None-Match) belongs to the epoch it was read under. A change of account retires and resets all of it
// in one step (adopt); a read or a reply that began under the epoch before is refused before it is sent (asEpoch)
// and writes nothing back. What belongs to no account (a tool's read, a write the user allowed) goes out with
// the token in use as before.

// errAccountChanged: the connector reads as another account than the one a read or a reply began under.
var errAccountChanged = errors.New("the GitHub account changed: read it again")

// account is whose the token in memory is. c.mu guards it.
type account struct {
	// login is the account GitHub last named for a token ("" until it has); known says it is the token in memory's.
	login string
	known bool
	// epoch counts the changes of login: what account-scoped state is read under.
	epoch uint64
	// cred counts the credentials: every token put in memory or dropped, every source chosen. Answers come back in
	// any order: one to a credential since replaced says nothing of the account now.
	cred uint64
}

// newCredential puts tok in memory ("" drops the one there): nobody knows whose it is until GitHub says. c.mu is
// held.
func (c *Connector) newCredential(tok string) uint64 {
	c.token = tok
	c.acct.cred++
	c.acct.known = false
	return c.acct.cred
}

// How often credential looks again when the credential is replaced under it (a source chosen while a computer or
// GitHub was answering).
const credentialTries = 4

// tokenNow is the token in memory and its credential, fetching one only when there is none or GitHub refused the
// one there (refused): a machine that went offline leaves the one in memory in use. What was fetched for a
// credential since replaced is dropped: the source it came from may no longer be the one chosen.
func (c *Connector) tokenNow(ctx context.Context, refused string) (string, uint64, error) {
	for range credentialTries {
		c.mu.Lock()
		tok, cred, source, fetch := c.token, c.acct.cred, c.source, c.fetch
		c.mu.Unlock()
		if tok != "" && tok != refused {
			return tok, cred, nil
		}
		var fresh string
		switch {
		case source == SourceDevice:
			if fresh = c.deviceState(ctx).Token; fresh == "" {
				return "", cred, errNoDeviceLogin
			}
		case fetch == nil:
			return "", cred, ErrNoMachine
		default:
			var err error
			if fresh, err = fetch(ctx, c.cfg.host()); err != nil {
				return "", cred, err
			}
		}
		c.mu.Lock()
		if c.acct.cred != cred {
			c.mu.Unlock()
			continue
		}
		if fresh != tok {
			cred = c.newCredential(fresh)
		}
		c.mu.Unlock()
		return fresh, cred, nil
	}
	return "", 0, errAccountChanged
}

// How much credential must know of whose the token is.
const (
	tokenAsIs  = iota // the token in use, whoever's: what belongs to no account
	tokenKnown        // GitHub has named its account: what is read or answered as an account (asEpoch)
	tokenAsked        // GitHub is asked now, even for a known one (check)
)

// credential is the token to send and the account epoch it reads as. For tokenKnown a token GitHub has not named
// the account of is asked about first, and nothing is sent with it before. refused is a token GitHub just answered
// 401 to: the machine may have signed in again since, so it is asked once more. The last result is the credential
// the answer or the error belongs to.
func (c *Connector) credential(ctx context.Context, refused string, need int) (tok string, epoch, cred uint64, err error) {
	again := refused != ""
	for range credentialTries {
		if tok, cred, err = c.tokenNow(ctx, refused); err != nil {
			return "", 0, cred, err
		}
		c.mu.Lock()
		known := c.acct.known && c.acct.cred == cred
		epoch = c.acct.epoch
		c.mu.Unlock()
		if need == tokenAsIs || need == tokenKnown && known {
			return tok, epoch, cred, nil
		}
		var u struct {
			Login string `json:"login"`
		}
		code, h, askErr := c.roundTrip(ctx, http.MethodGet, "/user", nil, nil, &u, tok)
		if code == http.StatusUnauthorized && !again {
			refused, again = tok, true
			continue
		}
		if askErr == nil && u.Login == "" {
			askErr = errors.New("GitHub GET /user: no login in the answer")
		}
		if askErr != nil {
			return "", 0, cred, askErr
		}
		c.mu.Lock()
		taken := c.adopt(cred, u.Login, h)
		epoch = c.acct.epoch
		c.mu.Unlock()
		if taken {
			return tok, epoch, cred, nil
		}
		refused = "" // the credential was replaced while GitHub answered: look at the one there now
	}
	return "", 0, cred, errAccountChanged
}

// adopt records GitHub's answer to whose the credential cred is: login, and h, the answer's headers (the token's
// scopes). An answer to a credential since replaced is dropped (false): it would put the account before back.
// A login other than the last is another account: everything read as the last one is retired and reset here, in
// the same step that makes the new one the account in use, so nothing read as one is ever served or answered as
// the other. The same login again (a token refreshed, the same account from another source) keeps everything.
// c.mu is held; the inbox's and the cache's own locks are taken under it, never the other way round.
func (c *Connector) adopt(cred uint64, login string, h http.Header) bool {
	if cred != c.acct.cred {
		return false
	}
	now := time.Now()
	if c.acct.login != "" && !strings.EqualFold(c.acct.login, login) {
		c.acct.epoch++
		c.inbox.retire(c.acct.epoch, now)
		c.etags.reset(c.acct.epoch)
	}
	c.acct.login, c.acct.known = login, true
	c.scopes, c.scopesKnown = scopesOf(h)
	c.status = connector.Status{State: connector.StateOK, Account: login, LastSync: now.UTC(), LastEvent: c.status.LastEvent}
	return true
}

// epochNow is the epoch of the account the connector reads as.
func (c *Connector) epochNow() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acct.epoch
}

type epochKey struct{}

// asEpoch binds everything asked under the returned context to the account epoch: a request is refused
// (errAccountChanged) before it is sent once the connector reads as another account, so one poll, one thread page
// or one reply is all one account's or fails. A context bound already keeps its epoch.
func asEpoch(ctx context.Context, epoch uint64) context.Context {
	if _, bound := boundEpoch(ctx); bound {
		return ctx
	}
	return context.WithValue(ctx, epochKey{}, epoch)
}

func boundEpoch(ctx context.Context) (uint64, bool) {
	epoch, ok := ctx.Value(epochKey{}).(uint64)
	return epoch, ok
}
