package persona.feature.settings

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
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
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.PersonaStore
import persona.core.designsystem.UiText
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.io.File
import java.io.IOException
import kotlin.random.Random

/** New from Photo's crop, the image sent and the sheet's outcomes (#2407), with the bounds iOS and the hub keep. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class AvatarUploadTest {
    @Test fun theCropStaysInsideTheImageAndWithinItsZoom() {
        val start = AvatarCrop.of(3000, 2000)
        assertEquals(Triple(500, 0, 2000), start.square)
        assertEquals(8f, start.maxZoom)
        // Out as far as it goes is the whole short side; in, never past 8×.
        assertEquals(1f, start.zoomed(0.5f).zoom)
        assertEquals(8f, start.zoomed(100f).zoom)
        assertEquals(250, start.zoomed(100f).square.third)
        // A drag to the right shows more of the left; never past the edge.
        assertEquals(0, start.moved(10_000f, 0f, 400f).square.first)
        assertEquals(1000, start.moved(-10_000f, 0f, 400f).square.first)
        assertEquals(Triple(400, 0, 2000), start.moved(20f, 0f, 400f).square)
        // A small image zooms only as far as leaves 64 of its pixels.
        val small = AvatarCrop.of(100, 160)
        assertEquals(100f / 64, small.maxZoom)
        assertEquals(64, small.zoomed(10f).square.third)
        assertEquals(1f, AvatarCrop.of(64, 64).maxZoom)
        // Sent at most 1024 px a side.
        assertEquals(1024, start.sendSide)
        assertEquals(64, small.zoomed(10f).sendSide)
    }

    @Test fun namesAreCutAsTheHubCountsThem() {
        assertEquals("Cat", cutCodePoints("Cat", 40))
        val faces = "😺".repeat(41)
        assertEquals(40, cutCodePoints(faces, 40).codePointCount(0, cutCodePoints(faces, 40).length))
        assertEquals("😺".repeat(40), cutCodePoints(faces, 40))
        val model = AvatarUploadModel(Bitmap.createBitmap(64, 64, Bitmap.Config.ARGB_8888))
        model.rename("x".repeat(50))
        assertEquals(40, model.name.length)
        model.describe("y".repeat(90))
        assertEquals(80, model.subtitle.length)
    }

    @Test fun theCropGoesAsAPngUnlessItIsALargeOpaquePhoto() {
        // A flat picture: a small PNG, the crop scaled to 1024.
        val flat = Bitmap.createBitmap(3000, 2000, Bitmap.Config.ARGB_8888).apply { eraseColor(0xFF3366CC.toInt()); setHasAlpha(false) }
        val png = encodeAvatar(flat, AvatarCrop.of(3000, 2000))
        assertTrue(png.isPng())
        BitmapFactory.decodeByteArray(png, 0, png.size).let { assertEquals(1024, it.width); assertEquals(1024, it.height) }
        // Noise (a photo's worth of detail) with no transparency: over 1.5 MB as PNG, so JPEG.
        val noise = noisy(1024, alpha = false)
        val jpeg = encodeAvatar(noise, AvatarCrop.of(1024, 1024))
        assertTrue(jpeg.size > 2 && jpeg[0] == 0xFF.toByte() && jpeg[1] == 0xD8.toByte())
        // The same with transparency stays PNG.
        assertTrue(encodeAvatar(noisy(1024, alpha = true), AvatarCrop.of(1024, 1024)).isPng())
        // A crop smaller than 1024 goes at its own size.
        val crop = AvatarCrop.of(3000, 2000).zoomed(4f)
        val closer = encodeAvatar(flat, crop)
        assertEquals(500, BitmapFactory.decodeByteArray(closer, 0, closer.size).width)
    }

    @Test fun theSheetSendsOnceAndTryAgainOnlyDownloads() = runBlocking {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        context.getSharedPreferences("persona-appearance", 0).edit().clear().commit()
        File(context.filesDir, "personas").deleteRecursively()
        val hub = SheetHub()
        val store = PersonaStore(context, AppStyleStore(context)) { hub }
        store.refresh()
        val model = AvatarUploadModel(Bitmap.createBitmap(200, 300, Bitmap.Config.ARGB_8888).apply { eraseColor(0xFF808080.toInt()) })
        // A name is needed.
        assertFalse(model.canSend)
        assertFalse(model.send(store))
        model.rename("  ")
        assertFalse(model.canSend)

        model.rename("Cat")
        hub.refuse = HubFailure(HubError.Server, "这张图片太小了", status = 400, code = "too_small")
        assertFalse(model.send(store))
        assertEquals(UiText.Plain("这张图片太小了"), model.error)
        hub.refuse = IOException("reset")
        assertFalse(model.send(store))
        assertEquals(UiText.Res(R.string.settings_avatar_upload_unreachable), model.error)

        hub.refuse = null
        hub.files = false
        assertFalse(model.send(store))
        assertEquals("u-00000000", model.made)
        assertEquals(UiText.Res(R.string.settings_avatar_upload_not_downloaded), model.error)
        assertEquals(1, hub.sent.size)
        // Try Again: no second upload, the package comes and the sheet can close.
        hub.files = true
        assertTrue(model.send(store))
        assertEquals(1, hub.sent.size)
        assertNull(model.error)
        assertFalse(model.busy)
        // What was sent: the crop, square, as PNG.
        val image = hub.sent.single()
        assertTrue(image.isPng())
        BitmapFactory.decodeByteArray(image, 0, image.size).let { assertEquals(200, it.width); assertEquals(200, it.height) }
    }

    private class SheetHub : PersonaPacks {
        val looks = mutableListOf<LookView>()
        var current = LookCurrent("persona", "persona")
        var files = true
        var refuse: Exception? = null
        val sent = mutableListOf<ByteArray>()
        override val uploads = true
        override suspend fun list(): List<PersonaInfo> = looks.mapNotNull { it.pack }
        override suspend fun file(id: String, file: String): ByteArray = if (files) byteArrayOf(1) else throw IOException("offline")
        override suspend fun looks() = LookListing(looks.toList(), current)
        override suspend fun choose(id: String, ifUnset: Boolean) = current
        override suspend fun upload(name: String, subtitle: String?, image: ByteArray, choose: Boolean): LookUploaded {
            refuse?.let { throw it }
            sent += image
            val id = "u-0000000${looks.size}"
            val look = LookView(id, "hub", name, rev = "0123456789abcdef", uploaded = true, manifest = PersonaManifest(
                format = 1, id = id, name = name, avatar = PersonaManifest.Avatar(mapOf("128" to "avatar-128.png")),
                dot = PersonaManifest.Dot(144, mapOf("idle" to PersonaManifest.DotState("dot/idle.png"))), source = PersonaManifest.Source("upload"),
            ))
            looks += look
            current = LookCurrent(id, id)
            return LookUploaded(look, current)
        }
    }

    private fun noisy(side: Int, alpha: Boolean): Bitmap {
        val random = Random(7)
        val pixels = IntArray(side * side) { (if (alpha) random.nextInt(0x80, 0x100) shl 24 else 0xFF shl 24) or random.nextInt(0x1000000) }
        return Bitmap.createBitmap(pixels, side, side, Bitmap.Config.ARGB_8888).apply { setHasAlpha(alpha) }
    }

    private fun ByteArray.isPng() = size > 4 && this[0] == 0x89.toByte() && this[1] == 'P'.code.toByte()
}
