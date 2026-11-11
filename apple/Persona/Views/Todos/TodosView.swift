import BeautifulUI
import SwiftUI

public struct TodoDetailRoute: Hashable, Sendable, Identifiable {
    public var id: String
    public init(id: String) { self.id = id }
}

struct TodosView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var store = TodoStore.shared
    @State private var query = ""
    @State private var showCreate = false
    @State private var creatingTitle = ""
    @State private var creatingNotes = ""
    /// The New To-Do sheet's notes mic (#2188).
    @State private var dictation = Dictation()
    /// Folded sections the user opened (Other open, Shelved, Done start folded), and merged rows opened to their members.
    @State private var unfolded: Set<TodoSection> = []
    @State private var openedGroups: Set<String> = []
    /// Restores from Shelved kept on this device (`TodoRestores`), and the moment the sections are read at: a minute's
    /// tick moves to-dos across the 24 h / 48 h lines while the page stays open.
    @State private var restored = TodoRestores.load()
    @State private var clock = Date.now
    /// A to-do opened from the calendar (and, in DEBUG screenshots, `-openTodoTitled <text>`: the first whose title has it).
    @State private var pushed: TodoDetailRoute?
    /// List or calendar, and the calendar's day / week / month (#2029): each device keeps its own.
    @StoredValue("todos.view") private var viewMode = TodosViewMode.list
    @StoredValue("todos.calendar.mode") private var calendarMode = TodoCalendarMode.day
    @State private var calendarAnchor = Date.now
    @State private var quickAdd: TodoCalendarSlot?
    @Environment(\.tabIsActive) private var inFront

    /// The list by status section (#3206, docs/assistant/todo-status-sop.md), related to-dos merged into one row. A
    /// search narrows the to-dos first, then places what is left.
    private var triaged: TodoTriageResult {
        let lookup = TodosLogic.makeSessionLookup(AppModel.shared.allSessions(inFront: inFront))
        return TodoTriageLogic.triageTodos(store.todos.filter(matchesQuery), lookup: lookup, now: clock, restored: restored)
    }

    private func matchesQuery(_ todo: TodoItem) -> Bool {
        if query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return true }
        let q = query.localizedLowercase
        if todo.title.localizedLowercase.contains(q) { return true }
        if let notes = todo.notes, notes.localizedLowercase.contains(q) { return true }
        return false
    }

    var body: some View {
        VStack(spacing: Theme.Space.flush) {
            TodosModeBar(view: $viewMode, mode: $calendarMode)
            if viewMode == .calendar {
                TodoCalendarView(todos: store.todos, mode: $calendarMode, anchor: $calendarAnchor,
                                 open: { pushed = TodoDetailRoute(id: $0) }, showList: { viewMode = .list })
            } else {
                list
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "To-dos"))
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    if viewMode == .calendar {
                        quickAdd = TodoCalendarSlot(day: calendarAnchor, time: nil)
                    } else {
                        creatingTitle = ""
                        creatingNotes = ""
                        showCreate = true
                    }
                } label: {
                    ThemedIcon(Theme.Icon.add)
                }
                .accessibilityLabel("New To-Do")
            }
        }
        .sheet(isPresented: $showCreate) {
            createSheet
        }
        .sheet(item: $quickAdd) { slot in
            TodoQuickAddSheet(slot: slot)
        }
        .task {
            await store.refresh()
            #if DEBUG
            // `-showDoneTodos YES`: the Done section starts open (screenshots of done todos).
            if UserDefaults.standard.bool(forKey: "showDoneTodos") { unfolded = [.other, .shelved, .done] }
            if let match = UserDefaults.standard.string(forKey: "openTodoTitled"),
               let todo = store.todos.first(where: { $0.title.contains(match) }) {
                pushed = TodoDetailRoute(id: todo.id)
            }
            #endif
        }
        .navigationDestination(item: $pushed) { r in TodoDetailView(todoID: r.id) }
    }

    private var list: some View {
        let result = triaged
        let openEmpty = [TodoSection.waiting, .working, .justDone, .pr].allSatisfy { result.groups($0).isEmpty }
        return List {
            Section {
                searchRow
            }
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)

            if result.isEmpty {
                emptySection
            } else {
                Section {
                    Text("\(result.open) open · \(result.shelved) shelved · \(result.finished) done")
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .accessibilityIdentifier("todos.summary")
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets(top: Theme.Space.flush, leading: Theme.Space.page, bottom: Theme.Space.flush, trailing: Theme.Space.page))
                .listRowSeparator(.hidden)

                ForEach(TodoSection.allCases, id: \.self) { section in
                    let groups = result.groups(section)
                    if !groups.isEmpty {
                        sectionView(section, groups: groups, count: result.todoCounts[section] ?? groups.count,
                                    folded: section.startsFolded && !isUnfolded(section, openEmpty: openEmpty))
                    }
                }
            }
        }
        .themedPage()
        .refreshable {
            await store.refresh()
        }
        .task(id: inFront) {
            // a minute's tick moves to-dos across the 24 h / 48 h lines while the page is in front
            while inFront, !Task.isCancelled {
                try? await Task.sleep(for: .seconds(60))
                clock = .now
            }
        }
    }

    /// A folded section is open when the user opened it, while searching, or (Other open) when nothing above it shows.
    private func isUnfolded(_ section: TodoSection, openEmpty: Bool) -> Bool {
        unfolded.contains(section) || !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || (section == .other && openEmpty)
    }

    @ViewBuilder private func sectionView(_ section: TodoSection, groups: [TodoTriageGroup], count: Int, folded: Bool) -> some View {
        Section {
            if !folded {
                if section == .shelved {
                    Text("No progress for 48 hours and nothing running. Not counted as open; nothing was deleted.")
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                }
                ForEach(groups) { group in
                    groupRows(group)
                }
            }
        } header: {
            if section.startsFolded {
                Button {
                    withAnimation(Theme.Motion.state) {
                        if unfolded.contains(section) { unfolded.remove(section) } else { unfolded.insert(section) }
                    }
                } label: {
                    HStack(spacing: Theme.Space.s) {
                        sectionTitle(section, count: count)
                        Spacer(minLength: Theme.Space.s)
                        ThemedIcon(folded ? Theme.Icon.disclosure : Theme.Icon.expand)
                            .font(Theme.Font.label)
                            .foregroundStyle(.secondary)
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityValue(folded ? Text("Collapsed") : Text("Expanded"))
                .accessibilityIdentifier("todos.section.\(section.rawValue)")
            } else {
                sectionTitle(section, count: count)
                    .accessibilityIdentifier("todos.section.\(section.rawValue)")
            }
        }
    }

    private func sectionTitle(_ section: TodoSection, count: Int) -> some View {
        Text("\(section.title) · \(count)")
            .font(Theme.Font.sectionHeader)
            .foregroundStyle(.secondary)
            .lineLimit(1)
    }

    /// A row: the group's lead, then "N related" opening the others under it.
    @ViewBuilder private func groupRows(_ group: TodoTriageGroup) -> some View {
        todoRow(group.lead, triage: group.triage[group.lead.id])
        if !group.related.isEmpty {
            let opened = openedGroups.contains(group.key)
            Button {
                withAnimation(Theme.Motion.state) {
                    if opened { openedGroups.remove(group.key) } else { openedGroups.insert(group.key) }
                }
            } label: {
                HStack(spacing: Theme.Space.xs) {
                    ThemedIcon(opened ? Theme.Icon.expand : Theme.Icon.disclosure)
                    Text("\(group.related.count) related")
                        .lineLimit(1)
                }
                .font(Theme.Font.captionStrong)
                .foregroundStyle(Theme.accent)
            }
            .buttonStyle(.plain)
            .accessibilityValue(opened ? Text("Expanded") : Text("Collapsed"))
            .accessibilityIdentifier("todos.related.\(group.key)")
            if opened {
                ForEach(group.related) { member in
                    todoRow(member, triage: group.triage[member.id])
                        .padding(.leading, Theme.Space.l)
                }
            }
        }
    }

    private func todoRow(_ todo: TodoItem, triage: TodoTriage?) -> some View {
        // a failed start waiting on the owner reads open: its checkbox and swipe close it (#3713)
        let open = TodoTriageLogic.checksAsOpen(todo, triage)
        return NavigationLink(value: TodoDetailRoute(id: todo.id)) {
            TodoRow(todo: todo, triage: triage, onToggleDone: { store.toggleDone(todo, done: open) },
                    onRestore: triage?.section == .shelved ? { restore(todo) } : nil)
        }
        .swipeActions(edge: .leading) {
            if triage?.section == .shelved {
                Button {
                    restore(todo)
                } label: {
                    Label("Restore", systemImage: Theme.Icon.restoreShelved)
                }
                .tint(Theme.accent)
            } else if !open {
                Button {
                    store.toggleDone(todo, done: false)
                } label: {
                    Label("Reopen", systemImage: Theme.Icon.undo)
                }
                .tint(Theme.accent)
            } else {
                Button {
                    store.toggleDone(todo, done: true)
                } label: {
                    Label("Done [Extension System Presentation c0b3fb]", systemImage: Theme.Icon.done)
                }
                .tint(Theme.positive)
            }
        }
        .swipeActions(edge: .trailing) {
            Button(role: .destructive) {
                Task { do { try await store.delete(id: todo.id) } catch { AppModel.shared.show(error) } }
            } label: {
                Label("Delete", systemImage: Theme.Icon.delete)
            }
        }
    }

    /// Restore from Shelved: counts as activity now, so it goes back to its section until another 48 hours pass.
    private func restore(_ todo: TodoItem) {
        withAnimation(Theme.Motion.layout) {
            clock = .now
            restored = TodoRestores.restore([todo.id], now: clock)
        }
    }

    private var searchRow: some View {
        HStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.search)
                .font(Theme.Font.secondaryMedium)
                .foregroundStyle(.secondary)
            TextField(String(localized: "Search to-dos"), text: $query)
                .textFieldStyle(.plain)
            if !query.isEmpty {
                Button { query = "" } label: {
                    ThemedIcon(Theme.Icon.clear).foregroundStyle(.tertiary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .scaledFrame(height: Theme.Size.avatar)
        .background(Theme.Fill.chip, in: .capsule)
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.xs)
    }

    @ViewBuilder private var emptySection: some View {
        Section {
            EmptyStateView(
                symbol: Theme.Icon.todo,
                title: query.isEmpty ? String(localized: "No to-dos yet") : String(localized: "No matching to-dos"),
                message: query.isEmpty ? String(localized: "Write down something to do, hand it to an agent, and track its progress.") : String(localized: "Try a different search"),
                action: query.isEmpty ? (String(localized: "New To-Do"), { showCreate = true }) : nil
            )
        }
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
    }

    private var createSheet: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(String(localized: "Title"), text: $creatingTitle)
                        .lineLimit(1)
                        .onSubmit { add() }
                    HStack(alignment: .top, spacing: Theme.Space.s) {
                        TextField(String(localized: "Notes (sent along when handed to an agent)"), text: $creatingNotes, axis: .vertical)
                            .lineLimit(3...8)
                        FieldMicButton(dictation: dictation, text: $creatingNotes, id: "todo.new.notes.mic")
                            .font(Theme.Font.body)
                            .foregroundStyle(Theme.accent)
                    }
                }
            }
            .themedPage()
            .onDisappear(perform: stopDictation) // cancelled, swiped away or added
            .navigationTitle("New To-Do")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { showCreate = false }
                        .lineLimit(1)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add [Beautiful U I Bridge 7a8a11]") { add() }
                        .lineLimit(1)
                        .disabled(creatingTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .presentationDetents([.medium])
    }

    private func add() {
        let t = creatingTitle.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        stopDictation() // what is saved is what the field shows
        let n = creatingNotes.trimmingCharacters(in: .whitespacesAndNewlines)
        // The sheet closes only once the todo is saved: a failed save keeps the typed title and notes.
        Task {
            do {
                _ = try await store.create(title: t, notes: n.isEmpty ? nil : n)
                showCreate = false
            } catch {
                AppModel.shared.show(error)
            }
        }
    }

    private func stopDictation() {
        if dictation.isListening { dictation.stop() }
    }
}

