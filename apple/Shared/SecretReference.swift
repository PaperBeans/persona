import Foundation

/// A secret provider's reference a setting holds: whose it is, the reference and what it names in words
/// (docs/extensions/secret-providers.md): an extension's secret, or a model provider's key and a computer's own key for it.
nonisolated struct SecretReference: Codable, Hashable, Sendable {
    var provider: String
    var providerName: String
    var reference: String
    var title: String?
    private enum CodingKeys: String, CodingKey { case provider, reference, title, providerName = "provider_name" }
}
