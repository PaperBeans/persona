import XCTest

/// Answering a to-do's asks on its V1 row (#10465; docs/apple/ios.md "To-dos (V1, #10465)") against
/// the mock hub's To-dos view (`MOCK_TODO_VIEW=1`, `PERSONA_TODOS_HUB`). `-showTodosV1 YES` stands for the release.
///
/// - OK on Plan the Kyoto trip takes it out of N at once: the Needs you header, the Filter's Needs you, the drawer's
///   To-dos row and the folded Pages tally say the same lower number before the answer has gone, and after the hub has it.
/// - A place draws the same answers (Today, a project), and OK there takes the to-do out of the place's N at once.
/// - A search's result offers none, and they are back once the field is cleared (`testASearchResultOffersNoAnswers`).
/// - With `PERSONA_TODOS_FRAMES=1` and `PERSONA_SHOTS=<dir>`, the V1 frames the answers draw, saved for review
///   (`PERSONA_TODOS_STYLES` and `PERSONA_TODOS_SCHEME`, comma lists; `PERSONA_TODOS_LANGUAGE`, `PERSONA_TODOS_COLOR`,
///   `PERSONA_TODOS_SIZE`, `PERSONA_TODOS_FRAMES_ONLY`, where `list` is the list as it opens), each from a fresh launch
///   and the mock put back after it.
/// - With `PERSONA_TODOS_TRAY=1`, the Needs You tray's decision row (`testTrayFrames`).
final class TodosV1AnswersTests: XCTestCase {
    private var app: XCUIApplication!
    private let env = ProcessInfo.processInfo.environment

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("iPhone only") }
    }

    @MainActor private func launch(_ hub: String, _ extra: [String] = [], layout: String = "sidebar") {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-app.navigation", layout,
                               "-showTodosV1", "YES", "-todos.v1.byProject", "NO", "-todos.v1.sort", "suggested"] + extra
        app.launchPinned()
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// An open menu's item by its identifier, else by its place: SwiftUI carries no identifier onto a menu's items on
    /// iOS 26 and later, and the Filter's menu lists its choices in a fixed order (as `TodosV1ListTests` finds them).
    @MainActor private func menuItem(_ id: String, at index: Int) -> XCUIElement {
        if let byID = app.buttons.matching(identifier: id).allElementsBoundByIndex.first(where: \.isHittable) { return byID }
        let menus = app.collectionViews.allElementsBoundByIndex.filter { $0.identifier != "todos.v1" }
        guard let menu = menus.last else { return app.buttons[id] }
        return menu.buttons.element(boundBy: index)
    }

    /// The last number in a spoken label ("Needs you, 3"): the count a badge adds to its row.
    private func number(_ label: String) -> Int? {
        label.split(whereSeparator: { !$0.isNumber }).last.flatMap { Int($0) }
    }

    /// The Needs you header's N, waited for until it is `expected` (or any, when nil).
    @MainActor private func headerN(_ expected: Int? = nil, timeout: TimeInterval = 10) -> Int? {
        let header = element("todos.section.needs_you")
        let deadline = Date.now.addingTimeInterval(timeout)
        repeat {
            if header.exists, let n = (header.value as? String).flatMap({ Int($0) }), expected == nil || n == expected { return n }
            usleep(100_000)
        } while Date.now < deadline
        return (header.value as? String).flatMap { Int($0) }
    }

    @MainActor func testOKTakesTheToDoOutOfNEverywhereAtOnce() throws {
        guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
        let base = try XCTUnwrap(URL(string: hub))
        _ = try mock(base, "GET", "mock/reset")
        defer { _ = try? mock(base, "GET", "mock/reset") }
        launch(hub)
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        let n = try XCTUnwrap(headerN(), "no Needs you header:\n\(app.debugDescription)")
        let ok = element("todos.answer.ok.t01")
        XCTAssertTrue(ok.waitForExistence(timeout: 20), "no OK on the Kyoto row:\n\(app.debugDescription)")
        XCTAssertGreaterThanOrEqual(ok.frame.height, 30, "the style's whole button, about 32 pt")
        ok.tap()
        // At once, while the answer still waits out its Undo window on the device.
        XCTAssertEqual(headerN(n - 1, timeout: 3), n - 1, "the header kept N")
        XCTAssertFalse(ok.exists, "the row's line says the answer in place of its buttons")
        // The Filter's Needs you (the counts the bar had before the foot toolbar) says the same; All again closes it.
        let filter = element("todos.filter")
        XCTAssertTrue(filter.waitForExistence(timeout: 5), "no Filter:\n\(app.debugDescription)")
        filter.tap()
        let needsYou = menuItem("todos.filter.needs_you", at: 1)
        XCTAssertTrue(needsYou.waitForExistence(timeout: 5), "no Needs you in the Filter's menu:\n\(app.debugDescription)")
        XCTAssertEqual(number(needsYou.label), n - 1, "the Filter's Needs you: \(needsYou.label)")
        menuItem("todos.filter.all", at: 0).tap()
        element("sidebar-drawer-button").tap()
        let row = element("sidebar-todos")
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no drawer:\n\(app.debugDescription)")
        XCTAssertEqual(number(row.label), n - 1, "the drawer's To-dos row: \(row.label)")
        let places = element("sidebar-fold-places")
        places.tap()
        let tally = element("sidebar-fold-places.todos")
        XCTAssertTrue(tally.waitForExistence(timeout: 10), "no tally on the folded Pages heading:\n\(app.debugDescription)")
        XCTAssertEqual(tally.value as? String, String(n - 1), "the folded Pages heading")
        places.tap() // unfolded again, as the next run expects
        // After the window the answer has gone, and the hub's view says the same: the decision taken, N as it was said.
        XCTAssertEqual(try hubN(base, n - 1, timeout: 20), n - 1, "the hub has the answer")
        XCTAssertEqual(number(row.label), n - 1, "the drawer after the hub's answer: \(row.label)")
    }

    /// The answers are the row's wherever the row is listed: on Today (Plan the Kyoto trip due today), on a project's
    /// place (Home's failed start offers Retry; Kyoto trip's decision OK), and OK there takes the to-do out of the
    /// place's N at once, as on All To-dos.
    @MainActor func testTheAnswersAreOnTheRowsInAPlace() throws {
        guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
        let base = try XCTUnwrap(URL(string: hub))
        _ = try mock(base, "GET", "mock/reset")
        defer { _ = try? mock(base, "GET", "mock/reset") }
        _ = try mock(base, "PATCH", "api/v1/todos/t01", body: ["due_date": today()])
        launch(hub, ["-openTodoPlace", "today"])
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        waitFor("todos.answer.ok.t01", timeout: 20)
        waitFor("todos.answer.options.t01", timeout: 2)

        app.terminate()
        _ = try mock(base, "GET", "mock/reset")
        launch(hub, ["-openTodoPlace", "project:home"])
        waitFor("todos.answer.retry.t03", timeout: 60)

        app.terminate()
        _ = try mock(base, "GET", "mock/reset")
        launch(hub, ["-openTodoPlace", "project:kyoto"])
        waitFor("todos.answer.ok.t01", timeout: 60)
        let n = try XCTUnwrap(headerN(), "no Needs you header:\n\(app.debugDescription)")
        let before = try XCTUnwrap(try hubN(base))
        tap("todos.answer.ok.t01")
        awaitGone("todos.answer.ok.t01", timeout: 3)
        // The place's heading drops at once; with its only ask answered it may say no number at all.
        let after = headerN(n - 1, timeout: 3)
        XCTAssertTrue(after == n - 1 || (n == 1 && after == nil), "the place's Needs you kept \(String(describing: after))")
        XCTAssertEqual(try hubN(base, before - 1, timeout: 20), before - 1, "the hub has the answer")
    }

    /// A search's result offers no answers, as it offers no Mark Done (DESIGN_SYSTEM 3.4 "To-dos list", the search):
    /// "Kyoto" finds Plan the Kyoto trip with no OK and no Options under it, and Cancel brings them back. That a result
    /// still says an answer given here is `TodoSearchTests.aResultOffersNoAnswers`'s: the answer goes as its 4 s hold
    /// ends and the mock takes it at once (`/mock/delay` does not hold the asks route), before a search typed after OK
    /// has landed. Launched in English only because the system's search controls carry no identifier and are found by
    /// their words; what the test checks of ours is by identifier.
    @MainActor func testASearchResultOffersNoAnswers() throws {
        guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
        let base = try XCTUnwrap(URL(string: hub))
        _ = try mock(base, "GET", "mock/reset")
        defer { _ = try? mock(base, "GET", "mock/reset") }
        launch(hub, ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"], layout: "separate")
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        waitFor("todos.answer.ok.t01", timeout: 20)
        waitFor("todos.answer.options.t01", timeout: 2)
        search("Kyoto")
        XCTAssertTrue(element("todos.row.t01").exists, "Plan the Kyoto trip found by its title:\n\(app.debugDescription)")
        XCTAssertFalse(element("todos.answer.ok.t01").exists, "no OK on a result")
        XCTAssertFalse(element("todos.answer.options.t01").exists, "no Options on a result")
        cancelSearch()
        // The answers again once the field is cleared.
        waitFor("todos.answer.ok.t01", timeout: 10)
        waitFor("todos.answer.options.t01", timeout: 2)
    }

    /// The list's search field opened, `words` typed, and the results drawn for them (Search Chats for …'s identifier
    /// once they are the words', a look landing after the key).
    @MainActor private func search(_ words: String) {
        let field = todosSearchField(app)
        field.tap()
        field.typeText(words)
        waitFor("todos.searchChats", timeout: 10)
    }

    /// Out of the search with the system's Cancel (or Close), found by its words.
    @MainActor private func cancelSearch() {
        let cancel = app.buttons.matching(NSPredicate(format: "label ==[c] 'cancel' OR label ==[c] 'close'")).allElementsBoundByIndex
            .first(where: \.isHittable)
        guard let cancel else { return XCTFail("no way out of the search:\n\(app.debugDescription)") }
        cancel.tap()
    }

    /// Today's date as the mock's PATCH takes a due date (the device's calendar, as the mock's start reads it).
    private func today() -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f.string(from: .now)
    }

    /// The mock hub's own N (`GET /api/v1/todos`), polled until it is `expected` (nil: as it is now) or `timeout` passes.
    @MainActor private func hubN(_ base: URL, _ expected: Int? = nil, timeout: TimeInterval = 0) throws -> Int? {
        let deadline = Date.now.addingTimeInterval(timeout)
        var n: Int?
        repeat {
            let listed = try JSONSerialization.jsonObject(with: mock(base, "GET", "api/v1/todos")) as? [String: Any]
            n = (listed?["counts"] as? [String: Any])?["needs_you"] as? Int
            if expected == nil || n == expected { return n }
            usleep(250_000)
        } while Date.now < deadline
        return n
    }

    /// The mock hub's section for to-do `id`, polled until it is `expected` or `timeout` passes.
    @MainActor private func hubSection(_ base: URL, _ id: String, _ expected: String, timeout: TimeInterval) throws -> String? {
        let deadline = Date.now.addingTimeInterval(timeout)
        var section: String?
        repeat {
            let listed = try JSONSerialization.jsonObject(with: mock(base, "GET", "api/v1/todos")) as? [String: Any]
            let row = (listed?["todos"] as? [[String: Any]])?.first { $0["id"] as? String == id }
            section = (row?["view"] as? [String: Any])?["section"] as? String
            if section == expected { return section }
            usleep(250_000)
        } while Date.now < deadline
        return section
    }

    /// Waits for `id` and taps it; a frame whose element never came fails rather than shooting the wrong screen.
    @MainActor private func tap(_ id: String, timeout: TimeInterval = 10) {
        let e = element(id)
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "no \(id):\n\(app.debugDescription)")
        e.tap()
    }

    /// Waits for `id`, the state a frame draws.
    @MainActor private func waitFor(_ id: String, timeout: TimeInterval = 10) {
        XCTAssertTrue(element(id).waitForExistence(timeout: timeout), "no \(id):\n\(app.debugDescription)")
    }

    /// Waits until `id` is gone (the row moved on, the answer sent).
    @MainActor private func awaitGone(_ id: String, timeout: TimeInterval = 15) {
        let gone = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: element(id))
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: timeout), .completed, "\(id) still there:\n\(app.debugDescription)")
    }

    @MainActor func testAnswerFrames() throws {
        guard env["PERSONA_TODOS_FRAMES"] == "1", let hub = env["PERSONA_TODOS_HUB"], let dir = env["PERSONA_SHOTS"] else {
            throw XCTSkip("PERSONA_TODOS_FRAMES=1 with PERSONA_TODOS_HUB and PERSONA_SHOTS")
        }
        let base = try XCTUnwrap(URL(string: hub))
        defer { _ = try? mock(base, "GET", "mock/reset") }
        // One run draws every style and appearance asked for (comma lists), each frame from a fresh launch.
        let styles = (env["PERSONA_TODOS_STYLES"] ?? UITestStyle.name).split(separator: ",").map(String.init)
        let schemes = (env["PERSONA_TODOS_SCHEME"] ?? "light").split(separator: ",").map(String.init)
        for style in styles {
            for scheme in schemes { try answerFrames(hub: hub, base: base, dir: dir, style: style, scheme: scheme) }
        }
    }

    /// The answers' frames in one style and appearance, named as the kit names them (`iphone-list~options-glass-dark.png`).
    @MainActor private func answerFrames(hub: String, base: URL, dir: String, style: String, scheme: String) throws {
        let language = env["PERSONA_TODOS_LANGUAGE"] ?? "en"
        let locale = ["en": "en_US", "zh-Hans": "zh_CN", "ja": "ja_JP"][language] ?? "en_US"
        var extra = ["-ui.style", style, "-ui.scheme", scheme, "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        if let color = env["PERSONA_TODOS_COLOR"] { extra += ["-ui.color", color] }
        if let size = env["PERSONA_TODOS_SIZE"] { extra += ["-UIPreferredContentSizeCategoryName", size] }
        let frames = Set((env["PERSONA_TODOS_FRAMES_ONLY"] ?? "").split(separator: ",").map(String.init))
        let wants = { (frame: String) in frames.isEmpty || frames.contains(frame.isEmpty ? "list" : frame) }
        let suffix = env["PERSONA_TODOS_SUFFIX"].map { "-" + $0 } ?? ""
        // Shot once the state it draws is there, after the unfold or fade that brought it (Theme.Motion.state, 0.25 s).
        let shot = { (frame: String) in
            usleep(600_000)
            let png = XCUIScreen.main.screenshot().pngRepresentation
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("iphone-list\(frame)\(suffix)-\(style)-\(scheme).png"))
        }
        // Each from a fresh launch on the mock as it started, the iPhone's own layout, as the frames draw it; on All
        // To-dos, or on `place` (Today with Plan the Kyoto trip due today, so its answers are there).
        let fresh = { (place: String?) in
            _ = try? self.mock(base, "GET", "mock/reset")
            if place == "today" { _ = try? self.mock(base, "PATCH", "api/v1/todos/t01", body: ["due_date": self.today()]) }
            self.app?.terminate()
            self.launch(hub, extra + (place.map { ["-openTodoPlace", $0] } ?? []), layout: "separate")
            if !self.element("todos.v1").waitForExistence(timeout: 60) { XCTFail("no V1 list:\n\(self.app.debugDescription)") }
            self.waitFor("todos.answer.ok.t01", timeout: 20)
        }
        if wants("") {
            fresh(nil)
            shot("")
        }
        if wants("~kyoto-answered") || wants("~kyoto-booking") {
            fresh(nil)
            let before = try XCTUnwrap(try hubN(base))
            tap("todos.answer.ok.t01")
            // The row says the answer in place of its buttons ("✓ You chose … · Undo", inside the row's one element).
            awaitGone("todos.answer.ok.t01", timeout: 3)
            if wants("~kyoto-answered") { shot("~kyoto-answered") } // inside the Undo window
            // Gone to the hub once its window ends: a held answer outlives a relaunch, so the next frame waits for it
            // before the mock starts again.
            XCTAssertEqual(try hubN(base, before - 1, timeout: 20), before - 1, "the hub has the answer")
            if wants("~kyoto-booking") {
                usleep(1_500_000) // the hub's push, and the row filed where it now is once the list rests
                shot("~kyoto-booking")
            }
        }
        if wants("~options") || wants("~options-t03") {
            fresh(nil)
            if wants("~options") {
                tap("todos.answer.options.t01")
                waitFor("ask.card.t01")
                shot("~options")
            }
            if wants("~options-t03") {
                let options = element("todos.answer.options.t03")
                for _ in 0..<4 where !(options.exists && options.isHittable) {
                    app.collectionViews.firstMatch.swipeUp(velocity: .slow)
                    usleep(500_000)
                }
                tap("todos.answer.options.t03")
                waitFor("ask.retry.t03")
                shot("~options-t03")
            }
        }
        if wants("~later") {
            fresh(nil)
            tap("todos.answer.options.t01")
            tap("ask.later.t01", timeout: 5)
            waitFor("todos.answer.later.t01")
            shot("~later")
        }
        if wants("~back") {
            fresh(nil)
            tap("todos.answer.retry.t03")
            // Started again once its window ends, and Working on the hub.
            XCTAssertEqual(try hubSection(base, "t03", "working", timeout: 20), "working", "the hub started it again")
            usleep(1_500_000) // the hub's push, and the row filed where it now is once the list rests
            shot("~back")
        }
        if wants("~can-wait") {
            fresh(nil)
            tap("todos.fold.can_wait")
            usleep(300_000)
            shot("~can-wait")
        }
        // The same answers on a place's list: Today, and the Kyoto trip project.
        for (frame, place) in [("~answers-today", "today"), ("~answers-kyoto", "project:kyoto")] where wants(frame) {
            fresh(place)
            shot(frame)
        }
    }

    /// With `PERSONA_TODOS_TRAY=1` and `PERSONA_SHOTS=<dir>`: the Needs You tray's decision row (Plan the Kyoto trip's)
    /// over the assistant's chat. Without `-showTodosV1`, against `MOCK_TODO_VIEW=old`, it is the row an old hub's app
    /// draws, taken with this build and with main's for a before = after pair (`PERSONA_TODOS_SUFFIX` says which); with
    /// `PERSONA_TODOS_TRAY_V1=1` (V1, on the view's mock) also the same row once Kaede is chosen, held with its Undo.
    @MainActor func testTrayFrames() throws {
        guard env["PERSONA_TODOS_TRAY"] == "1", let hub = env["PERSONA_TODOS_HUB"], let dir = env["PERSONA_SHOTS"] else {
            throw XCTSkip("PERSONA_TODOS_TRAY=1 with PERSONA_TODOS_HUB and PERSONA_SHOTS")
        }
        let base = try XCTUnwrap(URL(string: hub))
        defer { _ = try? mock(base, "GET", "mock/reset") }
        let v1 = env["PERSONA_TODOS_TRAY_V1"] == "1"
        let style = env["PERSONA_TODOS_STYLES"] ?? UITestStyle.name
        let suffix = env["PERSONA_TODOS_SUFFIX"].map { "-" + $0 } ?? ""
        for scheme in (env["PERSONA_TODOS_SCHEME"] ?? "light").split(separator: ",").map(String.init) {
            _ = try? mock(base, "GET", "mock/reset")
            app?.terminate()
            app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-ui.style", style, "-ui.scheme", scheme,
                                   "-AppleLanguages", "(en)", "-AppleLocale", "en_US"] + (v1 ? ["-showTodosV1", "YES"] : [])
            app.launchPinned()
            app.openActionTray("needs-you", timeout: 60)
            let option = element("decision.option.1")
            if !option.waitForExistence(timeout: 10) { app.expandTrayRows(timeout: 3) }
            XCTAssertTrue(option.waitForExistence(timeout: 20), "no decision in the tray:\n\(app.debugDescription)")
            sleep(2) // the tray's own unfold and the breathing dot's settle, as the before and after shots both wait
            save(dir, "iphone-tray~decision\(suffix)-\(style)-\(scheme)")
            if v1 {
                option.tap()
                XCTAssertTrue(element("decision.chosen.t01").waitForExistence(timeout: 3), "not held:\n\(app.debugDescription)")
                save(dir, "iphone-tray~chosen\(suffix)-\(style)-\(scheme)")
                // Taken back: a held answer outlives a relaunch, and the next appearance starts from the mock as it began.
                let undo = element("ask.undo")
                if undo.waitForExistence(timeout: 2) { undo.tap() }
            }
        }
    }

    /// The screen as `name`.png in `dir`.
    @MainActor private func save(_ dir: String, _ name: String) {
        let png = XCUIScreen.main.screenshot().pngRepresentation
        try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
    }

    /// A call to the mock hub, with `body` as JSON when given.
    private func mock(_ base: URL, _ method: String, _ path: String, body: [String: Any]? = nil) throws -> Data {
        var request = URLRequest(url: base.appending(path: path))
        request.httpMethod = method
        request.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        var out: Data?
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { data, _, _ in out = data; done.fulfill() }.resume()
        wait(for: [done], timeout: 20)
        return try XCTUnwrap(out)
    }
}
