package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcons

/** Where a photo in the composer is: on the computer already, on its way, not started, failed, or gone. */
enum class AttachmentState { Ready, Uploading, Waiting, Failed, Invalid }

/** The side of a composer photo thumbnail (iOS and the old app use the same 64). */
val AttachmentThumbSize = 64.dp

/**
 * A photo waiting in the composer: a square thumbnail with a remove button in its corner ([onRemove], "Remove this one").
 * While it uploads it dims under a spinner; a failed upload shows a retry mark and a red rim and retries when tapped
 * ([onRetry]); an invalid one (the file is gone) shows a warning and can only be removed. A null [image] draws a
 * placeholder: still decoding, unreadable, or a photo that only exists on the computer (restored from a sent message).
 * With [onEdit] a ready photo carries a pencil in its corner and a tap marks it up (iOS ComposerImages).
 * Glass is a tile with a light rim, brief a softer paper tile, the board a small-radius tile with a rule.
 */
@Composable
fun AttachmentThumb(
    image: ImageBitmap?,
    contentDescription: String,
    modifier: Modifier = Modifier,
    state: AttachmentState = AttachmentState.Ready,
    onRemove: (() -> Unit)? = null,
    onRetry: (() -> Unit)? = null,
    onEdit: (() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(when (style) { AppStyle.Glass -> 16.dp.corner; AppStyle.Brief -> 12.dp.corner; AppStyle.Board -> 6.dp.corner })
    val failed = state == AttachmentState.Failed || state == AttachmentState.Invalid
    val rim = when {
        failed -> Modifier.border(1.5.dp, palette.danger.dot, shape)
        style == AppStyle.Glass -> Modifier.border(1.dp, Brush.verticalGradient(listOf(palette.strokeTop, palette.strokeBottom)), shape)
        style == AppStyle.Board -> Modifier.border(1.dp, palette.line, shape)
        else -> Modifier
    }
    val retry = onRetry.takeIf { state == AttachmentState.Failed }
    val edit = onEdit.takeIf { state == AttachmentState.Ready }
    val label = when (state) {
        AttachmentState.Ready -> null
        AttachmentState.Uploading -> R.string.designsystem_attachment_uploading
        AttachmentState.Waiting -> R.string.designsystem_attachment_waiting
        AttachmentState.Failed -> R.string.designsystem_attachment_failed
        AttachmentState.Invalid -> R.string.designsystem_attachment_invalid
    }?.let { stringResource(R.string.designsystem_with_state, contentDescription, stringResource(it)) } ?: contentDescription
    val retryLabel = stringResource(R.string.designsystem_retry)
    val removeLabel = stringResource(R.string.designsystem_attachment_remove)
    val editLabel = stringResource(R.string.designsystem_attachment_edit)
    Box(modifier.size(AttachmentThumbSize)) {
        Box(
            Modifier.matchParentSize().clip(shape).background(if (style == AppStyle.Glass) palette.surfaceStrong else palette.surface)
                .then(when {
                    retry != null -> Modifier.clickable(role = Role.Button, onClickLabel = retryLabel, onClick = retry)
                    edit != null -> Modifier.clickable(role = Role.Button, onClickLabel = editLabel, onClick = edit)
                    else -> Modifier
                })
                .semantics { this.contentDescription = label },
            contentAlignment = Alignment.Center,
        ) {
            if (image != null) Image(image, null, Modifier.matchParentSize(), contentScale = ContentScale.Crop)
            else JIcon(PhIcons.Image, null, size = 24.dp, tint = palette.ink3)
            if (state != AttachmentState.Ready) {
                // Marks sit on the photo itself, so they use photo-overlay colours in every style and mode.
                Box(Modifier.matchParentSize().background(OnPhotoScrim))
                when (state) {
                    AttachmentState.Uploading -> JSpinner(Modifier.size(22.dp), color = OnPhoto, stroke = 2.dp)
                    AttachmentState.Waiting -> JIcon(PhIcons.Clock, null, size = 22.dp, tint = OnPhoto)
                    AttachmentState.Failed -> Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(2.dp.dense)) {
                        JIcon(PhIcons.ArrowClockwise, null, size = 20.dp, tint = OnPhoto)
                        Text(retryLabel, style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.Bold), color = OnPhoto, maxLines = 1)
                    }
                    else -> JIcon(PhIcons.WarningCircle, null, size = 22.dp, tint = OnPhoto)
                }
            }
        }
        // The rim goes over the photo, so a photo's own edge never hides it.
        Box(Modifier.matchParentSize().then(rim))
        if (edit != null) Box(
            Modifier.align(Alignment.BottomStart).padding(4.dp.dense).size(20.dp).clip(CircleShape).background(OnPhotoBadge),
            contentAlignment = Alignment.Center,
        ) { JIcon(PhIcons.PencilSimple, null, size = 12.dp, tint = OnPhoto) }
        if (onRemove != null) Box(
            Modifier.align(Alignment.TopEnd).size(32.dp).clip(CircleShape)
                .clickable(role = Role.Button, onClick = onRemove).semantics { this.contentDescription = removeLabel },
            contentAlignment = Alignment.TopEnd,
        ) {
            Box(
                Modifier.padding(4.dp.dense).size(20.dp).clip(if (style == AppStyle.Board) RoundedCornerShape(4.dp) else CircleShape).background(OnPhotoBadge),
                contentAlignment = Alignment.Center,
            ) { JIcon(PhIcons.X, null, size = 12.dp, tint = OnPhoto) }
        }
    }
}

// What is drawn on top of a photo: it can be any colour, so these do not follow the style's palette.
private val OnPhoto = Color.White
private val OnPhotoScrim = Color.Black.copy(alpha = 0.42f)
private val OnPhotoBadge = Color.Black.copy(alpha = 0.58f)
