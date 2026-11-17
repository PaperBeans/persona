#if os(iOS)
import SwiftUI

// The call to Persona's own page (#9290, docs/design/DESIGN_SYSTEM.md "Call screen"): full screen over the app, as the
// Phone app's in-call screen, because a call is one task going on with its controls always under the thumb; Minimize
// leaves it for a bar at the top that returns to it, and the call goes on (`PersonaCalls` runs it, not this page).

/// What the call screen says and how its orb looks, from the call's state alone (pure, for the tests).
nonisolated struct CallScreenState: Equatable, Sendable {
    nonisolated enum Line: Equatable, Sendable {
        /// Outgoing, the system asked to start it.
        case calling
        /// Started, waiting for the audio and the voice's first `ready`.
        case connecting
        /// How long it has been on (a running timer).
        case duration(since: Date)
        case onHold
        /// The voice's connection to the hub dropped and is being picked up again.
        case reconnecting
        /// Over, and why in a line (nil: hung up).
        case ended(note: String?)
    }

    var line: Line
    var orb: VoiceOrb.Phase

    /// `voice`: the call's voice once it began, its phase and link (a sample has none: `reconnecting` stands in).
    static func make(phase: CallPhase, held: Bool, muted: Bool, note: String?, voice: (phase: VoicePhase, link: VoiceLink)?,
                     reconnecting: Bool = false) -> CallScreenState {
        switch phase {
        case .dialing, .ringing:
            return CallScreenState(line: .calling, orb: .connecting)
        case .connecting:
            return CallScreenState(line: .connecting, orb: .connecting)
        case .ended:
            return CallScreenState(line: .ended(note: note), orb: .ended)
        case .connected(let since):
            if held { return CallScreenState(line: .onHold, orb: .paused) }
            let dropped: Bool = {
                guard let voice else { return reconnecting }
                if case .reconnecting = voice.phase { return true }
                return voice.link == .reconnecting
            }()
            if dropped { return CallScreenState(line: .reconnecting, orb: .connecting) }
            let orb = voice.map { VoiceBarStatus.make(phase: $0.phase, link: $0.link, muted: muted, working: false).orb }
                ?? (muted ? .muted : .listening)
            return CallScreenState(line: .duration(since: since), orb: orb)
        }
    }
}

/// The call to Persona's page: Minimize at the top; Persona's orb (moving with the voice), its name, one line of where
/// the call is and Persona's latest words in the middle; the round controls at the bottom. VoiceOver's escape
/// minimises. When the call ends its line says why for a moment (the call stays `PersonaCalls.endedStays`), then the
/// page goes. Minimize and the controls always keep their room: the middle takes what they leave (`CallScreenMiddle`).
struct CallScreen: View {
    let call: PersonaCall
    @State private var calls = PersonaCalls.shared
    /// The page's height, for the orb's size (`orbSize`).
    @State private var height: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let state = Self.state(call)
        VStack(spacing: Theme.Space.l) {
            HStack(spacing: Theme.Space.s) {
                minimise
                Spacer(minLength: 0)
            }
            CallScreenMiddle(call: call, state: state, page: height)
            CallScreenControls(call: call, route: calls.route)
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.l)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { h in
            if abs(height - h) >= 1 { height = h }
        }
        .background { Theme.pageBackground.ignoresSafeArea() }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: state)
        .accessibilityElement(children: .contain)
        .accessibilityAction(.escape) { calls.minimise() }
        .accessibilityIdentifier("call-screen")
        .modifier(CallAnnouncements(state: state))
    }

    /// The orb's size at the default text size (`VoiceOrb` grows it by `scale`): `callOrb`, but at most `callOrbShare`
    /// of the page's height and `free` (the middle's height less its words and the gap to them) once grown. Nil where
    /// not even the voice bar's orb fits: the words say where the call is, the orb gives way to them.
    static func orbSize(page: CGFloat, free: CGFloat, scale: CGFloat) -> CGFloat? {
        guard page > 0, scale > 0 else { return Theme.Size.callOrb }
        let points = min(Theme.Size.callOrb * scale, page * Theme.Size.callOrbShare, free)
        guard points >= Theme.Size.voiceOrb * scale else { return nil }
        return points / scale
    }

    static func state(_ call: PersonaCall) -> CallScreenState {
        var reconnecting = false
        #if DEBUG
        reconnecting = call.sampleReconnecting
        #endif
        return CallScreenState.make(phase: call.phase, held: call.held, muted: call.muted, note: call.note,
                                    voice: call.voice.map { ($0.phase, $0.link) }, reconnecting: reconnecting)
    }

    private var minimise: some View {
        Button {
            Haptics.tap()
            calls.minimise()
        } label: {
            ThemedIcon(Theme.Icon.callMinimise)
                .font(Theme.Font.heading)
                .foregroundStyle(Theme.Shell.ink)
                .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
                .background(Theme.Shell.roundMuted, in: .circle)
                .tapTargetFrame()
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Minimize [Call Screen]")
        .accessibilityHint("The call goes on; the bar at the top returns to it")
        .accessibilityIdentifier("call-minimise")
    }
}

