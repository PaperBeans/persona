package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/link"
)

// explain turns the two failures of a pairing a person can fix from here into one plain sentence that says how,
// with the raw error kept on the next line for debugging (#5045): the hub cannot be reached, or it refused the
// pairing link. Anything else is printed as it was.
func explain(err error) string {
	raw := err.Error()
	var se *link.SSHError
	switch {
	case rejected(err):
		return "hub 不认这条配对链接（过期了、用过了或被撤销了）：在 hub 机器上运行 personad hub pair --machine <这台的 id> --add 拿一条新链接，再 persona-link pair '<链接>'\n（原始错误：" + raw + "）"
	case errors.As(err, &se):
		return se.Error()
	case unreachable(err):
		return "连不上 hub（" + strings.Join(hostsOf(err), "、") + "）：检查 hub 在不在跑、地址对不对、这台电脑的网络通不通，再试一次\n（原始错误：" + raw + "）"
	}
	return raw
}

// rejected: some candidate's hub answered the pairing with "no" to its token. link.Pair says so as text, "The hub rejected:"
// and the hub's answer: its 401 body ("invalid token") or, from a proxy with no JSON, the status ("401 Unauthorized").
// A 403 (a link for another machine) or a 429 is a different matter and stays as it is.
func rejected(err error) bool {
	for _, e := range leavesOf(err) {
		s := e.Error()
		if i := strings.LastIndex(s, "hub 拒绝了："); i >= 0 {
			answer := s[i+len("hub 拒绝了："):]
			if answer == "invalid token" || strings.HasPrefix(answer, "401 ") {
				return true
			}
		}
	}
	return false
}

// unreachable: every candidate's request to the hub failed with no answer from a hub (refused, unreachable, no such
// name, or timed out: a hub that is up but hung reads the same here). Only requests count (each failure carries its URL), so a local network error elsewhere is not
// taken for the hub's.
func unreachable(err error) bool {
	leaves := leavesOf(err)
	for _, e := range leaves {
		if !errors.As(e, new(*url.Error)) || !networkFailure(e) {
			return false
		}
	}
	return len(leaves) > 0
}

// networkFailure: no connection to talk over. A failed TCP dial is an *net.OpError with Op "dial" on every platform
// (Windows' refused connection is WSAECONNREFUSED, which syscall.ECONNREFUSED does not match); TLS failures are not.
func networkFailure(err error) bool {
	var oe *net.OpError
	var dns *net.DNSError
	var ne net.Error
	return errors.As(err, &oe) && oe.Op == "dial" || errors.As(err, &dns) ||
		errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// leavesOf is err split at each errors.Join (one per candidate address in a pairing race).
func leavesOf(err error) []error {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			var out []error
			for _, part := range joined.Unwrap() {
				out = append(out, leavesOf(part)...)
			}
			return out
		}
	}
	return []error{err}
}

// hostsOf is the host:port of each request that failed, in order, once each.
func hostsOf(err error) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range leavesOf(err) {
		var ue *url.Error
		if !errors.As(e, &ue) {
			continue
		}
		if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" && !seen[u.Host] {
			seen[u.Host] = true
			out = append(out, u.Host)
		}
	}
	return out
}

// nodeHint follows "The hub cannot connect to this computer's node (...)": how to check the resident node, unless the error already says
// (link.nodeMissing names the command that runs it).
func nodeHint(nodeErr string) string {
	if strings.Contains(nodeErr, link.NodeDownMarker) {
		return ""
	}
	return "：运行 persona-link node status 看常驻 node 在不在跑"
}
