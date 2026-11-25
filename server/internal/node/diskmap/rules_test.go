package diskmap

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

var (
	aDir  = facts{exists: true, dir: true}
	aFile = facts{exists: true, regular: true}
)

type ruleCase struct {
	path    string
	f       facts
	trash   string // the refusal for trash ("" allowed)
	offload string
}

func runRules(t *testing.T, e ruleEnv, cases []ruleCase) {
	t.Helper()
	for _, c := range cases {
		if got := e.refusal(c.path, proto.StoragePurposeTrash, c.f); got != c.trash {
			t.Errorf("%s %s trash: %q, want %q", e.goos, c.path, got, c.trash)
		}
		if got := e.refusal(c.path, proto.StoragePurposeOffload, c.f); got != c.offload {
			t.Errorf("%s %s offload: %q, want %q", e.goos, c.path, got, c.offload)
		}
	}
}

func TestRulesDarwin(t *testing.T) {
	e := ruleEnv{goos: "darwin", home: "/Users/me", caches: map[string]string{
		"go-build": "/Users/me/Library/Caches/go-build", "go-mod": "/Users/me/go/pkg/mod", "npm-cache": "/Users/me/.npm/_cacache",
		"xcode-derived-data": "/Users/me/Library/Developer/Xcode/DerivedData"},
		agents: []string{"/Users/me/work/agent-home"}, persona: []string{"/Users/me/.local/share/persona-node"}}
	runRules(t, e, []ruleCase{
		{"/Users/me/Movies/trip.mov", aFile, "", ""},
		{"/Users/me/Documents/Old Project", aDir, "", ""},
		{"/users/ME/documents/old", aDir, "", ""}, // case-insensitive
		{"/Users/me/missing", facts{}, proto.RefusalNotFound, proto.RefusalNotFound},
		{"/Users/me/fifo", facts{exists: true}, proto.RefusalNotRegular, proto.RefusalNotRegular},
		{"/Users/me/link", facts{exists: true, link: true}, proto.RefusalSymlink, proto.RefusalSymlink},
		{"/tmp/x", aFile, proto.RefusalOutsideHome, proto.RefusalOutsideHome},
		{"/Users/other/x", aFile, proto.RefusalOutsideHome, proto.RefusalOutsideHome},
		{"/Users/me", aDir, proto.RefusalHomeRoot, proto.RefusalHomeRoot},
		{"/Users/me/Desktop", aDir, proto.RefusalStandardFolder, proto.RefusalStandardFolder},
		{"/Users/me/downloads", aDir, proto.RefusalStandardFolder, proto.RefusalStandardFolder},
		{"/Users/me/Library", aDir, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/Users/me/Library/Preferences/x.plist", aFile, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/Users/me/Library/Caches/com.example.app", aDir, "", proto.RefusalAppManaged},
		{"/Users/me/Library/Caches/com.example.app/blob", aFile, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/Users/me/Library/Caches/go-build", aDir, "", proto.RefusalAppManaged},
		{"/Users/me/Library/Developer/Xcode/DerivedData", aDir, "", proto.RefusalAppManaged},
		{"/Users/me/Library/Developer/Xcode/DerivedData/App-abc", aDir, "", proto.RefusalAppManaged},
		{"/Users/me/Library/Developer/CoreSimulator", aDir, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/Users/me/Library/Caches/com.apple.Safari", aDir, proto.RefusalSystemCache, proto.RefusalSystemCache},
		{"/Users/me/Library/Caches/CloudKit", aDir, proto.RefusalSystemCache, proto.RefusalSystemCache},
		{"/Users/me/Library/Keychains/login.keychain-db", aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/Users/me/Library/Mobile Documents/com~apple~CloudDocs/a.pdf", aFile, proto.RefusalCloudSynced, proto.RefusalCloudSynced},
		{"/Users/me/Library/CloudStorage/Drive/a.pdf", aFile, proto.RefusalCloudSynced, proto.RefusalCloudSynced},
		{"/Users/me/Dropbox/a.pdf", aFile, proto.RefusalCloudSynced, proto.RefusalCloudSynced},
		{"/Users/me/Applications/Tool.app", aDir, proto.RefusalAppBundle, ""},
		{"/Users/me/.Trash/old.zip", aFile, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/Applications/Example.app", aDir, proto.RefusalAppBundle, ""},
		{"/Applications/Example.app/Contents/MacOS/x", aFile, proto.RefusalInsideApp, proto.RefusalInsideApp},
		{"/Users/me/Downloads/Other.app", aDir, proto.RefusalAppBundle, ""},
		{"/Users/me/.config", aDir, proto.RefusalDotFolder, proto.RefusalDotFolder},
		{"/Users/me/.local/state/x", aFile, proto.RefusalDotFolder, proto.RefusalDotFolder},
		{"/Users/me/.cache/huggingface", aDir, "", proto.RefusalDotFolder},
		{"/Users/me/.npm/_cacache", aDir, "", proto.RefusalDotFolder},
		{"/Users/me/go/pkg/mod", aDir, "", ""},
		{"/Users/me/.ssh", aDir, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/Users/me/.ssh/id_ed25519", aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/Users/me/.aws/credentials", aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/Users/me/.claude/projects", aDir, proto.RefusalAgentData, proto.RefusalAgentData},
		{"/Users/me/.codex", aDir, proto.RefusalAgentData, proto.RefusalAgentData},
		{"/Users/me/work/agent-home/x", aFile, proto.RefusalAgentData, proto.RefusalAgentData},
		{"/Users/me/work", aDir, proto.RefusalAgentData, proto.RefusalAgentData}, // it holds an Agent's folder
		{"/Users/me/.persona/data", aDir, proto.RefusalPersonaData, proto.RefusalPersonaData},
		{"/Users/me/.local/share/persona-node/db", aFile, proto.RefusalPersonaData, proto.RefusalPersonaData},
		{"/Users/me/Pictures/Photos Library.photoslibrary", aDir, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
		{"/Users/me/VMs/Ubuntu.utm", aDir, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
		{"/Users/me/Downloads/big.iso.crdownload", aFile, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
		{"/Users/me/vm/Docker.raw", aFile, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
		{"/Users/me/Volumes-here/disk", facts{exists: true, dir: true, mount: true}, proto.RefusalMountPoint, proto.RefusalMountPoint},
	})
}

func TestRulesLinux(t *testing.T) {
	e := ruleEnv{goos: "linux", home: "/home/me", caches: map[string]string{"pip-cache": "/home/me/.cache/pip", "cargo-registry": "/home/me/.cargo/registry"}}
	runRules(t, e, []ruleCase{
		{"/home/me/Videos/a.mkv", aFile, "", ""},
		{"/home/me/videos/a.mkv", aFile, "", ""},
		{"/home/me/Documents", aDir, proto.RefusalStandardFolder, proto.RefusalStandardFolder},
		{"/home/me/Templates", aDir, proto.RefusalStandardFolder, proto.RefusalStandardFolder},
		{"/home/me/.cache/pip", aDir, "", proto.RefusalDotFolder},
		{"/home/me/.cache/thumbnails", aDir, "", proto.RefusalDotFolder},
		{"/home/me/.cache/thumbnails/x.png", aFile, proto.RefusalDotFolder, proto.RefusalDotFolder},
		{"/home/me/.cargo/registry", aDir, "", proto.RefusalDotFolder},
		{"/home/me/.local/share/Trash/files/x", aFile, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{"/home/me/.local/share/keyrings/login.keyring", aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/home/me/.kube/config", aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{"/home/me/Library/x", aFile, "", ""}, // only macOS has ~/Library
		{"/home/me/Tool.app", aDir, "", ""},   // nor app bundles
		{"/var/log/x", aFile, proto.RefusalOutsideHome, proto.RefusalOutsideHome},
		{"/home/me", aDir, proto.RefusalHomeRoot, proto.RefusalHomeRoot},
		{"/home/me/disk.qcow2", aFile, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
	})
}

func TestRulesWindows(t *testing.T) {
	e := ruleEnv{goos: "windows", home: `C:\Users\Me`, caches: map[string]string{"npm-cache": `C:\Users\Me\AppData\Local\npm-cache\_cacache`}}
	runRules(t, e, []ruleCase{
		{`C:\Users\Me\Videos\a.mp4`, aFile, "", ""},
		{`c:\users\me\videos\a.mp4`, aFile, "", ""},
		{`C:\Users\Me\Documents`, aDir, proto.RefusalStandardFolder, proto.RefusalStandardFolder},
		{`C:\Users\Me`, aDir, proto.RefusalHomeRoot, proto.RefusalHomeRoot},
		{`D:\data\x`, aFile, proto.RefusalOutsideHome, proto.RefusalOutsideHome},
		{`C:\Users\Me\AppData\Roaming\App\settings.json`, aFile, proto.RefusalAppManaged, proto.RefusalAppManaged},
		{`C:\Users\Me\AppData\Local\Temp\setup-123`, aDir, "", proto.RefusalAppManaged},
		{`C:\Users\Me\AppData\Local\npm-cache\_cacache`, aDir, "", proto.RefusalAppManaged},
		{`C:\Users\Me\AppData\Roaming\Microsoft\Credentials\x`, aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{`C:\Users\Me\OneDrive\a.docx`, aFile, proto.RefusalCloudSynced, proto.RefusalCloudSynced},
		{`C:\Users\Me\OneDrive - Example\a.docx`, aFile, proto.RefusalCloudSynced, proto.RefusalCloudSynced},
		{`C:\Users\Me\.ssh\id_rsa`, aFile, proto.RefusalCredentials, proto.RefusalCredentials},
		{`C:\Users\Me\.codex\x`, aFile, proto.RefusalAgentData, proto.RefusalAgentData},
		{`C:\Users\Me\VMs\disk.vhdx`, aFile, proto.RefusalManagedLibrary, proto.RefusalManagedLibrary},
		{`C:\Users\Me\link`, facts{exists: true, link: true}, proto.RefusalSymlink, proto.RefusalSymlink},
	})
}

func TestRulesEveryCodeIsKnown(t *testing.T) {
	// Every code the rules answer is in the closed list, and every code in the list is reachable (the cases above).
	seen := map[string]bool{}
	for _, c := range append(append([]string{}, proto.StorageRefusals...), "") {
		seen[c] = true
	}
	e := ruleEnv{goos: "darwin", home: "/Users/me"}
	for _, p := range []string{"/Users/me/a", "/x", "/Users/me", "/Users/me/.ssh"} {
		if r := e.refusal(p, proto.StoragePurposeTrash, aFile); !seen[r] {
			t.Errorf("%s: unknown code %q", p, r)
		}
	}
}

func TestCategoryAndDisplay(t *testing.T) {
	e := ruleEnv{goos: "darwin", home: "/Users/me", caches: map[string]string{"go-mod": "/Users/me/go/pkg/mod"}}
	for p, want := range map[string]string{
		"/Users/me/Documents/a":         proto.UsageCategoryPersonal,
		"/Users/me/Library/Caches/x":    proto.UsageCategoryCache,
		"/Users/me/Library/Preferences": proto.UsageCategoryAppData,
		"/Users/me/.config":             proto.UsageCategoryAppData,
		"/Users/me/.cache/x":            proto.UsageCategoryCache,
		"/Users/me/go/pkg/mod":          proto.UsageCategoryCache,
		"/Users/me/go/pkg/mod/x":        proto.UsageCategoryCache,
		"/Applications/X.app":           proto.UsageCategoryApps,
		"/Library/Fonts":                proto.UsageCategorySystem,
	} {
		if got := e.category(p); got != want {
			t.Errorf("category(%s) = %s, want %s", p, got, want)
		}
	}
	if d := e.display("/Users/me/Documents/a"); d != "~/Documents/a" {
		t.Errorf("display %s", d)
	}
	w := ruleEnv{goos: "windows", home: `C:\Users\Me`}
	if d := w.display(`C:\Users\Me\Videos\a.mp4`); d != "~/Videos/a.mp4" {
		t.Errorf("windows display %s", d)
	}
	if c := w.category(`C:\Users\Me\AppData\Local\Temp\x`); c != proto.UsageCategoryCache {
		t.Errorf("windows temp category %s", c)
	}
}
