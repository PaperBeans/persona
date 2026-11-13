package persona.core.domain.lines

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import persona.core.contracts.Item
import persona.core.contracts.Subagent
import java.time.Duration
import java.time.Instant
import java.util.Locale

/** A subagent's status word on its task card (the web's `AgentStatus`): Running, Completed, Failed or Stopped. */
enum class SubagentStatus { Running, Completed, Failed, Stopped }

/**
 * A subagent the agent launched, as its task card reads it (`docs/design/DESIGN_SYSTEM.md` section 4.7, "Task cards"; the
 * web's `lib/tasks.ts` and `SubagentCard`), from its Agent / Task call and the call's deliveries
 * (`docs/protocol/protocol.md`, "Subagent deliveries as tool results"). [agent] is the session's listing of it
 * (`subagents`, found by [callOf]), null when the session does not list it.
 */
object Subagents {
    /** The id prefix of a card for a subagent another subagent launched: its call is in that subagent's transcript. */
    const val Nested = "subagent:"

    /** An Agent / Task call: drawn as its subagent's card. */
    fun isAgent(call: ToolCall): Boolean = call.name == "Agent" || call.name == "Task"

    /** A card [withNested] added for a subagent of a subagent. */
    fun isNested(call: ToolCall): Boolean = call.use.id.startsWith(Nested)

    /** The call a listed subagent is found by: its own call, else (nested, no call in this chat) its id (web `subagentCall`). */
    fun callOf(agent: Subagent): String = agent.toolUseId?.takeIf(String::isNotBlank) ?: Nested + agent.id

    private val launchReceipt = Regex("""^\s*(?:Async agent launched|This is an internal ID for your use)""")
    private val agentIdLine = Regex("""(?m)^\s*agentId:.*$""")
    /** The frame Claude Code puts before a report a subagent hands back with SendMessage. */
    private const val HandbackFrame = "[Subagent hand-back]"

    /** What a call returned when it only started its subagent in the background (web `isLaunchReceipt`). */
    fun isLaunchReceipt(item: LineItem?): Boolean {
        val task = item?.task
        if (task?.kind == "agent" && (!task.detail.isNullOrEmpty() || noteStatus(task) != SubagentStatus.Running)) return false
        return launchReceipt.containsMatchIn(item?.text.orEmpty())
    }

    /** The call's own result: its latest `tool_result` (the receipt, an interim report or the final one). */
    fun result(call: ToolCall): LineItem? = call.results.lastOrNull { it.kind == "tool_result" } ?: call.result?.takeIf { it.kind == "tool_result" }

    private fun noteStatus(note: TaskNote?): SubagentStatus? {
        if (note?.kind != "agent") return null
        if (note.failed) return SubagentStatus.Failed
        return when (note.status) {
            "running", "waiting", "event" -> SubagentStatus.Running
            "completed", "ended" -> SubagentStatus.Completed
            "killed", "stopped" -> SubagentStatus.Stopped
            else -> null
        }
    }

    /**
     * How it is going (web `agentStatus`): a failed result fails; its deliveries' latest word; no result yet runs while
     * the exchange is [live] or the list says it does, else stopped; a result that is not the launch receipt completed; a
     * background one is the list's to tell. A nested one only the list knows.
     */
    fun status(call: ToolCall, agent: Subagent?, live: Boolean): SubagentStatus {
        if (isNested(call)) return if (agent?.running == true) SubagentStatus.Running else SubagentStatus.Completed
        val result = result(call)
        if (result?.item?.isError == true) return SubagentStatus.Failed
        (noteStatus(call.results.lastOrNull { it.task != null }?.task) ?: noteStatus(result?.task))?.let { return it }
        if (result == null) return if (live || agent?.running == true) SubagentStatus.Running else SubagentStatus.Stopped
        if (!isLaunchReceipt(result)) return SubagentStatus.Completed
        if (agent != null) return if (agent.running) SubagentStatus.Running else SubagentStatus.Completed
        return if (live) SubagentStatus.Running else SubagentStatus.Completed
    }

    /** What it reported, oldest first: each delivery with words that is not its launch receipt (web `TaskIndex.reports`). */
    fun reports(call: ToolCall): List<LineItem> = call.results.filter { item ->
        !isLaunchReceipt(item) && (if (item.kind == "tool_result") item.text else item.task?.detail)?.isNotBlank() == true
    }

    /** The delivery its result is read from: its latest report, else the call's result when that is not the receipt. */
    fun resultItem(call: ToolCall): LineItem? = reports(call).lastOrNull() ?: result(call)?.takeUnless(::isLaunchReceipt)

    /**
     * Its latest result as plain text (web `SubagentResult`): a returned report without Claude Code's `agentId` line and
     * hand-back frame, a notice's words; with none, once it is over, what the session last heard from it.
     */
    fun resultText(call: ToolCall, agent: Subagent?, status: SubagentStatus): String {
        val item = resultItem(call)
        val said = when {
            item == null -> if (status == SubagentStatus.Running) "" else summary(result(call), agent)
            item.kind == "tool_result" -> summary(item, null)
            else -> item.task?.detail.orEmpty().trim()
        }
        return if (said.trimStart().startsWith(HandbackFrame)) said.lines().drop(1).joinToString("\n").trim() else said
    }

    private fun summary(result: LineItem?, agent: Subagent?): String {
        val text = if (isLaunchReceipt(result)) "" else result?.text.orEmpty().replace(agentIdLine, "").trim()
        return text.ifEmpty { agent?.lastText?.trim().orEmpty() }
    }

