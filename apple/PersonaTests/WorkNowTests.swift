import Foundation
import Testing
@testable import Persona

/// What a running work line says the agent is doing (#2987, `WorkNow`): which step it names, "+N" for calls running
/// beside it, its words, and when it names nothing (the line keeps its counts).
@MainActor
struct WorkNowTests {
    private func item(_ id: String, _ kind: ItemKind, at seq: Int, text: String? = nil, tool: String? = nil,
                      input: [String: String] = [:]) -> Item {
        Item(id: id, seq: seq, ts: nil, role: "assistant", kind: kind, text: text, toolUseID: id, toolName: tool,
             toolInput: tool == nil ? nil : .object(input.mapValues { .string($0) }), isError: nil, image: nil)
    }

    /// A call begun at `seq` and answered at `answered`, right after it by default; `running`: not answered yet.
    private func call(_ id: String, _ tool: String, _ input: [String: String] = [:], at seq: Int, answered: Int? = nil,
                      running: Bool = false) -> WorkItem {
        let use = item(id, .toolUse, at: seq, tool: tool, input: input)
        let result = running ? nil : Item(id: id + "-r", seq: answered ?? seq + 1, ts: nil, role: "user", kind: .toolResult,
                                          text: "ok", toolUseID: id, toolName: nil, toolInput: nil, isError: nil, image: nil)
        return .step(.call(ToolCall(use: use, result: result)))
    }

    private func thought(_ id: String, _ text: String, at seq: Int) -> WorkItem { .step(.thought(item(id, .thinking, at: seq, text: text))) }

