import XCTest

/// The disk usage map (#14576, docs/server/machine-usage-map.md), against scripts/mock/mock_hub.py started with
/// MOCK_USAGE_MAP=1 (the Mac Studio's disk 93 % full, its folders, an offload provider):
///   TEST_RUNNER_PERSONA_USAGE_MAP_HUB=http://127.0.0.1:<port>
/// It opens the map from the Disk almost full chip on the Mac Studio's card, zooms into a box, goes back with the
/// breadcrumb, selects a file and reaches the trash confirmation. TEST_RUNNER_PERSONA_SHOTS=<dir> (and
/// TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark, TEST_RUNNER_PERSONA_SHOTS_WIDE=1 on an iPad for the wide shot of `testWideMapSidesBySide`) also saves a
/// screenshot of each step there.
final class UsageMapUITests: XCTestCase {
    private var app: XCUIApplication!

    @MainActor private func launch() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_USAGE_MAP_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_USAGE_MAP=1 (PERSONA_USAGE_MAP_HUB)")
        }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // Pair first (and dismiss the icon alert), then open Computers on a launch that is already paired.
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""]
        app.launchPinned()
        acceptIconAlert()
        app.terminate()
        let scheme = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { ["-ui.scheme", $0] } ?? []
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-tab", "settings", "-openMachines", "YES"] + scheme
        app.launchPinned()
        acceptIconAlert()
    }

    @MainActor private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + look + ".png"))
        }
    }

    /// A framed box's title strip: its middle holds its children's own targets, which open those instead.
    @MainActor private func tapTitle(_ box: XCUIElement) {
        box.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 12, dy: 8)).tap()
    }

    @MainActor private func any(_ id: String) -> XCUIElement { app.descendants(matching: .any).matching(identifier: id).firstMatch }

    @MainActor func testDiskChipOpensTheMapZoomsBackAndReachesTheTrashConfirmation() throws {
        try launch()
        // The card is a link that takes on its chip's identifier too: tap the smallest element that has it, the chip.
        let chips = app.descendants(matching: .any).matching(identifier: "machine.alert.disk.studio")
        XCTAssertTrue(chips.firstMatch.waitForExistence(timeout: 40), "the Mac Studio's Disk almost full chip never came")
        let chip = chips.allElementsBoundByIndex.min { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }!
        chip.tap()
        XCTAssertTrue(any("usage.map").waitForExistence(timeout: 20), "the map didn't open")
        // Reclaim mode: the headline and the marked items.
        XCTAssertTrue(any("usage.headline").waitForExistence(timeout: 10))
        shot("usage-map-overview")

        let home = any("usage.box.group:home")
        XCTAssertTrue(home.waitForExistence(timeout: 10))
        tapTitle(home)
        XCTAssertTrue(any("usage.crumb.1").waitForExistence(timeout: 10), "zooming in didn't add a breadcrumb step")
        let downloads = any("usage.box./Users/me/Downloads")
        XCTAssertTrue(downloads.waitForExistence(timeout: 10))
        shot("usage-map-zoomed")

        any("usage.crumb.0").tap()
        XCTAssertTrue(home.waitForExistence(timeout: 10))
        XCTAssertFalse(any("usage.crumb.1").exists, "the breadcrumb didn't go back")

        // Into Downloads, select its archive (a file: a tap selects it) and ask to move it to the Trash.
        tapTitle(home)
        XCTAssertTrue(downloads.waitForExistence(timeout: 10))
        tapTitle(downloads)
        let archive = any("usage.box./Users/me/Downloads/archive.zip")
        XCTAssertTrue(archive.waitForExistence(timeout: 10))
        archive.tap()
        let trash = app.buttons["usage.tray.trash"]
        for _ in 0..<6 where !(trash.exists && trash.isHittable) { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(trash.waitForExistence(timeout: 10), "the tray has no Move to Trash")
        shot("usage-map-selected")
        trash.tap()
        let confirm = app.buttons["usage.confirm"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 5) || app.sheets.firstMatch.waitForExistence(timeout: 1),
                      "the trash confirmation didn't appear")
        shot("usage-map-trash-confirm")
    }

    /// Screenshots only (TEST_RUNNER_PERSONA_SHOTS_WIDE=1, on an iPad): with the column hidden the local disk and the
    /// cloud stand side by side on one scale.
    @MainActor func testWideMapSidesBySide() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SHOTS_WIDE"] != nil else { throw XCTSkip("screenshots only") }
        try launch()
        let chips = app.descendants(matching: .any).matching(identifier: "machine.alert.disk.studio")
        XCTAssertTrue(chips.firstMatch.waitForExistence(timeout: 40))
        chips.allElementsBoundByIndex.min { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }!.tap()
        XCTAssertTrue(any("usage.box.group:home").waitForExistence(timeout: 20))
        _ = any("usage.never").waitForExistence(timeout: 3) // the measuring answer settles
        // Hide Settings' column (iPad): the map then has the page's whole width.
        let hide = app.buttons.matching(NSPredicate(format: "label IN %@", ["隐藏边栏", "Hide Sidebar"])).firstMatch
        if hide.exists { hide.tap() }
        _ = any("usage.never").waitForExistence(timeout: 2)
        shot("usage-map-wide")
    }
}
