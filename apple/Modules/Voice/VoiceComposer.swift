import SwiftUI

/// Where the bar was, after a call ended by itself (docs/assistant/voice.md 1.2 L): Voice Ended, why, and Continue (or Retry), Open Settings,
/// Details as the ending allows. It stays until the page goes; Continue picks the call up with its captions and what it
/// handed over (`VoiceCalls.resume`), and the voice says what finished meanwhile.
struct VoiceEndedStrip: View {
    let call: VoiceConversation
    let note: VoiceEndNote
    @Environment(AppModel.self) private var model
    @State private var showDetails = false
    @State private var showModel = false
    /// Why Continue did not pick the call up (Persona's urgent call is on, #9290).
    @State private var refusal: VoiceUnavailableNote?

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.voiceChannel).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                (call.surface == .call ? Text("Call ended · \(note.text)") : Text("Voice ended · \(note.text)"))
                    .font(Theme.Font.secondary)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .accessibilityIdentifier("voice-ended-text")
                Spacer(minLength: 0)
            }
            HStack(spacing: Theme.Space.m) {
                if let again = note.again {
                    Button {
                        Haptics.tap()
                        #if os(iOS)
                        // A system call that ended is not picked up again: its conversation is called again (a new
                        // system call, #9887 one button).
                        if call.surface == .call {
                            let key = call.key
                            Task { await PersonaCalls.shared.callConversation(key) }
                            return
                        }
                        #endif
                        #if os(iOS)
                        // A call to Persona that is on: asked first, never ended by itself.
                        if let name = PersonaCalls.shared.liveOwnerCallName {
                            refusal = .endCallFirst(name, key: call.key, again: true)
                            return
                        }
                        #endif
                        Task { refusal = await VoiceCalls.shared.resume(call.key) }
                    } label: {
                        // Calling Persona again is Call Persona's glyph, not the in-app voice chat's.
                        Label(again == .resume ? String(localized: "Continue") : String(localized: "Retry [Components b8784c]"),
                              systemImage: call.surface == .call ? Theme.Icon.callPersona : Theme.Icon.voiceChannel)
                            .lineLimit(1)
                    }
                    .accessibilityIdentifier("voice-continue")
                }
                if let place = note.settings {
                    Button {
                        if case .model = place { showModel = true } else { VoiceEntry.open(place, model: model) }
                    } label: {
                        Label("Open Settings [Voice Entry 93112c]", systemImage: Theme.Icon.settings).lineLimit(1)
                    }
                    .accessibilityIdentifier("voice-settings")
                }
                if note.details != nil {
                    Button { showDetails = true } label: { Label("Details [Readiness Card 979a33]", systemImage: Theme.Icon.info).lineLimit(1) }
                        .accessibilityIdentifier("voice-details")
                }
                Spacer(minLength: 0)
            }
            .font(Theme.Font.secondaryStrong)
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("voice-ended")
        .onAppear {
            VoiceHaptics.play(note.haptic)
            AccessibilityNotification.Announcement(String(localized: "Voice Ended")).post()
        }
        .alert("Details [Readiness Card 979a33]", isPresented: $showDetails) {
            Button("OK [In App Tunnel View f867f3]", role: .cancel) {}
        } message: {
            Text(note.details ?? "")
        }
        .voiceUnavailableAlert($refusal, model: model)
        // The default assistant's: calls talk to its voice extension.
        .modelConfigurationSheet(isPresented: $showModel, purposeID: "plugin." + call.extensionID, owner: .defaultAssistant)
    }
}

/// When the composer asks the group's hub about voice again: another group, or the app back in front.
nonisolated struct VoiceProbeID: Hashable, Sendable {
    var group: String
    var active: Bool
}

/// The composer's side of voice (docs/assistant/voice.md 6.1, 6.8): asks the group's hub whether it has a voice extension that
/// speaks protocol 2 (3.1; at most once a minute, the entry keeps the last answer meanwhile, `VoiceSupport`), says why the
/// 「+」 menu's Voice Chat cannot start one, and in DEBUG the screenshot switches (`VoiceSample`).
struct ComposerVoice: ViewModifier {
    let key: SessionKey
    @Binding var note: VoiceUnavailableNote?
    /// The app is in front.
    let active: Bool
    /// The page offers a call now.
    let offered: () -> Bool
    let start: () -> Void
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        content
            .tabActiveTask(id: VoiceProbeID(group: key.group, active: active)) {
                guard active, VoicePlatform.current == .phone, let api = model.api(key.group) else { return }
                await VoiceSupport.shared.refresh(group: key.group, api: api)
            }
            .voiceUnavailableAlert($note, model: model)
            #if DEBUG
            .task {
                guard let request = VoiceSample.request else { return }
                try? await Task.sleep(for: .seconds(2)) // past the launch's back and forth: the page that stays takes it
                // The real path against the hub (the mock's MOCK_VOICE=1): as soon as the page offers a call.
                if request == .open { for _ in 0..<30 where !offered() && !Task.isCancelled { try? await Task.sleep(for: .seconds(1)) } }
                guard !Task.isCancelled, VoiceSample.claim() else { return }
                switch request {
                case .open: start()
                case .sample(.setup): note = VoiceSample.setupNote
                case .sample(let sample): VoiceSample.start(sample, key: key, model: model)
                }
            }
            #endif
    }
}
