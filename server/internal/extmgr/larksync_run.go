package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// A binding's runs. Each binding has a runtime of its own: its refresh lock (the platform rotates the refresh token,
// so two refreshes of one token would leave the second with an invalid grant), its running flag and its reader (whose
// rate limits are its own). Nothing a binding does touches another's: its failures, its backoff and its health are
// its own.

// larkBindingRuntime is one binding's in-memory state.
type larkBindingRuntime struct {
	refreshMu sync.Mutex
	// unsaved is a rotated token pair the settings could not take yet: the old refresh token no longer works, so it
	// is kept here and saved before anything else is asked of the binding.
	unsaved *larksyncwire.Token
	running atomic.Bool

	syncerMu sync.Mutex
	syncer   larksyncwire.Syncer
}

// larkSyncRuntime is key's runtime, made on first use.
func (h *Manager) larkSyncRuntime(key string) *larkBindingRuntime {
	s := &h.bots.sync
	s.rtMu.Lock()
	defer s.rtMu.Unlock()
	if s.runtimes == nil {
		s.runtimes = map[string]*larkBindingRuntime{}
	}
	rt := s.runtimes[key]
	if rt == nil {
		rt = &larkBindingRuntime{}
		s.runtimes[key] = rt
	}
	return rt
}

// larkSyncDropRuntime forgets key's runtime (the binding was removed).
func (h *Manager) larkSyncDropRuntime(key string) {
	s := &h.bots.sync
	s.rtMu.Lock()
	delete(s.runtimes, key)
	s.rtMu.Unlock()
}

// readerOf is the runtime's reader, made on first use.
func (h *Manager) readerOf(rt *larkBindingRuntime) larksyncwire.Syncer {
	rt.syncerMu.Lock()
	defer rt.syncerMu.Unlock()
	if rt.syncer == nil {
		rt.syncer = h.larkSyncNewSyncer()
	}
	return rt.syncer
}

// larkSyncKindOf is the error kind of err (ErrKindOther when it is not the module's).
func larkSyncKindOf(err error) string {
	var e *larksyncwire.Error
	if errors.As(err, &e) && e != nil {
		return e.Kind
	}
	return larksyncwire.ErrKindOther
}

// larkSyncTokenFunc is the token function a run of key is handed: every call gives the stored access token, refreshed
// first when it ends within larkSyncRefreshAt. The reader asks before every request, so a call never means the token
// was refused (that would refresh on every request); a refused token ends the run with ErrKindAuth, and the run
// refreshes once and reads again (larkSyncRunHeld). handed is the last token it gave (for that refresh).
func (h *Manager) larkSyncTokenFunc(key string) (token func(ctx context.Context) (string, error), handed func() string) {
	var mu sync.Mutex
	last := ""
	token = func(ctx context.Context) (string, error) {
		tok, err := h.larkSyncAccessToken(ctx, key, "")
		if err == nil {
			mu.Lock()
			last = tok
			mu.Unlock()
		}
		return tok, err
	}
	handed = func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
	return token, handed
}

// larkSyncAccessToken is key's access token, refreshed when it ends soon or is refused (the token a reader saw
// refused). Refreshing is single-flight per binding: a caller that waited for another's refresh reads the token it
// saved and does not refresh again. The rotated pair is saved, sealed, before it is handed out; an invalid grant marks
// the binding needs_reauth, and nothing tries again until the owner authorizes again.
func (h *Manager) larkSyncAccessToken(ctx context.Context, key, refused string) (string, error) {
	rt := h.larkSyncRuntime(key)
	rt.refreshMu.Lock()
	defer rt.refreshMu.Unlock()
	if rt.unsaved != nil {
		if err := h.larkSyncSaveToken(ctx, key, *rt.unsaved); err != nil {
			return "", err
		}
		rt.unsaved = nil
	}
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return "", err
	}
	b := st.binding(key)
	if b == nil || !b.authorized() {
		return "", &larksyncwire.Error{Kind: larksyncwire.ErrKindInvalidGrant, Op: "token", Msg: "not authorized"}
	}
	if b.Health == larksyncwire.HealthNeedsReauth {
		return "", &larksyncwire.Error{Kind: larksyncwire.ErrKindInvalidGrant, Op: "token", Msg: "authorize again"}
	}
	access, err := h.larkSyncOpen(b.AccessTokenEnc)
	if err != nil {
		return "", err
	}
	now := h.bots.sync.clock()
	// An end the platform never gave (zero) is not "ending soon": the token is refreshed only once it is refused.
	fresh := b.TokenExpiresAt.IsZero() || b.TokenExpiresAt.After(now.Add(larkSyncRefreshAt))
	if access != "" && access != refused && fresh {
		return access, nil
	}
	refresh, err := h.larkSyncOpen(b.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	ua := h.larkSyncUserAuth()
	if ua == nil {
		return "", errLarkSyncUnavailable
	}
	app, err := h.larkSyncApp(ctx, &st, b.BotID)
	if err != nil {
		return "", err
	}
	tok, err := ua.Refresh(ctx, app, refresh)
	if err != nil {
		if larkSyncKindOf(err) == larksyncwire.ErrKindInvalidGrant {
			h.logf("lark sync: %s: the refresh token was refused: authorize again", key)
			_, _ = h.larkSyncChange(ctx, func(st *larkSyncState) error {
				if b := st.binding(key); b != nil {
					b.Health, b.LastError = larksyncwire.HealthNeedsReauth, larkSyncDetail(err)
				}
				return nil
			})
		}
		return "", err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refresh // a platform that did not rotate it
	}
	if err := h.larkSyncSaveToken(ctx, key, tok); err != nil {
		rt.unsaved = &tok
		return "", err
	}
	return tok.AccessToken, nil
}

// larkSyncSaveToken keeps a refreshed pair on key's binding, sealed (rt.refreshMu held).
func (h *Manager) larkSyncSaveToken(ctx context.Context, key string, tok larksyncwire.Token) error {
	access, err := h.larkSyncSeal(tok.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := h.larkSyncSeal(tok.RefreshToken)
	if err != nil {
		return err
	}
	_, err = h.larkSyncChange(ctx, func(st *larkSyncState) error {
		b := st.binding(key)
		if b == nil {
			return errLarkSyncNoBinding
		}
		b.AccessTokenEnc, b.RefreshTokenEnc, b.TokenExpiresAt = access, refresh, tok.ExpiresAt
		if !tok.RefreshExpiresAt.IsZero() {
			b.RefreshExpiresAt = tok.RefreshExpiresAt
		}
		if len(tok.Scopes) > 0 {
			b.GrantedScopes = slices.Clone(tok.Scopes)
		}
		return nil
	})
	return err
}

// larkSyncDetail is err as a binding's last error: the platform's words, never a token.
func larkSyncDetail(err error) string {
	if err == nil {
		return ""
	}
	var e *larksyncwire.Error
	if errors.As(err, &e) && e != nil {
		return strings.TrimSpace(e.Kind + ": " + e.Msg)
	}
	return cutUTF8(err.Error(), 300)
}

// larkSyncStartRun starts a full run of key unless one is running; false when one is.
func (h *Manager) larkSyncStartRun(ctx context.Context, key string) bool {
	rt := h.larkSyncRuntime(key)
	if !rt.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer rt.running.Store(false)
		if err := h.larkSyncRunHeld(ctx, key, larksyncwire.ModeFull, rt); err != nil {
			h.logf("lark sync: %s: %v", key, err)
		}
	}()
	return true
}

