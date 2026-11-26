package assistant

import (
	"reflect"
	"slices"
	"testing"
)

// postToInbox rebases a committed post on a live row that changed meanwhile field by field with reflect, which panics
// on an unexported field ("reflect.Value.Interface: cannot return value obtained from unexported field or method").
// The unexported ones are rebased by hand in rebaseInboxBookkeeping: a new one must be added there, and here.
func TestInboxPostRebaseCoversUnexportedFields(t *testing.T) {
	var unexported []string
	for f := range reflect.TypeFor[InboxItem]().Fields() {
		if !f.IsExported() {
			unexported = append(unexported, f.Name)
		}
	}
	if want := []string{"externalMoves", "externalChecking", "replyClassifySerial", "receivedInCollection"}; !slices.Equal(unexported, want) {
		t.Fatalf("InboxItem's unexported fields are %v; rebaseInboxBookkeeping (inbox_post.go) handles %v: rebase the new one there", unexported, want)
	}
}

func TestRebaseInboxBookkeepingKeepsConcurrentChanges(t *testing.T) {
	before := InboxItem{replyClassifySerial: 3, receivedInCollection: false}
	// changed on the live row while the post was saving: the live row's newer values stay
	live := InboxItem{replyClassifySerial: 3, receivedInCollection: true} // what the post's apply gave it
	latest := InboxItem{replyClassifySerial: 4, receivedInCollection: false}
	rebaseInboxBookkeeping(&live, &before, &latest)
	if live.replyClassifySerial != 4 {
		t.Fatalf("a classification started while the post was saving lost its serial: %d", live.replyClassifySerial)
	}
	if !live.receivedInCollection {
		t.Fatal("unchanged meanwhile: the post's own received mark should stay")
	}
	// a concurrent consumer cleared the received mark: that stays too
	before, latest = InboxItem{receivedInCollection: true}, InboxItem{receivedInCollection: false}
	live = InboxItem{receivedInCollection: true}
	rebaseInboxBookkeeping(&live, &before, &latest)
	if live.receivedInCollection {
		t.Fatal("a mark cleared while the post was saving came back")
	}
}
