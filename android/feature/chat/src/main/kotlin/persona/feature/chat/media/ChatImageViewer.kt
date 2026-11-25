package persona.feature.chat.media

import androidx.compose.ui.platform.LocalResources
import android.content.Context
import android.content.res.Resources
import android.graphics.Bitmap
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import persona.core.contracts.ImageLimits
import persona.core.designsystem.ImageEditor
import persona.core.designsystem.ImageViewer
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.ViewerAction
import persona.core.designsystem.ViewerImage
import persona.core.designsystem.decodePicture
import persona.core.designsystem.icons.PhIcons
import persona.feature.chat.ComposerAttachment
import persona.feature.chat.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The image viewer of a conversation: [ImageViewer] with "Save", "Edit" (only with [onEdited] or [onAdmitEdited]),
 * "Share" and "Copy".
 *
 * "Edit" opens the markup editor on the current picture; "Done" encodes it under [imageLimits] (in
 * `cache/chat-captures/`), hands it to [onAdmitEdited] (or [onEdited]), says "Added to the composer, add a line to
 * send" when admitted and closes the viewer through [onDismiss]. With [photoLimitReached] "Edit" says
 * [R.string.chat_viewer_photo_limit] with `imageLimits.perMessage` instead. Results of "Save" / "Share" / "Copy"
 * show inside the viewer, which stays open.
 */
@Composable
fun ChatImageViewer(
    images: List<ViewerImage>,
    initialIndex: Int,
    onDismiss: () -> Unit,
    /** null: no "Edit" button (unless [onAdmitEdited] is given). Gets the marked-up photo; the viewer closes after. */
    onEdited: ((ComposerAttachment) -> Unit)? = null,
    /** Suspending admission hook for the marked-up photo; returns true if added to the composer. */
    onAdmitEdited: (suspend (ComposerAttachment) -> Boolean)? = null,
    /** No room remains in the composer for another photo: "Edit" reports the limit instead of opening the editor. */
    photoLimitReached: Boolean = false,
    /** The hub's picture limits (per-message count, max long side, per-image byte cap for step-down). */
    imageLimits: ImageLimits = ImageLimits.Standard,
) {
    val context = LocalContext.current
    val res = LocalResources.current
    val feedback = LocalAppFeedback.current
    val media = remember(context) { AndroidChatMedia(context) }
    var editing by remember { mutableStateOf<EditRequest?>(null) }
    val edited by rememberUpdatedState(onEdited)
    val admitEdited by rememberUpdatedState(onAdmitEdited)
    val dismiss by rememberUpdatedState(onDismiss)
    val limitReached by rememberUpdatedState(photoLimitReached)
    val currentLimits by rememberUpdatedState(imageLimits)

    // Runs as the "Edit" action: the action stays busy while the editor is open, and what it returns shows in the viewer.
    suspend fun edit(bytes: ByteArray): String? {
        val source = try {
            withContext(Dispatchers.Default) { decodePicture(bytes, currentLimits.maxSide, atMost = false, software = true) }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Throwable) {
            return res.getString(R.string.chat_viewer_unreadable)
        }
        val request = EditRequest(source)
        editing = request
        try {
            val result = request.result.await() ?: return null
            // The composer may have filled up while the editor was open.
            if (limitReached) return res.getString(R.string.chat_viewer_photo_limit, currentLimits.perMessage)
            val attachment = try {
                media.writeEdited(result, currentLimits)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Throwable) {
                return res.getString(R.string.chat_viewer_not_attached)
            }
            val added = admitEdited?.invoke(attachment) ?: run {
                edited?.invoke(attachment)
                true
            }
            // The viewer closes right away, so this one goes to the app's own notices.
            if (added) feedback?.notice(res.getString(R.string.chat_viewer_attached))
            dismiss()
            return null
        } finally {
            if (editing === request) editing = null
        }
    }

    val editAction: suspend (ByteArray) -> String? = { bytes -> edit(bytes) }
    val hasEdit = onAdmitEdited != null || onEdited != null
    val actions = chatViewerActions(res, media, { limitReached }, editAction.takeIf { hasEdit }, maxPhotos = currentLimits.perMessage)
    ImageViewer(images, initialIndex, onDismiss, actions)
    editing?.let { request ->
        key(request) {
            ImageEditor(request.source, onCancel = { request.result.complete(null) }, onDone = { request.result.complete(it) })
        }
    }
}

/** One open editor: the picture it works on and what "Done" / "Cancel" gave (null: cancelled). */
private class EditRequest(val source: Bitmap) {
    val result = CompletableDeferred<Bitmap?>()
}

/** What the viewer's actions need from the phone; [AndroidChatMedia] does it for real, tests fake it. */
internal interface ChatMedia {
    suspend fun save(bytes: ByteArray): Boolean
    suspend fun share(bytes: ByteArray): Boolean
    suspend fun copy(bytes: ByteArray): Boolean
}

internal class AndroidChatMedia(private val context: Context) : ChatMedia {
    override suspend fun save(bytes: ByteArray) = MediaShare.saveImage(context, bytes)
    override suspend fun share(bytes: ByteArray) = MediaShare.shareImage(context, bytes)
    override suspend fun copy(bytes: ByteArray) = MediaShare.copyImage(context, bytes)
    suspend fun writeEdited(picture: Bitmap, limits: ImageLimits = ImageLimits.Standard): ComposerAttachment =
        MediaShare.writeEditedPhoto(context, picture, limits)
}

/**
 * The viewer's actions in order: "Save", "Edit" (when [edit] is given), "Share", "Copy". Each returns the notice the viewer
 * shows; a successful share shows none (the share sheet is the answer). "Edit" checks [limitReached] first.
 */
internal fun chatViewerActions(
    res: Resources,
    media: ChatMedia,
    limitReached: () -> Boolean,
    edit: (suspend (ByteArray) -> String?)?,
    maxPhotos: Int = ImageLimits.DEFAULT_PER_MESSAGE,
): List<ViewerAction> = buildList {
    fun text(id: Int) = res.getString(id)
    add(ViewerAction(text(R.string.chat_viewer_save), PhIcons.ArrowDown) { _, bytes ->
        text(if (media.save(bytes)) R.string.chat_viewer_saved else R.string.chat_viewer_save_failed)
    })
    if (edit != null) add(ViewerAction(text(R.string.chat_viewer_edit), PhIcons.PencilSimple) { _, bytes ->
        if (limitReached()) res.getString(R.string.chat_viewer_photo_limit, maxPhotos) else edit(bytes)
    })
    add(ViewerAction(text(R.string.chat_viewer_share), PhIcons.ShareNetwork) { _, bytes ->
        if (media.share(bytes)) null else text(R.string.chat_viewer_share_failed)
    })
    add(ViewerAction(text(R.string.chat_viewer_copy), PhIcons.Copy) { _, bytes ->
        text(if (media.copy(bytes)) R.string.chat_viewer_copied else R.string.chat_viewer_copy_failed)
    })
}
