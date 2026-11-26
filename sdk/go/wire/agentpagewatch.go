package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dashboards phase 2 watch pages (#13020; docs/assistant/dashboards.md "Phase 2: watch pages (#13020)"):
// one question, one of four displays (number, status, table, trend), display-specific fields, an optional alert rule,
// validated readings, and deterministic rule evaluation (Trips).

const (
	PageWatchBytesMost        = 16 << 10
	PageWatchReadingBytesMost = 32 << 10
	PageWatchQuestionMost     = 200
	PageWatchUnitMost         = 8
	PageWatchColumnsMin       = 1
	PageWatchColumnsMost      = 4
	PageWatchColumnMost       = 40
	PageWatchAlertRowsMin     = 1
	PageWatchAlertRowsMax     = 100
	PageWatchNumberMax        = 1e12
	PageWatchTextMost         = 80
	PageWatchRowsMost         = 20
	PageWatchCellMost         = 80
)

// PageWatchDisplay is how a watch page draws its readings.
type PageWatchDisplay string

const (
	PageWatchNumber PageWatchDisplay = "number"
	PageWatchStatus PageWatchDisplay = "status"
	PageWatchTable  PageWatchDisplay = "table"
	PageWatchTrend  PageWatchDisplay = "trend"
)

// Valid reports whether d is one of the four watch displays.
func (d PageWatchDisplay) Valid() bool {
	switch d {
	case PageWatchNumber, PageWatchStatus, PageWatchTable, PageWatchTrend:
		return true
	}
	return false
}

// PageWatchStatusValue is the level of a status watch reading or alert rule.
type PageWatchStatusValue string

const (
	PageWatchStatusOK      PageWatchStatusValue = "ok"
	PageWatchStatusWarning PageWatchStatusValue = "warning"
	PageWatchStatusProblem PageWatchStatusValue = "problem"
)

// Valid reports whether s is a valid status reading value.
func (s PageWatchStatusValue) Valid() bool {
	switch s {
	case PageWatchStatusOK, PageWatchStatusWarning, PageWatchStatusProblem:
		return true
	}
	return false
}

func (s PageWatchStatusValue) rank() int {
	switch s {
	case PageWatchStatusOK:
		return 0
	case PageWatchStatusWarning:
		return 1
	case PageWatchStatusProblem:
		return 2
	}
	return -1
}

// PageWatchAlertWhen is the kind of condition an alert rule checks.
type PageWatchAlertWhen string

const (
	PageWatchAlertAbove  PageWatchAlertWhen = "above"
	PageWatchAlertBelow  PageWatchAlertWhen = "below"
	PageWatchAlertStatus PageWatchAlertWhen = "status"
	PageWatchAlertRows   PageWatchAlertWhen = "rows"
)

// Valid reports whether w is one of the four alert rule kinds.
func (w PageWatchAlertWhen) Valid() bool {
	switch w {
	case PageWatchAlertAbove, PageWatchAlertBelow, PageWatchAlertStatus, PageWatchAlertRows:
		return true
	}
	return false
}

// PageWatchAlert is a watch's optional alert rule. In JSON it is {"when": "...", "value": ...}, where value is a
// finite number (above, below), "warning"|"problem" (status), or an integer 1..100 (rows).
type PageWatchAlert struct {
	When   PageWatchAlertWhen
	Number float64
	Status PageWatchStatusValue
	Rows   int
}

// MarshalJSON encodes PageWatchAlert as {"when": ..., "value": ...}.
func (a PageWatchAlert) MarshalJSON() ([]byte, error) {
	switch a.When {
	case PageWatchAlertAbove, PageWatchAlertBelow:
		if !validWatchNumber(a.Number) {
			return nil, fmt.Errorf("alert.value: finite number with abs <= %g required", PageWatchNumberMax)
		}
		v := a.Number
		if v == 0 {
			v = 0 // avoid -0
		}
		return json.Marshal(struct {
			When  PageWatchAlertWhen `json:"when"`
			Value float64            `json:"value"`
		}{When: a.When, Value: v})
	case PageWatchAlertStatus:
		if a.Status != PageWatchStatusWarning && a.Status != PageWatchStatusProblem {
			return nil, fmt.Errorf("alert.value: status rule must be %q or %q", PageWatchStatusWarning, PageWatchStatusProblem)
		}
		return json.Marshal(struct {
			When  PageWatchAlertWhen   `json:"when"`
			Value PageWatchStatusValue `json:"value"`
		}{When: a.When, Value: a.Status})
	case PageWatchAlertRows:
		if a.Rows < PageWatchAlertRowsMin || a.Rows > PageWatchAlertRowsMax {
			return nil, fmt.Errorf("alert.value: rows rule must be %d to %d", PageWatchAlertRowsMin, PageWatchAlertRowsMax)
		}
		return json.Marshal(struct {
			When  PageWatchAlertWhen `json:"when"`
			Value int                `json:"value"`
		}{When: a.When, Value: a.Rows})
	}
	return nil, fmt.Errorf("alert.when: unknown rule %q", a.When)
}

// UnmarshalJSON decodes {"when": ..., "value": ...} and validates its shape and limits.
func (a *PageWatchAlert) UnmarshalJSON(data []byte) error {
	fields, err := decodeStrictJSONObject(data, "alert", "when", "value")
	if err != nil {
		return err
	}
	whenRaw, ok := fields["when"]
	if !ok {
		return errors.New("alert.when: required")
	}
	valRaw, ok := fields["value"]
	if !ok {
		return errors.New("alert.value: required")
	}
	var when string
	if err := decodeStrictJSONString(whenRaw, &when); err != nil {
		return errors.New("alert.when: string required")
	}
	next := PageWatchAlert{When: PageWatchAlertWhen(when)}
	switch next.When {
	case PageWatchAlertAbove, PageWatchAlertBelow:
		n, err := decodeStrictJSONNumber(valRaw)
		if err != nil || !validWatchNumber(n) {
			return fmt.Errorf("alert.value: finite number with abs <= %g required", PageWatchNumberMax)
		}
		if n == 0 {
			n = 0
		}
		next.Number = n
	case PageWatchAlertStatus:
		var s string
		if err := decodeStrictJSONString(valRaw, &s); err != nil {
			return fmt.Errorf("alert.value: %q or %q required", PageWatchStatusWarning, PageWatchStatusProblem)
		}
		sv := PageWatchStatusValue(s)
		if sv != PageWatchStatusWarning && sv != PageWatchStatusProblem {
			return fmt.Errorf("alert.value: status rule must be %q or %q", PageWatchStatusWarning, PageWatchStatusProblem)
		}
		next.Status = sv
	case PageWatchAlertRows:
		n, err := decodeStrictJSONNumber(valRaw)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < PageWatchAlertRowsMin || n > PageWatchAlertRowsMax {
			return fmt.Errorf("alert.value: integer %d to %d required", PageWatchAlertRowsMin, PageWatchAlertRowsMax)
		}
		next.Rows = int(n)
	default:
		return fmt.Errorf("alert.when: must be above, below, status or rows, not %q", when)
	}
	*a = next
	return nil
}

// PageWatch is a dashboard page's watch definition.
type PageWatch struct {
	Question string           `json:"question"`
	Display  PageWatchDisplay `json:"display"`
	Unit     string           `json:"unit,omitempty"`
	Columns  []string         `json:"columns,omitempty"`
	Alert    *PageWatchAlert  `json:"alert,omitempty"`
}

// ParsePageWatch parses and validates a watch definition JSON object against every limit in
// docs/assistant/dashboards.md ("The watch").
func ParsePageWatch(raw json.RawMessage) (*PageWatch, error) {
	if len(raw) == 0 {
		return nil, errors.New("watch: JSON object required")
	}
	if len(raw) > PageWatchBytesMost {
		return nil, fmt.Errorf("watch: at most %d bytes", PageWatchBytesMost)
	}
	fields, err := decodeStrictJSONObject(raw, "watch", "question", "display", "unit", "columns", "alert")
	if err != nil {
		return nil, err
	}
	var w PageWatch
	qRaw, ok := fields["question"]
	if !ok {
		return nil, errors.New("watch.question: required")
	}
	if err := decodeStrictJSONString(qRaw, &w.Question); err != nil {
		return nil, errors.New("watch.question: string required")
	}
	dRaw, ok := fields["display"]
	if !ok {
		return nil, errors.New("watch.display: required")
	}
	var disp string
	if err := decodeStrictJSONString(dRaw, &disp); err != nil {
		return nil, errors.New("watch.display: string required")
	}
	w.Display = PageWatchDisplay(disp)

	if uRaw, has := fields["unit"]; has && !isJSONNull(uRaw) {
		if err := decodeStrictJSONString(uRaw, &w.Unit); err != nil {
			return nil, errors.New("watch.unit: string required")
		}
	}
	if cRaw, has := fields["columns"]; has && !isJSONNull(cRaw) {
		cols, err := decodeStrictJSONStringSlice(cRaw, "watch.columns")
		if err != nil {
			return nil, err
		}
		w.Columns = cols
	}
	if aRaw, has := fields["alert"]; has && !isJSONNull(aRaw) {
		var alert PageWatchAlert
		if err := json.Unmarshal(aRaw, &alert); err != nil {
			return nil, err
		}
		w.Alert = &alert
	}
	if err := CheckPageWatch(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

// CheckPageWatch validates and normalizes (trims single-line strings on) a PageWatch.
func CheckPageWatch(w *PageWatch) error {
	if w == nil {
		return errors.New("watch: required")
	}
	q, err := checkWatchLine("watch.question", w.Question, 1, PageWatchQuestionMost)
	if err != nil {
		return err
	}
	w.Question = q
	if !w.Display.Valid() {
		return fmt.Errorf("watch.display: must be number, status, table or trend, not %q", w.Display)
	}
	switch w.Display {
	case PageWatchNumber, PageWatchTrend:
		if len(w.Columns) > 0 {
			return fmt.Errorf("watch.columns: only valid for table display, not %s", w.Display)
		}
		if w.Unit != "" {
			u, err := checkWatchLine("watch.unit", w.Unit, 0, PageWatchUnitMost)
			if err != nil {
				return err
			}
			w.Unit = u
		}
	case PageWatchStatus:
		if w.Unit != "" {
			return errors.New("watch.unit: only valid for number and trend displays")
		}
		if len(w.Columns) > 0 {
			return errors.New("watch.columns: only valid for table display")
		}
	case PageWatchTable:
		if w.Unit != "" {
			return errors.New("watch.unit: only valid for number and trend displays")
		}
		if len(w.Columns) < PageWatchColumnsMin || len(w.Columns) > PageWatchColumnsMost {
			return fmt.Errorf("watch.columns: table display requires %d to %d columns", PageWatchColumnsMin, PageWatchColumnsMost)
		}
		cols := make([]string, len(w.Columns))
		for i, c := range w.Columns {
			clean, err := checkWatchLine(fmt.Sprintf("watch.columns[%d]", i), c, 1, PageWatchColumnMost)
			if err != nil {
				return err
			}
			cols[i] = clean
		}
		w.Columns = cols
	}
	if w.Alert != nil {
		if err := checkPageWatchAlert(w.Display, w.Alert); err != nil {
			return err
		}
	}
	return nil
}

func checkPageWatchAlert(display PageWatchDisplay, a *PageWatchAlert) error {
	switch display {
	case PageWatchNumber, PageWatchTrend:
		if a.When != PageWatchAlertAbove && a.When != PageWatchAlertBelow {
			return fmt.Errorf("watch.alert.when: %s display requires above or below, not %q", display, a.When)
		}
		if !validWatchNumber(a.Number) {
			return fmt.Errorf("watch.alert.value: finite number with abs <= %g required", PageWatchNumberMax)
		}
		if a.Number == 0 {
			a.Number = 0
		}
	case PageWatchStatus:
		if a.When != PageWatchAlertStatus {
			return fmt.Errorf("watch.alert.when: status display requires status, not %q", a.When)
		}
		if a.Status != PageWatchStatusWarning && a.Status != PageWatchStatusProblem {
			return fmt.Errorf("watch.alert.value: status alert must be %q or %q", PageWatchStatusWarning, PageWatchStatusProblem)
		}
	case PageWatchTable:
		if a.When != PageWatchAlertRows {
			return fmt.Errorf("watch.alert.when: table display requires rows, not %q", a.When)
		}
		if a.Rows < PageWatchAlertRowsMin || a.Rows > PageWatchAlertRowsMax {
			return fmt.Errorf("watch.alert.value: rows alert must be %d to %d", PageWatchAlertRowsMin, PageWatchAlertRowsMax)
		}
	default:
		return fmt.Errorf("watch.display: unknown display %q", display)
	}
	return nil
}

// PageWatchReading is one reading answering a watch's question.
type PageWatchReading struct {
	Value  *float64             `json:"value,omitempty"`
	Status PageWatchStatusValue `json:"status,omitempty"`
	Text   string               `json:"text,omitempty"`
	Rows   [][]string           `json:"rows,omitempty"`
}

// MarshalJSON encodes a PageWatchReading while preserving {"rows":[]} for empty table readings and {"value":0} for
// zero number readings.
func (r PageWatchReading) MarshalJSON() ([]byte, error) {
	switch {
	case r.Value != nil:
		v := *r.Value
		if !validWatchNumber(v) {
			return nil, fmt.Errorf("reading.value: finite number with abs <= %g required", PageWatchNumberMax)
		}
		if v == 0 {
			v = 0
		}
		return json.Marshal(struct {
			Value float64 `json:"value"`
		}{Value: v})
	case r.Status != "":
		return json.Marshal(struct {
			Status PageWatchStatusValue `json:"status"`
			Text   string               `json:"text,omitempty"`
		}{Status: r.Status, Text: r.Text})
	case r.Rows != nil:
		return json.Marshal(struct {
			Rows [][]string `json:"rows"`
		}{Rows: r.Rows})
	}
	return []byte("{}"), nil
}

// ParsePageReading parses and validates a reading JSON object against watch's display and limits in
// docs/assistant/dashboards.md ("Readings and page patch").
func ParsePageReading(watch *PageWatch, raw json.RawMessage) (*PageWatchReading, error) {
	if watch == nil {
		return nil, errors.New("reading: watch is required")
	}
	wCopy := *watch
	if err := CheckPageWatch(&wCopy); err != nil {
		return nil, fmt.Errorf("reading: invalid watch: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("reading: JSON object required")
	}
	if len(raw) > PageWatchReadingBytesMost {
		return nil, fmt.Errorf("reading: at most %d bytes", PageWatchReadingBytesMost)
	}
	switch wCopy.Display {
	case PageWatchNumber, PageWatchTrend:
		fields, err := decodeStrictJSONObject(raw, "reading", "value")
		if err != nil {
			return nil, err
		}
		vRaw, ok := fields["value"]
		if !ok {
			return nil, errors.New("reading.value: required")
		}
		n, err := decodeStrictJSONNumber(vRaw)
		if err != nil || !validWatchNumber(n) {
			return nil, fmt.Errorf("reading.value: finite number with abs <= %g required", PageWatchNumberMax)
		}
		if n == 0 {
			n = 0
		}
		return &PageWatchReading{Value: &n}, nil

	case PageWatchStatus:
		fields, err := decodeStrictJSONObject(raw, "reading", "status", "text")
		if err != nil {
			return nil, err
		}
		sRaw, ok := fields["status"]
		if !ok {
			return nil, errors.New("reading.status: required")
		}
		var s string
		if err := decodeStrictJSONString(sRaw, &s); err != nil {
			return nil, errors.New("reading.status: string required")
		}
		sv := PageWatchStatusValue(s)
		if !sv.Valid() {
			return nil, fmt.Errorf("reading.status: must be ok, warning or problem, not %q", s)
		}
		var text string
		if tRaw, has := fields["text"]; has {
			if err := decodeStrictJSONString(tRaw, &text); err != nil {
				return nil, errors.New("reading.text: string required")
			}
			clean, err := checkWatchLine("reading.text", text, 0, PageWatchTextMost)
			if err != nil {
				return nil, err
			}
			text = clean
		}
		return &PageWatchReading{Status: sv, Text: text}, nil

	case PageWatchTable:
		fields, err := decodeStrictJSONObject(raw, "reading", "rows")
		if err != nil {
			return nil, err
		}
		rRaw, ok := fields["rows"]
		if !ok {
			return nil, errors.New("reading.rows: required")
		}
		rowElems, err := decodeStrictJSONArray(rRaw, "reading.rows")
		if err != nil {
			return nil, err
		}
		if len(rowElems) > PageWatchRowsMost {
			return nil, fmt.Errorf("reading.rows: at most %d rows", PageWatchRowsMost)
		}
		wantCols := len(wCopy.Columns)
		rows := make([][]string, len(rowElems))
		for i, elem := range rowElems {
			cells, err := decodeStrictJSONStringSlice(elem, fmt.Sprintf("reading.rows[%d]", i))
			if err != nil {
				return nil, err
			}
			if len(cells) != wantCols {
				return nil, fmt.Errorf("reading.rows[%d]: expected %d cells to match columns, got %d", i, wantCols, len(cells))
			}
			for j, cell := range cells {
				clean, err := checkWatchLine(fmt.Sprintf("reading.rows[%d][%d]", i, j), cell, 0, PageWatchCellMost)
				if err != nil {
					return nil, err
				}
				cells[j] = clean
			}
			rows[i] = cells
		}
		return &PageWatchReading{Rows: rows}, nil
	}
	return nil, fmt.Errorf("reading: unknown display %q", wCopy.Display)
}

// Trips deterministically evaluates whether reading trips rule. Returns false for nil, incomplete or mismatched inputs.
func Trips(rule *PageWatchAlert, reading *PageWatchReading) bool {
	if rule == nil || reading == nil {
		return false
	}
	switch rule.When {
	case PageWatchAlertAbove:
		return reading.Value != nil && validWatchNumber(*reading.Value) && validWatchNumber(rule.Number) && *reading.Value > rule.Number
	case PageWatchAlertBelow:
		return reading.Value != nil && validWatchNumber(*reading.Value) && validWatchNumber(rule.Number) && *reading.Value < rule.Number
	case PageWatchAlertStatus:
		if rule.Status != PageWatchStatusWarning && rule.Status != PageWatchStatusProblem {
			return false
		}
		if !reading.Status.Valid() {
			return false
		}
		return reading.Status.rank() >= rule.Status.rank()
	case PageWatchAlertRows:
		return rule.Rows >= PageWatchAlertRowsMin && rule.Rows <= PageWatchAlertRowsMax && reading.Rows != nil && len(reading.Rows) >= rule.Rows
	}
	return false
}

func validWatchNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= PageWatchNumberMax
}

func checkWatchLine(field, text string, minLen, maxLen int) (string, error) {
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("%s: not valid UTF-8", field)
	}
	text = strings.TrimFunc(text, unicode.IsSpace)
	n := utf8.RuneCountInString(text)
	if n < minLen || n > maxLen {
		return "", fmt.Errorf("%s: %d to %d characters on one line", field, minLen, maxLen)
	}
	for _, r := range text {
		if hiddenTitleRune(r) {
			return "", fmt.Errorf("%s: no line breaks, control, direction or invisible characters (U+%04X)", field, r)
		}
	}
	return text, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeStrictJSONObject parses data as a single JSON object (no trailing tokens, no unknown keys) and returns its
// raw field map.
func decodeStrictJSONObject(data []byte, where string, allowed ...string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s: JSON object required", where)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%s: JSON object required", where)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	allow := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allow[k] = true
	}
	for k := range fields {
		if !allow[k] {
			return nil, fmt.Errorf("%s.%s: unknown field", where, k)
		}
	}
	return fields, nil
}

func decodeStrictJSONString(raw json.RawMessage, out *string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return errors.New("string required")
	}
	return json.Unmarshal(trimmed, out)
}

func decodeStrictJSONNumber(raw json.RawMessage) (float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return 0, errors.New("number required")
	}
	var n float64
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return 0, err
	}
	return n, nil
}

func decodeStrictJSONArray(raw json.RawMessage, where string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s: JSON array required", where)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(trimmed, &elems); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	return elems, nil
}

func decodeStrictJSONStringSlice(raw json.RawMessage, where string) ([]string, error) {
	elems, err := decodeStrictJSONArray(raw, where)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(elems))
	for i, e := range elems {
		if err := decodeStrictJSONString(e, &out[i]); err != nil {
			return nil, fmt.Errorf("%s[%d]: string required", where, i)
		}
	}
	return out, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON content")
	}
	return nil
}
