import SwiftUI

/// Watch recording does not depend on the hub connection: it saves without a network and sends back once near the phone.
/// `startsMeeting`: opened as Meeting Recording (the home card, the complication), it starts at once, marked a meeting
/// (docs/apple/recording.md「Apple Watch」); the phone names it after the calendar. While all-day recording runs it
/// marks the meeting in the day instead (one microphone), and the page shows the all-day state in place of the clock.
struct WatchRecordingView: View {
    var startsMeeting = false
    @State private var capture = RecordingCapture.shared
    @State private var startedMeeting = false
    @State private var library = RecordingLibrary.shared
    @State private var transfer = WatchRecordingTransfer.shared
    /// Starts in the app's UI language; the button cycles through the three the recorder supports.
    @State private var locale = WatchRecordingView.defaultLocale
    @State private var confirmingRolling = false
    @AppStorage("recording.rollingConsent") private var rollingConsent = false
    @AppStorage(WatchSync.phoneRollingKey) private var phoneRolling = false
    @State private var allDay = WatchAllDay.shared
    /// The phone's `recordingDay`: absent from a phone that doesn't take day recordings, 0 until its user agrees.
    @AppStorage(WatchSync.phoneDayKey) private var phoneDay: Int?

    /// The recorder's language for the app's UI language (also all-day recording's).
    static var defaultLocale: String {
        switch LanguageStore.shared.launched {
        case "en": "en-US"
        case "ja": "ja-JP"
        default: "zh-CN"
        }
    }

