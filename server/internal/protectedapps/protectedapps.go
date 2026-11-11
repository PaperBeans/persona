// Package protectedapps is the rule that Persona never operates System Settings or macOS's permission and
// authorization prompts for anyone (#4336, #4349; docs/extensions/computer-use.md "c) Per-App approval"). macOS's
// privacy permissions are meant to be answered by a person. The assistant's computer_run (extmgr) and the Agents' own
// Cua Driver tools (the node's PreToolUse gate) both go by this package: which apps, how a call names the apps it
// touches, and whether one of those apps has a window on the screen.
package protectedapps

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// ids: System Settings (both ids it has had) and the processes that show macOS's permission and authorization
// prompts: the privacy alerts, administrator sheets, Touch ID / password prompts, and the Accessibility alert of older
// systems.
var ids = map[string]bool{
	"com.apple.systempreferences": true, "com.apple.settings": true,
	"com.apple.usernotificationcenter": true, "com.apple.securityagent": true,
	"com.apple.localauthentication.uiagent": true, "com.apple.accessibility.universalaccessauthwarn": true,
}

// names: the same by the name a call or a window gives (case aside), in the languages the app ships plus the
// prompts' process names, for apps Cua Driver's list does not carry (the prompts run only while they show).
var names = []string{
	"system settings", "system preferences",
	// i18n-ignore: the names macOS itself gives System Settings, matched against what a call or a window says
	"系统设置", "系統設定", "システム設定", "系统偏好设置", "システム環境設定",
	"usernotificationcenter", "securityagent", "coreautha", "universalaccessauthwarn",
}

// Is says whether an app is one Persona never operates: by bundle id, or by name (a name that is a bundle id counts as
// one). A bundle id that is not a real one ("name:…", "") is matched by the name alone.
func Is(bundleID, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return ids[strings.ToLower(strings.TrimSpace(bundleID))] || ids[name] || slices.Contains(names, name)
}

// App is one of Cua Driver's list_apps: bundle id, name, and pid while running.
type App struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id"`
	PID      int    `json:"pid"`
	Running  bool   `json:"running"`
}

// Window is one of Cua Driver's list_windows.
type Window struct {
	AppName  string `json:"app_name"`
	PID      int    `json:"pid"`
	OnScreen bool   `json:"is_on_screen"`
}

