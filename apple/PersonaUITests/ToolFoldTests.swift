import XCTest

/// The last turn's tool rows in an agent's chat, opened and closed while the agent keeps working (#3005): what is open
/// stays open as steps arrive, as the turn ends and as the next one starts; the row tapped last never goes down or off
/// the screen (at the newest end it rises with what comes in, up to the top bar, #3757); a row closes at once, leaving
/// no blank space and no fading copy of what it showed.
///   PORT=18951 MOCK_TOOL_TURN=1 python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18951 xcodebuild test … -only-testing:PersonaUITests/ToolFoldTests
/// `PERSONA_TOOL_TURN_HUB` comes first, so one run can take these tests and others with a mock of their own.
final class ToolFoldTests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private static let chat = "macbook/a1f3c2d4-0000-4000-8000-0000000000f5"

    private struct Fold: Decodable {
        var what: String
        var frames: Int
        var moved: Int
        var end: Int?
        var step: Int?
        var ys: [Int?]
        var heights: [Int?]?
        /// How high a row at the newest end may rise (window y, `ChatPin.riseTop`).
        var top: Int?
    }

    override func setUpWithError() throws {
        let env = ProcessInfo.processInfo.environment
        guard let url = env["PERSONA_TOOL_TURN_HUB"] ?? env["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs MOCK_TOOL_TURN=1 (PERSONA_MOCK_HUB)") }
        hub = url
        continueAfterFailure = true
        mock("reset")
    }

    override func tearDown() {
        mock("reset")
        super.tearDown()
    }

    @discardableResult
    private func mock(_ step: String) -> String {
        guard let url = URL(string: hub + "/mock/tool_turn?step=" + step) else { return "" }
        let done = DispatchSemaphore(value: 0)
        var body = ""
        URLSession.shared.dataTask(with: url) { data, _, _ in
            body = data.map { String(decoding: $0, as: UTF8.self) } ?? ""
            done.signal()
        }.resume()
        _ = done.wait(timeout: .now() + 5)
        return body
    }

    private func launch() {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-probes", "YES", "-unfoldTrace", "YES", "-scrollTrace", "YES", "-openChatsFresh", "YES",
                               "-app.tab", "chats", "-tab", "dev", "-dev.openChat", Self.chat]
        app.launchPinned()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons
            .matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if allow.waitForExistence(timeout: 6) { allow.tap() }
    }

    private func row(_ subject: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label CONTAINS %@", subject)).firstMatch
    }

    private func text(_ value: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", value)).firstMatch
    }

    /// What a row's output shows, by a line only it has.
    private let readOutput = "One per source."
    private let findOutput = "PR #2647  OPEN"

    /// The newest fold measurement (`UnfoldTrace`), once one newer than `after` is in.
    private func fold(after: Int) -> Fold? {
        for _ in 0..<30 {
            if let seq = app.probeSeq("unfold", page: "agents") ?? app.probeSeq("unfold"), seq != after,
               let all = app.probe("unfold", as: [Fold].self, page: "agents") ?? app.probe("unfold", as: [Fold].self) {
                return all.last
            }
            Thread.sleep(forTimeInterval: 0.2)
        }
        return nil
    }

    private func foldSeq() -> Int { app.probeSeq("unfold", page: "agents") ?? app.probeSeq("unfold") ?? 0 }

    /// Taps a row's head and waits out its measurement (`UnfoldTrace.window`, 3 s), asking the app nothing meanwhile.
    private func tap(_ e: XCUIElement) -> Fold? {
        let before = foldSeq()
        e.tap()
        Thread.sleep(forTimeInterval: 3.2)
        return fold(after: before)
    }

    /// The row the user reads is still there, no lower than it was and no higher than the top bar: kept still, or at the
    /// newest end risen with what came in (#3757), never pushed off.
    private func assertKept(_ e: XCUIElement, from y: CGFloat, top: Int?, _ when: String, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(e.exists, "\(when): the row is still there", file: file, line: line)
        XCTAssertLessThanOrEqual(e.frame.minY, y + 2, "\(when): the row the user reads went down", file: file, line: line)
        XCTAssertGreaterThanOrEqual(e.frame.minY, CGFloat(top ?? 0) - 3, "\(when): the row went past the top bar", file: file, line: line)
    }

    /// Opens the running turn's work line (and, in the BeautifulUI style, its run of chips, so each call has a row).
    private func openWork() {
        XCTAssertTrue(app.buttons["work-head"].firstMatch.waitForExistence(timeout: 20), "the running turn's work line shows")
        sleep(2)
        // The newest work line is the lowest one on screen (the earlier turns have theirs above it).
        let heads = app.buttons.matching(identifier: "work-head").allElementsBoundByIndex.filter { $0.isHittable }
        guard let work = heads.max(by: { $0.frame.minY < $1.frame.minY }) else { return XCTFail("no work line on screen") }
        work.tap()
        let run = app.buttons["tool-run"].firstMatch
        if run.waitForExistence(timeout: 3) {
            sleep(2)
            run.tap()
        }
    }

    /// Opens the work line and two of its rows, then lets a step, the turn's end and the next turn come in; closes one.
    @MainActor func testOpenRowsStayOpenAndStillAsTheTurnGoesOn() throws {
        launch()
        openWork()
        let read = row("Read the open question on #2037"), find = row("Find the PR that closed it")
        XCTAssertTrue(read.waitForExistence(timeout: 5), "its steps are listed")
        sleep(2)

        // Opened: its output shows in full at once.
        let open = tap(read)
        XCTAssertTrue(text(readOutput).exists, "the opened row shows its output")
        assertAtOnce(open, "open read")
        var y = read.frame.minY

        // A new step arrives under it: the row stays open, and where it was.
        mock("call")
        XCTAssertTrue(row("Ask the lane for its status").waitForExistence(timeout: 5), "the new step shows")
        sleep(2)
        XCTAssertTrue(text(readOutput).exists, "still open after a step came in")
        assertKept(read, from: y, top: open?.top, "a step came in")
        y = read.frame.minY
        mock("result")
        sleep(2)
        XCTAssertTrue(text(readOutput).exists, "still open after the step's result came in")
        assertKept(read, from: y, top: open?.top, "a result came in")

        // A second row opened: both stay open. From here the row tapped last is the one kept: at the newest end it rises
        // with what comes in, up to the top bar, the first one above it going up with it (#3757).
        _ = tap(find)
        XCTAssertTrue(text(findOutput).exists, "the second row shows its output")
        XCTAssertTrue(text(readOutput).exists, "the first one still does")
        var y2 = find.frame.minY

        // The turn ends with the answer, then the queued message starts the next one.
        mock("answer")
        XCTAssertTrue(text("静音开关（#2036）还没开始").waitForExistence(timeout: 5), "the answer shows")
        sleep(2)
        XCTAssertTrue(text(readOutput).exists && text(findOutput).exists, "both rows still open after the turn ended")
        assertKept(find, from: y2, top: open?.top, "the turn ended")
        y2 = find.frame.minY
        mock("next")
        sleep(3)
        XCTAssertTrue(text(readOutput).exists && text(findOutput).exists, "both rows still open after the next turn started")
        assertKept(find, from: y2, top: open?.top, "the next turn started")

        // Closed, the row tapped last: gone at once with its space (no blank, no fading copy): the rows under it come
        // up, or, at the newest end, the closed row comes down to them (#3757); the row under it never goes down.
        let under = row("Ask the lane for its status")
        let yUnder = under.frame.minY
        let closed = tap(find)
        XCTAssertFalse(text(findOutput).exists, "the closed row's output is gone")
        XCTAssertTrue(text(readOutput).exists, "the other row stays open")
        XCTAssertLessThanOrEqual(under.frame.minY, yUnder + 2, "the row under it did not go down")
        assertAtOnce(closed, "close find after the next turn")
    }

    /// The list's height changed within two frames of the tap: what closed (or opened) went with its space at once,
    /// not after fading out over it.
    private func assertAtOnce(_ fold: Fold?, _ what: String, file: StaticString = #filePath, line: UInt = #line) {
        guard let h = fold?.heights else { return XCTFail("\(what): not measured", file: file, line: line) }
        let known = h.compactMap { $0 }
        let first = known.first ?? 0
        let changed = known.firstIndex { abs($0 - first) > 2 }
        print("FOLD \(what): moved \(fold?.moved ?? -1) heights \(known.prefix(20).map(String.init).joined(separator: " "))")
        XCTAssertNotNil(changed, "\(what): the list's height changed", file: file, line: line)
        XCTAssertLessThanOrEqual(changed ?? .max, 2, "\(what): within two frames of the tap, not after a fade", file: file, line: line)
    }

    /// Opened and closed with nothing coming in: each at once, the row where it was.
    @MainActor func testARowOpensAndClosesAtOnce() throws {
        launch()
        openWork()
        let read = row("Read the open question on #2037")
        XCTAssertTrue(read.waitForExistence(timeout: 5))
        sleep(2)
        let opened = tap(read)
        XCTAssertTrue(text(readOutput).exists)
        assertAtOnce(opened, "open read")
        let closed = tap(read)
        XCTAssertFalse(text(readOutput).exists, "the closed row's output is gone")
        assertAtOnce(closed, "close read")
        // Where it was kept, it stays; risen at the newest end with what opened (#3757), it comes back down by as much
        // at most, gliding.
        let rose = max(-(opened?.end ?? 0), 0)
        XCTAssertLessThanOrEqual(closed?.moved ?? .max, rose + 1, "the tapped row stayed where it was, or came back down to it")
        XCTAssertLessThanOrEqual(closed?.step ?? .max, 160, "no jump in one frame")
    }

    /// What is open survives the lazy list dropping its rows: scrolled far into the history and back.
    @MainActor func testOpenRowsSurviveScrollingAway() throws {
        launch()
        openWork()
        let find = row("Find the PR that closed it")
        XCTAssertTrue(find.waitForExistence(timeout: 5))
        sleep(2)
        _ = tap(find)
        XCTAssertTrue(text(findOutput).exists)
        for _ in 0..<4 { app.swipeDown(velocity: .fast) }
        sleep(1)
        let down = app.buttons.matching(NSPredicate(format: "label IN %@", ["回到最新", "Jump to Latest"])).firstMatch
        if down.waitForExistence(timeout: 3) { down.tap() } else { for _ in 0..<4 { app.swipeUp(velocity: .fast) } }
        sleep(2)
        for _ in 0..<4 where !text(findOutput).exists { app.swipeDown(velocity: .slow) }
        XCTAssertTrue(text(findOutput).exists, "the row is still open when it comes back")
    }
}
