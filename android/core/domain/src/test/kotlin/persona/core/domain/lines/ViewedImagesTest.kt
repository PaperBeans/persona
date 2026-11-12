package persona.core.domain.lines

import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Fold as WireFold
import persona.core.contracts.FoldImage
import persona.core.contracts.ImagePayload
import persona.core.contracts.Item

/**
 * The images a turn's work looked at ([ViewedImages], docs/protocol/protocol.md "Images viewed this turn", iOS
 * `ViewedImagesTests`), from the work itself and from a fold that hides it; and where their strip goes: between the work
 * line and the answer, also while the turn runs.
 */
class ViewedImagesTest {
    private fun item(seq: Int, kind: String, role: String = "assistant", text: String? = null, tool: String? = null, id: String? = null,
        input: Map<String, String>? = null, image: ImagePayload? = null, error: Boolean? = null) =
        LineItem(Item("$seq#0", seq, role = role, kind = kind, text = text, toolUseId = id, toolName = tool,
            toolInput = input?.let { map -> buildJsonObject { map.forEach { (k, v) -> put(k, JsonPrimitive(v)) } } },
            isError = error, image = image))

    /** A lazy picture, as a page carries it. */
    private fun png(w: Int, h: Int, size: Int) = ImagePayload(mime = "image/png", lazy = true, width = w, height = h, size = size)

    private fun kinds(lines: List<ChatLine>) = lines.map { line ->
        when {
            line is ChatLine.Row && line.row.type == "user" -> "user"
            line is ChatLine.Row && line.row.type == "reply" -> "answer"
            line is ChatLine.Work -> "work"
            line is ChatLine.Viewed -> "viewed"
            line is ChatLine.UserImages -> "photos"
            else -> "other"
        }
    }

    private fun viewed(lines: List<ChatLine>) = lines.filterIsInstance<ChatLine.Viewed>().flatMap { it.images }

    /**
     * The images results carried (a Read's is that file), a picture Read opened whose result carried none (only its path),
     * each once in the order they came: not the same file again, not an unchanged screen looked at again, not a file that is
     * no picture, not a failed Read, not the photo the user sent.
     */
    private val turn = listOf(
        item(1, "text", "user", "Look at the home page"),
        item(2, "image", "user", image = ImagePayload(mime = "image/png", data = "aGVsbG8=")),
        item(3, "tool_use", tool = "Read", id = "a", input = mapOf("file_path" to "/w/shot.png")),
        item(4, "tool_result", "user", "", id = "a"),
        item(5, "image", "user", id = "a", image = png(4, 3, 100)),
        item(6, "tool_use", tool = "mcp__persona__computer_state", id = "b", input = mapOf("machine" to "m1", "app" to "Notes")),
        item(7, "tool_result", "user", "ok", id = "b"),
        item(8, "image", "user", id = "b", image = png(16, 10, 900)),
        item(9, "tool_use", tool = "Read", id = "c", input = mapOf("file_path" to "/w/shot.png")),
        item(10, "tool_result", "user", "", id = "c"),
        item(11, "image", "user", id = "c", image = png(4, 3, 100)),
        item(12, "tool_use", tool = "Read", id = "d", input = mapOf("file_path" to "/w/big.JPG")),
        item(13, "tool_result", "user", "", id = "d"),
        item(14, "tool_use", tool = "Read", id = "e", input = mapOf("file_path" to "/w/main.kt")),
        item(15, "tool_result", "user", "", id = "e"),
        item(16, "tool_use", tool = "Read", id = "f", input = mapOf("file_path" to "/w/gone.png")),
        item(17, "tool_result", "user", "no such file", id = "f", error = true),
        item(18, "tool_use", tool = "mcp__persona__browser_look", id = "g", input = mapOf("machine" to "m2")),
        item(19, "tool_result", "user", "ok", id = "g"),
        item(20, "image", "user", id = "g", image = png(8, 5, 300)),
        item(21, "tool_use", tool = "mcp__persona__computer_state", id = "h", input = mapOf("machine" to "m1")),
        item(22, "tool_result", "user", "ok", id = "h"),
        item(23, "image", "user", id = "h", image = png(16, 10, 900)),
        item(24, "text", text = "Done looking"),
    )

    @Test fun whatTheWorkLookedAtInOrderEachOnce() {
        val lines = chatLines(turn).lines
        assertEquals(listOf("photos", "user", "work", "viewed", "answer"), kinds(lines).filter { it != "other" })
        val images = viewed(lines)
        assertEquals(listOf(ViewedImage.Source.Read, ViewedImage.Source.Screenshot, ViewedImage.Source.Read, ViewedImage.Source.Browser),
            images.map { it.source })
        assertEquals(listOf("/w/shot.png", null, "/w/big.JPG", null), images.map { it.path })
        assertEquals(listOf(null, "m1", null, "m2"), images.map { it.machine })
        assertEquals(listOf("5#0", "8#0", null, "20#0"), images.map { it.item?.id })
        assertEquals("big.JPG", images[2].name)
        assertEquals(4f / 3, images[0].aspect)
    }

