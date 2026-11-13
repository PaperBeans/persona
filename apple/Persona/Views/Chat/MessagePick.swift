#if os(iOS)
import SwiftUI

// Select Messages (#3838, docs/apple/ios.md "Forwarding a message"): Forward To's Select Messages… puts the chat into
// selecting, the pressed message already picked. Each message gets a round mark on its leading edge and a tap anywhere on
// it picks or unpicks it; the composer gives way to a bar with Cancel and Forward (N). Cancel leaves the chat as it was.
// The marks read the picked set themselves, so a tap redraws its mark and the bar, never the chat.

/// The chat's selecting state: `on` while selecting (the chat reads only this), `ids` the picked messages' item ids.
@Observable final class MessagePick {
    var on = false
    var ids: Set<String> = []

    /// Starts selecting with `id` picked.
    func start(_ id: String) {
        ids = [id]
        on = true
    }

    func end() {
        on = false
        ids = []
    }

    /// Picks or unpicks `id`; false when it would pick more than one forward takes.
    func toggle(_ id: String) -> Bool {
        if ids.remove(id) != nil { return true }
        guard ids.count < ForwardMessage.most else { return false }
        ids.insert(id)
        return true
    }
}

extension ChatRow {
    /// The message a row stands for while selecting, as its long-press bar has it; nil for rows that are not messages
    /// (an injected line, a /loop tick, thinking, steps, notices, pictures alone).
    var pickBar: MessageBarInfo? {
        switch self {
        case .user(let i):
            if let source = MessageSource.of(i) {
                return MessageBarInfo(id: i.id, text: i.text ?? "", isUser: false, date: i.ts, pending: false, from: source.mention)
            }
            if Injected.of(i) != nil || (i.text ?? "").hasPrefix("/loop") { return nil }
            if let from = i.from { return MessageBarInfo(id: i.id, text: i.text ?? "", isUser: false, date: i.ts, pending: false, from: from) }
            return MessageBarInfo(id: i.id, text: i.text ?? "", isUser: true, date: i.ts, pending: false)
        case .assistant(let i):
            return MessageBarInfo(id: i.id, text: i.text ?? "", isUser: false, date: i.ts, pending: false)
        default:
            return nil
        }
    }
}

extension MessageBarInfo {
    /// What VoiceOver reads for the message's row while selecting: its words on one line, the first 200 characters.
    var pickLabel: String {
        let words = text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return words.count > 200 ? String(words.prefix(200)) + "…" : words
    }
}

/// A message's mark while selecting: filled and in the accent when picked, an empty ring when not (the Inbox's Select).
struct PickMark: View {
    let pick: MessagePick
    let id: String

    var body: some View {
        let on = pick.ids.contains(id)
        ThemedIcon(on ? Theme.Icon.successFilled : Theme.Icon.unchecked)
            .font(Theme.Font.heading)
            .foregroundStyle(on ? Theme.accent : Color.secondary)
            .scaledFrame(height: Theme.Size.avatar)
            .accessibilityHidden(true)
    }
}

extension View {
    /// A chat row while selecting: a message gets its mark on the leading edge and picks or unpicks with a tap anywhere
    /// on it; nothing inside it (links, cards, long-press) answers. Not selecting, the row as it is. `mark`: false for a
    /// long answer's later parts (#4970), which keep the mark's room and pick the answer, and are one element with it.
    @ViewBuilder func pickable(_ bar: MessageBarInfo?, mark: Bool = true, pick: MessagePick, selecting: Bool, refused: @escaping () -> Void) -> some View {
        if !selecting {
            self
        } else if let bar {
            PickRow(pick: pick, id: bar.id, label: bar.pickLabel, mark: mark, refused: refused) { self }
        } else {
            allowsHitTesting(false)
        }
    }
}

private struct PickRow<Content: View>: View {
    let pick: MessagePick
    let id: String
    /// What VoiceOver reads (`MessageBarInfo.pickLabel`). Set here, since a bubble's text can be a UIKit view that
    /// `.combine` leaves as a separate element, which would leave the row unnamed.
    let label: String
    let mark: Bool
    var refused: () -> Void
    @ViewBuilder var content: Content

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.s) {
            PickMark(pick: pick, id: id).opacity(mark ? 1 : 0)
            content.allowsHitTesting(false)
        }
        .padding(.leading, Theme.Space.page)
        .contentShape(.rect)
        .onTapGesture {
            if pick.toggle(id) { Haptics.tap() } else { refused() }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label)
        .accessibilityAddTraits(pick.ids.contains(id) ? [.isButton, .isSelected] : .isButton)
        .accessibilityIdentifier("chat.pick." + id)
        .accessibilityHidden(!mark)
    }
}

/// In the composer's place while selecting: Cancel, how many are picked, and Forward (N), off with none picked.
struct PickBar: View {
    let pick: MessagePick
    var cancel: () -> Void
    var forward: () -> Void

    var body: some View {
        let n = pick.ids.count
        HStack(spacing: Theme.Space.m) {
            Button("Cancel", action: cancel)
                .themedButton()
                .lineLimit(1)
                .accessibilityIdentifier("chat.pickCancel")
            Spacer(minLength: Theme.Space.s)
            Text("\(n) Selected [Selected Messages]").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                .accessibilityIdentifier("chat.pickCount")
            Spacer(minLength: Theme.Space.s)
            Button(action: forward) {
                Text(n == 0 ? String(localized: "Forward [Message Menu]") : String(localized: "Forward \(n) [Selected Messages]"))
                    .lineLimit(1).minimumScaleFactor(0.8)
            }
            .themedButton(prominent: true)
            .disabled(n == 0)
            .accessibilityIdentifier("chat.pickForward")
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}
#endif
