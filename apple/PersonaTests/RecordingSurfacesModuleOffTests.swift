import Foundation
import Testing
@testable import Persona

/// What the iPhone's extensions draw while the hub's recording module is off (#15214, docs/apple/recording.md「With the
/// hub's recording module off (#15214)」): the Home Screen and Lock Screen widgets and the Control read the allowance
/// from the widgets' snapshot in the App Group, the recording's Live Activity from its content state. Both carry the
/// closed `PhoneRecordingAllowance`; absent and unknown mean allowed, and with the module on nothing is written.
@MainActor struct RecordingSurfacesModuleOffTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    /// The word in a file or a state: read leniently, written as it came.
    @Test func theAllowanceIsACodableWord() throws {
        struct Box: Codable, Equatable { var module: PhoneRecordingAllowance? }
        func read(_ json: String) throws -> Box { try JSONDecoder().decode(Box.self, from: Data(json.utf8)) }
        #expect(try read(#"{}"#).module == nil)
        #expect(try read(#"{"module":null}"#).module == nil)
        #expect(try read(#"{"module":"off"}"#).module == .off)
        #expect(try read(#"{"module":"allowed"}"#).module == .allowed)
        #expect(try read(#"{"module":"read_only"}"#).module == .unknown("read_only"))
        for odd in ["true", "0", "[\"off\"]", "{\"a\":1}"] {
            let box = try read("{\"module\":\(odd)}")
            #expect(box.module?.offersWrites == true && box.module != .off, "\(odd)")
        }
        let data = try JSONEncoder().encode(Box(module: .off))
        #expect(String(decoding: data, as: UTF8.self) == #"{"module":"off"}"#)
        #expect(try JSONDecoder().decode(Box.self, from: JSONEncoder().encode(Box(module: .unknown("later")))).module == .unknown("later"))
    }

    /// The widgets' snapshot: a file from before the field, one with the module on (nothing written) and one with a
    /// word this build doesn't know all offer the start; only `off` takes it away. The file's version is unchanged.
    @Test func theSnapshotOffersAStartUnlessOff() throws {
        var snapshot = HomeSnapshot(paired: true)
        #expect(snapshot.offersRecordingStart && snapshot.recordingModule == nil)
        let plain = try JSONEncoder().encode(snapshot)
        #expect(!String(decoding: plain, as: UTF8.self).contains("recordingModule"), "module on: the file as it was")
        #expect(try JSONDecoder().decode(HomeSnapshot.self, from: plain).offersRecordingStart, "a file from before the field")
        snapshot.recordingModule = .off
        let off = try JSONEncoder().encode(snapshot)
        let read = try JSONDecoder().decode(HomeSnapshot.self, from: off)
        #expect(read == snapshot && !read.offersRecordingStart && read.version == HomeSnapshot.version)
        let newer = String(decoding: off, as: UTF8.self).replacingOccurrences(of: "\"off\"", with: "\"later\"")
        let unknown = try JSONDecoder().decode(HomeSnapshot.self, from: Data(newer.utf8))
        #expect(unknown.offersRecordingStart && unknown.recordingModule == .unknown("later"))
        snapshot.recordingModule = .allowed
        #expect(snapshot.offersRecordingStart)
        #expect(HomeSnapshot.version == 1, "an optional field needs no bump")
    }

    /// The Live Activity's state: said only when off, so with the module on it is what it was; Bookmark and Ask / Talk
    /// are drawn unless off; a state from before the field and an unknown word draw them.
    @Test func theActivityStateSaysOffOnlyWhenOff() throws {
        var entry = RecordingEntry(id: UUID(), createdAt: now, source: .phone, title: "Weekly")
        entry.meeting = RecordingMeeting()
        let on = RecordingActivity.content(entry: entry, recording: true, interrupted: false, elapsed: 60, previous: nil, now: now)
        let same = RecordingActivity.content(entry: entry, recording: true, interrupted: false, elapsed: 60, previous: nil, now: now, module: .on)
        #expect(on == same && on.module == nil && on.offersChanges)
        let onBytes = try JSONEncoder().encode(on)
        #expect(!String(decoding: onBytes, as: UTF8.self).contains("module"), "module on: the state as it was")
        let off = RecordingActivity.content(entry: entry, recording: true, interrupted: false, elapsed: 60, previous: nil, now: now, module: .off)
        #expect(off.module == .off && !off.offersChanges && off != on, "a change of the allowance is a change of the state")
        var expected = on
        expected.module = .off
        #expect(off == expected, "nothing else differs")
        // Stale, it is still off; End / Stop are drawn either way (no field takes them away).
        #expect(off.drawn(stale: true).module == .off)
        typealias State = RecordingActivityAttributes.ContentState
        let offBytes = try JSONEncoder().encode(off)
        #expect(try JSONDecoder().decode(State.self, from: offBytes) == off)
        #expect(try JSONDecoder().decode(State.self, from: onBytes).offersChanges, "a state from before the field")
        let newer = String(decoding: offBytes, as: UTF8.self).replacingOccurrences(of: "\"off\"", with: "\"later\"")
        #expect(try JSONDecoder().decode(State.self, from: Data(newer.utf8)).offersChanges, "an unknown word")
    }
}
