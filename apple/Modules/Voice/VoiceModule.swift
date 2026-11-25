import PersonaModuleAPI
import SwiftUI

/// The `voice` module (#13431, part of #13430; docs/apple/ios.md "Modules", docs/assistant/voice.md): full-duplex voice in a
/// conversation's page — the composer's phone icon and Voice Chat, the call's bar in the composer's place, the line a
/// call that ended by itself leaves, its captions as the thread's last rows, Asking by Voice on a card, the voice settings
/// on the voice extension's page, and the DEBUG switches `-showVoiceSample` / `-openVoice`. The core reaches it only
/// through `Modules.registry`: the `ConversationVoice` capability the chat, the meeting page, the cards and Settings ask
/// for. The composer's dictation is the core's and stays.
///
/// What a call runs on stays in the core for now, because system calls, CarPlay and recording share it: the voice
/// engine (`VoiceCalls`, `VoiceConversation`, the audio, the socket; apple/Persona/Voice), the starts and refusals
/// (`VoiceEntry`) and the call screens (apple/Persona/Calls, apple/Persona/CarPlay). They follow as #13431 goes on.
struct VoiceModule: PersonaModule {
    typealias Host = PersonaHostApp

    let descriptor = ModuleDescriptor(id: "voice", hubGated: true, slots: [.capabilities])

    /// Built into every build that compiles the module (apple/modules.yml).
    var isAvailable: Bool { true }

    func register(into registrar: inout ModuleRegistrar<PersonaHostApp>) {
        registrar.provide((any ConversationVoice).self, ConversationVoiceState.shared)
    }
}

/// What the module answers the pages with. Its one piece of state is why a start from a page could not go ahead (the
/// alert `composerSupport` shows); everything else reads the core's voice engine, so a page draws again only when what
/// it asked about changes.
@MainActor @Observable
final class ConversationVoiceState: ConversationVoice {
    static let shared = ConversationVoiceState()

    /// Why the start from a page did not go ahead, by conversation (the 「+」 menu's Voice Chat where the page offers no
    /// call, a refusal after the start's wait, docs/assistant/voice.md 1.3).
    var notes: [SessionKey: VoiceUnavailableNote] = [:]

    private var calls: VoiceCalls { .shared }

    func isBound(to key: SessionKey) -> Bool { calls.isBound(to: key) }
    func isLive(on key: SessionKey) -> Bool { calls.isLive(on: key) }

    func showsEnded(on key: SessionKey) -> Bool { endNote(on: key) != nil }

    func isReturning(to key: SessionKey) -> Bool { calls.returning == key }
    func leave(_ key: SessionKey) { calls.leave(key) }
    func returned(_ key: SessionKey) { calls.returned(key) }

    func offersCall(in key: SessionKey) -> Bool {
        let model = AppModel.shared
        return VoiceSupport.shared.availability(for: model.session(key), group: key.group).isAvailable
    }

    func iconLabel(_ key: SessionKey) -> String { VoiceEntry.iconLabel(key) }

    func start(_ key: SessionKey, inApp: Bool) {
        let note = VoiceEntry.start(key, model: AppModel.shared, inApp: inApp) { [weak self] in self?.notes[key] = $0 }
        notes[key] = note
    }

    var listsVoiceChat: Bool { VoicePlatform.current == .phone }

    func bar(key: SessionKey, label: String?) -> AnyView {
        guard let call = calls.call(on: key) else { return AnyView(EmptyView()) }
        return AnyView(VoiceBar(call: call, key: key, label: label ?? VoiceEntry.label(key, model: AppModel.shared)))
    }

    func endedLine(key: SessionKey) -> AnyView {
        guard let call = calls.call(on: key), let note = endNote(on: key) else { return AnyView(EmptyView()) }
        return AnyView(VoiceEndedStrip(call: call, note: note))
    }

    func threadTail(key: SessionKey, held: Bool) -> AnyView { AnyView(VoiceThreadTail(key: key, held: held)) }

    func tailFollower(key: SessionKey, follow: @escaping () -> Void) -> AnyView {
        AnyView(Color.clear.allowsHitTesting(false).accessibilityHidden(true).modifier(VoiceTailFollow(key: key, follow: follow)))
    }

    func composerSupport(key: SessionKey, active: Bool, offered: @escaping () -> Bool, start: @escaping () -> Void) -> AnyView {
        AnyView(VoiceComposerSupport(key: key, active: active, offered: offered, start: start))
    }

    func askMark(_ interaction: Interaction) -> AnyView { AnyView(VoiceAskMark(interaction: interaction)) }

    var barHeight: CGFloat { VoiceBarRoom.shared.height }

    func sampleCards(_ key: SessionKey) -> [Interaction] {
        #if DEBUG
        VoiceSample.cards(key)
        #else
        []
        #endif
    }

    func showsExtensionSettings(_ plugin: HubExtension, manager: String?) -> Bool {
        VoiceTimbreSection.shows(plugin, manager: manager)
    }

    func extensionSettings(extensionID: String, group: String) -> AnyView {
        AnyView(VoiceTimbreSection(extensionID: extensionID, group: group))
    }

    /// The note a call bound to `key` left when it ended by itself.
    private func endNote(on key: SessionKey) -> VoiceEndNote? {
        guard let call = calls.call(on: key), call.isOver, case .ended(let end) = call.phase else { return nil }
        return VoiceEndNote.make(end, extensionID: call.extensionID)
    }
}

/// `ComposerVoice` with the page's note kept in the module's state: drawn clear behind the composer, it takes no touch.
private struct VoiceComposerSupport: View {
    let key: SessionKey
    let active: Bool
    let offered: () -> Bool
    let start: () -> Void

    var body: some View {
        let state = ConversationVoiceState.shared
        Color.clear
            .allowsHitTesting(false)
            .accessibilityHidden(true)
            .modifier(ComposerVoice(key: key, note: Binding(get: { state.notes[key] }, set: { state.notes[key] = $0 }),
                                    active: active, offered: offered, start: start))
    }
}
