import XCTest

/// Several groups of hubs on one phone (docs/protocol/multi-hub.md), against three scripts/mock_hub.py: A alone (「家里」), B and C one
/// group (「公司」, each lists the other in its hello's mesh; C owns the MacBook). Both groups have a MacBook with the same
/// machine and session ids and the same interaction ids, so a request sent to the wrong group's hub shows. Start them
/// as docs/operations/testing.md「Multiple hubs」 says and pass TEST_RUNNER_PERSONA_HUB_A / _B / _C (http://127.0.0.1:<port>).
final class MultiHubTests: XCTestCase {
    private struct Hubs { var a: String; var b: String; var c: String }

    private func hubs() throws -> Hubs {
        let env = ProcessInfo.processInfo.environment
        guard let a = env["PERSONA_HUB_A"], let b = env["PERSONA_HUB_B"], let c = env["PERSONA_HUB_C"] else {
            throw XCTSkip("needs three scripts/mock_hub.py (PERSONA_HUB_A, PERSONA_HUB_B, PERSONA_HUB_C)")
        }
        return Hubs(a: a, b: b, c: c)
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func launch(_ h: Hubs, _ args: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--", "-pairURL", pair(h.a), "-pairURL2", pair(h.b), "-pairURL3", pair(h.c),
                               "-openAgentsFresh", "YES", "-openChatsFresh", "YES", "-dev.openChat", ""] + args
        app.launchPinned()
        dismissNotificationPrompt()
        return app
    }

