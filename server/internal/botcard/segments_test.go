package botcard

import (
	"slices"
	"strconv"
	"testing"
)

func TestElementBytes(t *testing.T) {
	got := ElementBytes(func(c *Card) {
		c.Markdown("<&>")
		c.HR()
	})
	// json.Marshal escapes HTML and each element carries its joining comma; Card.JSON itself does not escape HTML.
	// Escaped Markdown is 49 bytes, HR is 12; each element also carries one joining comma.
	const want = 49 + 1 + 12 + 1
	if got != want {
		t.Fatalf("ElementBytes = %d, want %d", got, want)
	}
}

type segmentTestEntry struct {
	id       string
	bytes    int
	elements int
}

func segmentTestMeasure(e segmentTestEntry) (int, int) { return e.bytes, e.elements }

func segmentTestIDs(es []segmentTestEntry) []string {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = e.id
	}
	return ids
}

func TestSegments(t *testing.T) {
	tests := []struct {
		name         string
		entries      []segmentTestEntry
		head1, headN int
		hr           int
		want         [][]string
	}{
		{name: "empty input keeps one empty segment", want: [][]string{{}}},
		{
			name:    "one entry",
			entries: []segmentTestEntry{{id: "a", bytes: 10, elements: 1}},
			head1:   5,
			want:    [][]string{{"a"}},
		},
		{
			name: "byte budget includes dividers and later headers",
			entries: []segmentTestEntry{
				{id: "a", bytes: 12000, elements: 1},
				{id: "b", bytes: 11890, elements: 1},
				{id: "c", bytes: 12000, elements: 1},
			},
			head1: 100,
			headN: 50,
			hr:    10,
			want:  [][]string{{"a", "b"}, {"c"}}, // a+b, including HR, lands exactly on MaxBytes.
		},
		{
			name: "an oversized entry stays whole",
			entries: []segmentTestEntry{
				{id: "a", bytes: MaxBytes + 1, elements: 1},
				{id: "b", bytes: 1, elements: 1},
				{id: "c", bytes: 1, elements: 1},
			},
			want: [][]string{{"a"}, {"b", "c"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Segments(tc.entries, tc.head1, tc.headN, tc.hr, segmentTestMeasure)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d segments, want %d: %#v", len(got), len(tc.want), got)
			}
			for i := range got {
				if ids := segmentTestIDs(got[i]); !slices.Equal(ids, tc.want[i]) {
					t.Errorf("segment %d = %v, want %v", i, ids, tc.want[i])
				}
			}
		})
	}
}

func TestSegmentsElementBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		elements int
		want     []int
	}{
		{name: "plain entries", count: 50, elements: 1, want: []int{24, 25, 1}},
		{name: "button rows", count: 33, elements: 2, want: []int{16, 16, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]segmentTestEntry, tc.count)
			for i := range entries {
				entries[i] = segmentTestEntry{id: strconv.Itoa(i), elements: tc.elements}
			}
			got := Segments(entries, 0, 0, 0, segmentTestMeasure)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d segments, want %d", len(got), len(tc.want))
			}
			next := 0
			for i, seg := range got {
				if len(seg) != tc.want[i] {
					t.Errorf("segment %d has %d entries, want %d", i, len(seg), tc.want[i])
				}
				for _, e := range seg {
					if e.id != strconv.Itoa(next) {
						t.Fatalf("entry %d = %q; entries were lost or reordered", next, e.id)
					}
					next++
				}
			}
		})
	}
}
