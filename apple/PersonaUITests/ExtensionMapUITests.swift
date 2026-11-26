import Foundation
import XCTest

/// UI version 12's `map` component on an extension nav_page, compact overview_card, downgrade fallback, and
/// assistant inline `tool_views` (`MOCK_EXTENSIONS=1 MOCK_EXTENSION_MAP=1`, `TEST_RUNNER_PERSONA_MAP_HUB`).
final class ExtensionMapUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var pad: Bool { UIDevice.current.userInterfaceIdiom == .pad }
    private var scheme: String { env["PERSONA_MAP_SCHEME"] ?? "light" }
    private var shotTag: String { env["PERSONA_MAP_TAG"] ?? scheme }
    private let mapEntry = "sidebar-extension-example-maps-route-planner"
    private let chatConversation = "server/b2e4d6f8-0000-4000-8000-000000000012"

    @MainActor private func launch(tab: String = "assistant", extraArgs: [String] = []) throws -> XCUIApplication {
        guard let hub = env["PERSONA_MAP_HUB"] else {
            throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 MOCK_EXTENSION_MAP=1 (PERSONA_MAP_HUB)")
        }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", tab, "-dev.openChat", "", "-demo.on", "NO", "-ui.scheme", scheme] + extraArgs
        if let textSize = env["PERSONA_MAP_TEXT_SIZE"], !textSize.isEmpty {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", textSize]
        }
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for _ in 0..<2 where springboard.alerts.firstMatch.waitForExistence(timeout: 2) {
            var dismissed = false
            for label in ["允许", "Allow", "好", "OK"] where springboard.buttons[label].exists { // i18n-ignore
                springboard.buttons[label].tap()
                dismissed = true
                break
            }
            if !dismissed { break }
        }
        return app
    }

    @MainActor private func openRoutePlanner(_ app: XCUIApplication) {
        let row = app.descendants(matching: .any)[mapEntry]
        if !row.waitForExistence(timeout: 10) {
            let sidebar = app.buttons["assistant-sidebar-button"]
            XCTAssertTrue(sidebar.waitForExistence(timeout: 10), app.debugDescription)
            sidebar.tap()
        }
        XCTAssertTrue(row.waitForExistence(timeout: 10), app.debugDescription)
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["extension-ui-ready"].waitForExistence(timeout: 20),
                      app.debugDescription)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = "map-\(name)-\(shotTag)"
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let base = URL(fileURLWithPath: dir).appending(path: "map-\(pad ? "ipad" : "iphone")-\(name)-\(shotTag).png")
        try? png.pngRepresentation.write(to: base)
    }

    @MainActor private func waitEnabled(_ element: XCUIElement, enabled: Bool = true, timeout: TimeInterval = 8) {
        let pred = NSPredicate(format: "isEnabled == %@", NSNumber(value: enabled))
        let exp = XCTNSPredicateExpectation(predicate: pred, object: element)
        XCTAssertEqual(XCTWaiter().wait(for: [exp], timeout: timeout), .completed,
                       "expected isEnabled=\(enabled) on \(element.identifier):\n\(element.debugDescription)")
    }

    @MainActor func testNavPageRouteSwitchPanResetCoverStopPushAndLink() throws {
        if env["PERSONA_MAP_EXPECT_DOWNGRADE"] == "1" {
            throw XCTSkip("skipped when MOCK_EXTENSION_MAP_DOWNGRADE=1")
        }
        let app = try launch()
        openRoutePlanner(app)

        let map = app.descendants(matching: .any)["extension-map"]
        XCTAssertTrue(map.waitForExistence(timeout: 20), app.debugDescription)

        let reset = app.buttons["extension-map-reset"]
        XCTAssertTrue(reset.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse(reset.isEnabled, "initial fitted viewport keeps Reset View disabled")
        shot(app, "nav-page")

        // Switch routes on page and verify selection stays interactive.
        let altRoute = app.buttons["extension-map-route-r-alt"]
        XCTAssertTrue(altRoute.waitForExistence(timeout: 10), app.debugDescription)
        altRoute.tap()
        let fastRoute = app.buttons["extension-map-route-r-fastest"]
        XCTAssertTrue(fastRoute.exists, app.debugDescription)
        fastRoute.tap()

        // Pan away from the leading edge, then reset back to fitted bounds.
        let panStart = map.coordinate(withNormalizedOffset: CGVector(dx: 0.62, dy: 0.38))
        let panEnd = map.coordinate(withNormalizedOffset: CGVector(dx: 0.30, dy: 0.65))
        panStart.press(forDuration: 0.05, thenDragTo: panEnd)
        waitEnabled(reset, enabled: true)
        shot(app, "panned")
        reset.tap()
        waitEnabled(reset, enabled: false)

        // Open full-screen cover, switch route inside cover, and push stop detail from a stop action.
        app.buttons["extension-map-full-screen"].tap()
        let close = app.buttons["extension-map-close"]
        XCTAssertTrue(close.waitForExistence(timeout: 10), app.debugDescription)
        let cover = app.descendants(matching: .any)["extension-map-cover"]
        XCTAssertTrue(cover.waitForExistence(timeout: 10), app.debugDescription)
        let coverAltRoute = cover.buttons["extension-map-cover-route-r-alt"]
        if coverAltRoute.waitForExistence(timeout: 5) {
            coverAltRoute.tap()
        }
        shot(app, "full-screen")

        let waypointStop = cover.descendants(matching: .any)["extension-map-stop-waypoint"]
        XCTAssertTrue(waypointStop.waitForExistence(timeout: 10), app.debugDescription)
        waypointStop.tap()

        let pushedText = app.staticTexts["Stop details for Coffee Stop along the planned commute route."]
        XCTAssertTrue(pushedText.waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "stop-pushed")

        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.buttons["extension-map-full-screen"].waitForExistence(timeout: 15), app.debugDescription)

        // Open verified HTTPS link sheet and dismiss cleanly.
        let pageLink = app.buttons["extension-map-link-0"]
        if !pageLink.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(pageLink.waitForExistence(timeout: 10), app.debugDescription)
        pageLink.tap()
        let linkClose = app.buttons["link-close"]
        XCTAssertTrue(linkClose.waitForExistence(timeout: 10), app.debugDescription)
        linkClose.tap()
        XCTAssertTrue(app.descendants(matching: .any)["extension-map"].waitForExistence(timeout: 10), app.debugDescription)
    }

    @MainActor func testOverviewCardCompactPreviewOpensFullScreen() throws {
        if env["PERSONA_MAP_EXPECT_DOWNGRADE"] == "1" {
            throw XCTSkip("skipped when MOCK_EXTENSION_MAP_DOWNGRADE=1")
        }
        let app = try launch(tab: "settings")
        if !app.descendants(matching: .any)["settings-list"].waitForExistence(timeout: 5) {
            let sidebar = app.buttons["assistant-sidebar-button"]
            XCTAssertTrue(sidebar.waitForExistence(timeout: 10), app.debugDescription)
            sidebar.tap()
            let settings = app.drawerSettingsButton
            XCTAssertTrue(settings.waitForExistence(timeout: 10), app.debugDescription)
            settings.tap()
        }
        XCTAssertTrue(app.descendants(matching: .any)["settings-list"].waitForExistence(timeout: 15),
                      app.debugDescription)

        let plugins = app.descendants(matching: .any)["settings-plugins"]
        XCTAssertTrue(plugins.waitForExistence(timeout: 10), app.debugDescription)
        plugins.tap()

        let card = app.descendants(matching: .any)["plugin-card-example-maps-route-planner-card"]
        let extensionsList = app.descendants(matching: .any)["extensions-list"]
        for _ in 0..<10 where !card.waitForExistence(timeout: 2) {
            extensionsList.swipeUp(velocity: .fast)
        }
        XCTAssertTrue(card.waitForExistence(timeout: 10), app.debugDescription)
        let preview = card.buttons["extension-map-preview"]
        XCTAssertTrue(preview.waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "overview-card")

        preview.tap()
        let close = app.buttons["extension-map-close"]
        XCTAssertTrue(close.waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "overview-cover")
        close.tap()
        XCTAssertTrue(preview.waitForExistence(timeout: 10), app.debugDescription)
    }

    @MainActor func testAssistantChatInlineToolViewsRenderAndOpenFullScreen() throws {
        if env["PERSONA_MAP_EXPECT_DOWNGRADE"] == "1" {
            throw XCTSkip("skipped when MOCK_EXTENSION_MAP_DOWNGRADE=1")
        }
        let app = try launch(tab: "assistant", extraArgs: ["-openAssistant", chatConversation])
        let card = app.descendants(matching: .any)["tool-view-card-0"]
        XCTAssertTrue(card.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(card.staticTexts["Commute Options"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.staticTexts["Fastest drive takes 23 min (12.4 km) via Ring Rd, or take Transit in 35 min."].exists,
                      app.debugDescription)

        let preview = card.buttons["extension-map-preview"]
        XCTAssertTrue(preview.waitForExistence(timeout: 10), app.debugDescription)
        let sidebarBtn = app.buttons["assistant-sidebar-button"]
        let minVisibleY = sidebarBtn.exists ? sidebarBtn.frame.maxY + 12 : app.windows.firstMatch.frame.height * 0.2
        for _ in 0..<6 where !preview.isHittable || preview.frame.minY < minVisibleY {
            app.swipeDown(velocity: .slow)
        }
        XCTAssertTrue(preview.isHittable && preview.frame.minY >= minVisibleY,
                      "expected preview below top header (\(minVisibleY)), frame=\(preview.frame)\n\(app.debugDescription)")
        shot(app, "chat-inline")

        preview.tap()
        let close = app.buttons["extension-map-close"]
        XCTAssertTrue(close.waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "chat-inline-cover")

        // Opening an HTTPS link from full-screen cover defers until cover dismisses, avoiding nested modal conflicts.
        let cover = app.descendants(matching: .any)["extension-map-cover"]
        let coverLink = cover.buttons["extension-map-cover-link-0"]
        if coverLink.waitForExistence(timeout: 5) {
            coverLink.tap()
            let linkClose = app.buttons["link-close"]
            XCTAssertTrue(linkClose.waitForExistence(timeout: 10), app.debugDescription)
            linkClose.tap()
        } else {
            close.tap()
        }
        XCTAssertTrue(card.waitForExistence(timeout: 10), app.debugDescription)
    }

    @MainActor func testDowngradeFallbackRendersPlainRows() throws {
        guard env["PERSONA_MAP_EXPECT_DOWNGRADE"] == "1" else {
            throw XCTSkip("only runs when MOCK_EXTENSION_MAP_DOWNGRADE=1")
        }
        let app = try launch()
        openRoutePlanner(app)
        XCTAssertFalse(app.descendants(matching: .any)["extension-map"].exists)
        XCTAssertTrue(app.staticTexts["Fastest"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.staticTexts["Transit"].exists, app.debugDescription)
        XCTAssertTrue(app.staticTexts["Coffee Stop"].exists, app.debugDescription)
        shot(app, "downgrade")
    }
}
