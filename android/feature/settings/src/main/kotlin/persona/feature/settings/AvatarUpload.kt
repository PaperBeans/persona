package persona.feature.settings

import android.content.ContentResolver
import android.graphics.Bitmap
import android.graphics.ColorSpace
import android.graphics.ImageDecoder
import android.net.Uri
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.ClipOp
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.IntSize
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JIcon
import persona.core.designsystem.JNotice
import persona.core.designsystem.JSheet
import persona.core.designsystem.JTextField
import persona.core.designsystem.LookUploadOutcome
import persona.core.designsystem.PersonaStore
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.UiText
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.text
import java.io.ByteArrayOutputStream
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

// New from Photo (#2407, docs/apple/persona.md "Uploading an image"), as on iOS (#2405): the image decoded upright and shown
// under a square with a circle over it (drag to move, pinch to zoom), a name and an optional description, then the hub
// makes the avatar (POST /api/v1/looks/uploads) and this phone takes it on.

/** The bounds the hub and the iOS app keep (docs/protocol/protocol.md, `POST /api/v1/looks/uploads`). */
internal object AvatarImage {
    /** The hub refuses an image under this on a side; the crop never shows fewer of the image's pixels. */
    const val MIN_SIDE = 64
    /** Decoded at most this wide or tall: plenty for a 1024 px crop at 4× zoom, and a phone's photo fits in memory. */
    const val DECODE_MAX = 4096
    /** Sent at most this wide: the largest size the hub keeps is 512. */
    const val SEND_MAX = 1024
    const val MAX_ZOOM = 8f
    /** A photo whose PNG is larger than this goes as JPEG; one with transparency stays PNG. */
    const val JPEG_ABOVE = 1_500_000
    const val JPEG_QUALITY = 90
    /** Characters as the hub counts them (Unicode code points). */
    const val NAME_MAX = 40
    const val SUBTITLE_MAX = 80
}

/** [text] cut to [max] Unicode code points, as the hub counts a name, never in the middle of a surrogate pair. */
internal fun cutCodePoints(text: String, max: Int): String =
    if (text.codePointCount(0, text.length) <= max) text else text.substring(0, text.offsetByCodePoints(0, max))

/**
 * Where the crop square sits on an image [width] × [height] px: centred on ([x], [y]), [zoom] times closer than the whole
 * short side. Always inside the image, never under [AvatarImage.MIN_SIDE] px of it, never past [AvatarImage.MAX_ZOOM].
 */
internal data class AvatarCrop(val width: Int, val height: Int, val zoom: Float, val x: Float, val y: Float) {
    private val short: Int get() = min(width, height)
    /** The furthest in: 8×, or as far as leaves 64 px of the image. */
    val maxZoom: Float get() = min(AvatarImage.MAX_ZOOM, short.toFloat() / AvatarImage.MIN_SIDE).coerceAtLeast(1f)
    /** The square's side in image pixels. */
    val side: Float get() = short / zoom
    /** The square in whole image pixels: left, top, side. */
    val square: Triple<Int, Int, Int>
        get() {
            val s = side.roundToInt().coerceIn(1, short)
            val left = (x - s / 2f).roundToInt().coerceIn(0, width - s)
            val top = (y - s / 2f).roundToInt().coerceIn(0, height - s)
            return Triple(left, top, s)
        }
    /** The side sent to the hub. */
    val sendSide: Int get() = min(square.third, AvatarImage.SEND_MAX)

    /** Moved by a drag of ([dx], [dy]) on a crop box [view] px wide: the picture follows the finger. */
    fun moved(dx: Float, dy: Float, view: Float): AvatarCrop {
        val perPixel = side / view.coerceAtLeast(1f)
        return copy(x = x - dx * perPixel, y = y - dy * perPixel).bounded()
    }

    /** Zoomed by [factor] (a pinch), about the square's centre. */
    fun zoomed(factor: Float): AvatarCrop = copy(zoom = zoom * factor).bounded()

    private fun bounded(): AvatarCrop {
        val z = zoom.coerceIn(1f, maxZoom)
        val half = short / z / 2f
        return copy(zoom = z, x = x.coerceIn(half, width - half), y = y.coerceIn(half, height - half))
    }

    companion object {
        /** The whole short side, centred. */
        fun of(width: Int, height: Int) = AvatarCrop(width, height, 1f, width / 2f, height / 2f)
    }
}

/** Why an image can't be made into an avatar on this phone. */
internal class AvatarImageException(val reason: Reason) : Exception(reason.name) {
    enum class Reason { Unreadable, TooSmall, Unprepared }
}

