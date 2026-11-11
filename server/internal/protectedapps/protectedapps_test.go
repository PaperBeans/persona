package protectedapps

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIs(t *testing.T) {
	for _, c := range []struct {
		id, name string
		want     bool
	}{
		{"com.apple.systempreferences", "Réglages Système", true},
		{"", "System Settings", true},
		{"name:系统设置", "系统设置", true},
		{"", "com.apple.SecurityAgent", true},
		{"", "coreautha", true},
		{"com.apple.calculator", "Calculator", false},
		{"", "Settings", false},
	} {
		if got := Is(c.id, c.name); got != c.want {
			t.Errorf("%q %q: %v", c.id, c.name, got)
		}
	}
}

// A window's pid is matched to its app, so System Settings counts under any name; a pid the list lacks asks for a
// fresh list once; a window off the screen is not counted.
func TestOnScreen(t *testing.T) {
	apps := Apps(json.RawMessage(`{"apps": [{"name": "Calculator", "bundle_id": "com.apple.calculator", "pid": 63}]}`))
	windows, ok := Windows(json.RawMessage(`{"windows": [
		{"app_name": "Calculator", "pid": 63, "is_on_screen": true},
		{"app_name": "Réglages Système", "pid": 70, "is_on_screen": true},
		{"app_name": "SecurityAgent", "pid": 98, "is_on_screen": false}]}`))
	if !ok {
		t.Fatal("windows")
	}
	asked := 0
	found, latest := OnScreen(windows, apps, func() []App {
		asked++
		return append(apps, App{Name: "Réglages Système", BundleID: "com.apple.systempreferences", PID: 70})
	})
	if strings.Join(found, ",") != "Réglages Système" || asked != 1 || len(latest) != 2 {
		t.Fatalf("found %v, asked %d, %d apps", found, asked, len(latest))
	}
	if _, ok := Windows(json.RawMessage(`{"ok": true}`)); ok {
		t.Fatal("not a window list")
	}
}

func TestScriptAndStepsApps(t *testing.T) {
	if apps, all := ScriptApps(`const a = await cua.getApp("Calculator"); await cua.launch('Notes')`); strings.Join(apps, ",") != "Calculator,Notes" || !all {
		t.Errorf("literal: %v %v", apps, all)
	}
	if _, all := ScriptApps(`const a = await cua.getApp(name)`); all {
		t.Error("by expression: not all known")
	}
	if apps, all := StepsApps("Notes", json.RawMessage(`[{"launch_app": {"bundle_id": "com.apple.systempreferences"}}, {"click": {"name": "x"}}]`)); strings.Join(apps, ",") != "com.apple.systempreferences,Notes" || !all {
		t.Errorf("steps: %v %v", apps, all)
	}
	if !UnnamedSteps("", json.RawMessage(`[{"press_key": {"key": "return"}}]`)) || UnnamedSteps("Notes", json.RawMessage(`[{"press_key": {"key": "return"}}]`)) {
		t.Error("unnamed steps")
	}
}

func TestActs(t *testing.T) {
	for tool, want := range map[string]bool{"click": true, "run_script": true, "some_new_tool": true, "list_windows": false, "get_window_state": false} {
		if Acts(tool) != want {
			t.Errorf("%s: %v", tool, !want)
		}
	}
}
