import XCTest

/// Your own long message folds (#5676) against scripts/mock/mock_hub.py with MOCK_LONG_INPUT=1 (PERSONA_MOCK_HUB): past
/// 14 lines it shows 10 with Show More (「展开」) at the bubble's bottom, a twelve-line one stays whole; a long press on the
/// folded bubble opens it with no action bar, its top where it was; open, a long press lifts it with the bar as before;
/// Show Less (「收起」) folds it again. `testShots` takes the screenshots for review: before (`-showWholeBubbles`) and after,
/// folded and open, in an Agent chat and in Persona's conversation, light and dark, at the default and the largest text
/// size. PERSONA_SHOTS: a folder the shots are also written to.
final class BubbleFoldUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let agent = "macbook/a1f3c2d4-0000-4000-8000-000005676a00"
    private let persona = "server/b2e4d6f8-0000-4000-8000-000000000001"
    /// The long report's first and last words, and the twelve-line message's last line.
    private let first = "车道 sample-lane"
    private let last = "最后一行在这里"
    private let twelve = "第 12 行"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_LONG_INPUT=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    /// fresh: `-openChatsFresh` (no chat kept on disk, and every fold closed: `OpenReports`).
    @MainActor private func launch(_ chat: String, scheme: String = "light", largest: Bool = false, whole: Bool = false,
                                   fresh: Bool = true) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let open = chat == persona
            ? ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", chat]
            : ["-tab", "dev", "-openSession", chat]
        app.launchArguments = ["-pairURL", pair, "-openChatsFresh", fresh ? "YES" : "NO", "-ui.scheme", scheme,
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"] + open
            + (largest ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : [])
            + (whole ? ["-showWholeBubbles", "YES"] : [])
        app.launchPinned()
        // The app's icon follows the appearance: switching it, the system says so in an alert over the chat (it covered
        // the first shot of a run).
        let notice = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for source in [app!, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let alert = source.alerts.containing(notice).firstMatch
            if alert.waitForExistence(timeout: 2), alert.buttons.firstMatch.exists { alert.buttons.firstMatch.tap(); break }
        }
    }

    @MainActor private func shot(_ name: String) {
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = "fold-" + name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/fold-\(name).png"))
        }
    }

    private func text(_ s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    @MainActor private func press(_ words: XCUIElement) {
        words.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 60, dy: 20)).press(forDuration: 0.8)
    }

    /// Taps Show More where it shows: in the upside-down chat list an element's frame may come flipped (as
    /// MessageLiftUITests' `liftAt` finds), and `isHittable` is then wrong too.
    @MainActor private func tapShowMore() {
        let window = app.windows.firstMatch.frame
        var f = expand.firstMatch.frame
        if !window.insetBy(dx: 0, dy: window.height * 0.12).contains(CGPoint(x: f.midX, y: f.midY)) { f.origin.y = window.height - f.maxY }
        print("FOLD show-more frame \(expand.firstMatch.frame) tapped at y \(f.midY) in \(window)")
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.minX + 24, dy: f.midY)).tap()
    }

    private var expand: XCUIElementQuery { app.buttons.matching(identifier: "bubble.expand") }
    private var collapse: XCUIElement { app.buttons["bubble.collapse"].firstMatch }
    private var bar: XCUIElement { app.staticTexts["复制"].firstMatch }

    /// Folded with Show More; a long press opens it in place with no bar; a second long press lifts it with the bar; Show
    /// Less folds it again. The twelve-line message has no button.
    @MainActor func testALongPressOpensAFoldedMessage() throws {
        launch(agent)
        let words = text(first)
        XCTAssertTrue(words.waitForExistence(timeout: 20), "the chat did not open:\n\(app.debugDescription)")
        sleep(2)
        XCTAssertEqual(expand.count, 1, "Show More under the long message only, not under the twelve-line one")
        XCTAssertTrue(text(twelve).exists, "the twelve-line message is whole")
        let top = words.frame.minY

        // A long press on the folded bubble (its first lines: the words' element runs past the fold): open, no bar, its
        // top where it was.
        press(words)
        XCTAssertTrue(collapse.waitForExistence(timeout: 5), "the long press did not open it")
        sleep(1)
        XCTAssertFalse(bar.exists, "the long press that opened it also lifted it with the bar")
        XCTAssertEqual(words.frame.minY, top, accuracy: 2, "opening moved the bubble's top")
        shot("press-opened")

        // Open, a long press lifts it with the bar as before.
        press(words)
        XCTAssertTrue(bar.waitForExistence(timeout: 5), "open, a long press did not lift it with the bar")
        shot("press-lifted")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.1, dy: 0.15)).tap()
        XCTAssertTrue(bar.waitForNonExistence(timeout: 5), "the bar did not close")
        sleep(1)

        // Show Less folds it again; Show More opens it.
        for _ in 0..<8 where !collapse.isHittable {
            app.swipeUp(velocity: .slow)
            sleep(1)
        }
        collapse.tap()
        XCTAssertTrue(expand.firstMatch.waitForExistence(timeout: 5), "Show Less did not fold it")
        sleep(1)
        tapShowMore()
        XCTAssertTrue(collapse.waitForExistence(timeout: 5), "Show More did not open it")
    }

    /// Open is kept per message: the chat opened again shows it open.
    @MainActor func testOpenIsKept() throws {
        launch(agent)
        XCTAssertTrue(expand.firstMatch.waitForExistence(timeout: 20), "not folded:\n\(app.debugDescription)")
        sleep(1)
        tapShowMore()
        XCTAssertTrue(collapse.waitForExistence(timeout: 5))
        sleep(2) // written off the main thread (StateCache)
        app.terminate()
        launch(agent, fresh: false)
        XCTAssertTrue(text(first).waitForExistence(timeout: 20))
        sleep(2)
        XCTAssertTrue(collapse.exists, "the message folded again")
        foldBack() // as it was, for the other tests
    }

    /// Show Less, scrolled to where it is, then a moment for the open state to be written.
    @MainActor private func foldBack() {
        for _ in 0..<8 where !collapse.isHittable {
            app.swipeUp(velocity: .slow)
            sleep(1)
        }
        if collapse.isHittable { collapse.tap() }
        sleep(2)
    }

    /// The screenshots for review, before and after: each chat, scheme and text size.
    @MainActor func testShots() throws {
        for chat in [agent, persona] {
            for scheme in ["light", "dark"] {
                for largest in [false, true] {
                    let name = "\(chat == agent ? "agent" : "persona")-\(scheme)-\(largest ? "largest" : "default")"
                    launch(chat, scheme: scheme, largest: largest, whole: true)
                    XCTAssertTrue(text(last).waitForExistence(timeout: 20) || text(first).waitForExistence(timeout: 5),
                                  "\(name): the chat did not open")
                    if scheme == "dark" { acceptIconAlert(timeout: 3) }
                    sleep(2)
                    shot(name + "-before")
                    app.terminate()

                    launch(chat, scheme: scheme, largest: largest)
                    XCTAssertTrue(expand.firstMatch.waitForExistence(timeout: 20), "\(name): not folded:\n\(app.debugDescription)")
                    if scheme == "dark" { acceptIconAlert(timeout: 3) }
                    sleep(2)
                    shot(name + "-folded")
                    tapShowMore()
                    if largest, !collapse.waitForExistence(timeout: 5) {
                        // XCUITest has reported Show More's frame off the screen at the largest size (the flipped chat
                        // list): no tap reaches it there. The folded shot is what this size is for.
                        print("FOLD \(name): Show More not reached by the test at the largest size; no opened shot")
                        app.terminate()
                        continue
                    }
                    XCTAssertTrue(collapse.waitForExistence(timeout: 5), "\(name): Show More did not open it")
                    sleep(1)
                    shot(name + "-opened")
                    foldBack() // folded again for the next launch (open is kept)
                    app.terminate()
                }
            }
        }
    }
}
