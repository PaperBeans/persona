package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// lcuReleaseAPI is the GitHub release Persona installs LCU from. The archive is checked against the release's
// .sha256 sidecar before anything from it runs. Install uses the release's own install.sh --runtime-only, which
// records the ChatGPT app's Node and does not register Claude, Codex, or any other harness (those would show
// LCU's own approval UI).
var lcuReleaseAPI = "https://api.github.com/repos/amontlabs/lcu/releases/latest"

// lcuInstallRun runs the checked installer. Tests record the root instead of executing it.
var lcuInstallRun = runLCUInstaller

// lcuArchiveMost is a ceiling for one release archive (the published ones are about 6 MB).
const lcuArchiveMost = 80 << 20

func installLCU(ctx context.Context, step func(string)) error {
	if installOS != "darwin" && installOS != "linux" {
		return errors.New("LCU 目前只在 Mac 和 Linux 上安装")
	}
	if _, err := lcuAssetSuffix(installOS, installArch); err != nil {
		return err
	}
	if lcuBin() != "" {
		return errors.New("已经装好了")
	}
	if !chatgptApp() {
		return errors.New("先安装官方 ChatGPT 桌面版（https://chatgpt.com/download ）。LCU 用它自带的运行时，Persona 不会下载或登录它")
	}
	step("下载 LCU")
	asset, err := lcuReleaseAsset(ctx)
	if err != nil {
		return err
	}
	archive, err := fetchLimited(ctx, asset.URL, lcuArchiveMost, "")
	if err != nil {
		return fmt.Errorf("下载 LCU：%w", err)
	}
	step("校验安装包")
	sum, err := fetchLimited(ctx, asset.SumURL, 1<<16, "")
	if err != nil {
		return fmt.Errorf("下载校验：%w", err)
	}
	if err := checkSHA256(archive, sum); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "persona-lcu-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	step("解压")
	root, err := extractLCU(dir, archive)
	if err != nil {
		return err
	}
	if err := checkLCUInstaller(filepath.Join(root, "scripts", "install.sh")); err != nil {
		return err
	}
	step("安装 LCU")
	return lcuInstallRun(ctx, root)
}

// lcuInstallArgs is the only command the install runs. It must stay --runtime-only: an --agent flag would register
// a harness and put approvals on that harness's own screen.
func lcuInstallArgs(root string) []string {
	return []string{"/bin/bash", filepath.Join(root, "scripts", "install.sh"), "--runtime-only", "--yes"}
}

func runLCUInstaller(ctx context.Context, root string) error {
	args := lcuInstallArgs(root)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(lcuLastLines(msg, 8))
	}
	return nil
}

type lcuAsset struct {
	URL    string
	SumURL string
}

func lcuReleaseAsset(ctx context.Context) (lcuAsset, error) {
	suffix, err := lcuAssetSuffix(installOS, installArch)
	if err != nil {
		return lcuAsset{}, err
	}
	body, err := fetchLimited(ctx, lcuReleaseAPI, 1<<20, "application/vnd.github+json")
	if err != nil {
		return lcuAsset{}, fmt.Errorf("查询 LCU 版本：%w", err)
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(body, &rel) != nil {
		return lcuAsset{}, errors.New("LCU 的版本说明不是 JSON")
	}
	var archiveName, archiveURL, sumURL string
	for _, a := range rel.Assets {
		if strings.HasPrefix(a.Name, "lcu-") && strings.HasSuffix(a.Name, "-"+suffix+".tar.gz") {
			archiveName, archiveURL = a.Name, a.URL
		}
	}
	for _, a := range rel.Assets {
		if a.Name == archiveName+".sha256" {
			sumURL = a.URL
		}
	}
	if archiveURL == "" || sumURL == "" {
		return lcuAsset{}, fmt.Errorf("这个版本没有 %s 的安装包", suffix)
	}
	return lcuAsset{URL: archiveURL, SumURL: sumURL}, nil
}

// lcuAssetSuffix is the platform part of a release file name, without the version.
func lcuAssetSuffix(goos, goarch string) (string, error) {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "darwin-arm64", nil
	case goos == "linux" && goarch == "arm64":
		return "linux-arm64", nil
	case goos == "linux" && (goarch == "amd64" || goarch == "x64"):
		return "linux-x64", nil
	default:
		return "", errors.New("LCU 没有这个系统的安装包")
	}
}

