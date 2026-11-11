import Foundation

/// What a running work line says the agent is doing (#2987, the web's running bar): the newest call still running, in
/// its running form with the subject its step row shows ("Running go test ./server/...", "Editing ChatView.swift"; a
/// command Claude described: its description alone, "Run the widget snapshot tests"), "+N" for the calls running beside
/// it; with none running, the latest step (a thought: its first line). Nil when no step is known (only notices or words
/// so far, or work the hub folded away): the line says what it said before. A finished line never asks.
nonisolated struct WorkNow: Equatable, Sendable {
    /// The step it names.
    var id: String
    /// Its words: "Running go test ./server/...", "Reading 3 files", a thought's first line.
    var text: String
    /// Calls running beside it.
    var more = 0
    /// Where the words give way on a narrow line: a sentence (a thought, Claude's description of a command) loses its
    /// end, a path or a command its middle, as in the step's own row.
    var tail = false

    /// What follows the words and is never cut: " +2 · 1 min 4 s". `alone`: on a line of its own under them (the
    /// accessibility text sizes), without what joined it to them: "+2 · 1 min 4 s", "1 min 4 s".
    func trailer(took: String, alone: Bool = false) -> String {
        let count = more > 0 ? String(localized: "+\(more)") : ""
        let lead = alone || count.isEmpty ? count : " " + count
        guard !took.isEmpty else { return lead }
        return alone && lead.isEmpty ? took : lead + " · " + took
    }

    /// The least time between two steps the running line names (`WorkLine.follow`).
    static let repaint: TimeInterval = 0.5

    /// What the line names: nothing while no step is known (it says its counts, or its subagents' words); else the step
    /// last let through, or the known one when none has been since the line last named nothing, so a step coming back
    /// (subagents done, the fold passed) never shows the counts first.
    static func named(doing: WorkNow?, shown: WorkNow?) -> WorkNow? {
        guard let doing else { return nil }
        return shown ?? doing
    }

    /// How long `next` waits before the line names it: until `repaint` has passed since `shown` was let through at
    /// `shownAt`. Only a change between two steps waits; to or from no step is at once (`named` already shows it).
    static func hold(_ next: WorkNow?, shown: WorkNow?, shownAt: Date, now: Date) -> TimeInterval {
        guard next != nil, shown != nil else { return 0 }
        return max(0, repaint - now.timeIntervalSince(shownAt))
    }

    /// Tools that keep the books rather than do the work: never what the line names.
    private static let quiet: Set<String> = ["TodoWrite", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "ToolSearch"]

    /// Which step the line names, and the calls running now (newest first, the named one among them). An agent goes on
    /// only once its calls are answered, so only the newest batch of calls can still be running: back to a thought, its
    /// words, or a call answered before the next one began (calls made together all begin before any is answered). An
    /// older call without a result is one whose result never came.
    static func pick(_ items: [WorkItem]) -> (step: ToolStep, running: [ToolCall])? {
        var running: [ToolCall] = []
        var latest: ToolStep?
        var current = true
        var next: ToolCall? // the call after this one
        for item in items.reversed() {
            switch item {
            case .step(.call(let c)):
                guard !quiet.contains(ToolPresentation.baseName(c.name)) else { continue }
                if let next, let r = c.result, r.seq < next.use.seq { current = false }
                if current && c.isRunning { running.append(c) }
                if latest == nil { latest = .call(c) }
                next = c
            case .step(.thought(let t)) where t.kind == .notice:
                continue // a background task's report or a compaction: not a step the agent took
            case .step(.thought(let t)):
                if latest == nil { latest = .thought(t) }
                current = false
            case .step(.folded):
                guard latest != nil else { return nil } // the newest work is folded away: its last step is not known here
                current = false
            case .text:
                current = false
            }
            if !current && latest != nil { break }
        }
        if let newest = running.first { return (.call(newest), running) }
        guard let latest else { return nil }
        return (latest, [])
    }

    /// The line's words for these items, nil when no step is known.
    @MainActor static func of(_ items: [WorkItem]) -> WorkNow? {
        guard let picked = pick(items) else { return nil }
        let running = picked.running
        switch picked.step {
        case .call(let c):
            if running.count > 1, running.allSatisfy({ ToolPresentation.baseName($0.name) == "Read" }) {
                return WorkNow(id: c.id, text: String(localized: "Reading \(running.count) files [Work Line]"))
            }
            let (text, tail) = words(c)
            return WorkNow(id: c.id, text: text, more: max(0, running.count - 1), tail: tail)
        case .thought(let t):
            // As its row shows it (Codex's summaries open with a bold title: without the stars).
            var line = clip(ToolPresentation.firstLine(t.text ?? "").trimmingCharacters(in: .whitespaces))
            if line.count > 4, line.hasPrefix("**"), line.hasSuffix("**") { line = String(line.dropFirst(2).dropLast(2)) }
            return WorkNow(id: t.id, text: line.isEmpty ? String(localized: "Thinking [Work Line]") : line, tail: true)
        case .folded:
            return nil
        }
    }

    /// A call in its running form, and whether its words give way at the end.
    @MainActor private static func words(_ c: ToolCall) -> (String, Bool) {
        // `machinesNow`: read while the chat draws its lines, `machines` would redraw them with every machine report.
        let subject = clip(ToolPresentation.subject(c, machines: AppModel.shared.machinesNow))
        let tail = ToolPresentation.truncation(c.name) == .tail
        guard !subject.isEmpty else { return (ToolPresentation.verb(c.name), tail) }
        switch ToolPresentation.baseName(c.name) {
        case "Bash":
            // Claude's own words for the command are already an imperative ("Run the widget snapshot tests") and the
            // spinner says it runs: they stand alone, a sentence. The command itself reads as code after "Running".
            if c.input["description"]?.string?.isEmpty == false { return (subject, true) }
            return (String(localized: "Running \(subject) [Work Line]"), tail)
        case "Read": return (String(localized: "Reading \(subject) [Work Line]"), tail)
        case "Edit", "MultiEdit", "NotebookEdit": return (String(localized: "Editing \(subject) [Work Line]"), tail)
        case "Write": return (String(localized: "Writing \(subject) [Work Line]"), tail)
        case "Grep": return (String(localized: "Searching for \(subject) [Work Line]"), tail)
        case "Glob", "LS": return (String(localized: "Finding \(subject) [Work Line]"), tail)
        case "WebFetch": return (String(localized: "Opening \(subject) [Work Line]"), tail)
        case "WebSearch": return (String(localized: "Searching the web for \(subject) [Work Line]"), tail)
        // Every other tool as its row names it: "Use Skill review".
        default: return (String(localized: "\(ToolPresentation.verb(c.name)) \(subject)"), tail)
        }
    }

    /// The longest subject kept: a one-line script can run to kilobytes, and the line shows a few dozen characters.
    private static let longest = 240

    private static func clip(_ s: String) -> String {
        let head = s.prefix(longest)
        return head.endIndex < s.endIndex ? String(head) + "…" : s
    }
}