struct TodoRow: View {
    @Environment(AppModel.self) private var model
    @Environment(\.tabIsActive) private var inFront
    @Environment(\.openURL) private var openURL
    var todo: TodoItem
    /// Its place on the list by status (#3206); nil where the row is drawn outside it.
    var triage: TodoTriage? = nil
    var onToggleDone: () -> Void
    /// Set for a shelved row: one tap brings it back.
    var onRestore: (() -> Void)? = nil

    var body: some View {
        let lookup = TodosLogic.makeSessionLookup(AppModel.shared.allSessions(inFront: inFront))
        let status = TodosLogic.todoStatus(todo, lookup: lookup)
        let urgent = TodosLogic.mostUrgentAgent(todo, lookup: lookup)
        let activeAgents = todo.agents.filter { $0.releasedAt == nil }
        let ultracode = Ultracode.todoShown(todo, lookup: lookup)
        // a failed start waiting on the owner reads open, not done, and says why in red (#3713, DESIGN_SYSTEM 5.6)
        let shownDone = !TodoTriageLogic.checksAsOpen(todo, triage)
        let failedOpen = !shownDone && todo.failedToStart

        HStack(alignment: .top, spacing: Theme.Space.m) {
            Button(action: onToggleDone) {
                ThemedIcon(shownDone ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                    .font(Theme.Font.title)
                    .foregroundStyle(shownDone ? Theme.positive : .secondary)
            }
            .buttonStyle(.plain)
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)

            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(alignment: .firstTextBaseline) {
                    UltracodeMark.after(todo.title, on: ultracode, font: Theme.Font.labelStrong)
                        .font(Theme.Font.bodyStrong)
                        .foregroundStyle(shownDone ? Color.secondary : Color.primary)
                        .strikethrough(shownDone)
                        .lineLimit(2)
                        .animation(UltracodeMark.motion, value: ultracode)
                    Spacer(minLength: Theme.Space.xs)
                    if let d = todo.updatedAt ?? todo.createdAt {
                        Text(Fmt.relative(d))
                            .font(Theme.Font.label)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }

                HStack(spacing: Theme.Space.sm) {
                    StatusDot(state: urgent?.status ?? (status == .done ? .idle : .unknown), size: Theme.Size.dotS)
                    Text(todo.statusLabel(status))
                        .font(Theme.Font.labelStrong)
                        .foregroundStyle(todo.shownState(status).todoTone.text)
                    if !activeAgents.isEmpty {
                        Text("· \(activeAgents.count) agents")
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    if !todo.isDone { ReviewChip(todo: todo, compact: true) }
                }

                TodoPlanLine(todo: todo)

                if triage?.section == .pr, let pr = todo.prLink {
                    HStack(spacing: Theme.Space.s) {
                        Button {
                            openURL(pr)
                        } label: {
                            Label(todo.prLabel ?? String(localized: "View PR"), systemImage: Theme.Icon.pullRequest)
                                .lineLimit(1)
                        }
                        .font(Theme.Font.captionStrong)
                        .foregroundStyle(Theme.accent)
                        .buttonStyle(.borderless)
                        if let last = triage?.last, last > .distantPast {
                            Text("Last moved \(Fmt.relative(last))")
                                .font(Theme.Font.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }

                if let onRestore {
                    Button(action: onRestore) {
                        Label("Restore", systemImage: Theme.Icon.restoreShelved)
                            .lineLimit(1)
                    }
                    .font(Theme.Font.captionStrong)
                    .foregroundStyle(Theme.accent)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("todos.restore")
                }

                if failedOpen, let error = todo.startError {
                    Text("Failed to start: \(error)")
                        .font(Theme.Font.caption)
                        .foregroundStyle(Theme.danger)
                        .lineLimit(2)
                } else if let reason = todo.closedReason {
                    Text(reason)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                } else if let summary = mostUrgentSummary(urgent: urgent) {
                    Text(summary)
                        .font(Theme.Font.caption)
                        .foregroundStyle(urgent?.status == .needsYou ? StatusPalette.needsYou.text : .secondary)
                        .lineLimit(2)
                } else if let notes = todo.notes, !notes.isEmpty {
                    Text(notes)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                } else {
                    Text("Not handed to an agent yet")
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            .padding(.vertical, Theme.Space.xs)
        }
    }

    private func mostUrgentSummary(urgent: TodoMostUrgent?) -> String? {
        guard let urgent else { return nil }
        if urgent.isFailed, let err = urgent.agent.startError, !err.isEmpty {
            return String(localized: "Failed to start: \(err)")
        }
        if let s = urgent.session {
            if let last = s.lastText, !last.isEmpty {
                return Fmt.plain(last)
            }
            return s.displayTitle
        }
        if let t = urgent.agent.title, !t.isEmpty {
            return t
        }
        return nil
    }
}
