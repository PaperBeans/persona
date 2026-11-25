package persona.feature.assistant.settings

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isToggleable
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantPagesFeature
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme

/**
 * Settings › Assistant's Dashboards switch (#15017): shown only while the hub says `assistant_pages`, and only on the
 * default assistant's page; it reads and writes the `dashboards` field of `/api/v1/assistant`, off when absent.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h2600dp")
class DashboardsSwitchTest {
    @get:Rule val rule = createComposeRule()
    @After fun reset() = AssistantManagerChoice.reset()

    private val label = "Let Persona make dashboards"
    private val roster = ManagerRoster(listOf(HubManager("assistant", "Personal", online = true, isDefault = true),
        HubManager("work", "Work", online = true)), "assistant")

    @Test fun theRuleOnlyShowsItForTheDefaultAssistantOfAHubThatHasIt() {
        assertFalse(dashboardsShown(emptySet(), "assistant", "assistant"))
        assertFalse(dashboardsShown(setOf("assistant_compact"), "assistant", "assistant"))
        assertTrue(dashboardsShown(setOf(AssistantPagesFeature), "assistant", "assistant"))
        assertFalse("another assistant's page", dashboardsShown(setOf(AssistantPagesFeature), "work", "assistant"))
        assertTrue("a hub without a roster has only its own assistant", dashboardsShown(setOf(AssistantPagesFeature), "assistant", null))
    }

    @Test fun hiddenWithoutTheFeatureAndSavedWithIt() {
        val values = mutableMapOf("assistant" to AssistantConfig(manager = "assistant", machineId = "server"),
            "work" to AssistantConfig(manager = "work", machineId = "server"))
        var features by mutableStateOf(emptySet<String>())
        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides roster, LocalHubFeatures provides features) {
                PersonaTheme(reduceMotion = true) {
                    AssistantSettingsScreen(AssistantSettingsManagerTest.Hub(values), emptyList(), onBack = {})
                }
            }
        }
        AssistantManagerChoice.choose("hub", null, "assistant")
        rule.waitForIdle()
        rule.onNodeWithText("Assistant profile").assertExists()
        rule.onNodeWithText(label).assertDoesNotExist()
        rule.onNodeWithText("Dashboards").assertDoesNotExist()

        // The hub announces it: the switch shows, off while the hub leaves the field out.
        rule.runOnIdle { features = setOf(AssistantPagesFeature) }
        rule.waitForIdle()
        rule.onNode(hasText(label) and isToggleable()).performScrollTo().assertIsOff()
        rule.onNodeWithText("When on, the assistant can draft pages you come back to; tap Keep in the chat and they appear in the sidebar.")
            .assertExists()
        rule.onNode(hasText(label) and isToggleable()).performClick()
        rule.waitForIdle()
        assertEquals(true, values.getValue("assistant").dashboards)
        rule.onNode(hasText(label) and isToggleable()).assertIsOn()
        assertNull("only the switched assistant's setting is written", values.getValue("work").dashboards)

        // Another assistant's page: the hub's pages are the default manager's alone.
        rule.runOnIdle { AssistantManagerChoice.pick("work") }
        rule.waitForIdle()
        rule.onNodeWithText(label).assertDoesNotExist()
    }
}
