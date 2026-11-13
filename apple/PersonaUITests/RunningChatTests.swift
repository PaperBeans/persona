import XCTest

/// A Persona conversation in the middle of a long turn, tool calls one after another, nobody touching the screen
/// (2026-11-03: the chat jumped between two places about once a second, then went blank for good). Whatever the pushes
/// do, the content area is never empty and the top line on screen stays where it is (or, at the newest, the newest
/// stays in view). Samples the pixels (what is drawn, not what the model holds) and `probe.chat-rows`.
///   PORT=18797 MOCK_ASSISTANT_RUN=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_RUN_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/RunningChatTests
final class RunningChatTests: XCTestCase {
    private var hub = ""
    private var token = "mock"
    private var chat = RunningChatTests.main
    private var app: XCUIApplication!
    /// The mock's main Persona conversation (ASK1), running with MOCK_ASSISTANT_RUN=1.
    private static let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_RUN_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_ASSISTANT_RUN=1 (PERSONA_RUN_HUB)")
        }
        hub = h
        // A real hub, for a check by hand against a throwaway conversation (never a user's): its token and the chat.
        let env = ProcessInfo.processInfo.environment
        if let t = env["PERSONA_RUN_TOKEN"], !t.isEmpty { token = t }
        if let c = env["PERSONA_RUN_CHAT"], !c.isEmpty { chat = c }
        continueAfterFailure = true
    }

    private struct Rows: Decodable {
        var items: Int
        var lines: Int
        var visible: Int
        var top: String?
        var topY: Int?
        var newest: Bool
        var follows: Bool
        var offset: Int?
        var content: Int?
        var frozen: Bool?
        var ids: [String]?
        var x: Int?
        var mode: String?
        var gap: Int?
        var held: Bool?
    }

    private func launch(_ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + token
        app.launchArguments = ["-pairURL", pair]
        app.launchPinned()
        sleep(3)
        app.terminate()
        app.launchArguments = ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", chat, "-probes", "YES"] + extra
        app.launchPinned()
    }

    /// The share of the content area (between the top bar and the composer) that is not background: 0 is blank.
    private func ink() -> Double {
        guard let cg = XCUIScreen.main.screenshot().image.cgImage else { return -1 }
        let w = 90, h = 180 // enough to see text, cheap to count
        var px = [UInt8](repeating: 0, count: w * h * 4)
        guard let ctx = CGContext(data: &px, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                  space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return -1 }
        ctx.interpolationQuality = .medium
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        // Rows from the top: CGContext's first row is the image's top. The band leaves out the top bar and the composer.
        var lum: [Double] = []
        for y in Int(Double(h) * 0.18)..<Int(Double(h) * 0.80) {
            for x in 0..<w {
                let i = (y * w + x) * 4
                lum.append((0.299 * Double(px[i]) + 0.587 * Double(px[i + 1]) + 0.114 * Double(px[i + 2])) / 255)
            }
        }
        let background = lum.sorted()[lum.count / 2]
        return Double(lum.filter { abs($0 - background) > 0.12 }.count) / Double(lum.count)
    }

    /// The follow state and the list's place in a sample, for a jump's message.
    private static func state(_ r: Rows) -> String {
        "top \(r.top.map { String($0.suffix(24)) } ?? "–") y \(r.topY ?? -1) \(r.mode ?? "?") held \((r.held ?? false) ? 1 : 0) frozen \((r.frozen ?? false) ? 1 : 0) gap \(r.gap ?? -1) offset \(r.offset ?? -1) content \(r.content ?? -1) items \(r.items)"
    }

    /// The running turn's work line, by its header (`work-head`): the newest work line on screen, the lowest, which in a
    /// chat at its newest end is the running one (an opened line's steps have no header of their own). Not by its words:
    /// since #4249 a running line names the step it is on instead of saying Working (#5394). Each use looks again
    /// (`var work: XCUIElement { runningWork() }`), as the list's rows come and go.
    private func runningWork() -> XCUIElement {
        let heads = app.buttons.matching(identifier: "work-head")
        let height = app.frame.height
        let shown = heads.allElementsBoundByIndex.filter { $0.exists && $0.frame.maxY > 0 && $0.frame.minY < height }
        return shown.max { $0.frame.midY < $1.frame.midY } ?? heads.firstMatch
    }

    func testARunningTurnNeitherBlanksNorJumps() {
        launch()
        var first: Rows?
        let ready = Date.now
        while Date.now.timeIntervalSince(ready) < 20 {
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self),
               r.items > 0, r.visible > 0 { first = r; break }
            Thread.sleep(forTimeInterval: 0.2)
        }
        XCTAssertNotNil(first, "the running conversation shows")
        sleep(3) // landed
        // As the user had it: the running work line open, and its first step open on its output.
        var work: XCUIElement { runningWork() } // by its header, looked up at each use (#5394)
        XCTAssertTrue(work.waitForExistence(timeout: 10), "the running work line")
        work.tap()
        // Opened at the newest end the header rises under the top bar over about half a second (#1123): asked whether it
        // can be tapped while it moves, XCUITest could not tell.
        sleep(1)
        // Up to where the turn starts (the user reading it from the top, not at the newest), then its first step open
        // on its output, as in the recording.
        for _ in 0..<6 {
            if work.isHittable && work.frame.minY > 120 { break }
            app.swipeDown(velocity: .slow)
            sleep(1)
        }
        let step = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "运行")).firstMatch
        if step.waitForExistence(timeout: 10), step.isHittable { step.tap() }
        if ProcessInfo.processInfo.environment["PERSONA_RUN_BACKGROUND"] == "1" {
            // Away for a while and back (the socket reconnects, the chat catches up and lands where it was read).
            sleep(2)
            XCUIDevice.shared.press(.home)
            sleep(8)
            app.activate()
        }
        sleep(2)

        sample(seconds: 60, label: "running chat")
    }

    /// Opening the running work line at the newest end of a chat taller than the screen shows its steps: the header goes
    /// up toward the top bar and its steps come in under it. Held where it was tapped, the steps opened under the
    /// composer, out of sight, and the tap seemed to do nothing; the next tap folded it, and what had arrived in
    /// between came in at once, moving the page up.
    func testOpeningAtTheNewestShowsTheSteps() {
        launch()
        let ready = Date.now
        while Date.now.timeIntervalSince(ready) < 20 {
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self),
               r.visible > 0, r.newest { break }
            Thread.sleep(forTimeInterval: 0.2)
        }
        sleep(3) // landed at the newest
        var work: XCUIElement { runningWork() } // by its header, looked up at each use (#5394)
        XCTAssertTrue(work.waitForExistence(timeout: 10) && work.isHittable, "the running work line's header on screen")
        let y0 = work.frame.minY
        work.tap() // open
        sleep(2) // its steps loaded and the hold over
        XCTAssertTrue(work.isHittable, "the header stays on screen")
        // XCUITest calls a step under the composer hittable too: what shows is where the header went. Its steps run
        // past the screen, so it goes up most of the way (it stayed within a point of where it was).
        let rose = y0 - work.frame.minY
        print(String(format: "TRUTH opened at the newest: header went up %.0f pt", rose))
        XCTAssertGreaterThan(rose, y0 / 2, "the header goes up for its steps to show")
    }

    /// Samples the content area's ink and `probe.chat-rows` for `seconds`: fails on a blank content area, a line-less
    /// list, or the top line moving by itself (away from the newest).
    /// `settle`: a blank in the first seconds does not count.
    private func sample(seconds: Double, label: String, settle: Double = 0, file: StaticString = #filePath, line: UInt = #line) {
        var blank: [Double] = [], jumps: [String] = [], inks: [Double] = []
        var last: Rows?
        let start = Date.now
        while Date.now.timeIntervalSince(start) < seconds {
            let t = Date.now.timeIntervalSince(start)
            let k = ink()
            inks.append(k)
            if inks.count % 7 == 1 { // a picture every couple of seconds, to compare with what users see
                let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
                shot.name = String(format: "\(label) %.1f s", t)
                shot.lifetime = .keepAlways
                add(shot)
            }
            if k >= 0, k < 0.004, t >= settle { blank.append(t) }
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self) {
                if r.visible == 0 {
                    jumps.append(String(format: "%.1f s: no line on screen (%d lines, offset %d of content %d, frozen %d) ",
                                        t, r.lines, r.offset ?? -1, r.content ?? -1, (r.frozen ?? false) ? 1 : 0) + (r.ids ?? []).joined(separator: ",") + " x \(r.x ?? 0)")
                }
                if let l = last, !l.newest, !r.newest {
                    if l.top != r.top {
                        jumps.append(String(format: "%.1f s: top line %@ → %@", t, l.top ?? "–", r.top ?? "–"))
                    } else if let a = l.topY, let b = r.topY, abs(a - b) > 2 {
                        jumps.append(String(format: "%.1f s: top line moved %d pt (", t, b - a) + Self.state(l) + " → " + Self.state(r) + ")")
                    }
                }
                if let l = last, l.newest != r.newest {
                    jumps.append(String(format: "%.1f s: %@ the newest", t, r.newest ? "back at" : "left"))
                }
                last = r
            }
            Thread.sleep(forTimeInterval: 0.3)
        }
        let sorted = inks.filter { $0 >= 0 }.sorted()
        print(String(format: "TRUTH %@: %d samples, ink min %.4f median %.4f, blank %d, jumps %d",
                     label, sorted.count, sorted.first ?? -1, sorted.isEmpty ? -1 : sorted[sorted.count / 2], blank.count, jumps.count))
        for j in jumps.prefix(20) { print("TRUTH \(label): " + j) }
        XCTAssertTrue(blank.isEmpty, label + ": the content area went blank at " + blank.map { String(format: "%.1f s", $0) }.joined(separator: ", "))
        XCTAssertTrue(jumps.isEmpty, label + ": the chat moved by itself:\n" + jumps.prefix(12).joined(separator: "\n"))
    }

    /// Opening a running work line from its header and folding it again, in a conversation that fits on one screen
    /// folded (a new one, MOCK_ASSISTANT_RUN=alone), as the user did (2026-11-03): folded, the chat went blank and
    /// stayed so until the app was killed (#686). Opened, the header stays on screen and the chat is never blank, not
    /// even for the frame right after the tap (#754).
    func testFoldingARunningWorkLineKeepsTheChat() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_RUN_ALONE"] == "1" else {
            throw XCTSkip("needs MOCK_ASSISTANT_RUN=alone (PERSONA_RUN_ALONE=1)")
        }
        launch()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 5) { allow.tap() }
        let ready = Date.now
        while Date.now.timeIntervalSince(ready) < 20 {
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self), r.visible > 0 { break }
            Thread.sleep(forTimeInterval: 0.2)
        }
        sleep(3)
        var work: XCUIElement { runningWork() } // by its header, looked up at each use (#5394)
        XCTAssertTrue(work.waitForExistence(timeout: 10) && work.isHittable, "the running work line's header on screen")
        let y0 = work.frame.minY
        work.tap() // open
        if ProcessInfo.processInfo.environment["PERSONA_TRACE"] == "1" { trace(seconds: 4, label: "open") }
        sample(seconds: 3, label: "opened")
        XCTAssertTrue(work.exists && work.isHittable, "the header stays on screen when its work line opens")
        if work.exists {
            print(String(format: "TRUTH opened: header moved %.0f pt", work.frame.minY - y0))
        }
        if work.exists && work.isHittable {
            work.tap() // and folded again
            if ProcessInfo.processInfo.environment["PERSONA_TRACE"] == "1" { trace(seconds: 3, label: "fold") }
            sample(seconds: 4, label: "folded again")
        }
    }

    private func mock(_ what: String) {
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: URL(string: hub + "/mock/" + what)!) { _, _, _ in done.signal() }.resume()
        _ = done.wait(timeout: .now() + 5)
    }

    /// A recorded turn replayed (MOCK_ASSISTANT_REPLAY, the items outside the repo): read from its start, the work
    /// line and its first call's whole output open, the next calls pushed, then the work line folded from its header
    /// `PERSONA_FOLD_AT` seconds after a text of the agent's starts streaming (it is written 1.5 s in).
    func testReplayFold() throws {
        guard let at = ProcessInfo.processInfo.environment["PERSONA_FOLD_AT"].flatMap(Double.init) else {
            throw XCTSkip("needs MOCK_ASSISTANT_REPLAY and PERSONA_FOLD_AT")
        }
        // As the user had it, tapping no rows (XCUITest places rows of the upside-down list wrong): the calls whose
        // output was cut (the first one's, 4 KB) open by themselves once the work line is opened from its header.
        launch(["-showFullOutput", "card"])
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 8) { allow.tap() }
        let ready = Date.now
        while Date.now.timeIntervalSince(ready) < 20 {
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self), r.visible > 0 { break }
            Thread.sleep(forTimeInterval: 0.2)
        }
        sleep(3)
        var work: XCUIElement { runningWork() } // by its header, looked up at each use (#5394)
        XCTAssertTrue(work.waitForExistence(timeout: 10) && work.isHittable, "the running work line's header on screen")
        let headerY = work.frame.minY
        work.tap() // open
        trace(seconds: 2.5, label: "open")
        sample(seconds: 3, label: "replay open")
        XCTAssertTrue(work.isHittable, "the header stays on screen when its work line opens")
        if work.isHittable, abs(work.frame.minY - headerY) > 2 {
            print(String(format: "TRUTH replay open: header moved %.0f pt", work.frame.minY - headerY))
        }
        mock("replay?n=1") // the next call
        sleep(2)
        mock("replay?n=1") // its result
        sleep(2)
        // Back up to the header (the open first call under it), as the user was: drags on the chat, not on rows.
        for _ in 0..<8 where !(work.exists && work.isHittable && work.frame.minY > 90) {
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            from.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.70)))
            sleep(1)
        }
        XCTAssertTrue(work.exists && work.isHittable, "the work line's header, scrolled back to")
        sample(seconds: 3, label: "replay read")
        mock("replay?n=1") // a text: streams, then written
        Thread.sleep(forTimeInterval: at)
        if work.exists && work.isHittable { work.tap() }
        trace(seconds: 2.5, label: "fold")
        sample(seconds: 12, label: String(format: "replay fold at %.1f s", at))
    }

    /// The probe as fast as it can be read, for `seconds`: each change of where the list is and what it shows.
    private func trace(seconds: Double, label: String) {
        let start = Date.now
        var last = ""
        while Date.now.timeIntervalSince(start) < seconds {
            if let r = app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self) {
                let line = "lines \(r.lines) visible \(r.visible) top \((r.top ?? "–").suffix(14)) y \(r.topY ?? -1) offset \(r.offset ?? -1) content \(r.content ?? -1) newest \(r.newest ? 1 : 0) follows \(r.follows ? 1 : 0) frozen \((r.frozen ?? false) ? 1 : 0) x \(r.x ?? 0)"
                if line != last {
                    print(String(format: "TRUTH trace %@ %.2f s: ", label, Date.now.timeIntervalSince(start)) + line)
                    last = line
                }
            }
        }
    }
}
