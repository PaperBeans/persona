#if os(iOS)
import SwiftUI

/// One to-do on the V1 list (#10465; docs/design/proposals/todos-redesign.md 3.4, "Row anatomy"; DESIGN_SYSTEM 3.4 "The
/// work-item row" with the tick in the source mark's slot), compact as Reminders' rows: one 12-pt padding above and
/// below; the tick; the semibold title ("!" before it on a high-priority one) with its time in words ("Due Fri", "16m
/// ago"); line 2, the mark and the hub's line in words; line 3, what the grouping does not already say; then, on one
/// line at its foot, its answers (`TodoRowAnswersLine`: OK or Retry, and Options ⌄, which unfolds the ask's `AskCard`
/// under it). An answer given here says so in line 2's place ("✓ You chose … · Undo"). A search's result found by its
/// notes says the sentence that matched in line 2's place (`TodoFoundText`), and a result offers no Mark Done and no
/// answers: one found by its title still says an answer given here, without its Undo. No door and no chevron: the row
/// is its own way to its asks. It reads its own cell only
/// (`TodosListModel.Row`), so a run's new line draws this row alone (P5), and takes the list's model, not closures, so
/// a list update that leaves it alone does not draw it again (P6). The tick is a button of its own (a 22-pt ring hit as
/// a finger's square without growing the row, spoken as the title with Done or Not done, HIG-006 and HIG-007); the rest
/// of the row opens the to-do and is spoken as one element, with its answers, the swipe's actions and Delete… as its
/// own; the answers' buttons follow it.
struct TodoListRow: View {
    var row: TodosListModel.Row
    /// What the tick, Mark Done and the row's actions do (`TodosListModel.tapTick`).
    var model: TodosListModel
    @Environment(\.accessibilityVoiceOverEnabled) private var voiceOver
    @Environment(\.accessibilitySwitchControlEnabled) private var switchControl

