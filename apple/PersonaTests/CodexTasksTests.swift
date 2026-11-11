import Foundation
import Testing
@testable import Persona

/// Codex subagents under their chat (`CodexTasks`), as the Codex App's sidebar and Android's `CodexTaskForest` show them;
/// these cases are Android's `CodexTaskForestTest`.
@MainActor struct CodexTasksTests {
    private func row(_ id: String, parent: String? = nil, machine: String = "box", state: String = "working",
                     source: String? = "codex_history", extra: [String: Any] = [:]) throws -> Session {
        var fields: [String: Any] = ["machine_id": machine, "session_id": id, "title": id, "cwd": "/code/app",
                                     "status": state == "offline" ? "offline" : "idle", "state": state, "source_thread_id": id]
        if let source { fields["source_kind"] = source }
        if let parent { fields["parent_thread_id"] = parent }
        fields.merge(extra) { _, new in new }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    private func projection(_ sessions: [Session], _ edit: (inout AgentListProjection.Settings) -> Void = { _ in }) -> AgentListProjection {
        var settings = AgentListProjection.Settings()
        edit(&settings)
        return AgentListProjection.build(.init(sessions: sessions, machines: [], settings: settings))
    }

    @Test func theNodesFieldsAreDecoded() throws {
        let s = try row("child", extra: ["is_subagent": true, "parent_session_id": "chat", "root_session_id": "chat"])
        #expect(s.isSubagent == true && s.parentSessionID == "chat" && s.rootSessionID == "chat")
        #expect(s.isCodexSubagent)
    }

    @Test func subagentsGoUnderTheirChatOnTheirOwnMachine() throws {
        let rows = try [row("root"), row("child", parent: "root"), row("leaf", parent: "child"), row("orphan", parent: "missing"),
                        row("remote", parent: "root", machine: "other-box")]
        let tasks = CodexTasks.build(rows)
        #expect(tasks.roots.map(\.sessionID) == ["root", "orphan", "remote"])
        #expect(tasks.children[rows[0].key]?.map(\.sessionID) == ["child"])
        #expect(tasks.children[rows[1].key]?.map(\.sessionID) == ["leaf"])
        #expect(tasks.flattened(under: rows[0].key).map { "\($0.session.sessionID)@\($0.depth)" } == ["child@0", "leaf@1"])
        // The list is the chats', its counts the agents' that are not subagents.
        let list = projection(rows)
        #expect(list.base.map(\.sessionID) == ["root", "orphan", "remote"])
        #expect(list.filterCounts[.working] == 1)
        #expect(list.tasks[rows[0].key]?.map(\.session.sessionID) == ["child", "leaf"])
        #expect(list.tasks[rows[3].key] == nil)
    }

    @Test func aChatsSubagentsAreNewestFirst() throws {
        // Named so that their names sort the other way round.
        let rows = try [row("root"), row("a-old", parent: "root", extra: ["updated_at": "2026-11-01T00:00:00Z"]),
                        row("b-new", parent: "root", extra: ["updated_at": "2026-11-02T00:00:00Z"])]
        #expect(CodexTasks.build(rows).children[rows[0].key]?.map(\.sessionID) == ["b-new", "a-old"])
    }

    @Test func filtersGoByTheChatAndASearchKeepsTheChatOfAMatchingSubagent() throws {
        let rows = try [row("root", state: "idle"), row("needle", parent: "root")]
        let working = projection(rows) { $0.filter = .working }
        #expect(working.filtered.isEmpty)
        #expect(working.filterCounts[.working] == 0)
        let search = projection(rows) { $0.query = "needle" }
        #expect(search.base.map(\.sessionID) == ["root"])
        #expect(search.taskMatches[rows[0].key]?.map(\.session.sessionID) == ["needle"])
    }

    @Test func theParentsSessionIDGoesFirstAndTheRootIsTheFallback() throws {
        let parent = try row("root")
        let viaRoot = try row("child", parent: "gone", extra: ["root_session_id": "root", "is_subagent": true])
        #expect(CodexTasks.build([parent, viaRoot]).roots.map(\.sessionID) == ["root"])
        let explicit = try row("child", parent: "gone", extra: ["parent_session_id": "root", "is_subagent": true])
        #expect(CodexTasks.build([parent, explicit]).children[parent.key]?.map(\.sessionID) == ["child"])
    }

    @Test func parentsAreLookedUpInTheirOwnHubGroupAndMachine() throws {
        let root = try row("root")
        var child = try row("child", extra: ["parent_session_id": "root"])
        child.group = "other-hub"
        #expect(CodexTasks.build([root, child]).roots.count == 2)
        var localRoot = try row("root")
        localRoot.group = "other-hub"
        #expect(CodexTasks.build([root, localRoot, child]).children[localRoot.key]?.map(\.sessionID) == ["child"])
    }

    @Test func aTerminalCodexOwnsItsHistoricalSubagent() throws {
        let parent = try row("root", source: nil, extra: ["agent": "codex"])
        let child = try row("child", parent: "root", extra: ["parent_session_id": "root"])
        let tasks = CodexTasks.build([parent, child])
        #expect(tasks.roots.map(\.sessionID) == ["root"])
        #expect(tasks.children[parent.key]?.map(\.sessionID) == ["child"])
    }

    @Test func anUnresolvedParentIsNotGuessedFromAnotherHomesThread() throws {
        let root = try row("root")
        let named = try row("child", parent: "root", extra: ["parent_session_id": "other-home-root"])
        #expect(CodexTasks.build([root, named]).roots.count == 2)
        let otherHome = try row("child", parent: "root", extra: ["source_instance_id": "other-home"])
        #expect(CodexTasks.build([root, otherHome]).roots.count == 2)
    }

    @Test func otherAgentsStayRowsAndACycleKeepsOneRoot() throws {
        let regular = try [row("root", source: "runtime"), row("child", parent: "root", source: "runtime")]
        #expect(CodexTasks.build(regular).roots.map(\.sessionID) == ["root", "child"])
        #expect(projection(regular).filterCounts[.working] == 2)
        let cycle = CodexTasks.build(try [row("a", parent: "b"), row("b", parent: "a")])
        #expect(cycle.roots.count == 1)
        #expect(cycle.children.values.flatMap { $0 }.count == 1)
        #expect(CodexTasks.build(try [row("a", parent: "b"), row("b", parent: "a")]).roots.map(\.sessionID) == cycle.roots.map(\.sessionID))
    }

    /// Searching, a chat's Subtasks open on what the search finds and the subagents those hang off, not on the others.
    @Test func aSearchListsTheSubagentsItFindsAndTheirParents() throws {
        let rows = try [row("root", state: "idle"), row("other", parent: "root"), row("mid", parent: "root"),
                        row("needle", parent: "mid"), row("late", parent: "root", extra: ["updated_at": "2026-11-02T00:00:00Z"])]
        let search = projection(rows) { $0.query = "needle" }
        #expect(search.base.map(\.sessionID) == ["root"])
        #expect(search.taskMatches[rows[0].key]?.map { "\($0.session.sessionID)@\($0.depth)" } == ["mid@0", "needle@1"])
        #expect(search.tasks[rows[0].key]?.count == 4)
        #expect(projection(rows).taskMatches.isEmpty)
        let flat = CodexTasks.build(rows).flattened(under: rows[0].key)
        #expect(flat.map { "\($0.session.sessionID)@\($0.depth)" } == ["late@0", "mid@0", "needle@1", "other@0"])
        #expect(CodexTasks.kept(flat) { $0.sessionID == "late" }.map(\.session.sessionID) == ["late"])
        #expect(CodexTasks.kept(flat) { $0.sessionID == "other" || $0.sessionID == "needle" }.map(\.session.sessionID) == ["mid", "needle", "other"])
        // Kept under a sibling's subtree too: a parent before a kept sibling's kept child.
        let deep = [CodexTasks.Task(session: rows[1], depth: 0), CodexTasks.Task(session: rows[3], depth: 1),
                    CodexTasks.Task(session: rows[2], depth: 0), CodexTasks.Task(session: rows[4], depth: 1)]
        #expect(CodexTasks.kept(deep) { $0.sessionID == "needle" || $0.sessionID == "late" }.map(\.session.sessionID)
                == ["other", "needle", "mid", "late"])
    }

