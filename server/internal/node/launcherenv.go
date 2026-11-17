package node

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// A provider's environment for claude (#2455, docs/integrations/launchers.md): one pure function the main conversation
// and every Agent use on the CLI (its --settings env, `personad launch`'s env). Cindy's env-builder, the Anthropic part.

// legacyEnvClear: what `personad launch` has always dropped from the shell (another launcher or account) before a
// managed launcher from before #2455 runs. A trailing "*" is a prefix.
var legacyEnvClear = []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*",
	"CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*"}

// providerEnvClear: what claude must not inherit before a provider's own env is set: legacyEnvClear and every other
// variable providerEnv may set but the proxy: the shell's stays (a gateway reachable only through it) unless the
// provider has its own (proxyEnvClear).
var providerEnvClear = append(slices.Clone(legacyEnvClear), "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS")

// proxyEnvClear: the shell's proxy, dropped when the provider sets its own.
var proxyEnvClear = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// envClearFor is what to drop before l runs: a launcher from before #2455 keeps the shell's proxy and timeout, as it
// always has; a provider keeps the shell's proxy unless it has one (a dead one is dropped at launch,
// withoutDeadProxies). Its bypass list replaces the shell's in both spellings (`personad launch` adds the inherited
// ones to it first when it follows the shell's proxy, mergesNoProxy). What the launcher's own environment sets (Env)
// goes too, a proxy variable in both spellings: its value is the one that counts. So does the reply limit when the
// provider's models set one.
func envClearFor(l proto.ManagedLauncher) []string {
	var clear []string
	switch {
	case !providerShaped(l):
		clear = slices.Clone(legacyEnvClear)
	case l.Proxy != "":
		clear = append(slices.Clone(providerEnvClear), proxyEnvClear...)
	case l.NoProxy != "":
		clear = append(slices.Clone(providerEnvClear), "NO_PROXY", "no_proxy")
	default:
		clear = slices.Clone(providerEnvClear)
	}
	// The reply limit only when Persona works one out (outputTokens): otherwise the shell's stays, as it always has. One
	// the launcher's own environment sets is cleared below with the rest of it, in its order.
	if outputLimits(l) != nil {
		_, set := providerEnv(l, "", "", false)[maxOutputEnv]
		clear = withOutputClear(clear, set)
	}
	for _, kv := range launcherEnvVars(l.Env) {
		for _, k := range append([]string{kv[0]}, otherSpelling(kv[0])...) {
			if !slices.Contains(clear, k) {
				clear = append(clear, k)
			}
		}
	}
	return clear
}

// proxyNames are the proxy variables Claude Code (Node's fetch) reads in either spelling, the lower-case one first; an
// empty value falls through to the other one.
var proxyNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

// mergesNoProxy says whether l's bypass list is added to the ones its claude would otherwise read (#6238): the user's
// settings env's, else the shell's at launch or the node's for the main conversation (withInheritedNoProxy). Only for a
// provider that follows the
// computer's proxy, whose list goes with it. One with a proxy of its own brings its whole list: the computer's is for
// the computer's proxy, and its "*" or the gateway in it would turn the provider's off. Nor when the provider's own
// environment sets the list or a proxy (it is set after everything else), or the list is "*" (no proxy at all).
func mergesNoProxy(l proto.ManagedLauncher) bool {
	if l.Proxy != "" || l.NoProxy == "" || l.NoProxy == "*" {
		return false
	}
	return !slices.ContainsFunc(launcherEnvVars(l.Env), func(kv [2]string) bool {
		return slices.Contains([]string{"NO_PROXY", "HTTP_PROXY", "HTTPS_PROXY"}, strings.ToUpper(kv[0]))
	})
}

// withInheritedNoProxy is env (KEY=value entries) with the bypass lists lookup gives added to the provider's
// (addNoProxy), for a launcher that mergesNoProxy: the provider's hosts go direct, and the inherited ones keep going
// direct for the commands Claude runs (#6238).
func withInheritedNoProxy(env []string, lookup func(string) string) []string {
	upper, lower := addNoProxy(envValue(env, "NO_PROXY"), lookup)
	out := slices.Clone(env)
	for i, kv := range out {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "NO_PROXY":
			out[i] = k + "=" + upper
		case "no_proxy":
			out[i] = k + "=" + lower
		}
	}
	return out
}