/// The middle of the call screen: Persona's orb, the name, the call's line and the latest caption, in the height Minimize
/// and the controls leave it, centred. The orb gives way to the words (`CallScreen.orbSize`), down to the voice bar's
/// orb and then altogether (a short phone at the largest text size); where the words alone do not fit, they scroll, and
/// the controls stay. The caption keeps its two lines' room, so nothing moves as Persona's words come.
private struct CallScreenMiddle: View {
    let call: PersonaCall
    let state: CallScreenState
    /// The page's height (the orb takes a share of it at most).
    let page: CGFloat
    /// The height the middle has, and what its words take of it.
    @State private var room: CGFloat = 0
    @State private var words: CGFloat = 0
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        let free = room > 0 ? room - words - Theme.Space.l : .infinity
        ScrollView {
            VStack(spacing: Theme.Space.l) {
                if let size = CallScreen.orbSize(page: page, free: free, scale: Theme.sizeScale(typeSize)) {
                    CallScreenOrb(call: call, phase: state.orb, size: size)
                }
                VStack(spacing: Theme.Space.l) {
                    VStack(spacing: Theme.Space.s) {
                        Text(verbatim: CallHandle.persona)
                            .font(Theme.Font.display)
                            .lineLimit(1)
                            .accessibilityAddTraits(.isHeader)
                        CallStateLine(line: state.line)
                    }
                    CallCaptionLine(call: call)
                }
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { h in
                    if abs(words - h) >= 1 { words = h }
                }
            }
            .frame(maxWidth: .infinity, minHeight: room)
        }
        .scrollBounceBehavior(.basedOnSize)
        .scrollIndicators(.hidden)
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { h in
            if abs(room - h) >= 1 { room = h }
        }
    }
}

/// Persona's orb at the call screen's size, moving with the voice (its levels: the microphone's while it listens,
/// Persona's own while it speaks). A tap while Persona speaks stops the voice, as in the voice bar.
private struct CallScreenOrb: View {
    let call: PersonaCall
    let phase: VoiceOrb.Phase
    let size: CGFloat

    var body: some View {
        let voice = call.voice
        VoiceOrb(phase: phase, level: { [levels = voice?.levels, speaking = phase == .speaking] in
            guard let levels else { return 0 }
            return Double(speaking ? levels.output : levels.input)
        }, size: size, interrupt: voice.map { voice in { voice.interruptSpeech() } })
    }
}

/// The one line under the name: Calling…, Connecting…, the running time, On Hold, Reconnecting…, or how it ended.
struct CallStateLine: View {
    let line: CallScreenState.Line

    var body: some View {
        Group {
            switch line {
            case .calling: Text("Calling… [Call Screen]")
            case .connecting: Text("Connecting…")
            case .duration(let since):
                Text(since, style: .timer)
                    .monospacedDigit()
            case .onHold: Text("On Hold [Call Screen]")
            case .reconnecting: Text("Reconnecting…")
            case .ended(let note):
                if let note, !note.isEmpty { Text("Call ended · \(note)") } else { Text("Call ended") }
            }
        }
        .font(Theme.Font.body)
        .foregroundStyle(.secondary)
        .lineLimit(2)
        .multilineTextAlignment(.center)
        .accessibilityIdentifier(Self.isDuration(line) ? "call-duration" : "call-state")
    }

