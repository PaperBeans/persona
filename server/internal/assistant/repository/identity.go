// Package repository normalizes remote references and checks existence metadata.
// It never clones, fetches source, or establishes a caller's repository permissions.
package repository

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Identity struct {
	Provider     string `json:"provider"`
	InstanceURL  string `json:"instance_url"`
	Path         string `json:"path"`
	CanonicalURL string `json:"canonical_url"`
}

var ErrRemote = errors.New("invalid repository remote")

// Parse accepts HTTPS and the conventional git SSH user, never passwords,
// query strings or fragments. Custom instances require an explicit provider.
// SSH ports other than 22 cannot imply an HTTPS API port and are refused.
func Parse(remote, provider string) (Identity, error) {
	bad := func() (Identity, error) { return Identity{}, ErrRemote }
	if len(remote) > 2048 || remote == "" || strings.ContainsAny(remote, " \t\r\n\\%?#") {
		return bad()
	}
	if strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		host, path, ok := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		if !ok || strings.ContainsAny(host, "/:@") {
			return bad()
		}
		remote = "ssh://git@" + host + "/" + path
	}
	u, err := url.Parse(remote)
	if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return bad()
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return bad()
		}
	case "ssh":
		if u.User == nil || u.User.String() != "git" || u.Port() != "" && u.Port() != "22" {
			return bad()
		}
	default:
		return bad()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || len(host) > 253 {
		return bad()
	}
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return bad()
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return bad()
				}
			}
		}
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if u.Scheme == "https" && u.Port() != "" && u.Port() != "443" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1024 || port > 65535 || strconv.Itoa(port) != u.Port() {
			return bad()
		}
		authority = net.JoinHostPort(host, u.Port())
	}
	known := ""
	switch host {
	case "github.com":
		known = "github"
	case "gitlab.com":
		known = "gitlab"
	}
	if provider == "" {
		provider = known
	}
	if provider != "github" && provider != "gitlab" || known != "" && provider != known {
		return bad()
	}
	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || provider == "github" && len(parts) != 2 || len(parts) > 20 || len(path) > 512 {
		return bad()
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".git") || strings.HasPrefix(part, "-") || strings.HasPrefix(part, ".") {
			return bad()
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-", r)) {
				return bad()
			}
		}
	}
	path = strings.ToLower(path)
	instance := "https://" + authority
	return Identity{Provider: provider, InstanceURL: instance, Path: path, CanonicalURL: instance + "/" + path}, nil
}