    @Test func theNewestRunningCallInItsRunningForm() throws {
        let now = try #require(WorkNow.of([call("a", "Read", ["file_path": "/repo/README.md"], at: 1),
                                           call("b", "Bash", ["command": "go test ./server/...\necho done"], at: 3, running: true)]))
        #expect(now.id == "b")
        #expect(now.text == String(localized: "Running \("go test ./server/...") [Work Line]"), "the command's first line")
        #expect(now.more == 0)
        #expect(!now.tail, "a command gives way in the middle, as its row")
    }

    /// Claude's description is already an imperative and the spinner says it runs: no "Running" before it.
    @Test func claudesDescriptionOfACommandStandsAlone() throws {
        let now = try #require(WorkNow.of([call("a", "Bash", ["command": "go test ./server/...", "description": "Run hub tests"],
                                                at: 1, running: true)]))
        #expect(now.text == "Run hub tests")
        #expect(now.tail, "a sentence gives way at its end")
        // An empty description is no description: the command, after "Running".
        let bare = try #require(WorkNow.of([call("b", "Bash", ["command": "go test ./server/...", "description": ""], at: 1, running: true)]))
        #expect(bare.text == String(localized: "Running \("go test ./server/...") [Work Line]"))
        #expect(!bare.tail)
    }

    /// Calls made together all begin before any is answered: those still running are counted beside the newest.
    @Test func callsRunningAtOnceAreCountedBesideTheNewest() throws {
        let items = [thought("t", "Checking both", at: 1), call("a", "Grep", ["pattern": "WorkSummary"], at: 2, answered: 5),
                     call("b", "Edit", ["file_path": "/repo/ChatView.swift"], at: 3, running: true),
                     call("c", "Bash", ["command": "swift build"], at: 4, running: true)]
        let now = try #require(WorkNow.of(items))
        #expect(now.id == "c")
        #expect(now.more == 1)
        #expect(now.trailer(took: "") == " " + String(localized: "+\(1)"))
        let picked = try #require(WorkNow.pick(items))
        #expect(picked.running.map(\.id) == ["c", "b"], "newest first, the answered one left out")
        // The newest answered first: the one still running is named.
        let first = try #require(WorkNow.of([call("a", "Bash", ["command": "swift build"], at: 1, running: true),
                                             call("b", "Read", ["file_path": "/b.swift"], at: 2, answered: 3)]))
        #expect(first.id == "a")
    }

    @Test func filesReadAtOnceAreOneStep() throws {
        let now = try #require(WorkNow.of([call("a", "Read", ["file_path": "/a.swift"], at: 1, running: true),
                                           call("b", "Read", ["file_path": "/b.swift"], at: 2, running: true),
                                           call("c", "Read", ["file_path": "/c.swift"], at: 3, running: true)]))
        #expect(now.text == String(localized: "Reading \(3) files [Work Line]"))
        #expect(now.more == 0)
    }

    @Test func withNoCallRunningTheLatestStep() throws {
        let edit = try #require(WorkNow.of([call("a", "Bash", ["command": "ls"], at: 1),
                                            call("b", "Edit", ["file_path": "/repo/ChatView.swift"], at: 3)]))
        #expect(edit.id == "b")
        #expect(edit.text == String(localized: "Editing \("ChatView.swift") [Work Line]"), "the file's name, as its row")
        let thinking = try #require(WorkNow.of([call("a", "Bash", ["command": "ls"], at: 1),
                                                thought("t", "\nChecking whether the hub cuts the tail\nmore", at: 3)]))
        #expect(thinking.id == "t")
        #expect(thinking.text == "Checking whether the hub cuts the tail")
        #expect(thinking.tail)
        #expect(WorkNow.of([thought("t", "**Inspecting the fold**\n\nbody", at: 1)])?.text == "Inspecting the fold")
        #expect(WorkNow.of([thought("t", "  ", at: 1)])?.text == String(localized: "Thinking [Work Line]"))
        // The agent's words after its calls (a reply being written) still name the latest call.
        let words = try #require(WorkNow.of([call("a", "Edit", ["file_path": "/repo/ChatView.swift"], at: 1),
                                             .text(item("w", .text, at: 3, text: "Done editing"))]))
        #expect(words.id == "a")
        #expect(words.more == 0)
    }

    /// An agent goes on only once its calls are answered: an older call that never got a result is not running.
    @Test func aCallWhoseResultNeverCameIsNotRunning() throws {
        let afterThought = try #require(WorkNow.of([call("a", "Bash", ["command": "sleep 1"], at: 1, running: true),
                                                    thought("t", "Next", at: 2),
                                                    call("b", "Read", ["file_path": "/x.swift"], at: 3, running: true)]))
        #expect(afterThought.id == "b")
        #expect(afterThought.more == 0)
        #expect(WorkNow.of([call("a", "Bash", ["command": "sleep 1"], at: 1, running: true), thought("t", "Next", at: 2)])?.id == "t")
        // Calls one after another, no thought between: the one answered before the next began ends the batch.
        let afterCalls = try #require(WorkNow.pick([call("a", "Bash", ["command": "sleep 1"], at: 1, running: true),
                                                    call("b", "Read", ["file_path": "/x.swift"], at: 2),
                                                    call("c", "Edit", ["file_path": "/x.swift"], at: 4, running: true)]))
        #expect(afterCalls.running.map(\.id) == ["c"])
        // The agent's words end a batch too: a call before them without a result is not counted beside the newest.
        let afterWords = try #require(WorkNow.of([call("a", "Bash", ["command": "sleep 1"], at: 1, running: true),
                                                  .text(item("w", .text, at: 2, text: "Now the edit")),
                                                  call("b", "Edit", ["file_path": "/x.swift"], at: 3, running: true)]))
        #expect(afterWords.id == "b")
        #expect(afterWords.more == 0)
    }

    @Test func noticesAndBookkeepingAreNotSteps() throws {
        let notice = Item(id: "n", seq: 3, ts: nil, role: "system", kind: .notice, text: "Background command finished",
                          toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil, wake: true)
        let now = try #require(WorkNow.of([call("a", "WebSearch", ["query": "swift diff"], at: 1), .step(.thought(notice)),
                                           call("b", "TodoWrite", at: 4), call("c", "ToolSearch", ["query": "select:Read"], at: 6, running: true)]))
        #expect(now.id == "a")
        #expect(now.text == String(localized: "Searching the web for \("swift diff") [Work Line]"))
    }

    @Test func otherToolsAsTheirRowNamesThem() throws {
        let now = try #require(WorkNow.of([call("a", "Skill", ["skill": "review"], at: 1, running: true)]))
        #expect(now.text == String(localized: "\(ToolPresentation.verb("Skill")) \("review")"))
        #expect(WorkNow.of([call("b", "Bash", at: 1, running: true)])?.text == ToolPresentation.verb("Bash"), "no subject: the verb alone")
    }

    @Test func aSubjectLongerThanTheLineIsClipped() throws {
        let now = try #require(WorkNow.of([call("a", "Bash", ["command": String(repeating: "x", count: 5000)], at: 1, running: true)]))
        #expect(now.text.count < 300)
        #expect(now.text.contains("…"))
    }

    /// Nothing known: the line keeps what it said before (its counts).
    @Test func noStepKnownNamesNothing() {
        let fold = Item(id: "fold:1", seq: 2, ts: nil, role: "assistant", kind: .fold, text: nil, toolUseID: nil, toolName: nil,
                        toolInput: nil, isError: nil, image: nil,
                        fold: Fold(items: 12, tools: 6, thoughts: 1, until: nil, throughSeq: 20, throughID: "20#0"))
        #expect(WorkNow.of([]) == nil)
        #expect(WorkNow.of([.text(item("w", .text, at: 1, text: "Looking"))]) == nil)
        #expect(WorkNow.of([call("a", "Read", ["file_path": "/a.swift"], at: 0), .step(.folded(fold))]) == nil, "the newest work is folded away")
        #expect(WorkNow.of([.step(.folded(fold)), call("b", "Read", ["file_path": "/b.swift"], at: 21, running: true)])?.id == "b")
    }

    @Test func theTrailerKeepsTheCountAndTheTime() {
        #expect(WorkNow(id: "a", text: "x").trailer(took: "") == "")
        #expect(WorkNow(id: "a", text: "x").trailer(took: "5 s") == " · 5 s")
        #expect(WorkNow(id: "a", text: "x", more: 2).trailer(took: "5 s") == " " + String(localized: "+\(2)") + " · 5 s")
        #expect(WorkNow(id: "a", text: "x", more: 2).trailer(took: "") == " " + String(localized: "+\(2)"))
        // On a line of its own under the words (the accessibility text sizes): nothing to join it to them.
        #expect(WorkNow(id: "a", text: "x").trailer(took: "", alone: true) == "")
        #expect(WorkNow(id: "a", text: "x").trailer(took: "5 s", alone: true) == "5 s")
        #expect(WorkNow(id: "a", text: "x", more: 2).trailer(took: "5 s", alone: true) == String(localized: "+\(2)") + " · 5 s")
        #expect(WorkNow(id: "a", text: "x", more: 2).trailer(took: "", alone: true) == String(localized: "+\(2)"))
    }

    /// The line follows its step at most about twice a second, and a step coming back after none (subagents done, the
    /// fold passed) is named at once, never the counts first (`WorkLine.follow`).
    @Test func theLineFollowsItsStepAtMostTwiceASecond() {
        let x = WorkNow(id: "x", text: "Reading a.swift"), y = WorkNow(id: "y", text: "Editing a.swift")
        let t = Date(timeIntervalSinceReferenceDate: 1000)
        // Between two steps the one shown stays until half a second has passed.
        #expect(WorkNow.named(doing: y, shown: x) == x)
        #expect(WorkNow.hold(y, shown: x, shownAt: t, now: t.addingTimeInterval(0.25)) == WorkNow.repaint - 0.25)
        #expect(WorkNow.hold(y, shown: x, shownAt: t, now: t.addingTimeInterval(2)) == 0)
        // No step known now: the counts (or the subagents' words) at once, whatever was shown.
        #expect(WorkNow.named(doing: nil, shown: x) == nil)
        #expect(WorkNow.hold(nil, shown: x, shownAt: t, now: t) == 0)
        // A step coming back after none: named at once.
        #expect(WorkNow.named(doing: y, shown: nil) == y)
        #expect(WorkNow.hold(y, shown: nil, shownAt: t, now: t) == 0)
    }

    /// The running line redraws when its step changes; a finished one keeps today's words and ignores steps.
    @Test func theLineIsEqualOnItsStep() {
        let a = [call("a", "Bash", ["command": "swift build"], at: 1, running: true)]
        let b = [call("b", "Bash", ["command": "swift build"], at: 1, running: true)]
        func line(_ items: [WorkItem], live: Bool, agents: AgentsWorking? = nil) -> WorkLine {
            WorkLine(items: items, live: live, start: nil, end: nil, open: false, agents: agents, openTerminal: nil) {}
        }
        #expect(line(a, live: true) != line(b, live: true))
        #expect(line(a, live: true) == line(a, live: true))
        #expect(line(a, live: false) == line(b, live: false))
        let agents = AgentsWorking(count: 1, doing: [])
        #expect(line(a, live: true, agents: agents) == line(b, live: true, agents: agents), "the subagents' words win")
    }
}