    /// A launch without -pairURL before this one (the unit tests' host app) asked for notifications: the system's
    /// alert can still be up, over the app (and the app inactive under it).
    private func dismissNotificationPrompt() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["不允许", "Don’t Allow", "Don't Allow"] where springboard.alerts.buttons[label].waitForExistence(timeout: 1) {
            springboard.alerts.buttons[label].tap()
            return
        }
    }

    private func mock(_ hub: String, _ path: String) -> [String: Any] {
        guard let data = try? Data(contentsOf: URL(string: hub + path)!),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return obj
    }

    private func resolved(_ hub: String) -> [String] {
        (mock(hub, "/mock/resolved")["resolved"] as? [[String: Any]] ?? []).compactMap { $0["id"] as? String }
    }

    private func shot(_ name: String) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["好", "OK"] where springboard.alerts.buttons[label].waitForExistence(timeout: 1) {
            springboard.alerts.buttons[label].tap()
            _ = springboard.alerts.firstMatch.waitForNonExistence(timeout: 2)
            break
        }
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }

    private func text(_ app: XCUIApplication, containing s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    /// The Agents list's ⋯ menu: show one group only (All Hubs: every group).
    private func showOnly(_ app: XCUIApplication, _ group: String) {
        let menu = app.buttons.matching(NSPredicate(format: "label BEGINSWITH '分组' OR label BEGINSWITH '当前'")).firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 5), "no ⋯ menu:\n\(app.debugDescription)")
        menu.tap()
        let item = app.buttons[group].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no \(group) in the menu:\n\(app.debugDescription)")
        item.tap()
    }

    /// A chat's pictures come from its own group's hub while another group is the current one (#1555: they were asked
    /// of the current group's hub, which did not have them, and stayed on Retry). Home (A) runs with
    /// `MOCK_TURN_IMAGES=many` (the blog chat on its studio machine, whose last turn looked at five pictures; Work keeps
    /// no studio), said with TEST_RUNNER_PERSONA_HUB_A_TURN_IMAGES=1.
    @MainActor func testAChatsPicturesComeFromItsOwnHub() throws {
        let h = try hubs()
        guard ProcessInfo.processInfo.environment["PERSONA_HUB_A_TURN_IMAGES"] == "1" else {
            throw XCTSkip("hub A with MOCK_TURN_IMAGES=many (PERSONA_HUB_A_TURN_IMAGES=1)")
        }
        let app = launch(h, ["-tab", "dev", "-openHub", "2", "-openSession", "studio/a1f3c2d4-0000-4000-8000-000000000003",
                             "-openImagesFresh", "YES"])
        let tile = app.buttons["viewed-image-0"]
        XCTAssertTrue(tile.waitForExistence(timeout: 30), "no viewed images in Home's blog chat:\n\(app.debugDescription)")
        let notLoaded = "图片没载入，点按重试"
        let tiles = (0..<5).map { app.buttons["viewed-image-\($0)"] }
        // Loaded: every tile there has its picture's caption, none says it did not load.
        let deadline = Date.now.addingTimeInterval(20)
        while Date.now < deadline, tiles.contains(where: { $0.exists && $0.label == notLoaded }) || tile.label.isEmpty {
            usleep(500_000)
        }
        shot("pictures from their own hub")
        XCTAssertFalse(tiles.contains { $0.exists && $0.label == notLoaded }, "a picture did not load: \(tiles.map(\.label))")
        tile.tap()
        XCTAssertTrue(app.descendants(matching: .any)["viewer-page-0"].waitForExistence(timeout: 10), "the viewer has no picture")
        // And none was asked of Work's hubs, which do not have them.
        for (name, hub) in [("B", h.b), ("C", h.c)] {
            XCTAssertEqual(mock(hub, "/mock/images?asked=1")["asked"] as? Int, 0, "a picture was asked of hub \(name)")
        }
    }

    /// The Agents list shows both groups' requests, each saying its group, and can show one group only; Allow on each
    /// goes to that group's hub only (both groups have the same interaction id on the same machine and session ids).
    @MainActor func testMergedListAndAnswersGoToTheirOwnHub() throws {
        let h = try hubs()
        let app = launch(h, ["-tab", "dev"])
        // The cards that need you; not by their section's title, 「需要你」, which the filter chip says too.
        let needs = app.descendants(matching: .any).matching(identifier: "agents.interaction").firstMatch
        XCTAssertTrue(needs.waitForExistence(timeout: 20), "no cards that need you")
        let home = text(app, containing: "MacBook · 家里")
        XCTAssertTrue(home.waitForExistence(timeout: 10), "the first group's MacBook does not say its group:\n\(app.debugDescription)")
        shot("merged")

        // Only the second group: its request shows, the first group's do not.
        showOnly(app, "公司")
        let work = text(app, containing: "工作 MacBook · 公司")
        XCTAssertTrue(work.waitForExistence(timeout: 10), "no request from the second group:\n\(app.debugDescription)")
        XCTAssertFalse(text(app, containing: "· 家里").exists, "the hub filter left the first group's rows")
        shot("second group only")

        let beforeA = resolved(h.a), beforeWork = resolved(h.b) + resolved(h.c)
        let workCard = app.cells.containing(NSPredicate(format: "label CONTAINS %@", "工作 MacBook · 公司")).firstMatch
        XCTAssertTrue(workCard.buttons["允许"].waitForExistence(timeout: 5), "no 允许 on the second group's card")
        workCard.buttons["允许"].tap()
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline, resolved(h.b).count + resolved(h.c).count == beforeWork.count { usleep(200_000) }
        let work2 = resolved(h.b) + resolved(h.c)
        XCTAssertEqual(work2.count, beforeWork.count + 1, "the second group's hubs did not get the answer")
        XCTAssertEqual(resolved(h.a), beforeA, "the first group's hub got the second group's answer")

        // Every group again, and the first group's own (the same interaction id, at the other hub).
        showOnly(app, "全部 hub")
        let homeCard = app.cells.containing(NSPredicate(format: "label CONTAINS %@", "MacBook · 家里")).firstMatch
        XCTAssertTrue(homeCard.buttons["允许"].waitForExistence(timeout: 10), "no 允许 on the first group's card")
        homeCard.buttons["允许"].tap()
        let d2 = Date.now.addingTimeInterval(10)
        while Date.now < d2, resolved(h.a).count == beforeA.count { usleep(200_000) }
        XCTAssertEqual(resolved(h.a).count, beforeA.count + 1, "the first group's hub did not get its answer")
        XCTAssertEqual((resolved(h.b) + resolved(h.c)).count, work2.count, "the second group got the first group's answer")
        shot("answered")
    }

    /// Settings › hub lists the two groups (B and C one group); disconnecting 「公司」 leaves 「家里」 and its agents as they were.
    @MainActor func testDisconnectingOneGroupLeavesTheOther() throws {
        let h = try hubs()
        let app = launch(h, ["-tab", "settings", "-openHubs", "YES"])
        XCTAssertTrue(app.navigationBars["hub"].waitForExistence(timeout: 20), "no hub page:\n\(app.debugDescription)")
        // Two groups: the second one has both its hubs.
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "18761")).firstMatch
            .waitForExistence(timeout: 10))
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "18762")).firstMatch.exists)
        let removes = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'hubs.remove.'"))
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline, removes.count < 2 { usleep(200_000) } // B and C merge once their hellos are in
        XCTAssertEqual(removes.count, 2, "B and C did not become one group:\n\(app.debugDescription)")
        shot("hubs")
        // The second group's Disconnect This Group (the default group is first).
        removes.element(boundBy: 1).tap()
        let confirm = app.buttons["断开这一组"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        let gone = NSPredicate(format: "count == 1")
        expectation(for: gone, evaluatedWith: removes)
        waitForExpectations(timeout: 10)
        shot("one group left")

        // Back to one group: Agents is what it was with one hub (no group names), the first hub's agents all there.
        let relaunch = XCUIApplication()
        relaunch.launchArguments = ["-pairURL", pair(h.a), "-openAgentsFresh", "YES", "-dev.openChat", "", "-tab", "dev"]
        relaunch.launchPinned()
        XCTAssertTrue(text(relaunch, containing: "补齐网络层的单元测试").waitForExistence(timeout: 20))
        XCTAssertFalse(text(relaunch, containing: "· 家里").exists, "one group but rows still name it")
        XCTAssertFalse(text(relaunch, containing: "公司").exists, "the disconnected group's rows are still there")
    }

    /// New Chat, switching to the other group's MacBook: the first MacBook's directories go as soon as it is picked, not
    /// when the other hub answers, and a hub that does not answer says so (they stayed under the second card, and the
    /// directory browser showed only "Can't open" with an empty path).
    @MainActor func testNewChatOnTheOtherGroupsMachine() throws {
        let h = try hubs()
        // No -newChat.machine: a launch argument outranks what the sheet saves, so the picked card would never change.
        let app = launch(h, ["-tab", "dev"])
        defer { for hub in [h.b, h.c] { _ = mock(hub, "/mock/delay?s=0"); _ = mock(hub, "/mock/down?s=0") } }
        let newChat = app.buttons["新建会话"]
        XCTAssertTrue(newChat.waitForExistence(timeout: 20), "no 新建会话:\n\(app.debugDescription)")
        newChat.tap()
        // The machine cards (labelled name, then group), not the Agents rows under the sheet that name the machine too.
        let home = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "MacBook")).firstMatch
        let work = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "工作 MacBook")).firstMatch
        let recent = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "weather")).firstMatch
        XCTAssertTrue(home.waitForExistence(timeout: 15), "no MacBook card:\n\(app.debugDescription)")
        home.tap()
        XCTAssertTrue(recent.waitForExistence(timeout: 15), "the first group's MacBook lists no recent directory:\n\(app.debugDescription)")
        XCTAssertTrue(work.exists, "no MacBook from the second group")
        shot("first group")

        // The second group's hubs answer slowly: picking its MacBook empties the list at once, then fills it with its own.
        for hub in [h.b, h.c] { _ = mock(hub, "/mock/delay?s=6") }
        work.tap()
        let gone = NSPredicate(format: "exists == false")
        expectation(for: gone, evaluatedWith: recent)
        waitForExpectations(timeout: 2)
        XCTAssertTrue(app.buttons["newChat.directory"].label.contains("选择目录"), app.buttons["newChat.directory"].label)
        XCTAssertFalse(app.buttons["开始对话"].isEnabled, "Start Chat with the other machine's directory")
        shot("second group loading")
        XCTAssertTrue(recent.waitForExistence(timeout: 30), "the second group's MacBook never listed its own directories")
        for hub in [h.b, h.c] { _ = mock(hub, "/mock/delay?s=0") }

        // Back to the first, then the second while its hubs are gone: nothing of the first stays, and it says why.
        home.tap()
        XCTAssertTrue(recent.waitForExistence(timeout: 15))
        for hub in [h.b, h.c] { _ = mock(hub, "/mock/down?s=120") }
        work.tap()
        expectation(for: gone, evaluatedWith: recent)
        waitForExpectations(timeout: 2)
        let error = app.descendants(matching: .any).matching(identifier: "newChat.error").firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 45), "no word on the hub out of reach:\n\(app.debugDescription)")
        XCTAssertTrue(error.label.contains("连不上这台电脑所在的 hub"), error.label)
        XCTAssertFalse(app.buttons["开始对话"].isEnabled)
        shot("second group out of reach")

        app.buttons["newChat.directory"].tap()
        let retry = app.buttons["dirBrowser.retry"]
        XCTAssertTrue(retry.waitForExistence(timeout: 45), "the directory browser did not offer Retry:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "连不上这台电脑所在的 hub").exists, "the directory browser does not say the hub is out of reach")
        shot("directory browser out of reach")
    }

    /// The hub the second group is on goes away: the group goes on through its other hub.
    @MainActor func testGroupMovesToItsOtherHub() throws {
        let h = try hubs()
        let app = launch(h, ["-tab", "settings", "-openHubs", "YES"])
        XCTAssertTrue(app.navigationBars["hub"].waitForExistence(timeout: 20), "no hub page:\n\(app.debugDescription)")
        func row(_ port: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'hubs.hub.' AND label CONTAINS %@", port)).firstMatch
        }
        let b = row("18761"), c = row("18762")
        XCTAssertTrue(b.waitForExistence(timeout: 10) && c.exists)
        let deadline = Date.now.addingTimeInterval(15)
        while Date.now < deadline, !b.label.contains("在用"), !c.label.contains("在用") { usleep(300_000) }
        let onB = b.label.contains("在用")
        XCTAssertTrue(onB || c.label.contains("在用"), "neither of the second group's hubs is in use: \(b.label) / \(c.label)")
        let (down, other) = onB ? (h.b, c) : (h.c, b)
        _ = mock(down, "/mock/down?s=40")
        let moved = NSPredicate(format: "label CONTAINS '在用'")
        expectation(for: moved, evaluatedWith: other)
        waitForExpectations(timeout: 30)
        shot("moved")
        _ = mock(down, "/mock/down?s=0")
    }

    /// A rejected credential belongs to one group, not to the merged title while another group remains online (#1872).
    /// Both hubs of the work group refuse the device so its route cannot recover through the other member.
    @MainActor func testRevokedGroupSaysPairingExpiredAndOffersPairAgain() throws {
        let h = try hubs()
        let scheme = ProcessInfo.processInfo.environment["PERSONA_SCHEME"] ?? "light"
        let app = launch(h, ["-tab", "settings", "-openHubs", "YES", "-ui.scheme", scheme])
        defer { for hub in [h.b, h.c] { _ = mock(hub, "/mock/reject?s=0") } }
        XCTAssertTrue(app.navigationBars["hub"].waitForExistence(timeout: 20), "no hub page:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "公司 2").waitForExistence(timeout: 15), "B and C did not become one group:\n\(app.debugDescription)")

        for hub in [h.b, h.c] { _ = mock(hub, "/mock/reject?s=30") }
        XCTAssertTrue(text(app, containing: "公司、在用").waitForNonExistence(timeout: 10), "the rejected group stayed in use")
        XCTAssertTrue(text(app, containing: "家里、在用").exists, "the other group was rejected too")
        shot("revoked group header \(scheme)")
        let expired = text(app, containing: "配对已失效")
        let deadline = Date.now.addingTimeInterval(15)
        while Date.now < deadline, !expired.exists {
            app.swipeUp()
            _ = expired.waitForExistence(timeout: 1)
        }
        XCTAssertTrue(expired.exists, "the revoked group looks merely offline:\n\(app.debugDescription)")
        let repair = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'hubs.repair.'")).firstMatch
        XCTAssertTrue(repair.waitForExistence(timeout: 5), "no Pair Again for the revoked group:\n\(app.debugDescription)")
        shot("revoked group \(scheme)")
        repair.tap()
        XCTAssertTrue(app.navigationBars["连接 hub"].waitForExistence(timeout: 5), "Pair Again did not open the pairing sheet")
    }
}
