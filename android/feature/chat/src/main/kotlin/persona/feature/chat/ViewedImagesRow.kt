package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import coil3.ImageLoader
import coil3.compose.AsyncImage
import persona.core.contracts.SessionKey
import persona.core.designsystem.FileCardRow
import persona.core.designsystem.FileThumbnails
import persona.core.designsystem.JIcon
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.ViewerImage
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.rememberCopy
import persona.core.domain.lines.ViewedImage

/**
 * The images a turn's work looked at, between its work line and its answer (iOS `ViewedImagesRow`, [ViewedImage]):
 * thumbnails as tall as a card in a strip that scrolls sideways, one alone a little bigger. Nothing written around them: a
 * tap opens the viewer on this turn's images; what each is (its name, or what took it, and the machine or its size) is
 * its spoken label, and a long press on a file's copies its path.
 */
@Composable
internal fun ViewedImagesRow(images: List<ViewedImage>, session: SessionKey, loader: ImageLoader) {
    if (images.isEmpty()) return
    val actions = LocalChatRowActions.current
    val res = LocalResources.current
    val open: (Int) -> Unit = { index ->
        actions?.openImages?.invoke(images.map { viewerImage(it, res, actions) }, index)
    }
    if (images.size == 1) Box(Modifier.fillMaxWidth()) { ViewedImageTile(images[0], Tokens.Size.box80, session, loader) { open(0) } }
    else FileCardRow {
        images.forEachIndexed { index, image -> ViewedImageTile(image, Tokens.Size.box64, session, loader) { open(index) } }
    }
}

/** A thumbnail is as wide as its picture, between three quarters and 1.6 of its height (iOS `tileWidth`). */
internal fun viewedTileWidth(aspect: Float?, height: Dp): Dp = height * (aspect ?: 1f).coerceIn(0.75f, 1.6f)

/** The name of the file, or what took the picture (iOS `ViewedImagesRow.title`). */
internal fun viewedTitle(image: ViewedImage, res: Resources): String = when (image.source) {
    ViewedImage.Source.Screenshot -> res.getString(R.string.chat_viewed_screenshot)
    ViewedImage.Source.Browser -> res.getString(R.string.chat_viewed_browser)
    else -> image.name ?: if (image.source == ViewedImage.Source.Mcp) image.tool.substringAfterLast("__") else res.getString(R.string.chat_viewed_image)
}

/**
 * What the tile is called and the viewer says (iOS `ViewedImagesRow.caption`): its name, then the machine of a screenshot
 * or the picture's size ("Screenshot · Studio", "home.png · 480×300").
 */
internal fun viewedCaption(image: ViewedImage, res: Resources): String {
    val detail = image.machine ?: image.width?.let { w -> image.height?.let { h -> "$w×$h" } }
    return listOfNotNull(viewedTitle(image, res), detail).joinToString(" · ")
}

/** The viewer's page for [image]: its image item from the hub, or a file's largest thumbnail from the machine. */
private fun viewerImage(image: ViewedImage, res: Resources, actions: ChatRowActions): ViewerImage {
    val caption = viewedCaption(image, res)
    image.item?.let { item -> return actions.viewerImage(item, caption) }
    val path = image.path.orEmpty()
    return ViewerImage("viewed-" + image.id, caption) {
        actions.thumbnails?.invoke(path)?.second?.invoke(VIEWER_PIXELS) ?: error("picture unavailable")
    }
}

/** The long edge file/thumb gives at most. */
private const val VIEWER_PIXELS = 1024

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ViewedImageTile(image: ViewedImage, height: Dp, session: SessionKey, loader: ImageLoader, onClick: () -> Unit) {
    val palette = PersonaTheme.palette
    val res = LocalResources.current
    val actions = LocalChatRowActions.current
    val copy = rememberCopy()
    val shape = RoundedCornerShape(Tokens.Radius.control)
    val caption = viewedCaption(image, res)
    val view = stringResource(R.string.chat_viewed_view)
    val copyLabel = stringResource(R.string.chat_file_copy_path)
    val copied = stringResource(R.string.chat_file_path_copied)
    val thumbnail = image.path?.takeIf { image.item == null }?.let { actions?.thumbnails?.invoke(it) }
    val pixels = with(LocalDensity.current) { (height * 1.6f).roundToPx() }.coerceIn(32, VIEWER_PIXELS)
    val picture by produceState(thumbnail?.let { FileThumbnails.cached(it.first) }, thumbnail?.first) {
        if (value == null && thumbnail != null) value = FileThumbnails.picture(thumbnail.first, pixels, thumbnail.second)
    }
    val aspect = image.aspect ?: picture?.let { it.width.toFloat() / it.height.coerceAtLeast(1) }
    Box(
        Modifier.size(viewedTileWidth(aspect, height), height).clip(shape).background(palette.sunken)
            .border(Tokens.Size.hairline, palette.line, shape)
            .combinedClickable(role = Role.Image, onClickLabel = view, onClick = onClick,
                onLongClickLabel = image.path?.let { copyLabel }, onLongClick = image.path?.let { path -> { copy(path, copied) } })
            .semantics { contentDescription = caption },
    ) {
        // What took it, until its picture covers it (or for good, when there is none to show).
        if (picture == null) JIcon(sourceIcon(image), null, Modifier.align(Alignment.Center), size = Tokens.Size.iconXS, tint = palette.ink3,
            decorative = true)
        image.item?.let { item ->
            AsyncImage(ChatImage(session, item), null, imageLoader = loader, modifier = Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        }
        picture?.let { Image(it, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
        // A screenshot's or a web page's small mark in the corner.
        if (image.source == ViewedImage.Source.Screenshot || image.source == ViewedImage.Source.Browser) Box(
            Modifier.align(Alignment.BottomStart).padding(Tokens.Space.xs).size(Tokens.Size.iconXS).clip(CircleShape)
                .background(palette.surfaceOpaque),
            contentAlignment = Alignment.Center,
        ) { JIcon(sourceIcon(image), null, size = Tokens.Size.markS, tint = palette.ink2, decorative = true) }
    }
}

private fun sourceIcon(image: ViewedImage): PhIcon = when (image.source) {
    ViewedImage.Source.Screenshot -> PersonaIcons.screen
    ViewedImage.Source.Browser -> PersonaIcons.safari
    else -> PersonaIcons.photo
}
