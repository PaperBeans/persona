import Darwin
import XCTest
@testable import PersonaLink

/// The App reads persona-link's status file instead of running it (docs/operations/link.md "Tray and menu bar App").
@MainActor final class ServiceStateTests: XCTestCase {
    func testStatusFileCarriesTheWriter() throws {
        let line = #"{"state":"connected","machine":"macbook","node":"online","pid":4242,"version":"v1.2.3","updated":"2026-11-25T01:02:03.123456789Z"}"#
        let s = try LinkModel.decoder.decode(LinkStatus.self, from: Data(line.utf8))
        XCTAssertEqual(s.pid, 4242)
        XCTAssertEqual(s.version, "v1.2.3")
    }

    func testAliveOnlyForALiveWriterThatDidNotStop() {
        let me = Int(getpid())
        XCTAssertTrue(LinkModel.alive(LinkStatus(state: "connected", pid: me)))
        XCTAssertFalse(LinkModel.alive(LinkStatus(state: "stopped", pid: me)), "a persona-link that said it stopped")
        XCTAssertFalse(LinkModel.alive(LinkStatus(state: "connected")), "no pid")
        XCTAssertFalse(LinkModel.alive(LinkStatus(state: "connected", pid: 0)))
        XCTAssertFalse(LinkModel.alive(nil))
    }

    func testLookWithoutARunningConnector() {
        let m = LinkModel()
        m.show(LinkStatus(state: "connected", node: "online"), running: false, machine: "macbook")
        XCTAssertEqual(m.look, .down, "paired, nothing runs")
        XCTAssertNil(m.liveStatus, "nothing a stopped persona-link said still holds")

        let fresh = LinkModel()
        fresh.show(nil, running: false, serviceInstalled: false)
        XCTAssertEqual(fresh.look, .unpaired)

        let up = LinkModel()
        up.show(LinkStatus(state: "connected", node: "online"), machine: "macbook")
        XCTAssertEqual(up.look, .connected)
        up.show(LinkStatus(state: "connected", node: "offline"), machine: "macbook")
        XCTAssertEqual(up.look, .nodeMissing)
    }

    func testIconFollowsTheWindowsTray() {
        // server/internal/linktray/icons.go iconOf: faded while the link is not up, a dot when the user is needed.
        XCTAssertEqual(LinkLook.allCases.filter(\.faded), [.connecting, .down, .unpaired])
        XCTAssertEqual(LinkLook.allCases.filter(\.badge), [.awaitingConfirm, .nodeMissing, .down])
    }

    func testMenuBarIconIsATemplateWithTheArt() {
        XCTAssertGreaterThan(MenuBarIcon.art.size.width, 0, "Resources/MenuBarIcon.png is in the bundle")
        for look in LinkLook.allCases {
            let image = MenuBarIcon.image(look)
            XCTAssertTrue(image.isTemplate)
            XCTAssertEqual(image.size, NSSize(width: 18, height: 18))
        }
        XCTAssertGreaterThan(MenuView.helper.size.width, 0, "Resources/HeaderIcon.png is in the bundle")
    }
}