// addNoProxy is ours (a provider's bypass list) added to each spelling's list that lookup gives, else to the other
// spelling's, so a tool that reads one spelling first keeps the hosts it had (Go NO_PROXY, curl and the runtime under
// Claude Code no_proxy): NO_PROXY's, then no_proxy's.
func addNoProxy(ours string, lookup func(string) string) (upper, lower string) {
	u, l := lookup("NO_PROXY"), lookup("no_proxy")
	return mergeNoProxy(ours, cmp.Or(u, l)), mergeNoProxy(ours, cmp.Or(l, u))
}

// mergeNoProxy is two bypass lists as one, each host once (the first's order first); "*" in either is all of them.
func mergeNoProxy(a, b string) string {
	var out []string
	seen := map[string]bool{}
	for _, h := range strings.FieldsFunc(a+","+b, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if h == "*" {
			return "*"
		}
		if k := strings.ToLower(h); !seen[k] {
			seen[k] = true
			out = append(out, h)
		}
	}
	return strings.Join(out, ",")
}

// otherSpelling is k's other spelling when k is one of proxyNames in either case, none otherwise.
func otherSpelling(k string) []string {
	for _, p := range proxyNames {
		switch k {
		case p:
			return []string{strings.ToLower(p)}
		case strings.ToLower(p):
			return []string{p}
		}
	}
	return nil
}

// launcherEnvVars is a launcher's own environment (Env), in order: one NAME=value a line, "export " before it is fine,
// a line starting with # is a comment. A value in quotes is what is inside them; one without loses a trailing " # …"
// comment, as in a shell. Lines that are no assignment are skipped: the hub refuses them (checkEnv, the same rules).
func launcherEnvVars(raw string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := envLine(line)
		if ok && k != "" {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// envLine reads one line of a launcher's environment: k "" (and ok) for a blank line or a comment, ok false for a line
// that is no assignment.
func envLine(line string) (k, v string, ok bool) {
	line = strings.TrimFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", true
	}
	line = exportRe.ReplaceAllString(line, "")
	k, v, ok = strings.Cut(line, "=")
	k = strings.TrimSpace(k)
	if !ok || !envNameRe.MatchString(k) {
		return "", "", false
	}
	rest := strings.TrimLeft(v, " \t")
	if rest != v && strings.HasPrefix(rest, "#") {
		return k, "", true // `A= # a note`: empty, as in a shell
	}
	v = rest
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return k, v[1 : end+1], true
		}
	}
	for _, sep := range []string{" #", "\t#"} { // a comment after the value
		if i := strings.Index(v, sep); i >= 0 {
			v = v[:i]
		}
	}
	return k, strings.TrimSpace(v), true
}

var envNameRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)

var exportRe = lazyre.New(`^export\s+`)

// withoutEnv is environ (KEY=value entries) without the variables clear names.
func withoutEnv(environ, clear []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		for _, c := range clear {
			if p, ok := strings.CutSuffix(c, "*"); ok && strings.HasPrefix(key, p) || key == c {
				return true
			}
		}
		return false
	})
}

const (
	// oneMillionSuffix: Claude Code runs a model with a 1M context window only when its name says so.
	oneMillionSuffix = "[1m]"
	// standardWindow: the window Claude Code assumes for a model, and the main conversation's compact window.
	standardWindow = 200000
	// markedWindow: the window Claude Code counts for a model whose name ends in [1m].
	markedWindow = 1000000
	// gatewayTimeoutMS: API_TIMEOUT_MS for a gateway that sets none (slow third-party gateways, cindy's default).
	gatewayTimeoutMS = 3000000
	// unknownModelOutput: the longest reply Claude Code asks a model it does not know for, and unknownModelOutputCap
	// the most CLAUDE_CODE_MAX_OUTPUT_TOKENS raises that to (2.1.325, and the official env-vars reference).
	unknownModelOutput    = 32000
	unknownModelOutputCap = 128000
)

