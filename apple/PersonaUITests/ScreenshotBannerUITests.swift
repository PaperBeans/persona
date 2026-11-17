import XCTest

/// The screenshot banner (#9508, docs/apple/ios.md 「Screenshot banner」) against scripts/mock/mock_hub.py
/// (PERSONA_MOCK_HUB): with Settings' switch on, a system screenshot of a chat (XCUIScreen: taken outside the app, as
/// the side and volume-up buttons take one) shows the assistant's banner where the top bar is, while the bar stays on
/// screen, takes taps and is VoiceOver's; off, the screenshot shows the bar. The Assistant chat and an Agent chat, light
/// and dark; and the switch in Settings › Appearance & Display. TEST_RUNNER_PERSONA_SHOTS=<dir> keeps
/// `screenshot-banner-<assistant|agent>-<on|off>-<light|dark>.png` and `screenshot-banner-settings-<light|dark>.png`.
///   PORT=18793 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18793 xcodebuild test … -only-testing:PersonaUITests/ScreenshotBannerUITests
final class ScreenshotBannerUITests: XCTestCase {
    private var hub = ""
    private static let main = "server/b2e4d6f8-0000-4000-8000-000000000001"
    private static let agent = "macbook/a1f3c2d4-0000-4000-8000-000000000002"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        hub = h
    }

    @MainActor func testAssistantChat() throws {
        for scheme in ["light", "dark"] {
            let off = try look(banner: false, scheme: scheme, page: "assistant", ["-openAssistant", Self.main], control: "assistant-new")
            let on = try look(banner: true, scheme: scheme, page: "assistant", ["-openAssistant", Self.main], control: "assistant-new",
                              tapThrough: "assistant-welcome")
            XCTAssertGreaterThan(off, 40, "\(scheme): with the switch off the screenshot has the bar's New Chat")
            XCTAssertLessThan(on, 12, "\(scheme): with it on the screenshot has the banner where New Chat is")
        }
    }

    @MainActor func testAgentChat() throws {
        let args = ["-openSession", Self.agent, "-holdChromeOpen", "YES"]
        for scheme in ["light", "dark"] {
            let off = try look(banner: false, scheme: scheme, page: "agent", args, control: "chat.more")
            let on = try look(banner: true, scheme: scheme, page: "agent", args, control: "chat.more")
            XCTAssertGreaterThan(off, 40, "\(scheme): with the switch off the screenshot has the header's ⋯")
            XCTAssertLessThan(on, 12, "\(scheme): with it on the screenshot has the banner where ⋯ is")
        }
    }

    /// The iPad's bar (`classicBar`: the face over the name in the middle, New Chat beside the sidebar): on screen and
    /// for VoiceOver with the switch on, and the screenshots kept for a look (the lockup can reach the middle of a
    /// narrow page, so no pixel check here). Skips on a phone.
    @MainActor func testIPad() throws {
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("an iPad's bar") }
        for scheme in ["light", "dark"] {
            for banner in [false, true] {
                _ = try look(banner: banner, scheme: scheme, page: "ipad", ["-openAssistant", Self.main], control: "persona-avatar")
            }
        }
    }

    /// Settings › Appearance & Display has the switch (on: the UI tests pass it, a fresh install's default is
    /// `ScreenshotBannerPrefs.enabledByDefault`, ScreenshotBannerTests).
    @MainActor func testTheSwitch() throws {
        for scheme in ["light", "dark"] {
            let app = launch(["-ui.scheme", scheme, "-tab", "settings", "-app.tab", "settings"], banner: true)
            defer { app.terminate() }
            let appearance = app.descendants(matching: .any)["settings-appearance"].firstMatch
            XCTAssertTrue(app.descendants(matching: .any)["settings-list"].firstMatch.waitForExistence(timeout: 30), "no Settings")
            for _ in 0..<8 where !appearance.isHittable { app.swipeUp() }
            appearance.tap()
            let toggle = app.switches["settings-screenshot-banner"].firstMatch
            XCTAssertTrue(app.descendants(matching: .any)["settings-style"].firstMatch.waitForExistence(timeout: 15), "no Appearance & Display")
            for _ in 0..<6 where !toggle.isHittable { app.swipeUp() }
            XCTAssertTrue(toggle.exists, "no Persona Banner in Screenshots switch")
            XCTAssertEqual(toggle.value as? String, "1")
            save(app.screenshot(), "screenshot-banner-settings-\(scheme)")
        }
    }

    // MARK: -

    @MainActor private func launch(_ extra: [String], banner: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let tab = extra.contains("-app.tab") ? [] : ["-app.tab", "assistant"]
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-openChatsFresh", "YES", "-ui.screenshotBanner", banner ? "YES" : "NO"]
            + tab + extra
        app.launchPinned()
        return app
    }

    /// Opens the page, checks its bar is on screen (it takes taps, VoiceOver reaches it) and the banner is not in the
    /// accessibility tree, takes a system screenshot and says how much the control's square varies in it (the
    /// lightest pixel's luminance less the darkest's): a button has edges, the banner's backdrop is one color.
    @MainActor private func look(banner: Bool, scheme: String, page: String, _ extra: [String], control id: String,
                                 tapThrough: String? = nil) throws -> Int {
        let app = launch(["-ui.scheme", scheme] + extra, banner: banner)
        defer { app.terminate() }
        let el = app.descendants(matching: .any)[id].firstMatch
        XCTAssertTrue(el.waitForExistence(timeout: 30), "\(page): no \(id)")
        XCTAssertTrue(el.isHittable, "\(page): \(id) is on screen")
        XCTAssertFalse(app.staticTexts["(Persona)"].exists, "\(page): the banner is out of VoiceOver's reach")
        RunLoop.current.run(until: Date().addingTimeInterval(2))
        let shot = XCUIScreen.main.screenshot()
        save(shot, "screenshot-banner-\(page)-\(banner ? "on" : "off")-\(scheme)")
        let spread = try Self.spread(shot, el.frame.insetBy(dx: 4, dy: 4))
        if let tapThrough {
            // The bar in the capture layer still takes its taps.
            el.tap()
            XCTAssertTrue(app.descendants(matching: .any)[tapThrough].firstMatch.waitForExistence(timeout: 10), "\(page): \(id) took no tap")
        }
        return spread
    }

    private static func spread(_ shot: XCUIScreenshot, _ rect: CGRect) throws -> Int {
        let image = shot.image
        let cg = try XCTUnwrap(image.cgImage)
        let scale = CGFloat(cg.width) / image.size.width
        let r = CGRect(x: rect.minX * scale, y: rect.minY * scale, width: rect.width * scale, height: rect.height * scale).integral
        let crop = try XCTUnwrap(cg.cropping(to: r))
        var px = [UInt8](repeating: 0, count: crop.width * crop.height * 4)
        let ctx = try XCTUnwrap(CGContext(data: &px, width: crop.width, height: crop.height, bitsPerComponent: 8, bytesPerRow: crop.width * 4,
                                          space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(crop, in: CGRect(x: 0, y: 0, width: crop.width, height: crop.height))
        var lo = 255, hi = 0
        for i in stride(from: 0, to: px.count, by: 4) {
            let l = (Int(px[i]) * 299 + Int(px[i + 1]) * 587 + Int(px[i + 2]) * 114) / 1000
            lo = min(lo, l)
            hi = max(hi, l)
        }
        return hi - lo
    }

    @MainActor private func save(_ shot: XCUIScreenshot, _ name: String) {
        let a = XCTAttachment(screenshot: shot)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? shot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}
