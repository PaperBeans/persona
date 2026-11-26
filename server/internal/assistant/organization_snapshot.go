package assistant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	organizationSnapshotRoute = assistantwire.OrganizationSnapshotPath
	orgAvailable              = "available"
	orgUnknown                = "unknown"
	orgOffline                = "offline"
	orgStale                  = "stale"
	orgNotApplicable          = "not_applicable"
)

// orgAvailability says how much an authoritative source can establish. A value
// can be present and still be stale or offline; it is never presented as live
// in those states.
type orgAvailability struct {
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	Source     string     `json:"source"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// orgField keeps a missing value distinct from a known zero, false, or empty
// string. Value is non-nil when the source returned one, including an empty
// requested model (the launcher default).
type orgField[T any] struct {
	Value      *T         `json:"value"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	Source     string     `json:"source"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

func orgObserved(status, reason, source string, at *time.Time) orgAvailability {
	return orgAvailability{Status: status, Reason: reason, Source: source, ObservedAt: at}
}

func orgValue[T any](value T, source string, at *time.Time) orgField[T] {
	return orgField[T]{Value: &value, Status: orgAvailable, Source: source, ObservedAt: at}
}

func orgNoValue[T any](status, reason, source string) orgField[T] {
	return orgField[T]{Status: status, Reason: reason, Source: source}
}

func orgTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}

func orgString(value string) *string { return &value }

type orgLink struct {
	Path string `json:"path"`
	App  string `json:"app"`
	Web  string `json:"web,omitempty"`
}

type orgReviewSummary struct {
	State                  string     `json:"state"`
	PendingCount           int        `json:"pending_count"`
	AutoReviewPendingCount int        `json:"auto_review_pending_count"`
	LatestReviewAt         *time.Time `json:"latest_review_at,omitempty"`
	TaskIDs                []string   `json:"task_ids"`
}

type orgConcurrency struct {
	Running             orgField[int] `json:"running"`
	UnlinkedStartClaims orgField[int] `json:"unlinked_start_claims"`
	Limit               orgField[int] `json:"limit"`
}

type orgNode struct {
	ID                    string                     `json:"id"`
	ParentID              *string                    `json:"parent_id"`
	ParentAvailability    orgAvailability            `json:"parent_availability"`
	Kind                  string                     `json:"kind"`
	DisplayName           string                     `json:"display_name"`
	State                 *string                    `json:"state"`
	Activity              string                     `json:"activity,omitempty"`
	StateAvailability     orgAvailability            `json:"state_availability"`
	CurrentGoal           orgField[string]           `json:"current_goal"`
	UpdatedAt             *time.Time                 `json:"updated_at"`
	UpdatedAtAvailability orgAvailability            `json:"updated_at_availability"`
	Conversation          orgField[orgLink]          `json:"conversation"`
	Todo                  orgField[orgLink]          `json:"todo"`
	Agent                 orgField[orgLink]          `json:"agent"`
	OrganizationDirection orgField[string]           `json:"organization_direction"`
	Launcher              orgField[string]           `json:"launcher"`
	Model                 orgField[string]           `json:"model"`
	RequestedModel        orgField[string]           `json:"requested_model"`
	Effort                orgField[string]           `json:"effort"`
	Preset                orgField[string]           `json:"preset"`
	Review                orgField[orgReviewSummary] `json:"review"`
	Drift                 orgField[string]           `json:"drift"`
	Concurrency           *orgConcurrency            `json:"concurrency,omitempty"`
}

type orgOpenTodo struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	State                 string            `json:"state"`
	Section               string            `json:"section"`
	DirectionID           string            `json:"direction_id"`
	NodeIDs               []string          `json:"node_ids"`
	Continues             string            `json:"continues,omitempty"`
	SupersededBy          string            `json:"superseded_by,omitempty"`
	ContinuationStatus    string            `json:"continuation_status"`
	ContinuationReason    string            `json:"continuation_reason,omitempty"`
	OrganizationDirection orgField[string]  `json:"organization_direction"`
	RuntimeDirection      orgField[string]  `json:"runtime_direction"`
	Availability          orgAvailability   `json:"availability"`
	Link                  orgField[orgLink] `json:"link"`
}

type orgMachine struct {
	MachineID           string          `json:"machine_id"`
	DisplayName         string          `json:"display_name"`
	State               string          `json:"state"`
	Freshness           orgAvailability `json:"freshness"`
	Running             orgField[int]   `json:"running"`
	UnlinkedStartClaims orgField[int]   `json:"unlinked_start_claims"`
	ConcurrencyLimit    orgField[int]   `json:"concurrency_limit"`
}

type orgModelObservation struct {
	NodeID         string           `json:"node_id"`
	ItemID         string           `json:"item_id,omitempty"`
	Seq            int              `json:"seq,omitempty"`
	ObservedAt     *time.Time       `json:"observed_at"`
	Availability   orgAvailability  `json:"availability"`
	Model          orgField[string] `json:"model"`
	RequestedModel orgField[string] `json:"requested_model"`
	Effort         orgField[string] `json:"effort"`
	Preset         orgField[string] `json:"preset"`
}

type orgModelDay struct {
	Observations []orgModelObservation `json:"observations"`
	Availability orgAvailability       `json:"availability"`
}

type orgP0Item struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	SourceNodeID string            `json:"source_node_id,omitempty"`
	TodoID       string            `json:"todo_id,omitempty"`
	Path         []string          `json:"path"`
	AgentLineage []string          `json:"agent_lineage,omitempty"`
	Status       string            `json:"status"`
	Reason       string            `json:"reason,omitempty"`
	ObservedAt   *time.Time        `json:"observed_at,omitempty"`
	Link         orgField[orgLink] `json:"link"`
}

type orgP0Snapshot struct {
	Items        []orgP0Item     `json:"items"`
	Availability orgAvailability `json:"availability"`
}

type orgSnapshot struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Day         string        `json:"day"`
	TimeZone    string        `json:"time_zone"`
	Nodes       []orgNode     `json:"nodes"`
	OpenTodos   []orgOpenTodo `json:"open_todos"`
	Machines    []orgMachine  `json:"machines"`
	ModelsToday orgModelDay   `json:"models_today"`
	P0Paths     orgP0Snapshot `json:"p0_paths"`
}

type orgSubagent struct {
	parent [2]string
	info   proto.Subagent
}

type orgWork struct {
	key           [2]string
	node          *orgNode
	scope         string
	scopeStatus   orgAvailability
	scopeConflict bool
	candidates    map[[2]string]map[string]bool
	todoIDs       []string
	session       *proto.Session
	subagent      *proto.Subagent
}

type organizationBuild struct {
	s               *Service
	ctx             context.Context
	now             time.Time
	cfg             Settings
	tb              *treeBuild
	names           map[string]string
	titles          map[string]shownTitle
	briefs          briefLinks
	sessions        map[[2]string]proto.Session
	subagents       map[[2]string]orgSubagent
	subagentParents map[[2]string]map[[2]string]bool
	lists           map[string][]proto.Session
	listErrors      map[string]error
	attempted       map[string]bool
	devices         map[string]Device
	deviceListErr   error
	machineFresh    map[string]orgAvailability
	machineClaims   map[string]int
	directionClaims map[string]int
	admission       AdmissionSettings
	acceptLoaded    bool
	interactions    []proto.Interaction
	interactionErr  error
	nodeOrder       []*orgNode
	nodeByID        map[string]*orgNode
	work            map[[2]string]*orgWork
	modelByNode     map[string][]orgModelObservation
	modelRead       map[string]orgAvailability
	presetByTodo    map[string]string
	supersededBy    map[string]string
	web             string
}

func (s *Service) organizationSnapshotRoutes() {
	s.handle("GET "+organizationSnapshotRoute, func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-organization.full_access_required"))
			return
		}
		out, err := s.organizationSnapshot(r.Context(), time.Now())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, out)
	})
}

// organizationSnapshot assembles one read-only view. Inventory loading is the
// smaller read path, not ensureTodosLoaded: the latter runs migrations, token
// synchronization, and report scheduling, none of which belongs to this GET.
func (s *Service) organizationSnapshot(ctx context.Context, now time.Time) (orgSnapshot, error) {
	if _, err := s.loadTodoInventory(ctx); err != nil {
		return orgSnapshot{}, errTodosUnavailable
	}
	now = now.UTC()
	tb, cfg, names := s.directionTreeParts(ctx, now)
	titles, _ := s.conversationTitles()
	b := &organizationBuild{
		s: s, ctx: ctx, now: now, cfg: cfg, tb: tb, names: names, titles: titles,
		briefs: s.briefLinksNow(), sessions: map[[2]string]proto.Session{},
		subagents: map[[2]string]orgSubagent{}, subagentParents: map[[2]string]map[[2]string]bool{},
		lists: map[string][]proto.Session{}, listErrors: map[string]error{}, attempted: map[string]bool{},
		devices: map[string]Device{}, machineFresh: map[string]orgAvailability{},
		machineClaims: map[string]int{}, directionClaims: map[string]int{},
		nodeByID: map[string]*orgNode{}, work: map[[2]string]*orgWork{},
		modelByNode: map[string][]orgModelObservation{}, modelRead: map[string]orgAvailability{},
		presetByTodo: map[string]string{}, supersededBy: map[string]string{}, web: s.webBase(ctx),
	}
	s.accept.mu.Lock()
	b.acceptLoaded = s.accept.loaded
	s.accept.mu.Unlock()
	for key, session := range tb.sessions {
		key[0] = s.canonical(key[0])
		b.sessions[key] = session
	}
	for machine, list := range tb.lists {
		canonical := s.canonical(machine)
		b.lists[canonical] = list
		b.attempted[canonical] = true
	}
	for _, todo := range tb.todos {
		for _, agent := range todo.Agents {
			if !agent.active() {
				continue
			}
			machine := s.canonical(agent.MachineID)
			if b.attempted[machine] {
				continue
			}
			b.attempted[machine] = true
			if !tb.listed[agent.MachineID] {
				b.listErrors[machine] = errors.New("session list unavailable")
			}
		}
	}
	b.indexSubagents()
	b.makeSupervisorNodes()
	b.makeWorkNodes()
	b.readMachineSources()
	b.attachWorkNodes()
	b.breakParentCycles()
	b.readModelSources()
	b.finishWorkNodes()
	b.finishSupervisorNodes()
	out := orgSnapshot{
		GeneratedAt: now,
		Day:         now.In(s.location(ctx)).Format(time.DateOnly),
		TimeZone:    s.location(ctx).String(),
		OpenTodos:   b.openTodos(),
		Machines:    b.machines(),
		ModelsToday: b.modelsToday(),
		P0Paths:     b.p0Paths(),
	}
	out.Nodes = make([]orgNode, 0, len(b.nodeOrder))
	for _, node := range b.nodeOrder {
		out.Nodes = append(out.Nodes, *node)
	}
	return out, nil
}
