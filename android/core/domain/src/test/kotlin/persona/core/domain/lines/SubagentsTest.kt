package persona.core.domain.lines

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.Subagent

/** A subagent's task card reads as the web's (`lib/tasks.ts`: `agentStatus`, `agentSummary`, `withNestedSubagents`; the model chip is `ModelNamesTest`'s). */
class SubagentsTest {
    private fun use(seq: Int, id: String, name: String = "Agent", at: String = "2026-11-12T10:00:00Z",
        input: String = """{"description":"Check $id","prompt":"Look at $id"}""") = LineItem(Item("u$seq", seq = seq,
        kind = "tool_use", role = "assistant", toolName = name, toolUseId = id, ts = at, toolInput = Json.parseToJsonElement(input)))
    private fun result(seq: Int, id: String, text: String? = "ok", status: String? = null, error: Boolean = false,
        at: String = "2026-11-12T10:01:20Z", detail: String? = null, output: String? = null, full: Int? = null) =
        LineItem(Item("r$seq", seq = seq, kind = "tool_result", role = "user", toolUseId = id, text = text, isError = error, ts = at, fullSize = full),
            task = status?.let { TaskNote("agent", "Check $id", it, toolUseId = id, detail = detail, outputFile = output) })
    private fun notice(seq: Int, id: String, status: String, detail: String? = null) = LineItem(Item("n$seq", seq = seq,
        kind = "notice", text = "x"), task = TaskNote("agent", "Check $id", status, toolUseId = id, detail = detail))
    private fun call(use: LineItem, vararg results: LineItem) = ToolCall(use, results.lastOrNull(), emptyList(), results.toList())
    private val receipt = "Async agent launched successfully.\nagentId: a1"
    private fun agent(running: Boolean = false, id: String = "a1", call: String? = "a", parent: String? = null, started: String? = null) =
        Subagent(id, "s@$id", "Explore", "Listed $id", call, running = running, startedAt = started, parentId = parent)

    @Test fun aCallWithoutAResultRunsWhileItsExchangeDoesOrTheListSaysSo() {
        val c = call(use(1, "a"))
        assertEquals(SubagentStatus.Running, Subagents.status(c, null, live = true))
        assertEquals(SubagentStatus.Running, Subagents.status(c, agent(running = true), live = false))
        assertEquals(SubagentStatus.Stopped, Subagents.status(c, agent(), live = false))
        assertNull(Subagents.took(c, null, SubagentStatus.Running))
    }

    @Test fun aResultCompletesItAndAnErrorFailsIt() {
        val done = call(use(1, "a"), result(2, "a", "All good\nagentId: a1"))
        assertEquals(SubagentStatus.Completed, Subagents.status(done, null, live = true))
        assertEquals("All good", Subagents.resultText(done, null, SubagentStatus.Completed))
        assertEquals(80L, Subagents.took(done, null, SubagentStatus.Completed))
        val failed = call(use(1, "b"), result(2, "b", "boom", error = true))
        assertEquals(SubagentStatus.Failed, Subagents.status(failed, null, live = true))
    }

