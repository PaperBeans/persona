import PersonaWire
import SwiftUI

/// One of a to-do's asks as a card (#10465; DESIGN_SYSTEM 3.3 and 4.8, "To-dos"; proposal 3.7; task cards P1 and P4).
/// It answers through `AskCardModel` (the to-do's own asks, held through their Undo window by `DecisionAnswers`), so a
/// row's card, the tray and the to-do's page answer one way on one device.
///
/// - **The home** (a to-do's own ask: a decision, a start that failed): its title (left to line 2 on a row), what it is
///   about (what each option leads to, then what happens when nobody answers), the other answers two side by side, the
///   recommended answer on a line of its own across the card, then Later · Don't handle · Discuss… (Discuss… only where
///   the page's composer can take "Answer: …"). A decision the hub recommends nothing for draws every option alike, two
///   side by side. An open decision takes two taps on an option, the first arming it.
/// - **The door** (a run's ask, at home in its session): "From ‹run› · ‹ask›", then Review Plan › or Review ›, which opens
///   the session where the ask lives. A list row offers no answers for a run's ask, so only the to-do's page draws it.
/// - Answered here, it says so in its place ("✓ You chose … · Undo", "Answered elsewhere"); put off with Later, it folds
///   to one line, "‹ask› · later today ⌄", which opens it again.
struct AskCard: View {
    var ask: OwnerAsk
    var todo: TodoItem
    var model: AskCardModel = .shared
    /// The card says its ask as its title; a row's card leaves it to the row's line 2, so it is drawn once.
    var showsTitle = true
    /// Discuss…: the to-do's page with "Answer: …" in its composer; hidden while there is none to take it.
    var discuss: (() -> Void)? = nil
    /// A run's ask: opens the session it lives in (Review Plan ›, Review ›).
    var review: (() -> Void)? = nil
    /// After an answer or Later: its row folds the card away.
    var done: () -> Void = {}

