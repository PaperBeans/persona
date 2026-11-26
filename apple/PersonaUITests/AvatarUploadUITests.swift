import ImageIO
import UIKit
import UniformTypeIdentifiers
import XCTest

/// New from Photo end to end (#2405) against mock_hub.py with MOCK_LOOK_UPLOADS=demo: the crop sheet opened with a
/// portrait picture (`-showAvatarCrop`), dragged, uploaded and chosen; then the avatar made deleted with the button in
/// its card's corner, and only that one (the mock's demo upload, 「豆豆」, stays).
/// Real touches: the crop sits in a Form, which scrolls, and a drag on the picture must move the picture, not the sheet.
/// `PERSONA_MOCK_HUB_NO_FILES` (a second mock with MOCK_LOOK_UPLOAD_FILES=fail): made but not downloaded, Try Again.
/// With `look_uploads_states` (#15449; the demo mock lists it): an upload with a picture of a state's own (moving) and a
/// chosen color reaches the hub as such, and the Edit sheet of the mock's `u-demo0002` saves a mark turned on.
/// `PERSONA_LOOK_APPEARANCE` light / dark, `PERSONA_LOOK_LANGUAGE` (en, zh-Hans, ja), `PERSONA_LOOK_SHOTS` a folder for
/// the screenshots.
final class AvatarUploadUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUp() {
        continueAfterFailure = false
    }

    private func hub(_ key: String) throws -> String {
        guard let hub = env[key] else { throw XCTSkip("needs \(key): mock_hub.py's address") }
        return hub
    }

    /// A portrait picture (taller than wide, so it moves up and down at the first zoom), striped so a move shows.
    private func portrait() throws -> String {
        let size = CGSize(width: 600, height: 1200)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let png = UIGraphicsImageRenderer(size: size, format: format).pngData { ctx in
            for (i, y) in stride(from: 0, to: size.height, by: 100).enumerated() {
                UIColor(hue: Double(i) / 12, saturation: 0.7, brightness: 0.9, alpha: 1).setFill()
                ctx.fill(CGRect(x: 0, y: y, width: size.width, height: 100))
            }
        }
        let url = FileManager.default.temporaryDirectory.appending(path: "avatar-upload-portrait.png")
        try png.write(to: url)
        return url.path(percentEncoded: false)
    }

    @MainActor private func launch(_ hub: String, _ image: String, name: String = "Sunny", extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", "persona", "-openPersona", "persona",
                               "-showAvatarCrop", image, "-showAvatarCropName", name,
                               "-ui.scheme", env["PERSONA_LOOK_APPEARANCE"] ?? "light"] + extra
        if let language = env["PERSONA_LOOK_LANGUAGE"] { app.launchArguments += ["-AppleLanguages", "(\(language))"] }
        app.launchPinned()
        allowNotifications()
        let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let deny = board.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["不允许", "Don’t Allow", "Don't Allow"])).firstMatch
        if deny.waitForExistence(timeout: 3) { deny.tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["PERSONA_LOOK_SHOTS"] {
            let tag = [env["PERSONA_LOOK_LANGUAGE"], env["PERSONA_LOOK_APPEARANCE"] ?? "light"].compactMap { $0 }.joined(separator: "-")
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name)-\(tag).png"))
        }
    }

    @MainActor func testDragMovesThePictureThenUploadChoosesItAndDeleteBringsTheDefaultBack() throws {
        let app = launch(try hub("PERSONA_MOCK_HUB"), try portrait())
        defer { app.terminate() }
        let crop = app.descendants(matching: .any)["avatar-crop"]
        XCTAssertTrue(crop.waitForExistence(timeout: 20), "the crop sheet did not open")
        let before = crop.screenshot().pngRepresentation
        let frame = crop.frame

        // Up the picture: the finger goes from the middle to near the top of the square.
        let start = crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
        start.press(forDuration: 0.05, thenDragTo: crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)))
        XCTAssertEqual(crop.frame, frame, "the sheet scrolled instead of the picture moving")
        XCTAssertNotEqual(crop.screenshot().pngRepresentation, before, "the picture did not follow the finger")
        let dragged = crop.screenshot().pngRepresentation

        // A quick flick, as people do: the same.
        crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2))
            .press(forDuration: 0, thenDragTo: crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.8)),
                   withVelocity: .fast, thenHoldForDuration: 0)
        XCTAssertEqual(crop.frame, frame, "a flick scrolled the sheet")
        XCTAssertNotEqual(crop.screenshot().pngRepresentation, dragged, "a flick did not move the picture")
        shot(app, "crop-dragged")

        app.buttons["avatar-upload"].tap()
        XCTAssertTrue(crop.waitForNonExistence(timeout: 30), "the sheet did not close after the upload")
        let made = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-u-' AND label CONTAINS 'Sunny'")).firstMatch
        XCTAssertTrue(made.waitForExistence(timeout: 10), "the avatar made is not listed")
        let selected = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: made)
        XCTAssertEqual(XCTWaiter.wait(for: [selected], timeout: 20), .completed, "the avatar made is not chosen here")
        shot(app, "after-upload")

        // The delete button sits on the uploaded cards only: the one made and the demo's, not Mochi or the hub's package.
        let madeID = String(made.identifier.dropFirst("persona-".count))
        let demo = app.buttons["persona-\(Self.demoID)"]
        XCTAssertTrue(demo.exists && app.buttons["persona-nwverse"].exists, "the demo upload or the hub's package is not listed")
        let deleteMade = app.buttons["persona-delete-\(madeID)"]
        XCTAssertTrue(deleteMade.exists, "no delete button on the avatar made")
        XCTAssertTrue(app.buttons["persona-delete-\(Self.demoID)"].exists, "no delete button on the demo upload")
        for id in ["persona", "nwverse"] {
            XCTAssertFalse(app.buttons["persona-delete-\(id)"].exists, "a delete button on \(id), which was not uploaded")
        }
        let deletes = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-delete-'"))
        XCTAssertEqual(deletes.count, 1 + Self.demoUploads, "delete buttons: \(deletes.allElementsBoundByIndex.map(\.identifier))")
        // On a hub that edits them (#15449), Edit is on the same cards, and only on them.
        let edits = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-edit-'"))
        if edits.count > 0 {
            XCTAssertEqual(edits.count, deletes.count, "edit buttons: \(edits.allElementsBoundByIndex.map(\.identifier))")
            XCTAssertTrue(app.buttons["persona-edit-\(madeID)"].exists && !app.buttons["persona-edit-nwverse"].exists)
        }

        // The demo's button asks about the demo, and chooses nothing; asked about again and cancelled, it stays.
        app.buttons["persona-delete-\(Self.demoID)"].tap()
        XCTAssertTrue(app.buttons["persona-delete-confirm"].waitForExistence(timeout: 5), "no confirmation")
        XCTAssertTrue(title(app, "豆豆").exists, "the confirmation is not about 豆豆")
        dismissConfirmation(app)
        // Choosing 「豆豆」 would download it first: give a wrong choice the time to land.
        let unchosen = expectation(for: NSPredicate(format: "selected == false"), evaluatedWith: made)
        XCTAssertEqual(XCTWaiter.wait(for: [unchosen], timeout: 3), .timedOut, "the delete button chose its card")
        XCTAssertFalse(demo.isSelected)

        deleteMade.tap()
        let confirm = app.buttons["persona-delete-confirm"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirmation")
        XCTAssertTrue(title(app, "Sunny").exists, "the confirmation is not about the avatar made")
        XCTAssertFalse(title(app, "豆豆").exists, "the confirmation is about 豆豆")
        shot(app, "delete-confirmation")
        confirm.tap()
        XCTAssertTrue(made.waitForNonExistence(timeout: 20), "the deleted avatar is still listed")
        let builtin = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: app.buttons["persona-persona"])
        XCTAssertEqual(XCTWaiter.wait(for: [builtin], timeout: 20), .completed, "the default did not come back")
        XCTAssertTrue(demo.exists, "the demo upload went too: another avatar was deleted")
        XCTAssertTrue(app.buttons["persona-delete-\(Self.demoID)"].exists)
        shot(app, "after-delete")
    }

    /// The mock's uploaded avatar at start (MOCK_LOOK_UPLOADS=demo), one made before the hub wrote colors.
    private static let demoID = "u-demo0001"
    /// The uploaded avatars the mock starts with: `demoID`, and `u-demo0002` with colors and pictures of its own (#15449).
    private static let demoUploads = 2

    /// The delete confirmation's title for `name`, in any of the three languages.
    @MainActor private func title(_ app: XCUIApplication, _ name: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label IN %@", ["Delete “\(name)”?", "删除「\(name)」？", "「\(name)」を削除しますか？"])).firstMatch
    }

    /// Closes the delete confirmation without deleting: its Cancel where the dialog has one, else a tap outside it.
    @MainActor private func dismissConfirmation(_ app: XCUIApplication) {
        let cancel = app.buttons.matching(NSPredicate(format: "label IN %@", ["Cancel", "取消", "キャンセル"])).firstMatch
        if cancel.exists {
            cancel.tap()
        } else {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.95)).tap()
        }
        XCTAssertTrue(app.buttons["persona-delete-confirm"].waitForNonExistence(timeout: 5), "the confirmation did not close")
    }

    /// The avatars, no sheet over them; `extra` launch arguments after the pairing's.
    /// The system's "Would Like to Send You Notifications" prompt, on a simulator where it was never answered (a freshly
    /// booted one): while it is up the app is inactive and keeps its connection closed, so the hub's features (and with
    /// them New from Photo and the delete buttons) never arrive. Answered the way the other UI tests do (ExtensionV3Tests).
    @MainActor private func allowNotifications() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
    }

    @MainActor private func launchPicker(_ hub: String, _ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", "persona", "-openPersona", "persona",
                               "-ui.scheme", env["PERSONA_LOOK_APPEARANCE"] ?? "light"] + extra
        if let language = env["PERSONA_LOOK_LANGUAGE"] { app.launchArguments += ["-AppleLanguages", "(\(language))"] }
        app.launchPinned()
        allowNotifications()
        return app
    }

    /// At the smallest text (the system's smallest, and Small in the app) the trash can drawn in the corner shrinks with
    /// the text, but the corner that answers stays a finger's size: 44 pt (it was 36 pt, `scaledFrame` alone).
    @MainActor func testTheDeleteButtonStaysAFingersSizeAtTheSmallestText() throws {
        let app = launchPicker(try hub("PERSONA_MOCK_HUB"),
                               ["-ui.textSize", "small", "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryXS"])
        defer { app.terminate() }
        let delete = app.buttons["persona-delete-\(Self.demoID)"]
        XCTAssertTrue(delete.waitForExistence(timeout: 20), "the demo upload is not listed")
        shot(app, "smallest-text")
        let frame = delete.frame
        XCTAssertGreaterThanOrEqual(frame.width, 43.5, "the delete button is narrower than a finger: \(frame)")
        XCTAssertGreaterThanOrEqual(frame.height, 43.5, "the delete button is shorter than a finger: \(frame)")
        // Still in its card's top trailing corner.
        let card = app.buttons["persona-\(Self.demoID)"].frame
        XCTAssertEqual(frame.maxX, card.maxX, accuracy: 1, "the delete button left its card's trailing edge: \(frame) in \(card)")
        XCTAssertEqual(frame.minY, card.minY, accuracy: 1, "the delete button left its card's top: \(frame) in \(card)")
    }

    /// The grid is one Form row, and a `.contextMenu` in it becomes the whole row's: a long press on Mochi lifts nothing
    /// and offers no other card's menu (it offered an uploaded avatar's Delete, or New from Photo's Photos and Files),
    /// while New from Photo held still offers its own.
    @MainActor func testALongPressOnACardOffersNoOtherCardsMenu() throws {
        let app = launchPicker(try hub("PERSONA_MOCK_HUB"))
        defer { app.terminate() }
        let mochi = app.buttons["persona-persona"]
        XCTAssertTrue(app.buttons["persona-delete-\(Self.demoID)"].waitForExistence(timeout: 20), "the demo upload is not listed")
        XCTAssertTrue(app.buttons["persona-new"].exists)
        mochi.press(forDuration: 1.2)
        let menu = app.buttons.matching(NSPredicate(format: "label IN %@", ["Delete Avatar", "删除形象", "アバターを削除",
                                                                         "Choose from Photos", "从照片选取", "写真から選択",
                                                                         "Choose Image File", "选取图片文件", "画像ファイルを選択"])).firstMatch
        let opened = menu.waitForExistence(timeout: 3)
        shot(app, "long-press-mochi")
        XCTAssertFalse(opened, "a long press on Mochi opened another card's menu: \(menu.label)")

        app.buttons["persona-new"].press(forDuration: 1.2)
        let photos = app.buttons.matching(NSPredicate(format: "label IN %@", ["Choose from Photos", "从照片选取", "写真から選択"])).firstMatch
        let files = app.buttons.matching(NSPredicate(format: "label IN %@", ["Choose Image File", "选取图片文件", "画像ファイルを選択"])).firstMatch
        XCTAssertTrue(photos.waitForExistence(timeout: 5), "New from Photo held offers no Photos")
        XCTAssertTrue(files.exists, "New from Photo held offers no Files")
        shot(app, "long-press-new")

        // A tap, as before: Photos, over the page.
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.95)).tap()
        XCTAssertTrue(photos.waitForNonExistence(timeout: 5), "the menu did not close")
        app.buttons["persona-new"].tap()
        let covered = expectation(for: NSPredicate(format: "hittable == false"), evaluatedWith: app.buttons["persona-new"])
        XCTAssertEqual(XCTWaiter.wait(for: [covered], timeout: 10), .completed, "a tap on New from Photo opened nothing")
        shot(app, "tap-new")
    }

    /// The hub made it but its files don't come: the sheet stays, says so, and its button turns into Try Again; closing
    /// it then asks nothing (the avatar is in the list, not chosen).
    @MainActor func testMadeButNotDownloadedSaysSoAndOffersTryAgain() throws {
        let app = launch(try hub("PERSONA_MOCK_HUB_NO_FILES"), try portrait())
        defer { app.terminate() }
        let crop = app.descendants(matching: .any)["avatar-crop"]
        XCTAssertTrue(crop.waitForExistence(timeout: 20), "the crop sheet did not open")
        let upload = app.buttons["avatar-upload"]
        upload.tap()
        XCTAssertTrue(app.descendants(matching: .any)["avatar-upload-error"].waitForExistence(timeout: 30), "no error after a failed download")
        XCTAssertTrue(crop.exists, "the sheet closed although the avatar is not on this device")
        let again = NSPredicate(format: "label IN %@ AND enabled == true", ["Try Again", "再试一次", "再試行"])
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: again, evaluatedWith: upload)], timeout: 10), .completed,
                       "the button is not Try Again: \(upload.label)")
        XCTAssertFalse(app.textFields["avatar-upload-name"].isEnabled, "the name can still be edited after the avatar was made")
        shot(app, "crop-made-not-downloaded")

        upload.tap() // still no files: the same, and the sheet stays
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: again, evaluatedWith: upload)], timeout: 30), .completed)
        XCTAssertTrue(crop.exists)

        // Close (the sheet's ✕, #9653): nothing to lose.
        app.buttons["sheet-close"].tap()
        XCTAssertTrue(crop.waitForNonExistence(timeout: 10), "Close did not close the sheet (or asked first)")
        let made = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-u-' AND label CONTAINS 'Sunny'")).firstMatch
        XCTAssertTrue(made.waitForExistence(timeout: 10), "the avatar made is not listed")
        XCTAssertFalse(made.isSelected)
        XCTAssertTrue(app.buttons["persona-persona"].isSelected)
    }


    // MARK: Pictures for each state and the theme color (#15449)

    /// A small animated GIF (four frames), for a state's own picture.
    private func animated() throws -> String {
        let url = FileManager.default.temporaryDirectory.appending(path: "avatar-upload-wave.gif")
        let dest = try XCTUnwrap(CGImageDestinationCreateWithURL(url as CFURL, UTType.gif.identifier as CFString, 4, nil))
        for i in 0..<4 {
            let format = UIGraphicsImageRendererFormat()
            format.scale = 1
            let frame = UIGraphicsImageRenderer(size: CGSize(width: 200, height: 200), format: format).image { ctx in
                UIColor(hue: Double(i) / 4, saturation: 0.6, brightness: 0.9, alpha: 1).setFill()
                ctx.fill(CGRect(x: 0, y: 0, width: 200, height: 200))
            }
            CGImageDestinationAddImage(dest, try XCTUnwrap(frame.cgImage),
                                       [kCGImagePropertyGIFDictionary: [kCGImagePropertyGIFDelayTime: 0.2]] as CFDictionary)
        }
        XCTAssertTrue(CGImageDestinationFinalize(dest))
        return url.path(percentEncoded: false)
    }

    /// The hub's list as the mock keeps it.
    private func looks(_ hub: String) async throws -> [[String: Any]] {
        var req = URLRequest(url: try XCTUnwrap(URL(string: hub + "/api/v1/looks")))
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        let (body, _) = try await URLSession.shared.data(for: req)
        let list = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        return try XCTUnwrap(list["looks"] as? [[String: Any]])
    }

    private func source(_ look: [String: Any]?) -> [String: Any]? {
        (look?["manifest"] as? [String: Any])?["source"] as? [String: Any]
    }

    @MainActor func testAStatesOwnPictureAndAChosenColorReachTheHub() async throws {
        let hub = try hub("PERSONA_MOCK_HUB")
        let app = launch(hub, try portrait(), name: "Mikado",
                         extra: ["-showAvatarStatePicture", try animated(), "-showAvatarCropColor", "#3A7BD5"])
        defer { app.terminate() }
        XCTAssertTrue(app.descendants(matching: .any)["avatar-crop"].waitForExistence(timeout: 20), "the crop sheet did not open")
        let states = app.buttons["avatar-state-pictures"]
        XCTAssertTrue(states.waitForExistence(timeout: 10), "no Pictures for Each State on a hub that takes them")
        states.tap()
        XCTAssertTrue(app.descendants(matching: .any)["avatar-state-working"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.switches["avatar-state-mark-working"].exists && app.switches["avatar-state-mark-thinking"].exists)
        XCTAssertFalse(app.switches["avatar-state-mark-idle"].exists) // idle has no mark
        shot(app, "states-own-pictures")
        app.navigationBars.buttons.firstMatch.tap()
        let upload = app.buttons["avatar-upload"]
        XCTAssertTrue(upload.waitForExistence(timeout: 10))
        upload.tap()
        XCTAssertTrue(upload.waitForNonExistence(timeout: 30), "the sheet did not close after the upload")
        let made = try await looks(hub).first { ($0["manifest"] as? [String: Any])?["name"] as? String == "Mikado" }
        let color = source(made)?["color"] as? [String: Any]
        XCTAssertEqual(color?["seed"] as? String, "#3A7BD5")
        XCTAssertEqual(color?["auto"] as? Bool, false)
        let own = source(made)?["states"] as? [String: [String: Bool]]
        XCTAssertEqual(own?["thinking"]?["mark"], true)
        XCTAssertEqual(own?["working"]?["mark"], false)
        XCTAssertNil(own?["idle"])
        // Gone again, so the other tests find the mock's own uploads only.
        if let id = made?["id"] as? String {
            var req = URLRequest(url: try XCTUnwrap(URL(string: hub + "/api/v1/looks/uploads/" + id)))
            req.httpMethod = "DELETE"
            req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
            _ = try await URLSession.shared.data(for: req)
        }
    }

    @MainActor func testEditSavesTheChangeAndKeepsTheAvatar() async throws {
        let hub = try hub("PERSONA_MOCK_HUB")
        let before = try await looks(hub).first { $0["id"] as? String == "u-demo0002" }
        let rev = try XCTUnwrap(before?["rev"] as? String, "needs MOCK_LOOK_UPLOADS=demo")
        let app = launchPicker(hub, ["-showAvatarEdit", "u-demo0002"])
        defer { app.terminate() }
        let save = app.buttons["avatar-upload"]
        XCTAssertTrue(save.waitForExistence(timeout: 20), "the Edit sheet did not open")
        XCTAssertFalse(save.isEnabled, "Save before anything changed")
        app.buttons["avatar-state-pictures"].tap()
        let mark = app.switches["avatar-state-mark-offline"]
        XCTAssertTrue(mark.waitForExistence(timeout: 10), "offline's own picture has no mark switch")
        let wasOn = (mark.value as? String) == "1"
        mark.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap() // the switch, at the row's end
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(save.waitForExistence(timeout: 10) && save.isEnabled, "Save not enabled after a change")
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 30), "the sheet did not close after Save")
        let after = try await looks(hub).first { $0["id"] as? String == "u-demo0002" }
        XCTAssertNotEqual(after?["rev"] as? String, rev, "the package was not changed")
        let offline = (source(after)?["states"] as? [String: [String: Bool]])?["offline"]
        XCTAssertEqual(offline?["mark"], !wasOn)
        XCTAssertTrue(app.buttons["persona-u-demo0002"].exists, "the edited avatar is gone from the grid")
    }
}