    var body: some View {
        ScrollView {
            VStack(spacing: Theme.Space.m) {
                // Still asking for the microphone: the clip view's wait and Cancel, whichever mode is starting.
                if capture.allDay, capture.state != .starting {
                    WatchAllDaySummary()
                } else {
                    clip
                }
                if let error = capture.error ?? transfer.error ?? library.error {
                    Text(error).font(Theme.Font.caption).foregroundStyle(Theme.caution)
                }
                Text("Once sent back to the phone it's transcribed on device; the text goes into Knowledge automatically and the audio isn't uploaded.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                // A day recording has its own page and summary; listed here it would wait to send all day.
                ForEach(Array(library.entries.filter { $0.source == .watch && !$0.isDay }.prefix(10))) { entry in
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        Text(entry.displayTitle).font(Theme.Font.secondaryMedium).lineLimit(2)
                        Text("\(entry.isRolling ? String(localized: "Rolling") + " · " : "")\(RecordingFormat.clock(entry.duration)) · \(transfer.pending(entry.id) == 0 && entry.endDelivered ? String(localized: "Sent to iPhone") : String(localized: "Waiting to send"))")
                            .font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(Theme.Space.s)
                    .background(Theme.Watch.cardFill, in: .rect(cornerRadius: Theme.Radius.card))
                }
            }.padding(Theme.Space.m)
        }
        .navigationTitle("Record [Recordings Section ef2821]")
        .onAppear { transfer.start() }
        .task {
            // Meeting Recording: one tap from the home list or the watch face; once, and never over a recording running.
            // While all-day recording runs, the same tap marks the meeting in the day.
            guard startsMeeting, !startedMeeting else { return }
            if capture.allDay {
                startedMeeting = true
                allDay.markMeeting()
                return
            }
            guard capture.state == .idle else { return }
            startedMeeting = true
            await capture.start(locale: locale, meeting: RecordingMeeting())
        }
        .confirmationDialog("Rolling recording", isPresented: $confirmingRolling) {
            Button("Start Rolling") {
                rollingConsent = true
                Task { await capture.start(locale: locale, rolling: true) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Keeps the last 6 hours on this watch and your iPhone. Audio stays on your devices. Ask others before recording them.")
        }
    }

    /// Every recording but all-day's: the clock, the start buttons, and the controls of the one running.
    @ViewBuilder private var clip: some View {
        Text(RecordingFormat.clock(capture.elapsed))
            .font(Theme.Font.display.monospacedDigit())
            .foregroundStyle(capture.state == .recording ? Theme.danger : .primary)
        if let id = capture.recordingID, library.entry(id)?.isMeeting == true {
            Label("Meeting", systemImage: Theme.Icon.meeting).font(Theme.Font.caption).foregroundStyle(Theme.accent)
                .accessibilityIdentifier("watch-recording-meeting")
        }
        if capture.state == .idle {
            Button { Task { await capture.start(locale: locale, meeting: RecordingMeeting()) } } label: {
                Label("Meeting Recording", systemImage: Theme.Icon.recording).lineLimit(1)
            }
            .buttonStyle(.borderedProminent)
            Button {
                let languages = ["zh-CN", "en-US", "ja-JP"]
                let index = languages.firstIndex(of: locale) ?? 0
                locale = languages[(index + 1) % languages.count]
            } label: {
                Text("Language: \(locale == "zh-CN" ? String(localized: "Chinese") : locale == "en-US" ? String(localized: "English") : String(localized: "Japanese"))")
                    .font(Theme.Font.secondary).lineLimit(1)
            }
            .buttonStyle(.bordered)
            Button { Task { await capture.start(locale: locale) } } label: {
                Label("Start Recording", systemImage: Theme.Icon.recording).lineLimit(1)
            }
            .buttonStyle(.bordered)
            .recordingPrimaryGesture()
            if phoneRolling {
                Button {
                    if rollingConsent { Task { await capture.start(locale: locale, rolling: true) } }
                    else { confirmingRolling = true }
                } label: {
                    Label("Rolling", systemImage: Theme.Icon.recurring).lineLimit(1)
                }
                .buttonStyle(.bordered)
            }
            allDayEntry
        } else if capture.state == .starting {
            ProgressView(String(localized: "Waiting for microphone access")).font(Theme.Font.secondary)
            Button("Cancel") { capture.cancelStarting() }.lineLimit(1)
        } else {
            Text(recordingStatus)
                .font(Theme.Font.caption).foregroundStyle(.secondary)
            HStack(spacing: Theme.Space.s) {
                Button { capture.togglePause() } label: {
                    Label(capture.state == .paused ? String(localized: "Continue") : String(localized: "Pause"), systemImage: capture.state == .paused ? Theme.Icon.run : Theme.Icon.pause)
                        .labelStyle(.iconOnly)
                }
                Button { capture.addMark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin).labelStyle(.iconOnly) }
                    .recordingPrimaryGesture()
                if capture.rolling {
                    Button { capture.eraseRecent() } label: { Label("Erase 10m", systemImage: Theme.Icon.delete).labelStyle(.iconOnly) }
                }
            }
            Button { capture.stop(); transfer.pump(force: true) } label: {
                Label("Stop & Save", systemImage: Theme.Icon.interrupt).lineLimit(1)
            }.tint(Theme.danger)
        }
    }

    /// All-Day Recording once the phone takes it: one tap starts it, or, until the user agreed on the iPhone, opens
    /// its page, which says so.
    @ViewBuilder private var allDayEntry: some View {
        let access = allDay.access(phoneDay: phoneDay)
        if access != .hidden {
            // Stopped by itself earlier: say so above the button that continues it.
            if allDay.state == .stopped { WatchAllDayStatus(state: .stopped, since: nil, startable: access == .ready) }
            Group {
                if access == .ready {
                    Button { allDay.requestStart() } label: { allDayLabel }
                } else {
                    NavigationLink { WatchAllDayView() } label: { allDayLabel }
                }
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("watch-recording-allday")
        }
    }

    private var allDayLabel: some View {
        Label("All-Day Recording", systemImage: Theme.Icon.allDay).lineLimit(1)
    }

    private var recordingStatus: String {
        if capture.state == .paused { return String(localized: "Paused") }
        return capture.rolling
            ? String(localized: "Rolling · keeps the last 6 hours")
            : String(localized: "Recording · keeps going with your wrist down")
    }
}

extension View {
    /// The double-tap gesture (watchOS 11) for a recording page's main action: start, continue, or a bookmark.
    @ViewBuilder func recordingPrimaryGesture() -> some View {
        if #available(watchOS 11.0, *) { handGestureShortcut(.primaryAction) } else { self }
    }
}
