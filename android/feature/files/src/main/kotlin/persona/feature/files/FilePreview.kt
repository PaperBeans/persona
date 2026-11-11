package persona.feature.files

import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import coil3.compose.AsyncImage
import coil3.decode.BitmapFactoryDecoder
import coil3.request.ImageRequest
import coil3.compose.AsyncImagePainter
import persona.core.contracts.api.SessionFileContent
import persona.core.contracts.api.SessionFiles
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.Base64

/**
 * A file or a working-tree diff over the browser: Markdown rendered (or its source), code and text with line numbers,
 * images to zoom, PDFs page by page, anything else described. ⋯ copies, shares and reloads.
 */
@Composable
internal fun FilePreview(state: FilesState, controller: FilesController, images: MarkdownImages, insert: (String) -> Unit, back: () -> Unit) {
    val selection = state.selection ?: return
    val context = LocalContext.current
    val resources = LocalResources.current
    val feedback = LocalAppFeedback.current
    val copy = rememberCopy()
    val scope = rememberCoroutineScope()
    var menu by remember(selection) { mutableStateOf(false) }
    val file = state.content
    val markdown = file?.text != null && FilesPaths.isMarkdown(file.name)
    val table = file != null && isTable(file)
    // A CSV / TSV is text too: ⋯ switches between the grid and its source.
    val delimited = table && file?.text != null
    val notebook = file?.text != null && Notebooks.isNotebook(file.name)
    val html = file?.text != null && HtmlPreview.isHtml(file.name)
    // Per file and only while it's open: scripts and the network come back off next time.
    var policy by remember(file?.path) { mutableStateOf(HtmlPreview.Policy()) }
    // Opened at a line (a message's `path:12`): the source, where lines are (iOS `goTo(line)`); ⋯ still switches back.
    val line = (selection as? FileSelection.File)?.line
    var source by rememberSaveable(selection.toString()) { mutableStateOf(line != null) }
    val path = when (selection) {
        is FileSelection.File -> file?.path?.ifEmpty { null } ?: selection.path
        is FileSelection.Diff -> joinRemotePath(state.git?.root ?: controller.cwd, selection.file.path)
        else -> null
    }
    val title = when (selection) {
        is FileSelection.Diff -> selection.file.name
        else -> file?.name?.ifEmpty { null } ?: path?.let(FilesPaths::name) ?: stringResource(R.string.files_title)
    }
    JPage(title, subtitle = path, onBack = back, actions = {
        if (path != null) JTextButton(stringResource(R.string.files_insert_path), { insert(path) })
        JIconButton(PhIcons.DotsThree, stringResource(R.string.files_preview_options), { menu = true })
    }) { padding ->
        val diff = state.diff
        val error = state.previewError
        val header: LazyListScope.() -> Unit = {
            if (selection is FileSelection.Diff && selection.file.staged && selection.file.unstaged) item("staging") {
                JSegmented(listOf(JSegment(stringResource(R.string.files_git_unstaged)), JSegment(stringResource(R.string.files_git_staged))), if (selection.staged) 1 else 0,
                    { controller.preview(selection.copy(staged = it == 1)) }, Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
            }
            // A picture, PDF, video or document fetched whole is not "only the first 2 MB".
            if (file != null) item("size") { FileMeta(file.size, file.truncated && !fetchedWhole(file)) }
            if (diff?.truncated == true) item("truncated") { Notice(stringResource(R.string.files_diff_truncated)) }
        }
        when {
            error != null -> LazyColumn(contentPadding = padding) {
                item { RetryState(if (selection is FileSelection.Diff) stringResource(R.string.files_diff_failed) else stringResource(R.string.files_file_failed), error, controller::reload) }
            }
            file != null && table && !source -> key(file.path) { SheetFile(file, padding, header, controller::download) }
            file != null && notebook && !source -> key(file.path) {
                NotebookFile(file.text.orEmpty(), FilesPaths.parent(file.path) ?: controller.cwd, padding, header,
                    onOpenPath = { controller.push(FileSelection.File(it)) }, loadImage = images::load, truncated = file.truncated)
            }
            file != null && html && !source -> key(file.path) {
                Column(Modifier.fillMaxSize().padding(padding)) {
                    FileMeta(file.size, file.truncated)
                    HtmlFile(file, policy, Modifier.weight(1f))
                }
            }
            file != null -> key(file.path) { FileBody(file, markdown && !source, padding, header, controller, images, line) }
            diff != null -> key(selection) {
                if (diff.diff.isBlank()) LazyColumn(contentPadding = padding) {
                    header(); item { JEmptyState(stringResource(R.string.files_diff_no_text), stringResource(R.string.files_diff_no_text_message)) }
                } else FileText(diff.diff, diff = true, contentPadding = padding, header = header)
            }
            else -> LazyColumn(contentPadding = padding) { item { Loading() } }
        }
    }
    if (menu) JActionSheet(buildList {
        if (markdown) add(JAction(if (source) stringResource(R.string.files_render_markdown) else stringResource(R.string.files_view_source), if (source) PhIcons.FileText else PhIcons.Code) { source = !source })
        if (delimited) add(JAction(if (source) stringResource(R.string.files_view_table) else stringResource(R.string.files_view_source), if (source) PhIcons.SquaresFour else PhIcons.Code) { source = !source })
        if (notebook) add(JAction(if (source) stringResource(R.string.files_view_notebook) else stringResource(R.string.files_view_source), if (source) PhIcons.BookOpen else PhIcons.Code) { source = !source })
        if (html) {
            add(JAction(if (source) stringResource(R.string.files_preview_web) else stringResource(R.string.files_view_source), if (source) PhIcons.GlobeSimple else PhIcons.Code) { source = !source })
            if (!source) {
                add(JAction(stringResource(R.string.files_allow_scripts), PhIcons.Lightning, checked = policy.scripts) { policy = policy.copy(scripts = !policy.scripts) })
                add(JAction(stringResource(R.string.files_allow_network), PhIcons.WifiHigh, checked = policy.network) { policy = policy.copy(network = !policy.network) })
            }
        }
        if (path != null) add(JAction(stringResource(R.string.files_copy_path), PhIcons.Copy) { copy(path, resources.getString(R.string.files_path_copied)) })
        file?.text?.let { text -> add(JAction(stringResource(R.string.files_copy_content), PhIcons.Copy) { copy(text, resources.getString(R.string.files_content_copied)) }) }
        state.diff?.diff?.takeIf { it.isNotEmpty() }?.let { text -> add(JAction(stringResource(R.string.files_copy_diff), PhIcons.Copy) { copy(text, resources.getString(R.string.files_diff_copied)) }) }
        if (file != null && FileShare.canShare(file)) add(JAction(stringResource(R.string.files_share), PhIcons.ShareNetwork) {
            scope.launch {
                val shared = runCatching { FileShare.share(context, file, controller::download) }.getOrDefault(false)
                if (!shared) feedback?.notice(resources.getString(R.string.files_share_failed))
            }
        })
        add(JAction(stringResource(R.string.files_refresh), PhIcons.ArrowClockwise, separatorBefore = true) { controller.reload() })
    }, { menu = false }, title)
}

/** CSV / TSV (as text or bytes) and xlsx (bytes) open in the table viewer ([SheetFile]). */
internal fun isTable(file: SessionFileContent): Boolean =
    TableFiles.isDelimited(file.name) && (file.text != null || file.data != null) || TableFiles.isWorkbook(file.name) && (file.data != null || file.stream)

@Composable
private fun FileBody(file: SessionFileContent, rendered: Boolean, padding: PaddingValues, header: LazyListScope.() -> Unit,
    controller: FilesController, images: MarkdownImages, line: Int? = null) {
    val text = file.text
    val mime = file.mime.orEmpty()
    when {
        text != null && rendered -> FileMarkdown(text, FilesPaths.parent(file.path) ?: controller.cwd, padding,
            onOpenPath = { controller.push(FileSelection.File(it)) }, loadImage = images::load, header = header)
        text != null || file.size == 0L -> FileText(text.orEmpty(), contentPadding = padding, focusLine = line, header = header)
        WholeFiles.isImage(file) && WholeFiles.hasBytes(file) -> Column(Modifier.fillMaxSize().padding(padding)) {
            FileMeta(file.size, false)
            if (WholeFiles.carried(file)) FileImage(file, Modifier.weight(1f))
            else {
                // Streamed (stream=1) or over 2 MB on an older hub: the picture comes whole from file/download.
                val whole by rememberWholeFile(file, "files-image", { into -> controller.download(file.path, into) })
                when (val load = whole) {
                    WholeLoad.Fetching -> Box(Modifier.weight(1f).fillMaxWidth()) { JLoading(text = stringResource(R.string.files_image_loading)) }
                    is WholeLoad.Failed -> JEmptyState(stringResource(R.string.files_image_failed), load.reason)
                    is WholeLoad.Ready -> FileImage(file, Modifier.weight(1f), load.file)
                }
            }
        }
        WholeFiles.isPdf(file) && WholeFiles.hasBytes(file) -> {
            val whole = if (WholeFiles.carried(file)) null
                else rememberWholeFile(file, "files-pdf-whole", { into -> controller.download(file.path, into) }).value
            PdfPages(file, padding, header, whole, fetching = stringResource(R.string.files_pdf_loading))
        }
        WholeFiles.isVideo(file) -> VideoFile(file, controller::download, padding, header)
        WholeFiles.isOffice(file) -> {
            // Word, PowerPoint, iWork: the PDF the machine's LibreOffice writes (web OfficeView's fallback, file/convert).
            val whole by rememberWholeFile(file, "files-office", { into -> controller.convertToPdf(file.path, into) }, always = true, converting = true)
            PdfPages(file, padding, header, whole, fetching = stringResource(R.string.files_office_converting),
                failedTitle = stringResource(R.string.files_office_failed))
        }
        AudioFiles.isAudio(file) -> AudioFile(file, controller::download, padding, header)
        else -> LazyColumn(contentPadding = padding) {
            header()
            item {
                JEmptyState(stringResource(R.string.files_no_preview),
                    stringResource(R.string.files_no_preview_message, mime.ifEmpty { stringResource(R.string.files_binary_file) }, fileSize(file.size, currentLocale)))
            }
        }
    }
}

/** The size line above a file's content: the size, and that only the first 2 MB came when the file is bigger. */
@Composable
internal fun FileMeta(size: Long, truncated: Boolean) {
    val text = fileSize(size, currentLocale)
    Notice(if (truncated) stringResource(R.string.files_size_truncated, text) else text)
}

/** Reads images a Markdown file names from the same machine (the hub's file read, base64 bytes). */
internal class MarkdownImages(private val api: SessionFiles, private val machineId: String, private val cwd: String) {
    private val cache = object : LinkedHashMap<String, ByteArray>(8, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ByteArray>?) = size > 12
    }

    suspend fun load(path: String): ByteArray? {
        synchronized(cache) { cache[path] }?.let { return it }
        val file = runCatching { api.file(machineId, cwd, path) }.getOrNull() ?: return null
        val mime = file.mime.orEmpty()
        if (file.data == null || !mime.startsWith("image/") || mime.startsWith("image/svg")) return null
        val bytes = withContext(Dispatchers.Default) { runCatching { Base64.getMimeDecoder().decode(file.data) }.getOrNull() } ?: return null
        synchronized(cache) { cache[path] = bytes }
        return bytes
    }
}

