package larksync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Index record kinds (larksyncwire.IndexRecord.Kind).
const (
	recMessage = "message"
	recDoc     = "doc"
	recMinute  = "minute"
)

// lookupBatch is how many keys one Index.Lookup asks for.
const lookupBatch = 100

// run is one Run's state. Everything read is kept as sightings per resource (a cursor key) and only decided at the
// end (finish), from the resources that did not fail: a resource that fails part way contributes nothing, neither
// content nor index records nor its cursor.
type run struct {
	s            *Syncer
	b            larksyncwire.Binding
	sess         *session
	cursors      map[string]string
	idx          larksyncwire.Index
	start        time.Time // the run's "now": windows' ends and SeenAt
	deadline     time.Time
	stepDeadline time.Time // optional per-step deadline in ModeIM (zero: use deadline)
	owner        string    // the owner's open id (Message.Mine)
	webHost      string    // the tenant's document site, learned from the first document URL read
	partial      bool
	p2pLoaded    bool

	res                map[string]*resourceState
	errs               []larksyncwire.ResourceError
	known              map[string]knownRecord
	msgs               map[string]*msgState
	msgOrder           []string
	docs               map[string]*docState
	docOrder           []string
	chats              map[string]chatInfo
	readChats          map[string]bool         // chat ids whose history readChat already attempted this run
	p2pChats           map[string]string       // known p2p chat_id -> peer display name (persisted in im:p2p_chats)
	chatMetaTried      map[string]bool         // p2p chat ids already queried via GET /open-apis/im/v1/chats/:chat_id this run
	wikiNodes          map[string]wikiNodeInfo // wiki node_token -> resolved object info learned during this run
	docLeads           []docLead
	leads              []minuteLead
	pendingLinks       map[string]*pendingLink // token -> pending linked doc persisted in links_queue
	pendingLinksLoaded bool
	docLeadsEnqueued   bool
	pendingLinksDirty  bool
	pendingLinksAuth   string
	// names are the senders' display names read via contact/v3 this run (open id -> name); namesOff stops contact/v3
	// lookups after a missing contact scope: names are a help for matching the gateway's copy, never a reason to fail.
	// chatMembers caches GET /open-apis/im/v1/chats/:chat_id/members per chat, read on demand (members.go), as a
	// fallback when contact/v3 misses external users; memberNames aggregates learned member names across chats;
	// msgNames caches display names carried in message payloads (sender.name, mentions, post @-nodes).
	// senderTypes records each sender id's sender_type ("user", "app", …) seen in messages this run.
	names          map[string]string
	chatMembers    map[string]*chatMemberList
	book           *nameBook // the binding's name book (members.go); nil until first used
	memberNames    map[string]string
	msgNames       map[string]string
	senderTypes    map[string]string
	namesOff       bool
	chatMembersOff bool
}

// resourceState is how one resource (a cursor key) ended: failed (its content is dropped), abandoned (the budget ran
// out in the middle: dropped too, without an error), or with a cursor to save.
type resourceState struct {
	failed, abandoned bool
	move              bool
	cursor            string
}

// knownRecord is the index's answer for one key, cached for the run (the run never writes the index).
type knownRecord struct {
	rec larksyncwire.IndexRecord
	ok  bool
}

// sighting is one place a key was read in this run.
type sighting struct {
	resource string
	ref      larksyncwire.SourceRef
	revision int64
}

type msgState struct {
	msg       larksyncwire.Message
	sightings []sighting
}

// docState is a document seen in this run: the best content read for it (the highest revision) and every sighting.
type docState struct {
	doc       larksyncwire.Doc
	kind      string // recDoc | recMinute
	fetched   bool   // doc holds the content of doc.Revision
	hasMeta   bool   // a docx's revision was asked for (metaRev; 0 when it could not be read)
	metaRev   int64
	lastErr   error // non-nil when consider recorded a revision-0 unreadable stub for this document
	sightings []sighting
}

// chatInfo is a chat the chat list gave.
type chatInfo struct {
	id, name, kind string
}

// docLead is a cloud document or wiki link found in a synced message.
type docLead struct {
	token, objType, url, resource, entry string
	messageAt                            int64 // unix seconds of the message that contained the link
	backfill                             bool  // true for historical backfill messages (lower priority than live links)
}

// wikiNodeInfo caches a resolved wiki node (node_token -> underlying cloud object).
type wikiNodeInfo struct {
	objToken, objType, title string
	edited                   int64
}

// minuteLead is a minutes token found in this run, in a resource's content.
type minuteLead struct {
	token, url, resource, entry string
}