// maxOutputEnv: the longest reply Claude Code asks for, one value for every model of the process, each capped at its
// own limit (outputTokens). outputMarkEnv carries the same value wherever Persona works one out: Claude Code passes
// its environment to the commands it runs, so a `personad launch` inside such a session can tell the value it
// inherited from Persona (still equal to the mark) from one the user set (inheritedOutputClear, #8816).
const (
	maxOutputEnv  = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"
	outputMarkEnv = "PERSONA_MAX_OUTPUT_TOKENS"
)

// outputWindowShare: a model's reply is asked for at most this share of its window (1/8, outputLimit, #8816). A gateway
// that counts input plus max_tokens against the window refuses a request once the input passes the window less that
// reply; Claude Code reserves only min(max output, 20000) below the window before it compacts, so a larger reply opens
// a stretch where such a gateway refuses every turn. Claude Code retries with less when the refusal is worded like
// Anthropic's, not when the gateway words it as an OpenAI-compatible upstream does (2.1.325); then it fails the turn
// or compacts early. An eighth keeps that stretch in the last eighth of the window.
const outputWindowShare = 8

// processModelVars: the variables naming a model the process runs or can switch to (the session, a tier, the small
// model, sub-Agents, the /model menu's extra entry).
var processModelVars = []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION"}

// claudeTierAliases: names Claude Code resolves to one of its tiers (whose model processModelVars already name), and
// tierAliasVars the variable each tier alias passed as --model runs.
var claudeTierAliases = []string{"opus", "sonnet", "haiku", "fable", "best", "opusplan", "default", "inherit"}

var tierAliasVars = map[string]string{"opus": "ANTHROPIC_DEFAULT_OPUS_MODEL", "sonnet": "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"haiku": "ANTHROPIC_DEFAULT_HAIKU_MODEL", "fable": "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"best": "ANTHROPIC_DEFAULT_OPUS_MODEL"} // best runs what opus does (Claude Code 2.1.321)

// claudeKnows: m looks like one of Claude's own IDs, also inside a gateway's spelling of it (anthropic/claude-opus-5-5,
// a dated ID, claude-3-5-sonnet): Claude Code matches its known IDs anywhere in a name and asks such a model for its
// own output limit. A family name with a version is needed: a gateway's …-claude-distill or claude-opus-distill-…,
// and claude-sonnet-latest, are IDs Claude Code does not know.
func claudeKnows(m string) bool { return claudeIDRe.MatchString(m) }

var claudeIDRe = lazyre.New(`(?i)claude-(?:(?:opus|sonnet|haiku|fable|mythos|instant)-[0-9]|[0-9])`)

// isTierAlias: m is a name Claude Code resolves to a model of its own (claudeTierAliases), "[1m]" or not.
func isTierAlias(m string) bool {
	return slices.Contains(claudeTierAliases, strings.ToLower(strings.TrimSuffix(m, oneMillionSuffix)))
}

// resolveTier is m with a tier alias (opus, sonnet, haiku, fable) replaced by the model its tier variable names (get),
// when that is no alias itself; otherwise m.
func resolveTier(m string, get func(string) string) string {
	if k, ok := tierAliasVars[strings.ToLower(strings.TrimSuffix(m, oneMillionSuffix))]; ok {
		if v := get(k); v != "" && !isTierAlias(v) {
			return v
		}
	}
	return m
}

// outputTokens is CLAUDE_CODE_MAX_OUTPUT_TOKENS for a process with env's models (#8798), limit giving what a model
// may be asked for (outputLimit, 0 none), and 0 to leave Claude Code's own limit: the least of the models the process
// runs, capped at what Claude Code accepts, when that raises the 32000 it asks a model it does not know for. None when
// the session's model is a Claude one, a tier alias that resolves to no model of the gateway's included (its own limit
// is its full one, and a lower value would lower it), and none when a model that is not reports no limit: the one
// value serves them all, and asking a model for more than it writes is refused by a strict gateway. A Claude-looking
// model with no limit is left to Claude Code, which holds a model it knows to that model's own limit.
func outputTokens(limit func(string) int, env map[string]string) int {
	session := resolveTier(env["ANTHROPIC_MODEL"], func(k string) string { return env[k] })
	if session == "" || isTierAlias(session) || claudeKnows(session) {
		return 0
	}
	n := 0
	for _, k := range processModelVars {
		m := env[k]
		if k == "ANTHROPIC_MODEL" {
			m = session
		}
		if m == "" || isTierAlias(m) { // a tier's own variable names its model
			continue
		}
		switch got := limit(m); {
		case got > 0:
			if n == 0 || got < n {
				n = got
			}
		case !claudeKnows(m):
			return 0
		}
	}
	if n <= unknownModelOutput {
		return 0
	}
	return min(n, unknownModelOutputCap)
}

