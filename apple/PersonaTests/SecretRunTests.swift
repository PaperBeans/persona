import Foundation
import SwiftUI
import Testing
import UIKit
@testable import Persona
import PersonaWire

/// Raw non-sudo env input and strict sudo-only stdin input. Keeping the wire dictionaries here lets the tests compile
/// before the request model learns `delivery`; the UI fixture can use the same contract without sharing test code.
private enum SecretRunDeliveryFixture {
    static let envCommands = [#"sh -c 'printf "%s" "$PERSONA_SECRET"'"#, #"sh -c 'test -n "$PERSONA_SECRET"'"#]
    static let stdinCommands = ["sudo -S -k -p '' cp backup.plist /Library/LaunchDaemons/",
                                "sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/backup.plist"]

    static func interaction(delivery: String?, commands: [String]) throws -> Interaction {
        var toolInput: [String: Any] = ["machine": "studio", "machine_name": "Mac Studio", "purpose": "Run the backup check",
                                        "commands": commands]
        toolInput["delivery"] = delivery
        let warning = delivery == "env" ? "\nEvery listed command receives the typed secret via PERSONA_SECRET." : ""
        let raw: [String: Any] = [
            "id": "ix_secret_run_env", "machine_id": "server", "session_id": "s1", "kind": "permission",
            "tool_name": "secret_run", "tool_input": toolInput,
            "summary": commands.map { "$ " + $0 }.joined(separator: "\n") + warning,
            "fields": [["key": "password", "label": "Password", "secret": true]],
        ]
        return try JSON.decoder.decode(Interaction.self, from: JSONSerialization.data(withJSONObject: raw))
    }
}

/// The password card (secret_run): what the card shows from its input, and how the chat's tool row reads the tool's
/// JSON result back, command by command.
@MainActor
struct SecretRunTests {
    private let commands = ["sudo -S -k -p '' cp backup.plist /Library/LaunchDaemons/",
                            "sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/backup.plist",
                            "launchctl print system/backup"]

    private var input: JSONValue {
        .object(["machine": .string("studio"), "machine_name": .string("Mac Studio"), "purpose": .string("Install the backup job"),
                 "commands": .array(commands.map { .string($0) }), "delivery": .string("stdin")])
    }

    private func call(_ name: String = "mcp__persona__secret_run", result: String?) -> ToolCall {
        let use = Item(id: "1#0", seq: 1, ts: Date(timeIntervalSince1970: 1), role: "assistant", kind: .toolUse, text: nil,
                       toolUseID: "t1", toolName: name, toolInput: input, isError: nil, image: nil)
        let res = result.map {
            Item(id: "2#0", seq: 2, ts: Date(timeIntervalSince1970: 2), role: "user", kind: .toolResult, text: $0,
                 toolUseID: "t1", toolName: nil, toolInput: nil, isError: nil, image: nil)
        }
        return ToolCall(use: use, result: res)
    }

