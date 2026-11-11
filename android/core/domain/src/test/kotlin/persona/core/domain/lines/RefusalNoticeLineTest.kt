package persona.core.domain.lines

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.ModelRefusal

/** #2528: an upstream refusal notice is a line of its own after the exchange, never a step inside the work row. */
class RefusalNoticeLineTest {
    private val refusal = ModelRefusal("model_refusal_fallback", "primary-model", "fallback-model", scope = "session", text = "no")
    private fun item(seq: Int, kind: String, text: String? = null, role: String = "assistant", tool: String? = null, refusal: ModelRefusal? = null) =
        LineItem(Item("$seq#0", seq, role = role, kind = kind, text = text, toolUseId = tool, toolName = tool?.let { "Bash" }, modelRefusal = refusal))

    private fun rows(items: List<LineItem>) = chatLines(items).lines

    @Test fun theRefusalIsItsOwnLineAfterTheAnswer() {
        val lines = rows(listOf(item(1, "text", "fix it", "user"), item(2, "tool_use", tool = "t2"),
            item(3, "notice", "no", "system", refusal = refusal), item(4, "tool_use", tool = "t4"), item(5, "text", "done")))
        val ids = lines.map { (it as? ChatLine.Row)?.row?.id }
        assertTrue("the notice is a row: $lines", "3#0" in ids)
        assertTrue("after the answer: $ids", ids.indexOf("3#0") > ids.indexOf("5#0"))
        val work = lines.filterIsInstance<ChatLine.Work>().flatMap { it.steps }
        assertTrue("not folded into the work row: $work", work.none { it.id == "3#0" })
    }

    @Test fun aRefusalWithNoExchangeStillShows() {
        val lines = rows(listOf(item(1, "notice", "no", "system", refusal = refusal)))
        assertEquals(listOf("1#0"), lines.mapNotNull { (it as? ChatLine.Row)?.row?.id })
    }

    @Test fun beforeAnApiErrorReplyToo() {
        // The hub's order (mock MOCK_MODEL_FALLBACK): the message, the refusal notice, then Claude Code's API error.
        val lines = rows(listOf(item(1, "text", "go", "user"), item(2, "notice", "no", "system", refusal = refusal),
            item(3, "text", "API Error: 404 model: fallback-model is not available for this account")))
        assertTrue("$lines", lines.any { it is ChatLine.Row && it.row.id == "2#0" })
        assertTrue("$lines", lines.none { it is ChatLine.Work })
    }
}
