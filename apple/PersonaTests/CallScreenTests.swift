import Foundation
import SwiftUI
import Testing
import UIKit
@testable import Persona

/// The call to Persona's page (#9290, docs/design/DESIGN_SYSTEM.md "Call screen"): its one line and its orb from the
/// call's state, the page shown again when the app comes back, Minimise never ending the call, the + panel's Call
/// Persona beside Voice Chat, and every control going through `PersonaCalls` (read from the sources).
@MainActor
struct CallScreenTests {
    private static let since = Date(timeIntervalSinceReferenceDate: 1_000)

    private static func state(_ phase: CallPhase, held: Bool = false, muted: Bool = false, note: String? = nil,
                              voice: (VoicePhase, VoiceLink)? = nil, reconnecting: Bool = false) -> CallScreenState {
        CallScreenState.make(phase: phase, held: held, muted: muted, note: note, voice: voice.map { (phase: $0.0, link: $0.1) },
                             reconnecting: reconnecting)
    }

    @Test func theLineSaysWhereTheCallIs() {
        #expect(Self.state(.dialing) == CallScreenState(line: .calling, orb: .connecting))
        #expect(Self.state(.connecting) == CallScreenState(line: .connecting, orb: .connecting))
        #expect(Self.state(.connected(since: Self.since), voice: (.live(.listening), .live)).line == .duration(since: Self.since))
        #expect(Self.state(.ended(.failed), note: "Can't reach the hub") == CallScreenState(line: .ended(note: "Can't reach the hub"), orb: .ended))
        #expect(Self.state(.ended(.hungUp)).line == .ended(note: nil))
    }

    /// On hold wins over everything else the voice says; the voice's connection being picked up again shows as
    /// Reconnecting…, from its phase or its link (a sample has neither: its flag stands in).
    @Test func onHoldAndReconnectingTakeTheLine() {
        let on = CallPhase.connected(since: Self.since)
        #expect(Self.state(on, held: true, voice: (.reconnecting(attempt: 1), .live)) == CallScreenState(line: .onHold, orb: .paused))
        #expect(Self.state(on, voice: (.reconnecting(attempt: 2), .live)) == CallScreenState(line: .reconnecting, orb: .connecting))
        #expect(Self.state(on, voice: (.live(.listening), .reconnecting)).line == .reconnecting)
        #expect(Self.state(on, reconnecting: true).line == .reconnecting)
        #expect(Self.state(on).line == .duration(since: Self.since))
    }

    /// The orb moves as the voice bar's does: speaking, hearing, muted, the upstream paused.
    @Test func theOrbFollowsTheVoice() {
        let on = CallPhase.connected(since: Self.since)
        #expect(Self.state(on, voice: (.live(.speaking), .live)).orb == .speaking)
        #expect(Self.state(on, voice: (.live(.hearing), .live)).orb == .hearing)
        #expect(Self.state(on, muted: true, voice: (.live(.listening), .live)).orb == .muted)
        #expect(Self.state(on, voice: (.live(.listening), .paused)).orb == .paused)
        #expect(Self.state(on, muted: true).orb == .muted, "a sample, no voice")
        #expect(Self.state(on).orb == .listening)
    }

