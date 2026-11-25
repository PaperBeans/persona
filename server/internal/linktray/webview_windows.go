//go:build windows

package linktray

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

// webWindow is a window whose whole client area is a WebView2 showing one of the tray's pages: the panel (a
// borderless flyout over acrylic) or the pairing window (a normal window over Mica). It lives on the tray's UI
// thread; every method must be called there.
type webWindow struct {
	hwnd     uintptr
	web      *edge.Chromium
	flyout   bool
	backdrop bool // the system draws a backdrop behind the page
	ready    bool // the page has loaded and can take scripts
	queue    []string
	// onMessage gets what the page posts (common.js's send), already parsed.
	onMessage func(request)
	scale     float64
}

// request is a message from a page: a command with its arguments, and the id to reply to.
type request struct {
	ID   int             `json:"id"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args"`
}

// webViewData is where WebView2 keeps its profile for the tray (cache, nothing of the user's).
func webViewData() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "Persona", "LinkTray", "WebView2")
}

// RemoveData deletes WebView2's profile for the tray (uninstall).
func RemoveData() error {
	dir := filepath.Dir(webViewData())
	err := os.RemoveAll(dir)
	_ = os.Remove(filepath.Dir(dir)) // %LOCALAPPDATA%\Persona, if nothing else is in it
	return err
}

// webViewInstalled says whether this computer has the WebView2 runtime (Windows 11 always does).
func webViewInstalled() bool {
	_, err := edgeVersion()
	return err == nil
}

// newWebWindow makes the window, hidden, and starts WebView2 in it. html is the page to show.
func newWebWindow(class *uint16, owner uintptr, flyout bool, title string, width, height int32, html string, onMessage func(request)) (*webWindow, error) {
	w := &webWindow{flyout: flyout, onMessage: onMessage, scale: 1}
	style, exStyle := uintptr(wsOverlappedWindow&^wsMaximizeBox), uintptr(wsExAppWindow)
	if flyout {
		style, exStyle = wsPopup, wsExToolWindow|wsExTopmost
	}
	hwnd, _, err := procCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16Ptr(title))),
		style, cwUseDefault, cwUseDefault, uintptr(width), uintptr(height), owner, 0, 0, 0)
	if hwnd == 0 {
		return nil, err
	}
	w.hwnd = hwnd
	w.applyFrame()

	c := edge.NewChromium()
	c.DataPath = webViewData()
	// PERSONA_TRAY_DEVTOOLS turns on the developer tools (F12); a port number also opens remote debugging there, for
	// scripts. go-webview2 clears WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS, so that variable can't do it.
	devtools := os.Getenv("PERSONA_TRAY_DEVTOOLS")
	if _, err := strconv.Atoi(devtools); err == nil {
		c.AdditionalBrowserArgs = []string{"--remote-debugging-port=" + devtools}
	}
	c.SetErrorCallback(func(err error) { log.Printf("webview: %v", err) }) // not its default: exit the program
	c.MessageCallback = func(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		var r request
		if err := json.Unmarshal([]byte(message), &r); err != nil {
			return
		}
		if r.Cmd == "ready" {
			return
		}
		w.onMessage(r)
	}
	c.NavigationCompletedCallback = func(*edge.ICoreWebView2, *edge.ICoreWebView2NavigationCompletedEventArgs) {
		w.ready = true
		for _, s := range w.queue {
			c.Eval(s)
		}
		w.queue = nil
	}
	if !c.Embed(hwnd) {
		procDestroyWindow.Call(hwnd)
		return nil, windows.ERROR_NOT_SUPPORTED
	}
	w.web = c
	if settings, err := c.GetSettings(); err == nil {
		_ = settings.PutAreDefaultContextMenusEnabled(false)
		_ = settings.PutAreDevToolsEnabled(devtools != "")
		_ = settings.PutIsStatusBarEnabled(false)
		_ = settings.PutIsZoomControlEnabled(false)
		_ = settings.PutAreBrowserAcceleratorKeysEnabled(false)
	}
	_ = c.PutIsGeneralAutofillEnabled(false)
	_ = c.PutIsPasswordAutosaveEnabled(false)
	_ = c.PutIsSwipeNavigationEnabled(false)
	w.applyBackground()
	w.setScale(float64(dpiForWindow(hwnd)) / 96)
	c.Resize()
	c.NavigateToString(html)
	return w, nil
}

