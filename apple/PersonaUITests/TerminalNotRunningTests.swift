import XCTest

/// The terminal of a session that isn't running (a finished task's Agent whose tab the hub closed, #4328), over the page:
/// it says so, offers Resume Session, and closes as in every other state — its Close, or a drag down on it. Before, the
/// no-terminal state took the bar with it and nothing closed the cover. Needs scripts/mock/mock_hub.py, where 「菜谱网站加搜索」
/// is offline (with MOCK_TERMINAL_STREAM=1 its stream ends "not running", as the hub's does; without, its screen reads
/// offline):
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/TerminalNotRunningTests
final class TerminalNotRunningTests: XCTestCase {
    /// 「菜谱网站加搜索」 on the MacBook, offline in the mock.
    private let recipe = "a1f3c2d4-0000-4000-8000-000000000007"

    override func setUp() { continueAfterFailure = false }

    /// The session page with its terminal come up over it, showing the no-terminal state.
    @MainActor private func openTerminal(_ extra: [String] = []) throws -> (XCUIApplication, XCUIElement) {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + recipe, "-openTerminalPane", "YES",
                               "-terminalTipsSeen", "YES"] + extra
        app.launchPinned()
        let state = app.descendants(matching: .any)["terminal-not-running"].firstMatch
        XCTAssertTrue(state.waitForExistence(timeout: 20), "no no-terminal state:\n\(app.debugDescription)")
        return (app, state)
    }

    @MainActor func testCloseIsThereAndClosesIt() throws {
        let (app, state) = try openTerminal()
        let close = app.buttons["terminal-close"]
        XCTAssertTrue(close.exists && close.isHittable, "no Close over a session that isn't running")
        XCTAssertGreaterThanOrEqual(close.frame.width, 44, "Close is smaller than a tap target")
        XCTAssertGreaterThanOrEqual(close.frame.height, 44, "Close is smaller than a tap target")
        XCTAssertTrue(app.buttons["恢复会话"].exists, "no Resume Session for a Claude session")
        XCTAssertFalse(app.buttons["terminal-keyboard"].exists, "the key bar is there with nothing to type into")
        close.tap()
        XCTAssertTrue(state.waitForNonExistence(timeout: 5), "Close did not close the terminal")
        XCTAssertFalse(close.exists, "the terminal's bar is still there")
    }

    @MainActor func testDraggingItDownClosesIt() throws {
        let (app, state) = try openTerminal()
        state.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.15))
            .press(forDuration: 0.05, thenDragTo: app.windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.95)))
        XCTAssertTrue(state.waitForNonExistence(timeout: 5), "dragging the no-terminal state down did not close it")
    }

    /// Resume Session brings the session back (the mock's start with resume_session_id), and the terminal shows in place.
    @MainActor func testResumeSessionOpensItsTerminal() throws {
        let (app, state) = try openTerminal()
        app.buttons["恢复会话"].tap()
        XCTAssertTrue(state.waitForNonExistence(timeout: 20), "the terminal did not come after Resume Session")
        XCTAssertTrue(app.buttons["terminal-close"].exists, "no Close over the resumed terminal")
        XCTAssertTrue(app.descendants(matching: .any)["terminal-screen"].firstMatch.waitForExistence(timeout: 10), "no terminal screen")
    }
}