/** The image at [uri] upright (its EXIF orientation applied; a GIF's first frame), in sRGB, at most 4096 px a side. */
internal suspend fun decodeAvatarImage(resolver: ContentResolver, uri: Uri): Bitmap = withContext(Dispatchers.IO) {
    var small = false
    val bitmap = try {
        ImageDecoder.decodeBitmap(ImageDecoder.createSource(resolver, uri)) { decoder, info, _ ->
            val (w, h) = info.size.width to info.size.height
            if (min(w, h) < AvatarImage.MIN_SIDE) { small = true; return@decodeBitmap }
            decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            decoder.setTargetColorSpace(ColorSpace.get(ColorSpace.Named.SRGB))
            val longest = max(w, h)
            if (longest > AvatarImage.DECODE_MAX) decoder.setTargetSize(
                (w.toLong() * AvatarImage.DECODE_MAX / longest).toInt().coerceAtLeast(1),
                (h.toLong() * AvatarImage.DECODE_MAX / longest).toInt().coerceAtLeast(1),
            )
        }
    } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (_: Exception) {
        throw AvatarImageException(AvatarImageException.Reason.Unreadable)
    }
    if (small || min(bitmap.width, bitmap.height) < AvatarImage.MIN_SIDE) throw AvatarImageException(AvatarImageException.Reason.TooSmall)
    bitmap
}

