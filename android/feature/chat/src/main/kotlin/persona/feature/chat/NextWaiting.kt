package persona.feature.chat

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.contracts.SessionKey
import persona.core.designsystem.ChatHint
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PhIcons

/**
 * One card an agent waits on you with, as the Persona home's "Needs you now" lists it (the app maps its `needsYou`
 * lines): the conversation, the card, the conversation's name and what the card asks (`Interaction.headline`).
 */
data class WaitingRequest(val session: SessionKey, val requestId: String, val who: String, val what: String)

/**
 * The cards other chats wait on you with and how to open one, for a chat whose page does not pass them itself: Persona's
 * conversation inside the Assistant tab (#4339 C18). The shell provides them.
 */
class WaitingElsewhere(val waiting: List<WaitingRequest>, val open: (SessionKey, requestId: String) -> Unit)

val LocalWaitingElsewhere = androidx.compose.runtime.compositionLocalOf<WaitingElsewhere?> { null }

/** What other chats still wait on you with (#4325, web #4091): the first in the list's order, and how many there are. */
internal data class NextWaiting(val request: WaitingRequest, val count: Int)

/** How long the line stays after an answer here: as long as the web shows its "Answered" receipts. */
internal const val NEXT_WAITING_MILLIS = 20_000L

/**
 * Whether this reply puts the next card on offer: only an answer that went through (not one handing the card back to
 * the computer) or one the hub had already (as the web's "Answered" receipt). A changed screen, an expired, withdrawn or
 * handed-back card is not: a changed screen brings a new card here at once.
 */
internal fun InteractionReply.offersNext(resolution: persona.core.contracts.Resolution): Boolean =
    (submission == InteractionSubmission.AlreadyAnswered && effective?.cancel != true) ||
        (submission == InteractionSubmission.Answered && resolution.dismiss != true && resolution.cancel != true)

/**
 * Whether the line shows now: offered after an answer here, this chat has no card left (Persona's conversation counts its
 * tasks' too), the page can answer and open another chat (not a read-only view), and the keyboard is down (as on the web).
 * Persona's conversations offer it too, as iOS's `NextNeedsYouStrip` does in every chat (#4339 C18).
 */
internal fun showsNextWaiting(offered: Boolean, cardsHere: Int, readOnly: Boolean, canOpen: Boolean, keyboard: Boolean): Boolean =
    offered && cardsHere == 0 && !readOnly && canOpen && !keyboard

/** The cards of [waiting] outside [here] (the same session id on another computer is another chat); null when none is. */
internal fun nextWaiting(waiting: List<WaitingRequest>, here: SessionKey): NextWaiting? {
    val others = waiting.filter { it.session != here }
    return others.firstOrNull()?.let { NextWaiting(it, others.size) }
}

/** "N more waiting for you · Next: <agent> · <what it asks> ›", under the answer's notice; a tap opens that chat with that card first. */
@Composable
internal fun NextWaitingRow(next: NextWaiting, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val text = pluralStringResource(R.plurals.chat_next_waiting, next.count, next.count,
        next.request.who.ifBlank { stringResource(R.string.chat_next_waiting_agent) }, next.request.what)
    ChatHint(text, PhIcons.Hand, PersonaTheme.palette.needsYou.dot, onOpen,
        modifier.padding(horizontal = Tokens.Space.m).testTag("chat-next-waiting"),
        onClickLabel = stringResource(R.string.chat_next_waiting_open))
}
