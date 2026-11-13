import XCTest

/// New Agent in Claude's desktop app (#5480), against scripts/mock/mock_hub.py with MOCK_DESKTOP_APPS=1 (the MacBook
/// offers the launcher claude-desktop): with no folder picked the session goes to the app's scratch folder (the start
/// carries no folder); browsing starts where the app's projects are, and New Folder… names one there. PERSONA_SHOTS
/// saves the screens (PERSONA_STYLE names the appearance the simulator is in).
final class DesktopStartUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        _ = try call("/mock/reset")
        let launchers = (try call("/api/v1/machines/macbook/launchers")["launchers"] as? [[String: Any]]) ?? []
        guard launchers.contains(where: { $0["name"] as? String == "claude-desktop" }) else {
            throw XCTSkip("needs MOCK_DESKTOP_APPS=1")
        }
        app = XCUIApplication()
    }

    private func launch() {
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-showNewChat", "YES",
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launchPinned()
    }

    @discardableResult
    private func call(_ path: String) throws -> [String: Any] {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        var out: Data?
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: req) { d, _, _ in out = d; done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
        return (try? JSONSerialization.jsonObject(with: out ?? Data()) as? [String: Any]) ?? [:]
    }

    /// The last start the hub got (POST /api/v1/sessions), once there is one with the desktop app's launcher.
    private func lastDesktopStart() throws -> [String: Any] {
        let end = Date().addingTimeInterval(10)
        while Date() < end {
            let started = (try call("/mock/started")["started"] as? [[String: Any]]) ?? []
            if let s = started.last(where: { $0["launcher"] as? String == "claude-desktop" }) { return s }
            usleep(300_000)
        }
        XCTFail("no start with claude-desktop")
        return [:]
    }

    @MainActor @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "missing: \(what)")
        return e
    }

    @MainActor private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("desktop-start-\(name)\(style).png"))
        }
    }

    /// The MacBook, and its claude-desktop launcher from the launcher list.
    @MainActor private func pickMacBook() {
        see(app.buttons["newChat.directory"], "the directory row", timeout: 15)
        see(app.buttons["newChat.machine.macbook"], "MacBook card").tap()
        see(app.buttons["newChat.launcher"], "the launcher row", timeout: 15).tap()
        see(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "claude-desktop")).firstMatch, "claude-desktop").tap()
        let launcher = see(app.buttons["newChat.launcher"], "the launcher row")
        XCTAssertTrue(launcher.label.contains("claude-desktop"), "launcher row: \(launcher.label)")
    }

    /// The desktop app's launcher with no folder picked: a temporary session, ready to start, sent with no folder.
    @MainActor func testNoFolderStartsATemporarySession() throws {
        launch()
        pickMacBook()
        let row = app.buttons["newChat.directory"]
        let end = Date().addingTimeInterval(10)
        while Date() < end, !row.label.contains("Temporary Session") { usleep(300_000) }
        XCTAssertTrue(row.label.contains("Temporary Session"), "directory row: \(row.label)")
        XCTAssertFalse(app.staticTexts["newChat.notReady"].exists, "it is ready with no folder")
        XCTAssertFalse(app.buttons["newChat.temporarySession"].exists, "nothing to go back to")
        shot("temporary")
        see(app.buttons["Start Chat"], "Start Chat").tap()
        let start = try lastDesktopStart()
        XCTAssertEqual(start["cwd"] as? String ?? "", "", "a temporary session is sent with no folder: \(start)")
    }

    /// Browsing starts where the app's projects are; New Folder… names one there, and going back to a temporary session
    /// is one tap.
    @MainActor func testANewFolderWhereTheProjectsAre() throws {
        launch()
        pickMacBook()
        see(app.buttons["newChat.directory"], "the directory row").tap()
        let path = see(app.textFields["dirBrowser.path"], "the path field", timeout: 15)
        let end = Date().addingTimeInterval(10)
        while Date() < end, (path.value as? String) != "/Users/me/code" { usleep(300_000) }
        XCTAssertEqual(path.value as? String, "/Users/me/code", "browsing starts in the folder of the app's projects")
        see(app.buttons["dirBrowser.newFolder"], "New Folder…")
        shot("browser")
        app.buttons["dirBrowser.newFolder"].tap()
        let name = see(app.textFields["Folder Name"], "the folder name field")
        name.typeText("report-2026")
        shot("new-folder")
        see(app.alerts.buttons["Use"], "Use").tap()
        let row = see(app.buttons["newChat.directory"], "the directory row")
        XCTAssertTrue(row.label.contains("report-2026"), "directory row: \(row.label)")
        see(app.buttons["newChat.temporarySession"], "Use a Temporary Session")
        shot("picked")
        see(app.buttons["Start Chat"], "Start Chat").tap()
        let start = try lastDesktopStart()
        XCTAssertEqual(start["cwd"] as? String, "/Users/me/code/report-2026", "\(start)")
    }
}