    /** Where it wrote its output, as its latest notice names it. */
    fun outputFile(call: ToolCall): String? = call.results.mapNotNull { it.task?.outputFile }.lastOrNull { it.isNotBlank() }

    /** Its title: the call's description, else the listing's, else its prompt; one line of at most 96 characters; null without any. */
    fun title(call: ToolCall, agent: Subagent?): String? {
        val input = call.use.item.toolInput as? JsonObject
        val listed = agent?.description?.takeIf(String::isNotBlank) ?: agent?.type?.takeIf(String::isNotBlank)
        return compact(input.text("description") ?: listed ?: input.text("prompt"), 96).ifEmpty { null }
    }

    /** What it was asked, one line of at most 260 characters. */
    fun prompt(call: ToolCall): String {
        val input = call.use.item.toolInput as? JsonObject
        return compact(input.text("prompt") ?: input.text("description"), 260)
    }

    /** When it started: the listing's start, else its call's. */
    fun started(call: ToolCall, agent: Subagent?): Instant? = time(agent?.startedAt) ?: call.use.time

    /** When it ended (web `subagentEnded`): a background one's last line, else its result. */
    fun ended(call: ToolCall, agent: Subagent?): Instant? {
        val result = result(call)
        return if (isLaunchReceipt(result)) time(agent?.updatedAt) ?: result?.time else result?.time ?: time(agent?.updatedAt)
    }

    /** How long it took, in seconds; null while it runs ("Running for …" counts instead) or when it is not known. */
    fun took(call: ToolCall, agent: Subagent?, status: SubagentStatus): Long? {
        if (status == SubagentStatus.Running) return null
        val start = started(call, agent) ?: return null
        val end = ended(call, agent) ?: return null
        return Duration.between(start, end).seconds.takeIf { it >= 0 }?.coerceAtLeast(1)
    }

    /** Whitespace runs as one space, at most [max] characters, an ellipsis when cut (web `compactText`). */
    fun compact(text: String?, max: Int): String {
        val chars = text.orEmpty().replace(Regex("""\s+"""), " ").trim().codePoints().toArray()
        return if (chars.size <= max) String(chars, 0, chars.size) else String(chars, 0, max - 1) + "…"
    }

    /** "23.4k", "950", "1.2M": tokens as every task card writes them, in every language (web `compactTokens`). */
    fun tokens(count: Long): String {
        val v = count.coerceAtLeast(0)
        if (v < 1000) return v.toString()
        for ((div, unit) in listOf(1e3 to "k", 1e6 to "M", 1e9 to "B")) {
            if (v / div < 999.95 || unit == "B") return String.format(Locale.ROOT, "%.1f%s", v / div, unit)
        }
        return v.toString()
    }

    /**
     * An open work line's lines with the subagents its subagents launched (and theirs), each a card of its own, flat, by when
     * it started (web `withNestedSubagents`): after the last step of the line that started before it, never before the call
     * of the subagent it descends from. A launcher naming itself or a loop adds nothing.
     */
    fun withNested(lines: List<ChatLine>, agents: List<Subagent>): List<ChatLine> {
        if (agents.none { !it.parentId.isNullOrBlank() }) return lines
        val kids = agents.filter { !it.parentId.isNullOrBlank() && it.parentId != it.id }.groupBy { it.parentId }
        val byCall = agents.associateBy(::callOf)
        val out = lines.toMutableList()
        val seen = mutableSetOf<String>()
        for (line in lines) {
            val step = line as? ChatLine.Step ?: continue
            val call = (step.step as? WorkItem.Tool)?.call?.takeIf(::isAgent) ?: continue
            val root = call.use.item.toolUseId?.let(byCall::get) ?: continue
            seen += root.id
            val from = out.indexOf(step)
            val queue = ArrayDeque(kids[root.id].orEmpty())
            while (queue.isNotEmpty()) {
                val kid = queue.removeFirst()
                if (!seen.add(kid.id)) continue
                queue.addAll(kids[kid.id].orEmpty())
                val at = time(kid.startedAt)
                var i = from + 1
                while (i < out.size) {
                    val next = out[i] as? ChatLine.Step ?: break
                    if (next.groupId != step.groupId) break
                    val ts = next.step.start ?: break
                    if (at == null || ts > at) break
                    i++
                }
                val before = (out[i - 1] as ChatLine.Step).step
                out.add(i, ChatLine.Step(step.groupId, WorkItem.Tool(nestedCall(kid, before)), false))
            }
        }
        return out
    }

    private fun nestedCall(agent: Subagent, before: WorkItem): ToolCall {
        val seq = when (before) {
            is WorkItem.Tool -> before.call.use.seq
            is WorkItem.Thought -> before.item.seq
            is WorkItem.Folded -> before.item.seq
            is WorkItem.Narration -> before.item.seq
        }
        val use = Item(Nested + agent.id, seq = seq, ts = agent.startedAt, role = "assistant", kind = "tool_use", toolName = "Agent",
            toolUseId = callOf(agent), toolInput = buildJsonObject { put("description", agent.description.orEmpty()) })
        return ToolCall(LineItem(use), null, emptyList())
    }

    private fun JsonObject?.text(key: String): String? =
        (this?.get(key) as? JsonPrimitive)?.takeIf { it.isString }?.content?.takeIf(String::isNotBlank)

    private fun time(value: String?): Instant? = value?.let { runCatching { Instant.parse(it) }.getOrNull() }
}
