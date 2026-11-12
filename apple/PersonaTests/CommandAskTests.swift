import Foundation
import Testing
@testable import Persona

/// Claude Code's permission prompt for a shell command read off its terminal (#5266): what the screen card says of it in
/// plain words, from the hub's dialog.
struct CommandAskTests {
    /// The dialog as the hub sends it for Claude Code 2.1.322's safety check (server/internal/screen, #5265).
    private static let hubJSON = """
    {"question":"Do you want to proceed?","rev":"44597c89094b",
     "detail":"Bash command\\nCount the example names in first-screen chunks\\ncd ./dist && ls",
     "warning":"Dangerous rm operation not ruled out — the shell may read this command differently than it was parsed",
     "deadline":"Claude Code will automatically block this request in 1:44, to avoid holding up an unattended session",
     "command":"cd ./dist && ls","description":"Count the example names in first-screen chunks","risk":"delete",
     "deadline_action":"deny","deadline_at":"2026-11-12T03:00:00Z",
     "options":[{"key":"1","label":"Yes","keys":["1"]},{"key":"2","label":"No","keys":["2"]}]}
    """

    private func dialog(_ json: String) throws -> ScreenParser.Dialog {
        try JSON.decoder.decode(ScreenDialog.self, from: Data(json.utf8)).parsed
    }

    @Test func readsTheHubsCommandPrompt() throws {
        let d = try dialog(Self.hubJSON)
        let ask = try #require(d.commandAsk)
        #expect(ask.command == "cd ./dist && ls")
        #expect(ask.description == "Count the example names in first-screen chunks")
        #expect(ask.risk == "delete" && !ask.deadlineAllows && !ask.folds(at: CommandAsk.foldedLines))
        #expect(ask.deadlineAt == ISO8601DateFormatter().date(from: "2026-11-12T03:00:00Z"))
        #expect(ask.riskLine == String(localized: "Can't confirm this command won't delete files"))
        // Claude Code's own words stay one tap away.
        #expect(ask.original?.hasPrefix("Dangerous rm operation not ruled out") == true)
        #expect(ask.original?.contains("automatically block this request in 1:44") == true)
        let pair = d.allowDeny
        #expect(pair?.allow.key == "1" && pair?.deny.key == "2")
    }

    @Test func anOlderHubsDialogIsAQuestionAsBefore() throws {
        let d = try dialog("""
        {"question":"Do you want to proceed?","rev":"r1","detail":"Bash command\\nrm -rf build",
         "options":[{"key":"1","label":"Yes","keys":["1"]},{"key":"2","label":"No","keys":["2"]}]}
        """)
        #expect(d.commandAsk == nil && d.allowDeny == nil)
        #expect(d.detail == "Bash command\nrm -rf build")
    }

    /// More choices than Yes and No ("Yes, and don't ask again for …"): they keep their own labels.
    @Test func moreChoicesKeepTheirLabels() throws {
        let three = Self.hubJSON.replacingOccurrences(
            of: #"{"key":"2","label":"No","keys":["2"]}"#,
            with: #"{"key":"2","label":"Yes, and don't ask again for ls commands","keys":["2"]},{"key":"3","label":"No","keys":["3"]}"#)
        let d = try dialog(three)
        #expect(d.commandAsk != nil && d.options.count == 3 && d.allowDeny == nil)
    }

    @Test func theWarningInPlainWords() {
        let other = CommandAsk(command: "ls", warning: "Command contains something unusual")
        #expect(other?.riskLine == String(localized: "Claude Code warns about this command"))
        #expect(CommandAsk(command: "ls")?.riskLine == nil)
        #expect(CommandAsk(command: "  ") == nil)
    }

    @Test func theCountdownsLine() throws {
        let now = Date(timeIntervalSince1970: 1_000_000)
        let deny = try #require(CommandAsk(command: "ls", deadlineAction: "deny", deadlineAt: now.addingTimeInterval(104)))
        #expect(deny.countdownLine(now: now) == String(localized: "Denied automatically in \("1:44")"))
        #expect(deny.countdownLine(now: now.addingTimeInterval(103.5)) == String(localized: "Denied automatically in \("0:01")"))
        #expect(deny.countdownLine(now: now.addingTimeInterval(104)) == deny.expiredLine)
        #expect(deny.expiredLine == String(localized: "Time's up: Claude Code denies it"))
        let allow = try #require(CommandAsk(command: "ls", deadlineAction: "allow", deadlineAt: now.addingTimeInterval(61)))
        #expect(allow.countdownLine(now: now) == String(localized: "Allowed automatically in \("1:01")"))
        #expect(CommandAsk(command: "ls")?.countdownLine(now: now) == nil)
    }

    @Test func aLongCommandFolds() {
        let folded = CommandAsk.foldedLines
        #expect(CommandAsk(command: "git status")?.folds(at: folded) == false)
        #expect(CommandAsk(command: String(repeating: "echo one two three; ", count: 12))?.folds(at: folded) == true)
        #expect(CommandAsk(command: (1...6).map { "step \($0)" }.joined(separator: "\n"))?.folds(at: folded) == true)
        // Beautiful UI's card folds at two rows: three short rows fold there and not in the default style's card.
        let three = CommandAsk(command: "cd ./dist\nls\nwc -l *.js")
        #expect(three?.folds(at: CommandAsk.compactFoldedLines) == true)
        #expect(three?.folds(at: folded) == false)
    }

    /// The needs-you row and the folded card say what it is for, not "Do you want to proceed?".
    @Test func askLineIsTheAgentsWords() throws {
        let d = try JSON.decoder.decode(ScreenDialog.self, from: Data(Self.hubJSON.utf8))
        let ix = Interaction(id: "scr_1", machineID: "m1", sessionID: "s1", sessionTitle: nil, cwd: nil, kind: .screen,
                             createdAt: nil, expiresAt: nil, toolName: nil, toolUseID: nil, questions: nil, plan: nil,
                             toolInput: nil, summary: nil, screen: nil, dialog: d)
        #expect(ix.askLine(includeToolName: false) == "Count the example names in first-screen chunks")
    }
}
