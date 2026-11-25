package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Server-side reply policy gates for Inbox owner drafts. The legacy Task 2 schema and the upstream compiled schema
// share the same fresh visibility boundary, but each interprets only its own explicit configuration fields.

// legacyReplyPolicyConfig identifies the original Task 2 policy schema. The upstream compiled schema and the legacy
// schema are deliberately disjoint: a classifier category must never be interpreted under the other schema's routes.
func legacyReplyPolicyConfig(cfg *assistantwire.InboxReplyPolicyConfig) bool {
	// Sources and RequireIndependentReview are shared with the compiled schema. Only fields unique to the original
	// Task 2 schema select legacy routing; otherwise a compiled source scope or review requirement would disable itself.
	return cfg != nil && (cfg.Routes != nil || cfg.UnknownMode != "")
}

func replyPolicyConfigOf(input any) *assistantwire.InboxReplyPolicyConfig {
	switch cfg := input.(type) {
	case *assistantwire.InboxReplyPolicyConfig:
		return cfg
	case assistantwire.InboxReplyPolicyConfig:
		return &cfg
	default:
		return nil
	}
}

const (
	ReplyRouteOwnerDraft = "owner_draft"
	ReplyRouteBot        = "bot"
	ReplyRouteOwnerOnly  = "owner_only"
	ReplyRouteNoReply    = "no_reply"
)

var errReplyPolicyDenied = errCannotReply

var replyPolicyCategories = map[string]bool{
	"personal_transaction": true, "security_compliance": true, "approval_permission": true,
	"decision_delegation": true, "technical_qa": true, "research": true, "alert_triage": true,
	"ppe_deploy": true, "mr_precheck": true, "no_reply": true, "ordinary_coordination": true,
	"logistical_followup": true, "acknowledgement": true, "other_owner_reply": true, "unknown": true,
}

var forcedReplyRoutes = map[string]string{
	"personal_transaction": ReplyRouteOwnerOnly, "security_compliance": ReplyRouteOwnerOnly,
	"approval_permission": ReplyRouteOwnerOnly, "decision_delegation": ReplyRouteOwnerOnly,
	"technical_qa": ReplyRouteBot, "research": ReplyRouteBot, "alert_triage": ReplyRouteBot,
	"ppe_deploy": ReplyRouteBot, "mr_precheck": ReplyRouteBot, "no_reply": ReplyRouteNoReply,
	"unknown": ReplyRouteOwnerOnly, "": ReplyRouteOwnerOnly,
}

func validReplyRoute(route string) bool {
	switch route {
	case ReplyRouteOwnerDraft, ReplyRouteBot, ReplyRouteOwnerOnly, ReplyRouteNoReply:
		return true
	default:
		return false
	}
}

// validReplyPolicyConfig rejects the whole enabled specification, rather than letting one safe-looking category hide
// a malformed route elsewhere. A disabled or absent policy is the legacy path and needs no validation here.
func validReplyPolicyConfig(cfg *assistantwire.InboxReplyPolicyConfig) bool {
	if cfg == nil || !cfg.Enabled {
		return true
	}
	if strings.TrimSpace(cfg.Revision) == "" || cfg.UnknownMode != "" && !validReplyRoute(cfg.UnknownMode) {
		return false
	}
	for category, route := range cfg.Routes {
		if !replyPolicyCategories[category] || !validReplyRoute(route) {
			return false
		}
	}
	return true
}

// replyPolicyApplies selects sources explicitly, or every source when an enabled policy has no source list. A disabled
// or nil policy is the compatibility path: none of the gates changes existing behavior.
func replyPolicyApplies(input any, source string) bool {
	cfg := replyPolicyConfigOf(input)
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if !policy.enabled {
			return false
		}
		if len(policy.sources) == 0 {
			return true
		}
		return policy.sources[strings.TrimSpace(source)]
	}
	if len(cfg.Sources) == 0 {
		return true
	}
	for _, candidate := range cfg.Sources {
		if candidate == source {
			return true
		}
	}
	return false
}

// replyPolicySourceUnion is the durable set of sources whose text a row still carries. It includes the empty source:
// an enabled policy with no source list applies to it too. Sorting makes the authorization content version stable.
func replyPolicySourceUnion(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range groups {
		for _, source := range group {
			if !seen[source] {
				seen[source] = true
				out = append(out, source)
			}
		}
	}
	sort.Strings(out)
	return out
}

func replyPolicySourceSet(it *InboxItem) []string {
	if it == nil {
		return nil
	}
	sources := []string{it.Source}
	sources = append(sources, it.ReplyContentSources...)
	for _, ref := range it.MergedFrom {
		sources = append(sources, ref.Source)
	}
	return replyPolicySourceUnion(sources)
}

func replyPolicySourcesApply(input any, sources []string) bool {
	cfg := replyPolicyConfigOf(input)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if !policy.enabled {
			return false
		}
		if len(policy.sources) == 0 {
			return true
		}
	}
	for _, source := range sources {
		if replyPolicyApplies(cfg, source) {
			return true
		}
	}
	return false
}

// replyPolicyItemApplies follows merged text, not just the survivor's current source. ReplyContentSources survives Undo
// because Undo does not remove the member text already copied into the survivor's context.
func replyPolicyItemApplies(input any, it *InboxItem) bool {
	cfg := replyPolicyConfigOf(input)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		return compileReplyPolicy(cfg).appliesToItem(it)
	}
	return it != nil && replyPolicySourcesApply(cfg, replyPolicySourceSet(it))
}

func resetReplyPolicyClassification(it *InboxItem) {
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "", "", "", ""
	it.ReplyReviewed = false
	it.ReplyPolicy = nil
}

func replyRoute(category string, input any) string {
	cfg := replyPolicyConfigOf(input)
	if cfg == nil || !cfg.Enabled {
		return ""
	}
	category = strings.TrimSpace(category)
	if !legacyReplyPolicyConfig(cfg) {
		_, route, err := compileReplyPolicy(cfg).resolve(category)
		if err != nil {
			return ReplyRouteOwnerOnly
		}
		return route
	}
	if !validReplyPolicyConfig(cfg) || !replyPolicyCategories[category] {
		return ReplyRouteOwnerOnly
	}
	if route, forced := forcedReplyRoutes[category]; forced {
		return route
	}
	if route := cfg.Routes[category]; validReplyRoute(route) {
		return route
	}
	return ReplyRouteOwnerOnly
}