    var body: some View {
        let c = row.content
        // A search's result (`row.search`): a notes match says its sentence; no Mark Done (`offersMarkDone`) and no
        // answers (`answers(_:)`), neither drawn nor among its actions. What this device did with its ask is a fact, not
        // an answer: line 2 still says it without its Undo, back if the field is cleared while the hold lasts.
        let found = c.found(by: row.search)
        let answers = c.answers(row.search)
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
            tickButton(c)
            // Facts first, then the actions, `Space.s` under the words; the card unfolds the same under them.
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    titleLine(c)
                    if let found {
                        foundLine(found, mark: c.mark)
                    } else {
                        switch c.answers?.mark {
                        case .chosen(let label, let undo)?:
                            AskChosenLine(text: label, undo: undo && answers != nil ? { model.undoAnswer(row.id) } : nil)
                        case .elsewhere?:
                            AskChosenLine(text: String(localized: "Answered elsewhere [To-dos Answers]"), elsewhere: true)
                        case .unsure?:
                            AskChosenLine(text: String(localized: "Not sure your answer went through [To-dos Answers]"), elsewhere: true)
                        default:
                            if let said = c.said {
                                lineTwo(said, mark: c.mark, unsaved: c.unsaved, markDone: c.offersMarkDone(row.search))
                            }
                        }
                    }
                    if let caption = c.caption {
                        Text(caption)
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(c.spoken(row.search))
                .accessibilityAddTraits(.isButton)
                .accessibilityActions { spokenActions(c, answers: answers) }
                .accessibilityIdentifier("todos.row." + row.todoID)
                if let answers, Self.answersShown(answers) {
                    TodoRowAnswersLine(row: row, answers: answers, model: model)
                    if row.optionsOpen, let item = model.item(row.id) {
                        AskCard(ask: answers.ask, todo: item, model: model.asks, showsTitle: false, done: {
                            withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.closeOptions(row.id) }
                        })
                        .transition(.opacity)
                    }
                }
            }
            // The separator under it starts at the titles' column, as Mail's and the Inbox's.
            .alignmentGuide(.listRowSeparatorLeading) { $0[.leading] }
        }
        // The row's one padding, inside a cell with no insets above or below of its own (`TodoListCell.insets`).
        .padding(.vertical, Theme.Space.m)
        // A re-filing put it in another group or fold (proposal 3.9): a new view there, drawn hidden from its first
        // frame, which fades it in where it now is as it appears, so nothing slides across the list. With Reduce Motion
        // the model hides nothing.
        .opacity(row.entering ? 0 : 1)
        .onAppear { if row.entering { withAnimation(Theme.Motion.conceal) { row.entered() } } }
        .accessibilityElement(children: .contain)
    }

    /// The swipe's actions and Delete… (`TodoRowActionList.spoken`), and, while VoiceOver or Switch Control runs, the
    /// Undo of the list's latest held write, whose toast they cannot reach (HIG-005): read only then, so no other row
    /// follows it.
    @ViewBuilder private func spokenActions(_ c: TodoRowContent, answers: TodoRowAnswers?) -> some View {
        // Its answers first, in their own words: the choice, Options (which unfolds the card, read next), Undo.
        if let answers {
            switch answers.mark {
            case .chosen(_, true)?:
                Button(String(localized: "Undo")) { model.undoAnswer(row.id) }
            case nil, .later?:
                if let primary = answers.primary {
                    Button(primary == .retry ? String(localized: "Retry [To-dos Answers]") : Self.okWords(primary)) { model.answer(row.id) }
                }
                if answers.options {
                    Button(String(localized: "Options [To-dos Answers]")) { model.toggleOptions(row.id) }
                }
            default:
                EmptyView()
            }
        }
        ForEach(TodoRowActionList.spoken(c), id: \.self) { action in
            Button(TodoRowActions.title(action)) { TodoRowActions.run(action, row.id, model) }
        }
        if voiceOver || switchControl, let held = model.undoable, !c.struck {
            Button(String(localized: "Undo “\(held.title)” [To-dos Actions]")) { model.undo(held.id, of: held.write) }
        }
    }

    /// Whether the answers' line is drawn: not once the answer was given here (line 2 says so).
    static func answersShown(_ answers: TodoRowAnswers) -> Bool {
        switch answers.mark {
        case .chosen?, .elsewhere?, .unsure?: false
        case .later?, .busy?, nil: answers.primary != nil || answers.options
        }
    }

    private static func okWords(_ primary: TodoRowAnswers.Primary) -> String {
        if case .ok(let option) = primary { return TodoRowAnswers.okLong(option) }
        return ""
    }

    /// The tick, hit well past its glyph without moving the row (`tapTargetOutset`); not on a row still being saved.
    private func tickButton(_ c: TodoRowContent) -> some View {
        Button { model.tapTick(row.id) } label: {
            TodoTickGlyph(closed: c.closed)
                .tapTargetOutset(.all)
        }
        .buttonStyle(.plain)
        .disabled(c.saving)
        .accessibilityLabel(c.title)
        .accessibilityValue(c.tickValue)
        .accessibilityAddTraits(c.closed ? .isSelected : [])
        .accessibilityIdentifier("todos.tick." + row.todoID)
    }

    private func titleLine(_ c: TodoRowContent) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            // A tick of a moment ago is struck through in the secondary color until the hub has it closed; a closed to-do
            // keeps its title as it was (the frames draw it so), its tick saying it is closed.
            Text(Self.title(c))
                .font(Theme.Font.bodyStrong)
                .strikethrough(c.struck)
                .foregroundStyle(c.struck ? Color.secondary : Color.primary)
                .lineLimit(2)
            Spacer(minLength: Theme.Space.xs)
            if c.unseen {
                Circle().fill(StatusPalette.unread.dot)
                    .scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + $0.height }
            }
            if let trailing = c.trailing {
                Text(trailing)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(c.overdue ? AnyShapeStyle(Theme.cautionText) : AnyShapeStyle(.secondary))
                    .monospacedDigit()
                    .lineLimit(1)
                    .fixedSize()
            }
        }
    }

    /// Line 2: the mark and the line; this device's own word on the owner's write in the tertiary color ("Saving…" while a
    /// new to-do is not on the hub yet, "Not on your hub yet" for a change that could not reach it); "· Mark Done" at the
    /// end of a finished one the owner has seen (proposal 3.4, `finished_seen`).
    private func lineTwo(_ said: TodoLineText.Said, mark: TodoMark?, unsaved: Bool, markDone: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            if let mark { TodoMarkView(mark: mark) }
            if unsaved {
                Text(said.rest ?? "").foregroundStyle(.tertiary).lineLimit(1)
            } else if markDone {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.flush) {
                    Text(Self.attributed(said, then: TodoTimeWords.separator)).lineLimit(1)
                    Button { model.tapTick(row.id) } label: {
                        Text("Mark Done [To-dos Actions]")
                            .foregroundStyle(Theme.accent)
                            .lineLimit(1)
                            .fixedSize()
                            .tapTargetOutset()
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("todos.markDone." + row.todoID)
                }
            } else {
                Text(Self.attributed(said))
                    .lineLimit(said.lead == nil ? 1 : 2) // a plain line (a to-do's notes) is one line
            }
        }
        .font(Theme.Font.secondary)
    }

    /// Line 2 of a search's result found by its notes: the mark, then the sentence that matched, its words marked.
    private func foundLine(_ found: TodoFoundLine, mark: TodoMark?) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            if let mark { TodoMarkView(mark: mark) }
            TodoFoundText(line: found)
        }
        .font(Theme.Font.secondary)
    }

    /// The title, after "!" in the caution tone on a high-priority to-do, the mark kept with the first word where the
    /// title wraps (Reminders' mark; the row's spoken label says "High priority").
    static func title(_ c: TodoRowContent) -> AttributedString {
        let title = AttributedString(c.title)
        guard c.highPriority else { return title }
        var mark = AttributedString("!\u{00A0}")
        mark.foregroundColor = Theme.cautionText
        return mark + title
    }

    /// The lead in its tone, then the rest in the secondary color: one paragraph, so it wraps as one line of text.
    /// `then`: more words after it, in the secondary color (the separator before Mark Done).
    static func attributed(_ said: TodoLineText.Said, then more: String? = nil) -> AttributedString {
        var out = AttributedString()
        let leadInk: Color = switch said.tone {
        case .needs: StatusPalette.needsYou.text
        case .danger: Theme.danger
        case .ready: StatusPalette.unread.text
        case .plain: .secondary
        }
        if let lead = said.lead {
            var part = AttributedString(lead)
            part.foregroundColor = leadInk
            out += part
        }
        if let rest = said.rest {
            var part = AttributedString(said.lead == nil ? rest : TodoTimeWords.separator + rest)
            part.foregroundColor = said.tone == .danger ? Theme.danger : .secondary
            out += part
        }
        if let more {
            var part = AttributedString(more)
            part.foregroundColor = .secondary
            out += part
        }
        return out
    }
}

