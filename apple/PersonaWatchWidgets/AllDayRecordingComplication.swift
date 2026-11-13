import SwiftUI
import WidgetKit

/// All-Day Recording on the watch face and in the Smart Stack (docs/assistant/day-recap.md): whether it records and
/// for how long, or that it waits for a tap. A tap opens the watch app to start, continue or show it
/// (persona://allday?action=…): only the app in front can open the microphone. It shows what the app last wrote
/// (`AllDaySnapshot`); a recording the app stopped confirming turns into "Check recording".
struct AllDayRecordingComplication: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: AllDaySnapshot.widgetKind, provider: AllDayProvider()) { entry in
            AllDayComplicationFamilyView(entry: entry)
                .containerBackground(WatchFaceTokens.background, for: .widget)
                .widgetURL(AllDayLink.url(entry.face.link))
        }
        .configurationDisplayName("All-Day Recording")
        .description("Shows whether all-day recording is on. A tap starts or continues it.")
        .supportedFamilies([.accessoryCircular, .accessoryCorner, .accessoryRectangular, .accessoryInline])
    }
}

/// The snapshot as it stands now, then once more when a recording would go stale; the app reloads the timeline on
/// every change of state and on its heartbeat, which moves that moment on.
struct AllDayProvider: TimelineProvider {
    func placeholder(in context: Context) -> AllDayEntry { AllDayEntry(date: .now, snapshot: nil) }

    func getSnapshot(in context: Context, completion: @escaping (AllDayEntry) -> Void) {
        // The gallery shows the face as it starts once set up: off, one tap to start.
        let now = Date.now
        let gallery = AllDaySnapshot(state: .off, updatedAt: now, access: .ready)
        completion(AllDayEntry(date: now, snapshot: context.isPreview ? gallery : AllDaySnapshot.read()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<AllDayEntry>) -> Void) {
        let now = Date.now
        let snapshot = AllDaySnapshot.read()
        let dates = snapshot?.timeline(now: now) ?? [now]
        completion(Timeline(entries: dates.map { AllDayEntry(date: $0, snapshot: snapshot) }, policy: .never))
    }
}

/// The face as WidgetKit sizes it, in the family it asks for.
struct AllDayComplicationFamilyView: View {
    @Environment(\.widgetFamily) private var family
    let entry: AllDayEntry

    var body: some View { AllDayComplicationView(entry: entry, family: family) }
}