    /**
     * A page that folded the work still has its images: the fold says what its run looked at, and the strip is the one the
     * work itself gives once the line is opened.
     */
    @Test fun aFoldGivesTheSameStripAsItsWork() {
        val images = listOf(
            FoldImage(itemId = "5#0", toolUseId = "a", tool = "Read", source = "read", path = "/w/shot.png", mime = "image/png",
                size = 100, width = 4, height = 3),
            FoldImage(itemId = "8#0", toolUseId = "b", tool = "mcp__persona__computer_state", source = "screenshot", machine = "m1",
                mime = "image/png", size = 900, width = 16, height = 10),
            FoldImage(toolUseId = "d", tool = "Read", source = "read", path = "/w/big.JPG"),
            FoldImage(itemId = "20#0", toolUseId = "g", tool = "mcp__persona__browser_look", source = "browser", machine = "m2",
                mime = "image/png", size = 300, width = 8, height = 5),
        )
        val fold = LineItem(Item("fold:3#0", 3, kind = "fold", fold = WireFold(items = 21, tools = 8, throughSeq = 23, throughId = "23#0",
            images = images)), fold = Fold(21, 8, 0, 23, "23#0"))
        val folded = viewed(chatLines(listOf(turn[0], turn[1], fold, turn.last())).lines)
        val open = viewed(chatLines(turn).lines)
        assertEquals(open.map { it.id }, folded.map { it.id })
        assertEquals(open.map { it.item?.id }, folded.map { it.item?.id })
        assertEquals(open.map { it.source }, folded.map { it.source })
        assertEquals(listOf(4, 16, null, 8), folded.map { it.width })
        assertTrue(folded.mapNotNull { it.item }.all { it.image?.lazy == true })
    }

    /**
     * While the turn runs the strip comes after the work line, before the words so far, and keeps its id as pictures come in
     * (it grows sideways in place); a call still reading is not in it yet.
     */
    @Test fun whileTheTurnRuns() {
        val running = turn.take(8) + listOf(item(9, "text", text = "Got it, one more"),
            item(10, "tool_use", tool = "Read", id = "c", input = mapOf("file_path" to "/w/next.png")))
        val lines = chatLines(running, LineOptions(live = true)).lines
        assertEquals(listOf("photos", "user", "work", "viewed", "answer"), kinds(lines).filter { it != "other" })
        assertEquals(2, viewed(lines).size)
        val more = running + listOf(item(11, "tool_result", "user", "", id = "c"), item(12, "image", "user", id = "c", image = png(2, 2, 40)))
        val later = chatLines(more, LineOptions(live = true)).lines
        assertEquals(3, viewed(later).size)
        assertEquals(lines.filterIsInstance<ChatLine.Viewed>().single().id, later.filterIsInstance<ChatLine.Viewed>().single().id)
    }

    @Test fun noImagesNoStrip() {
        val lines = chatLines(listOf(item(1, "text", "user", "Run it"), item(2, "tool_use", tool = "Bash", id = "a"),
            item(3, "tool_result", "user", "ok", id = "a"), item(4, "text", text = "Done"))).lines
        assertEquals(listOf("user", "work", "answer"), kinds(lines))
    }

    /** Only Read names a file; another server's tool that happens to be called Read does not. */
    @Test fun whereImagesCameFrom() {
        fun source(tool: String, input: Map<String, String> = emptyMap()) =
            ViewedImages.said(tool, buildJsonObject { input.forEach { (k, v) -> put(k, JsonPrimitive(v)) } })
        assertEquals(ViewedImages.Said(ViewedImage.Source.Read, "/w/a.png", null), source("Read", mapOf("file_path" to "/w/a.png")))
        assertEquals(ViewedImages.Said(ViewedImage.Source.Mcp, null, null), source("mcp__files__Read", mapOf("file_path" to "/w/a.png")))
        assertEquals(ViewedImages.Said(ViewedImage.Source.Screenshot, null, "Studio"), source("mcp__persona__computer_click", mapOf("machine" to "Studio")))
        assertEquals(ViewedImages.Said(ViewedImage.Source.Tool, null, null), source("view_image"))
        assertTrue(ViewedImages.isPicture("/w/A.HEIC"))
        assertTrue(!ViewedImages.isPicture("/w/a.svg"))
    }
}
