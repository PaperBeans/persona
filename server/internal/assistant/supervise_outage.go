package assistant

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modelretry"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Outage-scoped hold and cross-kind coalescing for supervision signals (#13158).
//
// During a provider-wide outage where typed provider/account-outage state and recovery metadata are
// available, outage-caused sigInterrupted and sigUnjudged signals for the same task are held and
// coalesced into a single sigOutageRecovery entry per task. Once the recovery point is reached (or
// positive recovery evidence appears earlier), one combined per-task recovery digest is delivered
// containing the task identity, latest task state, and suppressed signal count. Signals requiring an
// explicit owner decision, failures unrelated to the provider outage, and signals without recovery
// metadata retain their existing delivery behavior.

const (
	outageSourceCapacity = "capacity"
	outageSourceGateway  = "gateway"
	outageSourceAIPause  = "ai_pause"
)

// outageHoldMax bounds any outage hold so a malformed or repeatedly extended recovery timestamp can
// never suppress notifications indefinitely.
var outageHoldMax = 24 * time.Hour

// superviseOutage carries coalesced provider-outage hold metadata on a sigOutageRecovery signal.
type superviseOutage struct {
	Provider     string          `json:"provider,omitempty"`
	Source       string          `json:"source,omitempty"` // capacity | gateway | ai_pause
	Since        time.Time       `json:"since,omitzero"`
	Recovery     time.Time       `json:"recovery,omitzero"`
	Suppressed   int             `json:"suppressed,omitempty"`
	Interrupted  int             `json:"interrupted,omitempty"`
	Unjudged     int             `json:"unjudged,omitempty"`
	LatestKind   string          `json:"latest_kind,omitempty"`
	LatestAt     time.Time       `json:"latest_at,omitzero"`
	LatestDetail string          `json:"latest_detail,omitempty"`
	GateHeld     bool            `json:"gate_held,omitempty"`
	TurnKeys     []outageTurnKey `json:"turn_keys,omitempty"`
}

// outageTurnKey records one turn-ledger signal (Kind, Key) folded into a sigOutageRecovery hold so
// delivery or stale drop marks every folded turn signal told on the turn ledger.
type outageTurnKey struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type providerOutage struct {
	Provider string
	Source   string
	Recovery time.Time
	GateHeld bool
}

func (o *superviseOutage) clone() *superviseOutage {
	if o == nil {
		return nil
	}
	cp := *o
	cp.TurnKeys = slices.Clone(o.TurnKeys)
	return &cp
}

func (s *Service) outageHoldCoversTurn(todo, kind, key string) bool {
	if kind != sigInterrupted && kind != sigUnjudged {
		return false
	}
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	held, ok := b.pending[todo+"|"+sigOutageRecovery]
	return ok && held.Outage != nil && held.Outage.hasTurnKey(kind, key)
}

func (o *superviseOutage) hasTurnKey(kind, key string) bool {
	if o == nil || kind == "" || key == "" {
		return false
	}
	for _, tk := range o.TurnKeys {
		if tk.Kind == kind && tk.Key == key {
			return true
		}
	}
	return false
}

func (o *superviseOutage) addTurnKey(kind, key string) {
	if o == nil || kind == "" || key == "" || o.hasTurnKey(kind, key) {
		return
	}
	o.TurnKeys = append(o.TurnKeys, outageTurnKey{Kind: kind, Key: key})
}

func outageSignalKey(provider string, since time.Time) string {
	return fmt.Sprintf("outage@%s@%s", cmp.Or(provider, "provider"), since.UTC().Format(time.RFC3339Nano))
}

func boundOutageRecovery(anchor, recovery time.Time) time.Time {
	if maxAt := anchor.Add(outageHoldMax); recovery.After(maxAt) {
		return maxAt
	}
	return recovery
}

// outageCausedText reports whether an error text describes an upstream/provider availability failure
// rather than an unrelated client, auth, configuration, routing, or context error.
func outageCausedText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || proto.UnrecoverableTurnErrorCode(text) != "" {
		return false
	}
	code := turnErrorCode(text)
	if userTurnError(code) || code == proto.TurnErrorToolSearchUnsupported {
		return false
	}
	if code == proto.TurnErrorUpstreamUnavailable {
		return true
	}
	if modelretry.AccountExhausted(text) || modelretry.UpstreamServerError(text) {
		return true
	}
	return rateLimited.MatchString(text)
}

