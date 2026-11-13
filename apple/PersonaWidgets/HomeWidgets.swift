import AppIntents
import SwiftUI
import WidgetKit

/// Persona's Home Screen and Lock Screen widgets (docs/apple/home-widgets.md): each draws only the snapshot the app left
/// in the App Group (`HomeSnapshotStore`) and the persona's pictures the Live Activity's faces already hold
/// (`FleetLookStore`). The app reloads a kind's timeline when its part of the snapshot changes; a timeline carries one
/// more entry at the moment the snapshot turns stale, so old numbers step back without a reload.
enum HomeTimeline {
    static let placeholder = HomeEntry(date: .now, snapshot: HomeSnapshot(paired: true))

    /// `edit` applies the widget's Edit Widget choices before the Smart Stack's score, which counts only what it shows.
    static func entry(_ kind: HomeKind, at date: Date, snapshot: HomeSnapshot?, edit: (inout HomeEntry) -> Void) -> HomeEntry {
        var e = HomeEntry(date: date, snapshot: snapshot ?? HomeSnapshot(), pictures: HomeFaces.load())
        edit(&e)
        e.relevance = HomeRelevance.of(kind, e.context)
        return e
    }

    static func timeline(_ kind: HomeKind, edit: (inout HomeEntry) -> Void) -> Timeline<HomeEntry> {
        let snapshot = HomeSnapshotStore.shared.read()
        let now = Date.now
        var entries = [entry(kind, at: now, snapshot: snapshot, edit: edit)]
        if let snapshot, snapshot.at > 0 {
            let stale = Date(timeIntervalSince1970: snapshot.at + HomeSnapshot.staleAfter)
            if stale > now { entries.append(entry(kind, at: stale, snapshot: snapshot, edit: edit)) }
        }
        return Timeline(entries: entries, policy: .never)
    }
}

/// A widget with nothing to edit.
struct HomeProvider: TimelineProvider {
    var kind: HomeKind

    func placeholder(in context: Context) -> HomeEntry { HomeTimeline.placeholder }

    func getSnapshot(in context: Context, completion: @escaping (HomeEntry) -> Void) {
        completion(HomeTimeline.entry(kind, at: .now, snapshot: HomeSnapshotStore.shared.read()) { _ in })
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<HomeEntry>) -> Void) {
        completion(HomeTimeline.timeline(kind) { _ in })
    }
}

/// A widget with Edit Widget choices (Agents, Computers, Usage): the same timeline, filtered by them.
struct HomeEditProvider<Intent: HomeWidgetEdit>: AppIntentTimelineProvider {
    var kind: HomeKind

    func placeholder(in context: Context) -> HomeEntry { HomeTimeline.placeholder }

    func snapshot(for configuration: Intent, in context: Context) async -> HomeEntry {
        HomeTimeline.entry(kind, at: .now, snapshot: HomeSnapshotStore.shared.read()) { configuration.apply(to: &$0) }
    }

    func timeline(for configuration: Intent, in context: Context) async -> Timeline<HomeEntry> {
        HomeTimeline.timeline(kind) { configuration.apply(to: &$0) }
    }
}

/// The persona's pictures for the widgets: a hub package's, one a state, from where the app keeps the Live Activity's
/// (`FleetLookStore`, at the empty states' size); nil for Mochi, which is drawn in code.
enum HomeFaces {
    static func load() -> HomeFacePictures? {
        let store = FleetLookStore.shared
        guard let package = FleetLook.load(store).supported.package else { return nil }
        var pictures = HomeFacePictures()
        let points = HomeWidgetTokens.faceEmpty
        for state in PersonaState.allCases {
            guard let face = store.face(package, state, points: points), face.own,
                  let image = FleetFaces.image(face.png, points: CGFloat(FleetFaces.size(points))) else { continue }
            pictures.states[state.rawValue] = image
        }
        if let avatar = store.read(FleetLookStore.facesDir + "/" + FleetLookStore.folder(package) + "/"
                                   + FleetLookStore.file(FleetLookStore.avatarName, FleetFaces.size(points))) {
            pictures.avatar = FleetFaces.image(avatar, points: CGFloat(FleetFaces.size(points)))
        }
        return pictures.states.isEmpty && pictures.avatar == nil ? nil : pictures
    }
}

/// One kind's widget: its gallery name and line, its sizes (the Lock Screen's too), its view.
enum HomeWidgetConfiguration {
    static func make(_ kind: HomeKind) -> some WidgetConfiguration {
        StaticConfiguration(kind: kind.widgetKind, provider: HomeProvider(kind: kind)) { entry in
            HomeWidgetView(kind: kind, entry: entry)
        }
        .home(kind)
    }

    /// The same with Edit Widget's choices. A widget placed before it had any keeps its kind and takes the defaults.
    static func make<Intent: HomeWidgetEdit>(_ kind: HomeKind, editing: Intent.Type) -> some WidgetConfiguration {
        AppIntentConfiguration(kind: kind.widgetKind, intent: Intent.self, provider: HomeEditProvider<Intent>(kind: kind)) { entry in
            HomeWidgetView(kind: kind, entry: entry)
        }
        .home(kind)
    }
}

private extension WidgetConfiguration {
    func home(_ kind: HomeKind) -> some WidgetConfiguration {
        configurationDisplayName(Text(kind.title))
            .description(Text(kind.summary))
            .supportedFamilies(kind.families)
    }
}

/// The widgets (#5664). Each kind is a type of its own: a `Widget` has no arguments.
struct AgentsHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.agents, editing: AgentsWidgetEdit.self) }
}

struct InboxHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.inbox) }
}

struct RecordHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.record) }
}

struct TodayHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.today) }
}

struct TodosHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.todos) }
}

struct UsageHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.usage, editing: UsageWidgetEdit.self) }
}

struct ComputersHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.computers, editing: ComputersWidgetEdit.self) }
}

struct HealthHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.health) }
}
