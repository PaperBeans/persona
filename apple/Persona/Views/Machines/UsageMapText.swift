import Foundation
import PersonaWire

/// The usage map's words (#14576): box names for the fixed boxes, the categories, the Last Used steps, why an action is
/// not offered, the offload phases, and the lines the map composes. Built once per change by the model, never in `body`
/// (docs/apple/performance.md P4).
nonisolated enum UsageMapText {
    static func size(_ b: Int64) -> String { MetricFormat.bytes(b) }

    /// A box's name: its own, or the fixed box's (`key`).
    static func name(_ n: StorageUsageNode, machine: String) -> String {
        if !n.name.isEmpty { return n.name }
        switch n.key {
        case "apps": return String(localized: "Apps [Usage Map]")
        case "home": return String(localized: "Personal Files")
        case "appdata": return String(localized: "App Data")
        case "system": return String(localized: "System")
        case "free": return String(localized: "Free Space")
        case "other": return String(localized: "Other [Usage Map]")
        case "unmeasured": return String(localized: "Not Itemized")
        case "bundle": return String(localized: "App Itself")
        case "cache": return String(localized: "Cache [Usage Map]")
        case "data": return String(localized: "Data [Usage Map]")
        default: break
        }
        switch n.kind {
        case .root: return machine
        case .free: return String(localized: "Free Space")
        case .other: return String(localized: "Other [Usage Map]")
        case .unmeasured: return String(localized: "Not Itemized")
        default: return n.path.map { ($0 as NSString).lastPathComponent } ?? n.id
        }
    }

    /// The name of a box that only says what it is in its app (an app's Cache): "Example · Cache".
    static func fullName(_ n: StorageUsageNode, parent: StorageUsageNode?, machine: String) -> String {
        let own = name(n, machine: machine)
        guard let parent, ["bundle", "cache", "data"].contains(n.key ?? ""), !parent.name.isEmpty else { return own }
        return String(localized: "\(parent.name) · \(own)")
    }

    static func category(_ c: StorageUsageCategory) -> String {
        switch c {
        case .apps: String(localized: "Apps [Usage Map]")
        case .personal: String(localized: "Personal Files")
        case .appdata: String(localized: "App Data")
        case .cache: String(localized: "Caches")
        case .system: String(localized: "System")
        case .free: String(localized: "Free Space")
        case .other: String(localized: "Other [Usage Map]")
        }
    }

    static func age(_ a: Theme.UsageMap.Age) -> String {
        switch a {
        case .recent: String(localized: "This Month")
        case .months: String(localized: "1–6 Months Ago")
        case .halfYear: String(localized: "6–12 Months Ago")
        case .year: String(localized: "Over a Year Ago")
        }
    }

    /// "Changed 3 months ago" for the detail line and VoiceOver.
    static func changed(_ usedAt: Int64?, now: Date) -> String? {
        guard let usedAt, usedAt > 0 else { return nil }
        let when = Date(timeIntervalSince1970: TimeInterval(usedAt))
        return String(localized: "Changed \(when.formatted(.relative(presentation: .named)))")
    }

    /// Why Move to Trash (or Offload) is not offered: one sentence per protection rule of the node.
    static func refusal(_ code: String?) -> String {
        switch code ?? "" {
        case "not_found": String(localized: "It's no longer there.")
        case "not_regular": String(localized: "It isn't a regular file or folder.")
        case "symlink": String(localized: "It's a link. Act on what it points to.")
        case "outside_home": String(localized: "It's outside the home folder.")
        case "home_root": String(localized: "This is the home folder itself.")
        case "standard_folder": String(localized: "This standard folder stays. What's inside it can go.")
        case "app_managed": String(localized: "Apps manage this folder. Remove it from the app instead.")
        case "system_cache": String(localized: "The system uses this cache.")
        case "dot_folder": String(localized: "Settings of your tools live here.")
        case "credentials": String(localized: "It holds keys or passwords.")
        case "agent_data": String(localized: "Your Agents keep their own data here.")
        case "persona_data": String(localized: "Persona keeps its data here.")
        case "inside_app": String(localized: "It's part of an app.")
        case "app_bundle": String(localized: "Apps are removed with their own uninstaller, or offloaded.")
        case "managed_library": String(localized: "A library or disk image that its app manages.")
        case "cloud_synced": String(localized: "It syncs to your other devices: removing it removes it there too.")
        case "mount_point": String(localized: "It's another disk. Open it to see what's on it.")
        default: String(localized: "This item is protected.")
        }
    }

    static func phase(_ p: OffloadJobPhase?) -> String {
        switch p {
        case .install: String(localized: "Installing the Tool")
        case .check: String(localized: "Checking Items")
        case .upload: String(localized: "Uploading")
        case .verify: String(localized: "Verifying")
        case .cleanup: String(localized: "Freeing Space")
        case .download: String(localized: "Downloading")
        case .other, nil: String(localized: "Working")
        }
    }

    static func percent(_ v: Double) -> String { MetricFormat.percent(v * 100) }
}
