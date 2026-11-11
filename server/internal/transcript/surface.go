package transcript

import "strings"

// The client an agent runs in, as the apps draw it beside the agent's mark (a </> badge on the command line's).
const (
	SurfaceDesktop = "desktop"
	SurfaceCLI     = "cli"
)

// ClaudeSurface is the client of a Claude Code transcript line's entrypoint: "cli" in a terminal, "claude-desktop"
// (and "claude-desktop-3p", the desktop app on a third-party provider) in Claude's desktop app. The SDK's ("sdk-cli",
// "sdk-ts"…) and anything newer say neither.
func ClaudeSurface(entrypoint string) string {
	switch {
	case entrypoint == "cli":
		return SurfaceCLI
	case strings.HasPrefix(entrypoint, "claude-desktop"):
		return SurfaceDesktop
	}
	return ""
}

// ClaudeOneShot: a Claude Code transcript's entrypoint is a one-shot run of its command line by a program (`claude -p`,
// "sdk-cli"): a script at work, not a session of the user's. The Agent SDKs' ("sdk-ts", "sdk-py") are not: interactive
// hosts (Paseo, ACP clients) run the user's own sessions through them.
func ClaudeOneShot(entrypoint string) bool { return entrypoint == "sdk-cli" }

// CodexSurface is the client of a Codex rollout's originator (its session_meta): "Codex Desktop" is the Codex App,
// "codex-tui" / "codex_cli_rs" the command line. `codex exec` and the SDKs over it say neither.
func CodexSurface(originator string) string {
	switch originator {
	case "Codex Desktop":
		return SurfaceDesktop
	case "codex-tui", "codex_cli_rs":
		return SurfaceCLI
	}
	return ""
}
