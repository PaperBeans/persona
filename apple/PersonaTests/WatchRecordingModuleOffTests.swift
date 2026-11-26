import Foundation
import Testing
@testable import Persona

/// The Apple Watch follows the hub's recording module through the iPhone (#15214, docs/apple/recording.md「With the hub's
/// recording module off (#15214)」): the phone's word travels in the WatchConnectivity application context as one new
/// key, a closed type with an unknown case on both sides. Absent, allowed and unknown leave the watch as it was; only
/// `off` takes the starts and the marks away, and a recording that already runs can still be stopped.
@MainActor struct WatchRecordingModuleOffTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func defaults() throws -> (UserDefaults, String) {
        let name = "watch-recording-module-\(UUID().uuidString)"
        return (try #require(UserDefaults(suiteName: name)), name)
    }

    // MARK: The payload, in the four compatibility cases

    /// New phone, new watch: the phone writes its word under the one new key, and the watch reads the same value back.
    @Test func newPhoneAndNewWatchAgree() {
        #expect(RecordingAllowance.on.forWatch == .allowed && RecordingAllowance.off.forWatch == .off)
        for allowance in [PhoneRecordingAllowance.allowed, .off] {
            let ctx = WatchSync.capabilityContext(day: nil, recording: allowance)
            #expect(ctx[PhoneRecordingAllowance.contextKey] as? String == allowance.wire)
            #expect(PhoneRecordingAllowance(context: ctx[PhoneRecordingAllowance.contextKey]) == allowance)
        }
        #expect(PhoneRecordingAllowance.contextKey == "recordingModule", "the key is the contract: never renamed")
        #expect(PhoneRecordingAllowance.allowedWire == "allowed" && PhoneRecordingAllowance.offWire == "off")
    }

    /// Old phone, new watch: the key is absent, which means allowed (as no hello means the module is on).
    @Test func anAbsentKeyMeansAllowed() throws {
        #expect(PhoneRecordingAllowance(context: nil) == .allowed)
        #expect(PhoneRecordingAllowance(wire: nil) == .allowed)
        let (store, name) = try defaults()
        defer { store.removePersistentDomain(forName: name) }
        #expect(PhoneRecordingAllowance.stored(in: store) == .allowed, "before any phone said anything")
        // The old phone's context: the keys it always sent, nothing about the module.
        let old: [String: Any] = ["recordingRolling": true, RecordingDayContract.phoneKey: 1, "summary": Data()]
        #expect(WatchPhoneCapabilities.storeRecording(old, full: true, in: store) == 0)
        #expect(store.object(forKey: PhoneRecordingAllowance.storeKey) == nil)
        #expect(WatchRecordingOffers(.stored(in: store)) == WatchRecordingOffers(.allowed))
    }

    /// A word from a newer phone that this build doesn't know, or a value that is no string: kept as unknown, treated
    /// as allowed, never as off, and never a failure.
    @Test func anUnknownValueIsAllowedNeverOff() throws {
        let newer = PhoneRecordingAllowance(context: "read_only")
        #expect(newer == .unknown("read_only") && newer.offersWrites && newer.wire == "read_only")
        for odd in [true, false, 0, 2, Data(), ["off"], NSNull(), ""] as [Any] {
            let read = PhoneRecordingAllowance(context: odd)
            #expect(read != .off && read.offersWrites, "\(odd)")
            #expect(WatchRecordingOffers(read) == WatchRecordingOffers(.allowed), "\(odd)")
        }
        #expect(PhoneRecordingAllowance(context: "OFF") == .unknown("OFF"), "only the exact word is off")
        let (store, name) = try defaults()
        defer { store.removePersistentDomain(forName: name) }
        #expect(WatchPhoneCapabilities.storeRecording([PhoneRecordingAllowance.contextKey: "read_only"], full: true, in: store) == 1)
        #expect(PhoneRecordingAllowance.stored(in: store) == .unknown("read_only"), "kept as it came")
        #expect(WatchPhoneCapabilities.storeRecording([PhoneRecordingAllowance.contextKey: false], full: true, in: store) == 1)
        #expect(PhoneRecordingAllowance.stored(in: store).offersWrites)
    }

