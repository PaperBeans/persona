import Foundation
import Testing
@testable import Persona
import PersonaWire

/// iOS parity for #13899 ("Use the herdr tab name as the session name"):
/// - Session naming helpers (`Session.herdrTabName`, `displayTabName`, `displayTitle`, `displayTitleDetail`, `usesUnifiedHerdrRename`)
/// - Unified Rename form payload (`SessionRenameForm`) for Claude, Codex, and CLIs without native rename
/// - `GET`/`PUT /api/v1/agents/display-settings` wire codec and `AgentsDisplayStore` per-hub sync, cache, older-hub fallback, and Retry
/// - Row / Needs You / Settings search integration while keeping default-off rendering unchanged
@MainActor
struct HerdrTabNameTests {
    private func decodeSession(_ overrides: [String: Any] = [:]) throws -> Session {
        var fields: [String: Any] = [
            "machine_id": "mac",
            "session_id": "s1",
            "title": "CLI session name",
            "cwd": "/work/export",
            "status": "idle",
            "state": "idle",
            "agent": "claude",
            "space_kind": "herdr",
            "space_id": "w1",
            "space_name": "persona",
            "slot_id": "w1:t1",
            "slot_name": "Export work",
            "pane_id": "w1:p1",
        ]
        for (key, value) in overrides {
            if value is NSNull {
                fields.removeValue(forKey: key)
            } else {
                fields[key] = value
            }
        }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    private func decodeInteraction(_ overrides: [String: Any] = [:]) throws -> Interaction {
        var fields: [String: Any] = [
            "id": "ix1",
            "machine_id": "mac",
            "session_id": "s1",
            "session_title": "CLI session name",
            "kind": "question",
        ]
        for (key, value) in overrides {
            fields[key] = value
        }
        return try JSON.decoder.decode(Interaction.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    // MARK: Session naming

    @Test func herdrTabNameMatchesOnlyNonAssistantHerdrSessionsWithTabAssociation() throws {
        let base = try decodeSession()
        #expect(base.herdrTabName == "Export work")
        #expect(try decodeSession(["slot_name": "  Trimmed  "]).herdrTabName == "Trimmed")
        #expect(try decodeSession(["space_kind": NSNull(), "host": "herdr-core", "slot_id": NSNull(), "tab_id": "t1"]).herdrTabName == "Export work")
        #expect(try decodeSession(["space_kind": NSNull(), "host": NSNull(), "pane_id": NSNull()]).herdrTabName == "Export work")

        #expect(try decodeSession(["assistant": true]).herdrTabName == nil)
        #expect(try decodeSession(["slot_id": NSNull(), "tab_id": NSNull()]).herdrTabName == nil)
        #expect(try decodeSession(["slot_name": "   "]).herdrTabName == nil)
        #expect(try decodeSession(["slot_name": NSNull()]).herdrTabName == nil)
        #expect(try decodeSession(["space_kind": "tern"]).herdrTabName == nil)
        #expect(try decodeSession(["space_kind": NSNull(), "host": "orca"]).herdrTabName == nil)
        #expect(try decodeSession(["space_kind": NSNull(), "host": NSNull(), "pane_id": "botmux:1"]).herdrTabName == nil)
    }

    @Test func displayTitleAndDetailKeepDefaultOffUnchangedAndPromoteTabWhenEnabled() throws {
        let session = try decodeSession()
        #expect(session.displayTitle(preferTab: false) == "CLI session name")
        #expect(session.displayTabName(preferTab: false) == nil)
        #expect(session.displayTitleDetail(preferTab: false) == nil)
        #expect(!session.usesUnifiedHerdrRename(preferTab: false))

        #expect(session.displayTitle(preferTab: true) == "Export work")
        #expect(session.displayTabName(preferTab: true) == "Export work")
        #expect(session.displayTitleDetail(preferTab: true) == "CLI session name")
        #expect(session.usesUnifiedHerdrRename(preferTab: true))

        #expect(try decodeSession(["title": "Export work"]).displayTitleDetail(preferTab: true) == nil)
        #expect(try decodeSession(["title": "   "]).displayTitleDetail(preferTab: true) == nil)

        let untabbed = try decodeSession(["slot_id": NSNull(), "tab_id": NSNull(), "slot_name": NSNull()])
        #expect(untabbed.displayTitle(preferTab: true) == "CLI session name")
        #expect(untabbed.displayTitleDetail(preferTab: true) == nil)
        #expect(!untabbed.usesUnifiedHerdrRename(preferTab: true))
    }

    // MARK: Unified Rename

    @Test func unifiedRenameSyncsCliTitleOnlyForClaudeAndCodex() throws {
        let claude = try decodeSession(["agent": "claude"])
        #expect(SessionRenameForm.isUnified(claude, preferHerdrTabName: true))
        #expect(!SessionRenameForm.isUnified(claude, preferHerdrTabName: false))

        let claudeEdit = SessionRenameForm.body(claude, tab: " Updated export ", title: claude.title, unified: true)
        #expect(claudeEdit.tabName == "Updated export")
        #expect(claudeEdit.title == "Updated export")

        let codex = try decodeSession(["agent": "codex"])
        let codexEdit = SessionRenameForm.body(codex, tab: "Codex export", title: codex.title, unified: true)
        #expect(codexEdit.tabName == "Codex export")
        #expect(codexEdit.title == "Codex export")

        let opencode = try decodeSession(["agent": "opencode"])
        let opencodeEdit = SessionRenameForm.body(opencode, tab: "Tab only", title: opencode.title, unified: true)
        #expect(opencodeEdit.tabName == "Tab only")
        #expect(opencodeEdit.title == nil)

        // Keeping the tab name while Claude's CLI title still differs allows syncing both.
        let syncEdit = SessionRenameForm.body(claude, tab: "Export work", title: claude.title, unified: true)
        #expect(syncEdit.tabName == "Export work")
        #expect(syncEdit.title == "Export work")

        // When both already match, nothing is sent.
        let same = try decodeSession(["title": "Export work"])
        let sameEdit = SessionRenameForm.body(same, tab: "Export work", title: same.title, unified: true)
        #expect(sameEdit.tabName == nil && sameEdit.title == nil)

        // Non-Claude/Codex with unchanged tab name sends nothing.
        let sameOpencode = SessionRenameForm.body(opencode, tab: "Export work", title: opencode.title, unified: true)
        #expect(sameOpencode.tabName == nil && sameOpencode.title == nil)

        // Blank input or untabbed session sends nothing.
        let blankEdit = SessionRenameForm.body(claude, tab: "   ", title: claude.title, unified: true)
        #expect(blankEdit.tabName == nil && blankEdit.title == nil)
        let untabbed = try decodeSession(["slot_id": NSNull(), "tab_id": NSNull()])
        let untabbedEdit = SessionRenameForm.body(untabbed, tab: "New", title: untabbed.title, unified: true)
        #expect(untabbedEdit.tabName == nil && untabbedEdit.title == nil)

        // Unified mode off: keeps existing two-field behavior.
        let offEdit = SessionRenameForm.body(claude, tab: "Export work", title: "CLI only", unified: false)
        #expect(offEdit.tabName == nil && offEdit.title == "CLI only")
    }

    // MARK: Row, Needs You, and Search presentation

    @Test func slotRowAndCompactRowPromoteTabAndMoveCliTitleToDetailWithoutDuplicatingTab() throws {
        let session = try decodeSession()

        let offSlot = SlotRow(session: session, space: "persona", machine: "MacBook", preferHerdrTabName: false)
        #expect(offSlot.rowTitle == "CLI session name")
        #expect(offSlot.placeString == "persona › Export work · MacBook")
        #expect(offSlot.meta == "Claude · persona › Export work · MacBook")

        let onSlot = SlotRow(session: session, space: "persona", machine: "MacBook", preferHerdrTabName: true)
        #expect(onSlot.rowTitle == "Export work")
        #expect(onSlot.placeString == "CLI session name · persona · MacBook")
        #expect(onSlot.meta == "CLI session name · Claude · persona · MacBook")

        let onInsideSpace = SlotRow(session: session, preferHerdrTabName: true)
        #expect(onInsideSpace.placeString == "CLI session name")

        let matching = try decodeSession(["title": "Export work"])
        #expect(SlotRow(session: matching, preferHerdrTabName: true).placeString == "")

        let offCompact = CompactSlotRow(session: session, space: "persona", preferHerdrTabName: false)
        #expect(offCompact.place == "persona › Export work")
        let onCompact = CompactSlotRow(session: session, space: "persona", preferHerdrTabName: true)
        #expect(onCompact.place == "CLI session name · persona")
    }

    @Test func needsYouAndInteractionPlaceLineFollowPreferHerdrTabName() throws {
        let session = try decodeSession()
        #expect(NeedsYouWaitingCard.place(session, machine: "MacBook", preferHerdrTabName: false) == "CLI session name · MacBook")
        #expect(NeedsYouWaitingCard.place(session, machine: "MacBook", preferHerdrTabName: true) == "Export work · MacBook")

        let ix = try decodeInteraction()
        #expect(ix.placeLine(machine: "MacBook") == "CLI session name · MacBook")
        #expect(ix.placeLine(machine: "MacBook", sessionTitleOverride: "Export work") == "Export work · MacBook")

        // Folded side/subagent cards keep their own source label (including cross-machine folded cards).
        let sideCard = try decodeInteraction(["folded_under": "parent", "folded_as": SideFold.side, "session_title": "Side work"])
        #expect(sideCard.placeLine(machine: "MacBook", sessionTitleOverride: "Export work").contains("Side work"))
        let crossMachineCard = try decodeInteraction([
            "folded_under": "s1",
            "folded_under_machine_id": "other-mac",
            "session_title": "Remote child",
        ])
        #expect(crossMachineCard.placeLine(machine: "MacBook", sessionTitleOverride: "Export work").contains("Remote child"))
    }

    @Test func subagentOverviewHonorsPreferHerdrTabNameForFoldedChildren() throws {
        let parent = try decodeSession(["session_id": "parent", "title": "Parent CLI", "slot_name": "Parent tab"])
        let child = try decodeSession([
            "session_id": "child",
            "title": "Child CLI",
            "slot_name": "Child tab",
            "state": "working",
            "folded_under": "parent",
            "is_subagent": true,
        ])

        let stampOff = SubagentOverview.inputStamp(session: parent, children: [child], pending: [], tasks: TaskIndex(), preferTab: false)
        let stampOn = SubagentOverview.inputStamp(session: parent, children: [child], pending: [], tasks: TaskIndex(), preferTab: true)
        #expect(stampOff != stampOn)

        let offOverview = SubagentOverview.build(
            parent: parent, parentKey: parent.key, embedded: [], foldedChildren: [child], preferTab: false
        )
        #expect(offOverview.running.first?.title == "Child CLI")

        let onOverview = SubagentOverview.build(
            parent: parent, parentKey: parent.key, embedded: [], foldedChildren: [child], preferTab: true
        )
        #expect(onOverview.running.first?.title == "Child tab")
    }

    @Test func settingsSearchFindsAgentsByHerdrTabAliases() {
        #expect(SettingsCatalog.search("herdr").contains { $0.id == "agents" })
    }

    @Test func chatHeaderAndFullSearchMoveCliTitleToDetailWhenEnabled() throws {
        let session = try decodeSession()

        let offHeader = session.chatSubtitleParts(machine: "MacBook", tabShown: false, preferTab: false)
        #expect(offHeader.full == ["MacBook", "persona › Export work"])
        #expect(offHeader.short == ["persona › Export work"])

        let onHeader = session.chatSubtitleParts(machine: "MacBook", tabShown: false, preferTab: true)
        #expect(onHeader.full == ["CLI session name", "MacBook", "persona"])
        #expect(onHeader.short == ["CLI session name", "persona"])

        let sameHeader = try decodeSession(["title": "Export work"]).chatSubtitleParts(machine: "MacBook", tabShown: false, preferTab: true)
        #expect(sameHeader.full == ["MacBook", "persona"])
        #expect(sameHeader.short == ["persona"])

        #expect(session.fullSearchTitle(preferTab: false) == "CLI session name")
        #expect(session.fullSearchSubtitle(stateLabel: "Seen", machine: "MacBook", preferTab: false) == "Seen · MacBook")
        #expect(session.fullSearchTitle(preferTab: true) == "Export work")
        #expect(session.fullSearchSubtitle(stateLabel: "Seen", machine: "MacBook", preferTab: true) == "CLI session name · Seen · MacBook")

        // Search matching finds sessions by herdr tab name as well as CLI title.
        #expect(session.matchesScope(.all, terms: ["Export"]))
        #expect(session.matchesScope(.title, terms: ["Export"]))
        #expect(MentionRows.sessions([session], except: nil, query: "export").map(\.key) == [session.key])
        let mentionRows = MentionRows.rows(query: "export", cwd: nil, used: [], listing: nil, sessions: [session],
                                           except: nil, preferHerdrTabName: { _ in true })
        #expect(mentionRows.first?.name == "Export work")
        #expect(mentionRows.first?.detail.hasPrefix("CLI session name · ") == true)
        let forwarded = ForwardTargets.sections(
            [session], from: SessionKey(machineID: "other", sessionID: "x"), recents: [], main: nil,
            machines: [(ref: session.machineRef, name: "MacBook")], query: "export", online: { _ in true }
        )
        #expect(forwarded.first?.sessions.map(\.key) == [session.key])
    }

    @Test func staleInFlightReadDoesNotOverwriteNewerWrite() async {
        let hub = FakeHub()
        var resumeRead: CheckedContinuation<Data, Error>?
        var readStarted: CheckedContinuation<Void, Never>?

        hub.onAsync("GET", "/api/v1/agents/display-settings") { _ in
            try await withCheckedThrowingContinuation { cont in
                resumeRead = cont
                readStarted?.resume()
                readStarted = nil
            }
        }
        hub.on("PUT", "/api/v1/agents/display-settings",
               json: #"{"show_botmux":true,"configured":true,"use_herdr_tab_name":true}"#)

        let store = AgentsDisplayStore(deps: .init(api: { hub.client }))
        let readTask = Task { await store.refresh() }
        await withCheckedContinuation { cont in
            if resumeRead != nil {
                cont.resume()
            } else {
                readStarted = cont
            }
        }

        let saved = await store.setUseHerdrTabName(true)
        #expect(saved)
        #expect(store.useHerdrTabName == true)

        resumeRead?.resume(returning: Data(#"{"show_botmux":true,"configured":true,"use_herdr_tab_name":false}"#.utf8))
        await readTask.value
        #expect(store.useHerdrTabName == true, "a stale GET started before PUT must not revert the acknowledged PUT")
    }

    // MARK: API, Store sync/caching, older-hub fallback, and AppModel multi-hub scoping

    @Test func apiReadsAndWritesSingleFieldDisplaySettings() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/agents/display-settings",
               json: #"{"show_botmux":true,"configured":false,"use_herdr_tab_name":false}"#)
        hub.on("PUT", "/api/v1/agents/display-settings") { req in
            let body = try #require(req.body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] })
            #expect(body.count == 1 && (body["use_herdr_tab_name"] as? Bool) == true)
            return Data(#"{"show_botmux":true,"configured":false,"use_herdr_tab_name":true}"#.utf8)
        }

        let initial = try await hub.client.agentsDisplaySettings()
        #expect(initial.useHerdrTabName == false)

        let updated = try await hub.client.setAgentsUseHerdrTabName(true)
        #expect(updated.useHerdrTabName == true)
        #expect(hub.sent("GET", "/api/v1/agents/display-settings").count == 1)
        #expect(hub.sent("PUT", "/api/v1/agents/display-settings").count == 1)
    }

    @Test func storeCachesPerHubHonorsOlderHubsAndRetriesFailedWrites() async {
        let hub = FakeHub()
        var persisted: AgentsDisplayStore.Cached? = AgentsDisplayStore.Cached(useHerdrTabName: true)
        var failWrite = true

        hub.on("GET", "/api/v1/agents/display-settings",
               json: #"{"show_botmux":true,"configured":true,"use_herdr_tab_name":false}"#)
        hub.on("PUT", "/api/v1/agents/display-settings") { _ in
            if failWrite { throw APIError(status: 503, message: "Hub offline") }
            return Data(#"{"show_botmux":true,"configured":true,"use_herdr_tab_name":true}"#.utf8)
        }

        let store = AgentsDisplayStore(deps: .init(
            api: { hub.client },
            loadCache: { persisted },
            saveCache: { persisted = $0 }
        ))
        // Restores cached value immediately before network answers.
        #expect(store.useHerdrTabName == true)

        // Refresh syncs hub value and updates cache.
        await store.refresh()
        #expect(store.useHerdrTabName == false)
        #expect(persisted?.useHerdrTabName == false)

        // Failed PUT preserves last acknowledged value and records Retry state.
        let failed = await store.setUseHerdrTabName(true)
        #expect(!failed)
        #expect(store.useHerdrTabName == false)
        #expect(store.error != nil)
        #expect(store.pendingSave == true)

        // Retry succeeds and updates acknowledged state + cache.
        failWrite = false
        await store.retry()
        #expect(store.useHerdrTabName == true)
        #expect(store.error == nil)
        #expect(store.pendingSave == nil)
        #expect(persisted?.useHerdrTabName == true)

        // Transient 503 on GET preserves last acknowledged cached value.
        hub.on("GET", "/api/v1/agents/display-settings") { _ in throw APIError(status: 503, message: "Offline") }
        await store.refresh()
        #expect(store.useHerdrTabName == true)

        // Older hub (404 or omitted use_herdr_tab_name) clears to nil (hidden & off).
        hub.on("GET", "/api/v1/agents/display-settings") { _ in throw APIError(status: 404, message: "Not found") }
        await store.refresh()
        #expect(store.useHerdrTabName == nil)
        #expect(persisted == nil)

        hub.on("GET", "/api/v1/agents/display-settings", json: #"{"show_botmux":true,"configured":false}"#)
        await store.refresh()
        #expect(store.useHerdrTabName == nil)
    }

    @Test func appModelScopesHerdrTabNamePerHubAndExcludesAssistantAndDemo() async throws {
        let folder = FileManager.default.temporaryDirectory.appending(path: "herdr-tab-name-" + UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }

        let hubA = FakeHub()
        hubA.on("GET", "/api/v1/agents/display-settings",
                json: #"{"show_botmux":true,"configured":true,"use_herdr_tab_name":true}"#)
        let hubB = FakeHub()
        hubB.on("GET", "/api/v1/agents/display-settings",
                json: #"{"show_botmux":true,"configured":true,"use_herdr_tab_name":false}"#)

        let model = AppModel(stateFolder: folder)
        model.registry = HubRegistry(hubs: [
            PairedHub(id: "a", config: ServerConfig(url: URL(string: "http://127.0.0.1:1")!, token: "ta"), group: ""),
            PairedHub(id: "b", config: ServerConfig(url: URL(string: "http://127.0.0.1:2")!, token: "tb"), group: "g1"),
        ])
        model.linkHooksForTests = (transport: try #require(hubA.client.transport), socket: { SyncSocket() })
        model.buildLinks()
        model.link("g1")?.transportForTests = hubB.client.transport

        await model.loadAgentsDisplay()
        #expect(model.herdrTabNameGroups == [""])

        let sessionA = try decodeSession()
        var sessionB = try decodeSession()
        sessionB.group = "g1"
        let assistantA = try decodeSession(["assistant": true])

        #expect(model.prefersHerdrTabName(for: sessionA))
        #expect(!model.prefersHerdrTabName(for: sessionB))
        #expect(!model.prefersHerdrTabName(for: assistantA))

        // Demo mode never enables the setting.
        model.connectOnly(.demo)
        #expect(model.herdrTabNameGroups.isEmpty)
        #expect(!model.prefersHerdrTabName(for: sessionA))
    }
}
