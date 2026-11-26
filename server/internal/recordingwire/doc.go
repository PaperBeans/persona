// Package recordingwire is the meeting recording's part of the contract (docs/server/architecture.md "Contract
// packages"): the data types of a recording and its files (meta.json, meeting.json, minutes, day recap, photos, live
// log, meeting room, hub segments, the upload and its timeline), their constants, sentinel errors and the error types
// that carry a position. It holds only types, constants and pure helpers and imports only the standard library and
// other contract packages; internal/kb aliases these types and re-exports the constants and errors, so no caller
// changes (docs/plans/plan-kb-recording-port.md).
//
// A Go alias cannot take methods, so the methods these types keep are the ones that need nothing of the knowledge
// base: accessors over their own fields (Recording.AttendanceRole, Meeting.RoomView, Timeline.Merge and the like)
// and the lenient JSON readers (RecordingUpload.UnmarshalJSON, Minutes.UnmarshalJSON) with the parsing they call.
// Everything that applies the knowledge base's rules to them (cleaning an upload, planning minutes and recaps,
// retries, echo matching) is a function in internal/kb taking the value.
package recordingwire