// outputLimit is what outputTokens counts for l's model m ("<id>" or "<id>[1m]"): modelOutputLimit by the window l
// declares for its process (declaredCapacity).
func outputLimit(l proto.ManagedLauncher, m string) int {
	pm, _ := providerModel(l, m)
	return modelOutputLimit(pm, declaredCapacity(l))
}

// modelOutputLimit is pm's reported max_output_tokens, at most an eighth of its window (its context_window, or declared
// when that is smaller, as providerCapacity reads them; outputWindowShare); 0 when either is unknown.
func modelOutputLimit(pm proto.ProviderModel, declared int) int {
	if w := smallerCapacity(pm.ContextWindow, declared); pm.MaxOutputTokens > 0 && w > 0 {
		return min(pm.MaxOutputTokens, w/outputWindowShare)
	}
	return 0
}

// declaredCapacity is the window l declares for every model of its process, 0 none: providerCapacity without a model.
func declaredCapacity(l proto.ManagedLauncher) int {
	return providerCapacity(proto.ManagedLauncher{MaxContext: l.MaxContext, Env: l.Env, Args: l.Args}, "")
}

// outputLimits are outputLimit of l's models by ID, for `personad launch` to work out the reply limit for the model the
// session runs (launcher.env's max_output_tokens, sessionOutput); nil when none has one, or l's own environment sets
// the variable (its value stands).
func outputLimits(l proto.ManagedLauncher) map[string]int {
	if slices.ContainsFunc(launcherEnvVars(l.Env), func(kv [2]string) bool { return kv[0] == maxOutputEnv }) {
		return nil
	}
	var out map[string]int
	declared := declaredCapacity(l)
	for _, pm := range l.Models {
		if n := modelOutputLimit(pm, declared); n > 0 {
			if out == nil {
				out = map[string]int{}
			}
			out[pm.ID] = n
		}
	}
	return out
}

// withOutputClear is clear dropping the shell's reply limit and its mark exactly when the launch sets them (set):
// otherwise the shell's stay (inheritedOutputClear says which go).
func withOutputClear(clear []string, set bool) []string {
	clear = slices.DeleteFunc(slices.Clone(clear), func(k string) bool { return k == maxOutputEnv || k == outputMarkEnv })
	if set {
		clear = append(clear, maxOutputEnv, outputMarkEnv)
	}
	return clear
}

// inheritedOutputClear is clear for a launch whose environment (lookup) may come from a session Persona launched with
// a reply limit (#8816): when env sets none, the inherited value goes if it is still the one Persona marked, and the
// mark goes either way; a value the user set (no mark, or another value) stays.
func inheritedOutputClear(clear, env []string, lookup func(string) string) []string {
	mark := lookup(outputMarkEnv)
	if mark == "" {
		return clear
	}
	gone := []string{outputMarkEnv}
	if !slices.Contains(envNames(env), maxOutputEnv) && lookup(maxOutputEnv) == mark {
		gone = append(gone, maxOutputEnv)
	}
	clear = slices.Clone(clear)
	for _, k := range gone {
		if !slices.Contains(clear, k) {
			clear = append(clear, k)
		}
	}
	return clear
}

// unpinnedOutput is a settings "env" without the reply limit Persona worked out (the mark says so): the process env
// carries it for the model the session runs, and a value in the user's own settings env wins over it. One the
// provider's own environment sets stays pinned like the rest of it.
func unpinnedOutput(env map[string]string) map[string]string {
	if _, worked := env[outputMarkEnv]; worked {
		env = maps.Clone(env)
		delete(env, maxOutputEnv)
		delete(env, outputMarkEnv)
	}
	return env
}

