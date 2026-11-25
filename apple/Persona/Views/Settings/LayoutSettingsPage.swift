import SwiftUI
import PersonaWire

#if os(iOS)
/// Settings › Appearance › Layout (#2530): how the Agents list draws its agents, each choice showing two agents as the
/// list would draw them. Every grouping draws them so, By Repository too (#3753). And what an Agent read from Unread does
/// once seen (#12514): the system's inline picker, as Light & Dark's.
struct LayoutSettingsPage: View {
    @Environment(AppModel.self) private var model
    private let choice = AgentListDisplayChoice()
    private let seen = SeenRowsChoice()

    var body: some View {
        ScrollViewReader { proxy in
            form
                // Opened from the Agents list's notice (#12514): the page arrives at the choice the notice names.
                .task {
                    guard model.revealSeenRowsSetting else { return }
                    model.revealSeenRowsSetting = false
                    await Task.yield()
                    proxy.scrollTo(SeenRows.key, anchor: .top)
                }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Agents List Layout"))
        .navigationBarTitleDisplayMode(.inline)
    }

    private var form: some View {
        SettingsForm {
            Section {
                ForEach(AgentListDisplay.allCases) { display in
                    Button { choice.display = display } label: {
                        LayoutChoice(display: display, selected: choice.display == display)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("layout-agents-" + display.rawValue)
                    .accessibilityAddTraits(choice.display == display ? .isSelected : [])
                }
            } header: {
                Text("Agents List [Layout]")
            } footer: {
                if AppStyle.current.usesBeautifulUI {
                    Text("The BeautifulUI style draws its agents as its own capsules; this choice shows in the other styles and in Compact List.")
                }
            }
            Section {
                Picker("Seen Agents", selection: Binding(get: { seen.stored }, set: { pick($0) })) {
                    ForEach(SeenRows.allCases) { mode in
                        Text(mode.title).lineLimit(1)
                            .tag(mode)
                            .accessibilityIdentifier("layout-seen-" + mode.rawValue)
                    }
                }
                .pickerStyle(.inline)
                .labelsHidden()
            } header: {
                Text("Seen Agents").id(SeenRows.key)
            } footer: {
                Text("An agent you open or mark as seen from Unread or Just Finished keeps its place, marked as seen, until you pull down to refresh, tap Clear Seen or choose a filter. With Leave When the Chat Closes it moves to Seen as soon as you leave its chat.")
            }
        }
    }

    /// The choice made here also answers the list's one-time notice; leaving with the chat lets go of what this visit holds.
    private func pick(_ mode: SeenRows) {
        seen.stored = mode
        seen.noticed = true
        if mode == .leave { model.releaseAllSeen() }
    }
}

/// One way to draw the Agents list: its name, two agents drawn that way, and what it shows.
private struct LayoutChoice: View {
    private func card<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) { content() }
            .padding(Theme.Space.m)
            .background(Theme.card, in: .rect(cornerRadius: Theme.Radius.card))
    }

    var display: AgentListDisplay
    var selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.s) {
                Text(display.title).font(Theme.Font.bodyStrong).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                if selected {
                    ThemedIcon(Theme.Icon.done).font(Theme.Font.bodyStrong).foregroundStyle(Theme.accent)
                }
            }
            preview
                .allowsHitTesting(false)
                .accessibilityHidden(true)
            Text(display.summary).font(Theme.Font.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, Theme.Space.xs)
        .contentShape(.rect)
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private var preview: some View {
        let rows = LayoutSamples.sessions
        // On the page's color as the list is: the rows on the list's card, State Cards each a card of its own.
        Group {
            switch display.style {
            case .cards?:
                VStack(spacing: Theme.Space.s) {
                    ForEach(rows) { SlotRow(session: $0).styled(.cards, dense: false) }
                }
            case let style?:
                card {
                    ForEach(rows) { SlotRow(session: $0).styled(style, dense: false) }
                }
            case nil:
                card { ForEach(rows) { CompactSlotRow(session: $0) } }
            }
        }
        .padding(Theme.Space.s)
        .background(Theme.groupedPage, in: .rect(cornerRadius: Theme.Radius.card))
    }
}

/// The two agents the Layout page draws: one waiting for you, one at work. Made up, in the app's language.
private enum LayoutSamples {
    @MainActor static let sessions: [Session] = [
        sample(id: "layout-1", title: String(localized: "Add tests for the network layer [Layout sample]"), state: "needs_you",
               words: String(localized: "Which test framework should it use? I'd go with Swift Testing. [Layout sample]"),
               slot: "tests", minutesAgo: 2),
        sample(id: "layout-2", title: String(localized: "Add the weather widget to the home screen [Layout sample]"), state: "working",
               words: String(localized: "Running the tests: 128 of 160 pass so far. [Layout sample]"), slot: "widget", minutesAgo: 0),
    ].compactMap { $0 }

    private static func sample(id: String, title: String, state: String, words: String, slot: String, minutesAgo: Int) -> Session? {
        let fields: [String: Any] = [
            "machine_id": "layout", "session_id": id, "title": title, "cwd": "/", "status": state == "working" ? "working" : "blocked",
            "state": state, "space_name": "weather", "slot_name": slot, "pane_id": "p-" + id, "last_text": words,
            "attention_reason": state == "needs_you" ? words : "",
            "updated_at": ISO8601DateFormatter().string(from: Date.now.addingTimeInterval(TimeInterval(-60 * minutesAgo))),
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: fields) else { return nil }
        return try? JSON.decoder.decode(Session.self, from: data)
    }
}
#endif