// larkSyncRun runs a full sync of key now and waits for it; errLarkSyncRunning when a run is already under way.
func (h *Manager) larkSyncRun(ctx context.Context, key string) error {
	return h.larkSyncRunMode(ctx, key, larksyncwire.ModeFull)
}

// larkSyncRunMode runs key in mode (ModeFull or ModeIM) and waits for it; errLarkSyncRunning when a run of either mode
// is already under way on this binding (full and IM incremental runs share rt.running and never overlap).
func (h *Manager) larkSyncRunMode(ctx context.Context, key, mode string) error {
	rt := h.larkSyncRuntime(key)
	if !rt.running.CompareAndSwap(false, true) {
		return errLarkSyncRunning
	}
	defer rt.running.Store(false)
	return h.larkSyncRunHeld(ctx, key, mode, rt)
}

// larkSyncRunHeld is one run of key in mode (rt.running held): read with the binding's reader (sharing its per-binding
// rate limiter across full and IM incremental runs), then commit the cursors, the staged index records and the outbox
// rows in one transaction, so a run that fails before or during the hand-over moves nothing; then the binding's health
// and schedule, and the knowledge base is told.
func (h *Manager) larkSyncRunHeld(ctx context.Context, key, mode string, rt *larkBindingRuntime) error {
	store, _ := h.store.(larkSyncStore)
	if store == nil {
		return errLarkSyncUnavailable
	}
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return err
	}
	b := st.binding(key)
	if b == nil || b.pending() {
		return errLarkSyncNoBinding
	}
	app, err := h.larkSyncApp(ctx, &st, b.BotID)
	if err != nil {
		h.larkSyncFinish(ctx, key, mode, larksyncwire.Result{}, err, nil, false, 0, 0, b.AuthorizedAt, larkSyncNamesRound{})
		return err
	}
	reader := h.readerOf(rt)
	if reader == nil {
		return errLarkSyncUnavailable
	}
	tier := b.Tier
	if !larksyncwire.ValidTier(tier) {
		tier = st.tierOf(b.BotID)
	}
	label := b.Label
	if a := st.account(b.AccountID); a != nil && label == "" {
		label = a.Label
	}
	budget := larkSyncBudget
	if mode == larksyncwire.ModeIM {
		budget = larkSyncIMBudget
	}
	binding := larksyncwire.Binding{Key: key, BotID: b.BotID, AccountID: b.AccountID, Label: label, Tier: tier, Mode: mode,
		App: app, Backfill: larkSyncBackfill, Budget: budget, AuthorizedAt: b.AuthorizedAt, GrantedScopes: slices.Clone(b.GrantedScopes)}
	cursors, err := store.LarkSyncCursors(ctx, key)
	if err != nil {
		h.larkSyncFinish(ctx, key, mode, larksyncwire.Result{}, nil, err, false, 0, 0, b.AuthorizedAt, larkSyncNamesRound{})
		return err
	}
	linksBackfillVer := 0
	remediatedQueue := ""
	runCursors := cursors
	if tier == larksyncwire.TierFull && b.LinksBackfillVersion < larkSyncLinksBackfillVersion {
		if updated, done := h.larkSyncPrepareDroppedLinksBackfill(ctx, store, binding, &st, reader, cursors); done {
			linksBackfillVer = larkSyncLinksBackfillVersion
			if updated != "" {
				remediatedQueue = updated
				runCursors = maps.Clone(cursors)
				if runCursors == nil {
					runCursors = map[string]string{}
				}
				runCursors[larksyncwire.PendingLinksCursor] = updated
			}
		}
	}
	token, handed := h.larkSyncTokenFunc(key)
	idx := &larkSyncStaging{store: store}
	res, runErr := reader.Run(ctx, binding, token, runCursors, idx)
	if larkSyncKindOf(runErr) == larksyncwire.ErrKindAuth && ctx.Err() == nil {
		// The access token was refused before it ran out (revoked, or the app's secret changed): refresh once and
		// read again from the same cursors, with nothing of the first try kept. A refused refresh is an invalid grant
		// (needs_reauth); refused again after a good refresh, the run ends as auth (needs_reauth too).
		if refused := handed(); refused == "" {
			// No token was ever handed: the token func itself failed (the settings, the seal), which the reader
			// reports as auth. That is not the owner's to fix by authorizing again.
			runErr = &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: "token", Msg: larkSyncDetail(runErr)}
		} else if _, err := h.larkSyncAccessToken(ctx, key, refused); err != nil {
			runErr = err
		} else {
			idx = &larkSyncStaging{store: store}
			res, runErr = reader.Run(ctx, binding, token, runCursors, idx)
		}
	}
	var commitErr error
	var chats, docs int
	madeProgress := false
	backfillVer := 0
	committedLinksBackfillVer := 0
	var names larkSyncNamesRound
	if runErr == nil {
		moved := larkSyncMovedCursors(res)
		if remediatedQueue != "" {
			if _, ok := moved[larksyncwire.PendingLinksCursor]; !ok {
				moved[larksyncwire.PendingLinksCursor] = remediatedQueue
			}
		}
		madeProgress = larkSyncMadeProgress(cursors, res, moved)
		var rows []db.LarkSyncOutbox
		rows, chats, docs = larkSyncOutboxRows(binding, res, h.bots.sync.clock())
		commitErr = store.CommitLarkSyncRun(ctx, key, moved, idx.records(), rows)
		if commitErr == nil {
			committedLinksBackfillVer = linksBackfillVer
			if len(res.Messages) > 0 {
				h.emitLarkMessages(binding.Key, res.Messages)
			}
			if tier == larksyncwire.TierFull && b.BackfillVersion < larkSyncBackfillVersion {
				book := cursors[larksyncwire.SenderNamesCursor]
				if v, ok := moved[larksyncwire.SenderNamesCursor]; ok {
					book = v
				}
				progress := cursors[larksyncwire.SenderNamesProgressCursor]
				repaired, done, repairErr := h.larkSyncBackfillSenderNames(ctx, store, binding, &st, reader, token, handed, book, progress)
				chats += repaired
				names.ran = true
				if repairErr != nil {
					if kind := larkSyncKindOf(repairErr); larkSyncNamesBlocking(kind) {
						// Only what the owner must fix (authorize again, approve the contact scope) reaches the
						// binding's health; the scope reset below keeps new unnamed messages eligible.
						res.Errors = append(res.Errors, larksyncwire.ResourceError{
							Resource: larksyncwire.SenderNamesCursor, Kind: kind, Detail: larkSyncDetail(repairErr),
						})
					} else {
						// A lookup that timed out, was rate limited or failed passingly only degrades naming: the
						// messages are already committed, and the next round resumes from the saved progress.
						names.warning = strings.TrimSpace(larksyncwire.SenderNamesCursor + " " + kind + " " + larkSyncDetail(repairErr))
					}
				}
				if done {
					backfillVer = larkSyncBackfillVersion
				}
			}
		}
	}
	if gone := h.larkSyncFinish(ctx, key, mode, res, runErr, commitErr, madeProgress, backfillVer, committedLinksBackfillVer, b.AuthorizedAt, names); gone {
		// The binding was removed while it ran: what the run committed stays in the knowledge base, its cursors go.
		h.larkSyncForget(ctx, key)
		return errLarkSyncNoBinding
	}
	if runErr == nil && commitErr == nil {
		if chats > 0 {
			h.kbKick(larksyncwire.ConnectorID, connector.ScopeChats)
		}
		if docs > 0 {
			h.kbKick(larksyncwire.ConnectorID, connector.ScopeItems)
		}
	}
	return errors.Join(runErr, commitErr)
}

