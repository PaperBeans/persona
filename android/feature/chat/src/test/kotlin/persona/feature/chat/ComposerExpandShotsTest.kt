package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import java.io.File

/**
 * The composer's grow-then-expand behaviour (#5688) in its four states, in every style and appearance: a short draft
 * that only grows (no button), a long one past the line limit (the ↗↙ at the text's top right), the full-screen
 * editor, and back collapsed with the same text and caret; deleting under the limit takes the button away.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h640dp-xhdpi")
class ComposerExpandShotsTest {
    @get:Rule val rule = createComposeRule()

    private val short = "先跑一遍测试，然后把失败的用例列出来。"
    private val long = (1..9).joinToString("\n") { "第 $it 步：检查登录流程在弱网下的表现，记下每次重试的耗时" }

    @Test fun expandStartsOnlyAfterSixLines() {
        assertEquals(6, ComposerGrowLines)
        assertFalse(composerOffersExpand(6, ComposerGrowLines))
        assertTrue(composerOffersExpand(7, ComposerGrowLines))
    }

    @Test fun fourStatesInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val state = ChatComposerState()
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.testTag("shot").width(400.dp).height(640.dp).background(PersonaTheme.palette.background)) {
                        Column(Modifier.fillMaxSize()) {
                            Spacer(Modifier.weight(1f))
                            ChatComposer(SessionKey("box", "s1"), state, working = false, sending = false, canClear = true, service = null,
                                onSend = {}, onCommand = {}, onNotice = {})
                        }
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/composer-expand-shots").apply { mkdirs() }
        fun shot(name: String, tag: String = "shot") {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag(tag, useUnmergedTree = true).captureToImage().asAndroidBitmap()
            File(out, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            // 1. Under the limit: the field only grows, no button.
            state.value = TextFieldValue("$short\n$short", TextRange(4))
            rule.waitForIdle()
            rule.onNodeWithContentDescription("展开输入框").assertDoesNotExist()
            shot("1-growing")
            // 2. Past the limit: the field scrolls and offers Expand at its top right.
            state.value = TextFieldValue(long, TextRange(12))
            rule.waitForIdle()
            rule.onNodeWithContentDescription("展开输入框").assertIsDisplayed()
            shot("2-button")
            // 3. The full-screen editor: collapse at the top right, send at the bottom.
            rule.onNodeWithContentDescription("展开输入框").performClick()
            rule.waitForIdle()
            rule.onNodeWithContentDescription("收起输入框").assertIsDisplayed()
            rule.onAllNodesWithContentDescription("发送").assertCountEquals(2) // the field's and the editor's
            shot("3-expanded", "composer-expanded")
            // 4. Collapsed: the same text and caret, the button still there.
            rule.onNodeWithContentDescription("收起输入框").performClick()
            rule.waitForIdle()
            rule.onNodeWithTag("composer-expanded").assertDoesNotExist()
            assertEquals(long, state.value.text)
            assertEquals(TextRange(12), state.value.selection)
            rule.onNodeWithContentDescription("展开输入框").assertIsDisplayed()
            shot("4-collapsed")
            // Deleting back under the limit takes the button away.
            state.value = TextFieldValue(short, TextRange(short.length))
            rule.waitForIdle()
            rule.onNodeWithContentDescription("展开输入框").assertDoesNotExist()
        }
    }
}
