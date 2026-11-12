package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * One suggestion for what is being typed: the [word] to type, what it does; [destructive] marks /clear. [section] is the
 * title of the group it belongs to, drawn above the first of a run (the @ list's "Message a Task"); [status] puts that
 * state's dot before the word (a task's).
 */
@Immutable
data class ComposerSuggestion(
    val word: String, val detail: String, val destructive: Boolean = false,
    val key: String = word, val flexible: Boolean = false,
    val section: String? = null, val status: Status? = null,
)

/**
 * Suggestions for the word being typed (the "/" quick messages), above the field inside the composer's accessory: the
 * word in bold, what it does beside it, one line each; picking one calls [onPick]. Glass is a soft well inside the
 * floating composer, brief a paper list under hairlines, the board a bordered list. All six quick messages fit;
 * a longer list scrolls. Items in sections have each section's title above its first row, a quiet heading.
 */
@Composable
fun ComposerSuggestions(items: List<ComposerSuggestion>, onPick: (ComposerSuggestion) -> Unit, modifier: Modifier = Modifier) {
    if (items.isEmpty()) return
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(when (style) { AppStyle.Glass -> 20.dp.corner; AppStyle.Brief -> 16.dp.corner; AppStyle.Board -> 8.dp.corner })
    val frame = when (style) {
        AppStyle.Glass -> Modifier.clip(shape).background(palette.surface)
            .border(1.dp, Brush.verticalGradient(listOf(palette.strokeTop, palette.strokeBottom)), shape)
        AppStyle.Brief -> Modifier.clip(shape).background(palette.surface)
        AppStyle.Board -> Modifier.clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
    }
    Column(modifier.fillMaxWidth().then(frame).heightIn(max = 272.dp).verticalScroll(rememberScrollState()).padding(vertical = 4.dp.dense)) {
        items.forEachIndexed { index, item ->
            val spoken = stringResource(R.string.designsystem_with_state, item.word, item.detail)
            if (index > 0) JDivider(Modifier.padding(horizontal = RowInset))
            val section = item.section?.takeIf { it != items.getOrNull(index - 1)?.section }
            if (section != null) Text(section, Modifier.fillMaxWidth().semantics { heading() }
                .padding(start = RowInset, end = RowInset, top = Tokens.Space.s, bottom = Tokens.Space.xxs),
                style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Row(
                Modifier.fillMaxWidth().heightIn(min = 40.dp.row).clickable(role = Role.Button) { onPick(item) }
                    .semantics(mergeDescendants = true) { contentDescription = spoken }
                    .padding(horizontal = RowInset, vertical = 6.dp.dense),
                horizontalArrangement = Arrangement.spacedBy(12.dp.dense), verticalAlignment = Alignment.CenterVertically,
            ) {
                item.status?.let { JStatusDot(it) }
                Text(item.word, style = PersonaTheme.type.bodyStrong, color = if (item.destructive) palette.danger.text else palette.ink,
                    maxLines = 1, overflow = if (item.flexible) TextOverflow.Ellipsis else TextOverflow.Clip,
                    modifier = if (item.flexible) Modifier.weight(1f) else Modifier.widthIn(min = 84.dp))
                Text(item.detail, style = PersonaTheme.type.callout, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f))
            }
        }
    }
}

/** The rows' and section titles' side inset inside the frame. */
private val RowInset = 14.dp