    private static func isDuration(_ line: CallScreenState.Line) -> Bool {
        if case .duration = line { true } else { false }
    }
}

/// Persona's latest words (or the owner's, as they come), two lines at most: the call stays useful when looked at.
/// Its own view: captions change with every word, and only this redraws. VoiceOver does not hear them as they come
/// (they would talk over the voice); they read when touched.
private struct CallCaptionLine: View {
    let call: PersonaCall

    var body: some View {
        let text = caption
        Text(text ?? " ")
            .font(Theme.Font.secondary)
            .foregroundStyle(.secondary)
            .lineLimit(2, reservesSpace: true)
            .multilineTextAlignment(.center)
            .frame(maxWidth: Theme.Size.readable)
            .opacity(text == nil ? 0 : 1)
            .accessibilityHidden(text == nil)
            .accessibilityIdentifier("call-caption")
    }

    private var caption: String? {
        if let last = call.voice?.captions.last(where: { !$0.text.isEmpty }) { return last.text }
        #if DEBUG
        return call.sampleCaption
        #else
        return nil
        #endif
    }
}

/// Mute, Speaker (or Audio, the system's route picker, while Bluetooth, a car or headphones are there) and Resume when
/// the call offers it in a row, End under them; where the three do not fit one row (a narrow window at a large text
/// size: iPad's Split View or Slide Over), Mute and Speaker over Resume and End, as the urgent call's page does. Each
/// goes through `PersonaCalls`.
private struct CallScreenControls: View {
    let call: PersonaCall
    let route: CallRoute
    @State private var calls = PersonaCalls.shared

    var body: some View {
        let live = call.phase.isLive
        ViewThatFits(in: .horizontal) {
            VStack(spacing: Theme.Space.xl) {
                HStack(alignment: .top, spacing: Theme.Space.xl) {
                    mute
                    speaker
                    resume
                }
                end
            }
            VStack(spacing: Theme.Space.l) {
                HStack(alignment: .top, spacing: Theme.Space.xl) {
                    mute
                    speaker
                }
                HStack(alignment: .top, spacing: Theme.Space.xl) {
                    resume
                    end
                }
            }
        }
        .disabled(!live)
    }

    private var end: some View {
        CallControl(symbol: Theme.Icon.voiceEnd, label: String(localized: "End"), id: "call-end", kind: .end,
                    hint: String(localized: "Hangs up the call")) {
            calls.hangUp(call.id)
        }
    }

    private var mute: some View {
        CallControl(symbol: call.muted ? Theme.Icon.voiceUnmute : Theme.Icon.voiceMute, label: String(localized: "Mute [Voice Bar 7db2e1]"),
                    id: "call-mute", kind: .toggle(on: call.muted)) {
            calls.setMuted(!call.muted, call.id)
        }
    }

    @ViewBuilder private var speaker: some View {
        if route.external {
            CallAudioRouteControl(route: route)
        } else {
            CallControl(symbol: Theme.Icon.callSpeaker, label: String(localized: "Speaker [Call Control]"), id: "call-speaker",
                        kind: .toggle(on: route.speakerOn)) {
                calls.setSpeaker(!route.speakerOn)
            }
        }
    }

    @ViewBuilder private var resume: some View {
        if call.resumeOffered {
            CallControl(symbol: Theme.Icon.resume, label: String(localized: "Resume [Call Control]"), id: "call-resume",
                        hint: String(localized: "Takes the call off hold")) {
                calls.resume(call.id)
            }
        }
    }
}

/// VoiceOver hears where the call went when it changes by itself: on hold, picked up again, ended.
private struct CallAnnouncements: ViewModifier {
    let state: CallScreenState

    func body(content: Content) -> some View {
        content.onChange(of: Self.word(state.line)) { _, word in
            if let word { AccessibilityNotification.Announcement(word).post() }
        }
    }

    private static func word(_ line: CallScreenState.Line) -> String? {
        switch line {
        case .onHold: String(localized: "On Hold [Call Screen]")
        case .reconnecting: String(localized: "Reconnecting…")
        case .ended(let note):
            if let note, !note.isEmpty { String(localized: "Call ended · \(note)") } else { String(localized: "Call ended") }
        case .calling, .connecting, .duration: nil
        }
    }
}

