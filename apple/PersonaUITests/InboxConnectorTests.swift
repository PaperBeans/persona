import XCTest

/// A connector's items in the inbox (GitHub…, docs/plans/plan-connectors.md「App side」): the generic row with the
/// connector's tag, mark and thread, a filter for the source, the detail (a pull request as GitHub's thread) and
/// Open in GitHub, and AI Draft Reply whose Send comments on the thread through the hub. Against scripts/mock/mock_hub.py with
/// MOCK_GITHUB_INBOX=1 (PERSONA_MOCK_HUB), which lists them only to apps asking ?sources=all and records replies at
/// /mock/sent.
final class InboxConnectorTests: XCTestCase {
    @MainActor func testAGitHubItemReadsAndReplies() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", ""]
        app.launchPinned()
        func shot(_ name: String) {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "\(name) \(UITestStyle.name)"
            a.lifetime = .keepAlways
            add(a)
        }
        func text(_ s: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
        }

        let row = text("Add retries to the example uploader")
        guard row.waitForExistence(timeout: 20) else {
            XCTFail("no GitHub item (does the app ask for ?sources=all?):\n\(app.debugDescription)")
            return
        }
        XCTAssertTrue(text("请你 review").exists, "no connector tag")
        XCTAssertTrue(text("example-org/example-repo#12 · alice：请看一下重试的退避时间").exists, "no thread line")
        XCTAssertTrue(app.buttons["GitHub"].exists || text("GitHub").exists, "no GitHub filter")
        shot("inbox github row")

        row.tap()
        XCTAssertTrue(app.buttons["在 GitHub 打开"].waitForExistence(timeout: 10), "no 在 GitHub 打开:\n\(app.debugDescription)")
        XCTAssertTrue(text("上传失败时按指数退避重试三次").waitForExistence(timeout: 10), "the thread's text did not load")
        // A pull request opens as GitHub's thread (#1055, GitHubThreadTests), with its own box at the bottom.
        XCTAssertTrue(app.descendants(matching: .any)["thread.box"].exists, "no box to comment")
        shot("inbox github detail")

        let draft = app.buttons["AI 草拟回复"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 5))
        draft.tap()
        XCTAssertTrue(app.navigationBars["评论 example-repo#12"].waitForExistence(timeout: 10), "no reply sheet:\n\(app.debugDescription)")
        let editor = app.textViews.firstMatch
        let drafted = NSPredicate(format: "value CONTAINS %@", "收到，我看一下") // the mock's inbox_draft
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: drafted, evaluatedWith: editor)], timeout: 10), .completed, "no draft")
        shot("inbox github reply")
        app.navigationBars.buttons["发送"].tap()

        let deadline = Date.now.addingTimeInterval(8)
        var sent = ""
        while Date.now < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!), let s = String(data: data, encoding: .utf8),
               s.contains("github:review12") {
                sent = s
                break
            }
            usleep(250_000)
        }
        XCTAssertTrue(sent.contains("收到，我看一下"), "the reply did not reach the hub: \(sent)")
    }

    /// A source subscription's item (#5537, MOCK_SUBSCRIPTION_INBOX=1): its source is the subscription, not a connector,
    /// and it opens to Open in … and its content with no error card or Retry, whether the hub answers its thread request
    /// with a page that is no conversation or, as older hubs did, with 404 no connector. Light and dark of each are
    /// attached, and kept in TEST_RUNNER_PERSONA_SHOTS=<dir> when set (TEST_RUNNER_PERSONA_PHASE names them).
    @MainActor func testASubscriptionItemOpensToItsContent() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_SUBSCRIPTION_INBOX=1 (PERSONA_MOCK_HUB)")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        for (mode, name) in [("1", "hub"), ("old", "older-hub")] {
            _ = try Data(contentsOf: URL(string: hub + "/mock/subscription_inbox?mode=" + mode)!)
            for scheme in ["light", "dark"] {
                let app = XCUIApplication()
                app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", "",
                                       "-openInboxItem", "sub_example01:t1", "-ui.scheme", scheme]
                app.launchPinned()
                func text(_ s: String) -> XCUIElement {
                    app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
                }
                XCTAssertTrue(text("停车场 A、B 区当天上午临时关闭").waitForExistence(timeout: 20),
                              "\(name) \(scheme): the content did not show:\n\(app.debugDescription)")
                let openIn = NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", "示例邮件", "打开")
                XCTAssertTrue(app.buttons.matching(openIn).firstMatch.exists, "\(name) \(scheme): no Open in")
                let file = "subscription-item-\(env["PERSONA_PHASE"] ?? "after")-\(name)-\(scheme)"
                let s = app.screenshot()
                let a = XCTAttachment(screenshot: s)
                a.name = file
                a.lifetime = .keepAlways
                add(a)
                if let dir = env["PERSONA_SHOTS"] {
                    try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(file + ".png"))
                }
                XCTAssertFalse(text("no connector").exists, "\(name) \(scheme): the hub's error is shown")
                XCTAssertFalse(app.buttons["重试"].exists, "\(name) \(scheme): a Retry is shown")
                app.terminate()
            }
        }
    }
}
