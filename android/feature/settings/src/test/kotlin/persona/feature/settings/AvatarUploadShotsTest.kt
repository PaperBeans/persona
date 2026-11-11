package persona.feature.settings

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Shader
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onLast
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.LookCurrent
import persona.core.contracts.api.LookListing
import persona.core.contracts.api.LookUploaded
import persona.core.contracts.api.LookView
import persona.core.contracts.api.PersonaInfo
import persona.core.contracts.api.PersonaManifest
import persona.core.contracts.api.PersonaPacks
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.LocalAppStyleStore
import persona.core.designsystem.LocalPersonaDesign
import persona.core.designsystem.LocalPersonaPack
import persona.core.designsystem.LocalPersonaStore
import persona.core.designsystem.PersonaStore
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import java.io.File
import java.io.IOException

/**
 * New from Photo and deleting an avatar made from an image (#2407), in each style light and dark: Settings › Appearance ›
 * Avatar with one made from an image (its delete button, New from Photo, the footer), the delete confirmation, the crop
 * sheet, and the sheet after the avatar was made but not downloaded. Written as PNGs to `build/looks-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1500dp-xhdpi")
class AvatarUploadShotsTest {
    @get:Rule val rule = createComposeRule()

    private val example = generateSequence(File("").absoluteFile) { it.parentFile }
        .map { File(it, "schema/extension/examples/example-looks") }.first { it.isDirectory }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/looks-shots").also { it.mkdirs() }

    /** A hub that makes avatars from images, with one made already ("Pebble at home"), its files from the example looks. */
    private inner class UploadsHub : PersonaPacks {
        var files = true
        val looks = mutableListOf(uploadedLook("u-0a1b2c3d", "Pebble at home", "The one on the sofa"))
        var current = LookCurrent("persona", "persona")
        override val uploads = true
        override suspend fun list(): List<PersonaInfo> = looks.mapNotNull { it.pack }
        override suspend fun file(id: String, file: String): ByteArray = if (files) File(example, "looks/pebble/avatar-256.png").readBytes() else throw IOException("offline")
        override suspend fun looks() = LookListing(looks.toList(), current)
        override suspend fun choose(id: String, ifUnset: Boolean) = current
        override suspend fun upload(name: String, subtitle: String?, image: ByteArray, choose: Boolean): LookUploaded {
            val look = uploadedLook("u-11223344", name, subtitle)
            looks += look
            current = LookCurrent(look.id, look.id)
            return LookUploaded(look, current)
        }
    }

    private fun uploadedLook(id: String, name: String, subtitle: String?) = LookView(id, "hub", name, rev = "0123456789abcdef", uploaded = true,
        manifest = PersonaManifest(format = 1, id = id, name = name, subtitle = subtitle,
            avatar = PersonaManifest.Avatar(mapOf("256" to "avatar-256.png")),
            dot = PersonaManifest.Dot(144, mapOf("idle" to PersonaManifest.DotState("dot/idle.png"))), source = PersonaManifest.Source("upload")))

    /** A made-up photo: sky, a sun and hills, wider than tall, so the crop shows what it leaves out. */
    private fun photo(): Bitmap = Bitmap.createBitmap(1200, 800, Bitmap.Config.ARGB_8888).apply {
        val canvas = Canvas(this)
        canvas.drawPaint(Paint().apply { shader = LinearGradient(0f, 0f, 0f, 800f, 0xFF4A90D9.toInt(), 0xFFF6D365.toInt(), Shader.TileMode.CLAMP) })
        canvas.drawCircle(820f, 300f, 110f, Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFFFFF3B0.toInt() })
        canvas.drawOval(-200f, 560f, 700f, 1100f, Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFF3E7D4C.toInt() })
        canvas.drawOval(400f, 600f, 1500f, 1150f, Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFF2F6A3E.toInt() })
    }

    @Test fun drawsNewFromPhotoTheCropAndDelete() {
        android.graphics.BitmapFactory.decodeFile(File(example, "looks/pebble/avatar-256.png").path)
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        context.getSharedPreferences("persona-appearance", 0).edit().clear().commit()
        File(context.filesDir, "personas").deleteRecursively()
        val style = AppStyleStore(context)
        val hub = UploadsHub()
        val store = PersonaStore(context, style) { hub }
        runBlocking { store.refresh() }
        assertTrue(store.canUpload.value)
        assertEquals(setOf("u-0a1b2c3d"), store.uploaded.value)

        var look by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf("list")
        var run by mutableIntStateOf(0)
        val crop = AvatarUploadModel(photo()).apply { rename("Sunny hills"); describe("From the trip") }
        val failed = AvatarUploadModel(photo()).apply { rename("Sunny hills") }
        hub.files = false
        runBlocking { failed.send(store) }
        hub.files = true
        assertEquals("u-11223344", failed.made)
        hub.looks.removeAll { it.id == "u-11223344" }
        hub.current = LookCurrent("persona", "persona")
        runBlocking { store.refresh() }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = look) {
                key(run) {
                    val pack by store.current.collectAsState()
                    val face by style.face.collectAsState()
                    CompositionLocalProvider(LocalAppStyleStore provides style, LocalPersonaStore provides store,
                        LocalPersonaPack provides pack, LocalPersonaDesign provides face) {
                        Box(Modifier.testTag("shot").width(400.dp).height(1500.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                            PersonaSettings(demo = false)
                            when (scene) {
                                "crop" -> AvatarUploadSheet(crop, onSend = {}, onDismiss = {})
                                "not-downloaded" -> AvatarUploadSheet(failed, onSend = {}, onDismiss = {})
                            }
                        }
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            val window = rule.onAllNodes(isDialog()).fetchSemanticsNodes().isNotEmpty()
            val bitmap = (if (window) rule.onAllNodes(isDialog()).onLast() else rule.onNodeWithTag("shot")).captureToImage().asAndroidBitmap()
            File(out(), "avatar-upload-$name-${look.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            look = s; dark = d; run++
            scene = "list"
            rule.waitForIdle()
            assertEquals(1, rule.onAllNodesWithTag("avatar-new-from-photo").fetchSemanticsNodes().size)
            assertEquals(1, rule.onAllNodesWithText("The one on the sofa").fetchSemanticsNodes().size)
            capture("list")
            rule.onNodeWithTag("avatar-delete-u-0a1b2c3d").performClick()
            rule.waitForIdle()
            assertEquals(1, rule.onAllNodesWithText("Delete “Pebble at home”?").fetchSemanticsNodes().size)
            capture("delete")
            run++
            scene = "crop"
            capture("crop")
            scene = "not-downloaded"
            rule.waitForIdle()
            assertEquals(1, rule.onAllNodesWithText("Try again").fetchSemanticsNodes().size)
            capture("not-downloaded")
        }
    }
}
