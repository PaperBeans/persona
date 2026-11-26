package assistant

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type orgItemFixture struct {
	ID    string    `json:"id"`
	Seq   int       `json:"seq"`
	TS    time.Time `json:"ts"`
	Model string    `json:"model"`
}

type orgAgentFixture struct {
	Machine        string           `json:"machine"`
	Session        string           `json:"session"`
	Title          string           `json:"title"`
	Launcher       string           `json:"launcher"`
	RequestedModel string           `json:"requested_model"`
	Effort         string           `json:"effort"`
	Goal           string           `json:"goal"`
	Observations   []orgItemFixture `json:"observations"`
	Subagents      []struct {
		ID           string           `json:"id"`
		Description  string           `json:"description"`
		Model        string           `json:"model"`
		Observations []orgItemFixture `json:"observations"`
	} `json:"subagents"`
}

type orgTaskFixture struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Supervisor  string            `json:"supervisor"`
	DirectionID string            `json:"direction_id"`
	ReviewState string            `json:"review_state"`
	DoneWhen    []string          `json:"done_when"`
	Agents      []orgAgentFixture `json:"agents"`
}

type orgTreeFixture struct {
	Version int `json:"version"`
	Cases   []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Input       struct {
			GeneratedAt  time.Time        `json:"generated_at"`
			TimeZone     string           `json:"time_zone"`
			Main         string           `json:"main"`
			Directions   []string         `json:"directions"`
			Tasks        []orgTaskFixture `json:"tasks"`
			Interactions []struct {
				ID      string `json:"id"`
				Machine string `json:"machine"`
				Session string `json:"session"`
			} `json:"interactions"`
			MachineClaims   map[string]int `json:"machine_claims"`
			DirectionClaims map[string]int `json:"direction_claims"`
			MachineLimit    int            `json:"machine_limit"`
			DirectionLimit  int            `json:"direction_limit"`
		} `json:"input"`
		Expected struct {
			GeneratedAt time.Time `json:"generated_at"`
			Day         string    `json:"day"`
			TimeZone    string    `json:"time_zone"`
			Nodes       []struct {
				ID                          string   `json:"id"`
				ParentID                    *string  `json:"parent_id"`
				Kind                        string   `json:"kind"`
				DisplayName                 string   `json:"display_name"`
				State                       string   `json:"state"`
				Goal                        string   `json:"goal"`
				GoalSource                  string   `json:"goal_source"`
				GoalStatus                  string   `json:"goal_status"`
				DriftStatus                 string   `json:"drift_status"`
				ReviewPending               int      `json:"review_pending"`
				ReviewTaskIDs               []string `json:"review_task_ids"`
				Model                       string   `json:"model"`
				RequestedModel              *string  `json:"requested_model"`
				Effort                      string   `json:"effort"`
				PresetStatus                string   `json:"preset_status"`
				OrganizationDirectionStatus string   `json:"organization_direction_status"`
			} `json:"nodes"`
			OpenTodoIDs           []string       `json:"open_todo_ids"`
			OpenTodoUnknownIDs    []string       `json:"open_todo_unknown_ids"`
			ModelObservationNodes []string       `json:"model_observation_nodes"`
			MachineRunning        map[string]int `json:"machine_running"`
			MachineClaims         map[string]int `json:"machine_claims"`
			MachineLimits         map[string]int `json:"machine_limits"`
			UnknownMachineIDs     []string       `json:"unknown_machine_ids"`
			DirectionRunning      map[string]int `json:"direction_running"`
			DirectionClaims       map[string]int `json:"direction_claims"`
			DirectionLimits       map[string]int `json:"direction_limits"`
			CompleteP0Path        []string       `json:"complete_p0_path"`
			IncompleteP0ID        string         `json:"incomplete_p0_id"`
		} `json:"expected"`
	} `json:"cases"`
}

