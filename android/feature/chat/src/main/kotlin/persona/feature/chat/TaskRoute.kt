package persona.feature.chat

import persona.core.contracts.Todo
import persona.core.domain.tasks.TaskState
import java.time.Instant

/**
 * The tasks the @ menu offers (iOS `TaskMentionList`): the conversation's own, newest first, without the done and the
 * failed ones (nothing to say a word to there).
 */
internal fun mentionTasks(todos: List<Todo>, conversation: String, state: (Todo) -> TaskState): List<Pair<Todo, TaskState>> =
    todos.filter { it.origin == conversation }.sortedByDescending { it.created ?: Instant.MIN }
        .map { it to state(it) }.filter { (_, s) -> s != TaskState.Done && s != TaskState.Failed }

/** The task the conversation's next message goes to: none without a route, or while its to-do is not listed (iOS `RouteChip`). */
internal fun routedTask(routes: Map<String, String>, todos: List<Todo>, conversation: String): Todo? =
    routes[conversation]?.let { id -> todos.firstOrNull { it.id == id } }
