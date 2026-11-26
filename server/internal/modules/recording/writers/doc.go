// Package writers is the recording module's writers: the upload of a recording and a day recording's appends, with
// the rule that marks Persona's echo; the live log and meeting.json of a meeting, and the material of an answer asked
// in one; the rounds that make a meeting's minutes and a day recording's recap with a language model and store them
// next to the recording, and the requests that ask for them again or record the to-dos made of them; the live titles
// and the owner's rename; the photo intake; a hub-mode meeting's audio (the phone's socket, the gap uploads, the
// final pass's reads) and the hub's own transcript of it. They reach the knowledge base that holds the recordings only
// through the storage port (recordingwire.Core); the knowledge base reaches them only through recordingwire.Provider
// (docs/plans/plan-kb-recording-port.md, section 3; docs/architecture/core-and-modules.md "Recording").
//
//persona:layer application
package writers