/** Whether the page shows [file] from its whole bytes rather than the preview's first 2 MB. */
internal fun fetchedWhole(file: SessionFileContent): Boolean =
    WholeFiles.isImage(file) || WholeFiles.isPdf(file) || WholeFiles.isVideo(file) || WholeFiles.isOffice(file) || AudioFiles.isAudio(file)

/** A picture to zoom: the preview's bytes, or [whole] (the file fetched whole). */
@Composable
private fun FileImage(file: SessionFileContent, modifier: Modifier, whole: java.io.File? = null) {
    // A file fetched whole goes to Coil as a file (decoded with downsampling, never read whole onto the heap); only a
    // preview that carries the picture is decoded from its base64.
    val bytes by produceState<ByteArray?>(null, file.data, whole) {
        if (whole != null) return@produceState
        value = withContext(Dispatchers.Default) {
            try { Base64.getMimeDecoder().decode(file.data.orEmpty()) } catch (_: IllegalArgumentException) { byteArrayOf() }
        }
    }
    val context = LocalContext.current
    // From a file, BitmapFactory decodes a stream at the size drawn (ImageDecoder's file source isn't needed for that).
    val model: Any? = if (whole != null) remember(whole) {
        ImageRequest.Builder(context).data(whole).decoderFactory(BitmapFactoryDecoder.Factory()).build()
    } else bytes
    var scale by remember(file.path) { mutableFloatStateOf(1f) }
    var offset by remember(file.path) { mutableStateOf(Offset.Zero) }
    var failed by remember(file.path) { mutableStateOf(false) }
    val gestures = rememberTransformableState { zoom, pan, _ ->
        scale = (scale * zoom).coerceIn(1f, 6f)
        offset = if (scale == 1f) Offset.Zero else offset + pan
    }
    Box(modifier.fillMaxWidth().clipToBounds()) {
        when {
            model == null -> Loading()
            (model as? ByteArray)?.isEmpty() == true || failed -> JEmptyState(stringResource(R.string.files_image_failed), stringResource(R.string.files_image_failed_message))
            else -> AsyncImage(model = model, contentDescription = file.name, contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize().transformable(gestures).graphicsLayer {
                    scaleX = scale; scaleY = scale; translationX = offset.x; translationY = offset.y
                }, onState = { failed = it is AsyncImagePainter.State.Error })
        }
    }
}