    /// New phone, old watch: the new key is one more entry beside the ones an older watch reads, all of which keep
    /// their names and values (the old watch ignores a key it doesn't know; it cannot hide its buttons).
    @Test func theOldShapeIsUntouched() throws {
        let day = RecordingDayCapabilities(consent: true, hub: true, keepDays: 7)
        let before = WatchSync.capabilityContext(day: day)
        let after = WatchSync.capabilityContext(day: day, recording: .off)
        #expect(Set(after.keys) == Set(before.keys).union([PhoneRecordingAllowance.contextKey]), "one key added, none renamed")
        #expect(after["recordingRolling"] as? Bool == true && day.isIn(after))
        #expect(Set(WatchSync.capabilityContext(day: nil).keys) == ["recordingRolling"], "nothing said before the app knows")
        // What an older watch does with the new context: the store it has (`WatchPhoneCapabilities.store`, unchanged)
        // reads the keys it knows and writes the same values with or without the new key.
        let (old, oldName) = try defaults()
        let (new, newName) = try defaults()
        defer { old.removePersistentDomain(forName: oldName); new.removePersistentDomain(forName: newName) }
        #expect(WatchPhoneCapabilities.store(before, full: true, in: old) == WatchPhoneCapabilities.store(after, full: true, in: new))
        for key in [WatchPhoneCapabilities.rollingKey, WatchPhoneCapabilities.dayKey, WatchPhoneCapabilities.dayHubKey, WatchPhoneCapabilities.keepDaysKey] {
            #expect(old.object(forKey: key) as? NSObject == new.object(forKey: key) as? NSObject, "\(key)")
        }
        #expect(new.object(forKey: PhoneRecordingAllowance.storeKey) == nil, "the old store never writes the new word")
    }

    /// The watch keeps the phone's last word, in its own defaults and the App Group's (the complication), writes only
    /// on a change, and a full context without the key (the phone went back to an older build, or another phone)
    /// returns it to allowed. A pairing push or a reply, which carry part of the context, remove nothing.
    @Test func theWatchKeepsTheLastWordAndFollowsEveryChange() throws {
        let (store, name) = try defaults()
        let (group, groupName) = try defaults()
        defer { store.removePersistentDomain(forName: name); group.removePersistentDomain(forName: groupName) }
        let off: [String: Any] = ["recordingRolling": true, PhoneRecordingAllowance.contextKey: "off"]
        #expect(WatchPhoneCapabilities.storeRecording(off, full: true, in: store, group: group) == 1)
        #expect(PhoneRecordingAllowance.stored(in: store) == .off && PhoneRecordingAllowance.stored(in: group) == .off)
        #expect(WatchPhoneCapabilities.storeRecording(off, full: true, in: store, group: group) == 0, "the same context again")
        #expect(WatchPhoneCapabilities.storeRecording(["configs": Data()], full: false, in: store, group: group) == 0)
        #expect(PhoneRecordingAllowance.stored(in: store) == .off, "a partial context removes nothing")
        // The phone picked another hub, or its hub's module came back.
        let on: [String: Any] = ["recordingRolling": true, PhoneRecordingAllowance.contextKey: "allowed"]
        #expect(WatchPhoneCapabilities.storeRecording(on, full: true, in: store, group: group) == 1)
        #expect(PhoneRecordingAllowance.stored(in: store) == .allowed && PhoneRecordingAllowance.stored(in: group) == .allowed)
        #expect(WatchPhoneCapabilities.storeRecording(off, full: true, in: store, group: group) == 1)
        #expect(WatchPhoneCapabilities.storeRecording(["recordingRolling": true], full: true, in: store, group: group) == 1)
        #expect(store.object(forKey: PhoneRecordingAllowance.storeKey) == nil && PhoneRecordingAllowance.stored(in: group) == .allowed)
        #expect(WatchSync.phoneRecordingKey == PhoneRecordingAllowance.storeKey)
        #expect(PhoneRecordingAllowance.stored(in: nil) == .allowed, "no App Group (an unsigned build): the face stays as it was")
    }

    // MARK: What the watch offers

    /// Off: no start and no marks, and the line; what stops the microphone or erases the user's own audio stays.
    /// Allowed, absent and unknown: everything, and no line.
    @Test func controlsAreHiddenOnlyWhenOff() {
        let off = WatchRecordingOffers(.off)
        #expect(!off.starts && !off.marks && off.saysOff && off.stopsAndErases)
        for same in [PhoneRecordingAllowance.allowed, .unknown("later"), PhoneRecordingAllowance(context: nil)] {
            let offers = WatchRecordingOffers(same)
            #expect(offers.starts && offers.marks && !offers.saysOff && offers.stopsAndErases, "\(same)")
        }
    }

