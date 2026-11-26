import AppIntents
import SwiftUI
import WidgetKit

/// Control Center / Lock Screen / Action Button: Start Meeting (#8731; Record before), a meeting (`StartMeetingRecordingIntent`), without opening the
/// app; its Live Activity then shows the time and Stop. The kind is the control's identity: never change it (it was
/// Record a Meeting until #5243).
///
/// While the hub's recording module is off (#15214) it offers no start: the system always draws a placed control, so it
/// is drawn disabled, named for what it is with "Off on this hub" under it. What it draws comes from the widgets'
/// snapshot in the App Group (`HomeSnapshot.recordingModule`; absent or unreadable means allowed), never the keychain;
/// the app reloads it when the hub's answer changes (`RecordingModule.tellSurfaces`).
struct MeetingRecordingControl: ControlWidget {
    static let kind = "persona.control.meeting-recording"

    var body: some ControlWidgetConfiguration {
        StaticControlConfiguration(kind: Self.kind, provider: MeetingRecordingControlOffer()) { offered in
            ControlWidgetButton(action: StartMeetingRecordingIntent()) {
                if offered {
                    Label("Start Meeting", systemImage: RecordingSymbol.recordingAction)
                } else {
                    Label {
                        Text("Meeting")
                        Text("Off on this hub")
                    } icon: {
                        Image(systemName: RecordingSymbol.recordingAction)
                    }
                }
            }
            .disabled(!offered)
        }
        .displayName("Start Meeting")
        .description("Starts recording a meeting in Persona, named after the calendar event happening now.")
    }
}

/// Whether the control offers a start: what the app last wrote for the widgets, read from the App Group file.
struct MeetingRecordingControlOffer: ControlValueProvider {
    /// The gallery and a control placed before the app ever ran: Start Meeting, as always.
    var previewValue: Bool { true }

    func currentValue() async throws -> Bool {
        HomeSnapshotStore.shared.read()?.offersRecordingStart ?? true
    }
}
