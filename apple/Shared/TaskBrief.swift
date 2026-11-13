import Foundation

/// A task's brief (docs/assistant/unified-entry.md "Briefs"): one record per piece of work, made when it is handed out.
/// The hub sends it on a plan's task before it starts (`tasks[i].brief`). Decode-only: the apps never send it back, and
/// only the parts the plan card draws are read (the web's `lib/brief.ts` reads the rest for the task's page).
nonisolated struct TaskBrief: Decodable, Hashable, Sendable {
    /// The owner's message that led to the task, as they wrote it (`truncated`: the hub kept its first 2000 characters).
    struct Request: Decodable, Hashable, Sendable {
        var text: String
        var truncated: Bool?
        var at: Date?
    }

    var request: Request?
    /// owner / assistant / leader / routine / hub.
    var startedBy: String?
    var reason: String?
    var triggerTitle: String?
    var createdAt: Date?
    var goal: String?
    var doneWhen: [String]?
    var expectMinutes: Int?
    var outOfBounds: [String]?
    /// Made later for a task from before briefs: `handover` is the prompt its agent got, not the owner's words.
    var reconstructed: Bool?
    var handover: String?
    /// `done_when` and `expect_minutes` as the scope changes left them.
    var criteria: [String]?
    var expectNow: Int?

    enum CodingKeys: String, CodingKey {
        case request, reason, goal, criteria, reconstructed, handover
        case startedBy = "started_by", triggerTitle = "trigger_title", createdAt = "created_at", doneWhen = "done_when",
             expectMinutes = "expect_minutes", outOfBounds = "out_of_bounds", expectNow = "expect_minutes_now"
    }

    /// Who asked, as the brief's first line says it (the web's `briefAsked`).
    enum Asked: Equatable, Sendable {
        case owner(text: String, truncated: Bool, at: Date?)
        case started(by: String, why: String, at: Date?)
        case reconstructed
        case unknown(at: Date?)
    }

    var asked: Asked {
        if let text = request?.text.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty {
            return .owner(text: text, truncated: request?.truncated == true, at: request?.at)
        }
        if reconstructed == true { return .reconstructed }
        if let by = startedBy, ["assistant", "leader", "routine", "hub"].contains(by) {
            let why = (reason ?? "").isEmpty ? (triggerTitle ?? "") : (reason ?? "")
            return .started(by: by, why: why.trimmingCharacters(in: .whitespacesAndNewlines), at: createdAt)
        }
        return .unknown(at: createdAt)
    }

    /// The criteria in effect now (a hub that sends none: as the brief was made).
    var criteriaNow: [String] { criteria ?? doneWhen ?? [] }
    /// The expected minutes in effect now; 0 when none was given.
    var expect: Int { (expectNow ?? 0) > 0 ? expectNow ?? 0 : expectMinutes ?? 0 }
    var bounds: [String] { outOfBounds ?? [] }
    var goalText: String { (goal ?? "").trimmingCharacters(in: .whitespacesAndNewlines) }

    /// "About 30 min", "About 2.5 h".
    static func hoursText(_ minutes: Int) -> String {
        (Double((minutes * 10 + 30) / 60) / 10).formatted(.number.precision(.fractionLength(0...1)))
    }

    /// "2 criteria · about 45 min · 1 out of bounds": what a plan card's folded brief holds, in one line.
    var summary: String {
        var parts: [String] = []
        let n = criteriaNow.count
        if n == 1 { parts.append(String(localized: "1 criterion")) } else if n > 1 { parts.append(String(localized: "\(n) criteria")) }
        let m = expect
        if m > 0 && m < 120 { parts.append(String(localized: "about \(m) min")) }
        else if m >= 120 { parts.append(String(localized: "about \(TaskBrief.hoursText(m)) h")) }
        if !bounds.isEmpty { parts.append(String(localized: "\(bounds.count) out of bounds")) }
        return parts.joined(separator: " · ")
    }

    /// Whether the plan card has more to open than what its folded form shows (the goal says more than the title, or a
    /// summary, or the owner's words are cut).
    func hasMore(title: String) -> Bool {
        !summary.isEmpty || (!goalText.isEmpty && goalText != title.trimmingCharacters(in: .whitespacesAndNewlines))
    }
}