// MARK: The cover, the bar back to it, and coming back to the app

/// Puts the call to Persona's page over the app while it is shown (`PersonaCalls.presented`), and the bar that returns
/// to it at the top while it is minimised; shows it again when the app comes back to the front with the call on; says
/// why a call could not be placed (`PersonaCalls.refusal`) once the page has gone. It reads only `PersonaCalls`, which
/// makes no system call manager until a call needs one (#9264). An answered urgent call's page goes over it instead
/// (`urgentCallCover`): the call to Persona is on hold meanwhile, and its page comes back after.
struct PersonaCallCover: ViewModifier {
    @State private var calls = PersonaCalls.shared
    @State private var urgent = UrgentCallPresenter.shared
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase
    /// The app went to the background since it was last in front.
    @State private var away = false
    /// The page is up: a refusal waits for it to go (UIKit drops an alert presented while a cover closes).
    @State private var coverUp = false
    @State private var refusal: VoiceUnavailableNote?

    func body(content: Content) -> some View {
        content
            .overlay(alignment: .top) { CallReturnBar().modifier(AppTextSize(model: model)) }
            // Nothing dismisses the cover but the call (Minimize, the call over, an urgent call's page): no swipe.
            .fullScreenCover(item: Binding(get: { shown }, set: { _ in }), onDismiss: { coverUp = false; showRefusal() }) { call in
                CallScreen(call: call)
                    .modifier(CoverLook(model: model))
                    .onAppear { coverUp = true }
            }
            .onChange(of: calls.refusal != nil) { _, refused in if refused, !coverUp { showRefusal() } }
            .voiceUnavailableAlert(Binding(get: { refusal }, set: { refusal = $0; if $0 == nil { calls.refusal = nil } }), model: model)
            .onChange(of: scenePhase) { _, phase in
                if phase == .background { away = true }
                if phase == .active, away {
                    away = false
                    calls.returnedToApp()
                }
            }
    }

    /// The call the page shows: presented, and no urgent call's page over everything.
    private var shown: PersonaCall? {
        guard calls.presented, urgent.session == nil else { return nil }
        return calls.ownerCall
    }

    private func showRefusal() {
        if let note = calls.refusal { refusal = note }
    }
}

/// What `RootView` gives every screen and a cover attached outside it would miss: the accent as the tint, the style's
/// font design, the app's text size and BeautifulUI's theme.
private struct CoverLook: ViewModifier {
    let model: AppModel

    func body(content: Content) -> some View {
        content.tint(Theme.accent).fontDesign(model.style.fontDesign).modifier(AppTextSize(model: model)).buiTheme(.persona())
    }
}

/// The app's text size as `RootView` gives its pages: the system's, moved by Settings › Text Size (Small / Standard /
/// Large), where the interface stops (`Theme.interfaceTypeSize`). For what is drawn outside RootView, which would take
/// the window's size (the system's, capped: `WindowTextSize`) instead: the call's page and the bar back to it.
private struct AppTextSize: ViewModifier {
    let model: AppModel
    /// The system's size, not the window's (which `WindowTextSize` caps).
    @State private var system = WindowTextSize.system

    func body(content: Content) -> some View {
        content
            .dynamicTypeSize(Theme.interfaceTypeSize(model.textSize.applied(to: system)))
            .onReceive(NotificationCenter.default.publisher(for: UIContentSizeCategory.didChangeNotification)) { _ in
                system = WindowTextSize.system
            }
    }
}

