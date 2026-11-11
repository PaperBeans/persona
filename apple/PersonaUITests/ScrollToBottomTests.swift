import XCTest

/// ↓ Jump to Latest and following the newest in the chat, case by case (docs/apple/ios-state.md「Back to latest」, #1046): each case records
/// the chat's state over time (`probe.chat-rows`: the arrow, the follow mode, held back, near the newest, the gap to
/// it, a finger on the list) and asserts what the user should see. Each case saves a few screenshots along its
/// timeline (`/tmp/persona-follow-<case>-<n><suffix>.png`) for the review.
///   Agents chat:  PORT=… MOCK_STREAM=1 MOCK_LONG_HISTORY=1 MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py  (PERSONA_MOCK_HUB)
///   Persona chat:  PORT=… MOCK_ASSISTANT_RUN=1 (PERSONA_RUN_HUB); MOCK_ASSISTANT_RUN=alone for the one-screen chat;
///                  12: PORT=… MOCK_ASSISTANT_RUN=1 MOCK_LONG_ASSISTANT=1 (PERSONA_LONG_RUN_HUB, a long chat to glide in)
final class ScrollToBottomTests: XCTestCase {
    private var app: XCUIApplication!
    private static let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // streams with MOCK_STREAM, long history
    private static let photos = "studio/a1f3c2d4-0000-4000-8000-000000000005" // several pending cards with MOCK_AGENTS
    private static let main = "server/b2e4d6f8-0000-4000-8000-000000000001" // runs with MOCK_ASSISTANT_RUN

    private struct Rows: Decodable {
        var items: Int
        var lines: Int
        var visible: Int
        var newest: Bool
        var follows: Bool
        var frozen: Bool?
        var near: Bool?
        var mode: String?
        var arrow: Bool?
        var gap: Int?
        var dragging: Bool?
        var held: Bool?
        var glideTaps: Int?
        var jumpMs: Int?
        var content: Int?
    }

    private typealias Timeline = [(t: Double, r: Rows)]

    override func setUpWithError() throws {
        continueAfterFailure = true
    }

    private func hub(_ name: String) throws -> String {
        guard let h = ProcessInfo.processInfo.environment[name] else { throw XCTSkip("needs \(name)") }
        return h
    }

