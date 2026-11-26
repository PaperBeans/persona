package larksync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Open-platform answer codes the readers act on.
const (
	codeRateLimited              = 99991400 // the gateway's frequency limit (often on an HTTP 400)
	codeChatRateLimited          = 230020   // a chat's own limit
	codeDriveDownloadRateLimited = 1061045  // drive/v1/files/:file_token/download frequency limit (5 QPS)
)

// tokenGoneCodes are answers that mean the user access token itself was refused (expired, invalid, revoked or for
// another app): the token func is asked once more, since the manager refreshes it.
var tokenGoneCodes = map[int]bool{99991661: true, 99991663: true, 99991664: true, 99991668: true, 99991677: true}

// scopeCodes are answers that mean a scope is missing: not granted by the owner, or not approved by the tenant admin.
// The resource stops; the others go on.
var scopeCodes = map[int]bool{99991672: true, 99991676: true, 99991679: true}

// maxJSONBody bounds a JSON answer; documents' block lists are paged well below it.
const maxJSONBody = 32 << 20

// session is one run's (or one Media fetch's) way to the open platform: the base URL, the token func and the budget's
// deadline (zero: none).
type session struct {
	s        *Syncer
	base     string
	token    func(ctx context.Context) (string, error)
	deadline time.Time
}

// request is one open-platform call.
type request struct {
	family string
	op     string // for errors: "list chats", "docx blocks"…
	method string // "" is GET
	path   string // escaped
	query  map[string]string
	body   any // JSON, POST only
}

func (rq request) url(base string) string {
	u := base + rq.path
	if len(rq.query) == 0 {
		return u
	}
	q := url.Values{}
	for k, v := range rq.query {
		q.Set(k, v)
	}
	return u + "?" + q.Encode()
}

// answer is one HTTP answer, read in full (bounded).
type answer struct {
	status int
	header http.Header
	body   []byte
}

// getJSON makes rq and decodes the envelope's data into out (nil: ignore it).
func (ss *session) getJSON(ctx context.Context, rq request, out any) error {
	_, err := ss.exchange(ctx, rq, maxJSONBody, false, out)
	return err
}

// download makes rq and returns its body as a file: any 200 is the file, whatever its content type (a .json file
// someone sent comes labelled application/json), as the official SDK reads downloads.
func (ss *session) download(ctx context.Context, rq request, limit int64) ([]byte, string, error) {
	a, err := ss.exchange(ctx, rq, limit, true, nil)
	if err != nil {
		return nil, "", err
	}
	return a.body, a.header.Get("Content-Type"), nil
}

// exchange makes rq with the limiter, the token func, one retry with a fresh token on a refused token, and backoff
// on rate limits and passing failures. raw: a 200 is the answer as it is (a download); otherwise the JSON envelope's
// code decides and its data goes into out.
func (ss *session) exchange(ctx context.Context, rq request, limit int64, raw bool, out any) (answer, error) {
	s := ss.s
	method := rq.method
	if method == "" {
		method = http.MethodGet
	}
	var payload []byte
	if rq.body != nil {
		var err error
		if payload, err = json.Marshal(rq.body); err != nil {
			return answer{}, err
		}
	}
	retriedToken := false
	limited, transient := 0, 0
	for {
		if err := s.lim.wait(ctx, rq.family, s.sleep); err != nil {
			return answer{}, err
		}
		tok, err := ss.token(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return answer{}, ctx.Err()
			}
			if e := asSyncError(err); e != nil {
				return answer{}, e
			}
			return answer{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindAuth, Op: "token", Msg: err.Error()}
		}
		a, err := ss.once(ctx, method, rq.url(ss.base), payload, tok, limit)
		var fail *larksyncwire.Error
		switch {
		case err != nil && ctx.Err() != nil:
			return answer{}, ctx.Err()
		case err != nil:
			if errors.Is(err, botcard.ErrTooLarge) {
				return answer{}, err
			}
			fail = &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: rq.op, Msg: err.Error()}
		default:
			fail = classify(rq.op, a, raw, out)
		}
		if fail == nil {
			return a, nil
		}
		var retries *int
		maxRetries := 0
		switch fail.Kind {
		case larksyncwire.ErrKindAuth:
			if !retriedToken {
				retriedToken = true
				continue // the token func refreshes; one more try with what it gives
			}
			return answer{}, fail
		case larksyncwire.ErrKindRateLimited:
			retries, maxRetries = &limited, maxLimitedRetries
		case larksyncwire.ErrKindTransient:
			retries, maxRetries = &transient, maxTransientRetries
		default:
			return answer{}, fail
		}
		if *retries >= maxRetries {
			return answer{}, fail
		}
		d := s.backoff(*retries, fail.RetryAfter)
		if !ss.deadline.IsZero() && s.now().Add(d).After(ss.deadline) {
			return answer{}, fail // the wait would outlast the run's budget: the resource is read next time
		}
		*retries++
		if err := s.sleep(ctx, d); err != nil {
			return answer{}, err
		}
	}
}

