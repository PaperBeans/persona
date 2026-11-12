import Foundation

/// A to-do's owner-facing review round, shared with briefing cards. The timestamp stays byte-for-byte
/// as the hub sent it so accepting a recommendation cannot answer a newer round.
nonisolated struct TodoAsk: Codable, Hashable, Sendable {
    var outcome: String?
    var verdict: String?
    var recommend: String?
    var askedAt: String
    /// A decision that can wait, held for the next briefing: nothing was pushed for it (#4930, #5803;
    /// docs/assistant/todos.md "Decisions that can wait"). Absent from a hub before it, and once the briefing brought it.
    var forBriefing: Bool?
    /// A standing rule the hub suggests on this decision (#4929, #5802): the owner gave this go-ahead for this
    /// directory three times. A hub from before sends none.
    var suggestion: RuleSuggestion?

    enum CodingKeys: String, CodingKey {
        case outcome, verdict, recommend, suggestion
        case askedAt = "asked_at", forBriefing = "for_briefing"
    }

    /// Held for the next briefing and not pushed.
    var isForBriefing: Bool { forBriefing == true }

    func row(for todo: TodoItem) -> BriefBody.Row {
        let waited = JSON.parseDate(askedAt).map { Int(max(0, Date.now.timeIntervalSince($0))) } ?? 0
        return BriefBody.Row(id: todo.id, title: todo.title, waitedS: waited, askedAt: askedAt,
                             verdict: verdict, outcome: outcome, recommend: recommend,
                             forBriefing: isForBriefing, suggestion: suggestion)
    }
}

nonisolated extension TodoAsk {
    /// The round reads as it always did; the two newer fields are read apart, so one of the wrong type is left out
    /// without the ask and never hides a decision that waits.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        outcome = try c.decodeIfPresent(String.self, forKey: .outcome)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        recommend = try c.decodeIfPresent(String.self, forKey: .recommend)
        askedAt = try c.decode(String.self, forKey: .askedAt)
        forBriefing = (try? c.decodeIfPresent(Bool.self, forKey: .forBriefing)) ?? nil
        suggestion = (try? c.decodeIfPresent(RuleSuggestion.self, forKey: .suggestion)) ?? nil
    }
}
