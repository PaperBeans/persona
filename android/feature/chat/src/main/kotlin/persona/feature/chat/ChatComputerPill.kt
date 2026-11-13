package persona.feature.chat

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.delay
import persona.core.designsystem.JDockPill
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.text
import persona.core.domain.ComputerActivity
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.WorkItem

/**
 * "Controlling MacBook · Click AC → 6" above the composer while the chat's model operates a Mac, and for a few seconds
 * after (iOS `ComputerActivityPill`, #4339 C9 and C18; docs/extensions/computer-use.md "App"). iOS opens that Mac's live
 * screen from it; Android has none, so [onOpen] opens the work line that holds the call, where its steps come in as
 * words; with no such line ([onOpen] null) it is words only, as iOS's pill is for a Mac whose screen can't be shown.
 */
@Composable
internal fun ChatComputerPill(activity: ComputerActivity?, onOpen: (() -> Unit)?, modifier: Modifier = Modifier) {
    // A finished one stays up for its moment, then fades (iOS ticks each second; one wait does it here).
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(activity) {
        val ended = activity?.endedAtMs?.takeUnless { activity.running } ?: return@LaunchedEffect
        delay((ended + ComputerActivity.LINGERS_MS - System.currentTimeMillis()).coerceAtLeast(0))
        now = System.currentTimeMillis()
    }
    val shown = activity?.shown(maxOf(now, System.currentTimeMillis())) == true
    // What fades out is the last one shown.
    val last = remember { Held() }
    if (shown) last.activity = activity
    val still = PersonaTheme.reduceMotion
    AnimatedVisibility(shown, modifier,
        enter = if (still) EnterTransition.None else fadeIn(PersonaTheme.motion.enter()),
        exit = if (still) ExitTransition.None else fadeOut(PersonaTheme.motion.enter())) {
        last.activity?.let { a ->
            JDockPill(a.title.text(), a.running, PersonaIcons.screen, a.spoken.text(), Modifier.testTag("computer-activity"),
                onClick = onOpen, onClickLabel = stringResource(R.string.chat_computer_pill_opens))
        }
    }
}

private class Held { var activity: ComputerActivity? = null }

/** The work line among [lines] that holds the tool call [toolUseId], where the pill takes the user. */
internal fun computerWorkLine(lines: List<ChatLine>, toolUseId: String): ChatLine.Work? =
    lines.filterIsInstance<ChatLine.Work>().firstOrNull { line ->
        line.steps.any { it is WorkItem.Tool && it.call.use.item.toolUseId == toolUseId }
    }