// larkSyncMadeProgress reports whether a run delivered any content or advanced at least one cursor beyond its
// pre-run value. Used by ModeIM scheduling so a partial run that made forward progress keeps the 15-minute cadence
// instead of backing off for an hour.
func larkSyncMadeProgress(prev map[string]string, res larksyncwire.Result, moved map[string]string) bool {
	if len(res.Messages) > 0 || len(res.Docs) > 0 {
		return true
	}
	for k, v := range moved {
		if v != "" && v != prev[k] {
			return true
		}
	}
	return false
}

// larkSyncBackfillSenderNames performs a one-time, idempotent sender-name and unnamed-DM backfill for chat messages
// that landed in the outbox and knowledge base with empty SenderName or empty DM Chat.Name (e.g. pre-#15486 rows, or
// personal/external tenants where contact/v3 cannot see external users). It scans chat outbox rows in sequence order,
// skips messages/DMs already repaired by a newer row, restricts lookups to rows owned by binding.Key (verified through
// lark_sync_index for multi-tenant isolation), resolves sender and p2p chat names via ChatNameResolver (with chat
// members, chat info, and message-carried fallbacks) or legacy NameResolver, updates im:p2p_chats cursors when DM
// names are learned, and emits repair chat batches so kb.Engine.Ingest updates lines, Person.Name, and DM Chat.Name
// in place. done is true when the binding can be marked at larkSyncBackfillVersion.
//
// It runs in rounds (one per sync run until done): each round's lookups have their own budget (larkSyncNamesBudget,
// apart from the run's), stop where the budget runs out, and save how far they got
// (larksyncwire.SenderNamesProgressCursor, progress is its stored value) together with the names learned (the name
// book), so the next round resumes instead of starting over.
func (h *Manager) larkSyncBackfillSenderNames(
	ctx context.Context,
	store larkSyncStore,
	binding larksyncwire.Binding,
	st *larkSyncState,
	reader larksyncwire.Syncer,
	token func(ctx context.Context) (string, error),
	handed func() string,
	nameBook string,
	progress string,
) (repairedChats int, done bool, repairErr error) {
	chatResolver, hasChatResolver := reader.(larksyncwire.ChatNameResolver)
	legacyResolver, hasLegacyResolver := reader.(larksyncwire.NameResolver)
	if !hasChatResolver && !hasLegacyResolver {
		return 0, false, nil
	}
	type candidate struct {
		chat kbwire.SourceChat
		msg  kbwire.SourceMessage
	}
	type dmCandidate struct {
		chat        kbwire.SourceChat
		sampleMsgID string
	}
	candidates := map[string]candidate{}
	var order []string
	unnamedDMs := map[string]dmCandidate{}
	var dmOrder []string
	var after int64
	for {
		rows, err := store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, after, 100)
		if err != nil {
			h.logf("lark sync: %s: sender-name backfill scan: %v", binding.Key, err)
			return 0, false, err
		}
		for _, row := range rows {
			after = row.Seq
			var cb connector.ChatBatch
			if err := json.Unmarshal(row.Payload, &cb); err != nil {
				continue
			}
			if cb.Chat.Kind == "dm" && cb.Chat.ID != "" {
				if strings.TrimSpace(cb.Chat.Name) != "" {
					delete(unnamedDMs, cb.Chat.ID)
				} else {
					dc, exists := unnamedDMs[cb.Chat.ID]
					if !exists {
						dmOrder = append(dmOrder, cb.Chat.ID)
						dc.chat = cb.Chat
					}
					if dc.sampleMsgID == "" {
						for _, m := range cb.Messages {
							if m.ID != "" {
								dc.sampleMsgID = m.ID
								break
							}
						}
					}
					unnamedDMs[cb.Chat.ID] = dc
				}
			}
			for _, m := range cb.Messages {
				if m.ID == "" || m.Mine {
					continue
				}
				if strings.TrimSpace(m.SenderName) != "" {
					delete(candidates, m.ID)
					continue
				}
				if m.SenderID == "" {
					continue
				}
				// Skip bot/app senders in group/topic chats (contact/v3 and chat/members omit bots); keep bot
				// senders in 1-on-1 DMs when ChatNameResolver is available because GET /chats/:chat_id can name them.
				if strings.HasPrefix(m.SenderID, "cli_") && (cb.Chat.Kind != "dm" || !hasChatResolver) {
					continue
				}
				if _, exists := candidates[m.ID]; !exists {
					order = append(order, m.ID)
				}
				candidates[m.ID] = candidate{chat: cb.Chat, msg: m}
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	if len(candidates) == 0 && len(unnamedDMs) == 0 {
		return 0, true, nil
	}

	// Count authorized chat-enabled bindings so a single-binding setup can still repair legacy outbox rows whose
	// index entries are absent, while multi-binding setups strictly scope lookups to this binding's own messages.
	fullBindings := 0
	for _, b := range st.bindings {
		if !b.authorized() {
			continue
		}
		t := b.Tier
		if !larksyncwire.ValidTier(t) {
			t = st.tierOf(b.BotID)
		}
		if t == larksyncwire.TierFull {
			fullBindings++
		}
	}
	keySet := map[string]bool{}
	var keys []string
	var ids []string
	for _, id := range order {
		if _, ok := candidates[id]; ok {
			ids = append(ids, id)
			k := larksyncwire.KeyMessage + id
			if !keySet[k] {
				keySet[k] = true
				keys = append(keys, k)
			}
		}
	}
	var dmIDs []string
	for _, cid := range dmOrder {
		if dc, ok := unnamedDMs[cid]; ok {
			dmIDs = append(dmIDs, cid)
			if dc.sampleMsgID != "" {
				k := larksyncwire.KeyMessage + dc.sampleMsgID
				if !keySet[k] {
					keySet[k] = true
					keys = append(keys, k)
				}
			}
		}
	}
	indexMap := map[string]larksyncwire.IndexRecord{}
	for start := 0; start < len(keys); start += 100 {
		end := min(start+100, len(keys))
		got, err := store.LarkSyncIndex(ctx, keys[start:end])
		if err != nil {
			h.logf("lark sync: %s: sender-name backfill index: %v", binding.Key, err)
			return 0, false, err
		}
		for k, v := range got {
			indexMap[k] = v
		}
	}
	isOwned := func(msgID string) bool {
		if msgID != "" {
			if rec, exists := indexMap[larksyncwire.KeyMessage+msgID]; exists {
				return slices.ContainsFunc(rec.Sources, func(s larksyncwire.SourceRef) bool { return s.Binding == binding.Key })
			}
		}
		return fullBindings <= 1
	}

	owned := map[string]candidate{}
	var ownedOrder []string
	for _, id := range ids {
		if isOwned(id) {
			owned[id] = candidates[id]
			ownedOrder = append(ownedOrder, id)
		}
	}
	ownedDMs := map[string]kbwire.SourceChat{}
	var ownedDMOrder []string
	for _, cid := range dmIDs {
		dc := unnamedDMs[cid]
		if isOwned(dc.sampleMsgID) {
			ownedDMs[cid] = dc.chat
			ownedDMOrder = append(ownedDMOrder, cid)
		}
	}
	if len(owned) == 0 && len(ownedDMs) == 0 {
		return 0, true, nil
	}

	uniqueSenders := map[string]bool{}
	var openIDs []string
	chatSenderSets := map[string]map[string]bool{}
	sampleMessages := map[string]string{}
	sampleTimes := map[string]time.Time{}
	sampleChats := map[string]string{}
	for _, id := range ownedOrder {
		c := owned[id]
		sid := c.msg.SenderID
		if !strings.HasPrefix(sid, "cli_") && !uniqueSenders[sid] {
			uniqueSenders[sid] = true
			openIDs = append(openIDs, sid)
		}
		if c.chat.ID != "" {
			if chatSenderSets[c.chat.ID] == nil {
				chatSenderSets[c.chat.ID] = map[string]bool{}
			}
			chatSenderSets[c.chat.ID][sid] = true
		}
		if sampleMessages[sid] == "" {
			sampleMessages[sid] = id
			sampleTimes[sid] = c.msg.At
			sampleChats[sid] = c.chat.ID
		}
	}
	slices.Sort(openIDs)
	chatSenders := make(map[string][]string, len(chatSenderSets))
	for cid, set := range chatSenderSets {
		list := make([]string, 0, len(set))
		for sid := range set {
			list = append(list, sid)
		}
		slices.Sort(list)
		chatSenders[cid] = list
	}
	p2pChatIDs := slices.Clone(ownedDMOrder)
	slices.Sort(p2pChatIDs)

	typedOpenIDs := make([]larksyncwire.OpenID, len(openIDs))
	for i, id := range openIDs {
		typedOpenIDs[i] = larksyncwire.OpenID(id)
	}
	typedP2P := make([]larksyncwire.ChatID, len(p2pChatIDs))
	for i, cid := range p2pChatIDs {
		typedP2P[i] = larksyncwire.ChatID(cid)
	}

	resolveBinding := binding
	resolveBinding.Budget = larkSyncNamesBudget
	req := larksyncwire.NameResolveRequest{
		OpenIDs:        typedOpenIDs,
		ChatSenders:    chatSenders,
		P2PChatIDs:     typedP2P,
		SampleMessages: sampleMessages,
		SampleTimes:    sampleTimes,
		SampleChats:    sampleChats,
		NameBook:       nameBook,
		Progress:       progress,
	}
	runResolve := func() (larksyncwire.NameResolveResult, error) {
		if hasChatResolver {
			return chatResolver.ResolveNamesWithFallback(ctx, resolveBinding, token, req)
		}
		names, rerr := legacyResolver.ResolveUserNames(ctx, resolveBinding, token, openIDs)
		return larksyncwire.NameResolveResult{UserNames: names}, rerr
	}
	res, err := runResolve()
	if larkSyncKindOf(err) == larksyncwire.ErrKindAuth && ctx.Err() == nil {
		if refused := handed(); refused != "" {
			if _, rerr := h.larkSyncAccessToken(ctx, binding.Key, refused); rerr == nil {
				res, err = runResolve()
			}
		}
	}
	if err != nil {
		h.logf("lark sync: %s: sender-name backfill resolve: %v", binding.Key, err)
	} else if res.Partial {
		h.logf("lark sync: %s: sender-name backfill round ran out of its budget; the next round resumes", binding.Key)
	}
	// Complete when every lookup was tried without an error (unresolvable users included) and the round was not cut
	// short by its budget.
	complete := err == nil && !res.Partial
	// What the round learned is kept whatever happened to it: the progress and the name book.
	var cursorUpdates map[string]string
	if res.Progress != progress {
		cursorUpdates = map[string]string{larksyncwire.SenderNamesProgressCursor: res.Progress}
	}
	if res.NameBook != "" && res.NameBook != nameBook {
		if cursorUpdates == nil {
			cursorUpdates = map[string]string{}
		}
		cursorUpdates[larksyncwire.SenderNamesCursor] = res.NameBook
	}
	if len(res.UserNames) == 0 && len(res.P2PChatNames) == 0 {
		if len(cursorUpdates) > 0 {
			if cerr := store.CommitLarkSyncRun(ctx, binding.Key, cursorUpdates, nil, nil); cerr != nil {
				h.logf("lark sync: %s: sender-name backfill progress: %v", binding.Key, cerr)
				return 0, false, cerr
			}
		}
		return 0, complete, err
	}

	byChat := map[string]*connector.ChatBatch{}
	var chatOrder []string
	resolvedDMNames := map[string]string{}
	for _, id := range ownedOrder {
		c := owned[id]
		name := strings.TrimSpace(res.UserNames[c.msg.SenderID])
		if name == "" && c.chat.Kind == "dm" {
			name = strings.TrimSpace(res.P2PChatNames[c.chat.ID])
		}
		if name == "" {
			continue
		}
		cb := byChat[c.chat.ID]
		if cb == nil {
			chatCopy := c.chat
			cb = &connector.ChatBatch{Chat: chatCopy}
			byChat[c.chat.ID] = cb
			chatOrder = append(chatOrder, c.chat.ID)
		}
		msgCopy := c.msg
		msgCopy.SenderName = name
		if cb.Chat.Kind == "dm" && cb.Chat.Name == "" && !msgCopy.Mine {
			peer := strings.TrimSpace(res.P2PChatNames[c.chat.ID])
			if peer == "" {
				peer = name
			}
			cb.Chat.Name = peer
			resolvedDMNames[c.chat.ID] = peer
		}
		cb.Messages = append(cb.Messages, msgCopy)
	}
	for _, cid := range ownedDMOrder {
		peer := strings.TrimSpace(res.P2PChatNames[cid])
		if peer == "" {
			continue
		}
		resolvedDMNames[cid] = peer
		if cb := byChat[cid]; cb != nil {
			if cb.Chat.Name == "" {
				cb.Chat.Name = peer
			}
			continue
		}
		chatCopy := ownedDMs[cid]
		chatCopy.Name = peer
		byChat[cid] = &connector.ChatBatch{Chat: chatCopy}
		chatOrder = append(chatOrder, cid)
	}
	var repairRows []db.LarkSyncOutbox
	for _, cid := range chatOrder {
		cb := byChat[cid]
		slices.SortStableFunc(cb.Messages, func(x, y kbwire.SourceMessage) int { return x.At.Compare(y.At) })
		raw, merr := json.Marshal(cb)
		if merr != nil {
			continue
		}
		repairRows = append(repairRows, db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + cid, Payload: raw})
	}
	if len(resolvedDMNames) > 0 {
		if existingCursors, cerr := store.LarkSyncCursors(ctx, binding.Key); cerr == nil {
			const p2pKey = larksyncwire.ResIM + ":p2p_chats"
			saved := map[string]string{}
			if raw := strings.TrimSpace(existingCursors[p2pKey]); raw != "" {
				_ = json.Unmarshal([]byte(raw), &saved)
			}
			changed := false
			for cid, peer := range resolvedDMNames {
				if peer != "" && saved[cid] == "" {
					saved[cid] = peer
					changed = true
				}
			}
			if changed {
				if raw, merr := json.Marshal(saved); merr == nil {
					if cursorUpdates == nil {
						cursorUpdates = map[string]string{}
					}
					cursorUpdates[p2pKey] = string(raw)
				}
			}
		}
	}
	if len(repairRows) > 0 || len(cursorUpdates) > 0 {
		if cerr := store.CommitLarkSyncRun(ctx, binding.Key, cursorUpdates, nil, repairRows); cerr != nil {
			h.logf("lark sync: %s: sender-name backfill commit: %v", binding.Key, cerr)
			return 0, false, cerr
		}
	}
	return len(repairRows), complete, err
}

// larkSyncNamesRound is what a run's sender-name backfill round left for the binding: whether one ran, and why it
// stopped short ("" when it did not). It never fails the run.
type larkSyncNamesRound struct {
	ran     bool
	warning string
}

// larkSyncNamesBlocking says whether a sender-name lookup error of kind is the owner's to fix (authorize again,
// approve a scope) and so shows in the binding's health. Anything else (a timeout, a rate limit, a passing failure)
// only degrades naming.
func larkSyncNamesBlocking(kind string) bool {
	switch kind {
	case larksyncwire.ErrKindAuth, larksyncwire.ErrKindInvalidGrant, larksyncwire.ErrKindScope:
		return true
	}
	return false
}

// larkSyncPrepareDroppedLinksBackfill performs a one-time, read-only scan of already-synced chat outbox messages for
// binding.Key to recover cloud document and wiki links that were previously dropped when readLinkedDocs hit its run
// budget. Links whose canonical document key already has body content in lark_sync_index (or a synced wiki alias with
// body content in the doc outbox) are skipped; remaining links are merged into PendingLinksCursor as backfill entries
// without duplicating queued tokens or downgrading existing live entries. Nothing is committed here: the caller feeds
// the returned cursor into reader.Run and commits atomically with the run.
func (h *Manager) larkSyncPrepareDroppedLinksBackfill(
	ctx context.Context,
	store larkSyncStore,
	binding larksyncwire.Binding,
	st *larkSyncState,
	reader larksyncwire.Syncer,
	cursors map[string]string,
) (updatedCursor string, done bool) {
	backfiller, ok := reader.(larksyncwire.PendingLinkBackfiller)
	if !ok {
		return "", false
	}
	type msgLinks struct {
		links []larksyncwire.PendingLinkCandidate
	}
	byMsg := map[string]*msgLinks{}
	var msgOrder []string
	var after int64
	for {
		rows, err := store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, after, 100)
		if err != nil {
			h.logf("lark sync: %s: dropped-links backfill scan: %v", binding.Key, err)
			return "", false
		}
		for _, row := range rows {
			after = row.Seq
			var cb connector.ChatBatch
			if err := json.Unmarshal(row.Payload, &cb); err != nil {
				continue
			}
			for _, m := range cb.Messages {
				if m.ID == "" || !strings.Contains(m.Text, "http") {
					continue
				}
				extracted := backfiller.ExtractDocLinks(m.Text)
				if len(extracted) == 0 {
					continue
				}
				for i := range extracted {
					extracted[i].MessageAt = m.At
				}
				entry := byMsg[m.ID]
				if entry == nil {
					entry = &msgLinks{}
					byMsg[m.ID] = entry
					msgOrder = append(msgOrder, m.ID)
				}
				entry.links = append(entry.links, extracted...)
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	if len(byMsg) == 0 {
		return "", true
	}

	fullBindings := 0
	for _, b := range st.bindings {
		if !b.authorized() {
			continue
		}
		t := b.Tier
		if !larksyncwire.ValidTier(t) {
			t = st.tierOf(b.BotID)
		}
		if t == larksyncwire.TierFull {
			fullBindings++
		}
	}

	msgKeys := make([]string, 0, len(msgOrder))
	for _, id := range msgOrder {
		msgKeys = append(msgKeys, larksyncwire.KeyMessage+id)
	}
	msgIndex := map[string]larksyncwire.IndexRecord{}
	for start := 0; start < len(msgKeys); start += 100 {
		end := min(start+100, len(msgKeys))
		got, err := store.LarkSyncIndex(ctx, msgKeys[start:end])
		if err != nil {
			h.logf("lark sync: %s: dropped-links backfill message index: %v", binding.Key, err)
			return "", false
		}
		for k, v := range got {
			msgIndex[k] = v
		}
	}

	byToken := map[string]larksyncwire.PendingLinkCandidate{}
	var tokenOrder []string
	hasWiki := false
	for _, id := range msgOrder {
		rec, exists := msgIndex[larksyncwire.KeyMessage+id]
		mine := false
		if exists {
			mine = slices.ContainsFunc(rec.Sources, func(s larksyncwire.SourceRef) bool { return s.Binding == binding.Key })
		} else if fullBindings <= 1 {
			mine = true
		}
		if !mine {
			continue
		}
		for _, c := range byMsg[id].links {
			if c.Token == "" || c.ObjType == "" {
				continue
			}
			if c.ObjType == larksyncwire.LinkObjWiki {
				hasWiki = true
			}
			if prev, seen := byToken[c.Token]; seen {
				if c.MessageAt.After(prev.MessageAt) {
					prev.MessageAt = c.MessageAt
				}
				if prev.URL == "" || (strings.Contains(prev.URL, "/wiki/") && !strings.Contains(c.URL, "/wiki/") && c.URL != "") {
					prev.URL = c.URL
				}
				byToken[c.Token] = prev
				continue
			}
			byToken[c.Token] = c
			tokenOrder = append(tokenOrder, c.Token)
		}
	}
	if len(tokenOrder) == 0 {
		return "", true
	}

	docKeys := make([]string, 0, len(tokenOrder))
	for _, tok := range tokenOrder {
		docKeys = append(docKeys, larksyncwire.KeyDoc+tok)
	}
	docIndex := map[string]larksyncwire.IndexRecord{}
	for start := 0; start < len(docKeys); start += 100 {
		end := min(start+100, len(docKeys))
		got, err := store.LarkSyncIndex(ctx, docKeys[start:end])
		if err != nil {
			h.logf("lark sync: %s: dropped-links backfill doc index: %v", binding.Key, err)
			return "", false
		}
		for k, v := range got {
			docIndex[k] = v
		}
	}

	aliasHasBody := map[string]bool{}
	if hasWiki {
		var docAfter int64
		for {
			rows, err := store.LarkSyncOutboxAfter(ctx, db.LarkSyncDoc, docAfter, 100)
			if err != nil {
				h.logf("lark sync: %s: dropped-links backfill doc outbox: %v", binding.Key, err)
				return "", false
			}
			for _, row := range rows {
				docAfter = row.Seq
				var it kbwire.Item
				if err := json.Unmarshal(row.Payload, &it); err != nil {
					continue
				}
				if it.BodyHash == "" && strings.TrimSpace(it.Content) == "" {
					continue
				}
				for _, al := range it.Aliases {
					if al != "" {
						aliasHasBody[al] = true
					}
				}
			}
			if len(rows) < 100 {
				break
			}
		}
	}

	var missing []larksyncwire.PendingLinkCandidate
	for _, tok := range tokenOrder {
		if rec, exists := docIndex[larksyncwire.KeyDoc+tok]; exists && rec.BodyHash != "" {
			continue
		}
		if aliasHasBody[tok] {
			continue
		}
		missing = append(missing, byToken[tok])
	}
	if len(missing) == 0 {
		return "", true
	}
	updated, added := backfiller.MergePendingLinks(cursors[larksyncwire.PendingLinksCursor], missing)
	if added == 0 {
		return "", true
	}
	return updated, true
}

// larkSyncMovedCursors are the cursors of res to save: none of a resource that failed (the reader should not have
// moved them; this holds it to that).
func larkSyncMovedCursors(res larksyncwire.Result) map[string]string {
	out := map[string]string{}
	for k, v := range res.Cursors {
		if slices.ContainsFunc(res.Errors, func(e larksyncwire.ResourceError) bool { return e.Resource == k }) {
			continue
		}
		out[k] = v
	}
	return out
}

// larkSyncOutboxRows turn a run's result into the outbox's rows: one chat batch per chat (filed as
// "larksync-<chat id>", whichever binding read it) and one item per document. It also says how many of each.
func larkSyncOutboxRows(b larksyncwire.Binding, res larksyncwire.Result, now time.Time) (rows []db.LarkSyncOutbox, chats, docs int) {
	byChat := map[string]*connector.ChatBatch{}
	var order []string
	for _, m := range res.Messages {
		cb := byChat[m.ChatID]
		if cb == nil {
			kind := "group"
			if m.ChatType == "p2p" {
				kind = "dm"
			}
			cb = &connector.ChatBatch{Chat: kbwire.SourceChat{ID: m.ChatID, Name: m.ChatName, Kind: kind, Topic: m.ChatType == "topic"}}
			byChat[m.ChatID] = cb
			order = append(order, m.ChatID)
		}
		if cb.Chat.Name == "" {
			cb.Chat.Name = m.ChatName
		}
		var media []string
		for _, f := range m.Files {
			media = append(media, f.Ref)
		}
		cb.Messages = append(cb.Messages, kbwire.SourceMessage{ID: m.ID, SenderID: m.SenderID, SenderName: m.SenderName,
			Mine: m.Mine, Type: m.Type, Text: m.Text, At: m.At, Media: media})
	}
	for _, id := range order {
		cb := byChat[id]
		slices.SortStableFunc(cb.Messages, func(x, y kbwire.SourceMessage) int { return x.At.Compare(y.At) })
		raw, err := json.Marshal(cb)
		if err != nil {
			continue
		}
		rows = append(rows, db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + id, Payload: raw})
		chats++
	}
	for _, d := range res.Docs {
		token := d.ObjToken
		if token == "" {
			token = strings.TrimPrefix(strings.TrimPrefix(d.Key, larksyncwire.KeyDoc), larksyncwire.KeyMinute)
		}
		kind := d.Kind
		if kind == "" {
			kind = "docs"
		}
		sources := d.Sources
		if len(sources) == 0 {
			sources = []kbwire.SourceRef{{Channel: larksyncwire.ConnectorID, Binding: b.Key, Account: b.AccountID, SeenAt: now}}
		}
		it := kbwire.Item{Kind: kind, Source: larksyncwire.ConnectorID, ID: kb.SafeID(token), Title: d.Title, URL: d.URL, At: d.At,
			Note: d.Note, Seen: now, CanonicalKey: d.Key, Aliases: d.Aliases, Revision: d.Revision, BodyHash: d.BodyHash, Content: d.Body,
			Sources: sources}
		raw, err := json.Marshal(it)
		if err != nil {
			continue
		}
		rows = append(rows, db.LarkSyncOutbox{Kind: db.LarkSyncDoc, CanonicalKey: d.Key, Payload: raw})
		docs++
	}
	return rows, chats, docs
}

// larkSyncFinish keeps how a run of key went: its health, last error, counts and when it runs next. A failed run
// (an error before the hand-over, or the hand-over itself) counts towards the binding's circuit. IM incremental runs
// (ModeIM) advance NextIMRunAt without disturbing the daily full run's NextRunAt; when a ModeIM run was partial only
// because a large chat hit its budget/cap while still making forward progress, NextIMRunAt stays at 15 minutes
// (larkSyncIMEvery) instead of backing off for an hour. gone: the binding was removed meanwhile.
func (h *Manager) larkSyncFinish(ctx context.Context, key, mode string, res larksyncwire.Result, runErr, commitErr error, madeProgress bool, backfillVer, linksBackfillVer int, authorizedAt time.Time, names larkSyncNamesRound) (gone bool) {
	now := h.bots.sync.clock()
	_, err := h.larkSyncChange(context.WithoutCancel(ctx), func(st *larkSyncState) error {
		b := st.binding(key)
		if b == nil {
			return errLarkSyncNoBinding
		}
		if !b.AuthorizedAt.Equal(authorizedAt) {
			// A grant renewed during this run is already due now. Its health and schedule must not be
			// overwritten by the old grant's missing-scope result or daily backoff.
			return nil
		}
		imOnly := mode == larksyncwire.ModeIM
		if imOnly {
			b.LastIMRunAt = now
		} else {
			b.LastRunAt = now
		}
		if backfillVer > b.BackfillVersion {
			b.BackfillVersion = backfillVer
		}
		if linksBackfillVer > b.LinksBackfillVersion {
			b.LinksBackfillVersion = linksBackfillVer
		}
		if names.ran {
			b.NamesWarning = cutUTF8(names.warning, 300)
			if backfillVer >= larkSyncBackfillVersion {
				b.NamesRounds = 0
			} else {
				b.NamesRounds++
			}
		}
		for _, e := range res.Errors {
			if e.Resource == larksyncwire.SenderNamesCursor && e.Kind == larksyncwire.ErrKindScope {
				// New unnamed messages can arrive after an earlier repair completed. Keep those eligible
				// for repair when the owner restores contact access, not just pre-upgrade messages.
				b.BackfillVersion = 0
				break
			}
		}
		health, detail := larksyncwire.HealthOK, ""
		failed := false
		switch {
		case runErr != nil:
			health, detail, failed = larkSyncHealthOf(larkSyncKindOf(runErr)), larkSyncDetail(runErr), true
		case commitErr != nil:
			health, detail, failed = larksyncwire.HealthFailing, larkSyncDetail(commitErr), true
		case len(res.Errors) > 0:
			worst := ""
			for _, e := range res.Errors {
				if larkSyncSeverity(e.Kind) > larkSyncSeverity(worst) {
					worst = e.Kind
					detail = strings.TrimSpace(e.Resource + " " + e.Kind + " " + e.Detail)
				}
			}
			health = larkSyncHealthOf(worst)
		}
		if health == larksyncwire.HealthNeedsReauth || b.Health == larksyncwire.HealthNeedsReauth && runErr != nil {
			health = larksyncwire.HealthNeedsReauth
		}
		if imOnly && health == larksyncwire.HealthOK && b.Health == larksyncwire.HealthScopeMissing {
			// Keep a full run's missing-scope status (e.g. Drive/Wiki) until the next full run clears it.
		} else {
			b.Health, b.LastError = health, cutUTF8(detail, 300)
		}
		if runErr == nil && commitErr == nil {
			if !imOnly {
				b.LastMessages, b.LastDocs = len(res.Messages), len(res.Docs)
			} else if len(res.Messages) > 0 || len(res.Docs) > 0 {
				b.LastRunAt = now
				b.LastMessages, b.LastDocs = len(res.Messages), len(res.Docs)
			}
		}
		switch {
		case failed:
			b.Failures++
			wait := larkSyncFailSoon
			if b.Failures >= 3 {
				wait = larkSyncBackoff[min(b.Failures-3, len(larkSyncBackoff)-1)]
			}
			if imOnly {
				b.NextIMRunAt = now.Add(wait)
			} else {
				b.NextRunAt = now.Add(wait)
				b.NextIMRunAt = b.NextRunAt
			}
		case health == larksyncwire.HealthRateLimited:
			b.Failures = 0
			if imOnly {
				b.NextIMRunAt = now.Add(larkSyncPartialNext)
			} else {
				b.NextRunAt = now.Add(larkSyncPartialNext)
				b.NextIMRunAt = b.NextRunAt
			}
		case res.Partial:
			b.Failures = 0
			if imOnly {
				if madeProgress && health != larksyncwire.HealthScopeMissing {
					b.NextIMRunAt = now.Add(larkSyncIMEvery)
				} else {
					b.NextIMRunAt = now.Add(larkSyncPartialNext)
				}
			} else {
				b.NextRunAt = now.Add(larkSyncPartialNext)
				b.NextIMRunAt = b.NextRunAt
			}
		default:
			b.Failures = 0
			if imOnly {
				if health == larksyncwire.HealthScopeMissing {
					b.NextIMRunAt = now.Add(larkSyncPartialNext)
				} else {
					b.NextIMRunAt = now.Add(larkSyncIMEvery)
				}
			} else {
				b.NextRunAt = larkSyncNextSlot(now, st.scheduleHour(), key)
				b.NextIMRunAt = now.Add(larkSyncIMEvery)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLarkSyncNoBinding) {
		h.logf("lark sync: %s: keep the run: %v", key, err)
	}
	return errors.Is(err, errLarkSyncNoBinding)
}

// larkSyncHealthOf is the health an error kind leaves a binding in.
func larkSyncHealthOf(kind string) string {
	switch kind {
	case "":
		return larksyncwire.HealthOK
	case larksyncwire.ErrKindAuth, larksyncwire.ErrKindInvalidGrant:
		return larksyncwire.HealthNeedsReauth
	case larksyncwire.ErrKindScope:
		return larksyncwire.HealthScopeMissing
	case larksyncwire.ErrKindRateLimited:
		return larksyncwire.HealthRateLimited
	default:
		return larksyncwire.HealthFailing
	}
}

// larkSyncSeverity orders error kinds by what the owner must do about them: authorize again, then approve a scope,
// then wait.
func larkSyncSeverity(kind string) int {
	switch kind {
	case "":
		return 0
	case larksyncwire.ErrKindAuth, larksyncwire.ErrKindInvalidGrant:
		return 4
	case larksyncwire.ErrKindScope:
		return 3
	case larksyncwire.ErrKindRateLimited:
		return 1
	default:
		return 2
	}
}

// larkSyncNextSlot is a binding's next daily run after now: hour o'clock in now's time zone, at a minute of its own
// (spread from its key, so bindings do not all start at once).
func larkSyncNextSlot(now time.Time, hour int, key string) time.Time {
	f := fnv.New32a()
	_, _ = f.Write([]byte(key))
	minute := int(f.Sum32() % 60)
	slot := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	for !slot.After(now) {
		slot = slot.AddDate(0, 0, 1)
	}
	return slot
}

// larkSyncDue says whether b is due for a full daily run at now.
func larkSyncDue(b larkSyncBinding, now time.Time) bool {
	return b.Enabled && b.authorized() && b.Health != larksyncwire.HealthNeedsReauth && !now.Before(b.NextRunAt)
}

// larkSyncIMDue says whether b is due for a lightweight 15-minute IM incremental run at now. Only chat-enabled
// (TierFull) bindings that have completed at least one full run participate; failure/rate-limit backoff is respected.
func larkSyncIMDue(b larkSyncBinding, fallbackTier string, now time.Time) bool {
	if !b.Enabled || !b.authorized() || b.Health == larksyncwire.HealthNeedsReauth {
		return false
	}
	tier := b.Tier
	if !larksyncwire.ValidTier(tier) {
		tier = fallbackTier
	}
	if tier != larksyncwire.TierFull {
		return false
	}
	if b.LastRunAt.IsZero() {
		return false
	}
	if b.NextIMRunAt.IsZero() {
		if b.Failures > 0 {
			return false
		}
		return !now.Before(b.LastRunAt.Add(larkSyncIMEvery))
	}
	return !now.Before(b.NextIMRunAt)
}

// larkSyncLoop is the scheduler: every larkSyncTickEvery it runs the due bindings, reminds of authorizations that end
// soon and prunes the outbox. A member hub, a build without the lark module and a hub without the database run none.
func (h *Manager) larkSyncLoop(ctx context.Context) {
	if !h.larkSyncAvailable() {
		return
	}
	tick := h.bots.sync.tick
	if tick == nil {
		t := time.NewTicker(larkSyncTickEvery)
		defer t.Stop()
		tick = t.C
	}
	h.larkSyncTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			h.larkSyncTick(ctx)
		}
	}
}

// larkSyncTick is one look of the scheduler; it returns once the runs it started are over.
func (h *Manager) larkSyncTick(ctx context.Context) {
	if h.isMember() || ctx.Err() != nil {
		return
	}
	s := &h.bots.sync
	now := s.clock()
	if store, ok := h.store.(larkSyncStore); ok && (s.lastPrune.IsZero() || now.Sub(s.lastPrune) >= larkSyncPruneEvery) {
		if n, err := store.PruneLarkSyncOutbox(ctx, now.Add(-larkSyncOutboxKeep)); err != nil {
			h.logf("lark sync: prune: %v", err)
		} else {
			s.lastPrune = now
			if n > 0 {
				h.logf("lark sync: pruned %d outbox rows", n)
			}
		}
	}
	h.larkSyncRemind(ctx)
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return
	}
	type dueRun struct {
		key, mode string
	}
	var due []dueRun
	imPoll := st.imPollEnabled()
	for _, b := range st.bindings {
		if larkSyncDue(b, now) {
			due = append(due, dueRun{key: b.Key, mode: larksyncwire.ModeFull})
		} else if imPoll && larkSyncIMDue(b, st.tierOf(b.BotID), now) {
			due = append(due, dueRun{key: b.Key, mode: larksyncwire.ModeIM})
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, larkSyncParallel)
	for _, item := range due {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if err := h.larkSyncRunMode(ctx, item.key, item.mode); err != nil && !errors.Is(err, errLarkSyncRunning) {
				h.logf("lark sync: %s: %v", item.key, err)
			}
		}()
	}
	wg.Wait()
}

// larkSyncDaysLeft is how many days (rounded up) b's authorization has left at now.
func larkSyncDaysLeft(b larkSyncBinding, now time.Time) int {
	return int(math.Ceil(b.authEnds().Sub(now).Hours() / 24))
}

// larkSyncRemind tells the owner, once each at 14, 7 and 1 days before, that a binding's authorization ends; an
// authorization that ended leaves the binding needs_reauth.
func (h *Manager) larkSyncRemind(ctx context.Context) {
	now := h.bots.sync.clock()
	type remind struct {
		key, label string
		days       int
	}
	var tell []remind
	_, err := h.larkSyncChange(ctx, func(st *larkSyncState) error {
		// Authorizations started on the page and never finished are dropped once they ran out (their sealed
		// verifier or device code with them).
		st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool {
			if !b.pending() {
				return false
			}
			if !b.AuthExpiresAt.IsZero() {
				return now.After(b.AuthExpiresAt.Add(larkSyncStaleAuth - larkSyncAuthFor))
			}
			return now.After(b.AuthStartedAt.Add(larkSyncStaleAuth))
		})
		for i := range st.bindings {
			b := &st.bindings[i]
			if !b.authorized() || b.Health == larksyncwire.HealthNeedsReauth {
				continue
			}
			days := larkSyncDaysLeft(*b, now)
			if days <= 0 {
				b.Health, b.LastError = larksyncwire.HealthNeedsReauth, "authorization ended"
				continue
			}
			fresh := false
			for _, th := range larkSyncReminders {
				if days <= th && !slices.Contains(b.RemindedDays, th) {
					b.RemindedDays = append(b.RemindedDays, th)
					fresh = true
				}
			}
			if fresh {
				label := b.Label
				if a := st.account(b.AccountID); label == "" && a != nil {
					label = firstNonEmpty(a.Label, a.Name)
				}
				tell = append(tell, remind{b.Key, label, days})
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	for _, r := range tell {
		args := i18n.Args{"label": r.label, "count": r.days}
		h.pushLocalized(func(l i18n.Lang) map[string]any {
			return map[string]any{
				"aps": map[string]any{"alert": map[string]any{
					"title": i18n.T(l, "hub-larksync.reauth_title", args),
					"body":  i18n.T(l, "hub-larksync.reauth_body", args),
				}, "thread-id": "lark_sync", "interruption-level": "passive"},
				"lark_sync": map[string]any{"binding": r.key, "reauth_due_days": r.days},
			}
		}, "lark_sync.reauth."+r.key)
	}
}
