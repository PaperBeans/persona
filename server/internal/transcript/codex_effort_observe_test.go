package transcript

import "testing"

func TestObserveCodexEffortMetadata(t *testing.T) {
	var m Meta
	m.ObserveNamingMetadata([]byte(`{"timestamp":"2026-11-13T12:01:00Z","type":"turn_context","payload":{"effort":"max"}}`))
	m.ObserveNamingMetadata([]byte(`{"timestamp":"2026-11-13T12:00:00Z","type":"turn_context","payload":{"effort":"low"}}`))
	m.ObserveNamingMetadata([]byte(`{"timestamp":"2026-11-13T12:02:00Z","type":"turn_context","payload":{}}`))
	m.ObserveNamingMetadata([]byte(`{"timestamp":"2026-11-13T12:03:00Z","type":"event_msg","payload":{"type":"token_count"},"note":{"type":"turn_context","effort":"low"}}`))
	if m.Effort != "max" || m.EffortAt.IsZero() {
		t.Fatalf("small metadata observation lost the latest recorded effort: %q at %v", m.Effort, m.EffortAt)
	}
}

// Codex 0.169 writes thread_settings_applied when a turn starts and as soon as a setting changes mid-session (the
// TUI's /model with arguments, #12572): the model and effort the next turn runs with. Both the parser and the
// incremental scan take them up then, not at the next turn_context; a null effort (a model that takes none) leaves the
// last one, as a turn_context without one does, and an older line never wins over a newer one.
func TestCodexThreadSettingsApplied(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-11-21T15:18:18.881Z","type":"turn_context","payload":{"model":"gpt-test","effort":"medium","approval_policy":"on-request"}}`,
		`{"timestamp":"2026-11-21T15:18:18.881Z","ordinal":10,"type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-test","service_tier":"default","approval_policy":"on-request","reasoning_effort":"medium","collaboration_mode":{"mode":"default","settings":{"model":"gpt-test","reasoning_effort":"medium","developer_instructions":"…"}}}}}`,
		`{"timestamp":"2026-11-21T15:18:30.415Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-test","reasoning_effort":"low"}}}`,
		`{"timestamp":"2026-11-21T15:18:42.010Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-other","reasoning_effort":"max"}}}`,
		`{"timestamp":"2026-11-21T15:18:50.000Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-other","reasoning_effort":null}}}`,
	}
	var parsed Meta
	for i, line := range lines {
		items := ParseLine(i, []byte(line), &parsed)
		if i == 3 {
			if len(items) != 1 || items[0].Kind != "model_change" || items[0].Text != "gpt-test" || items[0].Model != "gpt-other" {
				t.Fatalf("line %d items = %+v, want model_change gpt-test -> gpt-other", i, items)
			}
			continue
		}
		if len(items) != 0 {
			t.Fatalf("line %d made items %+v", i, items)
		}
	}
	if parsed.Model != "gpt-other" || parsed.Effort != "max" || parsed.ModelAt.Format("15:04:05") != "15:18:50" || parsed.EffortAt.Format("15:04:05") != "15:18:42" {
		t.Fatalf("parsed %q at %v, effort %q at %v", parsed.Model, parsed.ModelAt, parsed.Effort, parsed.EffortAt)
	}
	if parsed.codexTurnModel != "gpt-test" {
		t.Fatalf("the running turn's model became %q: only a turn_context says what a turn runs with", parsed.codexTurnModel)
	}

	var observed Meta
	for _, line := range lines {
		observed.ObserveNamingMetadata([]byte(line))
	}
	observed.ObserveNamingMetadata([]byte(lines[2])) // an older line read again does not win
	if observed.Model != "gpt-other" || observed.Effort != "max" {
		t.Fatalf("observed %q / %q", observed.Model, observed.Effort)
	}
}
