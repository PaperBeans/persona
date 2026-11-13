import XCTest

/// Talk about this meeting on the iPhone (#3862 phase 6; contract docs/assistant/meeting-minutes.md「Phase 6: talk about
/// this meeting」, the phone's side docs/apple/recording.md「Talk about this meeting (phase 6)」), end to end against a mock
/// hub (PERSONA_MOCK_HUB): a finished meeting no room attended (`-showMeetingSample minutes`, made up, uploaded at launch)
/// offers **Talk about this meeting** on its page; the row opens the meeting's one thread, whose composer makes the
/// follow-up with the first question (`POST …/followup`), and the follow-up's answer shows in the thread. The app runs in
/// zh-Hans: elements are found by their identifiers. PERSONA_SHOTS=<dir> saves talk-<appearance>-<name>.png.
final class TalkAboutMeetingUITests: XCTestCase {
    private let past = "00000000-0000-0000-0000-000000000003"

    override func setUpWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        // A fresh start (the hub forgets the recordings, follow-ups and receipts) that writes the minutes.
        try post(hub, "/mock/recording_minutes", ["mode": "ready"])
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "off"])
    }

    @MainActor func testTalkAboutAFinishedMeetingWritesItsFollowUp() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings",
                               "-showMeetingSample", "minutes", "-openChatsFresh", "YES"]
        app.launchPinned()
        let talk = element(app, "recording-talk")
        XCTAssertTrue(talk.waitForExistence(timeout: 30), "no Talk about this meeting:\n\(app.debugDescription)")
        // The minutes come in too: the picture has the page once they have settled (the cards fade in over the
        // pending line for a moment after Copy appears).
        _ = app.buttons["minutes-copy"].waitForExistence(timeout: 45)
        sleep(2)
        try shot(app, "detail")

        talk.tap()
        let field = element(app, "meeting-ask-field")
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no thread:\n\(app.debugDescription)")
        XCTAssertFalse(element(app, "meeting-after-divider").exists, "no room before the follow-up: no divider")
        XCTAssertTrue(element(app, "meeting-followup-empty").waitForExistence(timeout: 5), app.debugDescription)
        // No room to wait for: the composer takes words once the hub has the recording.
        XCTAssertTrue(waitFor(30) { field.isEnabled }, "the composer stays off:\n\(app.debugDescription)")
        try shot(app, "start")
        field.tap()
        field.typeText("把今天的决定整理成给供应商的邮件")
        let send = app.buttons["meeting-ask-send"].firstMatch
        XCTAssertTrue(waitFor(10) { send.isEnabled }, "Send stays off:\n\(app.debugDescription)")
        send.tap()

        XCTAssertTrue(waitFor(30) { (try? self.meeting(hub, self.past))?["followup"] is [String: Any] },
                      "…/followup never made the follow-up")
        let answer = element(app, "meeting-thread-answer")
        XCTAssertTrue(answer.waitForExistence(timeout: 30), "no answer in the follow-up:\n\(app.debugDescription)")
        XCTAssertFalse(element(app, "meeting-followup-empty").exists, "the hint goes once the follow-up has started")
        // The keyboard is down after a send; the answer is the thread's end, which it follows.
        try shot(app, "thread")
    }

    // MARK: Helpers

    @MainActor private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    private func waitFor(_ seconds: TimeInterval = 10, _ ok: () -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if ok() { return true }
            usleep(250_000)
        }
        return ok()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/talk-\(appearance)-\(name).png"))
    }

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func post(_ hub: String, _ path: String, _ body: [String: Any]) throws {
        var request = URLRequest(url: URL(string: hub + path)!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    /// The mock's meeting.json of a recording (`GET /mock/meeting_conversation` → `meetings[id]`): room, private, followup.
    private func meeting(_ hub: String, _ id: String) throws -> [String: Any]? {
        let data = try Data(contentsOf: URL(string: hub + "/mock/meeting_conversation")!)
        let all = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        return (all?["meetings"] as? [String: Any])?[id] as? [String: Any]
    }
}