    @Test func theCardShowsTheComputerPurposeAndCommands() throws {
        let request = try #require(SecretRunRequest(input))
        #expect(request.machine == "Mac Studio")
        #expect(request.purpose == "Install the backup job")
        #expect(request.commands == commands)

        // No display name: the id the Agent gave. No commands: nothing to draw, the card falls back to its summary.
        let byID = try #require(SecretRunRequest(.object(["machine": .string("studio"), "machine_name": .string(" "),
                                                          "commands": .array([.string("true")])])))
        #expect(byID.machine == "studio")
        #expect(SecretRunRequest(.object(["machine": .string("studio"), "commands": .array([])])) == nil)
        #expect(SecretRunRequest(nil) == nil)
    }

    @Test func theCardIsAPasswordCardThatCannotBeQuickAllowed() throws {
        let raw = Data(#"""
        {"id":"ix_1","machine_id":"server","session_id":"s1","kind":"permission","tool_name":"secret_run",
         "tool_input":{"machine":"studio","commands":["true"]},
         "fields":[{"key":"password","label":"Password","secret":true}]}
        """#.utf8)
        let ix = try JSONDecoder().decode(Interaction.self, from: raw)
        #expect(ix.isSecretRun)
        #expect(ix.title == String(localized: "Run on \("studio") With Your Password")) // no display name: the id
        var named = ix
        named.toolInput = .object(["machine": .string("studio"), "machine_name": .string("Mac Studio"), "commands": .array([.string("true")])])
        #expect(named.title == String(localized: "Run on \("Mac Studio") With Your Password"))
        named.toolInput = nil
        #expect(named.title == String(localized: "Run With Your Password"))
        #expect(ApproveFirstIntent.resolution(for: ix) == nil) // Siri and the Action button never allow it
        // Without its field (a malformed card) it still needs filling in: no quick Allow sends it without a password.
        var bare = ix
        bare.fields = nil
        #expect(bare.requiresInput)
        #expect(ApproveFirstIntent.resolution(for: bare) == nil)
        var bash = ix
        bash.toolName = "Bash"
        #expect(!bash.isSecretRun)
    }

    @Test func deliveryDefaultsToStdinAndReadsTheExplicitEnvOptIn() throws {
        for delivery in [Optional<String>.none, "stdin"] {
            var raw = try #require(input.object)
            raw["delivery"] = delivery.map(JSONValue.string)
            let request = try #require(SecretRunRequest(.object(raw)))
            #expect(request.commands == commands)
            #expect(Self.delivery(in: request) == "stdin", "missing or explicit stdin is the unchanged default")
        }

        let env = try SecretRunDeliveryFixture.interaction(delivery: "env", commands: SecretRunDeliveryFixture.envCommands)
        let request = try #require(SecretRunRequest(env.toolInput))
        #expect(request.commands == SecretRunDeliveryFixture.envCommands)
        #expect(Self.delivery(in: request) == "env", "the explicit non-sudo env opt-in survives request parsing")
    }

    /// Every quick path reads `requiresInput`, including WatchInteractionView: it replaces Allow with the input-required
    /// note and leaves only Deny. The phone's Watch projection must carry the env card and the server's warning verbatim.
    @Test func envDeliveryKeepsTheFreshCardAndWatchApprovalGuards() throws {
        let ix = try SecretRunDeliveryFixture.interaction(delivery: "env", commands: SecretRunDeliveryFixture.envCommands)
        #expect(ix.isSecretRun)
        #expect(ix.requiresInput)
        #expect(ApproveFirstIntent.resolution(for: ix) == nil)

        var withoutField = ix
        withoutField.fields = nil
        #expect(withoutField.requiresInput, "a malformed env card cannot become quick-approvable")
        #expect(ApproveFirstIntent.resolution(for: withoutField) == nil)

        let model = AppModel()
        model.replaceInteractions("", [ix])
        let watch = try #require(model.watchSummary().interactions.first { $0.id == ix.id })
        #expect(watch.requiresInput, "Watch shows its existing input-required note, never Allow")
        #expect(watch.summary == ix.summary)
        #expect(watch.summary?.contains("PERSONA_SECRET") == true)
        for command in SecretRunDeliveryFixture.envCommands {
            #expect(watch.summary?.contains(command) == true, "Watch retains the command verbatim")
        }
    }

    @Test func theRowIsNamedForAPasswordRunWithItsPurpose() {
        let c = call(result: nil)
        #expect(ToolPresentation.verb(c.name) == String(localized: "Run With Password"))
        #expect(ToolPresentation.verb("mcp__\(LegacyNames.product)__secret_run") == ToolPresentation.verb(c.name))
        #expect(ToolPresentation.subject(c) == "Install the backup job")
        #expect(ToolPresentation.symbol(c.name) == Theme.Icon.credentials)
    }

    @Test func aRunWhereEveryCommandExitedZero() throws {
        let text = #"""
        {"status":"ran","machine":"Mac Studio","message":"Ran 3 commands.","results":[
         {"command":"sudo -S -k -p '' cp backup.plist /Library/LaunchDaemons/","exit_code":0,"output":"","timed_out":false,"truncated":false},
         {"command":"sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/backup.plist","exit_code":0,"output":"","timed_out":false,"truncated":false},
         {"command":"launchctl print system/backup","exit_code":0,"output":"…\tstate = running\n","timed_out":false,"truncated":true}]}
        """#
        let result = try #require(SecretRunResult.parse(text))
        #expect(result.status == .ran)
        #expect(result.machine == "Mac Studio")
        let rows = result.rows(commands: commands)
        #expect(rows.map(\.outcome) == [.exited(0), .exited(0), .exited(0)])
        #expect(rows.map(\.command) == commands)
        #expect(rows[2].output == "…\tstate = running") // the trailing newline would draw an empty last line
        #expect(rows.map(\.truncated) == [false, false, true])
    }

    @Test func commandsAfterAFailureDidNotRun() throws {
        let failed = try #require(SecretRunResult.parse(#"""
        {"status":"failed","machine":"Mac Studio","results":[
         {"command":"sudo -S -k -p '' cp backup.plist /Library/LaunchDaemons/","exit_code":0,"timed_out":false,"output":"","truncated":false},
         {"command":"sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/backup.plist","exit_code":5,"timed_out":false,
          "output":"Bootstrap failed: 5: Input/output error\n","truncated":false}]}
        """#))
        #expect(failed.status == .failed)
        let rows = failed.rows(commands: commands)
        #expect(rows.map(\.outcome) == [.exited(0), .exited(5), .notRun])
        #expect(rows[1].output == "Bootstrap failed: 5: Input/output error")
        #expect(rows[2].command == commands[2])

        let timedOut = try #require(SecretRunResult.parse(#"""
        {"status":"failed","results":[{"command":"sleep 999","exit_code":-1,"output":"","timed_out":true,"truncated":false}]}
        """#))
        #expect(timedOut.rows(commands: ["sleep 999", "true"]).map(\.outcome) == [.timedOut, .notRun])

        // Approved, but the run itself failed: no command results (the tool leaves `results` out).
        let noRun = try #require(SecretRunResult.parse(#"{"status":"failed","machine":"Mac Studio","message":"The computer went offline."}"#))
        #expect(noRun.rows(commands: commands).allSatisfy { $0.outcome == .notRun })
    }

    /// A run that failed before any command says so in the user's language, naming the computer when it can; the
    /// tool's English message for the Agent never shows. A failure with command results needs no note: the rows say it.
    @Test func aRunThatFailedBeforeAnyCommandSaysSoInTheUsersLanguage() throws {
        let noRun = try #require(SecretRunResult.parse(#"""
        {"status":"failed","machine":"Mac Studio","message":"node studio: dial tcp: connection refused"}
        """#))
        let note = try #require(noRun.note(machine: "Mac Studio"))
        #expect(note == String(localized: "Couldn't run on \("Mac Studio"); nothing was executed"))
        #expect(!note.contains("dial tcp"))
        #expect(noRun.note(machine: nil) == String(localized: "Couldn't run; nothing was executed"))
        #expect(noRun.note(machine: "") == noRun.note(machine: nil))

        let failed = try #require(SecretRunResult.parse(#"""
        {"status":"failed","machine":"Mac Studio","message":"1 command failed.","results":[
         {"command":"false","exit_code":1,"timed_out":false,"output":"","truncated":false}]}
        """#))
        #expect(failed.note(machine: "Mac Studio") == nil)
        let ran = try #require(SecretRunResult.parse(#"{"status":"ran","machine":"Mac Studio","results":[]}"#))
        #expect(ran.note(machine: "Mac Studio") == nil)
        let expired = try #require(SecretRunResult.parse(#"{"status":"expired","message":"The card expired."}"#))
        #expect(expired.note(machine: nil) == String(localized: "Not filled in before the card expired"))
    }

    @Test func nothingRanWhenDeclinedExpiredOrRefused() throws {
        var labels: Set<String> = []
        for status in ["declined", "expired", "refused"] {
            let result = try #require(SecretRunResult.parse(#"{"status":"\#(status)","machine":"Mac Studio","message":"Nothing ran."}"#))
            #expect(result.status.rawValue == status)
            #expect(result.rows(commands: commands).map(\.outcome) == [.notRun, .notRun, .notRun])
            labels.insert(result.status.label)
        }
        labels.formUnion([SecretRunResult.Status.ran.label, SecretRunResult.Status.failed.label])
        #expect(labels.count == 5) // every outcome reads differently
    }

    /// An output that contained the password comes back withheld whole: the row says so and shows none of it, even
    /// text the field says should not be there. A result from before the field reads as not withheld.
    @Test func anOutputThatHeldThePasswordIsWithheldWhole() throws {
        let result = try #require(SecretRunResult.parse(#"""
        {"status":"ran","machine":"Mac Studio","message":"Ran 2 commands.","results":[
         {"command":"sudo -S -k -p '' cat /etc/backup.conf","exit_code":0,"timed_out":false,"output":"not for the screen",
          "truncated":true,"output_withheld":true},
         {"command":"launchctl print system/backup","exit_code":0,"timed_out":false,"output":"state = running\n","truncated":false}]}
        """#))
        let rows = result.rows(commands: [])
        #expect(rows.map(\.withheld) == [true, false])
        #expect(rows[0].output.isEmpty)
        #expect(!rows[0].truncated) // nothing is shown, so nothing was cut either
        #expect(rows[1].output == "state = running")
    }

    /// An error before any card (plain text) and a status this app does not know are shown raw, as any tool's output.
    @Test func anythingElseStaysRaw() {
        #expect(SecretRunResult.parse(#"machine "nas" has no secret_run capability; update its node"#) == nil)
        #expect(SecretRunResult.parse(#"{"status":"queued"}"#) == nil)
        guard case .raw = SecretRunResult.reading(resultItem(#"machine "nas" is not reachable"#)) else {
            Issue.record("a plain-text error should read raw"); return
        }
    }

    /// A result longer than the page carries comes cut (`full_size`, the hub's cut line at the end): its start is never
    /// whole JSON, so the row reads the item's whole text instead of falling back to the raw start.
    @Test func aResultThePageCutIsReadFromItsWholeText() throws {
        let long = String(repeating: "launchctl: service output line\n", count: 300) // ~9 KB, past the page's 4 KB
        let whole = #"{"status":"failed","machine":"Mac Studio","message":"…","results":["#
            + #"{"command":"sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/backup.plist","exit_code":5,"timed_out":false,"output":"#
            + String(decoding: try JSONEncoder().encode(long), as: UTF8.self) + #","truncated":true}]}"#
        let start = String(decoding: whole.utf8.prefix(4096), as: UTF8.self) + "\n…（已截断，点开看全文，共 \(whole.utf8.count) 字节）" // i18n-ignore: the hub's cut line
        let cut = resultItem(start, fullSize: whole.utf8.count)
        #expect(SecretRunResult.parse(cut.cutText) == nil)
        guard case .whole(let id) = SecretRunResult.reading(cut) else { Issue.record("a cut result should ask for its whole text"); return }
        #expect(id == cut.id)

        let read = try #require(SecretRunResult.parse(whole))
        let rows = read.rows(commands: [commands[1], commands[2]])
        #expect(rows.map(\.outcome) == [.exited(5), .notRun])
        #expect(rows[0].truncated)
        #expect(rows[0].output.hasPrefix("launchctl: service output line"))

        guard case .parsed = SecretRunResult.reading(resultItem(whole)) else { Issue.record("an uncut result parses as it is"); return }
    }

    /// Reads the expected parsed delivery without naming a production property that does not exist on the base client.
    /// A String or an enum raw value both describe themselves as `stdin` / `env`; optionals are unwrapped first.
    private static func delivery(in request: SecretRunRequest) -> String? {
        guard let stored = Mirror(reflecting: request).children.first(where: { $0.label == "delivery" })?.value,
              let value = unwrapped(stored) else { return nil }
        return String(describing: value)
    }

    private static func unwrapped(_ value: Any) -> Any? {
        let mirror = Mirror(reflecting: value)
        guard mirror.displayStyle == .optional else { return value }
        guard let wrapped = mirror.children.first?.value else { return nil }
        return unwrapped(wrapped)
    }

    private func resultItem(_ text: String, fullSize: Int? = nil) -> Item {
        Item(id: "2#0", seq: 2, ts: Date(timeIntervalSince1970: 2), role: "user", kind: .toolResult, text: text,
             toolUseID: "t1", toolName: nil, toolInput: nil, isError: nil, fullSize: fullSize, image: nil)
    }
}

/// The real card's accessibility tree: env has an explicit warning and still offers only this card's Decline / Run;
/// stdin and a legacy card with no delivery have no env warning. Screenshots are opt-in and written only to PERSONA_SHOTS.
@MainActor @Suite(.serialized)
struct SecretRunDeliveryCardTests {
    private struct Element {
        var id: String
        var label: String
        var button: Bool
        var enabled: Bool
    }

    @Test func envCardWarnsAndKeepsEveryCommandAndOnlyDeliberateRun() async throws {
        let elements = try await render(delivery: "env", commands: SecretRunDeliveryFixture.envCommands)
        let warning = try #require(elements.first { $0.id == "secret-run.env-warning" },
                                   "env delivery must warn that every listed command receives the typed secret")
        #expect(warning.label.contains("PERSONA_SECRET"))

        let listed = try #require(elements.first { $0.id == "secret-run.commands" })
        for command in SecretRunDeliveryFixture.envCommands {
            #expect(listed.label.contains(command), "the env card dropped or rewrote its command: \(command)")
        }
        let buttons = elements.filter(\.button)
        let run = try #require(buttons.first { $0.id == "secret-run.run" })
        #expect(!run.enabled, "Run stays disabled until this run's secret is typed")
        #expect(buttons.contains { $0.label == String(localized: "Decline") })
        for quick in [String(localized: "Allow [Notification Actions ce7ef2]"),
                      String(localized: "Approve [Interaction Views 905819]"), String(localized: "Always Allow in Session")] {
            #expect(!buttons.contains { $0.label == quick }, "no quick or remembered approval for env: \(quick)")
        }
    }

    @Test func stdinAndLegacyCardsHaveNoEnvWarningAndKeepTheirCommands() async throws {
        for delivery in [Optional("stdin"), nil] {
            let elements = try await render(delivery: delivery, commands: SecretRunDeliveryFixture.stdinCommands)
            #expect(!elements.contains { $0.id == "secret-run.env-warning" }, "delivery: \(delivery ?? "missing")")
            let listed = try #require(elements.first { $0.id == "secret-run.commands" })
            for command in SecretRunDeliveryFixture.stdinCommands {
                #expect(listed.label.contains(command), "delivery \(delivery ?? "missing") changed a command")
            }
        }
    }

    private var appearance: String { ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "light" }
    private var largeText: Bool { ProcessInfo.processInfo.environment["PERSONA_SHOTS_LARGE"] == "1" }

    private func render(delivery: String?, commands: [String]) async throws -> [Element] {
        try automation(true)
        defer { try? automation(false) }
        let ix = try SecretRunDeliveryFixture.interaction(delivery: delivery, commands: commands)
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "secret-run-\(UUID().uuidString)"))
        model.forgetRestoredForTests()
        model.apiForTests = FakeHub().client
        model.replaceInteractions("", [ix])
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 390, height: 844)
        let scheme: ColorScheme = appearance == "dark" ? .dark : .light
        let size: ContentSizeCategory = largeText ? .accessibilityExtraExtraExtraLarge : .large
        window.rootViewController = UIHostingController(rootView: ScrollView {
            SecretRunForm(interaction: ix).padding()
        }.environment(model).environment(\.colorScheme, scheme).environment(\.sizeCategory, size))
        window.overrideUserInterfaceStyle = scheme == .dark ? .dark : .light
        window.backgroundColor = .systemBackground
        window.isHidden = false
        window.layoutIfNeeded()
        defer { window.isHidden = true }

        var elements = cardElements(in: window)
        for _ in 0..<30 where !elements.contains(where: { $0.id == "secret-run.commands" })
            || !elements.contains(where: { $0.id == "secret-run.run" }) {
            try await Task.sleep(for: .milliseconds(100))
            elements = cardElements(in: window)
        }
        shot(window, "secret-run-\(delivery ?? "legacy")-\(appearance)-\(largeText ? "largest" : "default")")
        return elements.map {
            Element(id: $0.id, label: $0.label, button: $0.traits.contains(.button), enabled: !$0.traits.contains(.notEnabled))
        }
    }

    private func cardElements(in window: UIWindow) -> [RuntimeCLI.Element] {
        RuntimeCLI.elements(matching: [:]).filter { RuntimeCLI.view(of: $0.object)?.window === window }
    }

    private func shot(_ window: UIWindow, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
        }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? image.pngData()?.write(to: URL(fileURLWithPath: dir + "/\(name).png"))
    }

    private func automation(_ on: Bool) throws {
        let handle = try #require(dlopen("/usr/lib/libAccessibility.dylib", RTLD_NOW))
        let set = try #require(dlsym(handle, "_AXSSetAutomationEnabled"))
        typealias Enable = @convention(c) (Int32) -> Void
        unsafeBitCast(set, to: Enable.self)(on ? 1 : 0)
    }
}
