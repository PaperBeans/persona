#if os(iOS)
import SwiftUI

/// Meeting or Note (#5243, docs/apple/recording.md「Meeting recordings」): what Record makes. A meeting gets minutes and
/// Persona can attend it (`kind: meeting`); a note gets neither (`kind: note`). Record is always a meeting unless Note is
/// picked for that one start on the Recordings page, and the live sheet's page menu switches a running recording either way
/// (`MeetingRecorder.setMeeting`). Never a stored setting: every one-tap start elsewhere is a meeting.
enum RecordingKindChoice: String, CaseIterable, Hashable {
    case meeting
    case note

    init(_ entry: RecordingEntry) { self = entry.isMeeting ? .meeting : .note }

    var title: String {
        switch self {
        case .meeting: String(localized: "Meeting")
        case .note: String(localized: "Note [Recording Kind]")
        }
    }

    /// The mark the recording's row carries for it (`RecordingRow`).
    var symbol: String {
        switch self {
        case .meeting: Theme.Icon.meeting
        case .note: Theme.Icon.recording
        }
    }
}

/// What one tap of Record starts on the Recordings page (`RecordingCaptureControls`).
enum RecordingStart: Equatable {
    /// `MeetingRecorder.startMeeting` with Persona's part; nil when the hub doesn't offer attending (the one-tap default).
    case meeting(RecordingAttendance?)
    /// The plain start: no calendar name, no minutes, no Persona.
    case note

    /// `kind`'s start with what the page shows: the role, the private mark (with the meeting conversation, #3862) and who
    /// attends (`attendee`: the one picked there and still listed, else the one picked last that isn't the main chat; read
    /// only when it matters). With nobody chosen to attend Persona starts off, and the main chat attends only when picked.
    static func make(_ kind: RecordingKindChoice, offered: Bool, conversation: Bool, photos: Bool, role: MeetingAttendance,
                     privateMark: Bool, attendee: @autoclosure () -> MeetingAttendees.Candidate?) -> RecordingStart {
        guard kind == .meeting else { return .note }
        guard offered else { return .meeting(nil) }
        // A room meeting whatever the role, off included: every upload carries `room` and the private mark.
        if conversation { return .meeting(.room(role, private: privateMark, photos: photos)) }
        guard role.attends, let chosen = attendee() else { return .meeting(RecordingAttendance(role: .off)) }
        return .meeting(MeetingAttendees.choose(chosen, in: RecordingAttendance(role: role)))
    }
}

/// Meeting / Note for the recording being made: a section of the live sheet's page menu (`RecordingLiveView`, `MeetingPage`),
/// so it stays a secondary choice and never a second button. Turning it into a note turns Persona off; the sheet then
/// shows the page for what it has become (a room meeting's page, or the live view).
struct RecordingKindPicker: View {
    var entry: RecordingEntry

    var body: some View {
        Section("Type") {
            Picker(selection: Binding(get: { RecordingKindChoice(entry) }, set: { kind in
                let id = entry.id
                Task { await MeetingRecorder.shared.setMeeting(kind == .meeting, recordingID: id) }
            })) {
                ForEach(RecordingKindChoice.allCases, id: \.self) { Label($0.title, systemImage: $0.symbol).tag($0) }
            } label: {
                Text("Type")
            }
            .accessibilityIdentifier("recording-kind-now")
        }
    }
}
#endif
