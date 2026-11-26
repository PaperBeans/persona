package assistantwire

import (
	"encoding/json"
	"fmt"
)

// NoticeKind is the source of a card handed to the notifications companion.
// The zero value is unknown and is a defect when passed to the board port.
type NoticeKind struct{ wire string }

const (
	noticeReport = "report"
	noticePush   = "push"
	noticeTask   = "task"
)

// NoticeReport is a task report.
func NoticeReport() NoticeKind { return NoticeKind{wire: noticeReport} }

// NoticePush is a notification sent by notify_user or another push source.
func NoticePush() NoticeKind { return NoticeKind{wire: noticePush} }

// NoticeTask is the mirrored result of a scheduled task.
func NoticeTask() NoticeKind { return NoticeKind{wire: noticeTask} }

// Known reports whether k is one of the companion's notice kinds.
func (k NoticeKind) Known() bool {
	switch k.wire {
	case noticeReport, noticePush, noticeTask:
		return true
	}
	return false
}

// RequireKnown raises a defect for an unknown notice kind.
func (k NoticeKind) RequireKnown() {
	if !k.Known() {
		panic(fmt.Errorf("assistantwire.NoticeKind: unknown notice kind %q", k.wire))
	}
}

// MarshalJSON encodes the companion's JSON kind value.
func (k NoticeKind) MarshalJSON() ([]byte, error) {
	k.RequireKnown()
	return json.Marshal(k.wire)
}
