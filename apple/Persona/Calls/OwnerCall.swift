#if os(iOS)
import AVFoundation
import Foundation
import Intents
import UIKit

// Placing a call to Persona (#9290): the call page in "Calling…" at once, then what can refuse it (an urgent call, a
// recording, no hub, no voice extension, the microphone), the main conversation and its voice extension as CarPlay
// finds them but on the selected hub, the system's Start, and the voice once the system started the call. A redial
// from the Phone app's Recents comes here as an `INStartCallIntent` (or `INStartAudioCallIntent`) user activity.

extension PersonaCalls {
    /// Calls Persona on the main conversation of `group`'s hub (nil: the selected hub). A call already on is shown
    /// instead. Refused with the page's note (and the page goes); when the system will not place the call, a voice chat
    /// in the app instead, with a line that says why.
    func callPersona(group: String? = nil, origin: CallOrigin = .app) async {
        let model = AppModel.shared
        if origin != .app { await Self.clearTheWay(model) }
        if let live = ownerCall, live.phase.isLive {
            presented = true
            return
        }
        // Feedback first, before anything slow.
        let call = add(kind: .owner(group: group ?? model.hub?.group ?? ""), direction: .outgoing)
        presented = true
        Self.log.notice("call \(call.id.uuidString, privacy: .public): calling Persona (\(origin.rawValue, privacy: .public))")
        // From Recents or Siri the app may have just launched: its hubs come first.
        if origin != .app { await Self.waitForHub(group) }
        guard call.phase.isLive else { return }
        if let note = admission() { return refuse(call, note: note) }
        guard let group = group ?? model.hub?.group, let api = model.api(group) else { return refuse(call, note: Self.noHubNote) }

        let key: SessionKey
        if let main = model.assistantMain, main.group == group {
            key = main
        } else {
            do {
                guard let confirmed = try await api.assistant().mainKey(group: api.group) else { return refuse(call, note: Self.noMainNote) }
                key = confirmed
            } catch {
                Self.log.notice("call \(call.id.uuidString, privacy: .public): the main conversation: \(error.localizedDescription, privacy: .public)")
                return refuse(call, note: Self.unreachableNote)
            }
        }
        // The same checks as the chat's Voice Chat, so the reasons and Open Settings match (`VoiceEntry`). It waits for
        // the group's probe even when the chat's page asked first (a cold launch from Recents).
        await VoiceSupport.shared.refresh(group: group, api: api)
        guard call.phase.isLive else { return }
        let availability = model.session(key).map { VoiceSupport.shared.availability(for: $0, group: group) }
            ?? VoiceSupport.shared.extensionAvailability(group: group)
        guard case .available(let extensionID) = availability else {
            return refuse(call, note: VoiceUnavailableNote.make(availability, extensionID: VoiceSupport.shared.voiceExtensionID(group: group))
                          ?? Self.unreachableNote)
        }
        // The microphone asked for here, in the app: a locked phone mid-call cannot show the prompt.
        switch AVAudioApplication.shared.recordPermission {
        case .granted: break
        case .undetermined:
            guard await AVAudioApplication.requestRecordPermission() else { return refuse(call, note: Self.microphoneNote) }
        default:
            return refuse(call, note: Self.microphoneNote)
        }
        guard call.phase.isLive else { return }
        // An urgent call or a recording may have come while the hub, the extension or the microphone prompt were asked.
        if let note = admission() { return refuse(call, note: note) }

        // An in-app voice chat ends first: its audio would end the call's.
        endVoiceChat(for: .replaced)
        // The call's category before Start (the system activates the session; the app never does).
        await prepareAudio(call)
        let recent = VoiceLine.recent(model.chat(key).items)
        let ownerDeps = ownerDeps
        let engine = OwnerCallEngine(key: key, extensionID: extensionID, recent: recent, audio: ownerAudio(), calls: voiceCalls,
                                     deps: { audio, events in ownerDeps(key.group, audio, events) })
        attach(engine, to: call)
        await place(call, origin: origin) { [weak self] why in
            self?.chatInstead(key, extensionID: extensionID, recent: recent, why: why)
        }
    }

    /// Asks the system to start the call. Too early (the app still coming to the front): once more half a second later.
    /// Refused otherwise: `instead` (the in-app voice chat), and the call ends.
    func place(_ call: PersonaCall, origin: CallOrigin, instead: (@MainActor (CallSystemError) -> Void)? = nil) async {
        if origin != .app { await Self.waitUntilActive() }
        let system = system
        var again = true
        while call.phase.isLive {
            // Asked again just before each Start: an urgent call that rang meanwhile goes first.
            if let note = admission() { return refuse(call, note: note) }
            do {
                try await system.start(call.id)
                // Hung up while the system took it: its call ends too.
                if call.phase.isEnded { try? await system.perform(.end, for: call.id) }
                return
            } catch {
                let why = CallSystemError(error)
                Self.log.notice("call \(call.id.uuidString, privacy: .public): the system did not start it: \(why.description, privacy: .public)")
                if why.isTooEarly, again {
                    again = false
                    try? await clock.sleep(for: .milliseconds(500))
                    continue
                }
                guard call.phase.isLive else { return }
                call.note = Self.insteadLine(why)
                dispatch(call, .settled(.failed))
                instead?(why)
                return
            }
        }
    }

    /// What refuses a call to Persona now: an urgent call ringing or on (its audio is the system's), a recording or
    /// dictation on the microphone. Asked first and again after every wait before the system's Start.
    func admission() -> VoiceUnavailableNote? {
        if urgentInProgress { return Self.urgentNote }
        if voiceCalls.isRecording(RecordingMicrophone.shared) { return Self.recordingNote }
        return nil
    }

