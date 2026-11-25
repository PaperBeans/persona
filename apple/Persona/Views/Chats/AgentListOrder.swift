import Foundation

/// The Agents list's order, folds and sections (By Status / By Machine), as plain functions of the sessions and the user's pins
/// and mutes, so they are unit-tested (`AgentListOrderTests`) apart from the view.
nonisolated struct AgentListOrder {
    var isPinned: (SessionKey) -> Bool
    var isMuted: (Session) -> Bool
    /// Under a quick filter, or sorted by Recently Updated (`AgentSort`), rows are ordered by time only: an agent seen from a
    /// quick filter keeps its place.
    var byRank = true
    /// The open agent that had unseen work when it was opened (`AppModel.heldInList`): until it is left it keeps that place
    /// and stays out of the idle folds, so it does not go from under the pointer while it is read (#2213).
    var held: SessionKey? = nil
    /// The held row was Just finished when opened; its hub flag clears immediately.
    var heldJustFinished = false
    /// The rows this visit of the list showed as Unread or Just finished (`AppModel.seenThisVisit`, #12514), by the
    /// section each was in: one seen since (opened, marked, or seen elsewhere) keeps that place, drawn as Seen, until
    /// the list is refreshed, as the open one does (`held`). Empty when seen rows leave with their chat (`SeenRows`).
    var heldSeen: [SessionKey: Held] = [:]

    /// Where a held row stays: the section it was shown in before it was seen.
    enum Held: Equatable, Sendable {
        case unread, justFinished
    }

    /// The place `s` is held in, whether or not it is seen yet; nil for a row that is not held.
    func heldPlace(_ s: Session) -> Held? {
        if let held, s.key == held { return heldJustFinished ? .justFinished : .unread }
        return heldSeen[s.key]
    }

    /// A held row that is seen now and still drawn in the place it was held in (Unread, Just finished, or Muted for a
    /// muted one): that section's count leaves it out (it counts what is still unseen) and its Clear Seen lets it go.
    func isHeldSeen(_ s: Session) -> Bool {
        guard s.isQuiet, !s.needsSeen(), let place = heldPlace(s) else { return false }
        return place == .unread || justFinished(s)
    }

    func justFinished(_ s: Session) -> Bool {
        guard s.isJustFinished(muted: false) || (heldPlace(s) == .justFinished && s.state == .idle) else { return false }
        return !isMuted(s)
    }

    /// The sections of By Status, in rank order.
    static let stateKeys = ["needs", "unread", "awaiting", "working", "background", "just_finished", "waiting", "muted", "idle"]

    /// Awaiting Reply (#5096): seen, stopped on a question nobody answered yet, and not muted (a muted one folds with
    /// the idle ones, as it did before). The one rule is `Session.awaitsYourReply(muted:)`, which the rows use too.
    func awaitsReply(_ s: Session) -> Bool { s.awaitsYourReply(muted: isMuted(s)) }

    /// Rows that stay out of the idle fold: busy, pinned, waiting to be woken (`Session.isWaiting`: looping, with
    /// background work, or the hub waiting it out), or waiting on the user's reply (`awaitsReply`).
    func stays(_ s: Session) -> Bool {
        !s.isQuiet || heldPlace(s) == .unread || isPinned(s.key) || s.isWaiting || awaitsReply(s) || justFinished(s)
    }

    /// One order for every grouping (inside a space, a machine, and the sections of By Status):
    /// Needs You → Unread → Awaiting Reply → Working → Waiting in the Background (`Session.waitsInBackground`, #6045) →
    /// Just finished → Waiting → Muted → Seen. Muted agents that are working, finished-unread or waiting rank as Muted; one that needs
    /// you stays first. The index into `stateKeys`.
    func rank(_ s: Session) -> Int {
        if s.state == .needsYou { return 0 }
        let unread = s.state == .unread || (heldPlace(s) == .unread && s.isQuiet)
        let live = s.state == .working || unread || s.isWaiting
        if live && isMuted(s) { return 7 }
        if unread { return 1 }
        if awaitsReply(s) { return 2 }
        switch s.state {
        case .unread: return 1
        case .working: return s.waitsInBackground ? 4 : 3
        default:
            if s.isWaiting { return 6 }
            return justFinished(s) ? 5 : 8
        }
    }

    /// By rank, then the most recently updated (working: most recently started) first, then by session id and, for the
    /// same session id on two machines, by the complete id (hub group, machine, session), so equal times never swap
    /// between renders or with the order the machines answered in: the core's last key too (#9064).
    func ordered(_ list: [Session]) -> [Session] {
        list.map { session -> (session: Session, rank: Int, sortDate: Date) in
            (session: session, rank: byRank ? rank(session) : 0, sortDate: session.sortDate)
        }.sorted { a, b in
            if a.rank != b.rank { return a.rank < b.rank }
            if a.sortDate != b.sortDate { return a.sortDate > b.sortDate }
            if a.session.sessionID != b.session.sessionID { return a.session.sessionID < b.session.sessionID }
            return a.session.id < b.session.id
        }
        .map(\.session)
    }

    /// A flat activity timeline, including idle and offline history. Reading a reply does not move its row.
    nonisolated static func recent(_ list: [Session]) -> [Session] {
        list.sorted { a, b in
            let lhs = a.updatedAt ?? .distantPast, rhs = b.updatedAt ?? .distantPast
            return lhs != rhs ? lhs > rhs : a.id < b.id
        }
    }

    /// The rows of a space or a working tree (`ordered`), each tab's agents together where its first one ranks
    /// (`TabPanes.entries`, #2320): those that stay out of the idle fold (a tab when any of its agents does) and the
    /// idle ones; with `all` off (a quick filter), every row stays.
    func tabEntries(_ list: [Session], all: Bool = true) -> (busy: [TabPanes.Entry], idle: [TabPanes.Entry]) {
        let entries = TabPanes.entries(ordered(list))
        guard all else { return (entries, []) }
        let stays = { (e: TabPanes.Entry) in e.sessions.contains(where: self.stays) }
        return (entries.filter(stays), entries.filter { !stays($0) })
    }

    /// A space that folds into Idle Spaces: every agent in it quiet, none awaiting the user's reply, and none held.
    func isQuiet(_ g: SpaceGroup) -> Bool { g.isQuiet && !g.sessions.contains { heldPlace($0) == .unread || awaitsReply($0) || justFinished($0) } }
    /// Newest first by `date`, then by `id` so equal times never swap between renders: spaces under a quick filter,
    /// spaces and machines under Recently Updated. `date` is a `sortDate`, so a working agent's every step does not move them.
    static func latestFirst<T>(_ items: [T], date: (T) -> Date, id: (T) -> String) -> [T] {
        items.map { (item: $0, at: date($0), id: id($0)) }
            .sorted { $0.at != $1.at ? $0.at > $1.at : $0.id < $1.id }
            .map(\.item)
    }

    /// The fold ids of the sections shown now (Collapse All): one per machine with live agents, or one per rank in use.
    func sectionIDs(_ sessions: [Session], byMachine: Bool) -> [String] {
        let live = sessions.filter { $0.state.isLive }
        if byMachine { return Set(live.map(\.machineRef.path)).map { "machine/" + $0 } }
        let ranks = Set(live.map(rank))
        return Self.stateKeys.enumerated().filter { ranks.contains($0.offset) }.map { "state/" + $0.element }
    }
}

/// Limits the iPhone list's initially eligible rows before its "Show All N" row (#5766). The List remains lazy; this
/// presentation budget does not eliminate full filtering or grouping work. Other clients keep their existing list
/// behavior, including the Mac's ↑ / ↓ navigation (`AgentListRows`).
nonisolated enum AgentRowCap {
    #if os(iOS)
    static let rows = 20
    /// A By App project's: several projects share a section (`AppGroups.projects`).
    static let projectRows = 8
    #else
    static let rows = Int.max
    static let projectRows = Int.max
    #endif
    /// How many to show of `count`; nil: all of them. A run only a few over the cap shows them all (no "Show All 22").
    static func capped(_ count: Int, cap: Int = rows) -> Int? {
        cap == .max || count <= cap + 5 ? nil : cap
    }
}