// sessionModel is the model a claude `personad launch` starts runs: the last --model of the caller's arguments, else
// of the launcher's own, else the environment's ANTHROPIC_MODEL (the provider's, its own env included); a tier alias
// (--model opus) is that tier's model (resolveTier).
func sessionModel(args, launcherArgs, env []string) string {
	m := cmp.Or(modelArg(args), modelArg(launcherArgs), envValue(env, "ANTHROPIC_MODEL"))
	return resolveTier(m, func(k string) string { return envValue(env, k) })
}

// sessionOutput is launcher.env's env and clear list with the reply limit for the model the session runs (session):
// the node works it out for the provider's model, and a start's --model may name another, which then runs beside the
// provider's tiers. limits nil (an older node, or the provider's own environment sets it): as they are.
func sessionOutput(env, clear []string, limits map[string]int, session string) ([]string, []string) {
	if limits == nil {
		return env, clear
	}
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if session != "" {
		m["ANTHROPIC_MODEL"] = session
	}
	n := outputTokens(func(x string) int { return cmp.Or(limits[x], limits[strings.TrimSuffix(x, oneMillionSuffix)]) }, m)
	env = withoutEnv(env, []string{maxOutputEnv, outputMarkEnv})
	if n > 0 {
		v := strconv.Itoa(n)
		env = append(env, maxOutputEnv+"="+v, outputMarkEnv+"="+v)
	}
	return env, withOutputClear(clear, n > 0)
}

// providerModel is what l knows of model m ("<id>" or "<id>[1m]").
func providerModel(l proto.ManagedLauncher, m string) (proto.ProviderModel, bool) {
	id := strings.TrimSuffix(m, oneMillionSuffix)
	for _, pm := range l.Models {
		if pm.ID == m || pm.ID == id {
			return pm, true
		}
	}
	return proto.ProviderModel{}, false
}

// sdkModel is the name Claude Code runs m (pm what is known of it) as: with [1m] for a window over 200K.
func sdkModel(m string, pm proto.ProviderModel) string {
	if pm.ContextWindow > standardWindow && !strings.HasSuffix(m, oneMillionSuffix) {
		return m + oneMillionSuffix
	}
	return m
}

// tierModel is the name Claude Code runs l's model m as when a tier, the small model or the subagent model names it
// (#5149): with [1m] when l's models give it a window of 1M or more (Claude Code drops the marker before it calls the
// gateway). A marked model counts as 1M whatever CLAUDE_CODE_MAX_CONTEXT_TOKENS says, and that and the compact window
// are one value per process, the session model's: a window over 200K but under 1M stays unmarked, as before, rather
// than be counted as 1M with nothing holding it to its size.
func tierModel(l proto.ManagedLauncher, m string) string {
	if pm, _ := providerModel(l, m); pm.ContextWindow >= markedWindow {
		return sdkModel(m, pm)
	}
	return m
}

// refusalFallbackModels: the models Claude Code moves a refused reply to, space-separated, as observed in Claude
// Code 2.1.322 (#7319). Which one a stopped reply goes to depends on the session's model and the stop's category:
//
//	session model       fallback
//	claude-opus-5-5     claude-opus-4-8 or claude-opus-5
//	claude-opus-5       claude-opus-4-8
//	claude-sonnet-5-5   claude-sonnet-5
//	any other model     claude-opus-4-8 or claude-opus-5 (a gateway's non-Claude models included)
//
// The session's model can change at run time and CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK is one value per process, so a
// launcher keeps the fallback only when its list has all of them. A version that falls back elsewhere is a change to
// this line and to TestRefusalFallbackModelsAsObserved.
const refusalFallbackModels = "claude-opus-4-8 claude-opus-5 claude-sonnet-5"

// declaredModelID is a model ID as the fallback check compares it: trimmed, lower case and without a trailing context
// marker such as "[1m]" (Claude Code drops it before it calls the gateway).
func declaredModelID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndexByte(id, '['); i >= 0 && strings.HasSuffix(id, "]") {
		id = strings.TrimSpace(id[:i])
	}
	return id
}