    /// A subagent waiting for you is not a row of the list, but it is the list's to show in Needs You.
    @Test func aSubagentThatNeedsYouIsAdmitted() throws {
        let rows = try [row("root"), row("asks", parent: "root", state: "needs_you"), row("elsewhere", parent: "gone-chat", state: "needs_you")]
        let list = projection(rows)
        #expect(list.admittedTasks.map(\.sessionID) == ["asks"])
        #expect(list.base.map(\.sessionID) == ["root", "elsewhere"])
        #expect(AgentsNeedsYou.waiting(list.base + list.admittedTasks).count == 2)
        #expect(projection(rows) { $0.machine = "another-box" }.admittedTasks.isEmpty)
    }

    @Test func aSubagentNamingNoParentIsARowOfItsOwnButNotCounted() throws {
        let rows = try [row("chat"), row("lone", extra: ["is_subagent": true])]
        let list = projection(rows)
        #expect(list.base.map(\.sessionID) == ["chat", "lone"])
        #expect(list.filterCounts[.working] == 1)
    }

    /// By App (#3756) lists the Codex App's chats, as its sidebar does: their subagents are not rows of the section.
    @Test func byAppListsTheChats() throws {
        let chat = try row("chat", extra: ["agent": "codex", "agent_surface": "desktop"])
        let sub = try row("sub", parent: "chat", extra: ["agent": "codex", "agent_surface": "desktop", "parent_session_id": "chat"])
        let list = projection([chat, sub]) { $0.grouping = "app" }
        let apps = AppGroups.build(list.unpinned)
        #expect(apps.flatMap(\.sessions).map(\.sessionID) == ["chat"])
    }
}
