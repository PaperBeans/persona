import SwiftUI

/// Full-duplex voice in a conversation's page (#13431; docs/assistant/voice.md 6), as the core's pages ask for it: what the
/// voice module (apple/Modules/Voice) provides through the registry (`ModuleSlot.capabilities`). Nil when that module is
/// not active (left out of the build, or turned off by the hub's `module.voice`): the page then offers no call, no
/// Voice Chat item, no bar and no captions, and the composer's microphone (dictation, core) is all there is.
///
/// Every read is of the module's own observed state, so the view that asks draws again only when that answer changes
/// (docs/apple/performance.md, P5): the page asks whether a call is bound or on, and the bar, the captions and the
/// composer's side read the call themselves.
@MainActor protocol ConversationVoice: AnyObject {
    /// A call, live or ended, is bound to `key`: its captions are the thread's last rows.
    func isBound(to key: SessionKey) -> Bool
    /// A call bound to `key` is on: its bar takes the composer's place and the dock steps aside.
    func isLive(on key: SessionKey) -> Bool
    /// A call bound to `key` ended by itself: the line it leaves sits over the composer.
    func showsEnded(on key: SessionKey) -> Bool
    /// The bar back to `key`'s call was tapped over a cover of its page: the cover goes.
    func isReturning(to key: SessionKey) -> Bool
    /// The page of `key` went (another conversation shown in its place, or hidden): the call stays bound, its bar on top.
    func leave(_ key: SessionKey)
    /// The page of `key` is shown again: the bar on top goes.
    func returned(_ key: SessionKey)

    /// The page of `key` can start a call now: the composer shows its phone icon.
    func offersCall(in key: SessionKey) -> Bool
    /// What the phone icon in `key`'s page says to VoiceOver.
    func iconLabel(_ key: SessionKey) -> String
    /// Starts a call from `key`'s page (`inApp`: the voice chat in the app whatever the device). Why it could not is
    /// shown by `composerSupport`.
    func start(_ key: SessionKey, inApp: Bool)
    /// This device lists Voice Chat in the composer's 「+」 menu and prompt bar.
    var listsVoiceChat: Bool { get }

    /// The call's bar in the composer's place while a call bound to `key` is on. `label` names who answers (nil: the
    /// conversation's agent, or the assistant in its own conversations).
    func bar(key: SessionKey, label: String?) -> AnyView
    /// The line a call that ended by itself leaves (Voice Ended, Continue).
    func endedLine(key: SessionKey) -> AnyView
    /// The call's captions as the thread's last rows; `held`: the chat holds back what arrives.
    func threadTail(key: SessionKey, held: Bool) -> AnyView
    /// Calls `follow` as the captions grow, as streamed words keep a following chat at its newest end. Draws nothing.
    func tailFollower(key: SessionKey, follow: @escaping () -> Void) -> AnyView
    /// The composer's side: asks the hub about voice while the app is `active`, says why a call could not start, and the
    /// DEBUG switches (`-showVoiceSample`, `-openVoice`; `offered` / `start` are the page's). Draws nothing.
    func composerSupport(key: SessionKey, active: Bool, offered: @escaping () -> Bool, start: @escaping () -> Void) -> AnyView
    /// Asking by Voice, on the card the voice is asking about.
    func askMark(_ interaction: Interaction) -> AnyView
    /// The bar's height as drawn, which the question cards keep clear of.
    var barHeight: CGFloat { get }
    /// DEBUG: the cards `-showVoiceSample` puts on `key`'s page.
    func sampleCards(_ key: SessionKey) -> [Interaction]

    /// The voice settings on a voice extension's page (the voice this device's calls speak with).
    func showsExtensionSettings(_ plugin: HubExtension, manager: String?) -> Bool
    func extensionSettings(extensionID: String, group: String) -> AnyView
}

extension AppModel {
    /// Full-duplex voice when the voice module is active.
    var voice: (any ConversationVoice)? { Modules.registry.capability((any ConversationVoice).self) }
}

/// What steps aside while a call is on in `key`'s page (docs/assistant/voice.md 6.2「通话期间」): the main conversation's task
/// dock and route chip. A view of its own, so only it reads the call.
struct HiddenDuringCall<Content: View>: View {
    let key: SessionKey
    @ViewBuilder var content: Content
    @Environment(AppModel.self) private var model

    var body: some View {
        if !(model.voice?.isLive(on: key) ?? false) { content }
    }
}
