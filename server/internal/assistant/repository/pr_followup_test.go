package repository

import "testing"

func TestPRFollowupWatchStatusHelpers(t *testing.T) {
	for _, tc := range []struct {
		name          string
		watchStatus   string
		wantEffective string
		wantUnwatched bool
	}{
		{name: "legacy empty reads active", watchStatus: "", wantEffective: PRFollowupWatchActive, wantUnwatched: false},
		{name: "explicit active", watchStatus: PRFollowupWatchActive, wantEffective: PRFollowupWatchActive, wantUnwatched: false},
		{name: "host not connected", watchStatus: PRFollowupWatchHostNotConnected, wantEffective: PRFollowupWatchHostNotConnected, wantUnwatched: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := PRFollowup{WatchStatus: tc.watchStatus}
			if got := f.EffectiveWatchStatus(); got != tc.wantEffective {
				t.Fatalf("EffectiveWatchStatus() = %q, want %q", got, tc.wantEffective)
			}
			if got := f.IsUnwatched(); got != tc.wantUnwatched {
				t.Fatalf("IsUnwatched() = %v, want %v", got, tc.wantUnwatched)
			}
		})
	}
}
