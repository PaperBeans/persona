import SwiftUI

/// Where a Feishu chat (or thread) breaks its messages up, like Feishu's own client: no time under each message;
/// a centred time separator when a new day starts or the chat was quiet for `separatorGap`; one avatar and name
/// for a run of messages from the same person, a run never reaching across a separator.
nonisolated enum LarkChatLayout {
    /// Quiet this long before a message: a time separator above it. Five minutes is what Feishu (and WeChat) use:
    /// a back-and-forth stays one block, a reply after a coffee gets its own time.
    static let separatorGap: TimeInterval = 5 * 60

    /// Space above a message: tight under the same sender's previous one.
    static let groupedGap: CGFloat = 3

    /// Same sender as the message before, both timed, with no separator between them: no second avatar / name.
    /// Senders compare by id; by name only when either has no id. All of mine are one sender (a message on its
    /// way has no id and is called Me until the chat has one of mine to copy).
    static func continues(_ m: LarkMessage, after prev: LarkMessage?, calendar: Calendar = .current) -> Bool {
        guard let prev, !m.isSystem, !prev.isSystem, sameSender(m, prev), prev.createdAt != nil, m.createdAt != nil,
              !showsTimeSeparator(m, after: prev, calendar: calendar) else { return false }
        return true
    }

    static func sameSender(_ a: LarkMessage, _ b: LarkMessage) -> Bool {
        if a.isMine || b.isMine { return a.isMine == b.isMine }
        if let x = a.senderID, let y = b.senderID, !x.isEmpty, !y.isEmpty { return x == y }
        return a.senderName == b.senderName
    }

    /// The first message, or the first of a new calendar day.
    static func startsDay(_ m: LarkMessage, after prev: LarkMessage?, calendar: Calendar = .current) -> Bool {
        guard let d = m.createdAt else { return false }
        guard let p = prev?.createdAt else { return true }
        return !calendar.isDate(d, inSameDayAs: p)
    }

    /// A time separator above `m`: a new day, or `separatorGap` or more since the message before.
    /// A message without a time gets none.
    static func showsTimeSeparator(_ m: LarkMessage, after prev: LarkMessage?, calendar: Calendar = .current) -> Bool {
        guard let d = m.createdAt else { return false }
        guard let p = prev?.createdAt else { return true }
        return startsDay(m, after: prev, calendar: calendar) || d.timeIntervalSince(p) >= separatorGap
    }

    /// The separator's words, tiered like the list's times (`LarkTime.tier`): 14:32, Yesterday 14:32, Wednesday 14:32,
    /// Oct 21 14:32, Oct 21, 2025 14:32. Days and dates follow the current locale; the time stays 24-hour.
    static func dayLabel(_ d: Date?, now: Date = .now, calendar cal: Calendar = .current) -> String {
        guard let d else { return "" }
        let c = cal.dateComponents([.hour, .minute], from: d)
        let time = String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
        let style = Date.FormatStyle(locale: .interface, calendar: cal, timeZone: cal.timeZone)
        return switch LarkTime.tier(of: d, now: now, calendar: cal) {
        case .today: time
        case .yesterday: String(localized: "Yesterday \(time)")
        case .thisWeek: d.formatted(style.weekday(.wide)) + " " + time
        case .thisYear: d.formatted(style.month().day()) + " " + time
        case .older: d.formatted(style.year().month().day()) + " " + time
        }
    }

    /// Which side a message sits on: mine on the right (Feishu on the phone), everyone else's on the left.
    enum Side: Equatable {
        case left, right
        var alignment: Alignment { self == .right ? .trailing : .leading }
        var horizontal: HorizontalAlignment { self == .right ? .trailing : .leading }
    }

    /// Mine, including one on its way, go right; system lines are centred by the row and count as left.
    static func side(of m: LarkMessage) -> Side { m.isMine && !m.isSystem ? .right : .left }

    /// Others' messages show the sender's avatar on the first of a run; mine never show my own.
    static func showsAvatar(_ m: LarkMessage, grouped: Bool) -> Bool { side(of: m) == .left && !grouped }

    /// In a group chat, others' name over the first of a run; never mine, never in a one-to-one chat.
    static func showsName(_ m: LarkMessage, grouped: Bool, isGroup: Bool) -> Bool { isGroup && showsAvatar(m, grouped: grouped) }

    /// Space above a message.
    static func gap(grouped: Bool) -> CGFloat { grouped ? groupedGap : Theme.Space.m }
}
