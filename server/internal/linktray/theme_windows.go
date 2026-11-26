//go:build windows

package linktray

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/nwverse/persona/server/internal/i18n"
)

// How Windows looks right now: light or dark (for the taskbar and flyouts, and separately for apps), whether
// transparency effects are on, the accent color, and whether this Windows can draw Mica and acrylic behind a window.

const personalize = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

func regDWORD(path, name string, fallback uint32) uint32 {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return fallback
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return fallback
	}
	return uint32(v)
}

// systemDark: the taskbar and its flyouts are dark ("Windows mode").
func systemDark() bool { return regDWORD(personalize, "SystemUsesLightTheme", 0) == 0 }

// appsDark: windows are dark ("App mode").
func appsDark() bool { return regDWORD(personalize, "AppsUseLightTheme", 1) == 0 }

// backdropAvailable: Windows 11 22H2 or later draws a system backdrop (DWMWA_SYSTEMBACKDROP_TYPE), and the user has
// not turned transparency effects off.
func backdropAvailable() bool {
	v := windows.RtlGetVersion()
	if v.MajorVersion < 10 || v.BuildNumber < 22621 {
		return false
	}
	return regDWORD(personalize, "EnableTransparency", 1) != 0
}

// accentPalette is the system accent in its seven shades, lightest first (Explorer\Accent\AccentPalette: eight
// RGBA entries, the last unused); ok is false when there is none.
func accentPalette() (shades [7][3]byte, ok bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, registry.QUERY_VALUE)
	if err != nil {
		return shades, false
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue("AccentPalette")
	if err != nil || len(b) < 28 {
		return shades, false
	}
	for i := range shades {
		shades[i] = [3]byte{b[4*i], b[4*i+1], b[4*i+2]}
	}
	return shades, true
}

func hexColor(c [3]byte) string { return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]) }

// themeFor is the pages' theme, dark or light: the accent shades Windows' own controls use in that theme (fill:
// Light2 in dark, Dark1 in light; text: Light3 / Dark2), and the color on an accent fill.
func themeFor(dark, backdrop bool) Theme {
	t := Theme{Dark: dark, Backdrop: backdrop}
	shades, ok := accentPalette()
	if !ok {
		return t
	}
	const light3, light2, light1, base, dark1, dark2 = 0, 1, 2, 3, 4, 5
	if dark {
		t.Accent, t.AccentHover, t.AccentText, t.OnAccent = hexColor(shades[light2]), hexColor(shades[light1]), hexColor(shades[light3]), "#000000"
	} else {
		t.Accent, t.AccentHover, t.AccentText, t.OnAccent = hexColor(shades[dark1]), hexColor(shades[base]), hexColor(shades[dark2]), "#FFFFFF"
	}
	return t
}

// uiLanguage is the language of the user's Windows, as one of ours (en when it is none of them).
func uiLanguage() i18n.Lang {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err == nil {
		for _, tag := range langs {
			if l := i18n.Resolve(tag); l != "" {
				return l
			}
		}
	}
	return i18n.Fallback
}
