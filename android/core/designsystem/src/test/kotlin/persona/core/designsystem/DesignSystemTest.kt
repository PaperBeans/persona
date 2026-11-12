package persona.core.designsystem

import androidx.compose.ui.unit.dp

import org.junit.Assert.*
import org.junit.Test

class DesignSystemTest {
    @Test
    fun statusLineUsesPriorityAndHidesInactiveSessions() {
        var state = StatusLineState(ended = true, machineOffline = true, submitting = true,
            unconfirmed = true, pendingRequests = 3, stopping = true, working = true, waiting = true)
        assertEquals(StatusLineKind.Ended, resolveStatusLine(state)?.kind)
        state = state.copy(ended = false)
        assertEquals(StatusLineKind.MachineOffline, resolveStatusLine(state)?.kind)
        state = state.copy(machineOffline = false)
        assertEquals(StatusLineKind.Submitting, resolveStatusLine(state)?.kind)
        assertFalse(resolveStatusLine(state)!!.actionable)
        state = state.copy(submitting = false)
        assertEquals(StatusLineKind.Unconfirmed, resolveStatusLine(state)?.kind)
        state = state.copy(unconfirmed = false)
        assertEquals(UiText.Plural(R.plurals.designsystem_line_needs_you_count, 3), resolveStatusLine(state)?.message)
        state = state.copy(pendingRequests = 0)
        assertEquals(StatusLineKind.Stopping, resolveStatusLine(state)?.kind)
        assertFalse(resolveStatusLine(state)!!.actionable)
        state = state.copy(stopping = false)
        assertEquals(StatusLineKind.Working, resolveStatusLine(state)?.kind)
        state = state.copy(working = false)
        assertEquals(StatusLineKind.Waiting, resolveStatusLine(state)?.kind)
        assertNull(resolveStatusLine(state.copy(waiting = false)))
        assertEquals(UiText.Res(R.string.designsystem_line_replaced), resolveStatusLine(StatusLineState(replaced = true, working = true))?.message)
        assertEquals(UiText.Res(R.string.designsystem_line_answering), resolveStatusLine(StatusLineState(answering = true, blocked = true))?.message)
    }

    @Test fun messageSendingAndProcessingAreNotRuntimeWorkingOrApprovalAnswers() {
        val sending = resolveStatusLine(StatusLineState(submitting = true))!!
        assertEquals(UiText.Res(R.string.designsystem_message_sending), sending.message)
        assertEquals(Status.Idle, sending.status)
        val processing = resolveStatusLine(StatusLineState(processing = true))!!
        assertEquals(StatusLineKind.Processing, processing.kind)
        assertEquals(UiText.Res(R.string.designsystem_message_processing), processing.message)
        assertEquals(Status.Idle, processing.status)
        assertEquals(UiText.Res(R.string.designsystem_line_answering), resolveStatusLine(StatusLineState(answering = true, processing = true))?.message)
        assertEquals(StatusLineKind.NeedsYou, resolveStatusLine(StatusLineState(blocked = true, processing = true))?.kind)
        assertEquals(StatusLineKind.Unconfirmed, resolveStatusLine(StatusLineState(unconfirmed = true, processing = true))?.kind)
    }

    @Test
    fun everyStyleKeepsTheStatusesApart() {
        for (style in AppStyle.entries) for (dark in listOf(false, true)) {
            val palette = Palettes.of(style, dark)
            val dots = listOf(Status.NeedsYou, Status.Working, Status.Unread, Status.Offline).map { palette.status(it).dot }
            assertEquals("$style dark=$dark", dots.size, dots.toSet().size)
            assertEquals(palette.status(Status.Idle), palette.status(Status.Unknown))
            assertEquals(palette.status(Status.Idle), palette.status(Status.Muted))
            assertEquals(dark, palette.dark)
        }
    }

    @Test
    fun styleKeysRoundTripAndDefaultToGlass() {
        AppStyle.entries.forEach { assertEquals(it, AppStyle.fromKey(it.key)) }
        assertEquals(AppStyle.Glass, AppStyle.fromKey(null))
        assertEquals(AppStyle.Glass, AppStyle.fromKey("material"))
        assertEquals(GlassPreference.Auto, GlassPreference.fromKey(null))
    }

    @Test
    fun legacyStatusToneFollowsTheActiveStyle() {
        ActiveStyle.value = AppStyle.Board
        assertEquals(Palettes.BoardLight.needsYou.dot, statusTone(Status.NeedsYou, false).dot)
        ActiveStyle.value = AppStyle.Brief
        assertEquals(Palettes.BriefDark.needsYou.dot, statusTone(Status.NeedsYou, true).dot)
        ActiveStyle.value = AppStyle.Default
    }

    @Test fun wideTextIsCentredAtTheReadingWidth() {
        assertEquals(0f, readingInset(360.dp).value, 0f)
        assertEquals(0f, readingInset(ReadingWidth).value, 0f)
        assertEquals(120f, readingInset(ReadingWidth + 240.dp).value, 0.01f)
    }
}
