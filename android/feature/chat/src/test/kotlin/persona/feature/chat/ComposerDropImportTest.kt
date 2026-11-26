package persona.feature.chat

import android.content.Context
import android.graphics.Bitmap
import android.net.Uri
import androidx.core.content.FileProvider
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Before
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.DraftUploadState
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ComposerDropImportTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val created = mutableListOf<File>()

    @Before fun setUpProvider() {
        useChatFileProvider(context)
    }

    @After fun cleanCopies() {
        created.forEach(File::delete)
        File(context.cacheDir, "chat-drops").deleteRecursively()
    }

    private fun source(name: String, bytes: ByteArray): Pair<File, Uri> {
        val file = File(File(context.cacheDir, "chat-captures").apply { mkdirs() }, name)
        file.writeBytes(bytes)
        created += file
        return file to FileProvider.getUriForFile(context, "${context.packageName}.chat.files", file)
    }

    @Test fun readableFileBecomesAnOwnedAttachmentAfterTheSourceDisappears() {
        val (file, uri) = source("notes.txt", "dropped words".toByteArray())
        val result = importDroppedAttachment(context, uri)
        val attachment = assertNotNullAttachment(result)
        assertEquals("notes.txt", attachment.name)
        assertFalse(attachment.image)
        assertFalse(attachment.video)
        assertEquals(13L, attachment.size)
        assertTrue(isDropUri(attachment.uri, context.packageName))
        assertTrue(file.delete())
        context.contentResolver.openInputStream(Uri.parse(attachment.uri)).use {
            assertEquals("dropped words", it!!.readBytes().decodeToString())
        }
    }

    @Test fun imagesUseThePhotoPipelineAndVideosKeepTheirBytes() {
        val imageFile = File(File(context.cacheDir, "chat-captures").apply { mkdirs() }, "picture.png")
        val bitmap = Bitmap.createBitmap(2, 2, Bitmap.Config.ARGB_8888)
        imageFile.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
        created += imageFile
        val imageUri = FileProvider.getUriForFile(context, "${context.packageName}.chat.files", imageFile)
        assertTrue(assertNotNullAttachment(importDroppedAttachment(context, imageUri)).image)
        val existing = List(20) { ComposerAttachment("p$it", "p$it.jpg", "", image = true) }
        val limited = importDroppedAttachment(context, imageUri, existing = existing, canSpill = false)
        assertNull(limited.attachment)
        assertEquals(context.getString(R.string.chat_composer_photo_limit, 20), limited.notice)

        val (_, videoUri) = source("movie.mp4", byteArrayOf(1, 2, 3, 4))
        val video = assertNotNullAttachment(importDroppedAttachment(context, videoUri))
        assertTrue(video.video)
        assertFalse(video.image)
        context.contentResolver.openInputStream(Uri.parse(video.uri)).use {
            assertArrayEquals(byteArrayOf(1, 2, 3, 4), it!!.readBytes())
        }
        val fourVideos = List(MAX_COMPOSER_VIDEOS) { ComposerAttachment("v$it", "v$it.mp4", "", image = false, video = true) }
        val fifthVideo = importDroppedAttachment(context, videoUri, existing = fourVideos)
        assertNull(fifthVideo.attachment)
        assertEquals(context.getString(R.string.chat_composer_video_limit, MAX_COMPOSER_VIDEOS), fifthVideo.notice)
    }

    @Test fun directoriesAndNonFilePayloadsAreRejected() {
        val directory = File(context.cacheDir, "chat-captures").apply { mkdirs() }
        assertNull(importDroppedAttachment(context, Uri.fromFile(directory)).attachment)
        assertNotNull(importDroppedAttachment(context, Uri.fromFile(directory)).notice)
        assertNull(importDroppedAttachment(context, Uri.parse("https://example.com/file")).attachment)
        assertNull(importDroppedAttachment(context, Uri.parse("content://missing.authority/document")).attachment)
        assertNotNull(importDroppedAttachment(context, Uri.parse("content://missing.authority/document")).notice)
    }

    @Test fun copyLimitAdmitsTheBoundaryAndDeletesAnOverflowPartial() {
        val (_, fourUri) = source("four.bin", byteArrayOf(1, 2, 3, 4))
        val destination = File(context.cacheDir, "chat-drops/boundary.bin").apply { parentFile!!.mkdirs() }
        assertEquals(4L, copyDroppedFile(context, fourUri, destination, limit = 4))
        assertArrayEquals(byteArrayOf(1, 2, 3, 4), destination.readBytes())

        val (_, fiveUri) = source("five.bin", byteArrayOf(1, 2, 3, 4, 5))
        try {
            copyDroppedFile(context, fiveUri, destination, limit = 4)
            fail("overflow must fail")
        } catch (_: IOException) {
            assertFalse(destination.exists())
        }
    }

    @Test fun declaredOversizeBecomesAnInvalidAttachmentWithoutACopy() {
        val (file, uri) = source("huge.bin", byteArrayOf(1))
        RandomAccessFile(file, "rw").use { it.setLength(MAX_COMPOSER_FILE_BYTES + 1) }
        val result = importDroppedAttachment(context, uri)
        val attachment = assertNotNullAttachment(result)
        assertEquals(DraftUploadState.Invalid, attachment.uploadState)
        assertEquals(MAX_COMPOSER_FILE_BYTES + 1, attachment.size)
        assertTrue(attachment.error!!.contains("200"))
        assertFalse(File(context.cacheDir, "chat-drops").exists())
    }

    @Test fun cleanupOnlyRemovesUnusedDropCopies() {
        val drop = ComposerAttachment("drop", "a", "content://app.chat.files/chat-drops/a", false)
        val kept = ComposerAttachment("kept", "b", "content://app.chat.files/chat-drops/b", false)
        val picker = ComposerAttachment("picker", "c", "content://other/document", false)
        assertEquals(listOf(drop.uri), leftDrops(listOf(drop, kept, picker), listOf(kept), "app"))
        assertTrue(leftDrops(listOf(drop), emptyList(), "app", keptNow = listOf(drop.uri)).isEmpty())

        val (_, uri) = source("owned.bin", byteArrayOf(1))
        val imported = assertNotNullAttachment(importDroppedAttachment(context, uri))
        deleteDrop(context, imported.uri)
        assertFalse(attachmentReadable(context, imported))
        val captureUri = "content://${context.packageName}.chat.files/chat-captures/owned.bin"
        deleteDrop(context, captureUri)
        assertTrue(created.any { it.name == "owned.bin" && it.exists() })
    }

    private fun assertNotNullAttachment(result: ComposerDropImportResult): ComposerAttachment {
        assertNotNull(result.attachment)
        return result.attachment!!
    }
}
