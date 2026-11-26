package runtimepkg

import (
	"os"
	"testing"
)

// TestDistBudget bounds what personad, persona-node and persona-manager embed of the runtime packages: every file
// Generate writes from integrations/. The complete Antigravity package, including the image guard (#11790),
// measured 192,060 B; preserving delivered replies through a background wake (#11756) added 1,822 B. Claude Code
// plugin 0.2.0 session settings overrides brought the total to 196,211 B; busy owner input queueing + turn interrupts
// add 2,129 B on their branch. Main measured 196,937 B on 2026-11-21, and keeping an Antigravity send from rewriting
// the conversation's history (#12014) adds 738 B (199,188 B ceiling). Embedding persona-opencode-runtime
// (opencode/package.json, index.js, companion.mjs, lib/*.js including #12504/#12545; shared runtime-sdk blob reused)
// measured 248,704 B on 2026-11-21. Session-bound A2A tool bridges bring the generated total to about 251.3 kB.
// Minifying the remaining non-compat Antigravity lib sources (acp.js, file-links.js, models.js; #12781, #13123)
// brings the generated total from 254,085 B back to 251,311 B under the unchanged 252,000 B ceiling.
// Antigravity's empty-response retry and interrupted-turn notices (#13957) use the remaining room and add 850 B.
// Runtime fixes merged on 2026-11-24 add 872 B (253,709 B): the runtime-sdk's shell gates reading quoted $(…) and
// $'…' words (#14040, 574 B), opencode's JSONC comma-brace strings and provider:org/model split (#14091, 189 B), pi's
// top-level oldText/newText (#14092, 11 B) and paseo's WebSocket RSV-bit and Close-payload checks (#14095, 98 B).
// OpenCode live ACP tool-call normalization (#14130) adds 383 B on main (254,092 B).
// The dedicated-Assistant environment handoff (#14068) adds 114 B (254,206 B).
// Claude Code plugin 0.3.0 permission / question / plan approval bridging (#14214) adds 6,363 B (260,569 B).
// Antigravity multi-instance companion flags and --agy-dir env propagation (#14483) add 373 B (260,942 B).
// An Antigravity CLI without session/set_mode: checks skip the switch and starts say to update agy (#14533), 149 B (261,091 B).
// Antigravity Gemini API-key gateway model discovery routes through HTTP(S)_PROXY and honors NO_PROXY (#14552), 1,497 B (262,588 B).
// Keeping Antigravity steps at a reused lower index when the export goes on past them, dropping them again once the run re-covers them (#14626), adds 230 B (262,818 B).
// Antigravity native error backfill at every unexported index, loading conversations another agy process created, with read failures in the history token (#14627), 427 B (263,245 B).
// Antigravity subagent reports and background task results as task notices, invoke_subagent Role / Prompt (#14625), 749 B (263,994 B).
// Untitled Antigravity conversations take a short title from their first prompt line (#14629), 218 B (264,212 B).
// An API error agy streams as message text becomes the turn's error, kept in history and settled idle (#14628), 231 B (264,443 B).
// Antigravity GENERIC timestamp header stripping (#13662) adds 176 B (264,619 B).
// Subsequent runtime fixes on main (#15269, #15275, #13312, #12952, #15475) bring the total to 265,362 B.
// Antigravity shared ACP exit cleanup (#15756) and always-allow permission choices (#15785) bring the total to 265,686 B.
// Antigravity permission mode persistence across companion restarts and resume re-application (#15807) add 273 B (265,959 B).
// Antigravity agent-mode MCP proxy budgeting for non-assistant leader and worker catalogs (#15822) adds 253 B (266,212 B).
// Antigravity launchers.check honoring explicit params.model (#15905) adds 1 B (266,213 B).
// Antigravity withoutStaleWorking coverage for send policy, fork, rewind, modes, and native error reads (#15924) adds 48 B (266,261 B).
// Runtime package lifecycle support for preserving tools across upgrades (#16016) adds 4,368 B (270,629 B).
// ACP instruction fitting to the mounted device tools and supervision role (#16052) adds 244 B (270,873 B).
// Antigravity settled-turn recovery across delayed shifted-index exports and structured agy_turn_end turn_duration items (#15619) add 1,169 B (272,042 B).
// Antigravity non-rewind negative cache for summary_behind GetCascadeTrajectory checks + summarizeTranscript prefix summary reuse (#13086) add 872 B (272,914 B).
// Antigravity Desktop session.start (ConversationService/CreateConversation), model discovery, and lifecycle controls (#15994) add 4,922 B (277,836 B).
// Antigravity Assistant run_command command-gate routing, unclipped command & per-call Cwd forwarding, fail-closed reconnect, tracks Assistant detection, and Assistant allow_always bypass guard (#16202) add 511 B (278,347 B).
// Raise the budget only for code a runtime needs, never to embed the packages raw again.
func TestDistBudget(t *testing.T) {
	const budget = 278_347
	_, out := generateIntegrations(t)
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(0)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
	}
	if size > budget {
		t.Fatalf("generated runtime packages are %d bytes, over the budget of %d", size, budget)
	}
	t.Logf("generated runtime packages: %d bytes (budget %d)", size, budget)
}
