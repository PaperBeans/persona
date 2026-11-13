package persona.core.designsystem

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Waiting in the background's own tone (#4338 A5, A11; iOS `StatusPalette.backgroundWaiting`, `Session.stateTone`): Glass takes
 * the generated `status.<mode>.backgroundWaiting` tokens, Brief and Board keep Working's colors (decision 8).
 */
class BackgroundWaitingToneTest {
    @Test fun glassTakesTheGeneratedTokens() {
        for (dark in listOf(false, true)) {
            val tokens = if (dark) Tokens.Status.Dark.BackgroundWaiting.let { listOf(it.dot, it.text, it.tint) }
                else Tokens.Status.Light.BackgroundWaiting.let { listOf(it.dot, it.text, it.tint) }
            val tone = Palettes.of(AppStyle.Glass, dark).status(Status.Working, waitsInBackground = true)
            assertEquals("dark=$dark", tokens, listOf(tone.dot, tone.text, tone.tint))
        }
    }

    @Test fun glassDrawsItLighterThanWorking() {
        for (dark in listOf(false, true)) {
            val palette = Palettes.of(AppStyle.Glass, dark)
            val working = palette.status(Status.Working)
            val waiting = palette.status(Status.Working, waitsInBackground = true)
            // Working's amber, lighter; its words the secondary gray (a lighter amber is too faint for small text).
            assertEquals(working.dot.copy(alpha = 1f), waiting.dot.copy(alpha = 1f))
            assertTrue("dark=$dark", waiting.dot.alpha < working.dot.alpha)
            assertEquals(palette.idle.text, waiting.text)
            assertEquals(working.dot.copy(alpha = 1f), waiting.tint.copy(alpha = 1f))
        }
    }

    @Test fun onlyAWorkingAgentTakesIt() {
        for (style in AppStyle.entries) for (dark in listOf(false, true)) {
            val palette = Palettes.of(style, dark)
            assertEquals(palette.working, palette.status(Status.Working, waitsInBackground = false))
            for (status in Status.entries.filter { it != Status.Working }) {
                assertEquals("$style dark=$dark $status", palette.status(status), palette.status(status, waitsInBackground = true))
            }
        }
    }

    @Test fun briefAndBoardKeepWorkingsColors() {
        for (style in listOf(AppStyle.Brief, AppStyle.Board)) for (dark in listOf(false, true)) {
            val palette = Palettes.of(style, dark)
            assertEquals("$style dark=$dark", palette.working, palette.backgroundWaiting)
            assertEquals("$style dark=$dark", palette.working, palette.status(Status.Working, waitsInBackground = true))
        }
    }
}
