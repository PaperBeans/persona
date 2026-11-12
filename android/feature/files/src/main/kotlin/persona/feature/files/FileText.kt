package persona.feature.files

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.JLoading
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext
import persona.core.designsystem.dense

internal enum class FileLineKind { Code, Context, Add, Delete, Info }
internal data class FileTextLine(val text: String, val old: Int? = null, val new: Int? = null, val kind: FileLineKind = FileLineKind.Code)

internal fun fileLines(text: String, diff: Boolean): List<FileTextLine> {
    if (!diff) return text.split('\n').mapIndexed { index, line -> FileTextLine(line.removeSuffix("\r").replace("\t", "    "), new = index + 1) }
    var old = 0
    var new = 0
    var inHunk = false
    val header = Regex("^@@ -(\\d+)(?:,\\d+)? \\+(\\d+)(?:,\\d+)? @@.*")
    // A file's header (diff --git, index, ---, +++) only repeats the page's title; inside a hunk "--- x" is a removed line.
    val fileHeader = listOf("diff --git ", "index ", "--- ", "+++ ")
    return text.split('\n').mapNotNull { raw ->
        val line = raw.removeSuffix("\r").replace("\t", "    ")
        val match = header.matchEntire(line)
        when {
            match != null -> {
                old = match.groupValues[1].toInt(); new = match.groupValues[2].toInt(); inHunk = true
                FileTextLine(line, kind = FileLineKind.Info)
            }
            line.startsWith("diff --git ") -> { inHunk = false; null }
            !inHunk && fileHeader.any { line.startsWith(it) } -> null
            inHunk && line.startsWith('+') -> FileTextLine(line.drop(1), new = new++, kind = FileLineKind.Add)
            inHunk && line.startsWith('-') -> FileTextLine(line.drop(1), old = old++, kind = FileLineKind.Delete)
            inHunk && line.startsWith(' ') -> FileTextLine(line.drop(1), old = old++, new = new++, kind = FileLineKind.Context)
            else -> FileTextLine(line, kind = FileLineKind.Info)
        }
    }
}

// One ordered scan keeps comment and string tokens from receiving overlapping keyword spans.
private val syntax = Regex("\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'|//.*|#.*|\\b(?:fun|func|val|var|let|const|class|struct|interface|enum|object|import|package|return|if|else|for|while|when|switch|case|break|continue|try|catch|throw|throws|async|await|suspend|public|private|internal|static|final|override|def|from|in|true|false|null|nil|None|True|False)\\b|\\b\\d+(?:\\.\\d+)?\\b")

internal fun highlighted(text: String, keyword: Color, string: Color, comment: Color): AnnotatedString {
    val result = AnnotatedString.Builder(text)
    syntax.findAll(text).forEach { match ->
        val color = when {
            match.value.startsWith("//") || match.value.startsWith('#') -> comment
            match.value.startsWith('"') || match.value.startsWith('\'') -> string
            else -> keyword
        }
        result.addStyle(SpanStyle(color), match.range.first, match.range.last + 1)
    }
    return result.toAnnotatedString()
}

/**
 * The row of [line] (1-based) among [count] lines, as far as the text goes (a file cut at 2 MB ends before it), or
 * null when there is no line to go to.
 */
internal fun focusRow(line: Int?, count: Int): Int? = if (line == null || line < 1 || count < 1) null else minOf(line, count) - 1

/** The list item to scroll to for [row]: the rows come after the page's [headers] items. */
internal fun focusItem(row: Int, headers: Int): Int = headers.coerceAtLeast(0) + row

/**
 * Each visible row shares the same horizontal offset and width; gutters stay fixed. [focusLine] (1-based, not for a
 * diff) is scrolled to a third of the way down and marked, as iOS `CodeLinesView.focus`.
 */
@Composable
internal fun FileText(text: String, diff: Boolean = false, modifier: Modifier = Modifier, contentPadding: PaddingValues = PaddingValues(0.dp),
    focusLine: Int? = null, header: LazyListScope.() -> Unit = {}) {
    val measurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val style = PersonaTheme.type.code
    val colors = PersonaTheme.palette
    val normal = colors.ink
    val muted = colors.ink3
    val keyword = colors.accent
    val string = colors.done.text
    data class Prepared(val lines: List<FileTextLine>, val spans: List<AnnotatedString>, val widthPx: Int)
    val prepared by produceState<Prepared?>(null, text, diff, density, style, keyword, string, muted) {
        value = null
        value = withContext(Dispatchers.Default) {
            val lines = fileLines(text, diff)
            val spans = lines.map { if (diff) AnnotatedString(it.text) else highlighted(it.text, keyword, string, muted) }
            val width = lines.maxOfOrNull { measurer.measure(it.text.ifEmpty { " " }, style, softWrap = false, maxLines = 1).size.width } ?: 0
            Prepared(lines, spans, width)
        }
    }
    val data = prepared
    if (data == null) {
        Box(modifier.fillMaxSize().padding(contentPadding), contentAlignment = androidx.compose.ui.Alignment.Center) { JLoading() }
        return
    }
    val horizontal = rememberScrollState()
    val list = rememberLazyListState()
    val focus = if (diff) null else focusRow(focusLine, data.lines.size)
    LaunchedEffect(data, focus) {
        if (focus == null) return@LaunchedEffect
        // The header's items are known once the list has laid out: the rows follow them.
        val total = snapshotFlow { list.layoutInfo.totalItemsCount }.first { it >= data.lines.size }
        list.scrollToItem(focusItem(focus, total - data.lines.size))
        list.scrollBy(-list.layoutInfo.viewportSize.height / 3f)
    }
    val gutter = if (diff) 104.dp else 60.dp
    BoxWithConstraints(modifier.background(colors.sunken)) {
        val width = maxOf(with(density) { data.widthPx.toDp() } + 24.dp, (maxWidth - gutter).coerceAtLeast(1.dp))
        SelectionContainer {
            LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = contentPadding) {
                header()
                itemsIndexed(data.lines, key = { index, _ -> index }, contentType = { _, _ -> "line" }) { index, line ->
                    val tone = when (line.kind) { FileLineKind.Add -> colors.diffAdd; FileLineKind.Delete -> colors.diffDel; else -> muted }
                    // The requested line is a stronger neutral well; diff signs carry additions and deletions.
                    val band = if (index == focus) colors.statusSurface else colors.sunken
                    Row(Modifier.fillMaxWidth().background(band)) {
                        val marker = when (line.kind) { FileLineKind.Add -> "+"; FileLineKind.Delete -> "−"; else -> " " }
                        val number = if (diff) "${line.old?.toString().orEmpty().padStart(4)} ${line.new?.toString().orEmpty().padStart(4)} $marker"
                            else line.new?.toString().orEmpty()
                        Text(number, Modifier.width(gutter).padding(end = 8.dp.dense), style = style,
                            color = if (diff) tone else muted, textAlign = TextAlign.End, maxLines = 1, softWrap = false)
                        Box(Modifier.weight(1f).horizontalScroll(horizontal)) {
                            Text(data.spans[index], Modifier.width(width).padding(end = 16.dp.dense), style = style,
                                color = if (line.kind == FileLineKind.Info) muted else normal, maxLines = 1, softWrap = false)
                        }
                    }
                }
            }
        }
    }
}