func readOrgTreeFixture(t *testing.T) orgTreeFixture {
	t.Helper()
	raw, err := os.ReadFile("../../../spec/fixtures/organization-snapshot/tree.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture orgTreeFixture
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Version != 1 || len(fixture.Cases) == 0 {
		t.Fatalf("invalid organization tree fixture: %v", err)
	}
	return fixture
}

func orgFixtureItems(observations []orgItemFixture) []proto.Item {
	items := make([]proto.Item, 0, len(observations))
	for _, observation := range observations {
		items = append(items, proto.Item{ID: observation.ID, Seq: observation.Seq, TS: observation.TS,
			Role: "assistant", Kind: proto.KindText, Model: observation.Model})
	}
	return items
}

func orgSnapshotFromTreeCase(t *testing.T, fixture orgTreeFixture, index int) (*todoEventEnv, orgSnapshot) {
	t.Helper()
	testCase := fixture.Cases[index]
	input := testCase.Input
	e := directionEnv(t, &memStore{})
	setSettings(t, e.s, func(v *Settings) { v.Main, v.Timezone = input.Main, input.TimeZone })
	for _, direction := range input.Directions {
		e.title(direction, "Direction "+strings.TrimPrefix(direction, "conv_lead_"))
	}
	e.leaders(input.Directions...)
	e.syncLeaders()
	lists := map[string][]proto.Session{}
	itemsBySession := map[string][]proto.Item{}
	for _, taskFixture := range input.Tasks {
		taskFixture := taskFixture
		generatedID := e.task(taskFixture.Title, func(todo *Todo) {
			todo.Supervision = &Supervision{Supervisor: taskFixture.Supervisor, DoneWhen: taskFixture.DoneWhen,
				State: taskFixture.ReviewState, UpdatedAt: input.GeneratedAt}
			todo.DirectionID = taskFixture.DirectionID
			todo.CreatedAt, todo.UpdatedAt = input.GeneratedAt, input.GeneratedAt
			if taskFixture.ReviewState != "" {
				todo.Supervision.Reviews = []TaskReview{{At: input.GeneratedAt.Add(-10 * time.Minute), Verdict: reviewRework}}
			}
			for _, agent := range taskFixture.Agents {
				todo.Agents = append(todo.Agents, TodoAgent{MachineID: agent.Machine, SessionID: agent.Session,
					Title: agent.Title, Agent: "claude", LinkedAt: input.GeneratedAt.Add(-time.Hour)})
			}
		})
		if generatedID != taskFixture.ID {
			e.s.todos.mu.Lock()
			todo := e.s.todos.todos[generatedID]
			delete(e.s.todos.todos, generatedID)
			todo.ID = taskFixture.ID
			e.s.todos.todos[taskFixture.ID] = todo
			e.s.todos.mu.Unlock()
		}
		for _, agent := range taskFixture.Agents {
			requested := agent.RequestedModel
			session := proto.Session{MachineID: agent.Machine, SessionID: agent.Session, Title: agent.Title,
				Agent: "claude", Launcher: agent.Launcher, Model: "pending-display-not-evidence", RequestedModel: &requested,
				Effort: agent.Effort, Status: proto.StatusWorking, State: proto.StateWorking,
				UpdatedAt: input.GeneratedAt.Add(-time.Minute)}
			if agent.Goal != "" {
				session.Goal = &proto.GoalInfo{Objective: agent.Goal, Status: proto.GoalActive, UpdatedAt: input.GeneratedAt.Add(-5 * time.Minute)}
			}
			for _, subagent := range agent.Subagents {
				session.Subagents = append(session.Subagents, proto.Subagent{ID: subagent.ID,
					SessionID: agent.Session + "@" + subagent.ID, Description: subagent.Description, Model: subagent.Model,
					Running: true, StartedAt: input.GeneratedAt.Add(-20 * time.Minute), UpdatedAt: input.GeneratedAt.Add(-time.Minute)})
				itemsBySession[agent.Session+"@"+subagent.ID] = orgFixtureItems(subagent.Observations)
			}
			lists[agent.Machine] = append(lists[agent.Machine], session)
			itemsBySession[agent.Session] = orgFixtureItems(agent.Observations)
		}
	}
	for machine, list := range lists {
		e.f.setSessions(machine, list...)
		e.s.fleetKeepPrevious(machine, list)
	}
	for _, interaction := range input.Interactions {
		e.f.hubCards = append(e.f.hubCards, proto.Interaction{ID: interaction.ID, MachineID: interaction.Machine,
			SessionID: interaction.Session, Kind: proto.IxQuestion, CreatedAt: input.GeneratedAt.Add(-time.Minute)})
	}
	e.f.answerNode(DeviceSessionItems, func(_ string, raw json.RawMessage) (any, error) {
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return historyPage{Items: itemsBySession[params.SessionID]}, nil
	})
	e.s.admission.mu.Lock()
	e.s.admission.cached = &AdmissionSettings{MaxClaudeProcs: &input.MachineLimit, DirectionCap: &input.DirectionLimit}
	e.s.admission.at = time.Now()
	for machine, count := range input.MachineClaims {
		if e.s.admission.machineClaims == nil {
			e.s.admission.machineClaims = map[string]int{}
		}
		e.s.admission.machineClaims[machine] = count
	}
	for direction, count := range input.DirectionClaims {
		if e.s.admission.directionClaims == nil {
			e.s.admission.directionClaims = map[string]int{}
		}
		e.s.admission.directionClaims[direction] = count
	}
	e.s.admission.mu.Unlock()
	snapshot, err := e.s.organizationSnapshot(context.Background(), input.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	return e, snapshot
}

func orgNodesByID(snapshot orgSnapshot) map[string]orgNode {
	out := make(map[string]orgNode, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		out[node.ID] = node
	}
	return out
}

func TestOrganizationSnapshotTreeFixture(t *testing.T) {
	fixture := readOrgTreeFixture(t)
	for index, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			_, snapshot := orgSnapshotFromTreeCase(t, fixture, index)
			expected := testCase.Expected
			if !snapshot.GeneratedAt.Equal(expected.GeneratedAt) || snapshot.Day != expected.Day || snapshot.TimeZone != expected.TimeZone {
				t.Fatalf("common snapshot time: %s %s %s", snapshot.GeneratedAt, snapshot.Day, snapshot.TimeZone)
			}
			nodes := orgNodesByID(snapshot)
			if len(nodes) != len(expected.Nodes) {
				t.Fatalf("got %d nodes, want %d: %+v", len(nodes), len(expected.Nodes), snapshot.Nodes)
			}
			for _, want := range expected.Nodes {
				node, ok := nodes[want.ID]
				if !ok {
					t.Errorf("missing node %s", want.ID)
					continue
				}
				if want.ParentID == nil && node.ParentID != nil || want.ParentID != nil && (node.ParentID == nil || *node.ParentID != *want.ParentID) {
					t.Errorf("%s parent %v, want %v", want.ID, node.ParentID, want.ParentID)
				}
				if node.Kind != want.Kind {
					t.Errorf("%s kind %s, want %s", want.ID, node.Kind, want.Kind)
				}
				if want.DisplayName != "" && node.DisplayName != want.DisplayName {
					t.Errorf("%s display %q, want %q", want.ID, node.DisplayName, want.DisplayName)
				}
				if want.State != "" && (node.State == nil || *node.State != want.State) {
					t.Errorf("%s state %v, want %s", want.ID, node.State, want.State)
				}
				if want.Goal != "" && (node.CurrentGoal.Value == nil || *node.CurrentGoal.Value != want.Goal) {
					t.Errorf("%s goal %+v, want %q", want.ID, node.CurrentGoal, want.Goal)
				}
				if want.GoalSource != "" && node.CurrentGoal.Source != want.GoalSource {
					t.Errorf("%s goal source %s, want %s", want.ID, node.CurrentGoal.Source, want.GoalSource)
				}
				if want.GoalStatus != "" && node.CurrentGoal.Status != want.GoalStatus {
					t.Errorf("%s goal status %s, want %s", want.ID, node.CurrentGoal.Status, want.GoalStatus)
				}
				if want.DriftStatus != "" && node.Drift.Status != want.DriftStatus {
					t.Errorf("%s drift status %s, want %s", want.ID, node.Drift.Status, want.DriftStatus)
				}
				if want.Model != "" && (node.Model.Value == nil || *node.Model.Value != want.Model || node.Model.Status != orgAvailable) {
					t.Errorf("%s model %+v, want available %s", want.ID, node.Model, want.Model)
				}
				if want.RequestedModel == nil && node.RequestedModel.Value != nil {
					t.Errorf("%s requested model %v, want unknown", want.ID, *node.RequestedModel.Value)
				} else if want.RequestedModel != nil && (node.RequestedModel.Value == nil || *node.RequestedModel.Value != *want.RequestedModel) {
					t.Errorf("%s requested model %+v, want %s", want.ID, node.RequestedModel, *want.RequestedModel)
				}
				if want.Effort != "" && (node.Effort.Value == nil || *node.Effort.Value != want.Effort) {
					t.Errorf("%s effort %+v, want %s", want.ID, node.Effort, want.Effort)
				}
				if want.PresetStatus != "" && node.Preset.Status != want.PresetStatus {
					t.Errorf("%s preset status %s, want %s", want.ID, node.Preset.Status, want.PresetStatus)
				}
				if want.OrganizationDirectionStatus != "" && node.OrganizationDirection.Status != want.OrganizationDirectionStatus {
					t.Errorf("%s organization direction status %s, want %s", want.ID, node.OrganizationDirection.Status, want.OrganizationDirectionStatus)
				}
				if want.ReviewPending != 0 {
					if node.Review.Value == nil || node.Review.Value.PendingCount != want.ReviewPending || !slices.Equal(node.Review.Value.TaskIDs, want.ReviewTaskIDs) {
						t.Errorf("%s review %+v, want pending %d tasks %v", want.ID, node.Review, want.ReviewPending, want.ReviewTaskIDs)
					}
				}
				if strings.Contains(node.DisplayName, "raw-worker") {
					t.Errorf("%s renders a raw session id as its label", want.ID)
				}
			}
			var openIDs, unknownIDs []string
			for _, todo := range snapshot.OpenTodos {
				openIDs = append(openIDs, todo.ID)
				if todo.Availability.Status == orgUnknown {
					unknownIDs = append(unknownIDs, todo.ID)
				}
			}
			if !slices.Equal(openIDs, expected.OpenTodoIDs) || !slices.Equal(unknownIDs, expected.OpenTodoUnknownIDs) {
				t.Errorf("open to-dos %v unknown %v, want %v %v", openIDs, unknownIDs, expected.OpenTodoIDs, expected.OpenTodoUnknownIDs)
			}
			var observationNodes []string
			for _, observation := range snapshot.ModelsToday.Observations {
				observationNodes = append(observationNodes, observation.NodeID)
				if observation.ObservedAt == nil || observation.Model.Value == nil || observation.ItemID == "" {
					t.Errorf("observation lacks actual transcript identity: %+v", observation)
				}
			}
			if !slices.Equal(observationNodes, expected.ModelObservationNodes) {
				t.Errorf("model observation nodes %v, want %v", observationNodes, expected.ModelObservationNodes)
			}
			if snapshot.ModelsToday.Availability.Status != orgUnknown {
				t.Errorf("daily history coverage must remain explicitly partial: %+v", snapshot.ModelsToday.Availability)
			}
			for _, machine := range snapshot.Machines {
				if slices.Contains(expected.UnknownMachineIDs, machine.MachineID) {
					if machine.Running.Value != nil || machine.Freshness.Status != orgUnknown {
						t.Errorf("machine %s must retain unknown runtime availability: %+v", machine.MachineID, machine)
					}
					continue
				}
				if machine.Running.Value == nil || *machine.Running.Value != expected.MachineRunning[machine.MachineID] ||
					machine.UnlinkedStartClaims.Value == nil || *machine.UnlinkedStartClaims.Value != expected.MachineClaims[machine.MachineID] ||
					machine.ConcurrencyLimit.Value == nil || *machine.ConcurrencyLimit.Value != expected.MachineLimits[machine.MachineID] {
					t.Errorf("machine %s mismatch: %+v", machine.MachineID, machine)
				}
				if machine.Running.ObservedAt == nil || !machine.Running.ObservedAt.Equal(expected.GeneratedAt) {
					t.Errorf("machine %s does not share the snapshot observation time", machine.MachineID)
				}
			}
			for id, node := range nodes {
				if node.Concurrency == nil {
					continue
				}
				if node.Concurrency.Running.Value == nil || *node.Concurrency.Running.Value != expected.DirectionRunning[id] ||
					node.Concurrency.UnlinkedStartClaims.Value == nil || *node.Concurrency.UnlinkedStartClaims.Value != expected.DirectionClaims[id] ||
					node.Concurrency.Limit.Value == nil || *node.Concurrency.Limit.Value != expected.DirectionLimits[id] {
					t.Errorf("direction %s concurrency mismatch: %+v", id, node.Concurrency)
				}
			}
			var complete bool
			for _, item := range snapshot.P0Paths.Items {
				if item.Status == "complete" && slices.Equal(item.Path, expected.CompleteP0Path) {
					complete = true
				}
				if item.ID == expected.IncompleteP0ID && (item.Status != "incomplete" || len(item.Path) != 0) {
					t.Errorf("partial P0 became a red path: %+v", item)
				}
			}
			if !complete {
				t.Errorf("missing complete P0 path %v: %+v", expected.CompleteP0Path, snapshot.P0Paths.Items)
			}
		})
	}
}