// applyFrame asks DWM for the window's look: rounded corners, dark title bar in dark mode, and the backdrop (acrylic
// for the flyout, Mica for the window) over the whole client area.
func (w *webWindow) applyFrame() {
	corner := uint32(dwmwcpRound)
	dwmSet(w.hwnd, dwmwaCornerPreference, corner)
	dark := appsDark()
	if w.flyout {
		dark = systemDark()
	}
	var on uint32
	if dark {
		on = 1
	}
	dwmSet(w.hwnd, dwmwaUseImmersiveDarkMode, on)
	w.backdrop = false
	if backdropAvailable() {
		kind := uint32(dwmsbtMainWindow)
		if w.flyout {
			kind = dwmsbtTransientWindow
		}
		m := margins{-1, -1, -1, -1}
		procDwmExtendFrameIntoClientArea.Call(w.hwnd, uintptr(unsafe.Pointer(&m)))
		w.backdrop = dwmSet(w.hwnd, dwmwaSystemBackdropType, kind)
	}
}

// applyBackground makes WebView2 transparent over a backdrop, else the page's own solid color shows.
func (w *webWindow) applyBackground() {
	if w.backdrop {
		w.web.SetBackgroundColour(0, 0, 0, 0)
		return
	}
	// Until the page paints, the window shows this: the solid surface of the theme, never a white flash.
	dark := appsDark()
	if w.flyout {
		dark = systemDark()
	}
	if dark {
		w.web.SetBackgroundColour(0x2C, 0x2C, 0x2C, 0xFF)
	} else {
		w.web.SetBackgroundColour(0xF9, 0xF9, 0xF9, 0xFF)
	}
}

// theme is the page's theme for this window now.
func (w *webWindow) theme() Theme {
	if w.flyout {
		return themeFor(systemDark(), w.backdrop)
	}
	return themeFor(appsDark(), w.backdrop)
}

// themeChanged follows the system's light/dark, transparency and accent.
func (w *webWindow) themeChanged() {
	w.applyFrame()
	w.applyBackground()
	w.send(map[string]any{"type": "theme", "theme": w.theme()})
}

// setScale makes the page's CSS pixels the monitor's: 1 at 100 %, 1.5 at 150 %.
func (w *webWindow) setScale(scale float64) {
	w.scale = scale
	if c3 := w.web.GetController().GetICoreWebView2Controller3(); c3 != nil {
		// Not c3.PutRasterizationScale: it passes a pointer to the scale where the method takes the double itself.
		c3.Vtbl.PutRasterizationScale.Call(uintptr(unsafe.Pointer(c3)), uintptr(math.Float64bits(scale)))
	}
}

// send hands msg to the page, now if it has loaded, else once it has.
func (w *webWindow) send(msg any) {
	s, err := Script(msg)
	if err != nil {
		return
	}
	if !w.ready {
		w.queue = append(w.queue, s)
		return
	}
	w.web.Eval(s)
}

// reply answers a request: data on success, the error's text otherwise.
func (w *webWindow) reply(id int, data any, err error) {
	m := map[string]any{"type": "reply", "id": id, "data": data}
	if err != nil {
		m["error"] = err.Error()
	}
	w.send(m)
}

// place moves and sizes the window (physical pixels) and fits WebView2 to it.
func (w *webWindow) place(x, y, width, height int32, flags uint32) {
	after := uintptr(0)
	if w.flyout {
		after = hwndTopmost
	} else {
		flags |= swpNoZOrder
	}
	setWindowPos(w.hwnd, after, x, y, width, height, flags)
	w.web.Resize()
	_ = w.web.NotifyParentWindowPositionChanged()
}

func (w *webWindow) visible() bool { return windowVisible(w.hwnd) }

func (w *webWindow) hide() {
	procShowWindow.Call(w.hwnd, swHide)
	_ = w.web.GetController().PutIsVisible(false)
}

// show brings the window up, in front, with the keyboard in the page.
func (w *webWindow) show() {
	_ = w.web.GetController().PutIsVisible(true)
	procShowWindow.Call(w.hwnd, swShow)
	procSetForegroundWindow.Call(w.hwnd)
	w.web.Focus()
}

func (w *webWindow) destroy() {
	w.web.ShuttingDown()
	procDestroyWindow.Call(w.hwnd)
}

// edgeVersion is the installed WebView2 runtime's version; an error when there is none.
func edgeVersion() (string, error) {
	v, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err == nil && v == "" {
		err = windows.ERROR_FILE_NOT_FOUND
	}
	return v, err
}