    var body: some View {
        let mark = model.mark(ask, todo: todo.id)
        Group {
            switch mark {
            case .chosen(let label, let undo)?:
                AskChosenLine(text: label, undo: undo ? { model.undo(ask, todo: todo) } : nil)
            case .elsewhere?:
                AskChosenLine(text: String(localized: "Answered elsewhere [To-dos Answers]"), elsewhere: true)
            case .unsure?:
                AskChosenLine(text: String(localized: "Not sure your answer went through [To-dos Answers]"), elsewhere: true)
            case .later?:
                laterLine
            case .busy?, nil:
                if ask.home == OwnerAskHome.run { door } else { home(busy: mark == .busy) }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("ask.card." + todo.id)
    }

    // MARK: The home

    /// The recommendation, only when the hub gives one among the options: never the first option in its place.
    private var recommended: String? { ask.recommendedOption }

    private var otherRows: [[String]] { Self.optionRows(ask) }

    /// The options besides the recommended one (every one without a recommendation), two to a row: every one reachable,
    /// none squeezed (task cards P1).
    static func optionRows(_ ask: OwnerAsk) -> [[String]] {
        let recommended = ask.recommendedOption
        let others = (ask.options ?? []).map(\.label).filter { $0 != recommended }
        return stride(from: 0, to: others.count, by: 2).map { Array(others[$0..<min($0 + 2, others.count)]) }
    }

    private func home(busy: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if showsTitle {
                Text(title)
                    .font(Theme.Font.bodyStrong)
                    .fixedSize(horizontal: false, vertical: true)
            }
            let about = model.about(ask, todo: todo.id)
            if !about.isEmpty {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    ForEach(about, id: \.self) { line in
                        Text(line)
                            .font(Theme.Font.secondary)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            ForEach(otherRows, id: \.self) { row in
                StackingButtonRow {
                    ForEach(row, id: \.self) { option in
                        answerButton(option, prominent: false)
                    }
                }
            }
            primaryButton
            exits
        }
        .disabled(busy)
        .padding(Theme.Space.l)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
    }

    private var title: String {
        if let text = ask.text, !text.isEmpty { return text }
        return ask.kind == OwnerAskKind.retry ? String(localized: "Didn't start [To-dos Line]")
            : String(localized: "A decision for you [To-dos Line]")
    }

    @ViewBuilder private var primaryButton: some View {
        if ask.kind == OwnerAskKind.retry {
            Button { act { await model.retry(ask, todo: todo) } } label: {
                Text("Retry [To-dos Answers]").aloneAcross()
            }
            .font(Theme.Font.secondaryStrong)
            .themedButton(prominent: true)
            .accessibilityLabel(String(localized: "Retry \(todo.title) [To-dos Answers]"))
            .accessibilityIdentifier("ask.retry." + todo.id)
        } else if let recommended {
            answerButton(recommended, prominent: true)
        }
    }

    /// An option: the recommended one prominent across the card, saying so; the others plain, side by side.
    private func answerButton(_ option: String, prominent: Bool) -> some View {
        let index = ask.options?.firstIndex { $0.label == option }
        let armed = index != nil && index == model.armed(ask, todo: todo.id)
        let words = armed ? String(localized: "Confirm: \(option)") : option
        return Button { act { await model.choose(option, of: ask, todo: todo) } } label: {
            if prominent && !armed {
                // "‹option› · Recommended" where it fits across the card, else the option alone: line 2 already names
                // the recommendation, and the spoken label says it. Each candidate measured once (P7).
                ViewThatFits(in: .horizontal) {
                    MeasuredOnce { Text(String(localized: "\(option) · Recommended [To-dos Answers]")).lineLimit(1) }
                    MeasuredOnce { Text(option).lineLimit(1).minimumScaleFactor(0.8) }
                }
                .answerHeight()
                .frame(maxWidth: .infinity)
            } else if prominent {
                Text(words).aloneAcross()
            } else {
                // Measured whole by the stacking row, which stacks them before a label would shrink or be cut.
                Text(words).buttonLabelLine(minimumScale: 1).answerHeight().frame(maxWidth: .infinity)
            }
        }
        .font(Theme.Font.secondaryStrong)
        .themedButton(prominent: prominent)
        .accessibilityLabel(prominent ? String(localized: "Choose \(option), the recommended option, for \(todo.title)")
                                      : String(localized: "Choose \(option) for \(todo.title)"))
        .accessibilityValue(armed ? String(localized: "Tap again to confirm") : "")
        .accessibilityIdentifier("ask.option.\(index ?? -1)." + todo.id)
    }

    /// Later · Don't handle · Discuss…: the ask's other outcomes (task cards P4), as words in the accent.
    private var exits: some View {
        FlowLayoutRow(spacing: Theme.Space.s) {
            exit(String(localized: "Later [To-dos Answers]"), id: "later") {
                model.putOff(ask, todo: todo)
                done()
            }
            if model.declinable(ask, todo: todo) {
                separator
                exit(String(localized: "Don't handle [To-dos Answers]"), id: "dont") { act { await model.decline(ask, todo: todo) } }
            }
            if let discuss {
                separator
                exit(String(localized: "Discuss… [To-dos Answers]"), id: "discuss", action: discuss)
            }
        }
        .font(Theme.Font.secondary)
    }

    private var separator: some View {
        Text(verbatim: "·").foregroundStyle(.tertiary).accessibilityHidden(true)
    }

    /// Words that are a button: hit as a finger's height, and at least as wide.
    private func exit(_ words: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(words).lineLimit(1).foregroundStyle(Theme.accent).scaledFrame(minWidth: Theme.Size.tapTarget).tapTargetOutset()
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("ask.\(id)." + todo.id)
    }

    /// Put off: one line that opens the card again, with the folds' ⌄.
    private var laterLine: some View {
        Button { model.bringBack(ask, todo: todo) } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text(String(localized: "\(title) · later today [To-dos Answers]"))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                ThemedIcon(Theme.Icon.expand)
                    .font(Theme.Font.labelHeavy)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .font(Theme.Font.secondary)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .accessibilityValue(String(localized: "Collapsed [To-dos Answers]"))
        .accessibilityIdentifier("ask.laterLine." + todo.id)
    }

    // MARK: The door

    /// What a run asks, at home in its session: its words for the request (DESIGN_SYSTEM 5.6).
    private var request: String {
        switch ask.kind {
        case OwnerAskKind.plan: String(localized: "Plan awaiting approval [To-dos Line]")
        case OwnerAskKind.tool: String(localized: "Waiting for your approval [To-dos Line]")
        default: ask.text.flatMap { $0.isEmpty ? nil : $0 } ?? String(localized: "Has a question [To-dos Answers]")
        }
    }

    private var door: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            let run = ask.run?.title.flatMap { $0.isEmpty ? nil : $0 } ?? String(localized: "An Agent [To-dos Line]")
            Text(String(localized: "From \(run) · \(request) [To-dos Answers]"))
                .font(Theme.Font.secondaryMedium)
                .foregroundStyle(StatusPalette.needsYou.text)
                .fixedSize(horizontal: false, vertical: true)
            if let review {
                Button(action: review) {
                    HStack(spacing: Theme.Space.xs) {
                        Text(ask.kind == OwnerAskKind.plan ? String(localized: "Review Plan [To-dos Answers]")
                                                           : String(localized: "Review [To-dos Answers]"))
                        ThemedIcon(Theme.Icon.forward).font(Theme.Font.labelHeavy)
                    }
                    .lineLimit(1)
                }
                .font(Theme.Font.secondaryStrong)
                .themedButton()
                .fixedSize()
                .accessibilityIdentifier("ask.review." + todo.id)
            }
        }
    }

    /// Runs an answer, then folds the card on its row.
    private func act(_ work: @escaping @MainActor () async -> Void) {
        Task {
            await work()
            done()
        }
    }
}

private extension View {
    /// A card's answer as tall as a finger: the style's button adds its own margin around this line.
    func answerHeight() -> some View { scaledFrame(minHeight: Theme.Size.well) }
}

private extension Text {
    /// The card's one prominent answer, alone across it: one line that may shrink as the app's buttons do (0.8) at the
    /// largest text sizes, where nothing can stack beside it.
    func aloneAcross() -> some View {
        lineLimit(1).minimumScaleFactor(0.8).answerHeight().frame(maxWidth: .infinity)
    }
}

/// "✓ You chose Ryokan Hanami · Undo" where an ask was: the answer given on this device, its check in the positive ink and
/// its words in the primary (green words fall under 4.5:1 on white, DESIGN_SYSTEM 6.3), and Undo in the accent while it
/// is held (`DecisionAnswers`); "Answered elsewhere" and "Not sure your answer went through" in the secondary ink, with no
/// check. One paragraph: on one line while it fits, else the words wrap beside the check and Undo takes a line of its
/// own under them (each candidate measured once, P7).
struct AskChosenLine: View {
    var text: String
    var undo: (() -> Void)? = nil
    var elsewhere = false

