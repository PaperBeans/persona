package projection

import (
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestFoldedSubagentSupervisorHeldAndMixedCards(t *testing.T) {
	t.Run("folded sub-agent read-only permission held by supervisor stays in working persona_working", func(t *testing.T) {
		for _, parentState := range []string{proto.StateNeedsYou, proto.StateIdle, proto.StateWorking} {
			f := Facts{
				ID:        "todo_alpha_held",
				Title:     "Inspect dir-alpha repository",
				CreatedAt: hours(-4),
				UpdatedAt: hours(-1),
				Origin:    "dir-alpha",
				Supervision: &Supervision{
					Supervisor: "dir-alpha",
					UpdatedAt:  hours(-2),
				},
				Agents: []Agent{{MachineID: "m-alpha", SessionID: "s-alpha-parent", Title: "Alpha worker"}},
				Sessions: []proto.Session{{
					MachineID:  "m-alpha",
					SessionID:  "s-alpha-parent",
					State:      parentState,
					StateSince: hours(-1),
					UpdatedAt:  hours(-1),
				}},
				Interactions: []proto.Interaction{{
					ID:                   "ix-readonly-perm",
					MachineID:            "m-worker",
					SessionID:            "s-alpha-sub",
					FoldedUnder:          "s-alpha-parent",
					FoldedUnderMachineID: "m-alpha",
					Kind:                 proto.IxPermission,
					ToolName:             "Read",
					Delegating:           true,
					CreatedAt:            hours(-1),
				}},
			}
			views := Views([]Facts{f}, testNow)
			v := views[0]
			if v.Section != assistantwire.TodoListSectionWorking {
				t.Fatalf("parentState=%s: section=%s reason=%s, want working", parentState, v.Section, v.Reason)
			}
			if parentState != proto.StateWorking && v.Reason != assistantwire.TodoReasonPersonaWorking {
				t.Fatalf("parentState=%s: reason=%s, want persona_working", parentState, v.Reason)
			}
			if c := Counts([]Facts{f}, views); c.NeedsYou != 0 || c.InProgress != 1 {
				t.Fatalf("parentState=%s: counts=%+v, want NeedsYou=0 InProgress=1", parentState, c)
			}
		}
	})

	t.Run("folded side session held permission stays working and escalation surfaces in needs_you", func(t *testing.T) {
		base := Facts{
			ID:          "todo_alpha_side",
			Title:       "Check dir-alpha side session",
			CreatedAt:   hours(-4),
			UpdatedAt:   hours(-1),
			Origin:      "dir-alpha",
			Supervision: &Supervision{Supervisor: "dir-alpha", UpdatedAt: hours(-2)},
			Agents:      []Agent{{MachineID: "m-alpha", SessionID: "s-alpha-side", Title: "Alpha side"}},
			Sessions: []proto.Session{{
				MachineID:  "m-alpha",
				SessionID:  "s-alpha-side",
				State:      proto.StateNeedsYou,
				StateSince: hours(-1),
				UpdatedAt:  hours(-1),
			}},
			Interactions: []proto.Interaction{{
				ID:         "ix-side-perm",
				MachineID:  "m-alpha",
				SessionID:  "s-alpha-side-child",
				FoldedSide: "s-alpha-side",
				Kind:       proto.IxPermission,
				ToolName:   "Read",
				Delegating: true,
				CreatedAt:  hours(-1),
			}},
		}
		views := Views([]Facts{base}, testNow)
		if views[0].Section != assistantwire.TodoListSectionWorking || views[0].Reason != assistantwire.TodoReasonPersonaWorking {
			t.Fatalf("held side card view=%+v, want working/persona_working", views[0])
		}
		if c := Counts([]Facts{base}, views); c.NeedsYou != 0 {
			t.Fatalf("held side card counts=%+v, want NeedsYou=0", c)
		}

		base.Interactions[0].Delegating = false
		viewsEsc := Views([]Facts{base}, testNow)
		if viewsEsc[0].Section != assistantwire.TodoListSectionNeedsYou || len(viewsEsc[0].Asks) != 1 || viewsEsc[0].Asks[0].Interaction != "ix-side-perm" {
			t.Fatalf("escalated side card view=%+v, want needs_you with ix-side-perm", viewsEsc[0])
		}
		if c := Counts([]Facts{base}, viewsEsc); c.NeedsYou != 1 {
			t.Fatalf("escalated side card counts=%+v, want NeedsYou=1", c)
		}
	})

	t.Run("escalated folded sub-agent card surfaces in needs_you even when parent is working", func(t *testing.T) {
		f := Facts{
			ID:          "todo_alpha_esc",
			Title:       "Escalate dir-alpha change",
			CreatedAt:   hours(-4),
			UpdatedAt:   hours(-1),
			Origin:      "dir-alpha",
			Supervision: &Supervision{Supervisor: "dir-alpha", UpdatedAt: hours(-2)},
			Agents:      []Agent{{MachineID: "m-alpha", SessionID: "s-alpha-parent", Title: "Alpha worker"}},
			Sessions: []proto.Session{{
				MachineID:  "m-alpha",
				SessionID:  "s-alpha-parent",
				State:      proto.StateWorking,
				StateSince: hours(-1),
				UpdatedAt:  hours(-1),
			}},
			Interactions: []proto.Interaction{{
				ID:                   "ix-cross-esc",
				MachineID:            "m-worker",
				SessionID:            "s-alpha-sub",
				FoldedUnder:          "s-alpha-parent",
				FoldedUnderMachineID: "m-alpha",
				Kind:                 proto.IxPermission,
				ToolName:             "Bash",
				Delegating:           false,
				CreatedAt:            hours(-1),
			}},
		}
		views := Views([]Facts{f}, testNow)
		v := views[0]
		if v.Section != assistantwire.TodoListSectionNeedsYou || v.Reason != assistantwire.TodoReasonAgentAsks {
			t.Fatalf("view=%+v, want needs_you/agent_asks", v)
		}
		if len(v.Asks) != 1 || v.Asks[0].Interaction != "ix-cross-esc" {
			t.Fatalf("asks=%+v, want ix-cross-esc", v.Asks)
		}
		if c := Counts([]Facts{f}, views); c.NeedsYou != 1 {
			t.Fatalf("counts=%+v, want NeedsYou=1", c)
		}
	})

	t.Run("older supervisor-held card never masks a newer escalated user card on the same run", func(t *testing.T) {
		olderHeld := proto.Interaction{
			ID:         "ix-older-held",
			MachineID:  "m-beta",
			SessionID:  "s-beta-run",
			Kind:       proto.IxPermission,
			ToolName:   "Read",
			Delegating: true,
			CreatedAt:  hours(-3),
		}
		newerEscalated := proto.Interaction{
			ID:         "ix-escalated",
			MachineID:  "m-beta",
			SessionID:  "s-beta-run",
			Kind:       proto.IxPermission,
			ToolName:   "Bash",
			Delegating: false,
			CreatedAt:  hours(-1),
		}
		for _, cards := range [][]proto.Interaction{
			{olderHeld, newerEscalated},
			{newerEscalated, olderHeld},
		} {
			f := Facts{
				ID:          "todo_beta_mixed",
				Title:       "Verify dir-beta rollout",
				CreatedAt:   hours(-4),
				UpdatedAt:   hours(-1),
				Origin:      "dir-beta",
				Supervision: &Supervision{Supervisor: "dir-beta", UpdatedAt: hours(-2)},
				Agents:      []Agent{{MachineID: "m-beta", SessionID: "s-beta-run", Title: "Beta worker"}},
				Sessions: []proto.Session{{
					MachineID:           "m-beta",
					SessionID:           "s-beta-run",
					State:               proto.StateNeedsYou,
					PendingInteractions: 2,
					StateSince:          hours(-1),
					UpdatedAt:           hours(-1),
				}},
				Interactions: cards,
			}
			views := Views([]Facts{f}, testNow)
			v := views[0]
			if v.Section != assistantwire.TodoListSectionNeedsYou || v.Reason != assistantwire.TodoReasonAgentAsks {
				t.Fatalf("mixed cards view=%+v, want needs_you/agent_asks", v)
			}
			if len(v.Asks) != 1 || v.Asks[0].Interaction != "ix-escalated" {
				t.Fatalf("mixed cards asks=%+v, want ix-escalated", v.Asks)
			}
			if c := Counts([]Facts{f}, views); c.NeedsYou != 1 {
				t.Fatalf("mixed cards counts=%+v, want NeedsYou=1", c)
			}
		}
	})
}
