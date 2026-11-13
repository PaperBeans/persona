package persona.core.domain

import org.junit.Assert.assertEquals
import org.junit.Test

/** New Agent's first message is written as the chat composer writes its messages (#2321); feature:agents compares the two. */
class ComposerMessageTest {
    private val tag = "\n\n<attached_images>\n/tmp/photo.jpg\n</attached_images>"

    @Test fun photosGoInTheirBlockAfterTheWordsAndFiles() {
        assertEquals("看这里$tag", ComposerMessage.text("看这里", listOf("/tmp/photo.jpg"), emptyList(), photosOnly = "请看图片"))
        assertEquals("请看图片$tag", ComposerMessage.text("", listOf("/tmp/photo.jpg"), emptyList(), photosOnly = "请看图片"))
        assertEquals("请看图片$tag", ComposerMessage.text("  \n", listOf("/tmp/photo.jpg"), emptyList(), photosOnly = "请看图片"))
        // A file says something already: no stand-in words.
        assertEquals("/tmp/notes.txt$tag", ComposerMessage.text("", listOf("/tmp/photo.jpg"), listOf("/tmp/notes.txt"), photosOnly = "请看图片"))
        assertEquals("只有字", ComposerMessage.text("只有字", emptyList(), emptyList()))
        assertEquals("", ComposerMessage.text("", emptyList(), emptyList(), photosOnly = "请看图片"))
    }

    @Test fun filesFollowTheWordsQuotedWhenTheShellWouldSplitThem() {
        assertEquals("修一下 /tmp/a.log '/tmp/my notes.txt' '/tmp/it'\\''s.md'",
            ComposerMessage.text("修一下", emptyList(), listOf("/tmp/a.log", "/tmp/my notes.txt", "/tmp/it's.md")))
        assertEquals("'/tmp/a(1).txt'", ComposerMessage.quotePath("/tmp/a(1).txt"))
    }

    @Test fun videosGoAsTagsBetweenTheWordsAndThePhotos() {
        val video = AttachedFile("video", "/tmp/clip.mp4", "clip.mp4", 2048, 3.0)
        assertEquals("Please look at the video\n\n${video.toXml()}",
            ComposerMessage.text("", emptyList(), emptyList(), listOf(video), photosOnly = "Please look at the photos", videosOnly = "Please look at the video"))
        assertEquals("Please look at the photos\n\n${video.toXml()}$tag",
            ComposerMessage.text("", listOf("/tmp/photo.jpg"), emptyList(), listOf(video), "Please look at the photos", "Please look at the video"))
    }
}
