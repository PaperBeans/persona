package persona.core.domain.lines

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import persona.core.contracts.FoldImage
import persona.core.contracts.ImagePayload
import persona.core.contracts.Item

/**
 * An image the agent looked at in a turn (docs/protocol/protocol.md "Images viewed this turn", iOS `ViewedImage`), for the
 * strip before its answer: one a tool's result carried ([item], loaded as any picture of the chat), or a picture file Read
 * opened whose result carried none (only [path], drawn from the machine's thumbnail).
 */
data class ViewedImage(
    /** What it is told apart by ([ViewedImages.key]): one path once, one image once. */
    val id: String,
    val item: Item?,
    val path: String?,
    val source: Source,
    /** The tool's name as the transcript has it (`Read`, `mcp__persona__computer_state`). */
    val tool: String,
    /** The machine a screenshot's or a browser's call named (an id or a name). */
    val machine: String? = null,
) {
    enum class Source(val wire: String) { Read("read"), Screenshot("screenshot"), Browser("browser"), Mcp("mcp"), Tool("tool") }

    val width: Int? get() = item?.image?.width
    val height: Int? get() = item?.image?.height
    /** Width over height when known: the tile takes its shape before the bytes arrive. */
    val aspect: Float? get() { val w = width ?: return null; val h = height ?: return null; return if (w > 0 && h > 0) w.toFloat() / h else null }
    /** The file's name, when it is a file. */
    val name: String? get() = path?.substringAfterLast('/')?.takeIf(String::isNotEmpty)
}

/**
 * A turn's images from its work, by the same rules as the hub's `transcript.ViewedImages` (iOS `ViewedImages`): what the
 * calls' results carried, a picture file Read opened when its result carried none, and what a folded run looked at
 * (`fold.images`), in the order they came, each once. Images the user sent are not among them (they are in the bubble).
 */
object ViewedImages {
    /** The files whose Read is looking at a picture. */
    val pictureExtensions = setOf("png", "jpg", "jpeg", "gif", "webp", "heic", "heif", "bmp", "tif", "tiff")

    fun of(work: List<WorkItem>): List<ViewedImage> {
        val out = mutableListOf<ViewedImage>()
        val seen = mutableSetOf<String>()
        fun add(image: ViewedImage) { if (seen.add(image.id)) out += image }
        for (item in work) when (item) {
            is WorkItem.Tool -> images(item.call).forEach(::add)
            is WorkItem.Folded -> item.item.item.fold?.images.orEmpty().map(::image).forEach(::add)
            else -> Unit
        }
        return out
    }

    /** What a call looked at: the images its result carried; a picture file Read opened, once it did, when none. */
    fun images(call: ToolCall): List<ViewedImage> {
        val said = said(call.name, call.use.item.toolInput as? JsonObject)
        val pictures = call.images.map { it.item }.filter { it.image != null }
        if (pictures.isNotEmpty()) {
            // Read reads one picture: it is that file. Another tool's images are its own.
            val file = if (pictures.size == 1) said.path else null
            return pictures.map { ViewedImage(key(file, it), it, file, said.source, call.name, said.machine) }
        }
        val path = said.path
        if (call.result == null || call.failed || path == null || !isPicture(path)) return emptyList()
        return listOf(ViewedImage(key(path, null), null, path, said.source, call.name, said.machine))
    }

    /** A folded run's image: its image item when the hub kept one, to load lazily like the chat's other pictures. */
    fun image(f: FoldImage): ViewedImage {
        val item = f.itemId?.takeIf(String::isNotEmpty)?.let { id ->
            Item(id, kind = "image", toolUseId = f.toolUseId,
                image = ImagePayload(mime = f.mime ?: "image/png", lazy = true, width = f.width, height = f.height, size = f.size))
        }
        val id = f.path?.let { "p:$it" } ?: sizeKey(f.mime, f.size, f.width, f.height) ?: ("i:" + (f.itemId ?: f.toolUseId))
        return ViewedImage(id, item, f.path, ViewedImage.Source.entries.firstOrNull { it.wire == f.source } ?: ViewedImage.Source.Tool,
            f.tool, f.machine)
    }

    internal data class Said(val source: ViewedImage.Source, val path: String?, val machine: String?)

    /** Where a call's images came from, and the file or the machine it named. Only Read names a file. */
    internal fun said(tool: String, input: JsonObject?): Said {
        var base = tool
        var mcp = false
        val last = tool.lastIndexOf("__")
        if (tool.startsWith("mcp__") && last > 4) {
            base = tool.substring(last + 2)
            mcp = true
        }
        fun field(name: String) = (input?.get(name) as? JsonPrimitive)?.takeIf { it.isString }?.content?.takeIf(String::isNotEmpty)
        val machine = field("machine")
        return when {
            base == "Read" && !mcp -> Said(ViewedImage.Source.Read, field("file_path"), null)
            mcp && base.startsWith("computer_") -> Said(ViewedImage.Source.Screenshot, null, machine)
            mcp && base.startsWith("browser_") -> Said(ViewedImage.Source.Browser, null, machine)
            mcp -> Said(ViewedImage.Source.Mcp, null, null)
            else -> Said(ViewedImage.Source.Tool, null, null)
        }
    }

    fun isPicture(path: String): Boolean = path.substringAfterLast('/').substringAfterLast('.', "").lowercase() in pictureExtensions

    /**
     * One file once; an image the same as one before (its bytes' size and shape: an unchanged screen looked at again)
     * once; else the image itself.
     */
    internal fun key(path: String?, item: Item?): String {
        if (path != null) return "p:$path"
        val image = item?.image ?: return ""
        val size = image.size ?: image.data.takeIf(String::isNotEmpty)?.let { it.length * 3 / 4 }
        return sizeKey(image.mime, size, image.width, image.height) ?: ("d:" + item.id)
    }

    private fun sizeKey(mime: String?, size: Int?, width: Int?, height: Int?): String? {
        if (size == null || width == null || height == null || size <= 0 || width <= 0 || height <= 0) return null
        return "s:${mime.orEmpty()}:$size:${width}x$height"
    }
}