func newRun(s *Syncer, b larksyncwire.Binding, token func(context.Context) (string, error), cursors map[string]string, idx larksyncwire.Index, deadline time.Time) *run {
	start := s.now()
	return &run{
		s:             s,
		b:             b,
		sess:          &session{s: s, base: botcard.ResolveDomain(b.App.Domain), token: token, deadline: deadline},
		cursors:       cursors,
		idx:           idx,
		start:         start,
		deadline:      deadline,
		owner:         openIDOf(b.AccountID),
		res:           map[string]*resourceState{},
		known:         map[string]knownRecord{},
		msgs:          map[string]*msgState{},
		docs:          map[string]*docState{},
		names:         map[string]string{},
		chatMembers:   map[string]*chatMemberList{},
		memberNames:   map[string]string{},
		msgNames:      map[string]string{},
		senderTypes:   map[string]string{},
		chats:         map[string]chatInfo{},
		readChats:     map[string]bool{},
		p2pChats:      map[string]string{},
		chatMetaTried: map[string]bool{},
		wikiNodes:     map[string]wikiNodeInfo{},
	}
}

func (r *run) overBudget() bool {
	now := r.s.now()
	if !r.stepDeadline.IsZero() && !now.Before(r.stepDeadline) {
		return true
	}
	return !now.Before(r.deadline)
}

// backfillFrom is where a resource without a cursor starts reading.
func (r *run) backfillFrom() time.Time {
	return r.start.Add(-cmpDuration(r.b.Backfill, defaultBackfill))
}

func (r *run) state(resource string) *resourceState {
	st := r.res[resource]
	if st == nil {
		st = &resourceState{}
		r.res[resource] = st
	}
	return st
}

// valid: the resource's sightings count (it did not fail or stop half way).
func (r *run) valid(resource string) bool {
	st := r.res[resource]
	return st == nil || !st.failed && !st.abandoned
}

// fail records that resource could not be read: its content is dropped and its cursor stays.
func (r *run) fail(resource string, err error) {
	st := r.state(resource)
	if st.failed {
		return
	}
	st.failed, st.move = true, false
	kind, detail := larksyncwire.ErrKindOther, err.Error()
	if e := asSyncError(err); e != nil {
		kind = e.Kind
	}
	r.errs = append(r.errs, larksyncwire.ResourceError{Resource: resource, Kind: kind, Detail: detail})
}

// abandon drops what resource read so far without an error: the budget ran out in the middle of it.
func (r *run) abandon(resource string) {
	r.state(resource).abandoned = true
	r.partial = true
}

// moveCursor saves cursor for resource once the run is handed over.
func (r *run) moveCursor(resource, cursor string) {
	st := r.state(resource)
	if st.failed || st.abandoned || cursor == "" {
		return
	}
	st.move, st.cursor = true, cursor
}

// fatal: an error that ends the whole run (a refused token, a cancelled context) rather than one resource.
func fatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	e := asSyncError(err)
	return e != nil && (e.Kind == larksyncwire.ErrKindAuth || e.Kind == larksyncwire.ErrKindInvalidGrant)
}

// settle handles a reader's error for resource: a fatal one goes up, a context that ran out (the budget's safety net)
// abandons the resource, anything else fails only the resource.
func (r *run) settle(ctx context.Context, resource string, err error) error {
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		r.abandon(resource)
		return ctx.Err()
	case fatal(err):
		r.state(resource).abandoned = true // the run stops here: nothing of this resource counts
		return err
	}
	r.fail(resource, err)
	return nil
}

func (r *run) ref(entry string) larksyncwire.SourceRef {
	return larksyncwire.SourceRef{Channel: larksyncwire.ConnectorID, Binding: r.b.Key, Account: r.b.AccountID, Entry: entry, SeenAt: r.start}
}

// addMessage records a sighting of m in resource, through entry.
func (r *run) addMessage(resource string, m larksyncwire.Message, entry string) {
	key := larksyncwire.KeyMessage + m.ID
	st := r.msgs[key]
	if st == nil {
		st = &msgState{msg: m}
		r.msgs[key] = st
		r.msgOrder = append(r.msgOrder, key)
	} else if st.msg.ChatName == "" {
		st.msg.ChatName = m.ChatName
	}
	st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(entry)})
}

// lookup is the index's record for key, asking the index for keys not asked yet (all of keys in batches).
func (r *run) lookup(ctx context.Context, keys ...string) error {
	var ask []string
	for _, k := range keys {
		if _, ok := r.known[k]; !ok {
			ask = append(ask, k)
			r.known[k] = knownRecord{} // asked once, even when it turns out unknown
		}
	}
	if r.idx == nil {
		return nil
	}
	for len(ask) > 0 {
		n := min(len(ask), lookupBatch)
		got, err := r.idx.Lookup(ctx, ask[:n])
		if err != nil {
			for _, k := range ask {
				delete(r.known, k)
			}
			return &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: "index lookup", Msg: err.Error()}
		}
		for _, k := range ask[:n] {
			if rec, ok := got[k]; ok {
				r.known[k] = knownRecord{rec: rec, ok: true}
			}
		}
		ask = ask[n:]
	}
	return nil
}

