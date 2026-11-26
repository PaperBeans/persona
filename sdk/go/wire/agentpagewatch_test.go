package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type watchFixture struct {
	Draws []struct {
		Name     string            `json:"name"`
		Watch    json.RawMessage   `json:"watch"`
		Readings []json.RawMessage `json:"readings"`
		Tripped  bool              `json:"tripped"`
	} `json:"draws"`
	Trips []struct {
		Rule    PageWatchAlert   `json:"rule"`
		Reading PageWatchReading `json:"reading"`
		Trips   bool             `json:"trips"`
	} `json:"trips"`
}

func loadWatchFixture(t *testing.T) watchFixture {
	t.Helper()
	path := filepath.Join("..", "..", "..", "spec", "fixtures", "dashboards", "watch.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f watchFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

// Every "trips" case in spec/fixtures/dashboards/watch.json must match Trips(rule, reading).
// Also cross-checks every "draws" case's watch, readings, and tripped flag.
func TestWatchFixtureTripsAndDraws(t *testing.T) {
	f := loadWatchFixture(t)
	if len(f.Trips) == 0 {
		t.Fatal("fixture has no trips cases")
	}
	for i, c := range f.Trips {
		got := Trips(&c.Rule, &c.Reading)
		if got != c.Trips {
			t.Errorf("trips[%d] (rule=%+v reading=%+v): got %v, want %v", i, c.Rule, c.Reading, got, c.Trips)
		}
		// Rule and reading JSON round-trip must preserve Trips behavior.
		ruleJSON, err := json.Marshal(c.Rule)
		if err != nil {
			t.Fatalf("trips[%d] marshal rule: %v", i, err)
		}
		var ruleAgain PageWatchAlert
		if err := json.Unmarshal(ruleJSON, &ruleAgain); err != nil {
			t.Fatalf("trips[%d] unmarshal rule %s: %v", i, ruleJSON, err)
		}
		readJSON, err := json.Marshal(c.Reading)
		if err != nil {
			t.Fatalf("trips[%d] marshal reading: %v", i, err)
		}
		var readAgain PageWatchReading
		if err := json.Unmarshal(readJSON, &readAgain); err != nil {
			t.Fatalf("trips[%d] unmarshal reading %s: %v", i, readJSON, err)
		}
		if Trips(&ruleAgain, &readAgain) != c.Trips {
			t.Errorf("trips[%d] after round-trip: rule=%s reading=%s", i, ruleJSON, readJSON)
		}
	}

	if len(f.Draws) == 0 {
		t.Fatal("fixture has no draws cases")
	}
	for _, d := range f.Draws {
		w, err := ParsePageWatch(d.Watch)
		if err != nil {
			t.Errorf("draws.%s ParsePageWatch: %v", d.Name, err)
			continue
		}
		var last *PageWatchReading
		for j, raw := range d.Readings {
			// Fixture readings carry "at" (hub-stamped in storage); strip it before ParsePageReading.
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("draws.%s reading[%d]: %v", d.Name, j, err)
			}
			delete(m, "at")
			stripped, _ := json.Marshal(m)
			r, err := ParsePageReading(w, stripped)
			if err != nil {
				t.Errorf("draws.%s reading[%d] ParsePageReading: %v", d.Name, j, err)
				continue
			}
			last = r
		}
		if got := Trips(w.Alert, last); got != d.Tripped {
			t.Errorf("draws.%s Trips = %v, want %v", d.Name, got, d.Tripped)
		}
	}
}

func TestParsePageWatchValidAndRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"number_minimal", `{"question":"Rent","display":"number"}`},
		{"number_with_unit_and_alert", `{"question":"  Rent on Elm St  ","display":"number","unit":" USD ","alert":{"when":"below","value":2000}}`},
		{"number_max_lengths", `{"question":"` + strings.Repeat("q", 200) + `","display":"number","unit":"` + strings.Repeat("u", 8) + `","alert":{"when":"above","value":1e12}}`},
		{"number_neg_bound", `{"question":"Temp","display":"number","alert":{"when":"below","value":-1e12}}`},
		{"trend_with_alert", `{"question":"Weight","display":"trend","unit":"kg","alert":{"when":"above","value":80.5}}`},
		{"status_no_alert", `{"question":"Sites up?","display":"status"}`},
		{"status_warning_alert", `{"question":"Sites up?","display":"status","alert":{"when":"status","value":"warning"}}`},
		{"status_problem_alert", `{"question":"Sites up?","display":"status","alert":{"when":"status","value":"problem"}}`},
		{"table_1_col", `{"question":"Listings","display":"table","columns":["Title"]}`},
		{"table_4_cols_alert", `{"question":"Listings","display":"table","columns":["A","B","C","` + strings.Repeat("c", 40) + `"],"alert":{"when":"rows","value":100}}`},
		{"null_optional_fields", `{"question":"Rent","display":"number","unit":null,"columns":null,"alert":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := ParsePageWatch(json.RawMessage(c.raw))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			marshaled, err := json.Marshal(w)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			again, err := ParsePageWatch(marshaled)
			if err != nil {
				t.Fatalf("re-parse %s: %v", marshaled, err)
			}
			if again.Question != w.Question || again.Display != w.Display || again.Unit != w.Unit {
				t.Fatalf("round-trip mismatch: %+v vs %+v", again, w)
			}
		})
	}
}

func TestParsePageWatchRefuses(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ``},
		{"null", `null`},
		{"array", `[]`},
		{"trailing", `{"question":"Q","display":"number"}{}`},
		{"unknown_field", `{"question":"Q","display":"number","extra":1}`},
		{"missing_question", `{"display":"number"}`},
		{"empty_question", `{"question":"   ","display":"number"}`},
		{"question_201", `{"question":"` + strings.Repeat("x", 201) + `","display":"number"}`},
		{"question_newline", `{"question":"line1\nline2","display":"number"}`},
		{"question_bidi", `{"question":"Rent\u202e","display":"number"}`},
		{"missing_display", `{"question":"Q"}`},
		{"bad_display", `{"question":"Q","display":"chart"}`},
		{"unit_too_long", `{"question":"Q","display":"number","unit":"123456789"}`},
		{"unit_newline", `{"question":"Q","display":"number","unit":"U\nS"}`},
		{"unit_on_status", `{"question":"Q","display":"status","unit":"%"}`},
		{"unit_on_table", `{"question":"Q","display":"table","columns":["A"],"unit":"$"}`},
		{"columns_on_number", `{"question":"Q","display":"number","columns":["A"]}`},
		{"columns_on_status", `{"question":"Q","display":"status","columns":["A"]}`},
		{"columns_on_trend", `{"question":"Q","display":"trend","columns":["A"]}`},
		{"table_no_columns", `{"question":"Q","display":"table"}`},
		{"table_empty_columns", `{"question":"Q","display":"table","columns":[]}`},
		{"table_5_columns", `{"question":"Q","display":"table","columns":["1","2","3","4","5"]}`},
		{"table_empty_col_name", `{"question":"Q","display":"table","columns":["  "]}`},
		{"table_col_41_chars", `{"question":"Q","display":"table","columns":["` + strings.Repeat("c", 41) + `"]}`},
		{"table_col_null", `{"question":"Q","display":"table","columns":[null]}`},
		{"alert_unknown_field", `{"question":"Q","display":"number","alert":{"when":"above","value":1,"extra":true}}`},
		{"alert_missing_value", `{"question":"Q","display":"number","alert":{"when":"above"}}`},
		{"alert_wrong_for_number_status", `{"question":"Q","display":"number","alert":{"when":"status","value":"problem"}}`},
		{"alert_wrong_for_number_rows", `{"question":"Q","display":"number","alert":{"when":"rows","value":1}}`},
		{"alert_wrong_for_trend_rows", `{"question":"Q","display":"trend","alert":{"when":"rows","value":1}}`},
		{"alert_wrong_for_status_above", `{"question":"Q","display":"status","alert":{"when":"above","value":1}}`},
		{"alert_wrong_for_table_below", `{"question":"Q","display":"table","columns":["A"],"alert":{"when":"below","value":1}}`},
		{"alert_status_ok", `{"question":"Q","display":"status","alert":{"when":"status","value":"ok"}}`},
		{"alert_number_string", `{"question":"Q","display":"number","alert":{"when":"above","value":"10"}}`},
		{"alert_number_too_big", `{"question":"Q","display":"number","alert":{"when":"above","value":1000000000001}}`},
		{"alert_number_too_small", `{"question":"Q","display":"number","alert":{"when":"below","value":-1000000000001}}`},
		{"alert_rows_0", `{"question":"Q","display":"table","columns":["A"],"alert":{"when":"rows","value":0}}`},
		{"alert_rows_101", `{"question":"Q","display":"table","columns":["A"],"alert":{"when":"rows","value":101}}`},
		{"alert_rows_float", `{"question":"Q","display":"table","columns":["A"],"alert":{"when":"rows","value":2.5}}`},
		{"alert_rows_string", `{"question":"Q","display":"table","columns":["A"],"alert":{"when":"rows","value":"3"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := ParsePageWatch(json.RawMessage(c.raw)); err == nil {
				t.Fatalf("expected error, got %+v", got)
			}
		})
	}
}

