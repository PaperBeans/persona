import Foundation
import PersonaWire

/// A sentence of a to-do's notes that the search matched (DESIGN_SYSTEM 3.4 "List controls", a row found by what is
/// inside it): the row says it as its line 2, the words marked (`TodoFoundText`).
nonisolated struct TodoFoundLine: Equatable, Sendable {
    var text: String
    /// Where the words are in `text`, `[start, end)` in UTF-16, as the chat search's hits say them
    /// (`AssistantSearchAnswer.Hit.ranges`).
    var marks: [[Int]]
}

/// What the list's search found in one to-do: its title, or else a sentence of its notes (`found`).
nonisolated struct TodoSearchHit: Equatable, Sendable {
    /// The sentence of the notes that matched, when the title did not: a row found by its name keeps its own line 2.
    var found: TodoFoundLine?
}

/// The To-dos list's search (#10465; DESIGN_SYSTEM 3.4 "To-dos list"): the titles and notes of the current assistant's
/// to-dos the device holds, open and closed, matched as the system's search matches (case, accents and width aside).
/// The phone holds every to-do (`GET /api/v1/todos` returns them all), so nothing is asked of the hub. Pure; `find`
/// runs off the main actor.
nonisolated enum TodoSearch {
    /// The words as searched: "" is no search.
    static func words(_ query: String) -> String {
        query.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Every to-do of `items` that `words` match, by id.
    static func hits(_ items: [String: TodoItem], words: String) -> [String: TodoSearchHit] {
        guard !words.isEmpty else { return [:] }
        var out: [String: TodoSearchHit] = [:]
        for (id, item) in items {
            if let hit = hit(item, words: words) { out[id] = hit }
        }
        return out
    }

    /// `hits`, off the main actor: what the list's search runs as the words change (P4). A newer look cancels this one,
    /// which then stops early with nothing.
    @concurrent static func find(_ items: [String: TodoItem], words: String) async -> [String: TodoSearchHit] {
        guard !words.isEmpty else { return [:] }
        var out: [String: TodoSearchHit] = [:]
        for (n, (id, item)) in items.enumerated() {
            if n % 128 == 0, Task.isCancelled { return [:] }
            if let hit = hit(item, words: words) { out[id] = hit }
        }
        return out
    }

    /// Whether `words` are in the to-do's title (its line 2 kept), else which sentence of its notes holds them.
    static func hit(_ item: TodoItem, words: String) -> TodoSearchHit? {
        if item.title.localizedStandardContains(words) { return TodoSearchHit() }
        guard let notes = item.notes, let found = sentence(in: notes, matching: words) else { return nil }
        return TodoSearchHit(found: found)
    }

    /// The sentence of `notes` that holds the first match, on one line, with every match in it marked; nil when
    /// nothing matches.
    static func sentence(in notes: String, matching words: String) -> TodoFoundLine? {
        guard !words.isEmpty, let first = notes.localizedStandardRange(of: words) else { return nil }
        // The sentence around the match (one that runs over a sentence's end keeps the next one too).
        var lower = notes.startIndex, upper = notes.endIndex
        notes.enumerateSubstrings(in: notes.startIndex..<notes.endIndex, options: [.bySentences, .substringNotRequired]) { _, range, _, stop in
            if range.upperBound <= first.lowerBound { return }
            if range.lowerBound <= first.lowerBound { lower = range.lowerBound }
            if range.upperBound >= first.upperBound {
                upper = range.upperBound
                stop = true
            }
        }
        let text = notes[lower..<upper].split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }.joined(separator: " ")
        var marks: [[Int]] = []
        var from = text.startIndex
        while from < text.endIndex, let r = text[from...].localizedStandardRange(of: words) {
            marks.append([text.utf16.distance(from: text.utf16.startIndex, to: r.lowerBound),
                          text.utf16.distance(from: text.utf16.startIndex, to: r.upperBound)])
            from = r.upperBound > r.lowerBound ? r.upperBound : text.index(after: r.lowerBound)
        }
        return marks.isEmpty ? nil : TodoFoundLine(text: text, marks: marks)
    }
}
