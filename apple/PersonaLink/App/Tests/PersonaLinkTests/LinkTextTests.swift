import Foundation
import XCTest
@testable import PersonaLink

/// Runs `body` with `linkText` reading one language's resources. Swift 6.2's `swift test` (Xcode 26.0 CI) copies
/// `Localizable.xcstrings` into `Bundle.module` without compiling `.lproj` directories, whereas newer toolchains and
/// `xcodebuild` (`mac-link.sh`) compile it at build time; compile the catalog on demand when `.lproj` is absent.
func withLinkLanguage(_ language: String, in resourceBundle: Bundle = .module,
                      compiledFallback: (() throws -> URL)? = nil,
                      file: StaticString = #filePath, line: UInt = #line,
                      _ body: () throws -> Void) throws {
    let resolvedPath: String?
    if let builtPath = resourceBundle.path(forResource: language, ofType: "lproj") {
        resolvedPath = builtPath
    } else {
        let dir = try (compiledFallback ?? { try CompiledLinkCatalog.moduleDirectory.get() })()
        resolvedPath = CompiledLinkCatalog.lprojPath(for: language, in: dir)
    }
    let path = try XCTUnwrap(resolvedPath, "no \(language) resources", file: file, line: line)
    let saved = linkTextBundle
    linkTextBundle = try XCTUnwrap(Bundle(path: path), file: file, line: line)
    defer { linkTextBundle = saved }
    try body()
}

/// Compiles `Localizable.xcstrings` into `.lproj` directories when SwiftPM left the String Catalog uncompiled (#6258).
private enum CompiledLinkCatalog {
    static let sourceCatalog = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("Sources/PersonaLink/Resources/Localizable.xcstrings")

    nonisolated(unsafe) private static var moduleTempPath: String?
    static let moduleDirectory: Result<URL, Error> = Result {
        let dir = try compile(in: .module)
        moduleTempPath = dir.path
        atexit {
            if let path = CompiledLinkCatalog.moduleTempPath {
                try? FileManager.default.removeItem(atPath: path)
            }
        }
        return dir
    }

    static func lprojPath(for language: String, in directory: URL) -> String? {
        let candidate = directory.appendingPathComponent("\(language).lproj").path
        var isDir: ObjCBool = false
        return FileManager.default.fileExists(atPath: candidate, isDirectory: &isDir) && isDir.boolValue ? candidate : nil
    }

    static func compile(in resourceBundle: Bundle) throws -> URL {
        guard let catalog = resourceBundle.url(forResource: "Localizable", withExtension: "xcstrings") else {
            throw NSError(
                domain: "CompiledLinkCatalog",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: "no Localizable.xcstrings in \(resourceBundle.bundlePath)"]
            )
        }
        let output = FileManager.default.temporaryDirectory
            .appendingPathComponent("PersonaLinkTests-l10n-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/xcrun")
        process.arguments = ["xcstringstool", "compile", catalog.path, "--output-directory", output.path]
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
        } catch {
            try? FileManager.default.removeItem(at: output)
            throw error
        }
        let message = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        process.waitUntilExit()
        if process.terminationStatus != 0 {
            try? FileManager.default.removeItem(at: output)
            throw NSError(
                domain: "CompiledLinkCatalog",
                code: Int(process.terminationStatus),
                userInfo: [NSLocalizedDescriptionKey: "xcstringstool compile failed (\(process.terminationStatus)): \(message)"]
            )
        }
        return output
    }
}

final class LinkTextTests: XCTestCase {
    func testDuration() throws {
        try withLinkLanguage("zh-Hans") { try durationCases() }
    }

    func testEnglishAndJapaneseText() throws {
        let since = Date(timeIntervalSince1970: 1_000_000)
        let later = Date(timeIntervalSince1970: 1_000_000 + 7_320)
        try withLinkLanguage("en") {
            XCTAssertEqual(LinkText.duration(since: since, now: later), "2 h 2 min")
            XCTAssertEqual(LinkText.hub(name: "x", mesh: 2), "Hub “x” · 2 hubs in its mesh")
            XCTAssertEqual(LinkText.mode(true), "The hub connects to me (passive)")
            XCTAssertEqual(LinkText.kinds["lan"], "LAN")
            XCTAssertEqual(StatePill.title(.unpaired), "Not Paired")
            XCTAssertTrue(PairLink.notice(for: .otherApp)?.contains("not for pairing this computer") == true)
        }
        try withLinkLanguage("ja") {
            XCTAssertEqual(LinkText.duration(since: since, now: later), "2時間2分")
            XCTAssertEqual(LinkText.hub(name: "x", mesh: 1), "ハブ「x」・他のハブとは連携していません")
            XCTAssertEqual(StatePill.title(.connected), "接続済み")
        }
    }