/// The sentence of a to-do's notes that a search matched (DESIGN_SYSTEM 3.4 "List controls": a row found by what is
/// inside it): in the secondary color, the words in the primary on the accent's soft ground, as "List controls" marks a
/// match (the accent tint). Two lines, as a hub's line 2 may take, so the sentence is read rather than a fragment; cut
/// only when the words sit further in than about a line's worth, fewer characters kept before them as the text grows
/// (`lead(for:)`), so they stay in sight. The model found the sentence and the words off the main actor; this only
/// colors them. Its own view, so only it reads the text size.
struct TodoFoundText: View {
    @Environment(\.dynamicTypeSize) private var size
    let line: TodoFoundLine

    var body: some View {
        Text(Self.marked(line, lead: Self.lead(for: size)))
            .lineLimit(2)
    }

    /// Characters kept before the first match: about a line of the row's text column at the size (some 40 characters
    /// on a phone at the default size), so a match on the second line still shows the words that lead to it. The chat
    /// search's sidebar line is one narrow line (`AssistantSearchMerge.lead(for:)`); this row is wider and has two.
    static func lead(for size: DynamicTypeSize) -> Int {
        if size <= .large { return 32 }
        if size <= .xxLarge { return 20 }
        return 10
    }

    static func marked(_ line: TodoFoundLine, lead: Int) -> AttributedString {
        var base = AttributeContainer()
        base.foregroundColor = .secondary
        return AssistantSearchMerge.marked(line.text, line.marks, lead: lead, base: base, mark: AssistantSearchMerge.tint)
    }
}

/// The tick's 22-pt ring on the title's first line (its button hits a finger's square, `TodoListRow`), in the secondary
/// color, which keeps the 3:1 a mark needs on every card and canvas (the tertiary was 1.8:1 on white); filled in the
/// accent once done. A fold's line keeps its room, unseen, so its words sit in the titles' column.
struct TodoTickGlyph: View {
    var closed: Bool

    var body: some View {
        ThemedIcon(closed ? Theme.Icon.successFilled : Theme.Icon.unchecked)
            .font(Theme.Font.title)
            .foregroundStyle(closed ? AnyShapeStyle(Theme.accent) : AnyShapeStyle(.secondary))
            .accessibilityHidden(true)
    }
}
#endif
