package persona.feature.chat

import android.content.Context
import android.content.Intent
import android.content.res.Resources
import android.net.Uri
import android.os.Build
import android.os.ext.SdkExtensions
import android.provider.MediaStore
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContract
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.withContext
import persona.core.contracts.ImageLimits
import persona.core.contracts.ImageLimitsTable
import persona.core.contracts.api.DraftUploadState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.SocketHello
import persona.core.designsystem.LocalHubImageLimits

/** Videos keep their own cap of 4, independent of a message's picture limits (docs/protocol/protocol.md "Sending videos"). */
const val MAX_COMPOSER_VIDEOS = 4

/** Remaining video slots in a message; pictures and ordinary files do not count toward videos. */
fun composerVideoRoom(attachments: List<ComposerAttachment>): Int =
    (MAX_COMPOSER_VIDEOS - attachments.count { it.video && it.uploadState != DraftUploadState.Invalid }).coerceAtLeast(0)

/** Remaining inline picture slots in a message by count alone; videos and ordinary files do not count toward pictures. */
fun composerPhotoRoom(attachments: List<ComposerAttachment>, limits: ImageLimits): Int =
    limits.roomLeft(attachments.count { it.image && it.uploadState != DraftUploadState.Invalid })

/** Remaining slots for a media picker that picks both pictures and videos (including file slots pictures can spill into). */
fun composerMediaPickerRoom(attachments: List<ComposerAttachment>, limits: ImageLimits, canSpill: Boolean): Int =
    composerPhotoRoom(attachments, limits) + (if (canSpill) composerFileRoom(attachments) else 0) + composerVideoRoom(attachments)

/** Whether at least one more picture can join the message (inline, or spilled as a file when [canSpill]). */
fun composerCanAddPhoto(attachments: List<ComposerAttachment>, limits: ImageLimits, canSpill: Boolean): Boolean =
    composerPhotoRoom(attachments, limits) > 0 || (canSpill && composerFileRoom(attachments) > 0)

/**
 * One incoming photo candidate paired with its measured scaled JPEG byte count (used for base64 body-budget
 * accounting, while [attachment] retains the original file size in case it overflows to an ordinary file upload).
 * When [scaledBytes] is null, admission falls back to a positive [ComposerAttachment.scaledBytes] or
 * [ComposerAttachment.size], and conservatively reserves [ImageLimits.imageBytes] rather than zero when unknown.
 */
data class PhotoCandidate(
    val attachment: ComposerAttachment,
    val scaledBytes: Long? = attachment.scaledBytes ?: attachment.size,
)

/**
 * Byte count used for base64 body-budget accounting of a held inline photo: prefers its measured/staged scaled JPEG
 * size ([ComposerAttachment.scaledBytes]), falls back to a positive [ComposerAttachment.size] (legacy uploaded drafts),
 * and conservatively reserves [ImageLimits.imageBytes] when unknown so unmeasured held photos (such as those restored
 * from message tags) never contribute zero.
 */
internal fun heldPhotoBytes(attachment: ComposerAttachment, limits: ImageLimits): Long =
    attachment.scaledBytes?.takeIf { it > 0L }
        ?: attachment.size?.takeIf { it > 0L }
        ?: limits.imageBytes

private fun candidatePhotoBytes(candidate: PhotoCandidate, limits: ImageLimits): Long =
    candidate.scaledBytes?.takeIf { it > 0L }
        ?: candidate.attachment.scaledBytes?.takeIf { it > 0L }
        ?: candidate.attachment.size?.takeIf { it > 0L }
        ?: limits.imageBytes

/**
 * Measures scaled JPEG bytes for held inline photos that have a readable local URI but no recorded
 * [ComposerAttachment.scaledBytes] yet (for example, legacy drafts), notifying [onMeasured] for each resolved photo.
 */
internal suspend fun refreshHeldPhotoSizes(
    context: Context,
    attachments: List<ComposerAttachment>,
    limits: ImageLimits,
    onMeasured: (id: String, scaledBytes: Long) -> Unit = { _, _ -> },
): List<ComposerAttachment> {
    val needsMeasure = attachments.filter {
        it.image && it.uploadState != DraftUploadState.Invalid && it.scaledBytes == null && it.uri.isNotEmpty()
    }
    if (needsMeasure.isEmpty()) return attachments
    val measured = withContext(Dispatchers.IO) {
        needsMeasure.mapNotNull { att ->
            measurePhotoBytes(context, Uri.parse(att.uri), limits)?.takeIf { it > 0L }?.let { att.id to it }
        }.toMap()
    }
    if (measured.isEmpty()) return attachments
    measured.forEach(onMeasured)
    return attachments.map { att ->
        val bytes = measured[att.id] ?: return@map att
        att.copy(scaledBytes = bytes)
    }
}

