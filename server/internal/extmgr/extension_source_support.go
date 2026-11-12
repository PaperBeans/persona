package extmgr

import (
	"runtime"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// sourceRuntimeOS is the system source extensions would run on (runtime.GOOS; tests replace it).
var sourceRuntimeOS = runtime.GOOS

// cantRunHere is why this manager cannot run m now, nil when it can (#5639). Source extensions (runtime.mode
// "deno") run only on Linux, where their namespace sandbox and kernel resource limits exist, with the pinned runtime
// manager.json's extension_runtime names, which no installer sets up. The catalog marks such entries before anything
// is downloaded and market previews refuse them with this reason, instead of the install failing at its check step
// (macOS) or the extension failing to start after it (Linux without a runtime).
func (h *Manager) cantRunHere(m *extension.Manifest) error {
	if m == nil || m.Runtime == nil || m.Runtime.Mode != extension.RuntimeDeno {
		return nil
	}
	switch {
	case sourceRuntimeOS != "linux":
		return i18n.Err("hub-extension_market.source_needs_linux")
	case h.cfg == nil || h.cfg.ExtensionRuntime == nil:
		return i18n.Err("hub-extension_market.source_needs_runtime")
	}
	return nil
}