func sessionOutageCaused(c proto.Session) bool {
	if proto.SilentTurnKind(c.TurnErrorKind) || c.TurnErrorKind == proto.TurnErrorUnrecoverable {
		return false
	}
	if sessionTurnErrorCode(c) == proto.TurnErrorUpstreamUnavailable || c.TurnErrorCode == proto.TurnErrorUpstreamUnavailable {
		return true
	}
	if c.TurnErrorKind == proto.TurnErrorUsageLimit {
		return true
	}
	text, _ := latestTurnError(c)
	return outageCausedText(text)
}

func (s *Service) isOutageCausedInterrupted(ctx context.Context, t Todo, g superviseSignal) bool {
	if g.Kind != sigInterrupted {
		return false
	}
	if strings.TrimSpace(g.Detail) != "" {
		return outageCausedText(g.Detail)
	}
	agents := s.taskAgents(ctx, t)
	for _, c := range currentTaskAgents(agents, supersededAgents(t, agents)) {
		if sessionOutageCaused(c) {
			return true
		}
	}
	return false
}

func (s *Service) isOutageCausedUnjudged(ctx context.Context, t Todo, c taskDoneCheck, err error) bool {
	if err != nil {
		if ai.IsNotConfigured(err) || ai.IsModelUnavailable(err) || ai.IsContextTooLong(err) {
			return false
		}
		if ai.IsPaused(err) || ai.Transient(err) {
			return true
		}
		return outageCausedText(err.Error())
	}
	if c.Verdict != verdictUnsure {
		return false
	}
	if c.reasonKey == checkReasonNoReply {
		return true
	}
	agents := s.taskAgents(ctx, t)
	for _, agent := range currentTaskAgents(agents, supersededAgents(t, agents)) {
		if sessionOutageCaused(agent) {
			return true
		}
	}
	return false
}

func (s *Service) taskOutageTargets(ctx context.Context, t Todo) (providers []string, pools []GatewayQuotaPool) {
	addProvider := func(p string) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && !slices.Contains(providers, p) {
			providers = append(providers, p)
		}
	}
	addPool := func(pool GatewayQuotaPool) {
		if pool.valid() && !slices.Contains(pools, pool) {
			pools = append(pools, pool)
		}
	}
	agents := s.taskAgents(ctx, t)
	for _, c := range currentTaskAgents(agents, supersededAgents(t, agents)) {
		if c.Launcher != "" {
			if pool, ok := s.gatewayPool(ctx, c.Launcher); ok {
				addPool(pool)
				addProvider(pool.Provider)
			}
			if prov := s.launcherProvider(ctx, c.MachineID, c.Launcher); prov != "" {
				addProvider(prov)
			}
		}
		if c.Agent != "" {
			addProvider(c.Agent)
		}
	}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		if a.Agent != "" {
			if prov := s.launcherProvider(ctx, a.MachineID, a.Agent); prov != "" {
				addProvider(prov)
			} else {
				addProvider(a.Agent)
			}
		}
	}
	return providers, pools
}

func (s *Service) judgePause(ctx context.Context) (*ai.Pause, string) {
	b, err := s.resolveModel(ctx, "inbox.judge")
	if err != nil {
		return nil, ""
	}
	c, err := s.apiConfig(ctx, b)
	if err != nil || !c.Configured() {
		return nil, ""
	}
	return ai.Held(c), b.Provider
}

func (s *Service) judgeGateHeld(ctx context.Context) bool {
	p, _ := s.judgePause(ctx)
	return p != nil
}