// replyPolicyContentVersion covers the exact text the classifier saw, not only the latest source version. Merges can
// append a restricted member's context without changing Last, so Last alone is not an authorization version.
// replyPolicyContextTruncated also recognizes rows clipped before ReplyContextTruncated existed. A still-merged row at
// the clipping cap cannot prove that its retained prefix was complete; fail closed and persist the boundary on new writes.
func replyPolicyContextTruncated(it *InboxItem) bool {
	if it == nil {
		return false
	}
	// A mail snippet is not its body. Even after BodyOf catches up, readMailBody retains at most 3000 characters; a
	// prefix at that boundary cannot authorize text that was never presented to the classifier.
	mailIncomplete := it.Mail != nil && (it.BodyOf != it.Last ||
		it.MailBodyTruncated != nil && *it.MailBodyTruncated ||
		it.MailBodyTruncated == nil && utf8.RuneCountInString(it.Context) >= 3000)
	return it.ReplyContextTruncated || mailIncomplete || len(it.MergedFrom) > 0 && utf8.RuneCountInString(it.Context) >= mergeCtxMax
}

func replyPolicyContentVersion(it *InboxItem) string {
	if it == nil {
		return ""
	}
	version := strconv.Itoa(len(it.Last)) + "\x00" + it.Last + "\x00" + it.Context
	sources := replyPolicySourceSet(it)
	truncated := replyPolicyContextTruncated(it)
	if len(sources) > 1 || truncated {
		// A clipped append can leave Last and Context byte-for-byte unchanged. Use a separate domain and length-prefix
		// both text fields: Context itself may contain the source/truncation marker bytes, so suffixing the legacy form
		// would let an ordinary item collide with a different merged authorization state.
		version = "reply-policy-merged\x00" + strconv.Itoa(len(it.Last)) + "\x00" + it.Last + "\x00" + strconv.Itoa(len(it.Context)) + "\x00" + it.Context
		version += "\x00sources\x00" + strconv.Itoa(len(sources))
		for _, source := range sources {
			version += "\x00" + strconv.Itoa(len(source)) + "\x00" + source
		}
		version += "\x00truncated\x00" + strconv.FormatBool(truncated)
	}
	// The compiled policy also binds title, sender, preview and the physical reply target. Keep the legacy merged-source
	// domain above and add these fields as a length-unambiguous JSON suffix, so both authorization models invalidate
	// every content or target change covered by their respective assertions.
	payload := struct {
		ID, Source, Kind, Last, Title, Sender, Preview, Context, Target string
	}{
		ID: it.ID, Source: it.Source, Kind: it.Kind, Last: it.Last, Title: it.Title,
		Sender: it.Sender, Preview: it.Preview, Context: it.Context, Target: suggestionReplyTarget(it),
	}
	raw, _ := json.Marshal(payload)
	version += "\x00compiled-fields\x00" + string(raw)
	sum := sha256.Sum256([]byte(version))
	return hex.EncodeToString(sum[:])
}

func replyPolicyClassificationCurrent(it *InboxItem, input any) bool {
	cfg := replyPolicyConfigOf(input)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if !policy.appliesToItem(it) {
			return true
		}
		snap := it.ReplyPolicy
		if snap == nil || snap.ContentVersion == "" || snap.ContentVersion != replyPolicyContentVersion(it) || snap.PolicyRevision != policy.revision {
			return false
		}
		category, route, err := policy.resolve(snap.Category)
		if err != nil || snap.Category != category || snap.Route != route {
			return false
		}
		return route != assistantwire.ReplyRouteOwnerDraft || !policy.requireIndependentReview || snap.Reviewed
	}
	if it == nil {
		return false
	}
	if !replyPolicyItemApplies(cfg, it) {
		return true
	}
	if it.ReplyClassifiedOf == "" || it.ReplyClassifiedOf != replyPolicyContentVersion(it) || it.ReplyPolicyRev == "" || it.ReplyPolicyRev != cfg.Revision {
		return false
	}
	// A definitively non-draft result is already fail closed, including an unknown produced by a failed review. Only an
	// owner-draft route needs the independent review bit; turning that requirement on reclassifies an existing draft.
	if replyRoute(it.ReplyCategory, cfg) != ReplyRouteOwnerDraft {
		return true
	}
	return !cfg.RequireIndependentReview || it.ReplyReviewed
}

func replyPolicySnapshotCurrent(it *InboxItem, input any) bool {
	cfg := replyPolicyConfigOf(input)
	if it == nil || !replyPolicyItemApplies(cfg, it) {
		return true
	}
	if !replyPolicyClassificationCurrent(it, cfg) {
		return false
	}
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		return it.ReplyPolicy != nil && it.ReplyPolicy.Route == ReplyRouteOwnerDraft
	}
	route := replyRoute(it.ReplyCategory, cfg)
	return route == ReplyRouteOwnerDraft && it.ReplyMode == route
}

// replyPolicyQueuePending leaves a not-yet-classified item queued without starting work. The next pass classifies it;
// only a current, definitively non-owner-draft route is released like TwinNever.
func replyPolicyQueuePending(it *InboxItem, cfg *assistantwire.InboxReplyPolicyConfig) bool {
	if !legacyReplyPolicyConfig(cfg) {
		return false
	}
	return replyPolicyItemApplies(cfg, it) && validReplyPolicyConfig(cfg) && !replyPolicyClassificationCurrent(it, cfg)
}

func replyPolicyQueueBlocked(it *InboxItem, cfg *assistantwire.InboxReplyPolicyConfig) bool {
	if !legacyReplyPolicyConfig(cfg) {
		return false
	}
	if !replyPolicyItemApplies(cfg, it) {
		return false
	}
	if !validReplyPolicyConfig(cfg) {
		return true
	}
	if !replyPolicyClassificationCurrent(it, cfg) {
		return false
	}
	route := replyRoute(it.ReplyCategory, cfg)
	return route != ReplyRouteOwnerDraft || it.ReplyMode != route
}

func replyPolicySuggestionGate(it *InboxItem, kind string, cfg *assistantwire.InboxReplyPolicyConfig) error {
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if kind != SuggestReply && kind != SuggestAgent || !policy.appliesToItem(it) {
			return nil
		}
		return policy.allowsOwnerDraft(it.ReplyPolicy, replyPolicyContentVersion(it))
	}
	if (kind != SuggestReply && kind != SuggestAgent) || !replyPolicyItemApplies(cfg, it) {
		return nil
	}
	if !replyPolicySnapshotCurrent(it, cfg) {
		return errReplyPolicyDenied
	}
	return nil
}

// replyPolicyActionGate binds both reply and Agent confirmations to the classification the owner saw. In particular,
// an Agent suggestion created before a policy was enabled, or before its category became restricted, cannot execute.
func replyPolicyActionGate(it *InboxItem, g *InboxSuggestion, cfg *assistantwire.InboxReplyPolicyConfig) error {
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if g == nil || g.Kind != SuggestReply && g.Kind != SuggestAgent || !policy.appliesToItem(it) {
			return nil
		}
		return verifyReplyPolicySuggestionLocked(it, g, suggestionAttempt(it, g.Version), policy)
	}
	if g == nil || g.Kind != SuggestReply && g.Kind != SuggestAgent || !replyPolicyItemApplies(cfg, it) {
		return nil
	}
	if !replyPolicySnapshotCurrent(it, cfg) || g.ReplyCategory != it.ReplyCategory || g.ReplyMode != it.ReplyMode ||
		g.ReplyClassifiedOf != it.ReplyClassifiedOf || g.ReplyPolicyRev != it.ReplyPolicyRev || g.ReplyReviewed != it.ReplyReviewed {
		return errReplyPolicyDenied
	}
	return nil
}

