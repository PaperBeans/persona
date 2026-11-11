package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.*
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme

/** Leaving a connector's page while a save or delete is in flight is not a failure: no notice says it failed (#3898). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "w400dp-h1600dp")
class HubConnectorPageCancelTest {
    @get:Rule val rule = createComposeRule()

    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val github = HubConnector("github", "GitHub", status = "ok", enabled = true, capabilities = listOf("archive"))

    /** A hub that never answers a change: the request is still in flight when the page goes. */
    private inner class Hub : ConnectorsApiStub() {
        var started = 0
        override suspend fun connectors(all: Boolean) = listOf(github)
        override suspend fun setConnectorEnabled(id: String, enabled: Boolean): HubConnector { started++; awaitCancellation() }
        override suspend fun deleteConnectorData(id: String): ConnectorDataDeleted { started++; awaitCancellation() }
    }

    private class Notices : AppFeedback {
        val shown = mutableListOf<String>()
        override fun failure(error: Throwable, operation: Boolean) = false
        override fun notice(message: String): Boolean { shown += message; return true }
    }

    private fun leaveMidRequest(act: () -> Unit) {
        val hub = Hub()
        val notices = Notices()
        var open by mutableStateOf(true)
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides notices) {
                PersonaTheme(reduceMotion = true) { if (open) HubConnectorPage(hub, "github", onBack = {}) }
            }
        }
        rule.waitForIdle()
        act()
        rule.waitForIdle()
        assertEquals("the request is in flight", 1, hub.started)
        open = false
        rule.waitForIdle()
        assertTrue("no failure notice after leaving: ${notices.shown}", notices.shown.isEmpty())
    }

    @Test
    fun leavingDuringASwitchSaveShowsNoFailure() = leaveMidRequest {
        rule.onNodeWithText(res.getString(R.string.settings_src_enable)).performClick()
    }

    @Test
    fun leavingDuringADataDeleteShowsNoFailure() = leaveMidRequest {
        rule.onNodeWithText(res.getString(R.string.settings_src_delete_data)).performClick()
        rule.onNodeWithText(res.getString(R.string.settings_src_delete)).performClick()
    }
}