func fetchLimited(ctx context.Context, url string, most int64, accept string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "persona")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, most+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > most {
		return nil, errors.New("下载超过了大小限制")
	}
	return body, nil
}

func checkSHA256(archive, sidecar []byte) error {
	want := ""
	for _, f := range strings.Fields(string(sidecar)) {
		if len(f) == 64 {
			want = strings.ToLower(f)
			break
		}
	}
	if want == "" {
		return errors.New("校验文件里没有 SHA256")
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != want {
		return errors.New("安装包的校验没通过")
	}
	return nil
}

type lcuArchiveLink struct {
	name, target string
}

// The public v0.13.6 wrapper forwards every argument to the adjacent Node installer. Pin its bytes so accepting
// this form does not turn a marker in an arbitrary shell script into permission to run it. The release checksum is
// delivered by the same source as the archive, not an independent signature.
var lcuForwardingWrapperHashes = []string{
	"7d5791b0df7240df70ff75ea959be70103cc0c40ae405ef06df4a442d3b2b2e4",
}

// extractLCU unpacks into an empty private staging directory and returns the directory containing scripts/install.sh.
// Regular files retain their non-privileged permission bits when they are not group- or other-writable. Links are
// created only after all files and directories, so no archive link can become an ancestor of a write; their complete
// graph must stay inside the staging root.
func extractLCU(dir string, archive []byte) (string, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("LCU staging directory is not private")
	}
	stage, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer stage.Close()
	stageDir, err := stage.Open(".")
	if err != nil {
		return "", err
	}
	openedInfo, statErr := stageDir.Stat()
	names, readErr := stageDir.Readdirnames(1)
	closeErr := stageDir.Close()
	if statErr != nil {
		return "", statErr
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if !os.SameFile(info, openedInfo) || len(names) != 0 {
		return "", errors.New("LCU staging directory changed or is not empty")
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", errors.New("安装包不是 gzip")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var root string
	var links []lcuArchiveLink
	seen := make(map[string]bool)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name := pathClean(h.Name)
		if name == "" || strings.HasPrefix(name, "..") || filepath.IsAbs(h.Name) || strings.Contains(h.Name, `\`) {
			return "", errors.New("安装包里的路径不安全")
		}
		if seen[name] {
			return "", errors.New("LCU archive contains a duplicate path")
		}
		seen[name] = true
		if h.Mode < 0 || h.Mode&^int64(0o777) != 0 {
			return "", errors.New("LCU archive contains privileged permission bits")
		}
		dest := filepath.FromSlash(name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := stage.MkdirAll(dest, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Mode&0o022 != 0 {
				return "", errors.New("LCU archive file is writable by group or others")
			}
			if h.Size < 0 || h.Size > lcuArchiveMost {
				return "", errors.New("An LCU archive file exceeds the size limit")
			}
			if err := stage.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", err
			}
			f, err := stage.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(f, tr)
			if copyErr == nil {
				// Chmod the open file so the archive mode is exact even with a restrictive umask.
				copyErr = f.Chmod(os.FileMode(h.Mode))
			}
			if err := f.Close(); copyErr == nil {
				copyErr = err
			}
			if copyErr != nil {
				return "", copyErr
			}
			if strings.HasSuffix(name, "/scripts/install.sh") || name == "scripts/install.sh" {
				root = filepath.Join(dir, filepath.Dir(filepath.Dir(dest)))
			}
		case tar.TypeSymlink:
			if h.Linkname == "" || path.IsAbs(h.Linkname) || filepath.IsAbs(h.Linkname) ||
				filepath.VolumeName(h.Linkname) != "" || strings.ContainsAny(h.Linkname, "\\\x00") {
				return "", errors.New("LCU archive contains an unsafe symlink")
			}
			if err := stage.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", err
			}
			links = append(links, lcuArchiveLink{name: name, target: h.Linkname})
		default:
			return "", errors.New("LCU archive contains an unsupported entry type")
		}
	}
	if root == "" {
		return "", errors.New("安装包里没有 scripts/install.sh")
	}
	for _, link := range links {
		if err := stage.Symlink(link.target, filepath.FromSlash(link.name)); err != nil {
			return "", fmt.Errorf("Could not create an LCU archive symlink: %w", err)
		}
	}
	if err := checkLCUArchiveLinks(stage, links); err != nil {
		return "", err
	}
	return root, nil
}

// Resolve components in the completed graph using the staging filesystem's own name matching (including case and
// Unicode aliases). Missing components stay in the stack: a later-created in-tree target may make a link valid.
func checkLCUArchiveLinks(stage *os.Root, links []lcuArchiveLink) error {
	for _, link := range links {
		var stack []string
		if parent := path.Dir(link.name); parent != "." {
			stack = strings.Split(parent, "/")
		}
		pending := strings.Split(link.target, "/")
		followed := 0
		for len(pending) > 0 {
			part := pending[0]
			pending = pending[1:]
			switch part {
			case "", ".":
				continue
			case "..":
				if len(stack) == 0 {
					return errors.New("LCU archive contains an unsafe symlink")
				}
				stack = stack[:len(stack)-1]
			default:
				stack = append(stack, part)
				candidate := filepath.FromSlash(strings.Join(stack, "/"))
				info, err := stage.Lstat(candidate)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return fmt.Errorf("Could not inspect an LCU archive symlink: %w", err)
				}
				if info.Mode()&os.ModeSymlink != 0 {
					followed++
					if followed > 40 {
						return errors.New("LCU archive contains a cyclic or overly long symlink")
					}
					target, err := stage.Readlink(candidate)
					if err != nil || target == "" || path.IsAbs(target) || filepath.IsAbs(target) ||
						filepath.VolumeName(target) != "" || strings.ContainsAny(target, "\\\x00") {
						return errors.New("LCU archive contains an unsafe symlink")
					}
					stack = stack[:len(stack)-1]
					pending = append(strings.Split(target, "/"), pending...)
				}
			}
		}
	}
	return nil
}

func pathClean(name string) string {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	return pathCleanSlash(name)
}

func pathCleanSlash(name string) string {
	parts := strings.Split(name, "/")
	var out []string
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return ".."
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

func checkLCUInstaller(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return errors.New("安装包里没有 scripts/install.sh")
	}
	if !bytes.HasPrefix(b, []byte("#!")) {
		return errors.New("安装器不是脚本")
	}
	if bytes.Contains(b, []byte("--runtime-only")) {
		return nil
	}
	// A known forwarding wrapper has no runtime-only literal itself. Require its adjacent regular installer to
	// expose the supported source shape; this detects missing interfaces, not arbitrary JavaScript behavior.
	wrapperSum := sha256.Sum256(b)
	for _, hash := range lcuForwardingWrapperHashes {
		if hex.EncodeToString(wrapperSum[:]) != hash {
			continue
		}
		target := filepath.Join(filepath.Dir(path), "install.mjs")
		info, err := os.Lstat(target)
		if err != nil || !info.Mode().IsRegular() {
			break
		}
		if info.Size() > lcuInstallerSourceMost {
			break
		}
		f, err := os.Open(target)
		if err != nil {
			break
		}
		installer, readErr := io.ReadAll(io.LimitReader(f, lcuInstallerSourceMost+1))
		closeErr := f.Close()
		if readErr == nil && closeErr == nil && lcuForwardingInstallerCompatible(installer) {
			return nil
		}
	}
	return errors.New("LCU release installer format is not supported; update Persona and try again")
}

func lcuLastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
