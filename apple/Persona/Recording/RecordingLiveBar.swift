#if os(iOS)
import SwiftUI

/// The live sheet's bottom bar (docs/design/proposals/meeting-recording.md section 4C, items 6 and 7): pinned under the
/// thumb whatever is scrolled. Ask Persona is the only solid control (when Persona attends), with the keyboard button
/// beside it; Pause or Continue, Stop and Bookmark are three standard buttons, icon over label. At accessibility sizes the
/// bar stacks Ask Persona and Stop, with a More menu for the rest, every label on one line.
///
/// Stop asks first (verb: Stop everywhere): a stopped recording cannot be continued. The Live Activity's Stop stays one tap.
struct RecordingLiveBar: View {
    var entry: RecordingEntry
    var paused: Bool

    @Environment(\.dynamicTypeSize) private var size
    @State private var capture = RecordingCapture.shared
    @State private var ask = MeetingAsk.shared
    @State private var confirmingStop = false
    /// The typed question (the system's text-entry alert, DESIGN_SYSTEM.md 4.4), shown while `typing`.
    @State private var typing = false
    @State private var question = ""

    private var attends: Bool { entry.attended }

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            if size.isAccessibilitySize { stacked } else { standard }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .background { Theme.groupedPage.ignoresSafeArea(edges: .bottom) }
        .confirmationDialog("Stop Recording?", isPresented: $confirmingStop, titleVisibility: .visible) {
            Button("Stop Recording", role: .destructive) { MeetingRecorder.shared.stop(recordingID: nil) }
        } message: {
            Text("The audio is saved on this device and its text goes to Knowledge. A stopped recording can't be continued.")
        }
        .alert("Ask Persona", isPresented: $typing) {
            TextField(String(localized: "Ask about this meeting"), text: $question)
                .accessibilityIdentifier("meeting-ask-field")
            Button { send() } label: { Text("Ask [Meeting Ask]").lineLimit(1) }
                .disabled(question.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("meeting-ask-send")
            Button("Cancel", role: .cancel) { question = "" }
        } message: {
            Text((entry.attendance?.role.speaks ?? false)
                 ? String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers out loud.")
                 : String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers on screen."))
        }
    }

    // MARK: Layouts

    @ViewBuilder private var standard: some View {
        if attends {
            HStack(spacing: Theme.Space.m) {
                askButton
                typeButton
            }
        }
        HStack(spacing: Theme.Space.m) {
            pauseButton
            stopButton
            bookmarkButton
        }
    }

    /// Accessibility sizes: Ask Persona full width, then Pause or Continue, Stop and More side by side as icons (their names
    /// are their accessibility labels), so the bar is two rows tall whatever the text size and Pause stays one tap away.
    @ViewBuilder private var stacked: some View {
        if attends { askButton }
        HStack(spacing: Theme.Space.m) {
            iconButton(paused ? String(localized: "Continue") : String(localized: "Pause"), paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
                .accessibilityIdentifier("recording-pause")
            iconButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
                .accessibilityIdentifier("recording-stop")
            Menu {
                Button { addBookmark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin) }
                if attends {
                    Button { typing = true } label: { Label("Type a Question", systemImage: Theme.Icon.keyboard) }
                        .disabled(ask.busy)
                }
            } label: {
                ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading)
                    .frame(maxWidth: .infinity)
                    .scaledFrame(minHeight: Theme.Size.tapTarget)
            }
            .themedButton()
            .accessibilityLabel(Text("More"))
            .accessibilityIdentifier("recording-more")
        }
    }

    private func iconButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ThemedIcon(symbol).font(Theme.Font.heading)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
        .accessibilityLabel(Text(title))
    }

    // MARK: Controls

    private var askButton: some View {
        Button { askNow() } label: {
            HStack(spacing: Theme.Space.s) {
                if ask.busy { ProgressView() } else if !size.isAccessibilitySize { ThemedIcon(Theme.Icon.question) }
                Text(ask.busy ? String(localized: "Persona is looking…") : String(localized: "Ask Persona")).lineLimit(1).minimumScaleFactor(0.6)
            }
            .font(Theme.Font.bodyStrong)
            .frame(maxWidth: .infinity)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton(prominent: true)
        // One question at a time, deliberately stricter than the hub's two: an answer sits next to its question.
        .disabled(ask.busy)
        .accessibilityHint(Text("Asks about what was just said"))
        .accessibilityIdentifier("meeting-ask")
    }

    private var typeButton: some View {
        Button { typing = true } label: {
            ThemedIcon(Theme.Icon.keyboard).font(Theme.Font.heading)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
        }
        .themedButton()
        .clipShape(.circle)
        .disabled(ask.busy)
        .accessibilityLabel(Text("Type a Question"))
        .accessibilityIdentifier("meeting-ask-type")
    }

    private var pauseButton: some View {
        barButton(paused ? String(localized: "Continue") : String(localized: "Pause"), paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
            .accessibilityIdentifier("recording-pause")
    }

    private var stopButton: some View {
        barButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
            .accessibilityIdentifier("recording-stop")
    }

    private var bookmarkButton: some View {
        barButton(String(localized: "Bookmark"), Theme.Icon.pin) { addBookmark() }
            .accessibilityIdentifier("recording-bookmark")
    }

    /// A standard button, its icon over its label, at least a finger tall.
    private func barButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: Theme.Space.xxs) {
                ThemedIcon(symbol).font(Theme.Font.heading).scaledFrame(height: Theme.Size.icon)
                Text(title).font(Theme.Font.captionStrong).lineLimit(1).minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
    }

    // MARK: Actions

    private func askNow() {
        ask.ask(entry)
        Haptics.tap()
    }

    private func send() {
        guard ask.ask(entry, text: question) != nil else { return }
        question = ""
        Haptics.tap()
    }

    /// Pause: the haptic after the microphone is paused (a pulse on touch-down would overlap live capture). Continue: before
    /// it opens again. The state is announced.
    private func togglePause() {
        if paused {
            Haptics.tap()
            capture.togglePause()
            AccessibilityNotification.Announcement(String(localized: "Recording continued")).post()
        } else {
            capture.togglePause()
            Haptics.tap()
            AccessibilityNotification.Announcement(String(localized: "Recording paused")).post()
        }
    }

    private func addBookmark() {
        MeetingRecorder.shared.addMark(recordingID: entry.id.uuidString)
        AccessibilityNotification.Announcement(String(localized: "Bookmark added")).post()
    }
}
#endif
