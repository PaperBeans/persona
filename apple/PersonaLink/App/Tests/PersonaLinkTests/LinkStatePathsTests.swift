import Foundation
import XCTest
@testable import PersonaLink

final class LinkStatePathsTests: XCTestCase {
    func testExplicitPersonaRootAndEmptyDefault() {
        let home = URL(fileURLWithPath: "/tmp/example-home")
        for (input, expected) in [("~/profile", "/tmp/example-home/profile"), ("profile/../chosen", "/tmp/example-home/chosen"),
                                  ("~", "/tmp/example-home"), ("/var/lib/a/../state", "/var/lib/state"), ("profile space", "/tmp/example-home/profile space")] {
            XCTAssertEqual(LinkStatePaths.root(home: home, environment: ["PERSONA_HOME": input, "OTHER_HOME": "wrong"]).path, expected)
        }
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: ["PERSONA_HOME": "", "OTHER_HOME": "wrong"]).path, home.path + "/.persona")
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: ["OTHER_HOME": "wrong"]).path, home.path + "/.persona")
    }

    func testLoginUsesOnlyTheResidentNodesPersonaRootWithoutReadingCredentials() throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        let plist = home.appendingPathComponent("Library/LaunchAgents/com.persona.node.plist")
        try FileManager.default.createDirectory(at: plist.deletingLastPathComponent(), withIntermediateDirectories: true)
        let stored = ["EnvironmentVariables": ["PERSONA_HOME": "~/profile with spaces", "OTHER_HOME": "not-imported"]]
        let data = try PropertyListSerialization.data(fromPropertyList: stored, format: .xml, options: 0)
        try data.write(to: plist)
        let environment = LinkStatePaths.serviceEnvironment(home: home, environment: ["PATH": "/usr/bin"])
        XCTAssertEqual(environment, ["PATH": "/usr/bin", "PERSONA_HOME": "~/profile with spaces"])
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: environment).path, home.path + "/profile with spaces")
        XCTAssertFalse(FileManager.default.fileExists(atPath: home.path + "/profile with spaces"))
        for root in ["", "~/explicit"] {
            let explicit = ["PERSONA_HOME": root, "OTHER_HOME": "wrong"]
            XCTAssertEqual(LinkStatePaths.serviceEnvironment(home: home, environment: explicit), explicit)
        }
        try PropertyListSerialization.data(fromPropertyList: ["EnvironmentVariables": ["OTHER_HOME": "wrong"]], format: .xml, options: 0).write(to: plist)
        XCTAssertEqual(LinkStatePaths.serviceEnvironment(home: home, environment: ["PATH": "/usr/bin"]), ["PATH": "/usr/bin"])
    }

    func testPreviousRootEnvironmentAndInstalledServiceAreReadable() throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        let oldKey = LegacyNames.product.uppercased() + "_HOME"
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [oldKey: "~/profile"]).path, home.path + "/profile")
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: ["PERSONA_HOME": "", oldKey: "wrong"]).path, home.path + "/.persona")
        let oldFile = home.appendingPathComponent("Library/LaunchAgents/com." + LegacyNames.product + ".node.plist")
        try FileManager.default.createDirectory(at: oldFile.deletingLastPathComponent(), withIntermediateDirectories: true)
        let data = try PropertyListSerialization.data(fromPropertyList: ["EnvironmentVariables": [oldKey: "~/profile", "OTHER_HOME": "ignored"]], format: .xml, options: 0)
        try data.write(to: oldFile)
        let environment = LinkStatePaths.serviceEnvironment(home: home, environment: ["PATH": "/usr/bin"])
        XCTAssertEqual(environment, ["PATH": "/usr/bin", "PERSONA_HOME": "~/profile"])
        XCTAssertEqual(try Data(contentsOf: oldFile), data)
        XCTAssertFalse(FileManager.default.fileExists(atPath: home.path + "/profile"))
    }

    func testExistingPreviousRootAndSameInodeAliasAreNotMoved() throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        let old = home.appendingPathComponent("." + LegacyNames.product, isDirectory: true)
        let current = home.appendingPathComponent(".persona", isDirectory: true)
        try FileManager.default.createDirectory(at: old, withIntermediateDirectories: true)
        let file = old.appendingPathComponent("link.json"), bytes = Data("opaque fixture".utf8)
        try bytes.write(to: file)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [:]), old)
        try FileManager.default.createSymbolicLink(at: current, withDestinationURL: old)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [:]), current)
        XCTAssertEqual(try Data(contentsOf: file), bytes)
    }

    func testDefaultAlwaysNamesPersonaWithoutInspectingOrMovingOtherRoots() throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        let current = home.appendingPathComponent(".persona", isDirectory: true)
        let other = home.appendingPathComponent("other-state", isDirectory: true)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [:]), current)
        XCTAssertFalse(FileManager.default.fileExists(atPath: current.path))
        try FileManager.default.createDirectory(at: other, withIntermediateDirectories: true)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [:]), current)
        XCTAssertFalse(FileManager.default.fileExists(atPath: current.path))
        try FileManager.default.createSymbolicLink(at: current, withDestinationURL: other)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: [:]), current)
        XCTAssertEqual(LinkStatePaths.root(home: home, environment: ["PERSONA_HOME": "other-state"]), other)
        XCTAssertTrue(FileManager.default.fileExists(atPath: other.path))
    }
}
