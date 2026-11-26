package health

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// The core's types the module names, as the seam (internal/healthhost) declares them.
type (
	settingsStore = interface {
		Setting(ctx context.Context, key string, out any) (bool, error)
		PutSetting(ctx context.Context, key string, v any) error
	}
	healthIntakeStore = interface {
		HealthIntakeAdd(context.Context, assistantwire.HealthIntakeRow) (assistantwire.HealthIntakeRow, bool, error)
		HealthIntakeUndo(context.Context, string) (*assistantwire.HealthIntakeRow, bool, error)
		HealthIntakeRange(context.Context, time.Time, time.Time) ([]assistantwire.HealthIntakeRow, error)
		HealthIntakeChanges(context.Context, int64, int) ([]assistantwire.HealthIntakeRow, error)
	}
	healthWaterImportStore = interface {
		HealthIntakeImport(context.Context, []assistantwire.HealthIntakeRow) (int, error)
		HealthIntakeFinishWaterImport(ctx context.Context, settings, reminder, receipt any) error
	}
	waterExtensions = interface {
		WaterExtension(ctx context.Context, withState bool) (*assistantwire.WaterExtension, error)
		DisableWaterExtension(w http.ResponseWriter, r *http.Request, storageKey string) bool
	}
)

// Host is what the module asks of the core (internal/healthhost.Host, the same methods).
type Host interface {
	Logf(format string, args ...any)
	Settings() settingsStore
	IntakeStore() healthIntakeStore
	StrengthStore() strengthStore
	CardShown(context.Context, assistantwire.StrengthCard)
	CardChanged(context.Context, assistantwire.StrengthCard)
	PageLink(context.Context, string, string) string
	ImportStore() healthWaterImportStore
	WaterExtensions() waterExtensions
	Event(ctx context.Context, ev map[string]any)
	Lang(ctx context.Context) i18n.Lang
	Timezone(ctx context.Context) string
	Location(ctx context.Context) *time.Location
	Standby(ctx context.Context) bool
	QuietPush(ctx context.Context) bool
	Notify(ctx context.Context, n assistantwire.Notification) (assistantwire.NotifyResult, error)
	FitPage(view *wire.View, version int, l i18n.Lang) error
	UIVersion() int
}

// Service is the module's layer on one manager (internal/healthhost.Layer).
type Service struct {
	h Host
	// intakeMu serializes health items, intake settings, reminder state and local intake mutations.
	intakeMu   sync.Mutex
	strengthMu sync.Mutex
}

// New makes the module's layer on h.
func New(h Host) *Service { return &Service{h: h} }

// Routes registers the module's HTTP routes through handle.
func (s *Service) Routes(handle func(pattern string, fn http.HandlerFunc)) {
	s.intakeRoutes(handle)
	handle("GET /api/v1/assistant/health/items", s.getHealthItems)
	handle("PUT /api/v1/assistant/health/items", s.putHealthItems)
}

// Tool runs the health_intake tool; the core has checked the caller's space.
func (s *Service) Tool(ctx context.Context, call assistantwire.HealthToolCall) (string, error) {
	switch call.Name {
	case assistantwire.CapabilityHealthIntake:
		return s.toolHealthIntake(ctx, call.Args)
	case assistantwire.CapabilityHealthStrength:
		return s.toolHealthStrength(ctx, call)
	}
	return "", errors.New("unknown Health tool")
}

// ReminderLoop sends the water reminders until ctx ends.
func (s *Service) ReminderLoop(ctx context.Context) { s.waterReminderLoop(ctx) }

func (s *Service) logf(format string, args ...any) { s.h.Logf(format, args...) }

func (s *Service) store() settingsStore { return s.h.Settings() }

func (s *Service) event(ctx context.Context, ev map[string]any) { s.h.Event(ctx, ev) }

func (s *Service) lang(ctx context.Context) i18n.Lang { return s.h.Lang(ctx) }

// fullRole: r comes from a full device (the hub's role header, assistantwire.RoleHeader).
func fullRole(r *http.Request) bool {
	role := r.Header.Get(assistantwire.RoleHeader)
	return role == "" || role == "full"
}

// intakeUUIDPattern is an entry's id.
var intakeUUIDPattern = lazyre.New(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type strengthStore = interface {
	HealthStrengthExercises(context.Context, bool) ([]assistantwire.StrengthExercise, error)
	HealthStrengthExerciseSave(context.Context, assistantwire.StrengthExercise) (assistantwire.StrengthExercise, bool, error)
	HealthStrengthExerciseDelete(context.Context, string) (bool, error)
	HealthStrengthSetAdd(context.Context, []assistantwire.StrengthSet) ([]assistantwire.StrengthSet, error)
	HealthStrengthSetUndo(context.Context, string) (*assistantwire.StrengthSet, bool, error)
	HealthStrengthSets(context.Context, assistantwire.StrengthSetFilter) ([]assistantwire.StrengthSet, error)
	HealthStrengthWorkoutAdd(context.Context, assistantwire.StrengthWorkout) (int, error)
	HealthStrengthWorkout(context.Context, string) (*assistantwire.StrengthWorkout, error)
	HealthStrengthWorkoutLatest(context.Context, time.Time, time.Time) (*assistantwire.StrengthWorkout, error)
	HealthStrengthDraftCreate(context.Context, assistantwire.StrengthDraft, assistantwire.StrengthCard) error
	HealthStrengthDraft(context.Context, string) (*assistantwire.StrengthDraft, error)
	HealthStrengthDraftByCard(context.Context, string) (*assistantwire.StrengthDraft, error)
	HealthStrengthDrafts(context.Context, string) ([]assistantwire.StrengthDraft, error)
	HealthStrengthDraftEdit(context.Context, string, int64, json.RawMessage, json.RawMessage, time.Time) (assistantwire.StrengthDraft, assistantwire.StrengthCard, error)
	HealthStrengthDraftConfirm(context.Context, string, int64, []assistantwire.StrengthSet, json.RawMessage, json.RawMessage, time.Time) (assistantwire.StrengthDraft, assistantwire.StrengthCard, []assistantwire.StrengthSet, error)
	HealthStrengthDraftDiscard(context.Context, string, int64, json.RawMessage, json.RawMessage, time.Time) (assistantwire.StrengthDraft, assistantwire.StrengthCard, error)
	HealthStrengthDraftMove(context.Context, string, int64, json.RawMessage, assistantwire.StrengthCard) (assistantwire.StrengthDraft, error)
}
