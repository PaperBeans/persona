import SwiftUI
import WidgetKit

/// Record on the watch face and in the Smart Stack (docs/apple/recording.md「Apple Watch」): an explicit start, like the
/// iPhone's Control, so a tap opens the watch app straight into a meeting recording (persona://record?meeting=1). It says
/// Record, the word of every start (#5493). It shows the same at all times, except while the phone says its hub's
/// recording module is off (#15214, `PhoneRecordingAllowance` in the watch App Group): then it offers no start, only
/// the way to the Recordings page, which says so. The watch app reloads it when that word changes.
struct MeetingRecordingComplication: Widget {
    /// The complication's identity on faces already set up: never change it (it was Meeting Recording until #5493).
    static let kind = PhoneRecordingAllowance.recordWidgetKind

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: Self.kind, provider: MeetingRecordingProvider()) { entry in
            MeetingRecordingComplicationFamilyView(offersStart: entry.offersStart)
                .containerBackground(WatchFaceTokens.background, for: .widget)
                .widgetURL(URL(string: "persona://record?meeting=1"))
        }
        .configurationDisplayName("Record [Recordings Section ef2821]")
        .description("Starts recording a meeting on Apple Watch.")
        .supportedFamilies([.accessoryCircular, .accessoryCorner, .accessoryRectangular, .accessoryInline])
    }
}

struct MeetingRecordingEntry: TimelineEntry {
    var date: Date
    /// False only while the phone says its hub's recording module is off; the gallery always shows Record.
    var offersStart = true
}

struct MeetingRecordingProvider: TimelineProvider {
    /// What the watch app last stored for the face; allowed without the group or before any phone said it.
    private var stored: MeetingRecordingEntry {
        let recording = PhoneRecordingAllowance.stored(in: PhoneRecordingAllowance.groupDefaults())
        return MeetingRecordingEntry(date: .now, offersStart: WatchRecordingOffers(recording).starts)
    }

    func placeholder(in context: Context) -> MeetingRecordingEntry { MeetingRecordingEntry(date: .now) }
    func getSnapshot(in context: Context, completion: @escaping (MeetingRecordingEntry) -> Void) {
        completion(context.isPreview ? MeetingRecordingEntry(date: .now) : stored)
    }
    func getTimeline(in context: Context, completion: @escaping (Timeline<MeetingRecordingEntry>) -> Void) {
        completion(Timeline(entries: [stored], policy: .never))
    }
}

/// The face as WidgetKit sizes it, in the family it asks for.
struct MeetingRecordingComplicationFamilyView: View {
    @Environment(\.widgetFamily) private var family
    var offersStart = true

    var body: some View { MeetingRecordingComplicationView(family: family, offersStart: offersStart) }
}