// lacksRefusalFallback: l declares the models its gateway serves (Models, blank IDs aside) and at least one of
// refusalFallbackModels is not among them. A launcher that declares none is not known to lack any.
func lacksRefusalFallback(l proto.ManagedLauncher) bool {
	declared := map[string]bool{}
	for _, m := range l.Models {
		if id := declaredModelID(m.ID); id != "" {
			declared[id] = true
		}
	}
	if len(declared) == 0 {
		return false
	}
	return slices.ContainsFunc(strings.Fields(refusalFallbackModels), func(m string) bool { return !declared[m] })
}

// providerShaped: a provider as #2455 models it (an API provider, or any of its fields set), which gets a gateway's
// extras (no nonessential traffic, a long timeout). A launcher from before gets exactly the env it always had.
func providerShaped(l proto.ManagedLauncher) bool {
	return l.Kind == "api" || len(l.Models) > 0 || l.OpusModel != "" || l.SonnetModel != "" || l.HaikuModel != "" ||
		l.FableModel != "" || l.SubagentForce || l.Headers != "" || l.HasHeaders || l.Proxy != "" || l.NoProxy != "" ||
		l.Env != "" || l.HasEnv ||
		l.TimeoutMS != 0 || l.CompactWindow != 0 || l.MaxContext != 0 || l.Effort != "" || l.Source != ""
}

