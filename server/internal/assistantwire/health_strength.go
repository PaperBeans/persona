package assistantwire

import (
	"encoding/json"
	"errors"
	"time"
)

// StrengthReportPrompt carries assembled report inputs and the workout start used to choose its training chat.
type StrengthReportPrompt struct {
	Prompt string
	Start  time.Time
}

const CapabilityHealthStrength = "health_strength"
const WorkoutActivityStrength = "Strength training"

var (
	ErrHealthStrengthName         = errors.New("strength exercise name already exists")
	ErrHealthStrengthDraftStale   = errors.New("strength draft changed")
	ErrHealthStrengthDraftSettled = errors.New("strength draft already settled")
)

type StrengthExercise struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Aliases    []string   `json:"aliases"`
	Primary    []string   `json:"primary"`
	Secondary  []string   `json:"secondary"`
	Equipment  string     `json:"equipment"`
	WeightRule string     `json:"weight_rule"`
	Setting    string     `json:"setting"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}
type StrengthSet struct {
	ID         string     `json:"id"`
	ExerciseID string     `json:"exercise"`
	Weight     float64    `json:"weight"`
	Reps       int        `json:"reps"`
	Warmup     bool       `json:"warmup"`
	At         time.Time  `json:"at"`
	WorkoutID  *string    `json:"workout_id,omitempty"`
	DraftID    *string    `json:"draft_id,omitempty"`
	Seq        int64      `json:"seq"`
	CreatedAt  time.Time  `json:"created_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}
type StrengthSetFilter struct {
	Exercise, Workout string
	From, Until       time.Time
	Limit             int
}
type StrengthWorkout struct {
	ID         string
	Start, End time.Time
	Report     json.RawMessage
}
type StrengthDraft struct {
	ID, Kind, Status                string
	Revision                        int64
	Body                            json.RawMessage
	PlanDay                         *time.Time
	Conversation, MachineID, CardID string
	CardVersion                     int64
	CreatedAt, UpdatedAt            time.Time
	SettledAt                       *time.Time
}
type StrengthCard struct {
	ID, Conversation, MachineID, FeedID string
	View                                json.RawMessage
	Version                             int64
	At                                  time.Time
	CreatedAt                           time.Time
}

type HealthToolCall struct {
	Name, Machine, Session string
	Args                   json.RawMessage
}
type HealthCardAction struct {
	Conversation, Key, Action, ClientMsgID string
	Version                                int64
	Declared                               bool
}
type HealthCardResult struct {
	Outcome, Action, ClientMsgID string
	At                           time.Time
	Version                      int64
}
