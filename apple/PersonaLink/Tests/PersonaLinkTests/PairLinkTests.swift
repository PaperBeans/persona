import Foundation
import XCTest
@testable import PersonaLink

/// `persona://link?…` opened from outside (#4198): fills in the pairing window, never pairs.
@MainActor
final class PairLinkTests: XCTestCase {
    private let full = "persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&token=jp_example&machine=macbook&hub=h_example&fp=Zm9v&tls=18662"

    func testBothSchemeGenerationsParseIgnoringCase() throws {
        for scheme in ["persona", "PERSONA", LegacyNames.product, LegacyNames.product.uppercased()] {
            let raw = full.replacingOccurrences(of: "persona://", with: "\(scheme)://")
            guard case .link(let l) = PairLink.parse(URL(string: raw)!) else { return XCTFail("\(scheme) not parsed") }
            XCTAssertEqual(l.raw, raw)
            XCTAssertEqual(l.machine, "macbook")
            XCTAssertEqual(l.hubHost, "192.0.2.10:18660")
            XCTAssertTrue(l.pinned)
        }
    }

    func testLegacyOpeningStillRequiresAnExplicitPair() {
        let raw = full.replacingOccurrences(of: "persona://", with: LegacyNames.product + "://")
        let model = LinkModel()
        model.openLink(URL(string: raw)!)
        XCTAssertEqual(model.incoming?.raw, raw)
        XCTAssertEqual(model.pairRuns, 0)
        XCTAssertEqual(PairLink.parse(LegacyNames.product + "://pair?token=fixture"), .otherApp)
        XCTAssertEqual(PairLink.parse(LegacyNames.product + "://link?token="), .incomplete)
    }

    func testBothSchemesAreRegistered() throws {
        let package = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let plist = try PropertyListSerialization.propertyList(from: Data(contentsOf: package.appendingPathComponent("Info.plist")), format: nil) as? [String: Any]
        let types = plist?["CFBundleURLTypes"] as? [[String: Any]]
        XCTAssertEqual(Set(types?.flatMap { $0["CFBundleURLSchemes"] as? [String] ?? [] } ?? []), Set(["persona", LegacyNames.product]))
    }

    func testPassiveOutputAcceptsEitherContractOnly() {
        XCTAssertTrue(PairLink.isConnectorOutput("persona://connector?code=fixture"))
        XCTAssertTrue(PairLink.isConnectorOutput(LegacyNames.product + "://connector?code=fixture"))
        XCTAssertFalse(PairLink.isConnectorOutput("otherapp://connector?code=fixture"))
        XCTAssertFalse(PairLink.isConnectorOutput(LegacyNames.product + "://pair?token=fixture"))
    }

    func testWithoutFingerprintOrMachine() throws {
        let raw = "persona://link?url=https%3A%2F%2Fhub.example.com%2Fprefix&token=jp_example"
        guard case .link(let l) = PairLink.parse(raw) else { return XCTFail("not parsed") }
        XCTAssertNil(l.machine)
        XCTAssertFalse(l.pinned)
        XCTAssertEqual(l.hubHost, "hub.example.com/prefix")
    }

    func testOtherHostsAreForOtherApps() throws {
        XCTAssertEqual(PairLink.parse("persona://pair?url=http%3A%2F%2F192.0.2.10%3A18660&token=jp_example"), .otherApp)
        XCTAssertEqual(PairLink.parse("persona://open?machine=a&session=b"), .otherApp)
        XCTAssertEqual(PairLink.parse("persona://connector?addr=192.0.2.20%3A18663&code=jc_x"), .otherApp)
        XCTAssertNotNil(PairLink.notice(for: .otherApp))
        XCTAssertTrue(PairLink.notice(for: .otherApp)!.contains("这是给手机 / Mac App 用的链接"))
    }

    func testMissingTokenIsIncomplete() throws {
        XCTAssertEqual(PairLink.parse("persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&machine=macbook"), .incomplete)
        XCTAssertEqual(PairLink.parse("persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&token="), .incomplete)
        XCTAssertNotNil(PairLink.notice(for: .incomplete))
    }

