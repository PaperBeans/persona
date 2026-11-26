import XCTest

/// Real HTTP selection writes through a task-owned mock, with native switches and reorder handles.
final class HealthItemsUITests: XCTestCase {
    private var hub: String { ProcessInfo.processInfo.environment["PERSONA_HEALTH_HUB"] ?? "" }

    @MainActor private func configure(_ items: String, saved: Bool) async throws {
        guard !hub.isEmpty else { throw XCTSkip("requires PERSONA_HEALTH_HUB") }
        let url = try XCTUnwrap(URL(string: hub + "/mock/health-items?items=" + items + "&saved=" + (saved ? "1" : "0")))
        let (_, response) = try await URLSession.shared.data(from: url)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
    }
    @MainActor private func launch() -> XCUIApplication {
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-demo.on", "NO", "-dev.openChat", "",
                               "-openShellPage", "health", "-showHealthActivityExample", "full", "-showHealthHomeTrendsExample", "sample",
                               "-showHealthItemsEditor", "NO", "-showSameIcon", "YES"]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["Allow", "允许", "OK", "好"] where springboard.buttons[label].waitForExistence(timeout: 1) {
            springboard.buttons[label].tap()
        }
        XCTAssertTrue(app.buttons["health-items-edit"].waitForExistence(timeout: 25), app.debugDescription)
        return app
    }
    @MainActor func testFirstQuestionAddsProteinAndDoesNotAskAgain() async throws {
        try await configure("activity,trends", saved: false)
        var app = launch()
        XCTAssertTrue(app.otherElements["health-items-question"].waitForExistence(timeout: 10), app.debugDescription)
        let protein = app.switches["health-question-protein"]
        protein.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        XCTAssertEqual(protein.value as? String, "1", app.debugDescription)
        XCTAssertGreaterThanOrEqual(app.buttons["health-question-add"].frame.height, 44)
        XCTAssertGreaterThanOrEqual(app.buttons["health-question-skip"].frame.height, 44)
        XCTAssertFalse(app.buttons["health-question-add"].label.contains("\n"))
        app.buttons["health-question-add"].tap()
        XCTAssertTrue(app.buttons["health-question-add"].waitForNonExistence(timeout: 10))
        for _ in 0..<5 where !app.buttons["health-protein-ring"].exists { app.swipeUp() }
        XCTAssertTrue(app.buttons["health-protein-ring"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse(app.buttons["health-water-ring"].exists)
        app.terminate()
        app = launch()
        XCTAssertFalse(app.otherElements["health-items-question"].exists)
        for _ in 0..<5 where !app.buttons["health-protein-ring"].exists { app.swipeUp() }
        XCTAssertTrue(app.buttons["health-protein-ring"].waitForExistence(timeout: 10))
        app.terminate()
    }
    @MainActor func testNotNowSavesTheCurrentListAndDoesNotAskAgain() async throws {
        try await configure("activity,trends", saved: false)
        var app = launch()
        XCTAssertTrue(app.buttons["health-question-skip"].waitForExistence(timeout: 10))
        app.buttons["health-question-skip"].tap()
        XCTAssertTrue(app.buttons["health-question-skip"].waitForNonExistence(timeout: 10))
        app.terminate()
        app = launch()
        XCTAssertFalse(app.buttons["health-question-skip"].exists)
        app.terminate()
    }
    @MainActor func testEditorReordersAndRemovesWater() async throws {
        try await configure("water,protein,activity,trends", saved: true)
        let app = launch()
        app.buttons["health-items-edit"].tap()
        let water = app.switches["health-item-water"], protein = app.switches["health-item-protein"]
        XCTAssertTrue(water.waitForExistence(timeout: 10), app.debugDescription)
        water.switches.firstMatch.tap()
        XCTAssertEqual(water.value as? String, "0", app.debugDescription)
        let handles = app.buttons.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "Reorder", "重新排序"))
        XCTAssertGreaterThanOrEqual(handles.count, 3, app.debugDescription)
        let trendsRow = app.cells.containing(.switch, identifier: "health-item-trends").firstMatch
        let rowHandle = trendsRow.buttons.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "Reorder", "重新排序")).firstMatch
        let source = rowHandle.exists ? rowHandle : handles.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "Health Trends", "健康趋势")).firstMatch
        source.press(forDuration: 0.5, thenDragTo: handles.element(boundBy: 0))
        XCTAssertTrue(protein.exists)
        app.buttons["health-items-save"].tap()
        XCTAssertTrue(water.waitForNonExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse(app.buttons["health-water-ring"].exists)
        let (data, _) = try await URLSession.shared.data(from: XCTUnwrap(URL(string: hub + "/api/v1/assistant/health/items")))
        let state = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual((state["items"] as? [String])?.first, "trends")
        XCTAssertFalse((state["items"] as? [String] ?? []).contains("water"))
        app.terminate()
    }
}
