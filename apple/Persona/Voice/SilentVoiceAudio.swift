#if DEBUG
import Foundation

/// `-openVoice silent` (docs/assistant/voice.md 6.8; the voice module's `VoiceSample`): a call gets this audio instead of
/// the microphone and the speaker. In the core because a call to Persona (`OwnerCall`) and the page's start
/// (`VoiceEntry`) take it too.
///
/// No microphone (it sends nothing up; the extension hears silence) and no speaker: the voice's PCM is counted, and
/// it says it plays for as long as that PCM would sound, so the bar's Speaking and Interrupt Voice work as with a speaker.
@MainActor final class SilentVoiceAudio: VoiceAudio {
    /// The launch asked for it.
    nonisolated static var isRequested: Bool { UserDefaults.standard.string(forKey: "openVoice") == "silent" }

    private var events: (@MainActor (VoiceAudioEvent) -> Void)?
    private var rate = VoiceFrame.defaultOutputRate
    private var until = Date.distantPast
    private var quiet: Task<Void, Never>?

    func start(_ format: VoiceAudioFormat, uplink: VoiceUplink, events: @escaping @MainActor (VoiceAudioEvent) -> Void) throws {
        rate = format.outputRate
        self.events = events
    }

    func reconfigure(_ format: VoiceAudioFormat) throws { rate = format.outputRate }

    /// Nothing goes up, so nothing of the voice comes back.
    var cancelsEcho: Bool { true }

    func play(_ pcm: Data) {
        let now = Date.now
        if until < now { events?(.playing(true)) }
        until = max(until, now).addingTimeInterval(Double(pcm.count / 2) / Double(max(rate, 1)))
        events?(.outputLevel(0.5))
        quiet?.cancel()
        let left = until.timeIntervalSince(now)
        quiet = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(left))
            guard !Task.isCancelled, let self else { return }
            self.events?(.outputLevel(0))
            self.events?(.playing(false))
        }
    }

    func cue(_ pcm: Data) {}

    func clear() {
        quiet?.cancel()
        if until > .now { events?(.playing(false)) }
        until = .distantPast
    }

    func stop() {
        quiet?.cancel()
        events = nil
    }
}
#endif
