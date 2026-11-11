import Foundation
import Testing
@testable import Persona

struct DispatchedTasksTests {
    private func task(_ title: String, id: String? = nil, error: String? = nil) -> PlanTask {
        PlanTask(title: title, prompt: "Work", machine: "computer", cwd: "/home/user/project", todoID: id, error: error)
    }

    private func plan(_ tasks: [PlanTask], conversation: String = "main", state: TaskPlan.State = .started) -> TaskPlan {
        TaskPlan(id: "plan", conversation: conversation, tasks: tasks, state: state)
    }

    private func feed(_ kind: FeedItem.Kind, ref: String = "plan", body: JSONValue? = nil) -> FeedItem {
        FeedItem(id: "feed", conversation: "main", at: .now, kind: kind, ref: ref, body: body)
    }

    @Test func macDetailSelectionOpensTheRequestedSessionNotAnotherLead() {
        let first = SessionKey(group: "hub", machineID: "computer", sessionID: "first")
        let second = SessionKey(group: "hub", machineID: "computer", sessionID: "second")
        #expect(DispatchedTasks.selectedAgent(first, available: [first, second], lead: second) == first)
        #expect(DispatchedTasks.selectedAgent(second, available: [first, second], lead: first) == second)
    }

    @Test func macDetailSelectionRejectsAStaleOrOtherHubTarget() {
        let current = SessionKey(group: "hub", machineID: "computer", sessionID: "same-id")
        let otherHub = SessionKey(group: "other-hub", machineID: "computer", sessionID: "same-id")
        #expect(DispatchedTasks.selectedAgent(otherHub, available: [current], lead: current) == current)
        #expect(DispatchedTasks.selectedAgent(otherHub, available: [current], lead: otherHub) == nil)
    }

    @Test func macDetailSelectionWithoutAnAgentStaysOnTaskDetails() {
        let missing = SessionKey(machineID: "computer", sessionID: "missing")
        #expect(DispatchedTasks.selectedAgent(missing, available: [], lead: missing) == nil)
    }

    @Test func oneTaskKeepsItsExactTodoTarget() throws {
        let entries = try #require(DispatchedTasks.entries(for: feed(.task, ref: "new"), plans: [:],
                                                          titleOf: { $0 == "new" ? "Current title" : nil }, fallback: "Task"))
        #expect(entries == [.init(id: "todo:new", todoID: "new", title: "Current title")])
    }

    @Test func severalTasksKeepPlanOrderAndLiveTitles() throws {
        let p = plan([task("First", id: "one"), task("Second", id: "two"), task("Failed", error: "Unavailable")])
        let entries = try #require(DispatchedTasks.entries(for: feed(.plan), plans: [p.id: p],
                                                          titleOf: { $0 == "two" ? "Renamed task" : nil }, fallback: "Task"))
        #expect(entries.map(\.title) == ["First", "Renamed task", "Failed"])
        #expect(entries.map(\.todoID) == ["one", "two", nil])
        #expect(entries.map(\.id) == ["plan:plan:0", "plan:plan:1", "plan:plan:2"])
    }

    @Test func planTasksHaveNoDuplicateFeedLink() {
        let p = plan([task("Planned", id: "one")])
        #expect(DispatchedTasks.entries(for: feed(.task, ref: "one"), plans: [p.id: p], titleOf: { _ in nil }, fallback: "Task") == [])
    }

    @Test func anotherConversationDoesNotHideAStandaloneLink() throws {
        let p = plan([task("Elsewhere", id: "one")], conversation: "other")
        let entries = try #require(DispatchedTasks.entries(for: feed(.task, ref: "one"), plans: [p.id: p],
                                                          titleOf: { _ in "Standalone" }, fallback: "Task"))
        #expect(entries.map(\.todoID) == ["one"])
        #expect(DispatchedTasks.entries(for: feed(.plan), plans: [p.id: p], titleOf: { _ in nil }, fallback: "Task") == nil)
    }

    @Test func aMissingTodoRetainsItsTargetUntilItLoads() throws {
        let entries = try #require(DispatchedTasks.entries(for: feed(.task, ref: "loading"), plans: [:], titleOf: { _ in nil }, fallback: "Task"))
        #expect(entries.first?.todoID == "loading")
        #expect(entries.first?.title == "Task")
    }

    @Test func aPlanInTheFeedBodyWorksBeforeItsEventArrives() throws {
        let p = plan([task("Body title", id: "one")])
        let value = try JSON.decoder.decode(JSONValue.self, from: JSONEncoder().encode(p))
        let entries = try #require(DispatchedTasks.entries(for: feed(.plan, body: .object(["plan": value])), plans: [:],
                                                          titleOf: { _ in nil }, fallback: "Task"))
        #expect(entries.map(\.todoID) == ["one"])
    }

    @MainActor @Test func aDispatchDoesNotAbsorbItsRoutedHandoff() {
        let task = feed(.task, ref: "one")
        var routed = feed(.routed, ref: "one", body: .object(["from": .string("persona"), "text": .string("Work instructions")]))
        routed.id = "handoff"
        routed.at = task.at.addingTimeInterval(1)
        let lines = ChatLines.mergeFeed([], [task, routed])
        #expect(lines.count == 2)
        #expect(DispatchedTasks.entries(for: routed, plans: [:], titleOf: { _ in nil }, fallback: "Task") == nil)
    }

    @Test func decisionsAndDismissedPlansKeepTheirNormalCards() {
        for state in [TaskPlan.State.open, .dismissed, .unknown] {
            let p = plan([task("Proposed")], state: state)
            #expect(DispatchedTasks.entries(for: feed(.plan), plans: [p.id: p], titleOf: { _ in nil }, fallback: "Task") == nil)
        }
        #expect(DispatchedTasks.entries(for: feed(.message), plans: [:], titleOf: { _ in nil }, fallback: "Task") == nil)
    }
}
