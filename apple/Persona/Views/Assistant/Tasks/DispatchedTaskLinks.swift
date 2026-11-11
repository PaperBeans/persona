import SwiftUI

/// One dispatch, one set of task links. A plan names only its own tasks, each independently openable. Titles come
/// from the live to-do when loaded; a failed plan slot without a to-do stays plain text, with no invented target.
struct DispatchedTaskLinks: View {
    var tasks: [DispatchedTasks.Entry]
    private var store: TasksStore { .shared }

    var body: some View {
        if !tasks.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                if tasks.count > 1 {
                    Text("Dispatched \(tasks.count)")
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                }
                ForEach(tasks) { task in
                    if let id = task.todoID {
                        TaskOpenButton(title: tasks.count == 1 ? String(localized: "Dispatched: \(task.title)") : task.title,
                                       shortTitle: task.title) {
                            open(id)
                        }
                        .accessibilityIdentifier("dispatch.task.\(id)")
                    } else {
                        Text(task.title)
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .accessibilityIdentifier("dispatch.unstarted")
                    }
                }
            }
        }
    }

    private func open(_ id: String) {
        Haptics.tap()
        let agent = store.todos[id].flatMap { store.leadSession($0)?.key }
        #if os(iOS)
        store.openDispatchedTask(id, agent: agent, pushAgent: true)
        #else
        store.openDispatchedTask(id, agent: agent, pushAgent: false)
        #endif
    }
}