func replyPolicySendGate(it *InboxItem, g *InboxSuggestion, cfg *assistantwire.InboxReplyPolicyConfig) error {
	// Compiled suggestions are authorized by verifyReplyPolicySuggestionLocked / verifyReplyPolicySendAttemptLocked,
	// which preserve the durable-attempt error mapping. This early gate belongs only to the legacy field snapshot.
	if legacyReplyPolicyConfig(cfg) && g != nil && g.Kind == SuggestReply && replyPolicyItemApplies(cfg, it) {
		return replyPolicyActionGate(it, g, cfg)
	}
	return nil
}

// mirroredLegacyReply identifies the AI draft exposed to old clients through agent.draft. A direct owner-authored reply
// without this mirror stays on the legacy /reply path; a reply that edits the mirrored draft is still an AI suggestion
// confirmation and must pass the same edit veto and snapshot authorization as the versioned suggestion API.
func mirroredLegacyReply(it *InboxItem) *InboxSuggestion {
	if it == nil || it.Agent == nil || it.Agent.State != AgentDrafted {
		return nil
	}
	g := currentSuggestion(it)
	if g == nil || g.Kind != SuggestReply || g.Handback || it.Agent.Draft == "" || it.Agent.Draft != g.Text || suggestionAttempt(it, g.Version) != nil {
		return nil
	}
	return g
}

// replyInstructionForSuggestion returns the latest owner rewrite instruction not consumed by a later owner decision.
// Reported progress and other unrelated chain entries do not consume it. A published replacement carries the chain ID,
// but that ID authorizes only the identical generated text; every changed replacement needs its own classification.
func chainReplyInstruction(chain []InboxChainEntry) *InboxChainEntry {
	for i := len(chain) - 1; i >= 0; i-- {
		switch chain[i].Kind {
		case ChainConfirmed, ChainRejected:
			return nil
		case ChainInstructed:
			instruction := chain[i]
			return &instruction
		}
	}
	return nil
}

func replyInstructionForSuggestion(it *InboxItem, proposedText string, input any) (*InboxChainEntry, bool) {
	if it == nil {
		return nil, false
	}
	instruction := it.ReplyInstruction
	if instruction == nil {
		instruction = chainReplyInstruction(it.Chain)
	}
	if instruction == nil {
		return nil, false
	}
	g := currentSuggestion(it)
	needsClassification := instruction.ID == "" || g == nil || g.ReplyInstruction != instruction.ID || g.Text != proposedText
	if !needsClassification {
		switch policy := input.(type) {
		case replyPolicySpec:
			needsClassification = policy.allowsOwnerDraft(g.ReplyPolicy, replyPolicyContentVersion(it)) != nil
		default:
			cfg := replyPolicyConfigOf(input)
			if legacyReplyPolicyConfig(cfg) {
				needsClassification = replyPolicyActionGate(it, g, cfg) != nil
			} else {
				needsClassification = compileReplyPolicy(cfg).allowsOwnerDraft(g.ReplyPolicy, replyPolicyContentVersion(it)) != nil
			}
		}
	}
	return instruction, needsClassification
}

func snapshotReplyPolicy(g *InboxSuggestion, it *InboxItem, input any) {
	cfg := replyPolicyConfigOf(input)
	if g == nil || g.Kind != SuggestReply && g.Kind != SuggestAgent || !replyPolicyItemApplies(cfg, it) {
		return
	}
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		g.ReplyPolicy = cloneReplyPolicySnapshot(it.ReplyPolicy)
		return
	}
	g.ReplyCategory, g.ReplyMode = it.ReplyCategory, it.ReplyMode
	g.ReplyClassifiedOf, g.ReplyPolicyRev, g.ReplyReviewed = it.ReplyClassifiedOf, it.ReplyPolicyRev, it.ReplyReviewed
}

// strictReplyPolicyCategory accepts exactly one flat JSON object with no duplicate keys. Authorization must never
// depend on JSON's first-object-wins extraction or last-duplicate-wins unmarshalling.
func strictReplyPolicyCategory(raw string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return "", errors.New("reply policy response is not one JSON object")
	}
	seen := map[string]bool{}
	category := ""
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return "", errors.New("ambiguous duplicate reply policy key")
		}
		if key != "category" {
			return "", errors.New("unexpected reply policy key")
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", err
		}
		if key == "category" {
			if err := json.Unmarshal(value, &category); err != nil {
				return "", errors.New("invalid reply policy category")
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return "", err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", errors.New("multiple reply policy JSON values")
	}
	return strings.TrimSpace(category), nil
}

// classifyReplyPolicyInput returns only a category. The server, not the model, maps it to a route. Independent review
// is a second invocation that must reach the same category without seeing the first answer. An owner edit includes the
// original conversation: words such as "yes, go ahead" derive their authority from that context, not from the edit alone.
func (s *Service) classifyReplyPolicyInput(ctx context.Context, inputCfg any, in prompts.InboxReplyPolicyInput) (string, error) {
	cfg := replyPolicyConfigOf(inputCfg)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		data := replyPolicyItemData{id: "reply-policy-test", context: in.Context, contentVersion: "reply-policy-test"}
		snap, err := s.classifyReplyPolicy(ctx, compileReplyPolicy(cfg), data, in.ReplyText)
		if snap == nil {
			return "", err
		}
		return snap.Category, err
	}
	if cfg == nil {
		return "unknown", errors.New("missing reply policy config")
	}
	if !validReplyPolicyConfig(cfg) {
		return "unknown", errors.New("invalid reply policy config")
	}
	if strings.TrimSpace(in.Text) == "" {
		return "unknown", errors.New("empty reply policy input")
	}
	input := prompts.RenderInboxReplyPolicyInput(in)
	classify := func(system string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		raw, err := s.inboxModelComplete(cctx, purposeTriage, prompts.RenderJSONOnly(system), input, 1024)
		if err != nil {
			return "", err
		}
		category, err := strictReplyPolicyCategory(raw)
		if err != nil {
			return "", err
		}
		if !replyPolicyCategories[category] {
			return "", errors.New("invalid reply policy category")
		}
		return category, nil
	}
	category, err := classify(prompts.RenderInboxReplyPolicy())
	if err != nil {
		return "unknown", err
	}
	if cfg.RequireIndependentReview {
		reviewed, err := classify(prompts.RenderInboxReplyPolicyReview())
		if err != nil {
			return "unknown", err
		}
		if reviewed != category {
			return "unknown", errors.New("reply policy review disagreed")
		}
	}
	return category, nil
}