// providerEnv is the environment claude runs provider l with, for this model and effort ("" for the provider's own),
// for an assistant conversation (mainConversation: it switches its own thinking level) or not. A "" value means set empty: the variable must not come from anywhere else (the
// other credential of the two, which a gateway seeing both refuses with 401). What the shell carries is cleared
// first (providerEnvClear); settings env cannot unset, so a value here is the only way to override one there.
//
// Kept as it was for a launcher from before #2455 (managedEnvOrder): the gateway URL always (empty for none); no key
// stored (or the hub could not open it), neither credential, so Claude uses whatever sign-in it has; no model, none of
// the model variables. The Haiku tier is HaikuModel, else SmallModel (what it was), else the model. A tier, the small
// model or the subagent model named explicitly gets [1m] by its own window (tierModel).
func providerEnv(l proto.ManagedLauncher, model, effort string, mainConversation bool) map[string]string {
	// The provider's own environment replaces the endpoint below: fallback policy belongs to that effective gateway,
	// not the stored one. An empty override means the official default, not a custom gateway with no models declared.
	effectiveBaseURL := l.LaunchBaseURL()
	for _, kv := range launcherEnvVars(l.Env) {
		if kv[0] == "ANTHROPIC_BASE_URL" {
			effectiveBaseURL = kv[1]
		}
	}
	if effectiveBaseURL == "" {
		effectiveBaseURL = "https://" + proto.AnthropicAPIHost
	}
	// Claude Code adds /v1/messages: a provider stored with a trailing /v1, from a hub before it dropped it, would
	// otherwise call /v1/v1/messages though Test connection passed.
	env := map[string]string{"ANTHROPIC_BASE_URL": l.LaunchBaseURL()}
	if l.BaseURL != "" && providerShaped(l) {
		env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
		env["API_TIMEOUT_MS"] = strconv.Itoa(cmp.Or(l.TimeoutMS, gatewayTimeoutMS))
		// Claude Code moves a refused reply to a fallback model of its own choosing (refusalFallbackModels), an ID no
		// setting changes, and a gateway that does not serve it answers 404 after two requests that cannot succeed
		// (#7319). Off when the launcher's model list shows the gateway lacks one: the turn then ends on its refusal,
		// which the assistant's recovery reads (assistant_recovery.go). A launcher that declares no list, or one with
		// them all, keeps Claude Code's fallback; the launcher's own environment, applied below, wins either way.
		if !proto.AnthropicAPI(effectiveBaseURL) && lacksRefusalFallback(l) {
			env["CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK"] = "1"
		}
	} else if l.TimeoutMS > 0 {
		env["API_TIMEOUT_MS"] = strconv.Itoa(l.TimeoutMS)
	}
	switch {
	case l.APIKey == "":
	case l.BearerAuth():
		env["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_API_KEY"] = strings.TrimPrefix(l.APIKey, "Bearer "), ""
	default:
		env["ANTHROPIC_API_KEY"], env["ANTHROPIC_AUTH_TOKEN"] = l.APIKey, ""
	}
	set := func(k, v string) {
		if v != "" {
			env[k] = v
		}
	}
	m := cmp.Or(model, l.Model)
	pm, _ := providerModel(l, m)
	run := sdkModel(m, pm)
	// A tier, the small model or the subagent model set explicitly runs by its own window, else as the session's model.
	tier := func(explicit ...string) string {
		if e := cmp.Or(explicit...); e != "" {
			return tierModel(l, e)
		}
		return run
	}
	set("ANTHROPIC_MODEL", run)
	set("ANTHROPIC_DEFAULT_OPUS_MODEL", tier(l.OpusModel))
	set("ANTHROPIC_DEFAULT_SONNET_MODEL", tier(l.SonnetModel))
	set("ANTHROPIC_DEFAULT_FABLE_MODEL", tier(l.FableModel))
	set("ANTHROPIC_DEFAULT_HAIKU_MODEL", tier(l.HaikuModel, l.SmallModel))
	set("ANTHROPIC_SMALL_FAST_MODEL", tier(l.SmallModel))
	// Subagents follow the model unless the provider says otherwise (workflows, multi-agent runs).
	set("CLAUDE_CODE_SUBAGENT_MODEL", tier(l.SubagentModel))
	if l.SubagentForce {
		env["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"] = "1"
	}
	w := pm.ContextWindow
	if l.MaxContext > 0 {
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(l.MaxContext)
	} else if w > standardWindow {
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(w)
	}
	// The compact window the provider declares, else its model's window; none when neither is known, and Claude Code
	// compacts by the model it knows. The same for an assistant conversation as for an agent (#3066); an assistant start
	// writes its own, which holds the main conversation to assistant.main_compact_window (#7314, perStartEnv).
	if c := cmp.Or(l.CompactWindow, w); c > 0 {
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(c)
	}
	// Claude Code puts CLAUDE_CODE_EFFORT_LEVEL above --effort and /effort, which would override the caller's
	// choice and lock the session's effort control (#2307). A default effort goes to the command line (--effort)
	// instead, and the environment variable is set only when the provider's own env sets it explicitly.
	set("ANTHROPIC_CUSTOM_HEADERS", l.Headers)
	// Both spellings: Node's fetch reads the lower-case one first, so an upper-case value alone would lose to a lower-case
	// one in the user's settings env.
	if l.Proxy != "" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env[k] = l.Proxy
		}
	}
	// Both spellings: Claude Code's own check reads both, but the runtime under it (Bun) takes no_proxy || NO_PROXY,
	// so a lower-case list from the shell alone would still send the gateway through the proxy (#6238). A provider with
	// a proxy of its own brings its whole list, an empty one too: one from the shell or the user's settings would apply
	// to its proxy. "*" is no proxy at all.
	if l.NoProxy != "" || l.Proxy != "" {
		env["NO_PROXY"], env["no_proxy"] = l.NoProxy, l.NoProxy
	}
	if l.CustomModel != "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = l.CustomModel
		set("ANTHROPIC_CUSTOM_MODEL_OPTION_NAME", l.CustomModelName)
		set("ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION", l.CustomModelDescription)
	}
	k, v, _ := strings.Cut(artifactsOff, "=")
	env[k] = v
	// The launcher's own environment last: it wins over all of the above (#5953); an empty value sets the variable
	// empty. A proxy variable goes in both spellings unless both are given, or the shell's other spelling would still
	// count. Not for an assistant conversation the thinking level, which it switches while it runs.
	vars := launcherEnvVars(l.Env)
	given := map[string]bool{}
	for _, kv := range vars {
		given[kv[0]] = true
	}
	for _, kv := range vars {
		if mainConversation && kv[0] == "CLAUDE_CODE_EFFORT_LEVEL" {
			continue
		}
		env[kv[0]] = kv[1]
		for _, o := range otherSpelling(kv[0]) {
			if !given[o] {
				env[o] = kv[1]
			}
		}
	}
	// The reply limit by the models the process ends up with (the launcher's own environment may name one), unless
	// that environment sets it itself (empty: Claude Code's own limit).
	if n := outputTokens(func(m string) int { return outputLimit(l, m) }, env); n > 0 && !given[maxOutputEnv] {
		env[maxOutputEnv], env[outputMarkEnv] = strconv.Itoa(n), strconv.Itoa(n)
	}
	return env
}

