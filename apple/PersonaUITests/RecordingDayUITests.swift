import XCTest

/// All-day recording on the phone (docs/assistant/day-recap.md, #4741) against a mock hub (PERSONA_MOCK_HUB, with its
/// `recording_day` block: `POST /mock/recording_day {"on", "recap"}`). Made-up days stand in for the watch
/// (`-showDaySample`: yesterday's, closed, and today's, which the watch records or waits on after a call; `consent`: a
/// user who hasn't agreed to the consent sheet yet); the phone uploads them as it would a real day, and the mock writes
/// the recap its mode says. PERSONA_SHOTS=<dir> saves the Today section, the consent sheet, the recap ready, being written
/// and failed, and the pages at the largest accessibility text size (PERSONA_SHOTS_APPEARANCE names the appearance).
final class RecordingDayUITests: XCTestCase {
    private let pastDay = "00000000-0000-0000-0000-0000000000da"
    private let todayDay = "00000000-0000-0000-0000-0000000000db"
    private static let largestText = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
    /// The upload, the hub's recap and the page's next look at it (every 30 s while it waits) take this long at most.
    private static let recapTimeout: TimeInterval = 120

    /// The Today section: the switch mirrors the watch (recording: on), today's row opens the day, yesterday is a row of
    /// Recent Recordings and today's is not listed twice; today's text reaches the hub as a day recording.
    @MainActor func testTodaySection() throws {
        let hub = try mockHub(recap: "pending")
        let app = launch(hub, sample: "today")
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(wait(10) { "\(toggle.value ?? "")" == "1" }, "the switch doesn't show the watch recording: \(toggle.debugDescription)")
        XCTAssertFalse(app.buttons["allday-consent"].exists, "the samples stand in for a user who agreed")
        let today = app.buttons["allday-today"].firstMatch
        XCTAssertTrue(today.exists, app.debugDescription)
        let past = app.buttons["recording-day-row"].firstMatch
        XCTAssertTrue(past.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(app.buttons.matching(identifier: "recording-day-row").count, 1, "today's day is listed twice")
        XCTAssertTrue(wait(60) { (try self.day(hub, self.todayDay)?["next"] as? Int ?? 0) > 0 }, "today's text didn't reach the hub")
        try shot(app, "today")
        today.tap()
        XCTAssertTrue(app.descendants(matching: .any)["day-title"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any)["day-state"].firstMatch.exists, app.debugDescription)
        XCTAssertTrue(app.buttons["day-erase"].firstMatch.exists, "today's open recording offers Erase Last 10 Minutes")
    }

    /// Paused by a call on the watch: the mode is still on, and the line says the tap that continues it is on the watch.
    @MainActor func testPausedByACallSaysWhereToContinue() throws {
        let hub = try mockHub(recap: "pending")
        let app = launch(hub, sample: "paused")
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(wait(10) { "\(toggle.value ?? "")" == "1" }, toggle.debugDescription)
        XCTAssertTrue(wait(5) { toggle.label.contains("继续") || toggle.label.contains("Continue") }, toggle.label)
        try shot(app, "today-paused")
    }

    /// Before the user agreed: the Agree row opens the consent sheet (what is recorded, where it goes, who reads it, how
    /// long it is kept); Not Now changes nothing, and turning the switch on asks first too. Agree stores it, and the row
    /// goes.
    @MainActor func testConsentComesBeforeTheFirstRecording() throws {
        let hub = try mockHub(recap: "none")
        let app = launch(hub, sample: "consent")
        let agreeRow = app.buttons["allday-consent"].firstMatch
        XCTAssertTrue(agreeRow.waitForExistence(timeout: 20), app.debugDescription)
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertEqual("\(toggle.value ?? "")", "0", toggle.debugDescription)
        try shot(app, "consent-row")

        agreeRow.tap()
        let title = app.descendants(matching: .any)["allday-consent-title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(app.descendants(matching: .any).matching(identifier: "allday-consent-point").count, 5, app.debugDescription)
        try shot(app, "consent-sheet")
        app.buttons["allday-consent-not-now"].tap()
        XCTAssertTrue(wait(5) { !title.exists }, "Not Now didn't close the sheet")
        XCTAssertTrue(agreeRow.exists, "Not Now agreed")

        // The switch asks first, and stays off until the user agrees.
        flip(toggle)
        XCTAssertTrue(title.waitForExistence(timeout: 10), "turning the switch on didn't ask for consent:\n\(app.debugDescription)")
        XCTAssertEqual("\(toggle.value ?? "")", "0", "the switch went on before the user agreed")
        app.buttons["allday-consent-agree"].tap()
        XCTAssertTrue(wait(5) { !title.exists }, "Agree didn't close the sheet")
        XCTAssertTrue(wait(5) { !agreeRow.exists }, "the Agree row stays after agreeing")
        // Agreed from the switch: the start went to the watch, which needs a tap there.
        XCTAssertTrue(wait(5) { "\(toggle.value ?? "")" == "1" }, toggle.debugDescription)
    }

    /// The consent sheet at the largest accessibility text size: every point readable, Agree and Not Now within reach.
    @MainActor func testConsentAtTheLargestTextSize() throws {
        let hub = try mockHub(recap: "none")
        let app = launch(hub, sample: "consent", extra: Self.largestText)
        // Below the capture controls at this size: a List builds the row only once it is scrolled to.
        let agreeRow = app.buttons["allday-consent"].firstMatch
        XCTAssertTrue(reveal(app, agreeRow, after: recordingsTop(app)), app.debugDescription)
        agreeRow.tap()
        XCTAssertTrue(app.descendants(matching: .any)["allday-consent-title"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
        try shot(app, "consent-sheet-largest")
        XCTAssertTrue(app.buttons["allday-consent-agree"].isHittable, app.debugDescription)
        XCTAssertTrue(app.buttons["allday-consent-not-now"].isHittable, app.debugDescription)
    }

    /// Erase Last 10 Minutes on today's page: asked first, then the last minutes' text goes from the hub too (a repair that
    /// keeps every other sentence's place), with the range as an erased gap.
    @MainActor func testEraseTheLastTenMinutes() throws {
        let hub = try mockHub(recap: "none")
        let app = launch(hub, sample: "today")
        let today = app.buttons["allday-today"].firstMatch
        XCTAssertTrue(today.waitForExistence(timeout: 20), app.debugDescription)
        // Every made-up line is on the hub before the erase, so the erase has something there to take back.
        var said = 0
        XCTAssertTrue(wait(60) {
            said = try self.spoken(hub, self.todayDay)
            return said >= 6
        }, "today's text didn't reach the hub")
        today.tap()
        let erase = app.buttons["day-erase"].firstMatch
        XCTAssertTrue(erase.waitForExistence(timeout: 10), app.debugDescription)
        reveal(app, erase)
        erase.tap()
        let confirm = app.buttons.matching(NSPredicate(format: "label IN %@", ["删掉", "Erase"])).firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
        try shot(app, "erase-confirm")
        confirm.tap()
        XCTAssertTrue(wait(60) {
            let day = try self.day(hub, self.todayDay)
            let gaps = (day?["coverage"] as? [String: Any])?["gaps"] as? [[String: Any]] ?? []
            return try self.spoken(hub, self.todayDay) < said && gaps.contains { $0["reason"] as? String == "erased" }
        }, "the hub still has the last 10 minutes: \(String(describing: try? day(hub, todayDay)))")
    }

    /// The recap on the day's page: its summary (final: the day is complete), the timeline under the time of day with the
    /// meeting, a suggested to-do, and a time that opens the transcript at its line. The day went up whole.
    @MainActor func testRecapReadyJumpsToTheTranscript() throws {
        let hub = try mockHub(recap: "ready")
        let app = launch(hub, sample: "recap", open: true)
        let summary = app.descendants(matching: .any)["dayrecap-summary"].firstMatch
        XCTAssertTrue(summary.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap")
        // Closed, every seq settled and appended: the phone said the day is complete.
        XCTAssertTrue(wait(60) { try self.day(hub, self.pastDay)?["complete"] as? Bool == true }, "the day never became complete on the hub")
        XCTAssertTrue(app.descendants(matching: .any)["dayrecap-finality"].firstMatch.exists, app.debugDescription)
        let entry = app.descendants(matching: .any)["dayrecap-entry"].firstMatch
        reveal(app, entry)
        XCTAssertTrue(entry.exists, app.debugDescription)
        try shot(app, "recap-timeline")
        let action = app.descendants(matching: .any)["dayrecap-action"].firstMatch
        reveal(app, action)
        XCTAssertTrue(action.exists, "no suggested to-do:\n\(app.debugDescription)")
        XCTAssertFalse(app.buttons["day-erase"].exists, "a closed day offers no erase of its last minutes")
        let time = app.buttons.matching(identifier: "dayrecap-time").firstMatch
        reveal(app, time)
        time.tap()
        let lit = app.descendants(matching: .any)["day-transcript-line-lit"].firstMatch
        XCTAssertTrue(lit.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(lit.isHittable, "the line is not in view")
        try shot(app, "recap-transcript")
    }

    /// Being written: says so, with a spinner.
    @MainActor func testRecapBeingWritten() throws {
        let hub = try mockHub(recap: "pending")
        let app = launch(hub, sample: "pending", open: true)
        let writing = app.descendants(matching: .any)["dayrecap-writing"].firstMatch
        XCTAssertTrue(writing.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap-pending")
    }

    /// Writing it failed: Retry asks the hub again (`POST …/minutes` on the day), and the page follows it to the recap.
    @MainActor func testRecapRetry() throws {
        let hub = try mockHub(recap: "failed")
        let app = launch(hub, sample: "failed", open: true)
        let retry = app.buttons["dayrecap-retry"].firstMatch
        XCTAssertTrue(retry.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap-failed")
        // Settled first, so no later upload rewrites the recap and moves its version.
        XCTAssertTrue(wait(60) { try self.day(hub, self.pastDay)?["complete"] as? Bool == true }, "the day never became complete on the hub")
        // The page looks again by itself once more while the day's summary catches up with the hub (its recap's state, the
        // day complete): only after that is a recap it shows the one Retry asked for.
        XCTAssertTrue(pageSettled(hub), "the page kept reading the hub's list by itself")
        XCTAssertTrue(retry.waitForExistence(timeout: 5), app.debugDescription)
        // The mock writes recaps from now on (its switch writes one at once, version 1); a failed page doesn't look again by
        // itself, so the recap it then shows, one version on, is the one Retry asked for.
        try switchMock(hub, ["recap": "ready"])
        let before = try recapVersion(hub, pastDay) ?? 0
        reveal(app, retry)
        retry.tap()
        XCTAssertTrue(wait(10) { (try self.recapVersion(hub, self.pastDay) ?? 0) > before }, "Retry didn't reach the hub")
        XCTAssertTrue(app.descendants(matching: .any)["dayrecap-summary"].firstMatch.waitForExistence(timeout: 60), app.debugDescription)
    }

    /// The largest accessibility text size: the Today section and the recap still lay out (shots), and their controls
    /// can still be reached.
    @MainActor func testLargestTextSize() throws {
        let hub = try mockHub(recap: "ready")
        var app = launch(hub, sample: "today", extra: Self.largestText)
        // Below the fold at this size: rows a List hasn't scrolled to don't exist yet, so it scrolls before it waits.
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(reveal(app, toggle, after: recordingsTop(app)), app.debugDescription)
        try shot(app, "today-largest")
        app.terminate()

        app = launch(hub, sample: "recap", open: true, extra: Self.largestText)
        // The recap comes in under the day's header, which fills more than a screen at this size.
        let summary = app.descendants(matching: .any)["dayrecap-summary"].firstMatch
        XCTAssertTrue(reveal(app, summary, after: app.descendants(matching: .any)["day-title"].firstMatch, timeout: Self.recapTimeout),
                      app.debugDescription)
        try shot(app, "recap-largest")
        let time = app.buttons.matching(identifier: "dayrecap-time").firstMatch
        XCTAssertTrue(reveal(app, time), app.debugDescription)
    }

    /// Without a day and without a paired watch the Recordings page is as it was: no Today section, no day rows (an
    /// earlier test's made-up days go when the page appears).
    @MainActor func testWithoutAllDayRecordingThePageIsUnchanged() throws {
        let hub = try mockHub(recap: "none")
        let app = launch(hub, sample: nil)
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(wait(10) { !app.switches["allday-switch"].exists && !app.buttons["recording-day-row"].exists },
                      app.debugDescription)
        sleep(2) // nothing brings them back
        XCTAssertFalse(app.switches["allday-switch"].exists, app.debugDescription)
        XCTAssertFalse(app.buttons["recording-day-row"].exists, app.debugDescription)
    }

    /// A hub without `recording_day`: the day is still shown, with a caution that its text stays on the phone.
    @MainActor func testHubWithoutTheFlagSaysTheRecapNeedsAnUpdate() throws {
        let hub = try mockHub(recap: "none", on: false)
        defer { try? switchMock(hub, ["on": true]) }
        let app = launch(hub, sample: "today")
        let notice = app.descendants(matching: .any)["allday-hub-update"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue(app.switches["allday-switch"].exists, app.debugDescription)
        try shot(app, "hub-update")
    }

    // MARK: Helpers

    @MainActor private func launch(_ hub: String, sample: String?, open: Bool = false, extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings"]
            + (sample.map { ["-showDaySample", $0] } ?? [])
            + (open ? ["-openDayRecap", "YES"] : [])
            + extra
        app.launchPinned()
        return app
    }

    /// Scrolls until `element` exists and is on screen, then says whether it is: a List builds a row only once it is
    /// scrolled to, so an element below the fold never appears by waiting (the largest text size puts most of these
    /// below it). First waits for `anchor` (the page's top, so nothing is swiped before the page is there), then swipes
    /// up six times and back down six, again and again until `timeout`: an element that comes later (the recap, once the
    /// hub wrote it) is found wherever it lands.
    @MainActor @discardableResult
    private func reveal(_ app: XCUIApplication, _ element: XCUIElement, after anchor: XCUIElement? = nil,
                        timeout: TimeInterval = 30) -> Bool {
        if let anchor { _ = anchor.waitForExistence(timeout: 20) }
        let deadline = Date.now.addingTimeInterval(timeout)
        var swipes = 0
        while Date.now < deadline {
            if element.exists, element.isHittable { return true }
            if (swipes / 6).isMultiple(of: 2) { app.swipeUp() } else { app.swipeDown() }
            swipes += 1
        }
        return element.exists && element.isHittable
    }

    /// The Recordings page's first control: there once the page is.
    @MainActor private func recordingsTop(_ app: XCUIApplication) -> XCUIElement {
        app.buttons["recording-meeting-start"].firstMatch
    }

    /// Flips a `SettingsSwitchRow`'s switch: the switch itself when it is its own element, else the row's trailing end,
    /// where it is. A tap on the row's middle lands on its label, which doesn't flip it.
    @MainActor private func flip(_ toggle: XCUIElement) {
        let control = toggle.switches.firstMatch
        if control.exists {
            control.tap()
        } else {
            toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        }
    }

    /// The mock hub, starting this test fresh: no recordings stored (an earlier test's recaps and versions go;
    /// `/mock/recording_minutes` resets them), the hub listing `recording_day` (`on`) and writing recaps as `recap` says
    /// (`ready`, `pending`, `failed`, `none`).
    private func mockHub(recap: String, on: Bool = true) throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs its own mock hub (PERSONA_MOCK_HUB)") }
        _ = try post(hub, "/mock/recording_minutes", ["mode": "pending"])
        try switchMock(hub, ["on": on, "recap": recap])
        return hub
    }

    /// `POST /mock/recording_day`: only the switches sent change; the answer is all of them.
    private func switchMock(_ hub: String, _ body: [String: Any]) throws {
        let state = try post(hub, "/mock/recording_day", body)
        // A mock without main's all-day block answers this route with an error, not its switches.
        XCTAssertNotNil(state["recap"], "the mock hub has no recording_day switches (/mock/recording_day)")
        // As JSON values (a Bool comes back as an NSNumber).
        for (key, value) in body { XCTAssertEqual(state[key] as? NSObject, value as? NSObject, "the mock didn't take \(key)") }
    }

    private func post(_ hub: String, _ path: String, _ body: [String: Any]) throws -> [String: Any] {
        var request = URLRequest(url: URL(string: hub + path)!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        var answer = Data()
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { data, _, _ in
            answer = data ?? Data()
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 5)
        return (try? JSONSerialization.jsonObject(with: answer)) as? [String: Any] ?? [:]
    }

    /// A recording as the hub holds it (`/mock/recordings`: its last body with `next`, `coverage`, `complete`,
    /// `recap_state`…); nil when it has none.
    private func day(_ hub: String, _ id: String) throws -> [String: Any]? {
        let data = try Data(contentsOf: URL(string: hub + "/mock/recordings")!)
        let state = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        return (state?["recordings"] as? [String: Any])?[id] as? [String: Any]
    }

    /// The day's sentences on the hub that have text (an erased one keeps its place, empty).
    private func spoken(_ hub: String, _ id: String) throws -> Int {
        let segments = try day(hub, id)?["segments"] as? [[String: Any]] ?? []
        return segments.filter { !($0["text"] as? String ?? "").isEmpty }.count
    }

    /// How often the app read the hub's recordings list (`/mock/hits`), the page's recap look among them.
    private func listReads(_ hub: String) throws -> Int {
        let data = try Data(contentsOf: URL(string: hub + "/mock/hits")!)
        let hits = (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["hits"] as? [String: Any]
        return hits?["GET /api/v1/recordings"] as? Int ?? 0
    }

    /// The app stopped reading the hub's recordings list by itself: no new read for 3 seconds.
    private func pageSettled(_ hub: String) -> Bool {
        var reads = -1
        var since = Date.now
        return wait(30) {
            let now = try self.listReads(hub)
            if now != reads {
                reads = now
                since = .now
            }
            return Date.now.timeIntervalSince(since) >= 3
        }
    }

    /// The version of the day's recap on the hub (`detail.recap.version`); nil before one was written.
    private func recapVersion(_ hub: String, _ id: String) throws -> Int? {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/kb/recordings/\(id)")!)
        let answer = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        let detail = answer?["detail"] as? [String: Any]
        return (detail?["recap"] as? [String: Any])?["version"] as? Int
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let image = app.screenshot()
        let attachment = XCTAttachment(screenshot: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try image.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/allday-\(appearance)-\(name).png"))
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func wait(_ seconds: TimeInterval, _ ok: () throws -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if (try? ok()) == true { return true }
            usleep(200_000)
        }
        return false
    }
}