// finish decides every key read by the resources that did not fail, and builds the result: what is new or newer, the
// cursors to save, the errors, and the index records (staged on the index, see stageRecords).
func (r *run) finish(ctx context.Context) (larksyncwire.Result, error) {
	var res larksyncwire.Result
	var recs []larksyncwire.IndexRecord
	now := r.s.now()

	var keys []string
	for _, k := range r.msgOrder {
		if len(r.validSightings(r.msgs[k].sightings)) > 0 {
			keys = append(keys, k)
		}
	}
	if err := r.lookup(ctx, keys...); err != nil {
		return res, err
	}
	for _, k := range keys {
		st := r.msgs[k]
		refs := refsOf(r.validSightings(st.sightings))
		known := r.known[k]
		switch larksyncwire.Decide(known.rec, known.ok, 0, "") {
		case larksyncwire.DecideNew, larksyncwire.DecideNewer:
			m := st.msg
			m.Sources = refs
			res.Messages = append(res.Messages, m)
			recs = append(recs, larksyncwire.IndexRecord{Key: k, Kind: recMessage, BodyHash: hashOf(m.Text), Sources: larksyncwire.MergeSources(known.rec.Sources, refs), Updated: now})
		default:
			if rec, changed := mergedRecord(known.rec, refs, now); changed {
				recs = append(recs, rec)
			}
		}
	}

	r.savePendingLinks()

	keys = keys[:0]
	for _, k := range r.docOrder {
		if len(r.validSightings(r.docs[k].sightings)) > 0 {
			keys = append(keys, k)
		}
	}
	if err := r.lookup(ctx, keys...); err != nil {
		return res, err
	}
	for _, k := range keys {
		st := r.docs[k]
		valid := r.validSightings(st.sightings)
		refs := refsOf(valid)
		known := r.known[k]
		rev := st.doc.Revision
		for _, sg := range valid {
			rev = max(rev, sg.revision)
		}
		hash := ""
		if st.fetched && st.doc.Revision == rev {
			hash = st.doc.BodyHash
		}
		dec := larksyncwire.Decide(known.rec, known.ok, rev, hash)
		if (dec == larksyncwire.DecideNew || dec == larksyncwire.DecideNewer) && st.fetched && st.doc.Revision == rev {
			d := st.doc
			d.Sources = refs
			res.Docs = append(res.Docs, d)
			recs = append(recs, larksyncwire.IndexRecord{Key: k, Kind: st.kind, Revision: d.Revision, BodyHash: d.BodyHash, Sources: larksyncwire.MergeSources(known.rec.Sources, refs), Updated: now})
			continue
		}
		if !known.ok {
			continue // never stored and nothing to hand over (its content could not be read): next time
		}
		if rec, changed := mergedRecord(known.rec, refs, now); changed {
			recs = append(recs, rec)
		}
	}

	for _, name := range sortedKeys(r.res) {
		if st := r.res[name]; st.move && !st.failed && !st.abandoned {
			if res.Cursors == nil {
				res.Cursors = map[string]string{}
			}
			res.Cursors[name] = st.cursor
		}
	}
	res.Errors = r.errs
	res.Partial = r.partial
	if err := r.stageRecords(ctx, recs); err != nil {
		return larksyncwire.Result{}, err
	}
	return res, nil
}

// stageRecords hands the index records over. The manager's index stages them (Stage) and writes them in the same
// transaction as the cursors and the handed-over content, so a run whose result is not handed over leaves the index
// as it was; an index that cannot stage gets them written at once (Put).
func (r *run) stageRecords(ctx context.Context, recs []larksyncwire.IndexRecord) error {
	if len(recs) == 0 || r.idx == nil {
		return nil
	}
	if st, ok := r.idx.(interface {
		Stage([]larksyncwire.IndexRecord)
	}); ok {
		st.Stage(recs)
		return nil
	}
	if err := r.idx.Put(ctx, recs); err != nil {
		return &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: "index put", Msg: err.Error()}
	}
	return nil
}

func (r *run) validSightings(all []sighting) []sighting {
	var out []sighting
	for _, s := range all {
		if r.valid(s.resource) {
			out = append(out, s)
		}
	}
	return out
}

// refsOf is the sightings' source refs, one per (channel, binding, entry).
func refsOf(ss []sighting) []larksyncwire.SourceRef {
	var refs []larksyncwire.SourceRef
	for _, s := range ss {
		refs = larksyncwire.MergeSources(refs, []larksyncwire.SourceRef{s.ref})
	}
	return refs
}

// mergedRecord is old with refs' new sources; changed is false when there was none (nothing to write).
func mergedRecord(old larksyncwire.IndexRecord, refs []larksyncwire.SourceRef, now time.Time) (larksyncwire.IndexRecord, bool) {
	merged := larksyncwire.MergeSources(old.Sources, refs)
	if len(merged) == len(old.Sources) {
		return old, false
	}
	old.Sources, old.Updated = merged, now
	return old, true
}

// hashOf is the sha256 hex of s.
func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// unixCursor and parseUnix are the cursors' text: decimal seconds or milliseconds.
func unixCursor(v int64) string { return strconv.FormatInt(v, 10) }

func parseUnix(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
