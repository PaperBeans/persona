package hub

import (
	"context"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// featureMeetingAttendance: the default manager attends recorded meetings (docs/assistant/meeting-minutes.md
// "Attending a meeting"). The recordings' routes go to it, so its hello alone decides; an extension's report cannot
// add the feature (features).
const featureMeetingAttendance = assistantwire.CapabilityMeetingAttendance

// featureMeetingConversation: the default manager takes the meeting conversation's phase 1
// (docs/assistant/meeting-minutes.md "Meeting conversation (#3862)", "Flags and compatibility").
const featureMeetingConversation = assistantwire.CapabilityMeetingConversation

// meetingAttendanceFeatures is the feature while the default manager is connected and named the capability in its
// hello; nothing otherwise, so a phone offers no role to an older manager, which would ignore it.
func (h *Hub) meetingAttendanceFeatures() []string {
	if !h.defaultManagerNames(assistantwire.CapabilityMeetingAttendance) {
		return nil
	}
	return []string{featureMeetingAttendance}
}

// meetingConversationFeatures is meeting_conversation while the default manager names it beside meeting_attendance
// (a manager that takes the room takes #2981 too); nothing otherwise, so a phone asks no older manager for a room.
func (h *Hub) meetingConversationFeatures() []string {
	if !h.defaultManagerNames(assistantwire.CapabilityMeetingAttendance) || !h.defaultManagerNames(assistantwire.CapabilityMeetingConversation) {
		return nil
	}
	return []string{featureMeetingConversation}
}

// defaultManagerNames: the default manager (the one the recordings' routes go to) is connected and named capability
// in its hello.
func (h *Hub) defaultManagerNames(capability string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return h.archiveManagerHas(h.defaultOwner(ctx), capability) == http.StatusOK
}
