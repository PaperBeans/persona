package persona.feature.settings

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/** The connect step names no command (docs/assistant/onboarding.md 4.5): the hub's pair command waits under "Other ways". */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class PairingOtherWaysTest {
    @get:Rule val rule = createComposeRule()

    private val pairing = object : PairingService {
        override val isPaired = MutableStateFlow(false)
        override suspend fun pair(request: PairingRequest) = Unit
        override suspend fun unpair() = Unit
    }
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() = Unit
    }

    @Test fun theCommandIsFoldedAway() {
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                Box(Modifier.testTag("shot").width(400.dp).height(860.dp).background(PersonaTheme.palette.background)) {
                    PairingScreen(pairing, sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true)
                }
            }
        }
        rule.onNodeWithText("personad hub pair").assertDoesNotExist()
        val context = ApplicationProvider.getApplicationContext<Context>()
        rule.onNodeWithText(context.getString(R.string.settings_password_login_title)).assertExists()
        rule.onNodeWithText(context.getString(R.string.settings_password_login_pairing)).performClick()
        rule.waitForIdle()
        rule.onNodeWithText("personad hub pair").assertDoesNotExist()
        rule.onNodeWithText("其他方式").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("personad hub pair").assertExists()
        val dir = File(System.getProperty("persona.shots") ?: "build/onboarding-shots").also { it.mkdirs() }
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(dir, "pairing-other-ways-glass-light.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}
