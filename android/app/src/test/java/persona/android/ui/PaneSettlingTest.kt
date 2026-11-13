@file:OptIn(androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi::class)
package persona.android.ui

import android.app.Application
import android.content.ComponentName
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.width
import androidx.compose.material3.adaptive.layout.*
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.ui.unit.dp
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import persona.android.navigation.dragRange
import persona.core.designsystem.*

@RunWith(AndroidJUnit4::class)
@Config(application = Application::class, sdk = [35], qualifiers = "w1200dp-h800dp-land")
class PaneSettlingTest {
    @get:Rule val rule = createEmptyComposeRule()

    @Test fun leftFlingKeepsANoncollapsibleListVisible() = fling(false, -2000f, -20000f, 280f)
    @Test fun rightFlingKeepsRoomForTheDetail() = fling(false, 2000f, 20000f, 880f)
    @Test fun leftFlingOfACollapsibleListNeverWritesANegativeWidth() = fling(true, -190f, -20000f, 280f)

    @Test fun resizeDuringSettlingKeepsBothPanesWithinTheNewWindow() =
        fling(false, 2000f, 20000f, 480f, shrink = true)
    @Test fun aListThatBecomesNoncollapsibleWhileSettlingStaysVisible() =
        fling(true, -190f, -20000f, 280f, keepList = true)
    @Test fun aNewGesturePreventsTheOldSettleFromWritingAgain() =
        fling(false, -2000f, -20000f, 600f, interrupt = true)

    private fun fling(
        collapsible: Boolean, delta: Float, velocity: Float, target: Float,
        shrink: Boolean = false, keepList: Boolean = false, interrupt: Boolean = false,
    ) {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        lateinit var state: PaneExpansionState
        lateinit var scope: CoroutineScope
        var paneWidth by mutableFloatStateOf(1200f)
        var listWidth = -1
        var detailWidth = -1
        val splitter = PaneSplitter(null, null, collapsible).apply { width = 1200f; detailOpen = true }
        val haptics = object : HapticFeedback {
            override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) = Unit
        }
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent {
                state = rememberPaneExpansionState()
                scope = rememberCoroutineScope()
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ListDetailPaneScaffold(
                        directive = PaneScaffoldDirective.Default.copy(maxHorizontalPartitions = 2),
                        value = ThreePaneScaffoldValue(PaneAdaptedValue.Expanded, PaneAdaptedValue.Expanded, PaneAdaptedValue.Hidden),
                        paneExpansionState = state,
                        modifier = Modifier.width(paneWidth.dp).fillMaxHeight(),
                        listPane = {
                            JListPage("Agents", modifier = Modifier.onSizeChanged { listWidth = it.width }) {
                                item { JSearchField("", {}, "Search") }
                                items(4) { JRow("A session with a long title", subtitle = "A message from an agent", onClick = {}) }
                            }
                        },
                        detailPane = { Box(Modifier.fillMaxSize().onSizeChanged { detailWidth = it.width }) },
                    )
                }
            } }
            rule.waitForIdle()
            rule.mainClock.autoAdvance = false
            rule.runOnIdle {
                splitter.start()
                splitter.drag(state, delta, haptics)
                splitter.stop(state, velocity, scope)
            }
            repeat(120) { frame ->
                if (frame == 8) rule.runOnIdle {
                    if (shrink) { paneWidth = 800f; splitter.width = paneWidth }
                    if (keepList) { splitter.detailOpen = false; splitter.collapseEnabled = false }
                    if (interrupt) {
                        splitter.start()
                        splitter.drag(state, 600f - splitter.at, haptics)
                        splitter.stop(state, 0f, scope)
                    }
                }
                rule.mainClock.advanceTimeByFrame()
                rule.waitForIdle()
                rule.runOnIdle {
                    val range = dragRange(splitter.width, splitter.canCollapse)
                    println("PANE_SETTLE collapse=$collapsible velocity=$velocity frame=$frame at=${splitter.at} list=$listWidth detail=$detailWidth")
                    assertTrue("Settling stays inside $range: ${splitter.at}", splitter.at in range)
                    assertTrue("The detail retains its minimum width", detailWidth >= 320)
                    if (!splitter.canCollapse) assertTrue("The list retains its minimum width", listWidth >= 280)
                    if (shrink && frame >= 8) assertEquals(800, listWidth + detailWidth)
                    if (interrupt && frame >= 8) assertEquals(600f, splitter.at, 0.01f)
                }
            }
            rule.runOnIdle {
                assertEquals(target, splitter.at, 0.01f)
                assertFalse(splitter.moving)
                if (splitter.canCollapse) splitter.collapse(state)
            }
            if (splitter.canCollapse) {
                rule.waitForIdle()
                rule.runOnIdle {
                    assertTrue(splitter.collapsed)
                    assertEquals(0, listWidth)
                    assertEquals(1200, detailWidth)
                    splitter.expand(state)
                }
                rule.waitForIdle()
                rule.runOnIdle {
                    assertFalse(splitter.collapsed)
                    assertTrue(listWidth >= 280)
                }
            }
        }
    }
}
