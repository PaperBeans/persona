package persona.feature.chat

import persona.core.designsystem.AppStyle

/**
 * Where an Agent's chat opens its session's terminal (#4339 C7, iOS `ChatView.openTerminal`), which comes up over the
 * chat as a cover: ⋯'s Terminal in every style whenever it can open, and on Glass the mark at the end of the live work
 * line. Glass's header has no terminal button, as iOS's (decision 11 of the Android alignment plan); Brief and Board
 * keep theirs.
 */
internal object ChatTerminalPlaces {
    /** The chat has a terminal: an Agent's, not read-only, whose session runs in one (a Codex thread does not). */
    fun has(readOnly: Boolean, canTerminal: Boolean, style: ChatRenderStyle): Boolean =
        !readOnly && canTerminal && style != ChatRenderStyle.Assistant

    /** The header's button (Open terminal, or About the terminal when it can't open): Brief and Board only. */
    fun inHeader(style: AppStyle): Boolean = style != AppStyle.Glass

    /** The mark at the end of the work line (iOS `WorkLine.openTerminal`): the live line only, on Glass. */
    fun onWorkLine(live: Boolean, style: AppStyle): Boolean = live && style == AppStyle.Glass
}