/** IO-thread variant of [refreshHeldPhotoSizes] for synchronous drop imports. */
internal fun refreshHeldPhotoSizesBlocking(
    context: Context,
    attachments: List<ComposerAttachment>,
    limits: ImageLimits,
): List<ComposerAttachment> = attachments.map { att ->
    if (att.image && att.uploadState != DraftUploadState.Invalid && att.scaledBytes == null && att.uri.isNotEmpty()) {
        val bytes = measurePhotoBytes(context, Uri.parse(att.uri), limits)?.takeIf { it > 0L }
        if (bytes != null) att.copy(scaledBytes = bytes) else att
    } else att
}

/** Outcome of admitting one or more incoming photos under [ImageLimits]. */
data class ComposerPhotoPlan(
    val acceptedPhotos: List<ComposerAttachment> = emptyList(),
    val spilledFiles: List<ComposerAttachment> = emptyList(),
    val refused: List<ComposerAttachment> = emptyList(),
    val spilledByCount: Int = 0,
    val spilledByBytes: Int = 0,
    val refusedByCount: Int = 0,
    val refusedByBytes: Int = 0,
) {
    /** Every attachment that joins the composer (inline photos plus overflow file attachments). */
    val added: List<ComposerAttachment> get() = acceptedPhotos + spilledFiles

    /** Localized notices explaining which limit was hit and how many pictures went as files. */
    fun notices(resources: Resources, limits: ImageLimits): List<String> = buildList {
        if (spilledByCount > 0) add(
            if (spilledByCount == 1) resources.getString(R.string.chat_composer_photos_as_files_one, limits.perMessage)
            else resources.getString(R.string.chat_composer_photos_as_files_other, limits.perMessage, spilledByCount)
        )
        if (spilledByBytes > 0) add(
            if (spilledByBytes == 1) resources.getString(R.string.chat_composer_photos_bytes_as_files_one)
            else resources.getString(R.string.chat_composer_photos_bytes_as_files_other, spilledByBytes)
        )
        if (refusedByCount > 0) add(
            if (spilledByCount > 0) resources.getString(R.string.chat_composer_file_limit)
            else resources.getString(R.string.chat_composer_photo_limit, limits.perMessage)
        )
        if (refusedByBytes > 0) add(
            if (spilledByBytes > 0) resources.getString(R.string.chat_composer_file_limit)
            else resources.getString(R.string.chat_composer_photos_too_large)
        )
    }
}

/**
 * Admits [incoming] photos against [limits] (count `per_message` and base64 budget `request_bytes - 1 MiB`), spilling
 * overflow pictures into ordinary file attachments (`image = false, video = false`) when [canSpill] and file slots remain.
 *
 * Valid candidates are admitted in ascending scaled-JPEG byte order (breaking ties by pick index) so picker order
 * cannot starve smaller photos out of the inline body budget or file-spill slots, and original pick order is restored
 * in [ComposerPhotoPlan.acceptedPhotos], [ComposerPhotoPlan.spilledFiles], and [ComposerPhotoPlan.refused].
 */
fun planComposerPhotos(
    existing: List<ComposerAttachment>,
    incoming: List<PhotoCandidate>,
    limits: ImageLimits,
    canSpill: Boolean,
): ComposerPhotoPlan {
    val heldBase64 = existing.asSequence()
        .filter { it.image && it.uploadState != DraftUploadState.Invalid }
        .map { ImageLimits.base64Bytes(heldPhotoBytes(it, limits)) }
        .toMutableList()
    var fileSlots = composerFileRoom(existing)
    val acceptedIndexed = mutableListOf<Pair<Int, ComposerAttachment>>()
    val spilledIndexed = mutableListOf<Pair<Int, ComposerAttachment>>()
    val refusedIndexed = mutableListOf<Pair<Int, ComposerAttachment>>()
    var spilledCount = 0
    var spilledBytes = 0
    var refusedCount = 0
    var refusedBytes = 0

    val validCandidates = mutableListOf<IndexedValue<PhotoCandidate>>()
    incoming.forEachIndexed { index, candidate ->
        if (candidate.attachment.uploadState == DraftUploadState.Invalid) {
            acceptedIndexed += index to candidate.attachment
        } else {
            validCandidates += IndexedValue(index, candidate)
        }
    }

    val ordered = validCandidates.sortedWith(
        compareBy<IndexedValue<PhotoCandidate>> { candidatePhotoBytes(it.value, limits) }
            .thenBy { it.index }
    )

    for ((index, candidate) in ordered) {
        val attachment = candidate.attachment
        val effectiveBytes = candidatePhotoBytes(candidate, limits)
        val knownScaled = candidate.scaledBytes?.takeIf { it > 0L } ?: attachment.scaledBytes?.takeIf { it > 0L }
        val nextBase64 = ImageLimits.base64Bytes(effectiveBytes)
        when (val room = limits.room(heldBase64, nextBase64)) {
            ImageLimits.Room.Ok -> {
                acceptedIndexed += index to attachment.copy(
                    image = true,
                    video = false,
                    size = knownScaled ?: attachment.size,
                    scaledBytes = knownScaled,
                )
                heldBase64 += nextBase64
            }
            ImageLimits.Room.Count, ImageLimits.Room.Bytes -> {
                val withinFileCap = attachment.size == null || attachment.size <= MAX_COMPOSER_FILE_BYTES
                if (canSpill && fileSlots > 0 && withinFileCap) {
                    spilledIndexed += index to attachment.copy(image = false, video = false, scaledBytes = null)
                    fileSlots--
                    if (room == ImageLimits.Room.Count) spilledCount++ else spilledBytes++
                } else {
                    refusedIndexed += index to attachment
                    if (room == ImageLimits.Room.Count) refusedCount++ else refusedBytes++
                }
            }
        }
    }
    return ComposerPhotoPlan(
        acceptedPhotos = acceptedIndexed.sortedBy { it.first }.map { it.second },
        spilledFiles = spilledIndexed.sortedBy { it.first }.map { it.second },
        refused = refusedIndexed.sortedBy { it.first }.map { it.second },
        spilledByCount = spilledCount,
        spilledByBytes = spilledBytes,
        refusedByCount = refusedCount,
        refusedByBytes = refusedBytes,
    )
}

