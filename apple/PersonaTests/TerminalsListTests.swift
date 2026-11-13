import Foundation
import Testing
@testable import Persona

/// The Terminals page (#4754): every pane of a computer, Agents' and plain ones, and a stopped Agent's left behind; and
/// a stopped Agent's row is read-only (its terminal opens, nothing is sent).
@MainActor struct TerminalsListTests {
    private func panes(_ json: String) -> TerminalPanes {
        do { return try JSON.decoder.decode(TerminalPanes.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    private func session(_ extra: String, status: String = "offline") -> Session {
        let json = """
        {"machine_id":"m","session_id":"s1","title":"Fix the index","cwd":"/tmp/p","status":"\(status)","state":"offline"\(extra)}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func everyPaneIsARowOfItsKind() {
        let list = panes("""
        {"hosts":[{"kind":"herdr","new_tab":true,"workspaces":[{"id":"w1","label":"app"},{"id":"w2","label":""}],
          "tabs":[{"id":"w1:t1","workspace_id":"w1","label":"agent-1"}],
          "panes":[{"pane":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"claude","session_id":"a1"},
                   {"pane":"w1:p2","tab_id":"w1:t1","workspace_id":"w1","foreground":"vim","cwd":"/home/me/app"},
                   {"pane":"w2:p1","tab_id":"w2:t1","workspace_id":"w2","agent_session":"s1"}]}]}
        """)
        let rows = TerminalsList.rows(list)
        #expect(rows.map(\.kind) == [.agent, .plain, .finished])
        #expect(rows[1].pane.terminal == "pane:w1:p2", "a pane without its address gets the pane: one")
        #expect(rows[1].pane.foreground == "vim")
        #expect(TerminalsList.hasFinished(list))
        #expect(TerminalsList.spaces(list, newSpace: true).map(\.label) == ["app", "w2"], "a workspace without a label is named by its id")
        #expect(TerminalsList.folder("/home/me/app") == "app")
        #expect(TerminalsList.folder(nil).isEmpty)
    }

    @Test func rowsSayWhatRunsOnce() {
        #expect(TerminalsList.agentName("codex") == "Codex", "the app's name, not the runtime id")
        #expect(TerminalsList.agentName("claude") == "Claude")
        #expect(TerminalsList.agentName(nil) == "Claude")
        #expect(TerminalsList.agentName("pi") == "pi")
        #expect(TerminalsList.plainDetails(title: "shell · zsh", foreground: "zsh", folder: "persona") == ["persona"],
                "the title names the program already")
        #expect(TerminalsList.plainDetails(title: "shell", foreground: "vim", folder: "app") == ["vim", "app"])
        #expect(TerminalsList.plainDetails(title: "shell", foreground: nil, folder: "").isEmpty)
    }

    @Test func noNewTerminalWhereTheHostCannotOpenOne() {
        let list = panes(#"{"hosts":[{"kind":"orca","new_tab":false,"workspaces":[{"id":"w1","label":"app"}],"panes":[]}]}"#)
        #expect(TerminalsList.spaces(list, newSpace: true).isEmpty)
        #expect(!TerminalsList.hasFinished(list))
    }

    @Test func anEngineWithNoWorkspaceOffersTheHomeFolder() {
        let list = panes(#"{"hosts":[{"kind":"herdr","new_tab":true,"workspaces":[],"panes":[]},{"kind":"orca","new_tab":false,"workspaces":[],"panes":[]}]}"#)
        let spaces = TerminalsList.spaces(list, newSpace: true)
        #expect(spaces.map(\.id) == [""], "one home-folder entry, for the host that opens tabs only")
        #expect(spaces.first?.kind == "herdr")
        #expect(TerminalsList.spaces(list, newSpace: false).isEmpty, "a hub without pane_new_space: the Agent line instead")
        #expect(TerminalsList.rows(list).isEmpty)
    }

    @Test func aStoppedAgentIsReadOnly() {
        let stopped = session(#","capabilities":{"read":true,"terminal":true},"read_only_reason":"stopped""#)
        #expect(stopped.isStopped)
        #expect(!stopped.canSend)
        #expect(stopped.canTerminal)
        #expect(!stopped.canInterrupt)

        let old = session("") // a node before #4754: no capabilities, as before
        #expect(!old.isStopped)
        #expect(old.canSend)

        let live = session("", status: "idle")
        #expect(!live.isStopped)
        #expect(live.canSend && live.canTerminal)
    }

    /// #6153: a computer's section ends up listed, failed or late with its request's id, never on a bare spinner; an
    /// answer to a request a newer one replaced is ignored, and a new request clears the old failure and lateness.
    @Test func aComputerNeverSitsOnABareSpinner() {
        let list = panes(#"{"hosts":[]}"#)
        var load = TerminalsLoad()
        #expect(load.shown == .waiting)
        load.expire(nil) // the spinner showed with no request of this page's
        #expect(load.shown == .late(nil))

        load.start("aaaa1111")
        #expect(load.shown == .waiting, "a new request clears lateness")
        load.expire("old")
        #expect(load.shown == .waiting, "another request's patience is not this one's")
        load.expire("aaaa1111")
        #expect(load.shown == .late("aaaa1111"))
        load.answer("aaaa1111", list)
        #expect(load.shown == .rows(list), "a late answer still lists")

        load.start("bbbb2222")
        #expect(load.shown == .rows(list), "the list stays while a refresh is on its way")
        load.expire("bbbb2222")
        #expect(load.shown == .rows(list))
        load.fail("aaaa1111", "stale")
        #expect(load.shown == .rows(list), "a replaced request's failure is ignored")
        load.fail("bbbb2222", "The request timed out.")
        #expect(load.shown == .failed("The request timed out."))
        load.start("cccc3333")
        #expect(load.shown == .rows(list), "a new request clears the failure")

        #expect(TerminalsLoad(failure: "Not connected").shown == .failed("Not connected"))
        let named = TerminalsLoad.withRequest("No answer", "cccc3333") // the tests run in zh-Hans
        #expect(named.hasPrefix("No answer") && named.contains("cccc3333"))
        #expect(TerminalsLoad.withRequest("Bad (request cccc3333)", "cccc3333") == "Bad (request cccc3333)", "said once")
        #expect(TerminalsLoad.withRequest("No answer", nil) == "No answer")
        #expect(TerminalsLoad.newRequest().count == 8)
    }
}