type orgStateFixture struct {
	Version int `json:"version"`
	Cases   []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Input       struct {
			SessionState      string `json:"session_state"`
			SessionStatus     string `json:"session_status"`
			BackgroundWaiting bool   `json:"background_waiting"`
			AwaitsReply       bool   `json:"awaits_reply"`
			Planned           bool   `json:"planned"`
			OwnerDecision     bool   `json:"owner_decision"`
			SupervisorWait    bool   `json:"supervisor_wait"`
			MachineStatus     string `json:"machine_status"`
			AcceptLoaded      bool   `json:"accept_loaded"`
		} `json:"input"`
		Expected struct {
			State        *string `json:"state"`
			Activity     string  `json:"activity"`
			Availability string  `json:"availability"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestOrganizationSnapshotStateMappingFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/fixtures/organization-snapshot/state-mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture orgStateFixture
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Version != 1 || len(fixture.Cases) == 0 {
		t.Fatalf("invalid state fixture: %v", err)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			input := testCase.Input
			session := &proto.Session{State: input.SessionState, Status: input.SessionStatus,
				BackgroundWaiting: input.BackgroundWaiting, AwaitsReply: input.AwaitsReply}
			var task *listedTodo
			if input.Planned || input.OwnerDecision || input.SupervisorWait {
				task = &listedTodo{Todo: Todo{ID: "todo_state", Agents: []TodoAgent{}}}
				if input.OwnerDecision {
					task.Ask = &todoAsk{Outcome: "decide", AskedAt: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)}
				}
				if input.SupervisorWait {
					task.Supervision = &Supervision{Waits: &OwnerWait{}}
					task.Agents = []TodoAgent{{MachineID: "machine", SessionID: "session"}}
				}
			}
			fresh := orgObserved(input.MachineStatus, "", "test", nil)
			state, activity, availability := orgRuntimeState(session, nil, task, [2]string{"machine", "session"}, fresh, input.AcceptLoaded)
			if state == nil && testCase.Expected.State != nil || state != nil && testCase.Expected.State == nil ||
				state != nil && testCase.Expected.State != nil && *state != *testCase.Expected.State {
				t.Fatalf("state %v, want %v", state, testCase.Expected.State)
			}
			if activity != testCase.Expected.Activity || availability.Status != testCase.Expected.Availability {
				t.Fatalf("activity %q availability %q, want %q %q", activity, availability.Status, testCase.Expected.Activity, testCase.Expected.Availability)
			}
		})
	}
}