// providerOutageFor returns the active typed provider/account-outage state and future recovery point
// for task t and signal g. When typed outage state or recovery metadata is absent, it returns false
// so callers fall back to the existing bounded backoff/dedup path rather than suppressing indefinitely.
func (s *Service) providerOutageFor(ctx context.Context, t Todo, g superviseSignal, causeErr error, now time.Time) (providerOutage, bool) {
	providers, pools := s.taskOutageTargets(ctx, t)
	if causeErr != nil {
		var pe *ai.PausedError
		if errors.As(causeErr, &pe) && pe.Until.After(now) {
			prov := ""
			if len(providers) > 0 {
				prov = providers[0]
			}
			if prov == "" {
				if _, jp := s.judgePause(ctx); jp != "" {
					prov = jp
				} else if pe.Reason != "" {
					prov = pe.Reason
				}
			}
			return providerOutage{
				Provider: prov,
				Source:   outageSourceAIPause,
				Recovery: boundOutageRecovery(now, pe.Until),
				GateHeld: s.judgeGateHeld(ctx),
			}, true
		}
	}
	if s.devs != nil {
		for _, pool := range pools {
			if recovery := s.devs.GatewayRetryAt(pool.Source, pool.Provider, now); recovery.After(now) {
				return providerOutage{
					Provider: pool.Provider,
					Source:   outageSourceGateway,
					Recovery: boundOutageRecovery(now, recovery),
				}, true
			}
		}
	}
	snap := s.capacitySnapshot(ctx)
	if len(providers) > 0 {
		for _, prov := range providers {
			p, ok := snap.Provider(prov)
			if !ok || !p.Saturated() {
				continue
			}
			if p.EarliestRecovery != nil && p.EarliestRecovery.After(now) {
				return providerOutage{
					Provider: prov,
					Source:   outageSourceCapacity,
					Recovery: boundOutageRecovery(now, *p.EarliestRecovery),
				}, true
			}
			// Typed provider saturation exists, but recovery metadata is absent: do not hold indefinitely.
			return providerOutage{}, false
		}
	} else if len(snap.Providers) > 0 {
		// Task has no explicit provider recorded: only hold when every fresh configured provider is
		// saturated and has future recovery metadata.
		var earliest time.Time
		var matched []string
		allSaturated := true
		freshCount := 0
		for _, p := range snap.Providers {
			if p.Accounts <= 0 || p.Stale {
				continue
			}
			freshCount++
			if !p.Saturated() || p.EarliestRecovery == nil || !p.EarliestRecovery.After(now) {
				allSaturated = false
				break
			}
			matched = append(matched, p.Provider)
			if earliest.IsZero() || p.EarliestRecovery.Before(earliest) {
				earliest = *p.EarliestRecovery
			}
		}
		if freshCount > 0 && allSaturated && earliest.After(now) {
			prov := matched[0]
			if len(matched) > 1 {
				prov = strings.Join(matched, ",")
			}
			return providerOutage{
				Provider: prov,
				Source:   outageSourceCapacity,
				Recovery: boundOutageRecovery(now, earliest),
			}, true
		}
	}
	if g.Kind == sigUnjudged {
		if pause, prov := s.judgePause(ctx); pause != nil && pause.Until.After(now) {
			if len(providers) > 0 {
				prov = providers[0]
			}
			return providerOutage{
				Provider: prov,
				Source:   outageSourceAIPause,
				Recovery: boundOutageRecovery(now, pause.Until),
				GateHeld: true,
			}, true
		}
	}
	return providerOutage{}, false
}

// signalOutageAware routes outage-caused sigInterrupted and sigUnjudged signals into a per-task
// outage hold when typed outage state and recovery metadata are present, and falls back to ordinary
// s.signal delivery otherwise.
func (s *Service) signalOutageAware(ctx context.Context, t Todo, g superviseSignal, causeErr error, outageCaused bool) {
	if !outageCaused || t.Supervision == nil || (g.Kind != sigInterrupted && g.Kind != sigUnjudged) || strings.HasPrefix(g.Key, "start-unknown@") {
		s.signal(g)
		return
	}
	now := time.Now()
	outage, ok := s.providerOutageFor(ctx, t, g, causeErr, now)
	if !ok {
		s.signal(g)
		return
	}
	if s.reporterStopped(ctx, t.Origin) {
		return
	}
	if g.At.IsZero() {
		g.At = now
	}
	s.restoreSupervisePending(ctx)
	if turnSignalKind(g) && s.turnSignalled(g) {
		b := &s.supervise
		b.mu.Lock()
		b.init()
		b.sent[g.id()] = g.Key
		b.mu.Unlock()
		return
	}
	meta := &superviseOutage{
		Provider:     outage.Provider,
		Source:       outage.Source,
		Since:        now.UTC(),
		Recovery:     outage.Recovery.UTC(),
		Suppressed:   1,
		LatestKind:   g.Kind,
		LatestAt:     now.UTC(),
		LatestDetail: clip(oneLine(strings.TrimSpace(g.Detail)), 240),
		GateHeld:     outage.GateHeld,
	}
	if g.Kind == sigInterrupted {
		meta.Interrupted = 1
	} else if g.Kind == sigUnjudged {
		meta.Unjudged = 1
	}
	meta.addTurnKey(g.Kind, g.Key)
	held := superviseSignal{
		Todo:      g.Todo,
		Kind:      sigOutageRecovery,
		Machine:   g.Machine,
		Session:   g.Session,
		Detail:    g.Detail,
		At:        now,
		NotBefore: outage.Recovery,
		Outage:    meta,
	}
	s.queueSignal(held)
}