    @Test fun deliveriesSayHowItGoesAndAReceiptIsNeverItsResult() {
        val launched = call(use(1, "a"), result(2, "a", receipt, "running"))
        assertTrue(Subagents.isLaunchReceipt(launched.results.single()))
        assertEquals(SubagentStatus.Running, Subagents.status(launched, null, live = false))
        assertEquals("", Subagents.resultText(launched, null, SubagentStatus.Running))
        val interim = call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", "Half way", "running"))
        assertEquals(SubagentStatus.Running, Subagents.status(interim, null, live = false))
        assertEquals("Half way", Subagents.resultText(interim, null, SubagentStatus.Running))
        val ended = call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", "Half way", "running"), result(4, "a", null, "completed"))
        assertEquals(SubagentStatus.Completed, Subagents.status(ended, null, live = true))
        assertEquals("Half way", Subagents.resultText(ended, null, SubagentStatus.Completed)) // a status-only pointer adds no report
        assertEquals(SubagentStatus.Stopped, Subagents.status(call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", null, "killed")), null, true))
        assertEquals(SubagentStatus.Failed, Subagents.status(call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", "x", "failed")), null, true))
    }

    @Test fun aBackgroundOneIsTheListsToTellAndAnOlderHubsNoticeCarriesItsReport() {
        val waiting = call(use(1, "a"), result(2, "a", receipt))
        assertEquals(SubagentStatus.Running, Subagents.status(waiting, agent(running = true), live = false))
        assertEquals(SubagentStatus.Completed, Subagents.status(waiting, agent(), live = true))
        assertEquals(SubagentStatus.Running, Subagents.status(waiting, null, live = true))
        val reported = call(use(1, "a"), result(2, "a", receipt), notice(3, "a", "completed", "[Subagent hand-back]\nThe map is done."))
        assertEquals(SubagentStatus.Completed, Subagents.status(reported, null, live = false))
        assertEquals("The map is done.", Subagents.resultText(reported, null, SubagentStatus.Completed))
        // With nothing reported, what the session last heard from it.
        val quiet = call(use(1, "a"), result(2, "a", receipt), notice(3, "a", "completed"))
        assertEquals("Its last words", Subagents.resultText(quiet, agent().copy(lastText = " Its last words "), SubagentStatus.Completed))
    }

    @Test fun itsTimeRunsFromItsStartToItsResultOrABackgroundOnesLastLine() {
        val background = call(use(1, "a"), result(2, "a", receipt, at = "2026-11-12T10:00:01Z"), notice(3, "a", "completed"))
        val listed = agent().copy(startedAt = "2026-11-12T10:00:00Z", updatedAt = "2026-11-12T10:02:05Z")
        assertEquals(125L, Subagents.took(background, listed, SubagentStatus.Completed))
        assertEquals(1L, Subagents.took(call(use(1, "a"), result(2, "a", at = "2026-11-12T10:00:00Z")), null, SubagentStatus.Completed))
    }

    @Test fun titlePromptOutputAndTheCompactText() {
        val c = call(use(1, "a", input = """{"prompt":"  Read   every\nfile  "}"""), result(2, "a", "ok", "completed", output = "/tmp/out.txt"))
        assertEquals("Read every file", Subagents.title(c, null))
        assertEquals("Listed a1", Subagents.title(call(use(1, "a", input = "{}")), agent()))
        assertNull(Subagents.title(call(use(1, "a", input = "{}")), null))
        assertEquals("Read every file", Subagents.prompt(c))
        assertEquals("/tmp/out.txt", Subagents.outputFile(c))
        assertEquals("abcd…", Subagents.compact("abcdefgh", 5))
    }

    @Test fun tokensReadAsTheWebWritesThem() {
        assertEquals("950", Subagents.tokens(950))
        assertEquals("23.4k", Subagents.tokens(23_400))
        assertEquals("1.2M", Subagents.tokens(1_200_000))
    }

    @Test fun nestedSubagentsAreFlatCardsByWhenTheyStarted() {
        val root = WorkItem.Tool(call(use(1, "a", at = "2026-11-12T10:00:00Z")))
        val read = WorkItem.Tool(call(use(2, "r", "Read", at = "2026-11-12T10:00:30Z")))
        val bash = WorkItem.Tool(call(use(3, "b", "Bash", at = "2026-11-12T10:01:30Z")))
        val lines = listOf(ChatLine.Work("w", listOf(root, read, bash), true, null, null, true),
            ChatLine.Step("w", root, true), ChatLine.Step("w", read, false), ChatLine.Step("w", bash, false), ChatLine.End("w", 3))
        val agents = listOf(agent(running = true, started = "2026-11-12T10:00:00Z"),
            agent(running = true, id = "k1", call = null, parent = "a1", started = "2026-11-12T10:01:00Z"),
            agent(id = "k2", call = "kc", parent = "k1", started = "2026-11-12T10:02:00Z"),
            agent(id = "loop", call = null, parent = "loop", started = "2026-11-12T10:00:10Z"))
        val out = Subagents.withNested(lines, agents)
        assertEquals(listOf("w", "w/u1", "w/u2", "w/subagent:k1", "w/u3", "w/subagent:k2", "w-end"), out.map { it.id })
        val k1 = ((out[3] as ChatLine.Step).step as WorkItem.Tool).call
        assertTrue(Subagents.isNested(k1))
        assertEquals("subagent:k1", k1.use.item.toolUseId)
        assertEquals("subagent:k1", Subagents.callOf(agents[1]))
        assertEquals(SubagentStatus.Running, Subagents.status(k1, agents[1], live = false))
        assertEquals(SubagentStatus.Completed, Subagents.status(k1, agents[1].copy(running = false), live = true))
        assertEquals("kc", (((out[5] as ChatLine.Step).step as WorkItem.Tool).call).use.item.toolUseId)
        // No subagent of a subagent: the lines as they were.
        assertEquals(lines, Subagents.withNested(lines, agents.take(1)))
        assertFalse(Subagents.isNested(((out[1] as ChatLine.Step).step as WorkItem.Tool).call))
    }
}
