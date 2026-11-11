package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.ModelRefusal
import persona.core.contracts.ModelRestore
import persona.core.contracts.Session
import persona.core.contracts.api.HubCodedFailure
import persona.core.designsystem.*
import java.io.File

/**
 * The refusal fallback (#2528): the transcript's refusal line, then the card above the composer for a terminal session
 * (the /model step only), the main Persona conversation (Back to configured model and New main chat) and a pending
 * restore (no way back offered), in every style, light and dark. PNGs in `build/model-fallback-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2000dp-xhdpi")
class ModelFallbackShotsTest {
    @get:Rule val rule = createComposeRule()

    private val refusal = ModelRefusal("model_refusal_fallback", "primary-model", "fallback-model", "synthetic-policy", "session",
        "Upstream refused the request", "I can't help with that request.")
    private val terminal = Session(machineId = "box", sessionId = "t1", agent = "claude", status = "idle", modelFallback = refusal,
        turnError = "model not available")
    private val main = terminal.copy(sessionId = "a1", assistant = true, turnError = null)
    private val pending = main.copy(sessionId = "a2", modelRestore = ModelRestore("next_start", "configured-model", "2026-11-11T08:00:00Z"))

    @Test fun theViewFollowsTheRecord() {
        assertNull(fallbackView(terminal.copy(modelFallback = null)))
        assertNull(fallbackView(terminal.copy(modelFallback = refusal.copy(subtype = "model_refusal_notice"))))
        assertEquals(FallbackView("primary-model", "fallback-model", "model not available", false, ""), fallbackView(terminal))
        assertEquals(FallbackView("primary-model", "fallback-model", "", true, "configured-model"), fallbackView(pending))
    }

    @Test fun refusalsAreSaidInWords() {
        val resources = RuntimeEnvironment.getApplication().resources
        class Refused(override val code: String?) : Exception(), HubCodedFailure { override val status: Int? = 409 }
        assertEquals(resources.getString(R.string.chat_model_fallback_err_busy), restoreErrorText(resources, Refused("restore_busy")))
        assertEquals(resources.getString(R.string.chat_model_fallback_err_outdated), restoreErrorText(resources, Refused("node_outdated")))
    }

    @Test fun everyCaseInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l),
                        verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        RefusalNotice(refusal)
                        ModelFallbackCard(terminal, actions = null, modifier = Modifier.testTag("terminal"))
                        CompositionLocalProvider(LocalNewMainChat provides {}) {
                            ModelFallbackCard(main, actions = object : persona.core.contracts.api.ChatActions {
                                override suspend fun isMuted(session: Session) = false
                                override suspend fun execute(session: Session, action: persona.core.contracts.api.ChatAction) = error("unused")
                                override suspend fun verify(session: Session, action: persona.core.contracts.api.ChatAction) = error("unused")
                            })
                            ModelFallbackCard(pending, actions = null)
                        }
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            // The way back only where nothing is pending; New main chat in the main conversation, pending or not (web's
            // card); the terminal session neither.
            assertEquals(1, rule.onAllNodesWithText("Back to configured model").fetchSemanticsNodes().size)
            assertEquals(2, rule.onAllNodesWithText("New main chat").fetchSemanticsNodes().size)
            assertEquals(1, rule.onAllNodesWithText("Configured model: configured-model").fetchSemanticsNodes().size)
            assertEquals(1, rule.onAllNodesWithText("I can't help with that request.").fetchSemanticsNodes().size)
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val out = File(System.getProperty("persona.shots") ?: "build/model-fallback-shots").apply { mkdirs() }
            File(out, "model-fallback-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
        }
    }
}
