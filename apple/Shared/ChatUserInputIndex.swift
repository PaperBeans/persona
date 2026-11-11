import Foundation

/// Full-history count/position metadata, independent of the loaded transcript. It contains no message bodies.
nonisolated struct ChatUserInputIndex: Decodable, Equatable, Sendable {
    nonisolated struct Position: Decodable, Equatable, Sendable {
        var id: String
        var seq: Int
    }

    var total: Int
    var inputs: [Position]
    var history: String

    var isValid: Bool {
        guard total == inputs.count else { return false }
        var ids = Set<String>()
        var previous = 0
        for input in inputs {
            guard !input.id.isEmpty, input.seq > 0, input.seq >= previous, ids.insert(input.id).inserted else { return false }
            previous = input.seq
        }
        return true
    }
}
