package persona.core.designsystem

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import persona.core.designsystem.icons.PhIcon
import java.io.IOException
import java.util.Collections

/**
 * Thumbnails of a machine's files (the hub's `file/thumb`, iOS `FileThumbnails`): decoded pictures kept in memory
 * (process wide, the last 96), and the files the machine could not draw this launch, so a list scrolling back does not
 * ask again. Which files are worth asking is by extension: pictures, documents with a first page, videos.
 */
object FileThumbnails {
    private val pictures = setOf("png", "jpg", "jpeg", "gif", "webp", "heic", "heif", "tiff", "tif", "bmp", "svg")
    /** Word, Excel, PowerPoint, iWork, RTF and PDF: what has a first page to show (iOS `QuickLookFileType` + PDF). */
    private val documents = setOf("docx", "doc", "xlsx", "xls", "pptx", "ppt", "pages", "numbers", "key", "rtf", "pdf")
    val videos = setOf("mp4", "mov", "m4v", "mpeg", "mpg", "webm", "mkv", "avi")

    private fun extension(name: String) = name.substringAfterLast('/').substringAfterLast('.', "").lowercase()

    /** Whether to ask the machine for a picture of [name]: an empty file has none. */
    fun wanted(name: String, size: Long = -1): Boolean =
        size != 0L && extension(name).let { it in pictures || it in documents || it in videos }

    /** A document an answer names that shows as a card with its first page (iOS `DocumentCards.isDocument`). */
    fun isDocument(path: String): Boolean = !path.endsWith("/") && extension(path) in documents

    fun isVideo(name: String): Boolean = extension(name) in videos

    /** The last pictures decoded, at most this many. */
    internal const val KEEP = 96
    private val images: MutableMap<String, ImageBitmap> = Collections.synchronizedMap(object : LinkedHashMap<String, ImageBitmap>(64, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ImageBitmap>?) = size > KEEP
    })
    private val failed: MutableSet<String> = Collections.synchronizedSet(mutableSetOf())
    /** Three requests at a time, as iOS: each document's first page may start LibreOffice on the machine. */
    private val gate = Semaphore(3)
    /** One load per key: a second caller for the same file waits for the first one's answer instead of asking again. */
    private class Pending { val lock = Mutex(); var users = 0 }
    private val pending = HashMap<String, Pending>()

    fun cached(key: String): ImageBitmap? = images[key]

    /**
     * The picture for [key], loading it with [load] (the bytes at the pixel size asked; null: the machine has none)
     * the first time. A definitive answer (no picture, bytes that don't decode, a transport without thumbnails) is
     * remembered for the launch and the icon stays; a timeout or a dropped connection is tried again next time.
     */
    suspend fun picture(key: String, pixels: Int, load: suspend (Int) -> ByteArray?): ImageBitmap? =
        picture(key, pixels, load) { bytes -> withContext(Dispatchers.Default) { decodePicture(bytes, pixels).asImageBitmap() } }

    internal suspend fun picture(key: String, pixels: Int, load: suspend (Int) -> ByteArray?, decode: suspend (ByteArray) -> ImageBitmap?): ImageBitmap? {
        images[key]?.let { return it }
        if (key in failed) return null
        val entry = synchronized(pending) { pending.getOrPut(key) { Pending() }.also { it.users++ } }
        try {
            return entry.lock.withLock {
                images[key]?.let { return@withLock it }
                if (key in failed) return@withLock null
                var definitive = true
                val bytes = try {
                    gate.withPermit { load(pixels) }
                } catch (e: CancellationException) {
                    throw e
                } catch (_: IOException) {
                    definitive = false
                    null
                } catch (_: Exception) {
                    null
                }
                // Bytes that don't decode are definitive (ImageDecoder's DecodeException is an IOException too).
                val image = bytes?.let { try { decode(it) } catch (e: CancellationException) { throw e } catch (_: Exception) { null } }
                if (image != null) images[key] = image else if (definitive) failed += key
                image
            }
        } finally {
            synchronized(pending) { if (--entry.users == 0) pending.remove(key) }
        }
    }

    /** For tests: forget everything. */
    fun clear() { images.clear(); failed.clear() }
}

/**
 * A file's icon, replaced by its thumbnail once there is one ([FileThumbnails], iOS `FileThumbnailIcon`): the Files
 * page's rows at an icon's size, a chat's document card bigger ([width] × [height], the picture fitted in, a hairline
 * round it). [load] null: only the icon.
 */
