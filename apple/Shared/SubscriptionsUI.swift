import Foundation

/// Source subscriptions (#2041, docs/extensions/subscriptions.md "Pages"): the manager draws their pages as declarative UI at
/// `POST /api/v1/subscriptions/-/ui`, and the app opens them as an extension page under a reserved id.
nonisolated enum SubscriptionsUI {
    /// The id the pages are drawn under: no extension can have it (extension ids never start with "-").
    static let id = "-subscriptions"
    /// The pages' one interface.
    static let page = "pages"
    /// In the hub's features once subscriptions deliver to the Inbox and the knowledge base: the entry rows show only then.
    static let feature = "source_subscriptions"
    /// Beside it once the pages are Inbox Sources (#5278): every source of the Inbox, built-in ones too. Settings has the
    /// Inbox Sources row and the Inbox's menu says so only then.
    static let sourcesFeature = "inbox_sources"
    /// The most of a Set Up with … text taken: the pages name a source, they never carry its content.
    static let askMost = 8000
    /// The manager's route for drawing and pressing the pages.
    static let path = "/api/v1/subscriptions/-/ui"

    /// The text of the pages' Set Up with … (`persona://ask?text=…`, an older name's scheme too), for the assistant's main
    /// conversation; nil for any other link or an empty text. Only these pages may answer it (`ExtensionViewModel.opens`).
    static func askText(_ url: URL) -> String? {
        guard HubNaming.accepts(scheme: url.scheme),
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false), c.host?.lowercased() == "ask",
              c.path.isEmpty || c.path == "/",
              let text = c.queryItems?.first(where: { $0.name == "text" })?.value,
              !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, text.count <= askMost else { return nil }
        return text
    }

    /// A page request with the assistant's name in its context (`context.assistant`): the name the composer shows
    /// ("Ask {name}…"), which the pages' Set Up with … takes. Only these pages get it, never an extension.
    struct Request: Encodable {
        var request: ExtensionUIRequest
        var assistant: String?

        private struct Context: Encodable {
            var base: ExtensionUIContext?
            var assistant: String?
            private enum Keys: String, CodingKey { case assistant }
            func encode(to encoder: Encoder) throws {
                try base?.encode(to: encoder)
                var c = encoder.container(keyedBy: Keys.self)
                try c.encodeIfPresent(assistant, forKey: .assistant)
            }
        }

        private enum Keys: String, CodingKey { case context, state, action, values }

        func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: Keys.self)
            try c.encode(Context(base: request.context, assistant: assistant?.isEmpty == false ? assistant : nil), forKey: .context)
            try c.encodeIfPresent(request.state, forKey: .state)
            try c.encodeIfPresent(request.action, forKey: .action)
            try c.encodeIfPresent(request.values, forKey: .values)
        }
    }

    /// The pages' first state: every subscription, one extension's or one connector's.
    static func state(extension ext: String? = nil, connector: String? = nil) -> String? {
        let scope: [String: String] = ext.map { ["ext": $0] } ?? connector.map { ["c": $0] } ?? [:]
        guard !scope.isEmpty, let data = try? JSONSerialization.data(withJSONObject: scope, options: [.sortedKeys]) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
