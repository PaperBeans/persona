import Foundation

/// Wire readers accept both generations. Writers without a hello keep the pre-rename contract;
/// capability selection belongs to each hub link, never to a process-wide or persisted switch.
nonisolated enum HubNaming {
    static let feature = "persona_contracts_v1"
    static var productPattern: String { "(?:persona|" + NSRegularExpression.escapedPattern(for: LegacyNames.product) + ")" }
    static var contextPattern: String { productPattern + "-context" }
    static var filePattern: String { productPattern + "-file" }
    private static let agyMCPTool = try! NSRegularExpression(
        pattern: "^mcp_" + productPattern + "(?:_[0-9a-fA-F]{8,16})?_([a-z][a-z0-9_]*)$"
    )
    private static let agyToolResultHeader = try! NSRegularExpression(
        pattern: #"^(?:Created At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\r?\nCompleted At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})(?:\r?\n|$))+"#
    )

    /// Normalizes Antigravity's single-underscore Persona MCP names (`mcp_persona_<tool>`, `mcp_persona_<hex>_<tool>`)
    /// to `mcp__persona__<tool>`.
    static func canonicalMCPTool(_ name: String) -> String {
        guard name.hasPrefix("mcp_"), !name.hasPrefix("mcp__") else { return name }
        let range = NSRange(name.startIndex..., in: name)
        guard let match = agyMCPTool.firstMatch(in: name, range: range),
              let toolRange = Range(match.range(at: 1), in: name) else { return name }
        return "mcp__persona__" + name[toolRange]
    }

    /// Strips leading Antigravity `Created At:` / `Completed At:` RFC3339 wrapper header pairs (and an immediately
    /// following `Output:` line when preceded by those headers) so persisted JSON tool results still parse cleanly.
    static func stripToolResultTimestampHeaders(_ text: String) -> String {
        guard text.hasPrefix("Created At: ") else { return text }
        let range = NSRange(text.startIndex..., in: text)
        guard let match = agyToolResultHeader.firstMatch(in: text, range: range),
              let headerRange = Range(match.range, in: text) else { return text }
        let rest = text[headerRange.upperBound...]
        if rest.hasPrefix("Output:\r\n") { return String(rest.dropFirst("Output:\r\n".count)) }
        if rest.hasPrefix("Output:\n") { return String(rest.dropFirst("Output:\n".count)) }
        return String(rest)
    }

    static func accepts(scheme: String?) -> Bool {
        guard let scheme = scheme?.lowercased() else { return false }
        return scheme == "persona" || scheme == LegacyNames.urlScheme
    }

    #if !WIDGET_EXTENSION
    static func operationHeaders(_ id: UUID) -> [String: String] {
        let value = id.uuidString.lowercased()
        return [OpRetry.header: value, LegacyNames.operationIDHeader: value]
    }
    #endif

    /// Rewrite protocol tag names only, not product mentions, paths, URLs or JSON values in the user's text.
    /// Sanitized context bodies cannot close either generation's block (ContextText).
    static func outbound(_ text: String, canonical: Bool) -> String {
        guard !canonical else { return text }
        return text.replacingOccurrences(of: #"(<\s*/?\s*)persona-(context|file)(?=[\s/>])"#,
                                         with: "$1" + LegacyNames.product + "-$2", options: .regularExpression)
    }

    /// Dynamic compatibility keys stay outside the canonical Codable keys, so local snapshots encode canonical names.
    struct Field: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init(_ name: String) { stringValue = name }
        init?(stringValue: String) { self.init(stringValue) }
        init?(intValue: Int) { return nil }
    }
}