    var body: some View {
        ViewThatFits(in: .horizontal) {
            MeasuredOnce {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                    check
                    words.lineLimit(1)
                    if undo != nil {
                        Text(verbatim: "·").foregroundStyle(.tertiary).accessibilityHidden(true)
                        undoButton
                    }
                }
            }
            MeasuredOnce {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                        check
                        words.fixedSize(horizontal: false, vertical: true)
                    }
                    if undo != nil { undoButton }
                }
            }
        }
        .font(Theme.Font.secondary)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(elsewhere ? "ask.elsewhere" : "ask.chosen")
    }

    @ViewBuilder private var check: some View {
        if !elsewhere {
            ThemedIcon(Theme.Icon.done)
                .font(Theme.Font.labelHeavy)
                .foregroundStyle(Theme.positive)
                .accessibilityHidden(true)
        }
    }

    private var words: Text {
        Text(text).foregroundStyle(elsewhere ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary))
    }

    @ViewBuilder private var undoButton: some View {
        if let undo {
            Button(action: undo) {
                Text("Undo").foregroundStyle(Theme.accent).lineLimit(1).fixedSize()
                    .scaledFrame(minWidth: Theme.Size.tapTarget, alignment: .leading).tapTargetOutset()
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("ask.undo")
        }
    }
}
