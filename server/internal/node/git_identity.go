package node

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// gitIdentity reads only local metadata. Unlike full status, it never scans changed files or commit history.
func (n *Node) gitIdentity(ctx context.Context, cwd string) (proto.RepositoryIdentity, error) {
	// Resolve before gitDir cleans dot segments: link/.. must follow the actual
	// filesystem, rather than appear to stay inside an authorized checkout.
	if cwd == "~" || strings.HasPrefix(cwd, "~/") {
		cwd = n.home + strings.TrimPrefix(cwd, "~")
	}
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return proto.RepositoryIdentity{}, err
	}
	dir, err := n.gitDir(physical)
	if err != nil {
		return proto.RepositoryIdentity{}, err
	}
	dir = realPath(dir)
	result := proto.RepositoryIdentity{Version: 1, Cwd: dir, PathResolved: true}
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, nil
	}
	result.Root = realPath(strings.TrimSpace(root))
	common, err := git(ctx, result.Root, "rev-parse", "--git-common-dir")
	if err != nil {
		return proto.RepositoryIdentity{}, err
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(result.Root, common)
	}
	result.CommonDir, result.IsRepo = realPath(common), true
	if head, err := git(ctx, result.Root, "rev-parse", "--verify", "HEAD^{commit}"); err == nil {
		result.HeadOID = strings.TrimSpace(head)
	}
	if origin, err := git(ctx, result.Root, "config", "--get", "remote.origin.url"); err == nil {
		result.Origin = canonicalGitOrigin(strings.TrimSpace(origin), result.Root)
	}
	return result, nil
}

// canonicalGitOrigin treats standard SSH and HTTPS transports as the same host/path, discarding credentials.
// A custom SSH account can name another home directory, so it stays distinct through a non-reversible digest.
func canonicalGitOrigin(remote, root string) string {
	if remote == "" || strings.ContainsAny(remote, "\r\n\x00") {
		return ""
	}
	if !strings.Contains(remote, "://") {
		if strings.Contains(remote, "::") {
			return ""
		}
		if at := strings.Index(remote, ":"); at > 0 && !filepath.IsAbs(remote) {
			prefix, repository := remote[:at], remote[at+1:]
			if strings.Contains(prefix, "/") || repository == "" {
				return ""
			}
			user, host := "", prefix
			if before, after, ok := strings.Cut(prefix, "@"); ok {
				user, host = before, after
			}
			return gitNetworkOrigin(host, repository, user)
		}
		if !filepath.IsAbs(remote) {
			remote = filepath.Join(root, remote)
		}
		return "file:" + realPath(remote)
	}
	u, err := url.Parse(remote)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if u.Scheme == "file" {
		if u.Host != "" && u.Host != "localhost" {
			return ""
		}
		return "file:" + realPath(u.Path)
	}
	if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "git" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !(u.Scheme == "ssh" && port == "22") && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "git" && port == "9418") {
		host = net.JoinHostPort(host, port)
	}
	user := ""
	if u.Scheme == "ssh" && u.User != nil {
		user = u.User.Username()
	}
	return gitNetworkOrigin(host, u.Path, user)
}

func gitNetworkOrigin(host, repository, user string) string {
	host = strings.ToLower(host)
	repository = strings.TrimSuffix(strings.TrimSuffix(strings.Trim(repository, "/"), "/"), ".git")
	if host == "" || repository == "" || path.Clean(repository) != repository || strings.ContainsAny(host+repository, "\r\n\x00?#") {
		return ""
	}
	principal := ""
	if user != "" && user != "git" {
		digest := sha256.Sum256([]byte(user))
		principal = fmt.Sprintf("account-%x@", digest[:16])
	}
	return principal + host + "/" + repository
}
