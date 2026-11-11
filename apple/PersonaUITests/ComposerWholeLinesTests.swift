import XCTest

/// The composer shows whole lines while typing a draft of several lines with the keyboard up (#3703): on the open iPhone
/// Duo on its side the window is short, and the box cut a line of its text in half at its top.
/// The open iPhone Duo simulator (Device Hub), TEST_RUNNER_PERSONA_ORIENTATION=landscape; TEST_RUNNER_PERSONA_HOLD=<s>
/// holds each state (`duo-shot <name>`) for `simctl io --display=3 screenshot`. Against scripts/mock/mock_hub.py.
final class ComposerWholeLinesTests: XCTestCase {
    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    @MainActor func testADraftOfSeveralLinesShowsWholeLines() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        XCUIDevice.shared.orientation = ProcessInfo.processInfo.environment["PERSONA_ORIENTATION"] == "landscape" ? .landscapeLeft : .portrait
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-showComposerPhoto", "YES", "-probes", "YES"]
        app.launchPinned()
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        field.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard")
        sleep(1)
        if let v = field.value as? String, !v.isEmpty, !v.hasPrefix("问 ") {
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: v.count + 2))
        }
        for line in 1...6 { field.typeText("第 \(line) 行：我们点设置是不是应该和它的一样" + (line < 6 ? "\n" : "")) }
        sleep(1)
        shot(app, "typed")
        let box = probe(app)
        XCTAssertNotNil(box, "no composer probe")
        guard let box else { return }
        XCTAssertGreaterThanOrEqual(box.count, 6, "the draft is not in the box: \(box)")
        XCTAssertFalse(box.cutTop, "the box cuts a line at its top: \(box); the field \(field.frame), the keyboard \(app.keyboards.firstMatch.frame)\n\(app.debugDescription)")
        XCTAssertFalse(box.cutFoot, "the box cuts a line at its foot: \(box)")
    }

    /// The composer's text view (`probe.composer-lines`, DEBUG): its lines, and whether one is cut at its top or its foot.
    private struct Lines: Decodable, CustomStringConvertible {
        var count: Int; var cutTop: Bool; var cutFoot: Bool; var visible: Double; var offset: Double
        var maximumLines: Int; var viewport: Double; var line: Double
        var description: String {
            "\(count) lines, cut at the top \(cutTop), at the foot \(cutFoot), shown \(visible) from \(offset); up to \(maximumLines) lines of \(line) in \(viewport)"
        }
    }

    @MainActor private func probe(_ app: XCUIApplication) -> Lines? {
        app.probe("composer-lines", as: Lines.self)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        XCTContext.runActivity(named: "duo-shot composer-\(name)") { _ in
            if let hold = ProcessInfo.processInfo.environment["PERSONA_HOLD"].flatMap(UInt32.init) { sleep(hold) }
        }
    }
}
