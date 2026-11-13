package persona.core.domain.lines

import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.ImagePayload
import persona.core.contracts.Item

/**
 * Where the chat marks that the replies' model changed ([ModelChanges], the web's `lib/model-change.test.ts`, #5055):
 * after the message that started the exchange, only between two known, different reply models, never inside an exchange.
 */
class ModelChangesTest {
    private var seq = 0
    private fun user(text: String = "go") = LineItem(Item("${++seq}#0", seq, role = "user", kind = "text", text = text))
    private fun answer(model: String?, text: String = "done") = LineItem(Item("${++seq}#0", seq, role = "assistant", kind = "text", text = text, model = model))
    private fun tool(model: String?) = LineItem(Item("${++seq}#0", seq, role = "assistant", kind = "tool_use", toolUseId = "t$seq", toolName = "Read",
        toolInput = buildJsonObject { put("file_path", JsonPrimitive("/a.kt")) }, model = model))
    private fun result(of: LineItem) = LineItem(Item("${++seq}#0", seq, role = "user", kind = "tool_result", toolUseId = of.item.toolUseId, text = "ok"))
    /** A message with a photo, as a transcript has it: its words, then the picture (the chat draws the photo first). */
    private fun withPhoto(text: String): List<LineItem> = listOf(user(text),
        LineItem(Item("${++seq}#0", seq, role = "user", kind = "image", image = ImagePayload(mime = "image/png", data = "aGVsbG8="))))

    private fun marked(vararg items: LineItem) = ModelChanges.mark(chatLines(items.toList()).lines)

    /** The lines as words: user, answer, work, photos, and "model a → b" for a divider. */
    private fun kinds(lines: List<ChatLine>) = lines.map { line ->
        when {
            line is ChatLine.ModelChange -> "model ${line.from} → ${line.to}"
            line is ChatLine.Row && line.row.type == "user" -> "user"
            line is ChatLine.Row && line.row.type == "reply" -> "answer"
            line is ChatLine.Work -> "work"
            line is ChatLine.UserImages -> "photos"
            else -> "other"
        }
    }

    @Test fun theDividerFollowsTheMessageThatStartedTheExchange() {
        val lines = marked(user(), answer("claude-opus-5-5"), user(), answer("claude-sonnet-4-5-20250929"))
        assertEquals(listOf("user", "answer", "user", "model claude-opus-5-5 → claude-sonnet-4-5-20250929", "answer"), kinds(lines))
        val change = lines.filterIsInstance<ChatLine.ModelChange>().single()
        assertEquals("Opus 5.5", ModelChanges.replyModelName(change.from))
        assertEquals("Sonnet 4.5", ModelChanges.replyModelName(change.to))
    }

    @Test fun theSameModelWrittenAnotherWayIsNoChange() {
        val lines = marked(user(), answer("claude-opus-5-5"), user(), answer("us.anthropic.claude-opus-5-5-v1:0"),
            user(), answer("vendor/claude-opus-5-5@20260101"), user(), answer("CLAUDE-OPUS-5-5"))
        assertEquals(0, lines.count { it is ChatLine.ModelChange })
    }

    @Test fun aContextSizeIsAnotherModel() {
        val lines = marked(user(), answer("claude-opus-5-5"), user(), answer("claude-opus-5-5[1m]"))
        assertEquals("Opus 5.5 1M", ModelChanges.replyModelName(lines.filterIsInstance<ChatLine.ModelChange>().single().to))
    }

    @Test fun anUnknownModelNeitherMarksNorEndsTheRun() {
        // the middle exchange says nothing of its model: the third is compared with the first
        val lines = marked(user(), answer("glm-5.3"), user(), answer(null), user(), answer("glm-5.3"), user(), answer("gpt-5.5"))
        assertEquals(listOf("user", "answer", "user", "answer", "user", "answer", "user", "model glm-5.3 → gpt-5.5", "answer"), kinds(lines))
    }

    @Test fun nothingGoesInsideAnExchangeAndItsLatestStepCounts() {
        // a fallback model did work after the answer's words: the exchange's model is the latest one, and the next
        // exchange on that model is no change
        val first = user()
        val words = answer("claude-opus-5-5", "trying")
        val call = tool("glm-5.3")
        val back = result(call)
        val second = user()
        val reply = answer("glm-5.3")
        val lines = marked(first, words, call, back, second, reply)
        assertEquals(0, lines.count { it is ChatLine.ModelChange })
        // and against an exchange on Opus, the change shows once, after the next message
        val third = user()
        val opus = answer("claude-opus-5-5")
        val again = marked(first, words, call, back, second, reply, third, opus)
        val words2 = kinds(again)
        assertEquals(1, again.count { it is ChatLine.ModelChange })
        assertEquals("model glm-5.3 → claude-opus-5-5", words2[words2.lastIndexOf("user") + 1])
    }

    @Test fun aToolCallsModelCountsWhenNoWordsCame() {
        val first = user()
        val opus = answer("claude-opus-5-5")
        val second = user()
        val call = tool("glm-5.3")
        val lines = marked(first, opus, second, call, result(call))
        assertEquals(listOf("user", "answer", "user", "model claude-opus-5-5 → glm-5.3", "work"), kinds(lines))
    }

    @Test fun photosSentWithTheMessageStayBeforeTheDivider() {
        val first = user()
        val opus = answer("claude-opus-5-5")
        val message = withPhoto("look")
        val lines = marked(first, opus, *message.toTypedArray(), answer("glm-5.3"))
        assertEquals(listOf("user", "answer", "photos", "user", "model claude-opus-5-5 → glm-5.3", "answer"), kinds(lines))
    }

    @Test fun anExchangeNoMessageStartedGetsItAtItsStart() {
        // the chat's page starts inside an exchange: its first lines have no message of the user's
        val lines = ModelChanges.mark(chatLines(listOf(answer("claude-opus-5-5"), user(), answer("glm-5.3"))).lines)
        assertEquals(listOf("answer", "user", "model claude-opus-5-5 → glm-5.3", "answer"), kinds(lines))
        val head = ModelChanges.mark(listOf<ChatLine>(ChatLine.Row(ChatRow("reply", answer("a"))), ChatLine.Row(ChatRow("reply", answer("b")))))
        assertEquals("one exchange: no divider inside it", 0, head.count { it is ChatLine.ModelChange })
    }

    @Test fun eachChangeGetsItsOwnDividerWithAStableId() {
        val first = user()
        val a = answer("a-model")
        val second = user()
        val lines = marked(first, a, second, answer("b-model"), user(), answer("a-model"))
        val changes = lines.filterIsInstance<ChatLine.ModelChange>()
        assertEquals(listOf("a-model" to "b-model", "b-model" to "a-model"), changes.map { it.from to it.to })
        assertEquals("mc-" + second.id, changes.first().id)
    }

    @Test fun namesAsTheChatSaysThem() {
        assertEquals("Sonnet 4.5", ModelChanges.replyModelName("claude-sonnet-4-5-20250929"))
        assertEquals("Haiku 4.5", ModelChanges.replyModelName("us.anthropic.claude-haiku-4-5-20251001-v1:0"))
        assertEquals("glm-5.1", ModelChanges.replyModelName("vendor/glm-5.1"))
        assertEquals("deepseek-v3", ModelChanges.bareModel("deepseek-v3"))
        assertEquals("claude-opus-5-5[1m]", ModelChanges.bareModel("claude-opus-5-5-20260101[1m]"))
    }
}