    func testNotOurs() throws {
        for other in ["other-product", "personas", "personad"] {
            XCTAssertEqual(PairLink.parse("\(other)://link?token=fixture"), .notOurs)
        }
        XCTAssertEqual(PairLink.parse("https://example.com/link?token=x"), .notOurs)
        XCTAssertEqual(PairLink.parse("not a url"), .notOurs)
        XCTAssertNil(PairLink.notice(for: .notOurs))
    }

    /// Opening the link fills the window in (the link and its machine) and does nothing else.
    func testOpeningPrefillsWithoutPairing() throws {
        let m = LinkModel()
        m.openLink(URL(string: full)!)
        XCTAssertEqual(m.incoming?.raw, full)
        XCTAssertEqual(m.incoming?.machine, "macbook")
        XCTAssertNil(m.incomingNotice)
        XCTAssertEqual(m.pairRuns, 0, "opening a link must never pair by itself")
        m.clearIncoming()
        XCTAssertNil(m.incoming)
        XCTAssertEqual(m.pairRuns, 0)
    }

    func testOpeningAnotherAppsLinkOnlySaysSo() throws {
        let m = LinkModel()
        m.openLink(URL(string: full)!)
        m.clearIncoming()
        m.openLink(URL(string: "persona://pair?url=http%3A%2F%2F192.0.2.10%3A18660&token=jp_example")!)
        XCTAssertNil(m.incoming)
        XCTAssertNotNil(m.incomingNotice)
        XCTAssertEqual(m.pairRuns, 0)
        // A link for this app afterwards takes the notice away.
        m.openLink(URL(string: full)!)
        XCTAssertNil(m.incomingNotice)
        XCTAssertNotNil(m.incoming)
    }

    /// The status lines' `pending`: shown as 「等你确认」 (waiting for you to confirm) while the hub waits for 「是我的电脑」; without it, connected.
    func testPendingLook() throws {
        let m = LinkModel()
        var s = LinkStatus(state: "connected", machine: "macbook", node: "online")
        m.show(s)
        XCTAssertEqual(m.look, .connected)
        s.pending = true
        m.show(s)
        XCTAssertEqual(m.look, .awaitingConfirm)
        s.pending = false
        s.node = "offline"
        m.show(s)
        XCTAssertEqual(m.look, .nodeMissing)
        let line = #"{"state":"connected","machine":"macbook","node":"online","pending":true}"#
        XCTAssertEqual(try LinkModel.decoder.decode(LinkStatus.self, from: Data(line.utf8)).pending, true)
        let older = #"{"state":"connected","machine":"macbook","node":"online"}"#
        XCTAssertNil(try LinkModel.decoder.decode(LinkStatus.self, from: Data(older.utf8)).pending)
    }

    func testSSHDefaultsFromPairingLink() throws {
        guard case .link(let lan) = PairLink.parse(full) else { return XCTFail("lan link not parsed") }
        XCTAssertEqual(lan.defaultSSHTarget, "127.0.0.1:18662")
        XCTAssertFalse(lan.loopbackOnly)

        let loopback = "persona://link?url=http%3A%2F%2F127.0.0.1%3A18660&token=jp_example&machine=macbook&fp=Zm9v&tls=18662"
        guard case .link(let lb) = PairLink.parse(loopback) else { return XCTFail("loopback link not parsed") }
        XCTAssertEqual(lb.defaultSSHTarget, "127.0.0.1:18662")
        XCTAssertTrue(lb.loopbackOnly)

        let proxy = "persona://link?url=https%3A%2F%2Fhub.example.com%2Fprefix&token=jp_example"
        guard case .link(let px) = PairLink.parse(proxy) else { return XCTFail("proxy link not parsed") }
        XCTAssertEqual(px.defaultSSHTarget, "127.0.0.1:443")
        XCTAssertFalse(px.loopbackOnly)
    }

    func testSSHStatusDecoding() throws {
        let json = #"{"state":"waiting","machine":"macbook","ssh":{"server":"user@example.com","target":"127.0.0.1:18662","always":true},"ssh_error":{"code":"host_key","server":"user@example.com","target":"127.0.0.1:18662"}}"#
        let decoded = try LinkModel.decoder.decode(LinkStatus.self, from: Data(json.utf8))
        XCTAssertEqual(decoded.ssh, SSHConfig(server: "user@example.com", target: "127.0.0.1:18662", always: true))
        XCTAssertEqual(decoded.sshError, SSHStatusError(code: "host_key", server: "user@example.com", target: "127.0.0.1:18662"))
    }
}
