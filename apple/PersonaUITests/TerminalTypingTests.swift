import XCTest

/// Typing straight into the terminal against scripts/mock/mock_hub.py (TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>):
/// a tap brings the keyboard up on the terminal, keys go out in order (batched), the key bar's modifiers apply to them.
final class TerminalTypingTests: XCTestCase {
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"
    /// TEST_RUNNER_PERSONA_TERMINAL_ENGINE picks the terminal's renderer (the Ghostty branch; empty: our own).
    private var engine: [String] {
        ProcessInfo.processInfo.environment["PERSONA_TERMINAL_ENGINE"].map { ["-terminalEngine", $0] } ?? []
    }

    @MainActor func testKeyboardTypesIntoTheTerminal() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget, "-openPane", "terminal",
                               "-terminalInputMode", "direct", "-terminalTipsSeen", "YES", "-terminalSize", "mirror"]
        app.launchPinned()

        func sent() -> [(keys: [String], text: String)] {
            guard let data = try? Data(contentsOf: URL(string: hub + "/mock/keys")!),
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let list = obj["keys"] as? [[String: Any]] else { return [] }
            return list.map { ($0["keys"] as? [String] ?? [], $0["text"] as? String ?? "") }
        }
        let before = sent().count // the mock keeps what earlier runs sent
        let screen = app.descendants(matching: .any)["terminal-screen"]
        XCTAssertTrue(screen.waitForExistence(timeout: 15), "no terminal:\n\(app.debugDescription)")
        sleep(1)
        app.buttons["键盘"].tap() // only the key bar's button brings the keyboard up (a tap on the screen does not)
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard on the terminal")
        XCTAssertTrue(app.buttons["收起键盘"].exists)

        app.typeText("ls -la\n")
        // ctrl armed on the key bar, then c on the keyboard: ctrl+c.
        app.buttons["Control"].tap()
        app.typeText("c")

        var got: [(keys: [String], text: String)] = []
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline {
            got = Array(sent().dropFirst(before))
            if got.flatMap(\.keys).contains("ctrl+c") { break }
            usleep(300_000)
        }
        // However the keys were batched, they arrive in the order typed.
        var stream: [String] = []
        for r in got { stream.append(contentsOf: r.text.map { String($0) } + r.keys) }
        XCTAssertEqual(stream, "ls -la".map { String($0) } + ["enter", "ctrl+c"], "requests: \(got)")
        XCTAssertLessThan(got.count, 9, "one request per key: \(got)")

        app.buttons["收起键盘"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5), "the keyboard stayed up")
    }

    /// Driving the terminal (MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1): the phone's grid goes up with the
    /// subscribe, keys go out as the terminal's bytes over the WebSocket, and the keyboard coming up gives the
    /// terminal fewer rows.
    @MainActor func testDrivingTheTerminalSendsItsBytes() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget, "-openPane", "terminal",
                               "-terminalInputMode", "direct", "-terminalTipsSeen", "YES", "-terminalSize", "phone"]
        app.launchPinned()

        func input() -> [[String: Any]] {
            guard let data = try? Data(contentsOf: URL(string: hub + "/mock/terminal_input")!),
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
            return obj["input"] as? [[String: Any]] ?? []
        }
        let size = app.descendants(matching: .any)["terminal-size"]
        guard size.waitForExistence(timeout: 15), (size.label.contains("手机尺寸") || {
            sleep(2); return size.label.contains("手机尺寸")
        }()) else { throw XCTSkip("the mock does not drive terminals (MOCK_TERMINAL_CONTROL=1)") }
        let rowsBefore = Int(size.label.split(separator: "×").last ?? "") ?? 0
        let before = input().count
        let screen = app.descendants(matching: .any)["terminal-screen"]
        // A tap on the screen is a click for the program there (Claude's "Jump to bottom"), not the keyboard.
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)).tap()
        sleep(1)
        XCTAssertFalse(app.keyboards.firstMatch.exists, "a tap on the terminal brought the keyboard up")
        let clicks = input().dropFirst(before).compactMap { $0["click"] as? [Int] }
        XCTAssertEqual(clicks.count, 1, "no click for the tap: \(input().dropFirst(before))")
        XCTAssertTrue(clicks.first.map { $0[0] > 0 && $0[1] >= 0 } ?? false, "click at \(clicks)")
        app.buttons["键盘"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard on the terminal")
        sleep(1)
        app.typeText("ls\n")
        app.buttons["Control"].tap()
        app.typeText("c")

        var got: [[String: Any]] = []
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline {
            got = Array(input().dropFirst(before))
            if got.contains(where: { ($0["text"] as? String)?.contains("\u{03}") == true }) { break }
            usleep(300_000)
        }
        XCTAssertEqual(got.compactMap { $0["text"] as? String }.joined(), "ls\r\u{03}", "input: \(got)")
        let resized = got.compactMap { ($0["control"] as? [String: Int])?["rows"] }
        XCTAssertFalse(resized.isEmpty, "no new size with the keyboard up: \(got)")
        // (typeText may switch the simulator to its hardware keyboard afterwards: rows come back, more with the
        // page's header folded)
        XCTAssertLessThan(resized.min() ?? .max, rowsBefore, "the keyboard did not take rows: \(resized)")
        XCTAssertTrue(size.label.hasSuffix("×\(resized.last ?? 0)"), size.label)

        // Closing the terminal (dragging its bar down) lets it go (the pane goes back to the computer's size).
        app.buttons["收起键盘"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5))
        sleep(1)
        let bar = app.descendants(matching: .any)["terminal-bar"] // its handle is in the middle
        bar.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .press(forDuration: 0.05, thenDragTo: app.windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.9)))
        XCTAssertTrue(bar.waitForNonExistence(timeout: 5), "the terminal did not close when its bar was dragged down")
        let gone = Date.now.addingTimeInterval(5)
        while Date.now < gone, !input().dropFirst(before).contains(where: { $0["type"] as? String == "terminal_unsubscribe" }) {
            usleep(200_000)
        }
        XCTAssertTrue(input().dropFirst(before).contains { $0["type"] as? String == "terminal_unsubscribe" }, "still driving after leaving")
    }

    /// Driving the terminal, a drag is its mouse wheel (as in Moshi): down the screen back in history, up forward —
    /// herdr gives it to Claude Code, which scrolls its conversation itself. The view does not scroll or bounce.
    @MainActor func testSwipingIsTheWheelWhileDriving() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget, "-openPane", "terminal",
                               "-terminalInputMode", "direct", "-terminalTipsSeen", "YES", "-terminalSize", "phone"]
        app.launchArguments += engine
        app.launchPinned()
        func input() -> [[String: Any]] {
            guard let data = try? Data(contentsOf: URL(string: hub + "/mock/terminal_input")!),
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
            return obj["input"] as? [[String: Any]] ?? []
        }
        let size = app.descendants(matching: .any)["terminal-size"]
        guard size.waitForExistence(timeout: 15), (size.label.contains("手机尺寸") || {
            sleep(2); return size.label.contains("手机尺寸")
        }()) else { throw XCTSkip("the mock does not drive terminals (MOCK_TERMINAL_CONTROL=1)") }
        sleep(1)
        let before = input().count
        let screen = app.descendants(matching: .any)["terminal-screen"]
        screen.swipeDown()
        sleep(1)
        screen.swipeUp()
        var wheel: [[String: Any]] = []
        var scrolls: [Int] = []
        let deadline = Date.now.addingTimeInterval(8)
        while Date.now < deadline {
            wheel = input().dropFirst(before).filter { $0["scroll"] is Int }
            scrolls = wheel.compactMap { $0["scroll"] as? Int }
            if scrolls.contains(where: { $0 > 0 }), scrolls.contains(where: { $0 < 0 }) { break }
            usleep(300_000)
        }
        XCTAssertTrue(scrolls.contains { $0 > 0 }, "no wheel back in history: \(scrolls)")
        XCTAssertTrue(scrolls.contains { $0 < 0 }, "no wheel forward: \(scrolls)")
        XCTAssertLessThan(scrolls.firstIndex { $0 > 0 } ?? .max, scrolls.firstIndex { $0 < 0 } ?? 0, "out of order: \(scrolls)")
        // Over the cell where the finger went down, as a mouse's wheel (herdr's default 1;1 is where Codex pins the
        // prompt, which takes no wheel): the same cell for all of one swipe, inside the screen.
        let at = wheel.map { $0["at"] as? [Int] ?? [] }
        XCTAssertTrue(at.allSatisfy { $0.count == 2 && $0[0] >= 0 && $0[1] > 0 && $0[1] < 80 }, "wheel without a cell: \(at)")
        let down = at.enumerated().filter { scrolls[$0.offset] > 0 }.map(\.element)
        XCTAssertEqual(Set(down.map { "\($0)" }).count, 1, "one swipe, one cell: \(down)")
    }

    /// The terminal tab opened once the app is connected (as on a phone: the session page first, then Terminal):
    /// the view measures the phone's grid before the first frame, so the takeover starts (build 21 spun forever:
    /// the spinner stood in for the view that tells the grid the subscribe waits for).
    @MainActor func testOpeningTheTerminalWhenConnectedDrivesIt() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget,
                               "-terminalInputMode", "direct", "-terminalTipsSeen", "YES", "-terminalSize", "phone"]
        app.launchPinned()
        let tab = app.buttons["更多"].firstMatch // the terminal is in the header's ··· menu
        XCTAssertTrue(tab.waitForExistence(timeout: 15), "no session page:\n\(app.debugDescription)")
        sleep(3) // connected by now
        let unfold = app.buttons.matching(NSPredicate(format: "label ENDSWITH %@", "显示标题和标签")).firstMatch
        let opened = app.descendants(matching: .any)["terminal-close"] // the terminal's bar: it came up over the page
        for _ in 0..<5 where !opened.exists { // the header may have folded away: scrolling the chat brings it back
            if tab.isHittable, tab.frame.minY > 60 {
                tab.tap()
                let item = app.buttons["终端"].firstMatch
                if item.waitForExistence(timeout: 3) { item.tap() }
            } else if unfold.exists, unfold.isHittable { unfold.tap() } else {
                app.swipeDown()
            }
            sleep(1)
        }
        XCTAssertTrue(opened.exists, "the terminal did not come up")
        let size = app.descendants(matching: .any)["terminal-size"]
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline, !(size.exists && size.label.contains("手机尺寸")) { usleep(250_000) }
        XCTAssertTrue(size.exists && size.label.contains("手机尺寸"),
                      "the terminal did not start: \(size.exists ? size.label : "no size chip")\n\(app.debugDescription)")
    }
}
