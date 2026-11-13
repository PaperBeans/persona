import XCTest

/// `launchPinned()` keeps the style a test passes itself (#5683). It used to append the run's style after the test's,
/// and the later `-ui.style` won, so a test asking for Clay or BeautifulUI ran in Liquid Glass. Checks the arguments
/// only; nothing is launched.
final class UITestStylePinTests: XCTestCase {
    func testAStyleTheTestPassesIsKept() {
        let arguments = ["-pairURL", "persona://pair", "-ui.style", "clay", "-ui.scheme", "dark"]
        XCTAssertEqual(UITestStyle.pinned(arguments), arguments)
    }

    func testWithoutOneTheRunsStyleIsAdded() {
        XCTAssertEqual(UITestStyle.pinned(["-ui.scheme", "dark"]), ["-ui.scheme", "dark", "-ui.style", UITestStyle.name])
    }
}