    /// All-day recording: nothing starts or continues from stopped while off, whoever asks (the phone's switch
    /// included); the mode leaves the home list unless a wish to record is left to end; no notification prompt.
    @Test func allDayOffersNoStartWhenOff() {
        for state in [AllDaySnapshot.State.off, .stopped] {
            for phoneDay in [nil, 0, 1] as [Int?] {
                let access = AllDayAlertPolicy.access(phoneDay: phoneDay, state: state, recording: .off)
                #expect(access == .moduleOff, "\(state) \(String(describing: phoneDay))")
                #expect(!AllDayAlertPolicy.mayStart(access, askedByPhone: false) && !AllDayAlertPolicy.mayStart(access, askedByPhone: true))
                #expect(!AllDayAlertPolicy.asksForNotifications(access, onPage: true) && !AllDayAlertPolicy.asksForNotifications(access, onPage: false))
            }
        }
        #expect(!AllDayAlertPolicy.listed(.moduleOff, state: .off), "nothing to start: not on the home list")
        #expect(AllDayAlertPolicy.listed(.moduleOff, state: .stopped), "Stop for Today still ends the wish")
    }

    /// A recording that already runs when the module goes off is not lost: its page keeps its controls (the access it
    /// started with), so it can be paused, continued, stopped and saved; only the marks go (`WatchRecordingOffers`).
    @Test func aRunningRecordingCanStillBeStopped() {
        for state in [AllDaySnapshot.State.recording, .paused, .interrupted] {
            #expect(AllDayAlertPolicy.access(phoneDay: 1, state: state, recording: .off) == .ready, "\(state)")
            #expect(AllDayAlertPolicy.access(phoneDay: nil, state: state, recording: .off) == .ready, "\(state)")
        }
        #expect(WatchRecordingOffers(.off).stopsAndErases && !WatchRecordingOffers(.off).marks)
    }

    /// Allowed, absent and unknown: the policy answers exactly as before the allowance existed.
    @Test func allDayIsUnchangedWhenAllowed() {
        let states: [AllDaySnapshot.State] = [.off, .recording, .paused, .interrupted, .stopped]
        for state in states {
            for phoneDay in [nil, 0, 1, 2] as [Int?] {
                let before = AllDayAlertPolicy.access(phoneDay: phoneDay, state: state)
                for same in [PhoneRecordingAllowance.allowed, .unknown("later")] {
                    #expect(AllDayAlertPolicy.access(phoneDay: phoneDay, state: state, recording: same) == before)
                }
                #expect(before != .moduleOff)
                #expect(AllDayAlertPolicy.listed(before, state: state) == (before != .hidden))
                #expect(AllDayAlertPolicy.mayStart(before, askedByPhone: true))
                #expect(AllDayAlertPolicy.mayStart(before, askedByPhone: false) == (before == .ready))
                #expect(AllDayAlertPolicy.asksForNotifications(before, onPage: true) == (before != .hidden))
                #expect(AllDayAlertPolicy.asksForNotifications(before, onPage: false) == (before == .ready))
            }
        }
    }

    /// The All-Day complication with the module off: the neutral face it already has before the app wrote anything
    /// (the mode's name, no "Tap to start"), whose tap opens the page; never a start or a continue. A recording that
    /// runs keeps its face.
    @Test func theAllDayFaceOffersNoStartWhenOff() {
        let off = AllDaySnapshot(state: .off, updatedAt: now, access: .moduleOff).face(at: now)
        #expect(off == .off(.moduleOff) && off.link == .open)
        let stopped = AllDaySnapshot(state: .stopped, since: now, updatedAt: now, access: .moduleOff).face(at: now)
        #expect(stopped == .off(.moduleOff) && stopped.link == .open, "nothing continues it: no Tap to continue")
        let running = AllDaySnapshot(state: .recording, since: now, updatedAt: now, access: .ready).face(at: now)
        #expect(running == .recording(since: now))
        // The word reaching the face is a change it redraws for, both ways.
        let ready = AllDaySnapshot(state: .off, updatedAt: now, access: .ready)
        let gone = AllDaySnapshot(state: .off, updatedAt: now, access: .moduleOff)
        #expect(gone.redraws(over: ready) && ready.redraws(over: gone) && !gone.redraws(over: gone))
        // Unchanged with the module on.
        #expect(ready.face(at: now) == .off(.ready) && ready.face(at: now).link == .start)
        #expect(AllDaySnapshot(state: .stopped, updatedAt: now, access: .ready).face(at: now) == .stopped(startable: true))
        #expect(AllDaySnapshot(state: .stopped, updatedAt: now, access: .needsConsent).face(at: now) == .stopped(startable: false))
        // The snapshot file with the new word round-trips (the app and its face ship together).
        let data = try? JSONEncoder().encode(gone)
        #expect(data.flatMap { try? JSONDecoder().decode(AllDaySnapshot.self, from: $0) } == gone)
    }

