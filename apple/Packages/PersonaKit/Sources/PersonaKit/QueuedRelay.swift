import Foundation

/// The sender and body of a conversation relay as the hub writes it in the queue (queuedRelays).
public nonisolated struct QueuedRelay: Equatable, Sendable {
    public let source: String
    public let text: String
    public let noWake: Bool

    private static let prefix = try! NSRegularExpression(pattern: #"^来自「([^\n]*?)」(（不唤醒）)?："#) // i18n-ignore: hub queue label

    public init?(_ raw: String) {
        let rawText = raw as NSString
        guard let match = Self.prefix.firstMatch(in: raw, range: NSRange(location: 0, length: rawText.length)) else { return nil }
        let title = rawText.substring(with: match.range(at: 1)).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty else { return nil }
        source = title
        text = rawText.substring(from: NSMaxRange(match.range))
        noWake = match.range(at: 2).location != NSNotFound
    }
}