// Apps reads list_apps' answer (nil when it does not read).
func Apps(raw json.RawMessage) []App {
	var out struct {
		Apps []App `json:"apps"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Apps
}

// Windows reads list_windows' answer; ok is false when it has no window list at all (not a list_windows answer).
func Windows(raw json.RawMessage) (windows []Window, ok bool) {
	var out struct {
		Windows *[]Window `json:"windows"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Windows == nil {
		return nil, false
	}
	return *out.Windows, true
}

// AppOf is the running app with that pid in apps, ok false when none is.
func AppOf(apps []App, pid int) (App, bool) {
	if pid <= 0 {
		return App{}, false
	}
	for _, a := range apps {
		if a.PID == pid {
			return a, true
		}
	}
	return App{}, false
}

// OnScreen is the protected apps with a window on the screen, by name. A window's pid is matched to its app through
// apps, so System Settings counts in any language; one whose pid apps lacks (started since) makes it call more once
// for a fresh list, which it returns for the caller to keep (apps as it was otherwise).
func OnScreen(windows []Window, apps []App, more func() []App) (found []string, latest []App) {
	latest = apps
	refreshed := false
	for _, w := range windows {
		if !w.OnScreen {
			continue
		}
		app, has := AppOf(latest, w.PID)
		if !has && !refreshed && w.PID > 0 && more != nil {
			latest, refreshed = more(), true
			app, has = AppOf(latest, w.PID)
		}
		name := w.AppName
		if has && app.Name != "" {
			name = app.Name
		}
		if Is(app.BundleID, name) || Is("", w.AppName) {
			if n := firstOf(name, app.BundleID); !slices.Contains(found, n) {
				found = append(found, n)
			}
		}
	}
	return found, latest
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var (
	getAppLiteral = lazyre.New(`getApp\(\s*(?:"([^"\\]+)"|'([^'\\]+)')\s*\)`)
	launchLiteral = lazyre.New(`launch\(\s*(?:"([^"\\]+)"|'([^'\\]+)')\s*\)`)
	// getAppWord and launchWord count every occurrence of the callee's name. A call the literal patterns cannot
	// read (space or a comment before the parentheses, a computed callee, an argument that is not a plain string)
	// then makes the script's apps unknown instead of being missed: comparing substring counts of "getApp(" would
	// miss the same calls the literal pattern misses, and judge the script fully known.
	getAppWord = lazyre.New(`\bgetApp\b`)
	launchWord = lazyre.New(`\blaunch\b`)
)

// ScriptApps is the apps a Cua run_script names with literal cua.getApp("…") / cua.launch("…") calls, and whether
// that is all of them (false: one named by expression, a call the literal reader cannot read, or none at all).
func ScriptApps(script string) (apps []string, all bool) {
	add := func(a string) {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(apps, a) {
			apps = append(apps, a)
		}
	}
	for _, m := range getAppLiteral.FindAllStringSubmatch(script, -1) {
		add(m[1] + m[2])
	}
	for _, m := range launchLiteral.FindAllStringSubmatch(script, -1) {
		add(m[1] + m[2])
	}
	return apps, len(apps) > 0 && len(getAppWord.FindAllString(script, -1)) == len(getAppLiteral.FindAllString(script, -1)) &&
		len(launchWord.FindAllString(script, -1)) == len(launchLiteral.FindAllString(script, -1))
}

// StepsApps is the apps Cua run_steps steps name (each step's own app, else the call's), an app a launch_app step
// opens included, and whether that is all they may touch (false: a step with no app of its own or the call's, a
// launch_app naming no app).
func StepsApps(app string, steps json.RawMessage) (apps []string, all bool) {
	add := func(a string) {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(apps, a) {
			apps = append(apps, a)
		}
	}
	var list []map[string]json.RawMessage
	_ = json.Unmarshal(steps, &list)
	all = len(list) > 0
	for _, s := range list {
		if opened, ok := LaunchTarget(s); ok {
			if opened == "" {
				all = false
			} else {
				add(opened)
			}
			continue
		}
		found := stepApp(s)
		if found == "" {
			found = app
		}
		if found == "" {
			all = false
			continue
		}
		add(found)
	}
	return apps, all
}

// stepApp is the app a step names itself, in either form ({"click": {"app": …}} or {"tool": …, "args": {"app": …}}).
func stepApp(s map[string]json.RawMessage) string {
	found := ""
	for k, raw := range s {
		if k == "wait_for" || k == "expect" || k == "tool" {
			continue
		}
		var named struct {
			App string `json:"app"`
		}
		if json.Unmarshal(raw, &named) == nil && strings.TrimSpace(named.App) != "" {
			found = named.App
		}
	}
	return found
}

// LaunchTarget is the app a launch_app step opens, {"launch_app": {"name": …}} or {"tool": "launch_app", "args": {…}}:
// its name or bundle id, "" when it names neither (not the call's default app: what opens then is the backend's
// choice). ok is false for any other step.
func LaunchTarget(s map[string]json.RawMessage) (opened string, ok bool) {
	var tool string
	args := s["launch_app"]
	if raw, has := s["tool"]; has {
		_ = json.Unmarshal(raw, &tool)
	}
	switch {
	case tool == "launch_app":
		args = s["args"]
	case tool != "" || args == nil:
		return "", false
	}
	var a struct {
		Name     string `json:"name"`
		BundleID string `json:"bundle_id"`
	}
	_ = json.Unmarshal(args, &a)
	if opened = strings.TrimSpace(a.Name); opened == "" {
		opened = strings.TrimSpace(a.BundleID)
	}
	return opened, true
}

// UnnamedSteps: some step names no app of its own and the call names none either (it acts on whatever has the
// focus, or where it points).
func UnnamedSteps(app string, steps json.RawMessage) bool {
	if strings.TrimSpace(app) != "" {
		return false
	}
	var list []map[string]json.RawMessage
	_ = json.Unmarshal(steps, &list)
	for _, s := range list {
		if opened, ok := LaunchTarget(s); ok {
			if opened == "" {
				return true
			}
			continue
		}
		if stepApp(s) == "" {
			return true
		}
	}
	return false
}

// reads are Cua Driver's tools that only look (or manage Cua itself) and never act on an app. Any other tool, one
// added later included, counts as acting.
var reads = map[string]bool{
	"get_window_state": true, "get_desktop_state": true, "get_accessibility_tree": true, "get_browser_state": true,
	"list_apps": true, "list_windows": true, "get_screen_size": true, "get_cursor_position": true, "zoom": true,
	"parse_visual_regions": true, "verify_state": true, "check_permissions": true, "get_config": true, "get_guide": true,
	"health_report": true, "check_for_update": true, "get_session": true, "get_session_state": true, "list_sessions": true,
	"get_recording_state": true, "get_agent_cursor_state": true, "start_session": true, "end_session": true,
	"history_status": true, "history_query": true,
}

// Acts says whether a Cua Driver tool may act on an app (anything but the reads).
func Acts(tool string) bool { return !reads[tool] }

// Refusal is what the model is told when a call is refused on where (a Mac's name, or "this Mac") for why; it passes
// the gist on to the user.
func Refusal(where, why string) string {
	return "Refused on " + where + ": " + why + ". Persona never operates System Settings or macOS's permission and " +
		"password prompts for anyone. Tell the user to do this themselves, on the Mac or through its live screen in the " +
		"Persona app. Do not retry it or look for another way."
}

// UnreadableScript is what the model is told about a script whose apps cannot all be read.
const UnreadableScript = "Refused: this script names the apps it operates by expression, or names none, so they " +
	"cannot be checked. Name each one with a literal cua.getApp(\"…\") or cua.launch(\"…\"), or use steps."

// Unchecked is what the model is told when the windows on the screen could not be read to check.
const Unchecked = "Nothing was done: the Mac's windows could not be read to check for System Settings or a " +
	"permission prompt. Try again shortly."
