import XCTest

/// What the V1 To-dos UI suites share (#10465): the list's search field, opened, and a call to the mock hub.
extension XCTestCase {
    /// The To-dos list's search field in `app`, opened: the system's, at the foot on iOS 26 (folded to ⌕ while a filter
    /// is on) and under the title on iOS 18; found by its words, since it carries no identifier of ours.
    @MainActor func todosSearchField(_ app: XCUIApplication) -> XCUIElement {
        let field = app.searchFields.firstMatch
        if !(field.exists && field.isHittable) {
            let button = app.buttons.matching(NSPredicate(format: "identifier == 'Search' OR label ==[c] 'search'")).allElementsBoundByIndex
                .first(where: \.isHittable)
            if let button { button.tap() } else { app.collectionViews.firstMatch.swipeDown(velocity: .slow) }
        }
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no search field:\n\(app.debugDescription)")
        return field
    }

    /// `method` `path` on the mock hub at `base`, waited for. A query rides after the path as it is ("mock/delay?s=8"):
    /// `appending(path:)` would escape its "?".
    func todosMock(_ base: URL, _ method: String, _ path: String) throws -> Data {
        let parts = path.split(separator: "?", maxSplits: 1).map(String.init)
        var url = base.appending(path: parts[0])
        if parts.count == 2, let queried = URL(string: url.absoluteString + "?" + parts[1]) { url = queried }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        var out: Data?
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { data, _, _ in out = data; done.fulfill() }.resume()
        wait(for: [done], timeout: 20)
        return try XCTUnwrap(out)
    }
}
