package kb

import (
	"slices"
	"strings"
	"testing"
)

func hseg(start, end float64, text string) RecordingSegment {
	return RecordingSegment{Start: start, End: end, Text: text}
}

// The transcript of record: device mode is the phone's; hub mode the hub's sentences, with each phone segment the
// hub's cover no more than half of (their union), in time order, each with its source.
func TestTranscriptOfRecord(t *testing.T) {
	device := []RecordingSegment{hseg(0, 10, "手机一"), hseg(10, 20, "手机二"), hseg(20, 30, "phone three"), hseg(30, 40, "phone four")}
	hub := []RecordingSegment{
		hseg(0, 3, "我们先把 export cap 保持在 4 MB。"), hseg(3.5, 6, "Bo owns the vendor follow-up."), // 6 s of 0–10 together: 55%
		hseg(10, 14.9, "Let's revisit pricing next week."),       // 49% of 10–20
		hseg(30, 35.1, "下周三之前 Alice 要把 pricing 的 proposal 发出来。"), // 51% of 30–40
	}
	if got := TranscriptOfRecord(false, device, hub); !slices.Equal(got, device) {
		t.Fatalf("device mode %+v", got)
	}
	got := TranscriptOfRecord(true, device, hub)
	var lines []string
	for _, sg := range got {
		lines = append(lines, sg.Source+": "+sg.Text)
	}
	want := []string{
		"hub: 我们先把 export cap 保持在 4 MB。",
		"hub: Bo owns the vendor follow-up.",
		"hub: Let's revisit pricing next week.",
		"device: 手机二",
		"device: phone three",
		"hub: 下周三之前 Alice 要把 pricing 的 proposal 发出来。",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("record\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	// Hub only (the phone wrote nothing), and the phone only (the hub has nothing yet: failed).
	if got := TranscriptOfRecord(true, nil, hub); len(got) != 4 || got[0].Source != SourceHub {
		t.Fatalf("hub only %+v", got)
	}
	if got := TranscriptOfRecord(true, device, nil); len(got) != 4 || got[3].Source != SourceDevice {
		t.Fatalf("phone only %+v", got)
	}
}