    // MARK: The relayed minutes

    /// The hub's `paused` rides with minutes still being written, and the watch reads Paused for the module-off
    /// reason only; ready and failed minutes never carry it.
    @Test func pausedMinutesAreRelayed() {
        let entry = RecordingEntry(id: UUID(), createdAt: now, source: .phone, title: "Weekly")
        let held = MinutesRelay.short(entry, minutes: nil, status: "pending", paused: .moduleOff)
        #expect(held.state == .writing && held.paused == .moduleOff && held.isPaused)
        let writing = MinutesRelay.short(entry, minutes: nil, status: "pending")
        #expect(writing.paused == nil && !writing.isPaused, "nothing holds them: still Writing")
        #expect(!MinutesRelay.short(entry, minutes: nil, status: "pending", paused: .unknown("quota")).isPaused, "a reason this build doesn't know")
        #expect(MinutesRelay.short(entry, minutes: nil, status: "failed", paused: .moduleOff).paused == nil)
        var minutes = RecordingMinutes()
        minutes.points = [RecordingMinutes.Item(text: "One")]
        let ready = MinutesRelay.short(entry, minutes: minutes, status: "ready", paused: .moduleOff)
        #expect(ready.state == .ready && ready.paused == nil && !ready.isPaused)
    }

    /// While this phone's hub has the module off, minutes still on their way go as paused whatever the hub said (a
    /// recording that never reached it has no answer); with the module on every meeting goes exactly as it is.
    @Test func thePhonesOwnAllowanceMarksWaitingMinutes() {
        let writing = WatchMinutes.Meeting(id: UUID(), title: "Weekly", date: now, state: .writing)
        let ready = WatchMinutes.Meeting(id: UUID(), title: "Weekly", date: now, state: .ready, points: ["One"])
        let failed = WatchMinutes.Meeting(id: UUID(), title: "Weekly", date: now, state: .failed)
        #expect(MinutesRelay.relayed(writing, recording: .off).isPaused)
        #expect(MinutesRelay.relayed(ready, recording: .off) == ready && MinutesRelay.relayed(failed, recording: .off) == failed)
        for meeting in [writing, ready, failed] { #expect(MinutesRelay.relayed(meeting, recording: .on) == meeting) }
        var other = writing
        other.paused = .unknown("quota")
        #expect(MinutesRelay.relayed(other, recording: .off).paused == .unknown("quota"), "the hub's own reason is kept")
    }

    /// The minutes' payload both ways: with nothing paused the bytes carry no new key (an older watch sees what it
    /// always did); a payload from an older phone decodes with nothing paused; a newer reason neither fails the
    /// decode nor reads as paused; and the paused field round-trips.
    @Test func theMinutesPayloadIsCompatibleBothWays() throws {
        let id = try #require(UUID(uuidString: "00000000-0000-0000-0000-000000000005"))
        let plain = WatchMinutes(meetings: [WatchMinutes.Meeting(id: id, title: "Weekly", date: now, state: .writing)])
        let bytes = try #require(plain.data)
        #expect(!String(decoding: bytes, as: UTF8.self).contains("paused"), "module on: the old shape, byte for byte")
        // What an older phone sends: no `paused`.
        let fromOld = try #require(WatchMinutes(data: bytes))
        #expect(fromOld == plain && fromOld.meetings[0].paused == nil && !fromOld.meetings[0].isPaused)
        // New phone, new watch.
        var held = plain
        held.meetings[0].paused = .moduleOff
        let heldBytes = try #require(held.data)
        #expect(String(decoding: heldBytes, as: UTF8.self).contains("\"paused\":\"module_off\""))
        #expect(WatchMinutes(data: heldBytes) == held && WatchMinutes(data: heldBytes)?.meetings[0].isPaused == true)
        // New phone, old watch: the state is still one the old watch knows (`writing`), so its decode of the list
        // does not fail; it ignores the extra key and says Writing, as today.
        #expect(held.meetings[0].state == .writing)
        #expect(WatchMinutes.Meeting.State(rawValue: "paused") == nil, "never a new state an older watch would fail on")
        // A reason from a newer phone or hub, and a value that is no string.
        for odd in ["\"quota\"", "7", "{\"why\":\"x\"}", "null"] {
            let json = String(decoding: heldBytes, as: UTF8.self).replacingOccurrences(of: "\"module_off\"", with: odd)
            let read = try #require(WatchMinutes(data: Data(json.utf8)), "\(odd)")
            #expect(read.meetings.count == 1 && !read.meetings[0].isPaused, "\(odd)")
        }
    }
}
