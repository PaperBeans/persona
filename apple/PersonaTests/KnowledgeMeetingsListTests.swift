import Foundation
import Testing
@testable import Persona
import PersonaWire

/// One home for meetings (#8731, "S8"): Knowledge lists recordings under Meetings, beside the meetings imported from
/// connected apps, newest first; notes and all-day recordings carry a pill; recordings have no chip of their own.
@MainActor
struct KnowledgeMeetingsListTests {
    private func entry(_ kind: KBKind, _ id: String, updated: Date? = nil, recordingKind: String? = nil) -> KBEntry {
        KBEntry(kind: kind.rawValue, id: id, name: id, relevance: 0, updated: updated, recordingKind: recordingKind)
    }

    @Test func recordingsHaveNoChipAndOpenAsMeetings() {
        #expect(!KBKind.listed.contains(.recordings))
        #expect(KBKind.listed.contains(.meetings))
        #expect(KBKind.recordings.listedAs == .meetings)
        #expect(KBKind.people.listedAs == .people)
    }

    @Test func theListsMergeWithEachTagOnce() {
        let meetings = KBList(items: [entry(.meetings, "m1")], tags: ["周会", "客户"])
        let recordings = KBList(items: [entry(.recordings, "r1")], tags: ["周会", "旅行"])
        let merged = KBList.meetings(meetings, recordings: recordings)
        #expect(merged.items.map(\.id) == ["m1", "r1"])
        #expect(merged.tags == ["周会", "客户", "旅行"])
        #expect(KBList.meetings(KBList(items: []), recordings: KBList(items: [])).tags == [])
    }

    @Test func newestFirstWithUndatedLastInTheirOrder() {
        let old = Date(timeIntervalSince1970: 1_700_000_000), new = old.addingTimeInterval(86_400)
        let list = [entry(.meetings, "undated-a"), entry(.meetings, "old", updated: old), entry(.recordings, "new", updated: new),
                    entry(.meetings, "undated-b")]
        #expect(KBList.newestFirst(list).map(\.id) == ["new", "old", "undated-a", "undated-b"])
    }

    @Test func onlyNotesAndAllDayRecordingsCarryAPill() {
        #expect(entry(.recordings, "n", recordingKind: "note").recordingTag == String(localized: "Note [Recording Kind]"))
        #expect(entry(.recordings, "d", recordingKind: "day").recordingTag == String(localized: "All-Day"))
        #expect(entry(.recordings, "m", recordingKind: "meeting").recordingTag == nil)
        #expect(entry(.recordings, "older-hub").recordingTag == nil)
        #expect(entry(.meetings, "imported", recordingKind: "note").recordingTag == nil)
    }

    @Test func theWireKindDecodesAndALocalRecordingIsItsID() throws {
        let id = "0a1b2c3d-0000-4000-8000-000000000001"
        let json = #"{"kind":"recordings","id":"\#(id)","name":"周会复盘","relevance":0,"recording_kind":"note"}"#
        let e = try JSON.decoder.decode(KBEntry.self, from: Data(json.utf8))
        #expect(e.recordingKind == "note")
        #expect(e.localRecording == UUID(uuidString: id))
        #expect(entry(.meetings, id).localRecording == nil)
    }
}