/** The crop as the hub gets it: square, at most 1024 px, PNG (JPEG for a large photo with no transparency). */
internal fun encodeAvatar(image: Bitmap, crop: AvatarCrop): ByteArray {
    val (left, top, side) = crop.square
    val square = Bitmap.createBitmap(image, left, top, side, side)
    val sent = if (side > crop.sendSide) Bitmap.createScaledBitmap(square, crop.sendSide, crop.sendSide, true) else square
    val png = ByteArrayOutputStream().also { sent.compress(Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
    if (png.size <= AvatarImage.JPEG_ABOVE || sent.hasAlpha()) return png
    return ByteArrayOutputStream().also { sent.compress(Bitmap.CompressFormat.JPEG, AvatarImage.JPEG_QUALITY, it) }.toByteArray()
}

/**
 * The crop sheet's state: the image and its crop, the name and description, and how the upload went. One upload at a
 * time; when the avatar was made but not downloaded, [made] keeps its id and Try Again only downloads it again.
 */
internal class AvatarUploadModel(val image: Bitmap) {
    var crop by mutableStateOf(AvatarCrop.of(image.width, image.height))
    var name by mutableStateOf("")
        private set
    var subtitle by mutableStateOf("")
        private set
    var busy by mutableStateOf(false)
        private set
    var error by mutableStateOf<UiText?>(null)
        private set
    /** The avatar the hub made whose package didn't come here yet. */
    var made by mutableStateOf<String?>(null)
        private set

    fun rename(value: String) { name = cutCodePoints(value, AvatarImage.NAME_MAX) }
    fun describe(value: String) { subtitle = cutCodePoints(value, AvatarImage.SUBTITLE_MAX) }

    val canSend: Boolean get() = !busy && (made != null || name.isNotBlank())

    /** Sends the crop (or, after a made-but-not-downloaded one, downloads it again); true when the sheet can close. */
    suspend fun send(store: PersonaStore): Boolean {
        if (!canSend) return false
        busy = true
        error = null
        try {
            val outcome = made?.let { store.fetch(it) } ?: run {
                val bytes = try { withContext(Dispatchers.Default) { encodeAvatar(image, crop) } }
                    catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (_: Exception) {
                        error = UiText.Res(R.string.settings_avatar_upload_unprepared); return false
                    }
                store.upload(name.trim(), subtitle.trim().takeIf { it.isNotEmpty() }, bytes)
            }
            return when (outcome) {
                LookUploadOutcome.Done -> true
                is LookUploadOutcome.NotDownloaded -> { made = outcome.id; error = UiText.Res(R.string.settings_avatar_upload_not_downloaded); false }
                is LookUploadOutcome.Refused -> { error = UiText.Plain(outcome.words); false }
                LookUploadOutcome.Unreachable -> { error = UiText.Res(R.string.settings_avatar_upload_unreachable); false }
            }
        } finally {
            busy = false
        }
    }
}

/** "New from Photo", after the avatars: opens the system's photo picker. */
@Composable
internal fun NewFromPhotoOption(onClick: () -> Unit, enabled: Boolean) {
    val palette = PersonaTheme.palette
    PersonaOption(stringResource(R.string.settings_avatar_new_from_photo), stringResource(R.string.settings_avatar_new_from_photo_detail),
        selected = false, onClick = { if (enabled) onClick() }, radio = false, modifier = Modifier.testTag("avatar-new-from-photo")) {
        Box(Modifier.size(Tokens.Space.Standard.xxl * 2).clip(CircleShape).background(palette.surfaceStrong), contentAlignment = Alignment.Center) {
            JIcon(PhIcons.Plus, null, size = Tokens.Space.xl, tint = palette.accent)
        }
    }
}

/** The crop sheet: the picture under a square with a circle, its name and description, Upload (or Try Again). */
@Composable
internal fun AvatarUploadSheet(model: AvatarUploadModel, onSend: () -> Unit, onDismiss: () -> Unit) {
    val palette = PersonaTheme.palette
    JSheet(onDismiss, title = stringResource(R.string.settings_avatar_upload_title), dismissible = !model.busy, scrollable = true) {
        AvatarCropBox(model)
        Spacer(Modifier.height(Tokens.Space.s))
        Text(stringResource(R.string.settings_avatar_upload_crop_hint), style = PersonaTheme.type.caption, color = palette.ink2)
        Spacer(Modifier.height(Tokens.Space.l))
        JTextField(model.name, model::rename, Modifier.fillMaxWidth().testTag("avatar-upload-name"),
            label = stringResource(R.string.settings_avatar_upload_name), singleLine = true, enabled = !model.busy && model.made == null)
        Spacer(Modifier.height(Tokens.Space.s))
        JTextField(model.subtitle, model::describe, Modifier.fillMaxWidth().testTag("avatar-upload-subtitle"),
            label = stringResource(R.string.settings_avatar_upload_subtitle), singleLine = true, enabled = !model.busy && model.made == null)
        Spacer(Modifier.height(Tokens.Space.s))
        Text(stringResource(R.string.settings_avatar_upload_name_note), style = PersonaTheme.type.caption, color = palette.ink2)
        model.error?.let {
            Spacer(Modifier.height(Tokens.Space.m))
            JNotice(it.text(), Modifier.testTag("avatar-upload-error"), status = Status.NeedsYou)
        }
        Spacer(Modifier.height(Tokens.Space.l))
        JButton(stringResource(if (model.made != null) R.string.settings_avatar_upload_retry else R.string.settings_avatar_upload_send), onSend,
            Modifier.fillMaxWidth().testTag("avatar-upload-send"), kind = JButtonKind.Primary, size = JButtonSize.Large,
            enabled = model.canSend, loading = model.busy)
    }
}

/** The picture, filling a square box, with the circle the avatar will be: drag to move, pinch to zoom. */
@Composable
private fun AvatarCropBox(model: AvatarUploadModel) {
    val palette = PersonaTheme.palette
    val bitmap: ImageBitmap = remember(model.image) { model.image.asImageBitmap() }
    var box by remember { mutableStateOf(IntSize.Zero) }
    val zoomIn = stringResource(R.string.settings_avatar_upload_zoom_in)
    val zoomOut = stringResource(R.string.settings_avatar_upload_zoom_out)
    val described = stringResource(R.string.settings_avatar_upload_crop)
    val ring = with(LocalDensity.current) { Tokens.Space.xxs.toPx() }
    Box(
        Modifier.fillMaxWidth().aspectRatio(1f).clip(PersonaTheme.shapes.cardShape).clipToBounds().background(palette.mediaBackdrop)
            .onSizeChanged { box = it }
            .pointerInput(model) {
                detectTransformGestures { _, pan, zoom, _ ->
                    model.crop = model.crop.zoomed(zoom).moved(pan.x, pan.y, size.width.toFloat())
                }
            }
            .semantics {
                contentDescription = described
                // TalkBack zooms by actions, as VoiceOver swipes up and down on iOS.
                customActions = listOf(
                    CustomAccessibilityAction(zoomIn) { model.crop = model.crop.zoomed(1.25f); true },
                    CustomAccessibilityAction(zoomOut) { model.crop = model.crop.zoomed(0.8f); true },
                )
            }
            .testTag("avatar-crop"),
    ) {
        Canvas(Modifier.matchParentSize()) {
            val (left, top, side) = model.crop.square
            drawImage(bitmap, srcOffset = androidx.compose.ui.unit.IntOffset(left, top), srcSize = IntSize(side, side),
                dstSize = IntSize(size.width.roundToInt(), size.height.roundToInt()), filterQuality = androidx.compose.ui.graphics.FilterQuality.Medium)
            val circle = Path().apply { addOval(Rect(Offset.Zero, Size(size.width, size.height))) }
            clipPath(circle, ClipOp.Difference) { drawRect(palette.cropOutside) }
            drawCircle(palette.cropEdge, radius = size.minDimension / 2f - ring / 2f, style = Stroke(ring))
        }
    }
}
