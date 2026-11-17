import Foundation
import Testing

/// What only the sources can show about the app's calls (#9290), read next to this file like `CarPlayWiringSourceTests`:
/// one LiveCommunicationKit manager in the whole app (a second one takes every action from the first), no CallKit
/// anywhere (App Store China rejects a binary that links it; LiveCommunicationKit's errors are read by their domain
/// string instead), a system call's audio never touching the system's session, and voice chat refused during an
/// urgent call.
struct CallSourceTests {
    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    /// Every Swift file under `apple/` but the local packages, with `//` comments taken out.
    /// Paths relative to `apple/`, both sides with symlinks resolved: a checkout under a symlinked folder (`/var` →
    /// `/private/var`, where scripts/ci/apple.sh puts its snapshot) is walked under the resolved name.
    private static func sources(tests: Bool) throws -> [(path: String, lines: [String])] {
        let root = apple.resolvingSymlinksInPath()
        let walker = try #require(FileManager.default.enumerator(at: root, includingPropertiesForKeys: nil))
        var out: [(String, [String])] = []
        for case let url as URL in walker where url.pathExtension == "swift" {
            let full = url.resolvingSymlinksInPath().path
            guard full.hasPrefix(root.path + "/") else { continue }
            let path = String(full.dropFirst(root.path.count + 1))
            if path.hasPrefix("Packages/") || path.contains("/.build/") || path.contains("DerivedData") { continue }
            if !tests, path.hasPrefix("PersonaTests/") || path.hasPrefix("PersonaUITests/") { continue }
            let lines = try String(contentsOf: url, encoding: .utf8).components(separatedBy: "\n")
                .map { l in l.range(of: "//").map { String(l[..<$0.lowerBound]) } ?? l }
            out.append((path, lines))
        }
        return out
    }

    @Test func theAppMakesOneConversationManagerInOnePlace() throws {
        let files = try Self.sources(tests: false)
        #expect(files.count > 500, "the sources were found")
        var sites: [String] = []
        for file in files {
            for (n, line) in file.lines.enumerated() where Self.constructs(line, "ConversationManager(") {
                sites.append("\(file.path):\(n + 1)")
            }
        }
        #expect(sites.count == 1, "\(sites)")
        #expect(sites.first?.hasPrefix("Persona/Calls/CallSystem.swift") == true, "\(sites)")
    }

    @Test func noSwiftFileImportsCallKit() throws {
        let kit = "Call" + "Kit"
        let imports = try Regex(#"^\s*(@\w+\s+)*import\s+(class\s+|struct\s+|enum\s+|protocol\s+|func\s+|var\s+|let\s+|typealias\s+)?"# + kit + #"(\.|\s|$)"#)
        let types = try Regex(#"\bCX(Call|Provider|Transaction|Handle|Action|Start|End|Answer|SetMuted|SetHeld|Error)[A-Za-z]*\b"#)
        let files = try Self.sources(tests: true)
        var found: [String] = []
        for file in files {
            for (n, line) in file.lines.enumerated() where line.contains(imports) || line.contains(types) {
                found.append("\(file.path):\(n + 1): \(line.trimmingCharacters(in: .whitespaces))")
            }
        }
        #expect(found.isEmpty, "\(found)")
    }

    /// `line` calls `call` itself: not a longer name that ends so (`TelephonyConversationManager(`), nor a member's.
    private static func constructs(_ line: String, _ call: String) -> Bool {
        var rest = line[...]
        while let r = rest.range(of: call) {
            let before = r.lowerBound == line.startIndex ? nil : line[line.index(before: r.lowerBound)]
            if before.map({ !($0.isLetter || $0.isNumber || $0 == "_" || $0 == ".") }) ?? true { return true }
            rest = line[r.upperBound...]
        }
        return false
    }

    /// The body of the first `{ … }` starting on a line that contains `start`.
    private static func body(_ lines: [String], _ start: String) -> String? {
        guard let first = lines.firstIndex(where: { $0.contains(start) }) else { return nil }
        var depth = 0, opened = false, out = ""
        for line in lines[first...] {
            for c in line {
                if c == "{" { depth += 1; opened = true }
                if c == "}" { depth -= 1 }
            }
            out += line + "\n"
            if opened, depth == 0 { return out }
        }
        return nil
    }

    private static func lines(_ path: String) throws -> [String] {
        try #require(try sources(tests: false).first { $0.path == path }).lines
    }

    /// The system owns a call's session: nothing on the system-call path sets its category, activates it, moves the
    /// route or picks a microphone (the call's manager sets the category once, before Start).
    @Test func aSystemCallsAudioNeverTouchesTheSystemsSession() throws {
        let lines = try Self.lines("Persona/Voice/PhoneVoiceAudio.swift")
        for name in ["func startOnSystemSession(", "func activate()", "func deactivate()", "func setHeld(", "func bringUp()",
                     "func tearDown()", "func interrupted()"] {
            let body = try #require(Self.body(lines, name), "\(name)")
            for forbidden in ["setActive(", "setCategory(", "overrideOutputAudioPort(", "setPreferredInput(", "follow("] {
                #expect(!body.contains(forbidden), "\(name) calls \(forbidden)")
            }
        }
        let moved = try #require(Self.body(lines, "func routeMoved("))
        #expect(moved.contains("if owner == .app {"), "only the app's own session follows VoiceRoute")
    }

    /// The refusal itself is `VoiceCalls`' (`VoiceCallsTests`, `PersonaCallsTests`); here, that every way in says it:
    /// the page's Voice Chat at once, its Continue, CarPlay's start and Continue, and that `PersonaCalls` answers it.
    @Test func voiceChatIsRefusedDuringAnUrgentCall() throws {
        let entry = try Self.lines("Persona/Views/Voice/VoiceEntry.swift")
        let start = try #require(Self.body(entry, "static func start("))
        #expect(start.contains("PersonaCalls.shared.urgentInProgress"))
        let strip = try #require(Self.body(entry, "struct VoiceEndedStrip"))
        #expect(strip.contains("refusal = await VoiceCalls.shared.resume(") && strip.contains(".voiceUnavailableAlert($refusal"))
        let car = try Self.lines("Persona/CarPlay/CarPlayVoiceSession.swift").joined(separator: "\n")
        #expect(car.contains("if let refused = await VoiceCalls.shared.start("))
        #expect(car.contains("if let refused = await VoiceCalls.shared.resume("))
        let calls = try Self.lines("Persona/Calls/PersonaCalls.swift").joined(separator: "\n")
        #expect(calls.contains("voiceCalls.urgentCallOn = { [weak self] in self?.urgentInProgress ?? false }"))
        // Every way in waits for a call to Persona to let its audio go, in `VoiceCalls` itself (CarPlay too).
        #expect(calls.contains("voiceCalls.endSystemCall = { [weak self] in await self?.endForVoiceChat() }"))
        let voiceCalls = try Self.lines("Persona/Voice/VoiceCalls.swift")
        for name in ["func start(", "func resume("] {
            let body = try #require(Self.body(voiceCalls, name), "\(name)")
            #expect(body.contains("await handOff("), "\(name)")
        }
    }
}