func (s *Service) classifyReplyPolicyText(ctx context.Context, cfg *assistantwire.InboxReplyPolicyConfig, text string) (string, error) {
	return s.classifyReplyPolicyInput(ctx, cfg, prompts.InboxReplyPolicyInput{Text: text})
}

// classifyInboxReplyPolicies classifies actionable items before their queue pass. Failures are persisted as unknown /
// owner_only for this exact content and policy revision, so a model outage cannot turn into a draft on a later gate.
func (s *Service) classifyInboxReplyPolicies(ctx context.Context, input any) {
	cfg := replyPolicyConfigOf(input)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if !policy.enabled {
			return
		}
		b := &s.inbox
		b.mu.Lock()
		var todo []*InboxItem
		for _, it := range b.items {
			if !it.open() || !policy.appliesToItem(it) {
				continue
			}
			if it.Tier != TierAction && b.policyTwin(it) != TwinFirst && !it.handled() {
				continue
			}
			if !replyPolicyClassificationCurrent(it, cfg) {
				todo = append(todo, it)
			}
		}
		b.mu.Unlock()
		for _, it := range todo {
			if err := s.classifyInboxReplyPolicyItem(ctx, cfg, it); err != nil {
				s.logf("inbox: reply policy classification for %s: %v", it.ID, err)
			}
		}
		return
	}
	if cfg == nil || !cfg.Enabled || !validReplyPolicyConfig(cfg) {
		return
	}
	b := &s.inbox
	b.mu.Lock()
	var todo []*InboxItem
	for _, it := range b.items {
		if it.VisibilityExcluded || !it.open() || !replyPolicyItemApplies(cfg, it) {
			continue
		}
		// Classification runs before escalation. TwinFirst can enqueue an already-tiered FYI item, and an existing
		// handling session can receive work outside TierAction; both need a snapshot before the queue pass.
		if it.Tier != TierAction && b.policyTwin(it) != TwinFirst && !it.handled() {
			continue
		}
		stale := it.ReplyClassifiedOf != replyPolicyContentVersion(it) || it.ReplyPolicyRev != cfg.Revision
		if replyRoute(it.ReplyCategory, cfg) == ReplyRouteOwnerDraft && cfg.RequireIndependentReview && !it.ReplyReviewed {
			stale = true
		}
		if stale {
			todo = append(todo, it)
		}
	}
	b.mu.Unlock()
	for _, it := range todo {
		if err := s.classifyInboxReplyPolicyItem(ctx, cfg, it); err != nil {
			s.logf("inbox: reply policy classification for %s: %v", it.ID, err)
		}
	}
}

// classifyInboxReplyPolicyForDraft gives an explicitly requested on-demand draft the same classification fence as a
// queue pass, including an open FYI item that has no TwinFirst rule or previous handling session.
func (s *Service) classifyInboxReplyPolicyForDraft(ctx context.Context, id string, cfg *assistantwire.InboxReplyPolicyConfig) error {
	b := &s.inbox
	b.mu.Lock()
	it := b.items[id]
	b.mu.Unlock()
	if it == nil {
		return nil // draftReply returns the ordinary not-found error below.
	}
	return s.classifyInboxReplyPolicyItem(ctx, cfg, it)
}

// classifyInboxReplyPolicyItem classifies one current item and persists the exact content/policy snapshot. Model
// failures become the durable unknown/owner_only result; only a failed persistence write is returned to the caller.
func (s *Service) classifyInboxReplyPolicyItem(ctx context.Context, input any, it *InboxItem) error {
	cfg := replyPolicyConfigOf(input)
	if cfg != nil && !legacyReplyPolicyConfig(cfg) {
		policy := compileReplyPolicy(cfg)
		if !policy.enabled {
			return nil
		}
		b := &s.inbox
		b.mu.Lock()
		if !it.open() || !policy.appliesToItem(it) || replyPolicyClassificationCurrent(it, cfg) {
			b.mu.Unlock()
			return nil
		}
		data := snapshotItemForReplyPolicyLocked(it)
		it.replyClassifySerial++
		serial := it.replyClassifySerial
		b.mu.Unlock()

		snap, _ := s.classifyReplyPolicy(ctx, policy, data, "")
		if ctx.Err() != nil {
			return nil
		}
		b.mu.Lock()
		if !it.open() || replyPolicyContentVersion(it) != data.contentVersion || it.replyClassifySerial != serial {
			b.mu.Unlock()
			return nil
		}
		w := beginWrite(it)
		it.ReplyPolicy = snap
		b.settle(w)
		b.mu.Unlock()
		if failed := s.saveWrites(ctx, []*inboxWrite{w}, "its reply policy classification"); failed[it.ID] {
			return errInboxNotPersisted
		}
		return nil
	}
	if cfg == nil || !cfg.Enabled || !validReplyPolicyConfig(cfg) {
		return nil
	}
	b := &s.inbox
	b.mu.Lock()
	if it.VisibilityExcluded || !it.open() || !replyPolicyItemApplies(cfg, it) || replyPolicyClassificationCurrent(it, cfg) {
		b.mu.Unlock()
		return nil
	}
	version, text, truncated := replyPolicyContentVersion(it), it.Context, replyPolicyContextTruncated(it)
	// A slower earlier model response must not overwrite a newer classification of the same content. The serial is
	// process-local concurrency evidence; the durable category/content/revision snapshot remains the restart fence.
	it.replyClassifySerial++
	serial := it.replyClassifySerial
	b.mu.Unlock()
	allowed, visibilityErr := s.inboxVisibilityModelAllowed(ctx, it)
	if visibilityErr == nil && !allowed {
		return nil
	}
	category, err := "unknown", visibilityErr
	if visibilityErr == nil {
		if truncated {
			err = errors.New("merged reply context was truncated")
		} else {
			category, err = s.classifyReplyPolicyText(ctx, cfg, text)
		}
	}
	if err != nil {
		category = "unknown"
	}
	b.mu.Lock()
	if !it.open() || replyPolicyContentVersion(it) != version || it.replyClassifySerial != serial {
		b.mu.Unlock()
		return nil
	}
	w := beginWrite(it)
	it.ReplyCategory = category
	it.ReplyMode = replyRoute(category, cfg)
	it.ReplyClassifiedOf, it.ReplyPolicyRev = version, cfg.Revision
	it.ReplyReviewed = err == nil && cfg.RequireIndependentReview
	b.settle(w)
	b.mu.Unlock()
	if failed := s.saveWrites(ctx, []*inboxWrite{w}, "its reply policy classification"); failed[it.ID] {
		return errInboxNotPersisted
	}
	return nil
}

