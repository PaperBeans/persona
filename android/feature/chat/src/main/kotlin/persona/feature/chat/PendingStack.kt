package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.max
import androidx.compose.ui.unit.min
import persona.core.contracts.Interaction
import persona.core.contracts.SessionKey
import persona.core.contracts.parseTime
import persona.core.designsystem.Tokens
import java.time.Instant

/**
 * The cards above a conversation's composer (`DESIGN_SYSTEM.md` section 4.8, iOS `PendingCards`): stacked, the composer
 * still usable, each card folding on its own.
 */
internal object PendingStack {
    /** The share of the page the cards may take before they scroll (iOS `PendingCards.share`). */
    const val SHARE = 0.5f

    /** Permissions first (they hold a tool call back), then everything newest first (iOS `urgentFirst`). */
    fun urgentFirst(cards: List<Interaction>): List<Interaction> = cards.withIndex().sortedWith(
        compareBy<IndexedValue<Interaction>> { if (it.value.kind == "permission") 0 else 1 }
            .thenByDescending { parseTime(it.value.createdAt) ?: Instant.MIN }
            .thenByDescending { it.index },
    ).map { it.value }

    /** As they come while they fit; the most urgent first once they scroll. [first] (opened from a notification) leads both. */
    fun order(cards: List<Interaction>, scrolling: Boolean, first: String? = null): List<Interaction> {
        val ordered = if (scrolling) urgentFirst(cards) else cards
        return if (first == null) ordered else ordered.partition { it.id == first }.let { (opened, rest) -> opened + rest }
    }

    /**
     * How tall the cards may be before they scroll: half the [page], and never so tall that the composer leaves it: the
     * [page] less the [header] over the conversation, the [rest] of the footer (an open tray, the entries, the composer)
     * and the conversation's [floor]. With the [keyboard] up (the page is short, and a card is often what is being
     * answered) they take all of that room, so a card is whole or scrolls in all the room there is and the composer stays
     * over the keyboard (iOS `PendingCards.cap`). Never under a finger's height; half the page until the footer is measured.
     */
    fun cap(page: Dp, keyboard: Boolean, rest: Dp, header: Dp, floor: Dp): Dp {
        val half = page * SHARE
        if (rest <= Tokens.Space.flush) return half
        val room = page - header - rest - floor
        return max(Tokens.MinTouch.android, if (keyboard) room else min(half, room))
    }
}

/** The line over a card that is not the conversation's own (iOS `TaskQuestions`): the task it comes from, and how many more it and the others ask. */
internal data class StackLabel(val title: String, val more: Int = 0)

/** What a chat's stack holds, in order, with the lines over the cards that are not the conversation's own (its tasks'). */
internal data class StackCards(val cards: List<Interaction>, val labels: Map<String, StackLabel>) {
    val ids: Set<String> get() = cards.mapTo(mutableSetOf()) { it.id }
}

private val OldestFirst = compareBy<Interaction> { parseTime(it.createdAt) ?: Instant.MAX }

/**
 * A Persona conversation's stack above its composer (#4339 C10, iOS `TaskQuestions` and `PendingCards`, `DESIGN_SYSTEM.md`
 * section 4.8): a card the Needs you tray's Review opened ([reviewed]) first, then the oldest question its tasks' Agents
 * ask ([taskKeys]) under its task's name with how many more they ask (`titleOf`), then its own cards (#4883), as they come.
 * The next task question comes up when that one is answered; the tray keeps a row with Review for the others.
 */
internal fun assistantStack(requests: List<Interaction>, key: SessionKey, taskKeys: Set<SessionKey>, reviewed: Interaction?,
    titleOf: (Interaction) -> String?): StackCards {
    val own = requests.filter { it.sessionKey == key }
    val asks = requests.filter { it.sessionKey in taskKeys }.sortedWith(OldestFirst)
    val oldest = asks.firstOrNull()
    val opened = reviewed?.takeIf { r -> r.id != oldest?.id && own.none { it.id == r.id } }
    val labels = buildMap {
        oldest?.let { ask -> titleOf(ask)?.let { put(ask.id, StackLabel(it, asks.size - 1)) } }
        opened?.takeIf { it.sessionKey != key }?.let { card -> titleOf(card)?.let { put(card.id, StackLabel(it)) } }
    }
    return StackCards(listOfNotNull(opened, oldest) + own, labels)
}

/** Which of a conversation's cards are folded to their one line (iOS: each `InteractionPanel`'s own `collapsed`); all open at first. */
@Stable
internal class RequestFolds(initial: Set<String> = emptySet()) {
    var folded by mutableStateOf(initial)
        private set

    fun isFolded(id: String): Boolean = id in folded
    fun set(id: String, fold: Boolean) { folded = if (fold) folded + id else folded - id }
    fun open(id: String) = set(id, false)

    companion object {
        val Saver: Saver<RequestFolds, ArrayList<String>> = Saver(save = { ArrayList(it.folded) }, restore = { RequestFolds(it.toSet()) })
    }
}

@Composable
internal fun rememberRequestFolds(): RequestFolds = rememberSaveable(saver = RequestFolds.Saver) { RequestFolds() }