    private func launch(_ hub: String, chat: String, assistant: Bool, _ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-probes", "YES", "-openChatsFresh", "YES"]
            + (assistant ? ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", chat]
                         : ["-app.tab", "chats", "-tab", "dev", "-dev.openChat", chat])
            + extra
        app.launchPinned()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 6) { allow.tap() }
        let start = Date.now
        while Date.now.timeIntervalSince(start) < 20, (rows()?.visible ?? 0) == 0 { Thread.sleep(forTimeInterval: 0.2) }
        XCTAssertGreaterThan(rows()?.visible ?? 0, 0, "the chat shows")
        sleep(3)
    }

    private func rows() -> Rows? {
        app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self, page: "agents")
            ?? app.probe("chat-rows", as: Rows.self)
    }

    private var arrowButton: XCUIElement { app.buttons["回到最新"].firstMatch }

    /// The composer's field, waited for.
    private func composer() -> XCUIElement? {
        let f = app.textFields.firstMatch
        if f.waitForExistence(timeout: 8) { return f }
        let v = app.textViews.firstMatch
        return v.waitForExistence(timeout: 2) ? v : nil
    }

    /// The probe's state as one line, for the timeline.
    private func line(_ r: Rows) -> String {
        "mode \(r.mode ?? "?") arrow \((r.arrow ?? false) ? 1 : 0) follows \(r.follows ? 1 : 0) frozen \((r.frozen ?? false) ? 1 : 0) "
            + "near \((r.near ?? false) ? 1 : 0) newest \(r.newest ? 1 : 0) gap \(r.gap ?? -1) drag \((r.dragging ?? false) ? 1 : 0) "
            + "held \((r.held ?? false) ? 1 : 0) items \(r.items)"
    }

    private var shotCount: [String: Int] = [:]

    private func shot(_ label: String) {
        let n = (shotCount[label] ?? 0) + 1
        shotCount[label] = n
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
        let name = label.replacingOccurrences(of: " ", with: "_")
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-follow-\(name)-\(n)\(suffix).png"))
    }

    /// Each change of the state for `seconds`, printed as `TRUTH <case> t: …`; `shots` screenshots spread over it.
    @discardableResult
    private func record(_ label: String, seconds: Double, shots: Int = 0) -> Timeline {
        var out: Timeline = []
        var last = ""
        var taken = 0
        let start = Date.now
        while Date.now.timeIntervalSince(start) < seconds {
            let t = Date.now.timeIntervalSince(start)
            if let r = rows() {
                let l = line(r)
                if l != last {
                    print(String(format: "TRUTH %@ %.2f s: ", label, t) + l)
                    out.append((t, r))
                    last = l
                }
            }
            if taken < shots, t >= seconds * Double(taken) / Double(shots) {
                shot(label)
                taken += 1
            }
            Thread.sleep(forTimeInterval: 0.05)
        }
        return out
    }

    /// Waits until the chat's state passes `ok`.
    private func waitFor(seconds: Double, _ ok: (Rows) -> Bool) -> Bool {
        let start = Date.now
        while Date.now.timeIntervalSince(start) < seconds {
            if let r = rows(), ok(r) { return true }
            Thread.sleep(forTimeInterval: 0.05)
        }
        return false
    }

    /// The timeline, for a failure message.
    private func describe(_ s: Timeline) -> String {
        s.map { String(format: "%.2f ", $0.t) + line($0.r) }.joined(separator: "\n")
    }

    /// How often the arrow came and went in a timeline: on, then off again within 2 s (on longer, it was there).
    private func flashes(_ states: Timeline) -> Int {
        var n = 0
        var on: Double?
        for s in states {
            let arrow = s.r.arrow ?? false
            if arrow, on == nil { on = s.t }
            if !arrow, let t = on {
                if s.t - t < 2 { n += 1 }
                on = nil
            }
        }
        return n
    }

    /// The one truth's promises, at every moment: ↓ never at the newest end, nothing held back while following.
    private func consistent(_ label: String, _ s: Timeline) {
        for (t, r) in s {
            if r.newest { XCTAssertFalse(r.arrow ?? false, String(format: "%@ %.2f s: ↓ at the newest end", label, t)) }
            if r.mode == "following" && r.dragging != true {
                XCTAssertFalse(r.frozen ?? false, String(format: "%@ %.2f s: held back while following", label, t))
            }
        }
    }

    /// `rest`: the finger stays down at the end for that long, so the list does not glide on (the drag's distance is
    /// then the list's).
    private func drag(from: CGFloat, to: CGFloat, hold: TimeInterval = 0.05, velocity: XCUIGestureVelocity = .slow, rest: TimeInterval = 0) {
        let a = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: from))
        a.press(forDuration: hold, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: to)), withVelocity: velocity, thenHoldForDuration: rest)
    }

    // MARK: Cases

    /// 1. Streaming, at the newest: a finger drags the list up a little (40 pt) and lets go. It keeps following.
    func testA_DragUpALittleWhileStreaming() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        record("1 before", seconds: 2, shots: 1)
        drag(from: 0.55, to: 0.60, rest: 0.4) // about 45 pt, no glide after it
        let s = record("1 after a small drag", seconds: 10, shots: 4)
        let end = try XCTUnwrap(s.last?.r)
        let stopped = try XCTUnwrap(s.first?.r.gap, "where the drag left the list")
        XCTAssertLessThanOrEqual(stopped, 100, "the drag stayed within the tolerance:\n\(describe(s))")
        XCTAssertEqual(end.mode, "following", "a small drag keeps following:\n\(describe(s))")
        XCTAssertFalse(end.frozen ?? true, "nothing held back:\n\(describe(s))")
        XCTAssertFalse(end.arrow ?? true, "no ↓:\n\(describe(s))")
        XCTAssertGreaterThan(end.items, s.first?.r.items ?? .max, "the stream kept coming in")
        XCTAssertEqual(flashes(s), 0, "the arrow flashed")
        consistent("1", s)
    }

    /// 2. A flick toward the newest from a little way up: it glides to the end and follows; from far up it stops in
    /// the history, reading, with ↓.
    func testB_FlickTowardTheNewest() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        drag(from: 0.35, to: 0.75)
        sleep(1)
        let up = record("2 a screen up", seconds: 2, shots: 1)
        XCTAssertEqual(up.last?.r.mode, "reading", "a screen up it reads:\n\(describe(up))")
        drag(from: 0.75, to: 0.2, velocity: .fast)
        let s = record("2 after the flick", seconds: 5, shots: 3)
        let end = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(end.near ?? false, "the glide reached the newest end:\n\(describe(s))")
        XCTAssertEqual(end.mode, "following", "and follows:\n\(describe(s))")
        XCTAssertFalse(end.arrow ?? true, "no ↓")
        XCTAssertEqual(flashes(s), 0, "the arrow flashed during the glide")
        consistent("2", s)

        for _ in 0..<3 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        drag(from: 0.6, to: 0.4, velocity: .fast)
        let far = record("2 far", seconds: 3)
        let r = try XCTUnwrap(far.last?.r)
        XCTAssertFalse(r.near ?? true, "a short flick from far up stays in the history")
        XCTAssertEqual(r.mode, "reading")
        XCTAssertTrue((r.arrow ?? false) && (r.frozen ?? false), "↓ and held back:\n\(describe(far))")
    }

    /// 3. The running turn's work line opened at the newest end: its header goes up under the top bar (#1173) and stays
    /// there while the turn keeps growing under it — never held back, not following away from it; folded, it follows.
    func testC_OpenAndFoldAWorkLine() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        let work = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "正在工作")).firstMatch
        XCTAssertTrue(work.waitForExistence(timeout: 10))
        shot("3 before")
        work.tap()
        // A list still settling at the newest end takes the first touch to stop (docs/lessons/ios.md): tapped again.
        if !waitFor(seconds: 1.5, { $0.mode == "holding" }) { work.tap() }
        XCTAssertTrue(waitFor(seconds: 1.5, { $0.mode == "holding" }), "opening holds")
        record("3 opening", seconds: 2, shots: 6) // risen, the hold's first moment over
        shot("3 risen")
        XCTAssertTrue(work.waitForExistence(timeout: 3), "the header after opening:\n\(describe(record("3 lost", seconds: 1)))")
        let y0 = work.frame.minY
        let opened = record("3 opened", seconds: 6, shots: 4)
        XCTAssertTrue(work.isHittable, "the header stays on screen")
        XCTAssertLessThanOrEqual(abs(work.frame.minY - y0), 1, "the header holds still while steps come in under it")
        XCTAssertFalse(opened.contains { $0.r.frozen ?? false }, "opening the running turn held it back:\n\(describe(opened))")
        XCTAssertTrue(opened.allSatisfy { $0.r.mode == "holding" }, "held, not following away, until the user decides:\n\(describe(opened))")
        XCTAssertGreaterThan(opened.last?.r.items ?? 0, opened.first?.r.items ?? .max, "the turn kept growing")
        consistent("3 opened", opened)
        work.tap() // fold
        let folded = record("3 folded", seconds: 5, shots: 2)
        let r = try XCTUnwrap(folded.last?.r)
        XCTAssertTrue(r.mode == "following" && r.newest && !(r.frozen ?? true), "folded: at the newest, following:\n\(describe(folded))")
        XCTAssertFalse(r.arrow ?? true, "folded: no ↓")
        XCTAssertEqual(flashes(folded), 0, "the arrow flashed")
        consistent("3 folded", folded)
    }

    /// 4. The keyboard comes up (the composer tapped) and goes down, at the newest end.
    func testD_Keyboard() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        guard let field = composer() else { throw XCTSkip("the composer is not in the accessibility tree (#1006)") }
        field.tap()
        let up = record("4 keyboard up", seconds: 3, shots: 1)
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.25)).tap() // a tap on the page closes it
        let down = record("4 keyboard down", seconds: 3, shots: 1)
        for (name, s) in [("up", up), ("down", down)] {
            let r = try XCTUnwrap(s.last?.r)
            XCTAssertTrue(r.newest && r.mode == "following", "keyboard \(name): at the newest end, following:\n\(describe(s))")
            XCTAssertEqual(flashes(s), 0, "keyboard \(name): the arrow flashed")
            consistent("4 \(name)", s)
        }
    }

    /// 5. A message sent while reading history: back to the newest, following.
    func testE_SendWhileReadingHistory() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        for _ in 0..<3 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        XCTAssertEqual(rows()?.mode, "reading")
        shot("5 reading")
        guard let field = composer() else { throw XCTSkip("the composer is not in the accessibility tree (#1006)") }
        field.tap()
        field.typeText("再看一下")
        app.buttons["发送"].firstMatch.tap()
        let s = record("5 sent", seconds: 6, shots: 3)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following" && !(r.frozen ?? true), "sent from history: at the newest end, following:\n\(describe(s))")
        consistent("5", s)
    }

    /// 6. Pushes insert new content while the user reads history: held back with ↓; then ↓.
    func testF_PushesWhileReadingThenTheArrow() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        for _ in 0..<2 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        let reading = record("6 reading", seconds: 8, shots: 3) // MOCK_STREAM: a push every 2 s
        XCTAssertTrue(reading.allSatisfy { ($0.r.arrow ?? false) && $0.r.mode == "reading" && ($0.r.frozen ?? false) },
                      "reading: ↓ stays, held back:\n\(describe(reading))")
        XCTAssertTrue(arrowButton.waitForExistence(timeout: 2) && arrowButton.isHittable, "↓ to tap")
        arrowButton.tap()
        let s = record("6 after ↓", seconds: 4, shots: 2)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following" && r.arrow != true && !(r.frozen ?? true),
                      "↓: at the newest end, following, no ↓:\n\(describe(s))")
        consistent("6", s)
    }

    /// 7. Out of the chat and back in (the Agents list and the same chat again).
    func testG_LeaveAndComeBack() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        let back = app.buttons.matching(NSPredicate(format: "label == %@", "返回")).firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 10), "返回")
        back.tap()
        sleep(2)
        let rows = app.buttons.matching(identifier: "agents.row")
        let row = (0..<min(rows.count, 12)).map { rows.element(boundBy: $0) }.first { $0.isHittable && $0.label.contains("小组件") }
            ?? (0..<min(rows.count, 6)).map { rows.element(boundBy: $0) }.first { $0.isHittable }
        guard let row else { throw XCTSkip("no agent row to open again on the list (the test's selector, not measured)") }
        row.tap()
        let s = record("7 back in", seconds: 4, shots: 2)
        XCTAssertEqual(flashes(s), 0, "the arrow flashed on entry")
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following", "back in: at the newest end, following:\n\(describe(s))")
        consistent("7", s)
    }

    /// 8. To the background and back, at the newest end of a running turn.
    func testH_BackgroundAndBack() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        shot("8 before")
        XCUIDevice.shared.press(.home)
        sleep(6)
        app.activate()
        let s = record("8 back from the background", seconds: 6, shots: 3)
        XCTAssertEqual(flashes(s), 0, "the arrow flashed on return")
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following", "back from the background: at the newest end, following:\n\(describe(s))")
        consistent("8", s)
    }

    /// 9. A chat shorter than the screen: there is nothing to scroll, the arrow never shows, it keeps following.
    func testI_ShortChat() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        drag(from: 0.4, to: 0.6)
        let s = record("9 short chat", seconds: 6, shots: 1)
        XCTAssertFalse(s.contains { $0.r.arrow ?? false }, "↓ in a chat that fits on one screen:\n\(describe(s))")
        XCTAssertEqual(s.last?.r.mode, "following", "a drag in a one-screen chat keeps following")
        consistent("9", s)
    }

    /// 10. Several pending cards over the chat (#939): no ↓ at rest; scrolled up, ↓ above the cards brings it back.
    func testJ_PendingCards() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.photos, assistant: false)
        let s = record("10 cards", seconds: 4, shots: 1)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertFalse(r.arrow ?? true, "↓ at rest with the cards up:\n\(describe(s))")
        XCTAssertEqual(r.mode, "following", "following with the cards up")
        drag(from: 0.3, to: 0.7, velocity: .fast)
        sleep(2)
        guard arrowButton.exists else { return } // the chat is too short to leave the tolerance
        let cards = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "pending")).allElementsBoundByIndex
        XCTAssertNil(cards.first { $0.frame.intersects(arrowButton.frame) }, "↓ under a pending card")
        arrowButton.tap()
        let after = record("10 after ↓", seconds: 3, shots: 1)
        let a = try XCTUnwrap(after.last?.r)
        XCTAssertTrue(a.newest && a.mode == "following", "↓ with the cards up: at the newest end:\n\(describe(after))")
    }

    /// 11. ↓ tapped while the list still glides after a flick (#3027): the glide stops at once and the list goes to the
    /// newest end, following. XCUITest's own tap waits for the glide to stop: this one (`TouchScript`) is played while the
    /// flick's call still waits it out. `glideTaps` (the app's count of ↓ taps made while the list glided) proves it came
    /// in time.
    func testK_ArrowWhileTheListGlides() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false, ["-scrollTrace", "YES"])
        try tapWhileGliding("11")
    }

    /// 12. The same in Persona's chat, a long one (`MOCK_LONG_ASSISTANT=1`) whose turn runs (a step every second): after
    /// ↓ it stays at the newest end and keeps following what the turn adds.
    func testL_ArrowWhileTheListGlidesInPersonasChat() throws {
        launch(try hub("PERSONA_LONG_RUN_HUB"), chat: Self.main, assistant: true, ["-scrollTrace", "YES"])
        try tapWhileGliding("12")
    }

    /// From the newest end, as the chat opens or as ↓ leaves it: a thumb's flick toward the oldest (3000 pt/s, it glides
    /// on for screens) and ↓ 0.1 s after the list starts to glide, while it is fast, as in the report.
    private func tapWhileGliding(_ label: String) throws {
        guard (rows()?.content ?? 0) > Int(app.frame.height * 2) else {
            throw XCTSkip("a chat of two screens or more to glide in (Persona's: MOCK_LONG_ASSISTANT=1)")
        }
        drag(from: 0.3, to: 0.85, velocity: .fast)
        sleep(1)
        guard arrowButton.waitForExistence(timeout: 3), arrowButton.isHittable else {
            throw XCTSkip("no ↓ after a flick: the chat fits on a screen or two")
        }
        let arrow = CGPoint(x: arrowButton.frame.midX, y: arrowButton.frame.midY)
        for round in 1...3 {
            backToTheNewest()
            let counted = rows()?.glideTaps ?? 0
            let tap = try TouchScript.tapWhenGlideBegins(arrow)
            drag(from: 0.3, to: 0.85, velocity: XCUIGestureVelocity(3000))
            XCTAssertEqual(XCTWaiter().wait(for: [tap.played], timeout: 25), .completed, "the tap was played")
            print("TAP \(label) round \(round):\n\(tap.log())")
            XCTAssertNil(tap.error(), "the tap was played:\n\(tap.log())")
            let s = record("\(label) tapped while gliding \(round)", seconds: 4, shots: 1)
            let r = try XCTUnwrap(s.last?.r)
            XCTAssertEqual(r.glideTaps, counted + 1, "round \(round): the tap did not come during the glide (nothing tested)")
            XCTAssertTrue(r.newest && r.mode == "following" && r.arrow != true && !(r.frozen ?? true),
                          "round \(round): ↓ during the glide: at the newest end, following, no ↓:\n\(describe(s))")
            XCTAssertFalse(s.contains { $0.r.mode == "reading" }, "round \(round): reading again after ↓:\n\(describe(s))")
            // Quick from anywhere: a screen at most is animated (`ChatJump.longest`, 0.36 s), with room for a busy simulator.
            print("GLIDE \(label) round \(round): ↓ took \(r.jumpMs ?? -1) ms")
            XCTAssertTrue((0...700).contains(r.jumpMs ?? -1), "round \(round): ↓ took \(r.jumpMs ?? -1) ms to the newest end")
        }
    }

    /// At the newest end by ↓ (a tap at rest), as a round of `tapWhileGliding` begins: where the list is, it was put by ↓.
    private func backToTheNewest() {
        if rows()?.newest != true, arrowButton.waitForExistence(timeout: 2) { arrowButton.tap() }
        XCTAssertTrue(waitFor(seconds: 4) { $0.newest && $0.mode == "following" }, "back at the newest end before the round")
        sleep(1)
    }
}
