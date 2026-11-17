import Foundation
import Testing
@testable import Persona

/// Who owns the app's one call (docs/assistant/voice.md 6.3, 6.5, 6.10「VoiceCalls 的所有权」): one at a time, another
/// conversation's call ends the one before, the background and leaving the page end only the phone's.
@MainActor
struct VoiceCallsTests {
    @Test func oneCallAtATimeAndAnotherConversationEndsTheFirst() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let first = try #require(calls.current)
        #expect(calls.isBound(to: rig.key))

        // The same conversation again: the same call, no second socket.
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(calls.current === first && rig.sockets.count == 1)

        let other = SessionKey(group: rig.key.group, machineID: "m2", sessionID: "s9")
        await calls.start(other, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(first.phase == .ended(.replaced))
        #expect(calls.current !== first && calls.isBound(to: other) && !calls.isBound(to: rig.key))
        #expect(rig.sockets.count == 2 && rig.sockets[0].closed)
        #expect(rig.sockets[1].start?["target"] as? [String: String] == ["machine_id": "m2", "session_id": "s9"])
    }

    @Test func theBackgroundEndsOnlyThePhonesCall() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .carPlay, deps: rig.deps)
        calls.appDidEnterBackground()
        #expect(calls.current?.phase == .connecting, "a call from the car goes on")

        let phone = VoiceRig()
        let phoneCalls = VoiceCalls()
        await phoneCalls.start(phone.key, extensionID: "voice", surface: .phone, deps: phone.deps)
        phoneCalls.appDidEnterBackground()
        let ended = try #require(phoneCalls.current)
        #expect(ended.phase == .ended(.background))
        #expect(phoneCalls.isBound(to: phone.key), "继续 stays on the page")
    }

    @Test func aSurfaceEndsOnlyItsOwnCallButTheUserEndsAny() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        calls.end(.left, from: .carPlay)
        #expect(calls.current?.phase == .connecting, "the car does not stop the phone's call")
        calls.end(.user, from: .carPlay)
        #expect(calls.current == nil, "结束 tapped ends it whoever started it, and nothing stays")
    }

    @Test func leavingThePageEndsItsCallAndItsContinueRow() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        calls.leave(SessionKey(machineID: "m1", sessionID: "other"))
        #expect(calls.isBound(to: rig.key), "another page leaving changes nothing")
        let call = calls.current
        calls.leave(rig.key)
        #expect(call?.phase == .ended(.left))
        #expect(calls.current == nil)
        #expect(rig.microphone.claim(UUID()))
    }

    @Test func leavingAPageDoesNotEndTheCarsCall() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .carPlay, deps: rig.deps)
        calls.leave(rig.key)
        #expect(calls.current?.phase == .connecting)
    }

    /// A call to Persona (#9290) goes on in the background and off the page; only its manager ends it.
    @Test func aSystemCallGoesOnInTheBackgroundAndOffThePage() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        calls.appDidEnterBackground()
        calls.leave(rig.key)
        calls.end(.left, from: .phone)
        #expect(calls.current === call && call.phase == .connecting)
        #expect(rig.socket?.closed == false)
    }

    /// The same conversation from another surface is a call of its own: a voice chat on the main conversation while a
    /// call to Persona is on it ends the call, not picks it up.
    @Test func anotherSurfaceOnTheSameConversationIsACallOfItsOwn() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let chat = try #require(calls.current)
        let call = rig.call(surface: .call)
        await calls.begin(call)
        #expect(chat.phase == .ended(.replaced) && calls.current === call)
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(call.phase == .ended(.replaced) && calls.current?.surface == .phone)
    }

    @Test func anEndedSystemCallIsNeverPickedUpAgain() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        call.end(.lost)
        await calls.resume(rig.key)
        #expect(call.phase == .ended(.lost) && rig.sockets.count == 1)
        calls.release(call)
        #expect(calls.current == nil)
    }

    /// A system call goes on when its page goes (it is `PersonaCalls`'); once it ended by itself its line (「通话已结束 · 继续」)
    /// stays on the page and goes with it (#9290).
    @Test func aSystemCallOutlivesItsPageAndItsEndedLineGoesWithIt() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        calls.leave(rig.key)
        #expect(calls.current === call && !call.phase.isEnded, "the page going leaves the system call on")
        call.end(.lost)
        #expect(calls.current === call, "its ended line stays while the page is there")
        calls.leave(rig.key, from: .carPlay)
        #expect(calls.current === call, "only the phone's page lets it go")
        calls.leave(rig.key)
        #expect(calls.current == nil)
    }

    /// A system call's voice ended before its start ran (the system ended the call at once): it never begins.
    @Test func aSystemCallsVoiceThatEndedBeforeItBeganNeverBegins() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        call.end(.user)
        await calls.begin(call)
        #expect(call.phase == .ended(.user) && calls.current == nil)
        #expect(rig.sockets.isEmpty && rig.audio.uplink == nil && rig.microphone.isFree)
    }

    /// While Persona's urgent call is on (#9290) nothing starts or is picked up again from any surface, and nothing is
    /// touched: the call before goes on (or stays ended), no audio opens.
    @Test func noVoiceChatUnderAnUrgentCall() async throws {
        let rig = VoiceRig()
        var urgent = false
        let calls = VoiceCalls()
        calls.urgentCallOn = { urgent }
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let first = try #require(calls.current)
        first.end(.interrupted)
        urgent = true
        let stops = rig.audio.stops
        for surface in [VoiceSurface.phone, .carPlay] {
            let other = SessionKey(group: rig.key.group, machineID: "m2", sessionID: "s9")
            #expect(await calls.start(other, extensionID: "voice", surface: surface, deps: rig.deps) == .urgentCall)
        }
        #expect(await calls.resume(rig.key) == .urgentCall)
        #expect(calls.current === first && first.phase == .ended(.interrupted))
        #expect(rig.sockets.count == 1 && rig.audio.uplink == nil && rig.audio.stops == stops)
        urgent = false
        #expect(await calls.resume(rig.key) == nil)
        #expect(first.phase == .connecting && rig.sockets.count == 2)
    }

    @Test func continueOnAnEndedCallPicksItUp() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        rig.socket?.ready(rig.key)
        rig.audio.emit(.interrupted)
        let call = try #require(calls.current)
        #expect(call.phase == .ended(.interrupted))
        await calls.resume(rig.key)
        #expect(calls.current === call && call.phase == .connecting && rig.sockets.count == 2)
        // Starting the same conversation again does the same.
        call.end(.lost)
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(calls.current === call && rig.sockets.count == 3)
    }
}