/// While the call to Persona is on and its page minimised: a pill at the top of every page, under the status bar, in
/// the positive role's tint (a call that is on), "Persona · 03:12" with a mark for muted or on hold. A tap opens the
/// call screen again. Nothing else: hanging up is on the page, the voice bar and the system's call UI. Its room is the
/// window's (`CallBarRoom`): every page, the shell's UIKit-hosted bars too, starts below it, so it covers nothing.
struct CallReturnBar: View {
    @State private var calls = PersonaCalls.shared
    /// The pill's height as drawn (it grows with the text).
    @State private var height = Theme.Size.tapTarget
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let call = calls.presented ? nil : calls.ownerCall.flatMap { $0.phase.isLive ? $0 : nil }
        let room = call == nil ? 0 : height + Theme.Space.xs
        ZStack(alignment: .top) {
            if let call {
                CallReturnPill(call: call) { calls.present() }
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { h in
                        if abs(height - h) >= 1 { height = h }
                    }
                    .padding(.horizontal, Theme.Space.page)
                    // Drawn in the room above the pages' top (the window's safe area grew by `room`).
                    .offset(y: -room)
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity)
        .background { CallBarRoom(room: room) }
        .animation(reduceMotion ? nil : Theme.Motion.layout, value: call?.id)
    }
}

/// The room the bar takes at the top of the window: the window's root grows its safe area by it
/// (`additionalSafeAreaInsets`), so SwiftUI pages and the shell's UIKit containers alike start below the bar. A
/// SwiftUI `safeAreaInset` at the root did not reach the shell's top bars, which the bar then covered.
private struct CallBarRoom: UIViewRepresentable {
    let room: CGFloat

    func makeUIView(context: Context) -> RoomView {
        let view = RoomView()
        view.isUserInteractionEnabled = false
        view.isAccessibilityElement = false
        return view
    }

    func updateUIView(_ view: RoomView, context: Context) { view.room = room }

    final class RoomView: UIView {
        var room: CGFloat = 0 {
            didSet { if room != oldValue { applySoon() } }
        }

        override func didMoveToWindow() {
            super.didMoveToWindow()
            applySoon()
        }

        /// After SwiftUI's update: the root's layout changes with it.
        private func applySoon() {
            Task { @MainActor [weak self] in
                guard let self, let root = self.window?.rootViewController else { return }
                if abs(root.additionalSafeAreaInsets.top - self.room) > 0.5 { root.additionalSafeAreaInsets.top = self.room }
            }
        }
    }
}

private struct CallReturnPill: View {
    let call: PersonaCall
    let open: () -> Void

    var body: some View {
        let state = CallScreen.state(call)
        Button {
            Haptics.tap()
            open()
        } label: {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.voiceChannel)
                    .font(Theme.Font.captionStrong)
                    .foregroundStyle(Theme.positive)
                    .accessibilityHidden(true)
                HStack(spacing: Theme.Space.xs) {
                    Text(verbatim: CallHandle.persona)
                    Text(verbatim: "·") // i18n-ignore: a separator
                        .accessibilityHidden(true)
                    CallBarLine(line: state.line)
                }
                .font(Theme.Font.secondaryStrong)
                .foregroundStyle(Color.primary)
                .lineLimit(1)
                if call.held {
                    ThemedIcon(Theme.Icon.pause).font(Theme.Font.captionStrong).foregroundStyle(Theme.caution)
                        .accessibilityHidden(true)
                } else if call.muted {
                    ThemedIcon(Theme.Icon.voiceMute).font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                        .accessibilityLabel("Muted [Inbox Mutes View e5e468]")
                }
            }
            .padding(.horizontal, Theme.Space.l)
            .tapTargetHeight()
            .surface(tint: Theme.softTint(Theme.positive), interactive: true, in: .capsule)
            .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .combine)
        .accessibilityHint("Returns to the call")
        .accessibilityAddTraits(.isButton)
        .accessibilityIdentifier("call-return-bar")
    }
}

/// The bar's short form of the call's line: the running time, Calling…, Connecting…, On Hold, Reconnecting….
private struct CallBarLine: View {
    let line: CallScreenState.Line

    var body: some View {
        switch line {
        case .calling: Text("Calling… [Call Screen]")
        case .connecting: Text("Connecting…")
        case .duration(let since): Text(since, style: .timer).monospacedDigit()
        case .onHold: Text("On Hold [Call Screen]")
        case .reconnecting: Text("Reconnecting…")
        case .ended: Text("Call ended")
        }
    }
}

extension View {
    /// The call to Persona's page over the app, and the bar back to it (`PersonaCallCover`).
    func personaCallCover() -> some View { modifier(PersonaCallCover()) }
}
#endif
