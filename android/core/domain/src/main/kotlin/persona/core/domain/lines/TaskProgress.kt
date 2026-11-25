package persona.core.domain.lines

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.stripToolResultTimestampHeaders
import kotlinx.serialization.json.*

enum class TaskProgressStatus { Pending, InProgress, Completed, Unknown }

data class TaskProgressItem(
    val id: String,
    val subject: CoreText,
    val description: String?,
    val status: TaskProgressStatus,
    val sourceItemId: String,
    val sourceSeq: Int? = null,
)

/** Replay successful results, never unconfirmed tool inputs, in transcript order. */
internal fun rebuildTaskProgress(items: List<LineItem>): List<TaskProgressItem> {
    val uses = mutableMapOf<String, LineItem>()
    val tasks = linkedMapOf<String, TaskProgressItem>()
    for (item in items.sortedWith(itemOrder)) {
        if (item.kind == "fold") {
            for (snapshot in item.item.fold?.tasks.orEmpty()) {
                when (snapshot.replace) {
                    "todo" -> tasks.keys.removeAll { it.startsWith("todo:") }
                    "task" -> {
                        val ids = snapshot.entries.map { it.id }.toSet()
                        tasks.keys.removeAll { it.startsWith("task:") && it !in ids }
                    }
                }
                for (entry in snapshot.entries) {
                    if (entry.status == "deleted") { tasks.remove(entry.id); continue }
                    val previous = tasks[entry.id]
                    tasks[entry.id] = TaskProgressItem(entry.id,
                        entry.subject?.let(CoreText::plain) ?: previous?.subject ?: CoreText.of(Key.TaskNumber, entry.id.removePrefix("task:")),
                        entry.description ?: previous?.description,
                        entry.status?.let(::progressStatus) ?: previous?.status ?: progressStatus(snapshot.fallbackStatus),
                        snapshot.sourceItemId, snapshot.sourceSeq)
                }
            }
            continue
        }
        val callId = item.item.toolUseId ?: continue
        if (item.kind == "tool_use") { uses[callId] = item; continue }
        if (item.kind != "tool_result" || item.item.isError == true) continue
        val use = uses[callId] ?: continue
        // Only these tools change the list (the `when` below): any other result, a file or shell output, is not parsed.
        if (use.item.toolName !in progressTools) continue
        val input = use.item.toolInput as? JsonObject ?: JsonObject(emptyMap())
        val output = stripToolResultTimestampHeaders(item.text.orEmpty()).trim()
        val result = runCatching { Json.parseToJsonElement(output) }.getOrNull()
        val obj = result as? JsonObject
        if (obj?.get("success") == JsonPrimitive(false) || obj?.get("error")?.let { it != JsonNull } == true) continue
        fun put(id: String, fields: JsonObject, fallbackStatus: TaskProgressStatus = TaskProgressStatus.Unknown) {
            val key = "task:$id"
            if (fields.string("status") == "deleted") { tasks.remove(key); return }
            val previous = tasks[key]
            tasks[key] = TaskProgressItem(key,
                fields.string("subject")?.let(CoreText::plain) ?: previous?.subject ?: CoreText.of(Key.TaskNumber, id),
                fields.string("description") ?: previous?.description,
                fields.string("status")?.let(::progressStatus) ?: previous?.status ?: fallbackStatus,
                use.id, use.seq)
        }
        when (use.item.toolName) {
            "TodoWrite" -> {
                val todos = (obj?.get("todos") ?: input["todos"]) as? JsonArray ?: continue
                val parsed = todos.mapIndexed { index, raw ->
                    val todo = raw as? JsonObject ?: return@mapIndexed null
                    val subject = todo.string("content")?.takeIf(String::isNotBlank) ?: return@mapIndexed null
                    TaskProgressItem("todo:$index", CoreText.plain(subject), todo.string("activeForm"),
                        progressStatus(todo.string("status")), use.id, use.seq)
                }
                // A malformed snapshot must not erase previously confirmed tasks.
                if (parsed.any { it == null }) continue
                tasks.keys.removeAll { it.startsWith("todo:") }
                parsed.filterNotNull().forEach { tasks[it.id] = it }
            }
            "TaskCreate" -> {
                val fields = obj?.get("task") as? JsonObject ?: obj
                val id = fields?.string("id") ?: fields?.string("taskId")
                    ?: createdTask.find(output)?.groupValues?.get(1) ?: continue
                put(id, JsonObject(input + fields.orEmpty()), TaskProgressStatus.Pending)
            }
            "TaskUpdate" -> {
                val fields = obj?.get("task") as? JsonObject ?: obj
                val id = fields?.string("id") ?: fields?.string("taskId") ?: input.string("taskId") ?: continue
                put(id, JsonObject(input + fields.orEmpty()))
            }
            "TaskList" -> {
                val array = (obj?.get("tasks") ?: result) as? JsonArray
                val entries = if (array != null) array.map { raw ->
                    (raw as? JsonObject)?.takeIf { it.string("id") != null && it.string("subject") != null }
                } else if (output == "No tasks found" || output == "No tasks found.") emptyList() else {
                    output.lineSequence().filter(String::isNotBlank).map { line ->
                        listedTask.matchEntire(line.trim())?.let { match -> buildJsonObject {
                            put("id", match.groupValues[1]); put("status", match.groupValues[2]); put("subject", match.groupValues[3])
                        } }
                    }.toList().takeIf { it.isNotEmpty() } ?: continue
                }
                if (entries.any { it == null }) continue
                val ids = entries.filterNotNull().map { "task:${it.string("id")}" }.toSet()
                tasks.keys.removeAll { it.startsWith("task:") && it !in ids }
                entries.filterNotNull().forEach { put(it.string("id")!!, it) }
            }
        }
    }
    return tasks.values.toList()
}

private fun JsonObject.string(key: String): String? = (get(key) as? JsonPrimitive)?.contentOrNull
private fun progressStatus(value: String?): TaskProgressStatus = when (value) {
    "pending" -> TaskProgressStatus.Pending
    "in_progress" -> TaskProgressStatus.InProgress
    "completed" -> TaskProgressStatus.Completed
    else -> TaskProgressStatus.Unknown
}

private val progressTools = setOf("TodoWrite", "TaskCreate", "TaskUpdate", "TaskList")
private val createdTask = Regex("Task #([A-Za-z0-9_-]+) created successfully")
private val listedTask = Regex("#([A-Za-z0-9_-]+) \\[(pending|in_progress|completed)\\] (.+)")