// once makes one HTTP request and reads its answer, at most limit bytes (botcard.ErrTooLarge beyond).
func (ss *session) once(ctx context.Context, method, u string, payload []byte, tok string, limit int64) (answer, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := ss.s.http.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && resp.ContentLength > limit {
		return answer{}, fmt.Errorf("%w: larger than %d bytes", botcard.ErrTooLarge, limit)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return answer{}, err
	}
	if int64(len(b)) > limit {
		if resp.StatusCode == http.StatusOK {
			return answer{}, fmt.Errorf("%w: larger than %d bytes", botcard.ErrTooLarge, limit)
		}
		b = b[:limit]
	}
	return answer{status: resp.StatusCode, header: resp.Header, body: b}, nil
}

// classify is a's failure, or nil when a is a success (and then out holds its data).
func classify(op string, a answer, raw bool, out any) *larksyncwire.Error {
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if raw && a.status == http.StatusOK {
		return nil
	}
	jerr := json.Unmarshal(a.body, &env)
	after := retryWait(a.header, a.status, env.Code)
	fail := func(kind string) *larksyncwire.Error {
		msg := env.Msg
		if msg == "" {
			msg = "HTTP " + strconv.Itoa(a.status)
		}
		return &larksyncwire.Error{Kind: kind, Op: op, Code: env.Code, HTTPStatus: a.status, Msg: msg, RetryAfter: after}
	}
	switch {
	case a.status == http.StatusUnauthorized || tokenGoneCodes[env.Code]:
		return fail(larksyncwire.ErrKindAuth)
	case a.status == http.StatusTooManyRequests || env.Code == codeRateLimited || env.Code == codeChatRateLimited || env.Code == codeDriveDownloadRateLimited:
		return fail(larksyncwire.ErrKindRateLimited)
	case a.status >= 500:
		return fail(larksyncwire.ErrKindTransient)
	case scopeCodes[env.Code]:
		return fail(larksyncwire.ErrKindScope)
	case jerr != nil:
		e := fail(larksyncwire.ErrKindOther)
		e.Msg = "HTTP " + strconv.Itoa(a.status) + ": not JSON"
		return e
	case env.Code != 0 || a.status < 200 || a.status >= 300:
		return fail(larksyncwire.ErrKindOther)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: op, Msg: "decode: " + err.Error()}
	}
	return nil
}

// retryWait is the wait a throttled answer names: Retry-After (seconds) or x-ogw-ratelimit-reset, the seconds until
// the gateway's window resets, whichever is longer; a chat's own limit is a second.
// https://open.feishu.cn/document/server-docs/api-call-guide/frequency-control
func retryWait(h http.Header, status, code int) time.Duration {
	var after time.Duration
	for _, name := range []string{"Retry-After", "X-Ogw-Ratelimit-Reset"} {
		if secs, err := strconv.Atoi(strings.TrimSpace(h.Get(name))); err == nil && secs > 0 {
			after = max(after, time.Duration(secs)*time.Second)
		}
	}
	if code == codeChatRateLimited || code == codeDriveDownloadRateLimited {
		after = max(after, time.Second)
	}
	if status != http.StatusTooManyRequests && code != codeRateLimited && code != codeChatRateLimited && code != codeDriveDownloadRateLimited {
		return 0
	}
	return after
}

// pathEsc escapes one path segment (a token or an id).
func pathEsc(s string) string { return url.PathEscape(s) }
