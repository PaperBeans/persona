import SwiftUI
import WidgetKit

/// What the Record complication draws (`MeetingRecordingComplication`), the same at all times: the recording mark and
/// Record, the word of every start (#5493). The watch app compiles this file too, for its debug preview of the faces.
/// Only the view: the widget and its provider stay in the extension.
struct MeetingRecordingComplicationView: View {
    let family: WidgetFamily
    /// False while the phone says its hub's recording module is off (#15214): the same mark without the recording
    /// red, named for the page a tap opens (Recordings) instead of the start it no longer makes.
    var offersStart = true

    private var title: Text { offersStart ? Text("Record [Recordings Section ef2821]") : Text("Recordings") }
    private var tone: Color { offersStart ? WatchFaceTokens.recording : .secondary }

    var body: some View {
        switch family {
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                    .foregroundStyle(tone)
            }
            .accessibilityLabel(title)
        case .accessoryCorner:
            Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                .foregroundStyle(tone)
                .widgetLabel { title }
        case .accessoryInline:
            Label { title } icon: { Image(systemName: WatchFaceTokens.recordingSymbol) }
        default:
            HStack(spacing: WatchFaceTokens.previewSpacing) {
                Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                    .foregroundStyle(tone)
                VStack(alignment: .leading) {
                    title.font(WatchFaceTokens.title).lineLimit(1).minimumScaleFactor(WatchFaceTokens.titleMinScale)
                    // What a tap records, which the title alone no longer says (it was Meeting Recording until #5493);
                    // with the hub's recording module off, that instead.
                    (offersStart ? Text("Meeting") : Text("Off on this hub"))
                        .font(WatchFaceTokens.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

#if DEBUG
/// `-openWatchItem record-complication` in the watch app: every family at a 46 mm face's sizes, on a mock face's grey,
/// since a simulator cannot put a complication on a face from the command line (screenshots).
struct MeetingRecordingComplicationPreview: View {
    var body: some View {
        // As the face reads it from the App Group; here from the app's own copy (`-recording.phoneModule off`).
        let offersStart = WatchRecordingOffers(.stored()).starts
        let size = WatchFaceTokens.previewRectangular
        ScrollView {
            VStack(alignment: .leading, spacing: WatchFaceTokens.previewSpacing) {
                HStack(spacing: WatchFaceTokens.previewSpacing) {
                    MeetingRecordingComplicationView(family: .accessoryCircular, offersStart: offersStart)
                        .frame(width: WatchFaceTokens.previewCircular, height: WatchFaceTokens.previewCircular)
                    MeetingRecordingComplicationView(family: .accessoryCorner, offersStart: offersStart)
                        .frame(width: WatchFaceTokens.previewCorner, height: WatchFaceTokens.previewCorner)
                }
                MeetingRecordingComplicationView(family: .accessoryInline, offersStart: offersStart).font(WatchFaceTokens.previewInline).lineLimit(1)
                MeetingRecordingComplicationView(family: .accessoryRectangular, offersStart: offersStart).frame(width: size.width, height: size.height)
                    .background(WatchFaceTokens.previewWash, in: RoundedRectangle(cornerRadius: WatchFaceTokens.previewRadius))
            }
        }
        .accessibilityIdentifier("watch-record-complication-preview")
    }
}
#endif
