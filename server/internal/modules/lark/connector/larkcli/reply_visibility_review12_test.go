package larkcli

import "testing"

func TestReplyVisibilityReview12RequiresPositiveGroupMetadata(t *testing.T) {
	for _, tc := range []struct {
		name       string
		chatType   string
		chatMode   string
		wantDirect bool
		wantKnown  bool
	}{
		{name: "private type alone", chatType: "private", wantKnown: false},
		{name: "public type alone", chatType: "public", wantKnown: false},
		{name: "private mode alone", chatMode: "private", wantKnown: false},
		{name: "public mode with unknown type", chatType: "unrecognized", chatMode: "public", wantKnown: false},
		{name: "explicit group mode", chatType: "private", chatMode: "group", wantKnown: true},
		{name: "explicit topic mode", chatMode: "topic", wantKnown: true},
		{name: "explicit group type", chatType: "group", wantKnown: true},
		{name: "direct metadata still wins", chatType: "private", chatMode: "p2p", wantDirect: true, wantKnown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct, known := directChatType(tc.chatType, tc.chatMode)
			if direct != tc.wantDirect || known != tc.wantKnown {
				t.Errorf("directChatType(%q, %q) = (%v, %v), want (%v, %v)", tc.chatType, tc.chatMode, direct, known, tc.wantDirect, tc.wantKnown)
			}
		})
	}

	c := testConnector(t, "success")
	setReplyVisibility(t, c, `{"inbox_exclude_direct_chats":true}`)
	if !c.replyChatExcluded("synthetic-private", "private", "") {
		t.Error("chat_type private without group/topic metadata passed the all-direct-chats boundary")
	}
	if c.replyChatExcluded("synthetic-group", "private", "group") {
		t.Error("explicit positive group metadata was rejected")
	}
}
