import Foundation
import Testing
@testable import Persona

/// The ways into a recording from outside the app's pages follow the hub's `module.recording` flag too (#15214): Siri,
/// Shortcuts, the Control, the widgets, the quick action and a reminder's Start refuse plainly instead of starting a
/// local recording that could never sync, and the Live Activity's buttons that change a recording through the hub do
/// nothing. With the module on nothing changes.
@MainActor struct RecordingOuterSurfacesTests {
    @Test func aStartIsRefusedInWordsWithTheModuleOff() {
        #expect(MeetingRecorder.startRefusal(.on) == nil)
        #expect(MeetingRecorder.startRefusal(.off) == String(localized: "Meeting recording is off on this hub."))
    }

    /// What Siri and Shortcuts say: the refusal is the intent's failure, word for word.
    @Test func theIntentsFailureSaysTheRefusal() throws {
        let refusal = try #require(MeetingRecorder.startRefusal(.off))
        #expect(RecordingIntentFailure(message: refusal).errorDescription == refusal)
        #expect(MeetingRecorder.Outcome.failed(refusal) != .started)
    }

    @Test func theLiveActivitysChangesFollowTheModule() {
        #expect(MeetingRecorder.changesOffered(.on))
        #expect(!MeetingRecorder.changesOffered(.off))
    }

    @Test func theQuickActionMenuLeavesStartMeetingOut() {
        #expect(QuickAction.offered(recording: .on) == QuickAction.allCases)
        #expect(QuickAction.offered(recording: .off) == [.newConversation, .inbox])
    }

    @Test func noMeetingIsRemindedOfWithTheModuleOff() {
        #expect(MeetingReminders.reminds(enabled: true, recording: .on))
        #expect(!MeetingReminders.reminds(enabled: false, recording: .on))
        #expect(!MeetingReminders.reminds(enabled: true, recording: .off))
    }

    /// Nothing records here, so the Live Activity's buttons had nothing to act on before either: they stay harmless.
    @Test func withNothingRecordingTheButtonsDoNothing() {
        MeetingRecorder.shared.ask(recordingID: UUID().uuidString)
        MeetingRecorder.shared.addMark(recordingID: UUID().uuidString)
        MeetingRecorder.shared.talk(recordingID: UUID().uuidString, open: true)
        #expect(MeetingRecorder.shared.setRole(.off, recordingID: UUID().uuidString))
    }
}