// joinOutageRecovery coalesces two sigOutageRecovery signals for the same task during one outage.
// When cur and next carry the same hold Key (for example during restore or queue merge), it keeps
// the richer snapshot idempotently without double-counting suppressed symptoms.
func joinOutageRecovery(cur, next superviseSignal) superviseSignal {
	if cur.Outage == nil {
		return next
	}
	if next.Outage == nil {
		return cur
	}
	out := cur
	meta := cur.Outage.clone()
	in := next.Outage
	if cur.Key != "" && next.Key != "" && cur.Key == next.Key {
		if in.Suppressed > meta.Suppressed {
			meta.Suppressed = in.Suppressed
		}
		if in.Interrupted > meta.Interrupted {
			meta.Interrupted = in.Interrupted
		}
		if in.Unjudged > meta.Unjudged {
			meta.Unjudged = in.Unjudged
		}
	} else {
		added := max(in.Suppressed, 1)
		meta.Suppressed += added
		meta.Interrupted += in.Interrupted
		meta.Unjudged += in.Unjudged
	}
	for _, tk := range in.TurnKeys {
		meta.addTurnKey(tk.Kind, tk.Key)
	}
	if !in.LatestAt.Before(meta.LatestAt) {
		meta.LatestAt = in.LatestAt
		if in.LatestKind != "" {
			meta.LatestKind = in.LatestKind
		}
		if in.LatestDetail != "" {
			meta.LatestDetail = in.LatestDetail
		}
		if next.Detail != "" {
			out.Detail = next.Detail
		}
		if next.Machine != "" {
			out.Machine = next.Machine
		}
		if next.Session != "" {
			out.Session = next.Session
		}
	}
	if meta.Provider == "" {
		meta.Provider = in.Provider
	}
	if meta.Source == "" {
		meta.Source = in.Source
	}
	meta.GateHeld = meta.GateHeld || in.GateHeld
	anchor := meta.Since
	if anchor.IsZero() {
		anchor = out.At
	}
	if in.Recovery.After(meta.Recovery) {
		bounded := boundOutageRecovery(anchor, in.Recovery)
		meta.Recovery = bounded.UTC()
		if bounded.After(out.NotBefore) {
			out.NotBefore = bounded
		}
	}
	out.Outage = meta
	return out
}

// absorbPendingOutageSiblingLocked folds an already-pending undelivered outage-caused interrupted
// or unjudged signal for the same task into held so no duplicate intermediate signal escapes.
// b.mu is held.
func absorbPendingOutageSiblingLocked(b *superviseBook, held superviseSignal) (superviseSignal, []string) {
	if held.Kind != sigOutageRecovery || held.Outage == nil {
		return held, nil
	}
	var removed []string
	for _, kind := range []string{sigInterrupted, sigUnjudged} {
		id := held.Todo + "|" + kind
		sibling, ok := b.pending[id]
		if !ok || strings.HasPrefix(sibling.Key, "start-unknown@") {
			continue
		}
		if !outageCausedText(sibling.Detail) {
			continue
		}
		meta := &superviseOutage{
			Suppressed:   1,
			LatestKind:   kind,
			LatestAt:     sibling.At.UTC(),
			LatestDetail: clip(oneLine(strings.TrimSpace(sibling.Detail)), 240),
		}
		if kind == sigInterrupted {
			meta.Interrupted = 1
		} else {
			meta.Unjudged = 1
		}
		meta.addTurnKey(kind, sibling.Key)
		// Preserve held as the newer observation when sibling is older.
		siblingAsOutage := superviseSignal{
			Todo:    sibling.Todo,
			Kind:    sigOutageRecovery,
			Machine: sibling.Machine,
			Session: sibling.Session,
			Detail:  sibling.Detail,
			At:      sibling.At,
			Outage:  meta,
		}
		held = joinOutageRecovery(held, siblingAsOutage)
		delete(b.pending, id)
		removed = append(removed, id)
	}
	return held, removed
}

