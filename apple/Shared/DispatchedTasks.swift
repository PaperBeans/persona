import Foundation

/// The dispatch's own links, not the conversation's running-task list. This uses the existing feed contract shared
/// with web and Android (#3718): task.ref, and a started plan's tasks[].todo_id in plan order. No new wire fields.
nonisolated enum DispatchedTasks {
    struct Entry: Identifiable, Equatable, Sendable {
        var id: String
        var todoID: String?
        var title: String
        /// How the Agent came to the task (`task` items' `wake`): created, active or resumed; nil reads as created.
        var wake: String? = nil
        /// Why a plan's task did not start, before there is a to-do to say so.
        var error: String? = nil
    }

    /// The task detail's actual session selection. A requested target must still belong to this task; neither a
    /// stale request nor a stale lead can send the page to another task/hub. Pure so Mac routing is testable unlocked
    /// or locked, independently of the native UI test host.
    static func selectedAgent(_ requested: SessionKey?, available: [SessionKey], lead: SessionKey?) -> SessionKey? {
        if let requested, available.contains(requested) { return requested }
        if let lead, available.contains(lead) { return lead }
        return nil
    }

    /// nil means this is not a dispatch (keep its normal card); [] means a task already named by its plan.
    static func entries(for item: FeedItem, plans: [String: TaskPlan], titleOf: (String) -> String?, fallback: String) -> [Entry]? {
        switch item.kind {
        case .plan:
            guard let ref = item.ref, let plan = plans[ref] ?? item.plan,
                  plan.conversation == item.conversation, plan.state == .started else { return nil }
            return plan.tasks.enumerated().map { index, task in
                Entry(id: "plan:\(plan.id):\(index)", todoID: task.todoID,
                      title: task.todoID.flatMap(titleOf) ?? task.title, error: task.error)
            }
        case .task:
            guard let id = item.ref else { return [] }
            guard !plans.values.contains(where: { plan in
                plan.conversation == item.conversation && plan.tasks.contains(where: { $0.todoID == id })
            }) else { return [] }
            return [Entry(id: "todo:" + id, todoID: id, title: titleOf(id) ?? fallback, wake: item.wake)]
        default: return nil
        }
    }
}
