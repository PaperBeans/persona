#if os(iOS)
import SwiftUI

/// A To-dos row's answers, on one line at its foot after line 3 (#10465; DESIGN_SYSTEM 3.4 "To-dos list", the row;
/// `docs/design/DECISIONS.md` 11-21, "The To-dos list"): the one prominent choice, "OK: Ryokan Hanami" or Retry, in the
/// style's prominent button, then Options ⌄ in the accent, which unfolds the ask's card in place under the row
/// (`AskCard`; ⌃ while open, the choice folding into it so one primary shows). Each the style's button around one line
/// (32 pt in Liquid Glass; the Clay and Island soft buttons keep their own 44 pt) hit as a finger's height, `Space.m`
/// apart; where they do not fit whole (a narrow column, a long translation, a large text size) they stack, the choice
/// first (`StackingButtonRow`, packed), never cutting a label: "OK: ‹option›" says "OK" when it would pass
/// `Theme.Size.todoAnswerMax`, decided from its length and the text size (`TodoRowAnswers.okTitle`), never measured,
/// and a label wider than its column after all shrinks a little (`buttonLabelLine`) before anything is cut. Put off
/// with Later, the line says so and opens the card again (⌄, a fold). It reads its row's cell and the list's model,
/// never closures (P6).
struct TodoRowAnswersLine: View {
    var row: TodosListModel.Row
    var answers: TodoRowAnswers
    var model: TodosListModel
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        if answers.mark == .later {
            laterLine
        } else {
            StackingButtonRow(spacing: Theme.Space.m, stackedSpacing: Theme.Space.s, packed: true) {
                if let primary = answers.primary, !row.optionsOpen { choice(primary) }
                if answers.options { optionsButton }
            }
            .disabled(answers.mark == .busy)
        }
    }

    private func choice(_ primary: TodoRowAnswers.Primary) -> some View {
        let words = primary == .retry ? String(localized: "Retry [To-dos Answers]") : answers.okTitle(scale: Theme.textScale(typeSize))
        // The style's own button around one line: 32 pt in Liquid Glass at the default size, hit as a finger's height.
        return Button { model.answer(row.id) } label: {
            Text(words)
                .buttonLabelLine()
                .tapTargetOutset()
        }
        .font(Theme.Font.secondaryStrong)
        .themedButton(prominent: true)
        .accessibilityLabel(primary == .retry ? String(localized: "Retry \(row.content.title) [To-dos Answers]")
                                              : TodoRowAnswers.okLong(answerOption(primary)))
        .accessibilityIdentifier((primary == .retry ? "todos.answer.retry." : "todos.answer.ok.") + row.todoID)
    }

    private func answerOption(_ primary: TodoRowAnswers.Primary) -> String {
        if case .ok(let option) = primary { return option }
        return ""
    }

    /// Options ⌄ / ⌃: words in the accent with the fold's chevron, as the list's folds draw theirs.
    private var optionsButton: some View {
        Button { withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggleOptions(row.id) } } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text("Options [To-dos Answers]")
                ThemedIcon(row.optionsOpen ? Theme.Icon.collapse : Theme.Icon.expand)
                    .font(Theme.Font.labelHeavy)
                    .accessibilityHidden(true)
            }
            .lineLimit(1)
            .foregroundStyle(Theme.accent)
            .scaledFrame(minHeight: Theme.Size.well)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .font(Theme.Font.secondaryMedium)
        .fixedSize()
        .accessibilityLabel(String(localized: "Options for \(row.content.title) [To-dos Answers]"))
        .accessibilityValue(row.optionsOpen ? String(localized: "Expanded") : String(localized: "Collapsed [To-dos Answers]"))
        .accessibilityIdentifier("todos.answer.options." + row.todoID)
    }

    /// Later: "Later today ⌄", a fold that opens the card again in place.
    private var laterLine: some View {
        Button { withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggleOptions(row.id) } } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text("Later today [To-dos Answers]")
                ThemedIcon(Theme.Icon.expand)
                    .font(Theme.Font.labelHeavy)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .lineLimit(1)
            .foregroundStyle(.secondary)
            .scaledFrame(minHeight: Theme.Size.well)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .font(Theme.Font.secondary)
        .fixedSize()
        .accessibilityValue(String(localized: "Collapsed [To-dos Answers]"))
        .accessibilityIdentifier("todos.answer.later." + row.todoID)
    }
}
#endif