func signalNotBeforePending(g superviseSignal, now time.Time) bool {
	if g.Kind == sigOutageRecovery {
		return g.NotBefore.After(now)
	}
	return g.NotBefore.After(now.Add(time.Second))
}

// outageHoldStillValid verifies that a held sigOutageRecovery signal still describes an open,
// non-stale task at recovery or delivery time. Closed, reviewed, parked, owner-waiting, resumed,
// or cleanly recovered tasks return false so no obsolete recovery digest is emitted.
func (s *Service) outageHoldStillValid(ctx context.Context, g superviseSignal) (Todo, []proto.Session, bool) {
	t, ok := s.todoNow(g.Todo)
	if !ok || t.DoneAt != nil || t.Supervision == nil {
		return Todo{}, nil, false
	}
	v := t.Supervision
	st := v.state(t)
	if (st != "" && st != reviewRework) || v.Waits != nil || v.handoffResult() != nil {
		return Todo{}, nil, false
	}
	var latestAt time.Time
	if g.Outage != nil {
		latestAt = g.Outage.LatestAt
	}
	if !latestAt.IsZero() {
		s.tasks.mu.Lock()
		work, started := s.tasks.working[t.ID]
		s.tasks.mu.Unlock()
		if started && work.After(latestAt) {
			return Todo{}, nil, false
		}
	}
	agents := s.taskAgents(ctx, t)
	current := currentTaskAgents(agents, supersededAgents(t, agents))
	for _, c := range current {
		if c.Status == proto.StatusWorking || c.State == proto.StateWorking {
			return Todo{}, nil, false
		}
		if !latestAt.IsZero() && c.Status == proto.StatusDone && c.TurnError == "" && c.UpdatedAt.After(latestAt) {
			return Todo{}, nil, false
		}
	}
	return t, agents, true
}

func (s *Service) outageRecoveredEarly(ctx context.Context, g superviseSignal, now time.Time) bool {
	if g.Kind != sigOutageRecovery || g.Outage == nil || !g.NotBefore.After(now) {
		return false
	}
	o := g.Outage
	switch o.Source {
	case outageSourceCapacity, outageSourceGateway:
		if o.Provider == "" {
			return false
		}
		snap := s.capacitySnapshot(ctx)
		for _, prov := range strings.Split(o.Provider, ",") {
			prov = strings.TrimSpace(prov)
			if p, ok := snap.Provider(prov); ok && p.Accounts > 0 && !p.Stale && p.Available > 0 {
				return true
			}
		}
	case outageSourceAIPause:
		if o.GateHeld {
			pause, _ := s.judgePause(ctx)
			if pause == nil || !pause.Until.After(now) {
				return true
			}
		}
	}
	return false
}

// recheckOutageHolds drops closed/stale outage holds proactively and releases holds early when
// typed provider/account state positively shows recovery before the estimated recovery timestamp.
func (s *Service) recheckOutageHolds(ctx context.Context, now time.Time) {
	b := &s.supervise
	b.mu.Lock()
	var held []superviseSignal
	for _, g := range b.pending {
		if g.Kind == sigOutageRecovery {
			held = append(held, g)
		}
	}
	b.mu.Unlock()
	if len(held) == 0 {
		return
	}
	var drop, release []superviseSignal
	for _, g := range held {
		if _, _, valid := s.outageHoldStillValid(ctx, g); !valid {
			drop = append(drop, g)
			continue
		}
		if s.outageRecoveredEarly(ctx, g, now) {
			release = append(release, g)
		}
	}
	if len(drop) > 0 {
		b.mu.Lock()
		for _, g := range drop {
			if cur, ok := b.pending[g.id()]; ok && cur.Key == g.Key {
				delete(b.pending, g.id())
			}
		}
		b.mu.Unlock()
		s.turnsTold(ctx, drop)
		s.ackSupervisePending(drop)
	}
	if len(release) > 0 {
		var updated []superviseSignal
		b.mu.Lock()
		for _, g := range release {
			cur, ok := b.pending[g.id()]
			if !ok || cur.Key != g.Key {
				continue
			}
			cur.NotBefore = now
			if cur.Outage != nil {
				meta := cur.Outage.clone()
				meta.Recovery = now.UTC()
				cur.Outage = meta
			}
			b.pending[g.id()] = cur
			updated = append(updated, cur)
		}
		if len(updated) > 0 {
			s.updateSupervisePending(func(pending map[string]superviseSignal) {
				for _, cur := range updated {
					if p, ok := pending[cur.id()]; ok && p.Key == cur.Key {
						pending[cur.id()] = cur
					}
				}
			})
			s.scheduleDigestLocked(now)
		}
		b.mu.Unlock()
	}
}

