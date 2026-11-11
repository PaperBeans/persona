package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import persona.core.designsystem.icons.PhIcons

/** Synthetic native-rendered evidence, not emulator or glass-refraction acceptance. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class VisualNoiseShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun movedContentGeometryKeepsTheExistingMeasurements() {
        assertEquals(listOf(1f, 1.5f, 20f, 10f, 18f, 28f, 40f, 44f, 36f, 56f, 280f, 160f, 120f, 72f, 3f, 80f,
            100f, 3f, 3f, 160f, 240f, 160f, 380f), with(ContentTokens) {
            listOf(hairline, optionOutline, legacyXl, rowGap, statusIcon, smallAvatar, mediumAvatar, fieldHeight,
                taskQuoteMarkHeight, mailLabelWidth, mailBodyMinHeight, profileHeaderHeight, profileCoverHeight,
                profileAvatar, profileAvatarInset, profileLabelWidth, mediaWaveWidth, mediaWaveGap, mediaWaveBarWidth,
                mediaPreviewMinHeight, mediaPreviewMaxHeight, stickerMaxSize, reactionPickerMaxHeight).map { it.value }
        })
        assertEquals(listOf(14f, 18f, 22f), with(ContentTokens) { listOf(reactionEmoji, quickReactionEmoji, pickerReactionEmoji).map { it.value } })
    }

    @Test fun stateAdviceErrorsOptionsAndDestructiveButtonsHaveNeutralBackings() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val expected = mutableMapOf<String, Color>()
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                val p = PersonaTheme.palette
                Column(Modifier.width(400.dp).background(p.background)) {
                    for (tone in InlineTone.entries) {
                        val tag = "inline-$tone"
                        expected[tag] = p.statusSurface.compositeOn(p.background)
                        JInlineStatus("State", Modifier.testTag(tag), tone = tone)
                    }
                    expected["activity"] = (if (style == AppStyle.Board) p.surface else p.statusSurface).compositeOn(p.background)
                    JActivityBar("Activity", PhIcons.Target, p.working, {}, Modifier.testTag("activity"))
                    expected["error"] = (if (style == AppStyle.Board) p.surface else p.statusSurface).compositeOn(p.background)
                    ErrorCard("Failure", emptyList(), false, {}, Modifier.testTag("error")) {}
                    expected["option"] = (when (style) {
                        AppStyle.Glass -> p.statusSurface
                        AppStyle.Brief -> p.background
                        AppStyle.Board -> p.surface
                    }).compositeOn(p.background)
                    DecisionOption("Option", {}, Modifier.testTag("option"), recommended = true, selected = true)
                    expected["destructive"] = p.statusSurface.compositeOn(p.background)
                    JButton("Remove", {}, Modifier.testTag("destructive").width(160.dp), kind = JButtonKind.Destructive)
                    expected["voice-danger"] = p.statusSurface.compositeOn(p.background)
                    VoiceControl(PhIcons.PhoneDisconnect, "End call", {}, Modifier.testTag("voice-danger"), kind = VoiceControlKind.Danger)
                    if (style != AppStyle.Brief) {
                        expected["advice"] = p.statusSurface.compositeOn(p.background)
                        DecisionAdvice("Advice", Modifier.testTag("advice"))
                    } else expected.remove("advice")
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            expected.forEach { (tag, fill) ->
                val bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
                // Right-hand padding, clear of the text and icon. Density is 2px/dp.
                val actual = if (tag == "voice-danger") Color(bitmap.getPixel(bitmap.width / 2, 24))
                    else Color(bitmap.getPixel(bitmap.width - 12, bitmap.height / 2))
                for ((a, b) in listOf(actual.red to fill.red, actual.green to fill.green, actual.blue to fill.blue)) {
                    assertEquals("$tag ${s.key} dark=$d neutral backing", b, a, 1.1f / 255f)
                }
            }
        }
    }

    @Test fun approvalsAndFeedbackInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var feedback by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)
                    .padding(Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    Text(if (feedback) "Activity and outcomes" else "Routine decisions", style = PersonaTheme.type.title, color = PersonaTheme.palette.ink)
                    if (feedback) {
                        JActivityBar("Preparing a draft", PhIcons.Target, PersonaTheme.palette.working, {}, trailing = "Working")
                        JInlineStatus("Waiting for your answer", tone = InlineTone.Caution)
                        JInlineStatus("Draft saved", tone = InlineTone.Done)
                        JInlineStatus("Upload failed. Retry when connected.", tone = InlineTone.Failed)
                        ErrorCard("Model request failed", listOf(ErrorTag("503", ErrorTagTone.Danger)), false, {},
                            action = { JButton("Retry", {}, kind = JButtonKind.Secondary) }) {}
                        UserBubble("Please review the synthetic draft.")
                        DraftPreview("A short demo reply that has not been sent.")
                        Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                            JButton("Continue", {}, kind = JButtonKind.Primary)
                            JButton("Delete draft", {}, kind = JButtonKind.Destructive)
                        }
                    } else {
                        AgentRow("Review demo change", Status.NeedsYou, {}, statusText = "Needs you", preview = "Choose whether to continue", time = "Now")
                        AgentRow("Write demo tests", Status.Working, {}, statusText = "Working", preview = "Running checks", time = "Now")
                        StatusLine(StatusLineState(pendingRequests = 2))
                        DecisionCard(DecisionKind.Tool, "Read the demo project", advice = "Read-only operation; review the scope first.",
                            body = { Text("Access: demo files only", style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2) },
                            actions = { DecisionButtons(JAction("Allow", onClick = {}), secondary = JAction("Deny", onClick = {})) })
                        DecisionDock(DecisionKind.Question, "Which draft should I use?") {
                            DecisionOption("Short draft", {}, recommended = true, selected = true, detail = "Keep the reply concise")
                            Spacer(Modifier.height(Tokens.Space.s))
                            DecisionOption("Detailed draft", {}, detail = "Include supporting context")
                            Spacer(Modifier.height(Tokens.Space.m))
                            DecisionButtons(JAction("Confirm", onClick = {}), secondary = JAction("Not now", onClick = {}))
                        }
                    }
                }
            }
        }
        val out = File("build/visual-noise-shots").also { it.mkdirs() }
        for (f in listOf(false, true)) for (s in AppStyle.entries) {
            var light: ByteArray? = null
            for (d in listOf(false, true)) {
                feedback = f; style = s; dark = d
                rule.waitForIdle()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                val bytes = java.io.ByteArrayOutputStream().also { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
                if (!d) light = bytes else assertFalse("Dark must differ from light", bytes.contentEquals(light))
                File(out, "${if (f) "feedback" else "approvals"}-${s.key}-${if (d) "dark" else "light"}.png").writeBytes(bytes)
            }
        }
    }
}
