package assistantwire

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestNoticeKind(t *testing.T) {
	for _, tt := range []struct {
		kind NoticeKind
		wire string
	}{
		{NoticeReport(), `"report"`},
		{NoticePush(), `"push"`},
		{NoticeTask(), `"task"`},
	} {
		if !tt.kind.Known() {
			t.Errorf("%#v: want a known notice kind", tt.kind)
		}
		if got, err := json.Marshal(tt.kind); err != nil || string(got) != tt.wire {
			t.Errorf("marshal %#v = %s, %v, want %s", tt.kind, got, err, tt.wire)
		}
	}
}

func TestUnknownNoticeKindIsADefect(t *testing.T) {
	for _, raw := range []string{"", "unknown"} {
		t.Run(raw, func(t *testing.T) {
			kind := NoticeKind{wire: raw}
			if kind.Known() {
				t.Fatalf("%q is not a notice kind", raw)
			}
			defer func() {
				r := recover()
				want := fmt.Sprintf("assistantwire.NoticeKind: unknown notice kind %q", raw)
				if err, ok := r.(error); !ok || err.Error() != want {
					t.Errorf("RequireKnown panic = %v, want %q", r, want)
				}
			}()
			kind.RequireKnown()
		})
	}
}