    /// Minimise only hides the page; coming back to the app (the Dynamic Island, the Lock Screen's call) shows it again
    /// while the call is on, and never brings back a call that is over or an urgent call.
    @Test func comingBackToTheAppShowsTheCallAgain() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        rig.calls.presented = true
        rig.calls.minimise()
        #expect(!rig.calls.presented && call.phase.isLive && !socket.closed && rig.system.ended.isEmpty)
        rig.calls.returnedToApp()
        #expect(rig.calls.presented)
        rig.calls.minimise()
        rig.calls.hangUp()
        rig.system.act(.end, call.id)
        #expect(call.phase.isEnded)
        rig.calls.returnedToApp()
        #expect(!rig.calls.presented, "an ended call's page does not come back")
    }

    @Test func anUrgentCallIsNotTheCallScreens() async throws {
        let rig = CallRig()
        try await rig.calls.ring(UUID(), callID: "call_1", name: "Urgent")
        #expect(rig.calls.ownerCall == nil)
        rig.calls.returnedToApp()
        rig.calls.present()
        #expect(!rig.calls.presented)
    }

    /// Reading what the cover and the bar read makes no system call manager (#9264): only a call does.
    @Test func theCoverMakesNoCallManager() {
        var made = 0
        let calls = PersonaCalls(system: { made += 1; return FakeCallSystem() }, voiceCalls: VoiceCalls())
        _ = calls.presented
        _ = calls.ownerCall
        _ = calls.refusal
        _ = calls.route.external
        _ = calls.route.speakerOn
        calls.returnedToApp()
        calls.minimise()
        #expect(made == 0 && !calls.systemMade)
    }

    /// The + panel's capture rows: Call Persona right after Voice Chat, and not there where the page offers none (an
    /// Agent's chat, another assistant conversation).
    @Test func callPersonaSitsRightAfterVoiceChat() throws {
        let rows = AttachMoreSection.common(voice: {}, call: {}, camera: {}, glasses: nil, mention: {})
        let capture = try #require(rows.first { $0.id == "capture" })
        #expect(capture.rows.map(\.id).prefix(3) == ["composer-menu-voice", "composer-menu-call", "composer-menu-camera"])
        #expect(capture.rows[1].symbol == Theme.Icon.callPersona && capture.rows[1].symbol != capture.rows[0].symbol)
        let agent = AttachMoreSection.common(voice: {}, camera: {}, glasses: nil, mention: {})
        #expect(agent.flatMap(\.rows).allSatisfy { $0.id != "composer-menu-call" })
    }

    /// Call Persona is one glyph wherever it is offered (#9290): the + panel's (above), the App Shortcut's (a literal
    /// there) and the Continue of a call that ended (a new system call), apart from Voice Chat's handset.
    @Test func callPersonaHasOneGlyphEverywhere() throws {
        let shortcuts = try Self.code("Persona/Intents/PersonaShortcuts.swift")
        let call = try #require(shortcuts.range(of: "intent: CallPersonaIntent()"))
        let entry = String(shortcuts[call.upperBound...].prefix(600))
        #expect(entry.contains("systemImageName: \"\(Theme.Icon.callPersona)\""))
        let strip = try Self.code("Persona/Views/Voice/VoiceEntry.swift")
        #expect(strip.contains("systemImage: call.surface == .call ? Theme.Icon.callPersona : Theme.Icon.voiceChannel"))
    }

    /// The return-to-call bar's room at the window root is every page's, the Separate layout's iPad tabs too: only what
    /// a tab is given above the root's top (the tab bar's second room) is taken back off it.
    @Test func theTabsKeepTheCallBarsRoom() {
        let status: CGFloat = 24, bar: CGFloat = 52, tabBar: CGFloat = 64
        #expect(TabBarExtraRoom.extra(given: status + bar + tabBar, rootTop: status + bar) == tabBar)
        #expect(TabBarExtraRoom.extra(given: status + bar, rootTop: status + bar) == 0, "the bar's room stays")
        #expect(TabBarExtraRoom.extra(given: status + tabBar, rootTop: status) == tabBar, "no bar: as before")
    }

    /// The orb gives way on a short page or at a large text size: to its share of the page, then to the words around it
    /// (the room the middle has less theirs); where not even the voice bar's orb fits, it goes and the words stay.
    @Test func theOrbGivesWayToTheControlsAndTheWords() throws {
        #expect(CallScreen.orbSize(page: 0, free: 0, scale: 1) == Theme.Size.callOrb, "not measured yet")
        #expect(CallScreen.orbSize(page: 900, free: .infinity, scale: 1) == Theme.Size.callOrb)
        let large = try #require(CallScreen.orbSize(page: 740, free: .infinity, scale: 1.4))
        #expect(large * 1.4 <= 740 * Theme.Size.callOrbShare + 0.01 && large < Theme.Size.callOrb)
        #expect(CallScreen.orbSize(page: 900, free: 90, scale: 1) == 90, "what the words leave")
        let grown = try #require(CallScreen.orbSize(page: 900, free: 90, scale: 1.25))
        #expect(abs(grown * 1.25 - 90) < 0.01)
        #expect(CallScreen.orbSize(page: 900, free: Theme.Size.voiceOrb, scale: 1) == Theme.Size.voiceOrb)
        #expect(CallScreen.orbSize(page: 900, free: Theme.Size.voiceOrb - 1, scale: 1) == nil, "no room for even the smallest orb")
        #expect(CallScreen.orbSize(page: 100, free: .infinity, scale: 1) == nil)
    }

    #if DEBUG
    /// Every control stays on the page whatever the window, at the largest interface text size with Resume offered and a
    /// two-line caption, and the call's line and the caption stay in sight between Minimise and the controls: the
    /// narrowest iPad window (Slide Over or Split View on an iPad mini in landscape, 320 × 720 under the status bar; Mute,
    /// Speaker and Resume did not fit one row) and a short phone (an iPhone SE's 375 × 647 under its status bar; the page
    /// was taller than the screen). The page is hosted in a window of exactly that room and read as XCUITest reads it
    /// (`RuntimeCLI.elements`).
    @Test(arguments: [CGSize(width: 320, height: 720), CGSize(width: 375, height: 647)])
    func everyControlStaysOnThePage(window size: CGSize) async throws {
        let rig = CallRig()
        rig.calls.showSample(.resume)
        let call = try #require(rig.calls.ownerCall)
        #expect(call.resumeOffered && call.sampleCaption != nil)
        try automation(true)
        defer { try? automation(false) }
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        defer { window.isHidden = true }
        window.frame = CGRect(origin: CGPoint(x: 0, y: 100), size: size)
        let host = UIHostingController(rootView: CallScreen(call: call).dynamicTypeSize(.xxxLarge))
        // The window is the room under the status bar: the test screen's own insets (its island, its home bar) are not.
        host.safeAreaRegions = []
        window.rootViewController = host
        window.isHidden = false
        window.layoutIfNeeded()

        let controls = ["call-minimise", "call-mute", "call-speaker", "call-resume", "call-end"]
        let ids = controls + ["call-state", "call-caption"]
        func frames() -> [String: CGRect] {
            try? automation(true) // again: a test hosting its own page may have turned it off meanwhile
            var out: [String: CGRect] = [:]
            for e in RuntimeCLI.elements(matching: [:]) where ids.contains(e.id) && RuntimeCLI.view(of: e.object)?.window === window {
                out[e.id] = e.frame
            }
            return out
        }
        var found = frames()
        for _ in 0..<30 where found.count < ids.count {
            try? await Task.sleep(for: .milliseconds(100))
            found = frames()
        }
        // The page measures itself once (the orb's room): its second layout.
        try? await Task.sleep(for: .milliseconds(300))
        found = frames()
        let page = window.frame.insetBy(dx: -0.5, dy: -0.5)
        for id in controls {
            let frame = try #require(found[id], "\(id) is not on the page (\(size))")
            #expect(page.contains(frame), "\(id) at \(frame) is outside the \(size) window \(window.frame)")
        }
        // The line and the caption in sight: under Minimise, over the controls, not scrolled away.
        let top = try #require(found["call-minimise"]).maxY - 0.5
        let bottom = controls.dropFirst().compactMap { found[$0]?.minY }.min() ?? page.maxY
        for id in ["call-state", "call-caption"] {
            let frame = try #require(found[id], "\(id) is not on the page (\(size))")
            #expect(frame.minY >= top && frame.maxY <= bottom + 0.5, "\(id) at \(frame) is not between Minimise and the controls (\(top)…\(bottom))")
        }
        for (a, b) in [("call-mute", "call-speaker"), ("call-speaker", "call-resume"), ("call-mute", "call-resume"), ("call-resume", "call-end")] {
            if let fa = found[a], let fb = found[b] { #expect(!fa.insetBy(dx: 1, dy: 1).intersects(fb), "\(a) over \(b)") }
        }
        shot(window, "call-held-resume-\(Int(size.width))x\(Int(size.height))-ax")
    }

    /// The hosted page's picture, for a person to look at (`PERSONA_SHOTS`, as the UI tests').
    private func shot(_ window: UIWindow, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
        }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? image.pngData()?.write(to: URL(fileURLWithPath: dir + "/\(name).png"))
    }

    /// With no assistive technology running SwiftUI builds no accessibility elements: on as XCUITest turns it on, and off
    /// again after, for the tests that follow in this process (as `QueueStripHintTests`).
    private func automation(_ on: Bool) throws {
        let handle = try #require(dlopen("/usr/lib/libAccessibility.dylib", RTLD_NOW))
        let set = try #require(dlsym(handle, "_AXSSetAutomationEnabled"))
        typealias Enable = @convention(c) (Int32) -> Void
        unsafeBitCast(set, to: Enable.self)(on ? 1 : 0)
    }
    #endif

    @Test func theAudioControlNamesTheRoute() {
        #expect(CallAudioRouteControl.name(.bluetooth("AirPods Pro")) == "AirPods Pro")
        #expect(!CallAudioRouteControl.name(.speaker).isEmpty && !CallAudioRouteControl.name(.receiver).isEmpty)
        #expect(CallAudioRouteControl.name(.speaker) != CallAudioRouteControl.name(.receiver))
    }

    /// A round control fills only while its toggle is on.
    @Test func onlyAToggleThatIsOnIsOn() {
        #expect(CallControlKind.toggle(on: true).isOn)
        #expect(!CallControlKind.toggle(on: false).isOn && !CallControlKind.action.isOn && !CallControlKind.end.isOn)
    }

    // MARK: The sources

    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    private static func code(_ path: String) throws -> String {
        try String(contentsOf: apple.appending(path: path), encoding: .utf8).components(separatedBy: "\n")
            .map { l in l.range(of: "//").map { String(l[..<$0.lowerBound]) } ?? l }
            .joined(separator: "\n")
    }

    /// The call screen and its controls act only through `PersonaCalls` (so the system's call UI and AirPods say the
    /// same): never on the voice's mute, its hold, the in-app calls or the audio session themselves.
    @Test func theCallScreenActsThroughPersonaCalls() throws {
        for file in ["Persona/Calls/CallScreen.swift", "Persona/Calls/CallControls.swift"] {
            let code = try Self.code(file)
            for direct in ["VoiceCalls", "setCallMuted", "setHeld(", "muted.toggle", "muted = ", "overrideOutputAudioPort",
                           "AVAudioSession", ".end(."] {
                #expect(!code.contains(direct), "\(file) uses \(direct)")
            }
        }
        let screen = try Self.code("Persona/Calls/CallScreen.swift")
        for action in ["calls.setMuted(", "calls.setSpeaker(", "calls.resume(", "calls.hangUp(", "calls.minimise()", "calls.present()",
                       "calls.returnedToApp()"] {
            #expect(screen.contains(action), "the call screen has no \(action)")
        }
    }

    /// A system call's voice bar hangs up, mutes and expands through `PersonaCalls`; its ended line calls Persona again.
    @Test func aSystemCallsBarAndEndedLineGoThroughPersonaCalls() throws {
        let bar = try Self.code("Persona/Views/Voice/VoiceBar.swift")
        #expect(bar.contains("if call.surface == .call { return PersonaCalls.shared.setMuted(!call.muted) }"))
        #expect(bar.contains("if call.surface == .call { return PersonaCalls.shared.hangUp() }"))
        #expect(bar.contains("PersonaCalls.shared.present()"))
        let strip = try Self.code("Persona/Views/Voice/VoiceEntry.swift")
        #expect(strip.contains("PersonaCalls.shared.callPersona(group: group, origin: .app)"))
    }

    /// Urgent calls start on the earpiece as calls to Persona do (owner decision 2026-11-17): one call category for both
    /// kinds, with no loudspeaker by default; the urgent page has the Speaker control that moves it.
    @Test func anUrgentCallStartsOnTheEarpiece() throws {
        let calls = try Self.code("Persona/Calls/PersonaCalls.swift")
        let prepare = try #require(calls.range(of: "static func setCallCategory()"))
        let body = String(calls[prepare.upperBound...].prefix(400))
        #expect(body.contains("PhoneVoiceAudio.callSessionOptions") && !body.contains("PhoneVoiceAudio.sessionOptions"))
        #expect(!PhoneVoiceAudio.callSessionOptions.contains(.defaultToSpeaker))
        let owner = try Self.code("Persona/Calls/OwnerCall.swift")
        #expect(owner.contains("await prepareAudio(call)"), "a call to Persona's is the same")
        let page = try Self.code("Persona/Calls/UrgentCallView.swift")
        #expect(page.contains("calls.setSpeaker(") && page.contains("CallAudioRouteControl("))
    }

    /// The page is the app's, over everything, next to the urgent call's cover.
    @Test func theCoverIsTheAppsNextToTheUrgentCalls() throws {
        let app = try Self.code("Persona/App/PersonaApp.swift").components(separatedBy: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
        let cover = try #require(app.firstIndex(of: ".personaCallCover()"))
        #expect(app[cover + 1] == ".urgentCallCover()")
    }
}
