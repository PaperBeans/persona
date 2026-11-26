import PersonaDomainKit
import PersonaPlantedDomain

/// App code using the planted domain: it keeps the next item a snooze returns, and switches on the Outcome at the edge.
struct PlantedRow {
    func snooze(_ item: Item, until: Instant, now: Now) -> Item {
        switch item.snoozed(until: until, at: now) {
        case .ok(let next): return next
        case .failed: return item
        }
    }
}

/// The app's own parse, which is not the domain's: the kit declares a parse too, but what Draft's returns is the app's
/// to drop.
enum Draft {
    static func parse(_ raw: String) -> Bool { !raw.isEmpty }
}

struct DraftCheck {
    func check(_ raw: String) {
        _ = Draft.parse(raw)
        Draft.parse(raw)
    }
}
