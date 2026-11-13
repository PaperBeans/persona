package persona.feature.chat

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Bitmap
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerDelivery
import persona.core.contracts.api.DraftUploadState
import persona.core.designsystem.*
import java.io.File

/**
 * Videos and pasted pictures in the composer, a sent video's bubble, and Share's picture of a message (iOS
 * `VideoAttachmentsBar`, `PasteImageChip`, `AttachedFileCards`, `MessageShareCard`), light and dark. The composer and the
 * bubble in every style (Brief and Board look as on main); the Paste chip, the picture and its page are Glass's. PNGs in
 * `build/chat-media-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatMediaShotsTest {
    @get:Rule val rule = createComposeRule()

    private enum class Scene(val glassOnly: Boolean = false) { Composer, Paste, Bubble, Picture(true), Page(true) }

    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val key = SessionKey("mac", "s1")
    private val service = object : ChatComposerService {
        override val deliveries = MutableStateFlow(emptyMap<String, ComposerDelivery>())
        override suspend fun upload(session: SessionKey, name: String, file: File) = "/Users/me/.persona/uploads/$name"
        override suspend fun interrupt(session: SessionKey) {}
        override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean): ComposerDelivery = error("not here")
    }
    private val videoTag = "<persona-file kind=\"video\" path=\"/Users/me/.persona/uploads/screen-20261112-140501.mov\" " +
        "name=\"screen.mov\" size=\"24100000\" duration=\"72.4\"/>"
    private val reply = """
        Two tests fail, both in the login flow:

        | Test | Result |
        | --- | --- |
        | `LoginTest.retry` | times out after 30 s |
        | `LoginTest.offline` | expects the banner |

        ```kotlin
        assertEquals(LoginState.Offline, model.state.value)
        ```

        The retry waits on a socket that never opens; I'll fix that first.
    """.trimIndent()
    private val picture = MessagePicture(reply, user = false, source = "persona › fix login", // branch-only
        time = java.time.Instant.parse("2026-11-12T06:05:00Z"), agent = "Claude", mark = AgentMarkSpec(AgentMarkKind.Claude, "Claude Code", null)) // branch-only

    @Before fun clipboard() {
        // A screenshot's Copy: what the Paste chip offers over the focused composer.
        context.getSystemService(ClipboardManager::class.java).setPrimaryClip(
            ClipData(ClipDescription("screenshot", arrayOf("image/png")), ClipData.Item(Uri.parse("content://example.com/screenshot.png"))))
    }

    private fun attachments() = listOf(
        // The pasted screenshot, on the computer (Robolectric decodes no thumbnail: the tile shows its mark).
        ComposerAttachment("pasted", "pasted-1.png", "", true, "/Users/me/.persona/uploads/pasted-1.jpg", uploadState = DraftUploadState.Uploaded),
        ComposerAttachment("video", "screen.mov", "", false, "/Users/me/.persona/uploads/screen-20261112-140501.mov", size = 24_100_000,
            uploadState = DraftUploadState.Uploaded, video = true, duration = 72.4), // branch-only-video
    )

    @Composable
    private fun SceneContent(scene: Scene, cardDark: Boolean) {
        when (scene) {
            Scene.Composer, Scene.Paste -> {
                val state = remember(scene) {
                    ChatComposerState().apply {
                        val text = "Where does it stall?"
                        value = TextFieldValue(text, TextRange(text.length))
                        if (scene == Scene.Composer) attachments = attachments() else focusRequest = 1
                    }
                }
                Column(Modifier.fillMaxSize()) {
                    Spacer(Modifier.weight(1f))
                    ChatComposer(key, state, working = false, sending = false, canClear = true, service = service,
                        onSend = {}, onCommand = {}, onNotice = {})
                }
            }
            Scene.Bubble -> Column(Modifier.fillMaxSize().padding(Tokens.Space.l)) {
                MergedUserBubble(listOf(Item("u1", role = "user", kind = "text", text = "Where does it stall?\n\n$videoTag", ts = "2026-11-12T06:04:00Z")))
            }
            Scene.Picture -> Box(Modifier.testTag("picture")) { ShareCardPicture(picture, dark = cardDark, layer = null) } // branch-only
            Scene.Page -> MessageShareContent(picture, onDismiss = {}) // branch-only
        }
    }

    /** The picture Share hands on, read from the card's recording as the page's actions read it. */
    @Test fun thePictureThatGoesIsTheCardAtItsOwnWidth() { // branch-only
        lateinit var layer: androidx.compose.ui.graphics.layer.GraphicsLayer // branch-only
        var dark by mutableStateOf(false) // branch-only
        rule.setContent { // branch-only
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) { // branch-only
                layer = androidx.compose.ui.graphics.rememberGraphicsLayer() // branch-only
                Box(Modifier.width(400.dp)) { ShareCardPicture(picture, dark, layer) } // branch-only
            } // branch-only
        } // branch-only
        val out = File(System.getProperty("persona.shots") ?: "build/chat-media-shots").apply { mkdirs() } // branch-only
        for (d in listOf(false, true)) { // branch-only
            dark = d // branch-only
            rule.waitForIdle() // branch-only
            rule.onNodeWithTag("share-card").captureToImage() // branch-only: Robolectric draws (and so records) on a capture
            val png = kotlinx.coroutines.runBlocking { shareCardPng(layer.toImageBitmap().asAndroidBitmap()) } // branch-only
            val picture = android.graphics.BitmapFactory.decodeByteArray(png, 0, png.size) // branch-only
            // Wider than a phone's 390 dp for the table, within the 520 dp cap, at xhdpi's 2 px a dp. // branch-only
            org.junit.Assert.assertTrue(picture.width > 390 * 2 && picture.width <= 520 * 2) // branch-only
            File(out, "export-${if (d) "dark" else "light"}.png").writeBytes(png) // branch-only
        } // branch-only
    } // branch-only

    @Test fun mediaInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf(Scene.Composer)
        var cardDark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val height = when (scene) { Scene.Page -> Modifier.height(900.dp); Scene.Picture -> Modifier; Scene.Bubble -> Modifier.height(180.dp)
                    else -> Modifier.height(360.dp) }
                Box(Modifier.testTag("shot").width(400.dp).then(height).background(PersonaTheme.palette.background)) { SceneContent(scene, cardDark) }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/chat-media-shots").apply { mkdirs() }
        for (sc in Scene.entries) for (s in AppStyle.entries) for (d in listOf(false, true)) {
            if (sc.glassOnly && s != AppStyle.Glass) continue
            scene = sc; style = s; dark = d; cardDark = d
            rule.waitForIdle()
            Thread.sleep(200)
            rule.waitForIdle()
            when (sc) {
                Scene.Composer -> rule.onNodeWithText("screen.mov").assertExists()
                Scene.Paste -> if (s == AppStyle.Glass) rule.onNodeWithTag("composer-paste-image").assertExists() // branch-only
                    else rule.onNodeWithTag("composer-paste-image").assertDoesNotExist() // branch-only
                Scene.Bubble -> rule.onNodeWithText("24.1 MB · 1:12").assertExists()
                Scene.Picture, Scene.Page -> rule.onNodeWithText("persona › fix login").assertExists() // branch-only
            }
            val node = if (sc == Scene.Picture) "picture" else "shot"
            val bitmap = rule.onNodeWithTag(node).captureToImage().asAndroidBitmap()
            File(out, "${sc.name.lowercase()}-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
        }
    }
}