// managedEnvOrder: the order `personad launch` has always set a managed launcher's variables in; any other comes
// after them, sorted.
var managedEnvOrder = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL",
	"ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME", "ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_DISABLE_ARTIFACT",
}

// envList is env as KEY=value entries, managedEnvOrder first. The two credentials keep the order they always had: the
// one with the key first, the emptied one after it.
func envList(env map[string]string) []string {
	order := slices.Clone(managedEnvOrder)
	if env["ANTHROPIC_AUTH_TOKEN"] == "" {
		order[1], order[2] = order[2], order[1]
	}
	rest := maps.Clone(env)
	var out []string
	for _, k := range order {
		if v, ok := rest[k]; ok {
			out = append(out, k+"="+v)
			delete(rest, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(rest)) {
		out = append(out, k+"="+rest[k])
	}
	return out
}

// perStartEnv: what an assistant start decides (its compact window, the launcher's declared one as
// assistantCompactionPolicy reads it, capped for the main conversation; its thinking level, switched while it runs) and
// so never goes into settings env, which would win over the start's own environment.
var perStartEnv = []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_EFFORT_LEVEL"}

// pinnedEnv is env without perStartEnv: the part pinned over the user's ~/.claude/settings.json (the gateway, its key,
// the models), as a settings "env".
func pinnedEnv(env map[string]string) map[string]string {
	out := maps.Clone(env)
	for _, k := range perStartEnv {
		delete(out, k)
	}
	return out
}

// launchEnvFor is what `personad launch` drops from the shell and sets for a launcher (the node's clear and env lists):
// in the assistant's process (PERSONA_ASSISTANT=1), neither touches perStartEnv or CLAUDE_CONFIG_DIR, so its own
// compact window, thinking level and recorded profile stay. Terminal Agents retain the provider's full environment.
func launchEnvFor(clear, env []string, assistant bool) ([]string, []string) {
	if !assistant {
		return clear, env
	}
	clear = slices.DeleteFunc(slices.Clone(clear), func(c string) bool { return slices.Contains(perStartEnv, c) })
	env = slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(perStartEnv, k) || k == "CLAUDE_CONFIG_DIR"
	})
	clear = slices.DeleteFunc(clear, func(k string) bool { return k == "CLAUDE_CONFIG_DIR" })
	return clear, env
}

// launcherDefaultEffort returns the provider's default effort level (its configured Effort,
// or its default model's DefaultEffort).
func launcherDefaultEffort(l proto.ManagedLauncher) string {
	pm, _ := providerModel(l, l.Model)
	return cmp.Or(l.Effort, pm.DefaultEffort)
}

// noEffortModels are the IDs of l's models its gateway says take no thinking level (ProviderModel.NoEffort), for
// `personad launch` to leave out the default level for one (launcher.env's no_effort, launchEffort).
func noEffortModels(l proto.ManagedLauncher) []string {
	var ids []string
	for _, pm := range l.Models {
		if pm.NoEffort {
			ids = append(ids, pm.ID)
		}
	}
	return ids
}

// launchEffort is the default level `personad launch` passes as --effort (launcher.env's effort), "" when the
// session's model (sessionModel) is one of noEffort ("<id>" or "<id>[1m]", #8798): the provider's level is not asked
// of a model the gateway says has none. Leaving the flag out does not make Claude Code send no level: to a model it
// does not know it still sends its own default (high, 2.1.325). The start decides it once; a /model switch later in
// the session keeps the level it has.
func launchEffort(effort string, noEffort []string, session string) string {
	if effort == "" || session == "" {
		return effort
	}
	if slices.Contains(noEffort, session) || slices.Contains(noEffort, strings.TrimSuffix(session, oneMillionSuffix)) {
		return ""
	}
	return effort
}

// modelArg is the model the last -m / --model in args names before a "--" (what follows it is the prompt), "" none.
func modelArg(args []string) string {
	var m string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			return m
		case (a == "--model" || a == "-m") && i+1 < len(args):
			i++
			m = args[i]
		case strings.HasPrefix(a, "--model="):
			m = strings.TrimPrefix(a, "--model=")
		}
	}
	return m
}