func outageLang(language string) i18n.Lang {
	switch language {
	case "English":
		return i18n.En
	// i18n-ignore: language identifier returned by answerLanguage
	case "Japanese", "日本語":
		return i18n.Ja
	default:
		return i18n.ZhHans
	}
}

func (s *Service) outageLatestState(t Todo, agents []proto.Session, o *superviseOutage, language string) string {
	l := outageLang(language)
	var parts []string
	if t.Supervision != nil && t.Supervision.state(t) == reviewRework && t.Supervision.Round > 0 {
		parts = append(parts, i18n.T(l, "assistant-supervise_outage.rework_round", i18n.Args{"round": t.Supervision.Round}))
	}
	var latest proto.Session
	for _, c := range currentTaskAgents(agents, supersededAgents(t, agents)) {
		if latest.SessionID == "" || c.UpdatedAt.After(latest.UpdatedAt) {
			latest = c
		}
	}
	if latest.SessionID != "" {
		switch {
		case latest.Status == proto.StatusOffline || latest.State == proto.StateOffline:
			parts = append(parts, outageStateLabel(language, "offline"))
		case latest.Status == proto.StatusInterrupted:
			parts = append(parts, outageStateLabel(language, "interrupted"))
		case latest.TurnError != "":
			parts = append(parts, outageStateLabel(language, "stopped_error"))
		default:
			parts = append(parts, outageStateLabel(language, "stopped"))
		}
	} else if o != nil && o.LatestKind == sigUnjudged {
		parts = append(parts, outageStateLabel(language, "unjudged"))
	} else {
		parts = append(parts, outageStateLabel(language, "interrupted"))
	}
	if o != nil && o.LatestKind != "" {
		switch o.LatestKind {
		case sigUnjudged:
			parts = append(parts, outageStateLabel(language, "awaiting_judgment"))
		case sigInterrupted:
			parts = append(parts, outageStateLabel(language, "last_interrupted"))
		}
	}
	return strings.Join(parts, i18n.T(l, "assistant-supervise_outage.sep"))
}

func outageStateLabel(language, key string) string {
	l := outageLang(language)
	switch key {
	case "offline":
		return i18n.T(l, "assistant-supervise_outage.offline")
	case "interrupted":
		return i18n.T(l, "assistant-supervise_outage.interrupted")
	case "stopped_error":
		return i18n.T(l, "assistant-supervise_outage.stopped_error")
	case "stopped":
		return i18n.T(l, "assistant-supervise_outage.stopped")
	case "unjudged":
		return i18n.T(l, "assistant-supervise_outage.unjudged")
	case "awaiting_judgment":
		return i18n.T(l, "assistant-supervise_outage.awaiting_judgment")
	case "last_interrupted":
		return i18n.T(l, "assistant-supervise_outage.last_interrupted")
	}
	return key
}

func (s *Service) renderOutageRecovery(ctx context.Context, t Todo, agents []proto.Session, o *superviseOutage) string {
	lang := answerLanguage(string(s.botLang(ctx)))
	suppressed, interrupted, unjudged := 1, 0, 0
	provider, latestDetail := "", ""
	if o != nil {
		suppressed = max(o.Suppressed, 1)
		interrupted = o.Interrupted
		unjudged = o.Unjudged
		provider = o.Provider
		latestDetail = o.LatestDetail
	}
	return prompts.RenderOutageRecovery(prompts.OutageRecovery{
		Language:     lang,
		Provider:     provider,
		Suppressed:   suppressed,
		Interrupted:  interrupted,
		Unjudged:     unjudged,
		LatestState:  s.outageLatestState(t, agents, o, lang),
		LatestDetail: latestDetail,
	})
}
