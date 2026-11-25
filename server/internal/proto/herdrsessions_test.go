package proto

import (
	"slices"
	"strings"
	"testing"
)

func TestValidHerdrSessionName(t *testing.T) {
	for _, good := range []string{"agent", "build", "work_1", "v2.0", "a-b_c.1"} {
		if !ValidHerdrSessionName(good) {
			t.Errorf("ValidHerdrSessionName(%q) = false, want true", good)
		}
	}
	for _, bad := range []string{"", " ", ".", "..", "default", "persona-1234", "persona-", "a/b", "a:b", "a b", "@agent", strings.Repeat("a", 129)} {
		if ValidHerdrSessionName(bad) {
			t.Errorf("ValidHerdrSessionName(%q) = true, want false", bad)
		}
	}
}

func TestValidateBlockedHerdrSessions(t *testing.T) {
	got, err := ValidateBlockedHerdrSessions([]string{" agent ", "build"})
	if err != nil || !slices.Equal(got, []string{"agent", "build"}) {
		t.Fatalf("ValidateBlockedHerdrSessions = %v, %v", got, err)
	}
	if _, err := ValidateBlockedHerdrSessions([]string{"agent", " agent "}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := ValidateBlockedHerdrSessions([]string{"default"}); err == nil {
		t.Fatal("default accepted")
	}
	if _, err := ValidateBlockedHerdrSessions([]string{"persona-abc"}); err == nil {
		t.Fatal("persona-* accepted")
	}
	many := make([]string, MaxBlockedHerdrSessions+1)
	for i := range many {
		many[i] = "s" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	if _, err := ValidateBlockedHerdrSessions(many); err == nil {
		t.Fatal("too many accepted")
	}
}

func TestCleanBlockedHerdrSessions(t *testing.T) {
	got := CleanBlockedHerdrSessions([]string{" agent ", "default", "persona-x", "agent", "build", "bad/name"})
	if !slices.Equal(got, []string{"agent", "build"}) {
		t.Fatalf("CleanBlockedHerdrSessions = %v", got)
	}
}