    /// Simulates Swift 6.2 `swift test`, where SwiftPM copies `Localizable.xcstrings` into the resource bundle without
    /// generating `.lproj` directories.
    func testUncompiledCatalogBundleCompilesOnDemand() throws {
        let rawDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("PersonaLinkRaw-\(UUID().uuidString).bundle", isDirectory: true)
        try FileManager.default.createDirectory(at: rawDir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: rawDir) }
        try FileManager.default.copyItem(
            at: CompiledLinkCatalog.sourceCatalog,
            to: rawDir.appendingPathComponent("Localizable.xcstrings")
        )
        let rawBundle = try XCTUnwrap(Bundle(url: rawDir))
        XCTAssertNil(rawBundle.path(forResource: "zh-Hans", ofType: "lproj"))

        var cachedDir: URL?
        let fallback: () throws -> URL = { [weak self] in
            if let cachedDir { return cachedDir }
            let dir = try CompiledLinkCatalog.compile(in: rawBundle)
            self?.addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
            cachedDir = dir
            return dir
        }

        let since = Date(timeIntervalSince1970: 1_000_000)
        let later = Date(timeIntervalSince1970: 1_000_000 + 7_320)
        try withLinkLanguage("zh-Hans", in: rawBundle, compiledFallback: fallback) {
            try durationCases()
            try hubLineCases()
            XCTAssertTrue(PairLink.notice(for: .otherApp)?.contains("这是给手机 / Mac App 用的链接") == true)
        }
        try withLinkLanguage("en", in: rawBundle, compiledFallback: fallback) {
            XCTAssertEqual(LinkText.duration(since: since, now: later), "2 h 2 min")
        }
        try withLinkLanguage("ja", in: rawBundle, compiledFallback: fallback) {
            XCTAssertEqual(StatePill.title(.connected), "接続済み")
        }
    }

    private func durationCases() throws {
        let since = Date(timeIntervalSince1970: 1_000_000)
        let cases = [(30, "不到 1 分钟"), (60, "1 分钟"), (3660, "1 小时 1 分钟"), (7320, "2 小时 2 分钟")]
        for (seconds, expected) in cases {
            XCTAssertEqual(LinkText.duration(since: since, now: Date(timeIntervalSince1970: 1_000_000 + Double(seconds))), expected)
        }
    }

    func testHubLine() throws {
        try withLinkLanguage("zh-Hans") { try hubLineCases() }
    }

    private func hubLineCases() throws {
        XCTAssertEqual(LinkText.hub(name: "x", mesh: nil), "hub「x」")
        XCTAssertEqual(LinkText.hub(name: "x", mesh: 1), "hub「x」· 没和别的 hub 互联")
        XCTAssertTrue(LinkText.hub(name: "x", mesh: 1).contains("没和别的 hub 互联"))
        XCTAssertEqual(LinkText.hub(name: "x", mesh: 2), "hub「x」· 互联组共 2 台 hub")
        XCTAssertTrue(LinkText.hub(name: "x", mesh: 2).contains("互联组共 2 台 hub"))
    }

    func testSSHTextHelpers() throws {
        XCTAssertEqual(LinkText.kinds["ssh"], "SSH")
        XCTAssertEqual(LinkText.sshLoginCommand("user@example.com"), "ssh user@example.com")
        XCTAssertEqual(LinkText.sshLoginCommand("user@example.com:2222"), "ssh -p 2222 user@example.com")
        XCTAssertEqual(LinkText.sshLoginCommand("user@[2001:db8::1]:2222"), "ssh -p 2222 user@2001:db8::1")
        for code in ["no_ssh", "auth_refused", "host_key", "forward_refused", "target_unreachable"] {
            let msg = LinkText.sshError(SSHStatusError(code: code, server: "user@example.com", target: "127.0.0.1:18662"))
            XCTAssertFalse(msg.isEmpty)
        }
    }
}