@Composable
fun FileThumbnailIcon(
    key: String,
    icon: PhIcon,
    load: (suspend (pixels: Int) -> ByteArray?)?,
    modifier: Modifier = Modifier,
    width: Dp = 22.dp,
    height: Dp = 22.dp,
    tint: androidx.compose.ui.graphics.Color = PersonaTheme.palette.ink2,
) {
    val pixels = with(LocalDensity.current) { maxOf(width, height).roundToPx() }.coerceIn(32, 1024)
    // produceState keeps its value when the key changes (a rewritten file, a reused row): start from the new key's.
    val image by produceState(FileThumbnails.cached(key), key, load != null) {
        value = FileThumbnails.cached(key)
        if (value == null && load != null) value = FileThumbnails.picture(key, pixels, load)
    }
    Box(modifier.size(width, height), contentAlignment = Alignment.Center) {
        val picture = image
        if (picture != null) {
            val shape = androidx.compose.foundation.shape.RoundedCornerShape(3.dp)
            val aspect = picture.width.toFloat() / picture.height.coerceAtLeast(1)
            Image(picture, null, Modifier.aspectRatio(aspect.coerceIn(0.2f, 5f), matchHeightConstraintsFirst = aspect < width / height)
                .clip(shape).border(Dp.Hairline, PersonaTheme.palette.line, shape), contentScale = ContentScale.Fit)
        } else JIcon(icon, null, size = minOf(width, 22.dp), tint = tint, decorative = true)
    }
}

/**
 * A file as a card in a chat (iOS `AttachmentCard` / `AttachedVideoCard`): [leading] on the left (a thumbnail, or an
 * icon in a well), its name on one line, what it is or its size under it. A tap opens it; [onLongClick] offers more.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FileAttachmentCard(
    title: String,
    subtitle: String,
    leading: @Composable () -> Unit,
    onClick: (() -> Unit)?,
    modifier: Modifier = Modifier,
    onLongClick: (() -> Unit)? = null,
    onClickLabel: String? = null,
    /** What TalkBack announces for [onLongClick] (iOS's context menu item). */
    onLongClickLabel: String? = null,
) {
    val palette = PersonaTheme.palette
    val shape = PersonaTheme.shapes.cardShape
    val board = PersonaTheme.style == AppStyle.Board
    // A well that shows on the page: where the style's sunken fill is the page's own colour, the stronger fill.
    val well = if (palette.sunken == palette.surface || palette.sunken == palette.background) palette.surfaceStrong else palette.sunken
    val click = if (onClick != null || onLongClick != null)
        Modifier.combinedClickable(role = Role.Button, onClickLabel = onClickLabel, onLongClickLabel = onLongClickLabel, onClick = onClick ?: {}, onLongClick = onLongClick) else Modifier
    Row(
        modifier.widthIn(max = 280.dp).width(240.dp).heightIn(min = 64.dp.row).clip(shape).background(if (board) palette.surface else well)
            .then(if (board) Modifier.border(1.dp, palette.line, shape) else Modifier)
            .then(click).padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m),
    ) {
        leading()
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            Text(title, style = PersonaTheme.type.bodyStrong, color = palette.ink, maxLines = 1, overflow = TextOverflow.MiddleEllipsis)
            if (subtitle.isNotEmpty()) Text(subtitle, style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** An icon in a tinted square, the left of a [FileAttachmentCard] for a video, a recording or a file without a picture. */
@Composable
fun FileCardIcon(icon: PhIcon, modifier: Modifier = Modifier, busy: Boolean = false) {
    val palette = PersonaTheme.palette
    Box(modifier.size(46.dp).clip(PersonaTheme.shapes.controlShape).background(palette.accentSoft), contentAlignment = Alignment.Center) {
        if (busy) JSpinner(Modifier.size(18.dp)) else JIcon(icon, null, tint = palette.accent, decorative = true)
    }
}

/** Cards side by side that scroll sideways when they do not fit (iOS `AttachmentScroller`). */
@Composable
fun FileCardRow(modifier: Modifier = Modifier, alignEnd: Boolean = false, content: @Composable RowScope.() -> Unit) {
    Box(modifier.fillMaxWidth(), contentAlignment = if (alignEnd) Alignment.CenterEnd else Alignment.CenterStart) {
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), content = content)
    }
}

/** A document's first page on the left of a [FileAttachmentCard] (iOS `wellL` × `avatarXL`). */
@Composable
fun DocumentCardThumbnail(key: String, icon: PhIcon, load: (suspend (pixels: Int) -> ByteArray?)?) {
    FileThumbnailIcon(key, icon, load, width = 36.dp, height = 48.dp, tint = PersonaTheme.palette.accent)
}