// vetoEditedReply rechecks owner-introduced text before confirmation can reserve a send. Unedited text already carries
// the item's trusted classification; an edited text must independently classify as an owner-draft route.
func (s *Service) vetoEditedReply(ctx context.Context, id string, in suggestionReq, cfg *assistantwire.InboxReplyPolicyConfig) error {
	if in.Action != suggestConfirm || in.Text == nil || strings.TrimSpace(*in.Text) == "" {
		return nil
	}
	b := &s.inbox
	b.mu.Lock()
	it := b.items[id]
	var g *InboxSuggestion
	if it != nil {
		g = currentSuggestion(it)
	}
	if it == nil || g == nil || (g.Kind != SuggestReply && g.Kind != SuggestAgent) || g.Version != in.Version || !replyPolicyItemApplies(cfg, it) {
		b.mu.Unlock()
		return nil
	}
	text := strings.TrimSpace(*in.Text)
	original, conversation := g.Text, it.Context
	b.mu.Unlock()
	if text == original {
		return nil
	}
	category, err := s.classifyReplyPolicyInput(ctx, cfg, prompts.InboxReplyPolicyInput{Text: text, Original: conversation, Edit: true})
	if err != nil || replyRoute(category, cfg) != ReplyRouteOwnerDraft {
		// This veto was required by the enabled policy under which classification just ran. A later disable can restore
		// legacy behavior for a new attempt, but it cannot retroactively turn this rejected edit into an authorized send.
		return errReplyPolicyDenied
	}
	return nil
}

// Default reply-policy category names mapped on the server when inbox.reply_policy.categories is omitted.
const (
	// Normal owner-draftable category.
	ReplyCategoryRoutineReply = "routine_reply"

	// Four owner-only categories: must never produce an AI owner draft or confirmed suggestion send.
	ReplyCategoryApprovalCommitment = "approval_commitment"
	ReplyCategorySensitivePersonal  = "sensitive_personal"
	ReplyCategoryExternalPublic     = "external_public"
	ReplyCategoryEscalationDecision = "escalation_decision"

	// Five bot categories: must never produce an AI owner draft or confirmed suggestion send.
	ReplyCategoryBotNotification = "bot_notification"
	ReplyCategorySystemAlert     = "system_alert"
	ReplyCategoryWorkflowNotice  = "workflow_notice"
	ReplyCategoryAutomatedDigest = "automated_digest"
	ReplyCategoryBotCommand      = "bot_command"

	// No-reply category.
	ReplyCategoryNoReply = "no_reply"
)

const defaultReplyPolicyTimeout = 30 * time.Second

// independentReplyPolicyReviewPrompt deliberately contains no first-classifier answer or category list. The second
// invocation sees the same evidence and must independently return the identical category.
const independentReplyPolicyReviewPrompt = "independent reply-policy review prompt"

var (
	errReplyPolicyBlocked         = i18n.ErrEnglish("inbox-errors.reply_policy_blocked")
	errReplyPolicyUnknownCategory = errors.New("unknown reply policy category")
	errReplyPolicyInvalidRoute    = errors.New("invalid reply policy route")
	errReplyPolicyStaleSnapshot   = errors.New("stale reply policy snapshot")
	errReplyPolicyMissingSnapshot = errors.New("missing reply policy snapshot")
)

// OwnerOnlyReplyPolicyCategories returns the four default owner-only categories.
func OwnerOnlyReplyPolicyCategories() []string {
	return []string{
		ReplyCategoryApprovalCommitment,
		ReplyCategorySensitivePersonal,
		ReplyCategoryExternalPublic,
		ReplyCategoryEscalationDecision,
	}
}

// BotReplyPolicyCategories returns the five default bot categories.
func BotReplyPolicyCategories() []string {
	return []string{
		ReplyCategoryBotNotification,
		ReplyCategorySystemAlert,
		ReplyCategoryWorkflowNotice,
		ReplyCategoryAutomatedDigest,
		ReplyCategoryBotCommand,
	}
}

// DefaultReplyPolicyCategories returns a copy of the built-in server-side category-to-route table.
func DefaultReplyPolicyCategories() map[string]string {
	return map[string]string{
		ReplyCategoryRoutineReply:       assistantwire.ReplyRouteOwnerDraft,
		ReplyCategoryApprovalCommitment: assistantwire.ReplyRouteOwnerOnly,
		ReplyCategorySensitivePersonal:  assistantwire.ReplyRouteOwnerOnly,
		ReplyCategoryExternalPublic:     assistantwire.ReplyRouteOwnerOnly,
		ReplyCategoryEscalationDecision: assistantwire.ReplyRouteOwnerOnly,
		ReplyCategoryBotNotification:    assistantwire.ReplyRouteBot,
		ReplyCategorySystemAlert:        assistantwire.ReplyRouteBot,
		ReplyCategoryWorkflowNotice:     assistantwire.ReplyRouteBot,
		ReplyCategoryAutomatedDigest:    assistantwire.ReplyRouteBot,
		ReplyCategoryBotCommand:         assistantwire.ReplyRouteBot,
		ReplyCategoryNoReply:            assistantwire.ReplyRouteNoReply,
	}
}