/**
 * Admits a viewer-edited photo into [composer] through the shared scaling/measurement and budget accounting pipeline.
 * Deletes the temporary edited capture if it cannot fit. Returns true if the photo joined the composer.
 */
internal suspend fun admitViewerEditedPhoto(
    context: Context,
    composer: ChatComposerState,
    attachment: ComposerAttachment,
    limits: ImageLimits,
    canSpill: Boolean,
    resources: Resources = context.resources,
    onNotice: (String) -> Unit = {},
): Boolean {
    val existing = refreshHeldPhotoSizes(context, composer.attachments, limits) { id, bytes ->
        composer.updateAttachment(id) { if (it.scaledBytes == null) it.copy(scaledBytes = bytes) else it }
    }
    val measured = attachment.scaledBytes?.takeIf { it > 0L } ?: withContext(Dispatchers.IO) {
        attachment.uri.takeIf { it.isNotEmpty() }?.let { measurePhotoBytes(context, Uri.parse(it), limits) }
    }
    val plan = planComposerPhotos(existing, listOf(PhotoCandidate(attachment, measured)), limits, canSpill = canSpill)
    if (plan.added.isNotEmpty()) {
        composer.beginEdit()
        composer.attachments += plan.added
    }
    if (plan.refused.isNotEmpty()) {
        withContext(Dispatchers.IO) { plan.refused.forEach { deleteCapture(context, it.uri) } }
    }
    plan.notices(resources, limits).forEach(onNotice)
    return plan.added.isNotEmpty()
}

/**
 * Visual media picker sized dynamically at launch time by remaining room. Uses single-pick when room is 1 because
 * [ActivityResultContracts.PickMultipleVisualMedia] requires `maxItems > 1`.
 */
class DynamicPickVisualMedia : ActivityResultContract<Pair<PickVisualMediaRequest, Int>, List<@JvmSuppressWildcards Uri>>() {
    private val single = ActivityResultContracts.PickVisualMedia()
    private val parser = ActivityResultContracts.PickMultipleVisualMedia(2)

    override fun createIntent(context: Context, input: Pair<PickVisualMediaRequest, Int>): Intent {
        val (request, room) = input
        if (room <= 1) return single.createIntent(context, request)
        val clamped = room.coerceIn(2, systemPickImagesMaxLimit())
        return ActivityResultContracts.PickMultipleVisualMedia(clamped).createIntent(context, request)
    }

    override fun parseResult(resultCode: Int, intent: Intent?): List<Uri> =
        parser.parseResult(resultCode, intent)

    companion object {
        internal fun systemPickImagesMaxLimit(): Int = runCatching {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ||
                (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && SdkExtensions.getExtensionVersion(Build.VERSION_CODES.R) >= 2)
            ) MediaStore.getPickImagesMaxLimit().coerceAtLeast(2)
            else 100
        }.getOrDefault(100)
    }
}

/** Resolves the hub's image limits for [agent] on [connection]'s hub group, falling back to [LocalHubImageLimits]. */
@Composable
internal fun rememberSessionImageLimits(connection: HubConnection?, agent: String?): ImageLimits {
    val fallback = LocalHubImageLimits.current
    val currentHello by (connection?.currentHello ?: remember { MutableStateFlow<SocketHello?>(null) }).collectAsStateWithLifecycle()
    val table: ImageLimitsTable = currentHello?.hello?.imageLimits ?: connection?.lastHello?.imageLimits ?: fallback
    return remember(table, agent) { table.forAgent(agent) }
}