    /// The system would not place the call: the voice chat in the app on the main conversation, its page open.
    private func chatInstead(_ key: SessionKey, extensionID: String, recent: [VoiceLine], why: CallSystemError) {
        let model = AppModel.shared
        model.toast = Toast(Self.insteadLine(why))
        presented = false
        model.open(key)
        let calls = voiceCalls
        Task { await calls.start(key, extensionID: extensionID, surface: .phone, recent: recent, deps: .live(group: key.group)) }
    }

    nonisolated static func insteadLine(_ why: CallSystemError) -> String {
        why.isAtCapacity ? String(localized: "Another call is on, so this is a voice chat in the app instead")
            : String(localized: "This device didn't place the call, so this is a voice chat in the app instead")
    }

    // MARK: Recents

    /// The user activities a redial comes as (Recents may hand either).
    nonisolated static let activityTypes = ["INStartCallIntent", "INStartAudioCallIntent"]

    nonisolated static func isCall(_ activity: NSUserActivity) -> Bool {
        activityTypes.contains(activity.activityType) || activity.interaction?.intent is INStartCallIntent
    }

    /// The handle the redial names (`CallHandle.persona`, or whatever an older entry had): any calls the current hub's
    /// main conversation.
    nonisolated static func handle(_ activity: NSUserActivity) -> String? {
        (activity.interaction?.intent as? INStartCallIntent)?.contacts?.first?.personHandle?.value
    }

    /// A redial from Recents: calls Persona once the app is in front. True when the activity was one. The same tap can
    /// come twice (the scene's delegate and SwiftUI's `onContinueUserActivity`): once is taken.
    @discardableResult func continueActivity(_ activity: NSUserActivity) -> Bool {
        guard Self.isCall(activity) else { return false }
        let now = clock.now
        if let last = lastRedial, now.timeIntervalSince(last) < 2 { return true }
        lastRedial = now
        Self.log.notice("call: a redial (\(activity.activityType, privacy: .public), handle \(Self.handle(activity) == CallHandle.persona ? "Persona" : "other", privacy: .public))")
        Task { await callPersona(origin: .recents) }
        return true
    }

    // MARK: Waiting

    /// From Siri, Shortcuts or Recents the app comes to the front on whatever page it was left on. The call's page and a
    /// refusal's alert go over the app's root (`personaCallCover`), which presents nothing while the shell's Settings
    /// sheet is up: that sheet closes, as it does for a notification's chat (#3818), and the page waits for it to have
    /// gone (at most a second). Any other page stays under the call: Minimize goes back to it, with the bar to the call.
    static func clearTheWay(_ model: AppModel) async {
        guard model.shellSettings else { return }
        model.shellSettings = false
        for _ in 0..<20 where rootPresents {
            try? await Task.sleep(for: .milliseconds(50))
        }
    }

    /// Something is still up over the app's root (the closing sheet until its animation ends).
    private static var rootPresents: Bool {
        UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            .filter { $0.session.role == .windowApplication }
            .contains { ($0.keyWindow ?? $0.windows.first)?.rootViewController?.presentedViewController != nil }
    }

    /// A launch from Recents: the hubs read and the main conversation known (at most 8 s), so the call goes to them.
    private static func waitForHub(_ group: String?) async {
        let model = AppModel.shared
        for _ in 0..<32 {
            if let g = group ?? model.hub?.group, model.api(g) != nil, model.assistantMain != nil { return }
            try? await Task.sleep(for: .milliseconds(250))
        }
    }

    /// The system refuses a Start while the app is still coming to the front (at most 5 s).
    private static func waitUntilActive() async {
        for _ in 0..<50 where UIApplication.shared.applicationState != .active {
            try? await Task.sleep(for: .milliseconds(100))
        }
    }

    // MARK: Notes

    static var urgentNote: VoiceUnavailableNote { .urgentCall }

    static var recordingNote: VoiceUnavailableNote {
        VoiceUnavailableNote(title: String(localized: "Finish the recording or dictation first"),
                             message: String(localized: "A recording or dictation is using the microphone."), settings: nil)
    }

    static var noHubNote: VoiceUnavailableNote {
        VoiceUnavailableNote(title: String(localized: "Not paired with a hub yet"),
                             message: String(localized: "Pair this device with a hub to call Persona."), settings: nil)
    }

    static var noMainNote: VoiceUnavailableNote {
        VoiceUnavailableNote(title: String(localized: "Persona's main chat isn't set up yet"),
                             message: String(localized: "Open Persona's chat once, then call again."), settings: nil)
    }

    static var unreachableNote: VoiceUnavailableNote {
        VoiceUnavailableNote(title: String(localized: "Can't reach the hub"),
                             message: String(localized: "Try again once connected to the hub."), settings: nil)
    }

    static var microphoneNote: VoiceUnavailableNote {
        VoiceUnavailableNote(title: String(localized: "Microphone access is needed"),
                             message: String(localized: "Allow Persona to use the microphone in Settings, then call again."), settings: .system)
    }
}

extension VoiceCalls {
    /// A recording or dictation has the microphone (not a voice chat, which gives it way to a call).
    func isRecording(_ microphone: RecordingMicrophone) -> Bool {
        guard !microphone.isFree else { return false }
        guard let call = current, !call.phase.isEnded, call.surface != .call else { return true }
        return false
    }
}
#endif