// InboxReplyPolicySnapshot records a server-side reply-policy decision for one content version and policy revision.
// Persisted in InboxItem / InboxSuggestion JSON; stripped from listed client copies.
type InboxReplyPolicySnapshot struct {
	Category       string    `json:"category,omitempty"`
	Route          string    `json:"route,omitempty"`
	ContentVersion string    `json:"content_version,omitempty"`
	PolicyRevision string    `json:"policy_revision,omitempty"`
	Reviewed       bool      `json:"reviewed,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Error          string    `json:"error,omitempty"`
	At             time.Time `json:"at,omitempty"`
}

func cloneReplyPolicySnapshot(s *InboxReplyPolicySnapshot) *InboxReplyPolicySnapshot {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func sameReplyPolicyBinding(a, b *InboxReplyPolicySnapshot) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Category == b.Category &&
		a.Route == b.Route &&
		a.ContentVersion == b.ContentVersion &&
		a.PolicyRevision == b.PolicyRevision &&
		a.Reviewed == b.Reviewed
}

// replyPolicySpec is the compiled, immutable server-side reply policy for one evaluation.
type replyPolicySpec struct {
	enabled                  bool
	revision                 string
	categories               map[string]string // normalized category -> raw route ("" marks conflicting duplicate keys invalid)
	names                    []string          // sorted category names offered to the classifier
	sources                  map[string]bool
	requireIndependentReview bool
	rules                    string
	timeout                  time.Duration
}

func validReplyPolicyRoute(route string) bool {
	switch route {
	case assistantwire.ReplyRouteOwnerDraft,
		assistantwire.ReplyRouteBot,
		assistantwire.ReplyRouteOwnerOnly,
		assistantwire.ReplyRouteNoReply:
		return true
	default:
		return false
	}
}

// compileReplyPolicy normalizes inbox.reply_policy and derives a deterministic effective policy revision.
// Disabled or zero-value configuration returns an inactive spec so existing behavior stays byte-for-byte identical.
func compileReplyPolicy(input any) replyPolicySpec {
	var cfg assistantwire.InboxReplyPolicyConfig
	switch value := input.(type) {
	case assistantwire.InboxReplyPolicyConfig:
		cfg = value
	case *assistantwire.InboxReplyPolicyConfig:
		if value == nil {
			return replyPolicySpec{}
		}
		cfg = *value
	default:
		return replyPolicySpec{}
	}
	if !cfg.Enabled || legacyReplyPolicyConfig(&cfg) {
		return replyPolicySpec{}
	}
	rawCats := cfg.Categories
	if len(rawCats) == 0 {
		rawCats = DefaultReplyPolicyCategories()
	}
	cats := make(map[string]string, len(rawCats))
	// Sort raw keys so duplicate case/whitespace collisions resolve deterministically to invalid (fail closed).
	rawKeys := slices.Sorted(maps.Keys(rawCats))
	for _, k := range rawKeys {
		normKey := strings.ToLower(strings.TrimSpace(k))
		if normKey == "" {
			continue
		}
		normVal := strings.ToLower(strings.TrimSpace(rawCats[k]))
		if prev, exists := cats[normKey]; exists && prev != normVal {
			cats[normKey] = "" // ambiguous config fails closed
			continue
		}
		cats[normKey] = normVal
	}
	names := slices.Sorted(maps.Keys(cats))
	sources := map[string]bool{}
	for _, source := range cfg.Sources {
		if source = strings.TrimSpace(source); source != "" {
			sources[source] = true
		}
	}
	sourceNames := slices.Sorted(maps.Keys(sources))
	rules := strings.TrimSpace(cfg.Rules)
	timeout := defaultReplyPolicyTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}

	h := sha256.New()
	explicitRev := strings.TrimSpace(cfg.Revision)
	fmt.Fprintf(h, "rev=%s\nrules=%s\ntimeout=%d\nindependent_review=%t\n", explicitRev, rules, int(timeout/time.Second), cfg.RequireIndependentReview)
	for _, source := range sourceNames {
		fmt.Fprintf(h, "source=%s\n", source)
	}
	for _, name := range names {
		fmt.Fprintf(h, "%s=%s\n", name, cats[name])
	}
	digest := hex.EncodeToString(h.Sum(nil))
	rev := "sha256:" + digest[:16]
	if explicitRev != "" {
		rev = explicitRev + "@" + digest[:12]
	}
	return replyPolicySpec{
		enabled:                  true,
		revision:                 rev,
		categories:               cats,
		names:                    names,
		sources:                  sources,
		requireIndependentReview: cfg.RequireIndependentReview,
		rules:                    rules,
		timeout:                  timeout,
	}
}

// resolve maps a classifier-produced category name to a validated server route.
// Unknown categories and invalid routes always fail closed.
func (p replyPolicySpec) resolve(category string) (normCat, route string, err error) {
	normCat = strings.ToLower(strings.TrimSpace(category))
	if normCat == "" {
		return "", "", errReplyPolicyUnknownCategory
	}
	raw, ok := p.categories[normCat]
	if !ok {
		return normCat, "", errReplyPolicyUnknownCategory
	}
	if !validReplyPolicyRoute(raw) {
		return normCat, "", errReplyPolicyInvalidRoute
	}
	return normCat, raw, nil
}

func (p replyPolicySpec) appliesToItem(it *InboxItem) bool {
	if !p.enabled || it == nil {
		return false
	}
	if len(p.sources) == 0 {
		return true
	}
	for _, source := range replyPolicySourceSet(it) {
		if p.sources[source] {
			return true
		}
	}
	return false
}

// replyPolicyAllowsOwnerDraft checks whether snap authorizes owner-draft handling for the given content version
// under the live policy. Every missing, errored, stale, unknown, invalid, or non-owner_draft state fails closed.
func (p replyPolicySpec) allowsOwnerDraft(snap *InboxReplyPolicySnapshot, contentVersion string) error {
	if !p.enabled {
		return nil
	}
	if snap == nil {
		return errReplyPolicyMissingSnapshot
	}
	if snap.Error != "" {
		return fmt.Errorf("%w: %s", errReplyPolicyBlocked, snap.Error)
	}
	if contentVersion == "" || snap.ContentVersion != contentVersion || snap.PolicyRevision != p.revision {
		return errReplyPolicyStaleSnapshot
	}
	normCat, route, err := p.resolve(snap.Category)
	if err != nil {
		return err
	}
	if snap.Category != normCat || snap.Route != route || route != assistantwire.ReplyRouteOwnerDraft {
		return fmt.Errorf("%w: category %q routes to %q", errReplyPolicyBlocked, normCat, route)
	}
	if p.requireIndependentReview && !snap.Reviewed {
		return fmt.Errorf("%w: independent review is required", errReplyPolicyMissingSnapshot)
	}
	return nil
}

// replyPolicyItemData holds the lock-free snapshot of an item passed to the classifier.
type replyPolicyItemData struct {
	id             string
	title          string
	sender         string
	source         string
	kind           string
	context        string
	contentVersion string
	truncated      bool
	sources        []string
}

func snapshotItemForReplyPolicyLocked(it *InboxItem) replyPolicyItemData {
	return replyPolicyItemData{
		id:             it.ID,
		title:          it.Title,
		sender:         it.Sender,
		source:         it.Source,
		kind:           it.Kind,
		context:        it.Context,
		contentVersion: replyPolicyContentVersion(it),
		truncated:      replyPolicyContextTruncated(it),
		sources:        replyPolicySourceSet(it),
	}
}

func replyPolicyItemWithInstruction(item replyPolicyItemData, instruction *InboxChainEntry) replyPolicyItemData {
	if instruction != nil {
		item.context += "\n\nOwner draft instruction:\n" + instruction.Detail
	}
	return item
}

// strictReplyPolicyClassifierOutput accepts exactly one flat JSON object with no duplicate or unexpected keys.
// Authorization must not depend on first-object extraction or last-duplicate-wins unmarshalling.
func strictReplyPolicyClassifierOutput(raw string) (category, reason string, err error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return "", "", errors.New("reply policy response is not one JSON object")
	}
	seen := map[string]bool{}
	routeSeen := false
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return "", "", err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return "", "", errors.New("ambiguous duplicate reply policy key")
		}
		if key != "category" && key != "reason" && key != "route" {
			return "", "", errors.New("unexpected reply policy key")
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", "", err
		}
		if key == "route" {
			// Parse the whole object so the failed attempt can retain the server-resolved category, but never accept the
			// model's route or use it as authorization.
			routeSeen = true
			continue
		}
		target := &category
		if key == "reason" {
			target = &reason
		}
		if err := json.Unmarshal(value, target); err != nil {
			return "", "", fmt.Errorf("invalid reply policy %s", key)
		}
	}
	closing, err := dec.Token()
	if err != nil || closing != json.Delim('}') {
		return "", "", errors.New("unterminated reply policy JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", "", errors.New("multiple reply policy JSON values")
	}
	if routeSeen {
		return category, reason, errors.New("unexpected reply policy key")
	}
	return category, reason, nil
}

// classifyReplyPolicy invokes the small-task classifier model to obtain a category name and maps it to a route
// strictly on the server. A model-supplied route field is rejected and never used. Errors, timeouts, unknown
// categories, and invalid routes fail closed.
func (s *Service) classifyReplyPolicy(ctx context.Context, policy replyPolicySpec, item replyPolicyItemData, replyText string) (*InboxReplyPolicySnapshot, error) {
	now := time.Now().UTC()
	snap := &InboxReplyPolicySnapshot{
		ContentVersion: item.contentVersion,
		PolicyRevision: policy.revision,
		At:             now,
	}
	if !policy.enabled {
		return nil, nil
	}
	if item.truncated {
		// A partial input cannot authorize an owner draft. Persist a current, definitive owner-only result without
		// asking the model to infer the missing suffix.
		snap.Category, snap.Route, snap.Error = "unknown", assistantwire.ReplyRouteOwnerOnly, "reply context was truncated"
		if route := policy.categories["unknown"]; validReplyPolicyRoute(route) {
			snap.Route = route
		}
		return snap, fmt.Errorf("%w: reply context was truncated", errReplyPolicyBlocked)
	}
	if len(policy.names) == 0 {
		snap.Error = "no categories configured"
		return snap, errReplyPolicyUnknownCategory
	}
	hello := s.hello(ctx)
	language := answerLanguage(hello.Locale)
	system := prompts.RenderInboxReplyPolicy(prompts.InboxReplyPolicy{
		Language:   language,
		Owner:      inboxPromptOwner(hello),
		Categories: policy.names,
		Rules:      policy.rules,
	})
	input := prompts.RenderInboxReplyPolicyInput(prompts.InboxReplyPolicyInput{
		Language:  language,
		Title:     item.title,
		Sender:    item.sender,
		Source:    item.source,
		Kind:      item.kind,
		Context:   item.context,
		ReplyText: strings.TrimSpace(replyText),
	})
	cctx, cancel := context.WithTimeout(ctx, policy.timeout)
	raw, err := s.inboxModelComplete(cctx, purposeTriage, prompts.RenderJSONOnly(system), input, 1024)
	timedOut := errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	fail := func(message string, cause error) (*InboxReplyPolicySnapshot, error) {
		// Persist a definitive non-draft route with the exact content/policy snapshot. Error remains non-empty so a
		// later queue pass may retry, but no current or restarted gate can interpret the response as authorization.
		snap.Category, snap.Route, snap.Error = "unknown", assistantwire.ReplyRouteOwnerOnly, message
		if route := policy.categories["unknown"]; validReplyPolicyRoute(route) {
			snap.Route = route
		}
		if cause != nil {
			return snap, fmt.Errorf("%w: %s: %v", errReplyPolicyBlocked, message, cause)
		}
		return snap, fmt.Errorf("%w: %s", errReplyPolicyBlocked, message)
	}
	if err != nil {
		if timedOut {
			return fail("classification timed out", nil)
		}
		return fail("classification failed", nil)
	}
	category, reason, err := strictReplyPolicyClassifierOutput(raw)
	if err != nil {
		if category != "" {
			// A route-only schema violation still has a useful diagnostic category. Preserve its server-resolved route in
			// the durable failed attempt while Error keeps every authorization gate closed; all other malformed or
			// ambiguous responses retain the generic unknown/owner-only snapshot.
			normCat, route, resolveErr := policy.resolve(category)
			if resolveErr == nil && category == normCat {
				snap.Category, snap.Route, snap.Reason = normCat, route, clip(oneLine(reason), 80)
				snap.Error = "invalid classifier JSON"
				return snap, fmt.Errorf("%w: invalid classifier JSON: %v", errReplyPolicyBlocked, err)
			}
		}
		return fail("invalid classifier JSON", err)
	}
	normCat, route, err := policy.resolve(category)
	if err == nil && category != normCat {
		err = errReplyPolicyUnknownCategory
	}
	if err != nil {
		return fail("invalid classifier category", err)
	}
	snap.Category = normCat
	snap.Reason = clip(oneLine(reason), 80)
	snap.Route = route
	if policy.requireIndependentReview {
		reviewCtx, reviewCancel := context.WithTimeout(ctx, policy.timeout)
		reviewRaw, reviewErr := s.inboxModelComplete(reviewCtx, purposeTriage,
			prompts.RenderJSONOnly(system), input, 1024)
		reviewCancel()
		if reviewErr != nil {
			return fail("independent review failed", nil)
		}
		reviewedCategory, _, parseErr := strictReplyPolicyClassifierOutput(reviewRaw)
		if parseErr != nil {
			return fail("invalid independent review JSON", parseErr)
		}
		reviewedNorm, _, resolveErr := policy.resolve(reviewedCategory)
		if resolveErr != nil || reviewedCategory != reviewedNorm {
			return fail("invalid independent review category", resolveErr)
		}
		if reviewedNorm != normCat {
			return fail("independent review disagreed", nil)
		}
		snap.Reviewed = true
	}
	return snap, nil
}

// ---- Gate 1: Queue gate (before AI handling) ----

// gateReplyPolicyQueue classifies open items eligible for AI handling (and already-queued items without a session)
// whose reply-policy snapshot is missing, errored, or stale for the current content version or policy revision.
// Called before escalateWithPolicy and startInboxAgents in handPass.
func (s *Service) gateReplyPolicyQueue(ctx context.Context, policy replyPolicySpec, since time.Time) {
	if !policy.enabled {
		return
	}
	b := &s.inbox
	b.mu.Lock()
	var todo []replyPolicyItemData
	for _, it := range b.items {
		if !b.needsReplyPolicyQueueCheckLocked(it, policy, since) {
			continue
		}
		todo = append(todo, snapshotItemForReplyPolicyLocked(it))
	}
	b.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	var writes []*inboxWrite
	for _, data := range todo {
		snap, _ := s.classifyReplyPolicy(ctx, policy, data, "")
		if ctx.Err() != nil {
			return
		}
		b.mu.Lock()
		it := b.items[data.id]
		if it != nil && it.open() && replyPolicyContentVersion(it) == data.contentVersion {
			w := beginWrite(it)
			it.ReplyPolicy = snap
			b.settle(w)
			writes = append(writes, w)
		}
		b.mu.Unlock()
	}
	s.saveWrites(ctx, writes, "its reply policy classification")
}

// needsReplyPolicyQueueCheckLocked reports whether it is an escalation or queued-start candidate that needs a fresh
// reply-policy classification. Caller holds b.mu.
func (b *inboxBook) needsReplyPolicyQueueCheckLocked(it *InboxItem, policy replyPolicySpec, since time.Time) bool {
	if !policy.appliesToItem(it) || !it.open() || it.Context == "" {
		return false
	}
	cv := replyPolicyContentVersion(it)
	if it.ReplyPolicy != nil && it.ReplyPolicy.Error == "" &&
		it.ReplyPolicy.ContentVersion == cv && it.ReplyPolicy.PolicyRevision == policy.revision {
		return false // already has a non-error classification for this exact content and policy revision
	}
	if replyPolicyContextTruncated(it) {
		// Even an already-handled row must retain a durable fail-closed result for its incomplete merged input.
		return true
	}
	a := it.Agent
	// Already queued without a session: must be classified before startInboxAgents decides whether to start or release.
	if a != nil && a.State == AgentQueued && a.SessionID == "" && !a.savingRetry {
		return true
	}
	if a != nil && (a.Of == it.Last && !a.InterruptedByDisable || a.savingRetry) {
		return false
	}
	handled := it.handled()
	twin := b.policyTwin(it)
	if !handled && (twin == TwinNever || it.Tier != TierAction && twin != TwinFirst || !it.tiered()) {
		return false
	}
	if (a == nil || a.Of == "" && a.StartedAt == nil && a.SessionID == "") && !it.UpdatedAt.After(since) {
		return false
	}
	return true
}

// replyPolicyAllowsQueueLocked is Gate 1's enforcement check in escalateWithPolicy and startInboxAgents.
// Caller holds b.mu.
func replyPolicyAllowsQueueLocked(it *InboxItem, policy replyPolicySpec) bool {
	if !policy.appliesToItem(it) {
		return true
	}
	return policy.allowsOwnerDraft(it.ReplyPolicy, replyPolicyContentVersion(it)) == nil
}

// ---- Gate 2: Suggestion gate (when accepting an owner reply suggestion) ----

// gateReplyPolicySuggestion enforces inbox.reply_policy before accepting a reply suggestion (inbox_suggest /
// inbox_draft). Fails closed on any non-owner_draft category, unknown category, invalid route, classification
// failure/timeout, or stale snapshot.
func (s *Service) gateReplyPolicySuggestion(ctx context.Context, policy replyPolicySpec, item replyPolicyItemData, existing *InboxReplyPolicySnapshot, replyText string) (*InboxReplyPolicySnapshot, error) {
	if !policy.enabled {
		return nil, nil
	}
	if existing == nil && len(policy.sources) > 0 && len(item.sources) > 1 {
		// A policy that newly applies through retained member text needs an item classification first; a generated-text
		// response must not silently double as authorization for content whose source scope just changed.
		return nil, errReplyPolicyMissingSnapshot
	}
	// If the incoming item already has a fresh non-error classification that disallows owner drafts, fail closed at once.
	if existing != nil && existing.Error == "" &&
		existing.ContentVersion == item.contentVersion && existing.PolicyRevision == policy.revision {
		if err := policy.allowsOwnerDraft(existing, item.contentVersion); err != nil {
			return existing, err
		}
	}
	// Always classify the item together with the proposed reply text under the live policy so neither an unchecked
	// incoming item (e.g. handed-off or pre-policy session) nor an unsafe proposed reply can create an owner draft.
	snap, err := s.classifyReplyPolicy(ctx, policy, item, replyText)
	if err != nil {
		return snap, err
	}
	if err := policy.allowsOwnerDraft(snap, item.contentVersion); err != nil {
		return snap, err
	}
	return snap, nil
}

func replyPolicySuggestionErr(snap *InboxReplyPolicySnapshot, err error) error {
	if snap != nil && snap.Route != "" && snap.Route != assistantwire.ReplyRouteOwnerDraft {
		return errSuggestion{fmt.Sprintf("reply policy routed category %q to %q; an owner reply suggestion is not allowed", snap.Category, snap.Route)}
	}
	if err != nil {
		return errSuggestion{fmt.Sprintf("reply policy blocked this owner reply suggestion: %v", err)}
	}
	return errSuggestion{"reply policy blocked this owner reply suggestion"}
}

// ---- Gate 3: Send gate (immediately before the confirmed sender call) ----

// verifyReplyPolicySuggestionLocked checks that g has a valid, fresh, owner_draft reply-policy snapshot matching
// the item's current content version and the live policy revision. Caller holds b.mu.
func verifyReplyPolicySuggestionLocked(it *InboxItem, g *InboxSuggestion, a *InboxSuggestionAttempt, policy replyPolicySpec) error {
	if !policy.appliesToItem(it) {
		return nil
	}
	cv := replyPolicyContentVersion(it)
	if err := policy.allowsOwnerDraft(g.ReplyPolicy, cv); err != nil {
		return err
	}
	if !sameReplyPolicyBinding(g.ReplyPolicy, it.ReplyPolicy) {
		return errReplyPolicyStaleSnapshot
	}
	if a != nil {
		attemptSnap := &InboxReplyPolicySnapshot{
			Category:       a.Category,
			Route:          a.Route,
			ContentVersion: a.ContentVersion,
			PolicyRevision: a.PolicyRevision,
			Reviewed:       a.Reviewed,
		}
		if err := policy.allowsOwnerDraft(attemptSnap, cv); err != nil {
			return err
		}
	}
	return nil
}

// gateReplyPolicySend performs Gate 3's final-text veto immediately before confirmation proceeds to sending.
// It classifies the actual final text (original or owner-edited) under the live policy and fails closed on any
// non-owner_draft route, unknown category, invalid route, error, or timeout.
func (s *Service) gateReplyPolicySend(ctx context.Context, policy replyPolicySpec, item replyPolicyItemData, finalText string) (*InboxReplyPolicySnapshot, error) {
	if !policy.enabled {
		return nil, nil
	}
	snap, err := s.classifyReplyPolicy(ctx, policy, item, finalText)
	if err != nil {
		return snap, err
	}
	if err := policy.allowsOwnerDraft(snap, item.contentVersion); err != nil {
		return snap, err
	}
	return snap, nil
}

// verifyReplyPolicySendAttemptLocked is the synchronous pre-dispatch recheck immediately before the sender call
// (and again under once.mu in replyInboxDoMode) to prevent stale policy revisions or content changes from sending.
// Caller holds b.mu.
func verifyReplyPolicySendAttemptLocked(it *InboxItem, a *InboxSuggestionAttempt, policy replyPolicySpec) error {
	if !policy.appliesToItem(it) {
		return nil
	}
	if a == nil {
		return errReplyPolicyMissingSnapshot
	}
	cv := replyPolicyContentVersion(it)
	attemptSnap := &InboxReplyPolicySnapshot{
		Category:       a.Category,
		Route:          a.Route,
		ContentVersion: a.ContentVersion,
		PolicyRevision: a.PolicyRevision,
		Reviewed:       a.Reviewed,
	}
	if err := policy.allowsOwnerDraft(attemptSnap, cv); err != nil {
		return err
	}
	// The final-text veto may legitimately classify an edited reply differently from its original card. Bind the frozen
	// proposal to the item's authorization, while attemptSnap above independently authorizes the exact outgoing text.
	if a.Proposal == nil || !sameReplyPolicyBinding(a.Proposal.ReplyPolicy, it.ReplyPolicy) {
		return errReplyPolicyStaleSnapshot
	}
	return nil
}
