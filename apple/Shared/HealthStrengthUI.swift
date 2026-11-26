import Foundation

/// Strength pages share the manager-owned Health renderer and keep all returned state opaque.
nonisolated enum HealthStrengthUI {
    static let feature = "health_strength"
    static let home = #"{"page":"strength.home"}"#
    static let overview = #"{"page":"strength.overview"}"#
}
