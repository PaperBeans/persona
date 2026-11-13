package persona.feature.chat

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons

/**
 * Persona's own MCP tools (server `persona`: `mcp__persona__agent_read`…) on a step: which one is ask_user, and their
 * icons. Their words are `core:domain` `namedToolVerb` (#3100). Display only: the wire keeps `persona`.
 */
internal object PersonaTools {
    /** The server of an MCP tool's full name ("mcp__persona__agent_read" → "persona"), null for any other tool. */
    fun server(name: String): String? {
        if (!name.startsWith("mcp__")) return null
        val rest = name.removePrefix("mcp__")
        val i = rest.lastIndexOf("__")
        return if (i <= 0) null else rest.substring(0, i)
    }

    /** The tool's own name: what follows the last `__` of an MCP tool, the name itself otherwise. */
    fun base(name: String): String = if (name.startsWith("mcp__")) name.substringAfterLast("__") else name

    fun isPersona(name: String): Boolean = server(name) == "persona"

    /** ask_user: the assistant's own question tool (docs/assistant/assistant.md), which takes AskUserQuestion's questions. */
    fun isAskUser(name: String): Boolean = isPersona(name) && base(name) == "ask_user"

    /** The step's icon for a Persona tool (or a conversation / computer / phone / mail one); null for every other tool. */
    fun icon(name: String): PhIcon? {
        if (!name.startsWith("mcp__")) return null
        val n = base(name)
        return when {
            isAskUser(name) -> PhIcons.Question
            n.startsWith("conversation") -> PhIcons.ChatsCircle
            n.startsWith("computer_") -> PhIcons.Desktop
            n.startsWith("browser_") -> PhIcons.GlobeSimple
            n.startsWith("phone_") -> PhIcons.DeviceMobile
            n.startsWith("mail_") -> PhIcons.EnvelopeSimple
            n.startsWith("agent") || n == "list_agents" || n == "send_to_agent" -> PhIcons.Robot
            n.startsWith("kb_") || n == "search_knowledge" -> PhIcons.BookOpen
            n.startsWith("view_") -> PhIcons.SquaresFour
            n.contains("followup") || n.contains("routine") || n == "notify_user" -> PhIcons.BellSimple
            isPersona(name) -> PhIcons.Sparkle
            else -> null
        }
    }
}

/** What an ask_user call asked: each question with its options' labels and whether several could be picked. */
internal data class AskUserQuestion(val question: String, val options: List<String>, val multiSelect: Boolean)

/** The assistant's ask_user, opened (iOS `AskUserAnswers`): what was asked and, from its result, what came back. */
internal object AskUserAnswers {
    fun questions(input: JsonElement?): List<AskUserQuestion> =
        ((input as? JsonObject)?.get("questions") as? JsonArray).orEmpty().mapNotNull { value ->
            val q = value as? JsonObject ?: return@mapNotNull null
            AskUserQuestion(q.text("question").orEmpty(),
                (q["options"] as? JsonArray).orEmpty().mapNotNull { (it as? JsonObject)?.text("label") },
                ((q["multiSelect"] ?: q["multi_select"]) as? JsonPrimitive)?.booleanOrNull == true)
        }

    /** Each question's answer; empty when the result says nobody answered (or is not the hub's answer at all). */
    fun parse(result: String, questions: List<AskUserQuestion>): Map<String, String> {
        if (!result.startsWith("The user answered:")) return emptyMap() // i18n-ignore: the hub's tool result, matched as written
        val marks = questions.mapNotNull { q ->
            val mark = "\n- ${q.question} → "
            result.indexOf(mark).takeIf { it >= 0 }?.let { Triple(q.question, it, it + mark.length) }
        }.sortedBy { it.second }
        return buildMap {
            marks.forEachIndexed { index, (question, _, start) ->
                val end = marks.getOrNull(index + 1)?.second ?: result.length
                result.substring(start, end).trim().takeIf { it.isNotEmpty() }?.let { put(question, it) }
            }
        }
    }

    /** Whether [label] is among what was picked. */
    fun picked(label: String, answer: String?, multiSelect: Boolean): Boolean {
        if (answer == null) return false
        if (answer == label) return true
        return multiSelect && label in answer.split(", ")
    }

    /** An answer typed instead of picking an option. */
    fun typed(q: AskUserQuestion, answer: String?): String? =
        answer?.takeIf { a -> q.options.none { picked(it, a, q.multiSelect) } }
}

private fun JsonObject.text(key: String): String? = (get(key) as? JsonPrimitive)?.contentOrNull
