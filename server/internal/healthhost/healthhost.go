// Package healthhost is the seam between the core and the Health module (internal/modules/health,
// docs/architecture/core-and-modules.md "Health"): what the core asks of the module (Layer) and what the module asks of
// the core (Host), each in terms both sides name (the standard library, the contracts and the leaf libraries). The
// composition root (internal/modules/all) provides the module in the init of its file, under the module's build
// constraint; without it nothing is provided and the core runs with no Health intake: no intake routes, no
// health_intake tool, no health_intake or health_items capability, no water reminders.
//
// The module is provided while the program initializes, before main runs, and only read afterwards.
package healthhost

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// ModuleID is the Health module's id (its module.json): the modules row switches it off by this name.
const ModuleID = "health"

// Tool is the assistant tool the module serves (its description and schema are in prompts/mcp).
const Tool = "health_intake"
const StrengthTool = "health_strength"
const CardPrefix = "health-strength-"

type ToolCall = assistantwire.HealthToolCall
type CardAction = assistantwire.HealthCardAction
type CardResult = assistantwire.HealthCardResult

// Layer is what the core asks of the Health module.
type Layer interface {
	// Items returns the owner's added health items in order, without saving defaults.
	Items(ctx context.Context) ([]string, error)
	CardAction(context.Context, CardAction) (CardResult, error)
	WorkoutReceived(context.Context, assistantwire.StrengthWorkout) error
	WorkoutReport(context.Context, string, string) (assistantwire.StrengthReportPrompt, bool, error)
	// Routes registers the module's HTTP routes through handle, in their order.
	Routes(handle func(pattern string, fn http.HandlerFunc))
	// Tool runs the health_intake tool; the core has checked that the caller is a Health conversation.
	Tool(ctx context.Context, call ToolCall) (string, error)
	// ReminderLoop sends the water reminders the owner switched on until ctx ends.
	ReminderLoop(ctx context.Context)
}

// Settings is the core's settings rows (the settings table).
type Settings = interface {
	Setting(ctx context.Context, key string, out any) (bool, error)
	PutSetting(ctx context.Context, key string, v any) error
}

// IntakeStore keeps the intake entries (the health_intake table).
type IntakeStore = interface {
	HealthIntakeAdd(context.Context, assistantwire.HealthIntakeRow) (assistantwire.HealthIntakeRow, bool, error)
	HealthIntakeUndo(context.Context, string) (*assistantwire.HealthIntakeRow, bool, error)
	HealthIntakeRange(context.Context, time.Time, time.Time) ([]assistantwire.HealthIntakeRow, error)
	HealthIntakeChanges(context.Context, int64, int) ([]assistantwire.HealthIntakeRow, error)
}

// ImportStore commits the legacy water extension's import.
type ImportStore = interface {
	HealthIntakeImport(context.Context, []assistantwire.HealthIntakeRow) (int, error)
	HealthIntakeFinishWaterImport(ctx context.Context, settings, reminder, receipt any) error
}

type StrengthStore = interface {
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

// WaterExtensions exposes only the legacy water extension's migration, never arbitrary extension files.
type WaterExtensions = interface {
	WaterExtension(ctx context.Context, withState bool) (*assistantwire.WaterExtension, error)
	DisableWaterExtension(w http.ResponseWriter, r *http.Request, storageKey string) bool
}

// Host is what the Health module asks of the core: the same methods as the module's own Host.
type Host interface {
	Logf(format string, args ...any)
	// Settings is nil when the manager has no store; IntakeStore and ImportStore are nil when its store keeps no intake.
	Settings() Settings
	IntakeStore() IntakeStore
	StrengthStore() StrengthStore
	CardShown(context.Context, assistantwire.StrengthCard)
	CardChanged(context.Context, assistantwire.StrengthCard)
	PageLink(context.Context, string, string) string
	ImportStore() ImportStore
	// WaterExtensions is nil when the hub offers no water extension migration.
	WaterExtensions() WaterExtensions
	// Event sends an event to the open apps.
	Event(ctx context.Context, ev map[string]any)
	Lang(ctx context.Context) i18n.Lang
	// Timezone is the owner's time zone setting ("" when unset); Location the zone the assistant's tools use.
	Timezone(ctx context.Context) string
	Location(ctx context.Context) *time.Location
	// Standby: this manager stands by for another; QuietPush: the owner's pushes are quiet now.
	Standby(ctx context.Context) bool
	QuietPush(ctx context.Context) bool
	// Notify records n as a card of the water reminders and asks the hub to push it.
	Notify(ctx context.Context, n assistantwire.Notification) (assistantwire.NotifyResult, error)
	// FitPage fits an extension page view for the UI version, as every extension page is.
	FitPage(view *wire.View, version int, l i18n.Lang) error
	UIVersion() int
}

var port = moduleswitch.NewPort[func(Host) Layer]("healthhost")

// Provide provides the module under its id: new makes its layer for one manager's host (init only; twice panics).
func Provide(module string, new func(Host) Layer) { port.Provide(module, new) }

// InBuild says whether the Health module is in this build and s has it on.
func InBuild(s moduleswitch.Set) bool { return port.On(s) }

// New is the module's layer for h; false when the module is not in this build or s switched it off (the `modules`
// setting the manager started with), so it is absent exactly as one built out.
func New(s moduleswitch.Set, h Host) (Layer, bool) {
	newLayer, ok := port.Get(s)
	if !ok {
		return nil, false
	}
	return newLayer(h), true
}
