package persona.core.domain.sort

import kotlinx.coroutines.flow.Flow
import persona.core.contracts.AgentState
import persona.core.contracts.CoreText
import persona.core.contracts.CoreText.Key

// How the Agents list draws its agents (iOS `AgentRowStyles.swift`, #4338 A9): one of three row styles with the agent's
// latest words, or Compact List, one line each. One choice, kept in the two stored values iOS keeps it in: the row style,
// and whether the list is compact (Android's Compact / Detailed before the styles came).

/** How a row with the agent's latest words is drawn (iOS `AgentRowStyle`). */
enum class AgentRowStyle {
    /** A: the agent's mark in a round well, its state on the corner; the title and the time, its latest words, where it runs. */
    Avatar,
    /** B: the state's dot, the agent's mark and the title with the time; one line of its latest words, the source at its end. */
    TwoLines,
    /** C: a card each, its state's color down the leading edge and its state in words; the one waiting for you tinted. */
    Cards;

    companion object {
        val Default = Avatar
        /** The stored name; anything else (never chosen, or a name this version doesn't know) is the default. */
        fun fromPreference(value: String?): AgentRowStyle = entries.firstOrNull { it.name == value } ?: Default
    }
}

/** What the Agents list shows its agents as (iOS `AgentListDisplay`): one of the row styles, or Compact List. */
enum class AgentListDisplay(
    /** The row style it keeps; Compact List keeps the one chosen before it. */
    val style: AgentRowStyle?,
) {
    Avatar(AgentRowStyle.Avatar),
    TwoLines(AgentRowStyle.TwoLines),
    Cards(AgentRowStyle.Cards),
    /** One line per agent: its mark, its state, the title and the time. */
    CompactList(null);

    companion object {
        val Default = Avatar
        /** The display the two stored values make (iOS `init(style:list:)`): Compact List while [list], else the [style]. */
        fun of(style: AgentRowStyle, list: Boolean): AgentListDisplay = if (list) CompactList else entries.first { it.style == style }
    }
}

/** Where a row starts (iOS's leading column). */
enum class AgentRowLeading {
    /** The agent's mark in a round well with its state on the well's corner (A). */
    Well,
    /** The state's dot alone; the agent's mark goes before the title (B). */
    Dot,
    /** The state's color down the card's leading edge; the agent's mark goes before the title (C). */
    Bar,
    /** The agent's mark, then its state's mark (Compact List). */
    MarkAndState,
}

/** What a row draws in one display (iOS `SlotRow.styled`, `CompactSlotRow`); the rows lay these parts out. */
data class AgentRowParts(
    val leading: AgentRowLeading,
    /** The agent's mark before the title, where the leading column doesn't hold it (B, C). */
    val markBesideTitle: Boolean,
    /**
     * Lines of its latest words under the title: none in Compact List. Avatar's take one line on Android, as its row did
     * before the styles came; iOS gives them two.
     */
    val wordLines: Int,
    /** Where it runs on a line of its own: the source, the place, the model, the to-do, the bell (A, C). */
    val placeLine: Boolean,
    /** The source's tag at the end of the words' line instead (B), which has no place line. */
    val sourceAfterWords: Boolean,
    /** Its state in words at the foot, in its state's color, every state but Seen ([cardStateWord], C); else before the words. */
    val stateAtFoot: Boolean,
    /** A card of its own on the page (C): the list's hairlines and By Repository's cards give way to it. */
    val card: Boolean,
)

/** The parts each display draws, from iOS's rows. */
val AgentListDisplay.rowParts: AgentRowParts get() = when (this) {
    AgentListDisplay.Avatar -> AgentRowParts(AgentRowLeading.Well, markBesideTitle = false, wordLines = 1, placeLine = true,
        sourceAfterWords = false, stateAtFoot = false, card = false)
    AgentListDisplay.TwoLines -> AgentRowParts(AgentRowLeading.Dot, markBesideTitle = true, wordLines = 1, placeLine = false,
        sourceAfterWords = true, stateAtFoot = false, card = false)
    AgentListDisplay.Cards -> AgentRowParts(AgentRowLeading.Bar, markBesideTitle = true, wordLines = 2, placeLine = true,
        sourceAfterWords = false, stateAtFoot = true, card = true)
    AgentListDisplay.CompactList -> AgentRowParts(AgentRowLeading.MarkAndState, markBesideTitle = false, wordLines = 0, placeLine = false,
        sourceAfterWords = false, stateAtFoot = false, card = false)
}

/**
 * State Cards' word at the card's foot (iOS `cardRow`): its state spelled out, Awaiting reply and Waiting in the background
 * included; none for a Seen one or a muted one. Offline and unknown have no word on Android yet: the bar's color says them.
 */
fun AgentSession.cardStateWord(muted: Boolean): CoreText? = when {
    muted -> null
    asksYou -> CoreText.of(Key.StateAwaitingReply)
    state == AgentState.Idle -> null
    waitsInBackground -> CoreText.of(Key.StateBackgroundWaiting)
    state == AgentState.NeedsYou -> CoreText.of(Key.StateNeedsYou)
    state == AgentState.Working -> CoreText.of(Key.StateWorking)
    state == AgentState.Unread -> CoreText.of(Key.StateUnread)
    else -> null
}

/**
 * Where the list's display is kept (iOS `AgentListDisplayChoice`): the Agents page's ⋯ › Display and Settings › Interface
 * style read and set the same choice.
 */
interface AgentListDisplaySettings {
    val display: Flow<AgentListDisplay>
    suspend fun setDisplay(value: AgentListDisplay)
}
