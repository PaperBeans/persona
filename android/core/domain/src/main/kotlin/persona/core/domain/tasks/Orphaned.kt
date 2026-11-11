package persona.core.domain.tasks

import persona.core.contracts.CoreText
import persona.core.contracts.CoreText.Key
import persona.core.contracts.Todo

// "Nobody is working on this" (#3618, docs/assistant/todos.md "Nobody is working on this"): the hub finds an open to-do
// whose agents are all gone with nothing set to bring one back, and says so on the to-do. Web `orphanedLine`
// (lib/todos.ts).

/**
 * "Nobody is working on this · closed after an error" for an open to-do the hub found orphaned; null for every other.
 * A reason this app does not know leaves the lead alone.
 */
val Todo.orphanedLine: CoreText?
    get() {
        val found = orphaned?.takeIf { !isDone } ?: return null
        val lead = CoreText.of(Key.TodoOrphaned)
        val reason = when (found.reason) {
            "error" -> Key.TodoOrphanedError
            "offline" -> Key.TodoOrphanedOffline
            "closed" -> Key.TodoOrphanedClosed
            else -> return lead
        }
        return CoreText.of(Key.DotJoined, lead, CoreText.of(reason))
    }