func TestParsePageReadingValidation(t *testing.T) {
	wNum := &PageWatch{Question: "Rent", Display: PageWatchNumber, Unit: "USD"}
	wTrend := &PageWatch{Question: "Weight", Display: PageWatchTrend, Unit: "kg"}
	wStatus := &PageWatch{Question: "Up?", Display: PageWatchStatus}
	wTable := &PageWatch{Question: "Listings", Display: PageWatchTable, Columns: []string{"Name", "Price"}}

	// Valid readings across all displays and boundaries.
	valid := []struct {
		name  string
		watch *PageWatch
		raw   string
		want  string // expected marshaled JSON (when non-empty)
	}{
		{"number_zero", wNum, `{"value":0}`, `{"value":0}`},
		{"number_neg_zero", wNum, `{"value":-0}`, `{"value":0}`},
		{"number_max", wNum, `{"value":1e12}`, `{"value":1000000000000}`},
		{"number_min", wNum, `{"value":-1e12}`, `{"value":-1000000000000}`},
		{"trend_val", wTrend, `{"value":78.25}`, `{"value":78.25}`},
		{"status_ok_no_text", wStatus, `{"status":"ok"}`, `{"status":"ok"}`},
		{"status_warning_text_80", wStatus, `{"status":"warning","text":"` + strings.Repeat("w", 80) + `"}`, ``},
		{"status_problem_empty_text", wStatus, `{"status":"problem","text":""}`, `{"status":"problem"}`},
		{"table_0_rows", wTable, `{"rows":[]}`, `{"rows":[]}`},
		{"table_20_rows_80_cell", wTable, func() string {
			rows := make([][]string, 20)
			for i := range rows {
				rows[i] = []string{"", strings.Repeat("c", 80)}
			}
			b, _ := json.Marshal(map[string]any{"rows": rows})
			return string(b)
		}(), ``},
	}
	for _, c := range valid {
		t.Run("ok_"+c.name, func(t *testing.T) {
			r, err := ParsePageReading(c.watch, json.RawMessage(c.raw))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if c.want != "" && string(out) != c.want {
				t.Fatalf("marshaled %s, want %s", out, c.want)
			}
		})
	}

	// Refused readings.
	rows21 := make([][]string, 21)
	for i := range rows21 {
		rows21[i] = []string{"a", "b"}
	}
	raw21, _ := json.Marshal(map[string]any{"rows": rows21})

	invalid := []struct {
		name  string
		watch *PageWatch
		raw   string
	}{
		{"nil_watch", nil, `{"value":1}`},
		{"empty_raw", wNum, ``},
		{"null_raw", wNum, `null`},
		{"array_raw", wNum, `[]`},
		{"number_missing_value", wNum, `{}`},
		{"number_null_value", wNum, `{"value":null}`},
		{"number_string_value", wNum, `{"value":"10"}`},
		{"number_above_max", wNum, `{"value":1000000000001}`},
		{"number_below_min", wNum, `{"value":-1000000000001}`},
		{"number_extra_status", wNum, `{"value":1,"status":"ok"}`},
		{"number_extra_field", wNum, `{"value":1,"at":"2026-11-25T08:00:00Z"}`},
		{"trend_above_max", wTrend, `{"value":1e13}`},
		{"status_missing", wStatus, `{}`},
		{"status_bad_value", wStatus, `{"status":"critical"}`},
		{"status_null_text", wStatus, `{"status":"ok","text":null}`},
		{"status_text_81", wStatus, `{"status":"ok","text":"` + strings.Repeat("a", 81) + `"}`},
		{"status_text_newline", wStatus, `{"status":"ok","text":"a\nb"}`},
		{"status_extra_value", wStatus, `{"status":"ok","value":1}`},
		{"table_missing_rows", wTable, `{}`},
		{"table_null_rows", wTable, `{"rows":null}`},
		{"table_21_rows", wTable, string(raw21)},
		{"table_null_row", wTable, `{"rows":[null]}`},
		{"table_short_row", wTable, `{"rows":[["only-one"]]}`},
		{"table_long_row", wTable, `{"rows":[["a","b","c"]]}`},
		{"table_cell_number", wTable, `{"rows":[["a",123]]}`},
		{"table_cell_null", wTable, `{"rows":[["a",null]]}`},
		{"table_cell_81", wTable, `{"rows":[["a","` + strings.Repeat("x", 81) + `"]]}`},
		{"table_cell_newline", wTable, `{"rows":[["a","x\ny"]]}`},
		{"table_extra_value", wTable, `{"rows":[],"value":1}`},
	}
	for _, c := range invalid {
		t.Run("bad_"+c.name, func(t *testing.T) {
			if got, err := ParsePageReading(c.watch, json.RawMessage(c.raw)); err == nil {
				t.Fatalf("expected error, got %+v", got)
			}
		})
	}
}

func TestTripsEdgeCases(t *testing.T) {
	v := 10.0
	if Trips(nil, &PageWatchReading{Value: &v}) {
		t.Fatal("nil rule tripped")
	}
	if Trips(&PageWatchAlert{When: PageWatchAlertAbove, Number: 5}, nil) {
		t.Fatal("nil reading tripped")
	}
	if Trips(&PageWatchAlert{When: PageWatchAlertAbove, Number: 5}, &PageWatchReading{Status: PageWatchStatusProblem}) {
		t.Fatal("mismatched reading tripped")
	}
	if Trips(&PageWatchAlert{When: PageWatchAlertStatus, Status: PageWatchStatusOK}, &PageWatchReading{Status: PageWatchStatusProblem}) {
		t.Fatal("invalid status ok rule tripped")
	}
	if Trips(&PageWatchAlert{When: PageWatchAlertRows, Rows: 0}, &PageWatchReading{Rows: [][]string{}}) {
		t.Fatal("zero rows threshold tripped")
	}
}
