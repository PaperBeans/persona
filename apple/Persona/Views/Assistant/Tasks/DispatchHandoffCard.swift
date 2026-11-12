import SwiftUI

/// One dispatch, one card per task (docs/design/task-cards.md §3.2; the web's `HandoffCard`): an arrow, the task's title with
/// how its Agent came to it (New, Already Active, Resumed), when the Agent last moved and its state, and an arrow out. A plan
/// names only its own tasks, each independently openable. Titles come from the live to-do when loaded; a task whose Agent
/// did not start says why in red, and one with none yet says it is starting. The web draws one card per Agent; here a
/// task is one card, its lead Agent.
struct DispatchHandoffCards: View {
    var tasks: [DispatchedTasks.Entry]

    var body: some View {
        if !tasks.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                ForEach(tasks) { DispatchHandoffCard(entry: $0) }
            }
        }
    }
}

/// What a hand-off card says about its task, worked out from the stores. The card reads them (which makes it re-evaluate
/// on any to-do or session change, as the stores are shared) and hands this value to `DispatchHandoffCardBody`, which is
/// `Equatable`: a change that leaves it as it was redraws nothing below (docs/apple/performance.md P5, P6).
struct DispatchHandoffCard: View {
    var entry: DispatchedTasks.Entry
    private var store: TasksStore { .shared }

    /// What the meta line under the title says.
    enum Meta: Equatable {
        /// An Agent has the task: when it last moved (nil when nothing yet) and the task's state.
        case agent(at: Date?, state: TaskState)
        case failed(String)
        case gone
        case closedEarly
        case starting
    }

    var body: some View {
        let todo = entry.todoID.flatMap { store.todos[$0] }
        let lead = todo.flatMap { store.leadSession($0) }
        let meta = meta(todo: todo, lead: lead)
        DispatchHandoffCardBody(entry: entry, meta: meta).equatable()
    }

    private func meta(todo: TodoItem?, lead: Session?) -> Meta {
        if let lead, let todo {
            let at = lead.activityAt ?? lead.updatedAt
            return .agent(at: at.flatMap { $0.isMeaningful ? $0 : nil }, state: store.state(todo))
        }
        if let failure = failure(todo) { return .failed(failure) }
        if todo?.doneAt != nil { return .closedEarly }
        if todo == nil, entry.todoID != nil, !store.todos.isEmpty { return .gone }
        return .starting
    }

    /// Why the task's Agent did not start: the hub's word on the to-do, its plan's, the Agent's own row, or the plan task's.
    private func failure(_ todo: TodoItem?) -> String? {
        let why = todo.flatMap { store.startFailure($0) ?? $0.agents.first(where: { !($0.startError ?? "").isEmpty })?.startError } ?? entry.error
        let trimmed = why?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }
}

struct DispatchHandoffCardBody: View, Equatable {
    var entry: DispatchedTasks.Entry
    var meta: DispatchHandoffCard.Meta
    private var store: TasksStore { .shared }

    /// "3 min. ago": minutes are enough for when an Agent last moved.
    private static let relative: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f
    }()

    var body: some View {
        let card = HStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.followOn).font(Theme.Font.bodyStrong)
                .scaledFrame(width: Theme.Size.well, height: Theme.Size.well)
                .background(Theme.Fill.well, in: .circle)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.s) {
                    Text(entry.title).font(Theme.Font.bodyStrong).lineLimit(1)
                    badge
                }
                metaLine
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if entry.todoID != nil {
                ThemedIcon(Theme.Icon.open).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .card(padding: Theme.Space.m)
        if let id = entry.todoID {
            Button { open(id) } label: { card.contentShape(.rect) }
                .buttonStyle(.plain)
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isButton)
                .accessibilityIdentifier("dispatch.task.\(id)")
        } else {
            card
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("dispatch.unstarted")
        }
    }

    /// How the Agent came to the task: filled for New and Resumed, outlined for one that was running already.
    @ViewBuilder private var badge: some View {
        let active = entry.wake == "active"
        Text(active ? String(localized: "Already Active") : entry.wake == "resumed" ? String(localized: "Resumed") : String(localized: "New [Task Handoff Badge]"))
            .font(Theme.Font.labelHeavy)
            .textCase(.uppercase)
            .lineLimit(1)
            .fixedSize()
            .foregroundStyle(active ? AnyShapeStyle(.primary) : AnyShapeStyle(Theme.pageBackground))
            .padding(.horizontal, Theme.Space.s)
            .padding(.vertical, Theme.Space.xxs)
            .background { if !active { Capsule().fill(.primary) } }
            .overlay { if active { Capsule().strokeBorder(.primary, lineWidth: Theme.Size.hairline) } }
            .accessibilityIdentifier("dispatch.badge")
    }

    @ViewBuilder private var metaLine: some View {
        switch meta {
        case .agent(let at, let state):
            HStack(spacing: Theme.Space.xs) {
                if let at {
                    ThemedIcon(Theme.Icon.recent).font(Theme.Font.label).foregroundStyle(.secondary)
                    VisibleTimelineView(interval: 60) { now in
                        Text(Self.relative.localizedString(for: min(at, now), relativeTo: now)).lineLimit(1)
                    }
                } else {
                    Text("No activity yet").lineLimit(1).foregroundStyle(.tertiary)
                }
                Text(verbatim: "·").foregroundStyle(.tertiary)
                TaskMark(state: state).scaledFrame(width: Theme.Size.markM)
                Text(state.label).lineLimit(1).foregroundStyle(state == .needsYou ? AnyShapeStyle(state.labelColor) : AnyShapeStyle(.secondary))
            }
            .font(Theme.Font.caption)
            .foregroundStyle(.secondary)
        case .failed(let failure):
            Text("Didn't start: \(failure) [Tasks Store 38bbb7]").font(Theme.Font.caption).foregroundStyle(Theme.danger).lineLimit(2)
                .accessibilityIdentifier("dispatch.failure")
        case .gone:
            Text("This task no longer exists").font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1)
        case .closedEarly:
            Text("Closed before an Agent started").font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1)
        case .starting:
            Text("Starting the agent").font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1)
        }
    }

    private func open(_ id: String) {
        Haptics.tap()
        let agent = store.todos[id].flatMap { store.leadSession($0)?.key }
        #if os(iOS)
        store.openDispatchedTask(id, agent: agent, pushAgent: true)
        #else
        store.openDispatchedTask(id, agent: agent, pushAgent: false)
        #endif
    }
}
