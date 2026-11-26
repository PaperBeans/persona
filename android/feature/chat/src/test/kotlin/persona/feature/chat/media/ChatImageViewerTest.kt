package persona.feature.chat.media

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatImageViewerTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private class FakeMedia(var works: Boolean = true) : ChatMedia {
        val calls = mutableListOf<String>()
        override suspend fun save(bytes: ByteArray) = works.also { calls += "save ${bytes.size}" }
        override suspend fun share(bytes: ByteArray) = works.also { calls += "share ${bytes.size}" }
        override suspend fun copy(bytes: ByteArray) = works.also { calls += "copy ${bytes.size}" }
    }

    private val picture = byteArrayOf(1, 2, 3)

    @Test fun actionsInOrderWithEditOnlyWhenTheChatTakesThePhoto() {
        assertEquals(listOf("保存", "分享", "拷贝"), chatViewerActions(res, FakeMedia(), { false }, null).map { it.label })
        assertEquals(listOf("保存", "编辑", "分享", "拷贝"), chatViewerActions(res, FakeMedia(), { false }, { null }).map { it.label })
    }

    @Test fun noticesSayWhatHappened() = runTest {
        val media = FakeMedia()
        val actions = chatViewerActions(res, media, { false }, null).associateBy { it.label }
        assertEquals("已保存到相册", actions.getValue("保存").onClick(0, picture))
        assertNull(actions.getValue("分享").onClick(1, picture)) // the share sheet is the answer
        assertEquals("已拷贝图片", actions.getValue("拷贝").onClick(2, picture))
        assertEquals(listOf("save 3", "share 3", "copy 3"), media.calls)
        media.works = false
        assertEquals("保存失败", actions.getValue("保存").onClick(0, picture))
        assertEquals("分享失败", actions.getValue("分享").onClick(0, picture))
        assertEquals("拷贝失败", actions.getValue("拷贝").onClick(0, picture))
    }

    @Test fun editStopsAtHubPhotoLimitAndOtherwiseSaysWhatTheEditorSays() = runTest {
        var limit = true
        val edited = mutableListOf<Int>()
        val edit = chatViewerActions(res, FakeMedia(), { limit }, { bytes -> edited += bytes.size; "这张图片没法编辑" }).first { it.label == "编辑" }
        assertEquals("一次最多 20 张", edit.onClick(0, picture))
        assertEquals(emptyList<Int>(), edited)
        limit = false
        assertEquals("这张图片没法编辑", edit.onClick(0, picture))
        assertEquals(listOf(3), edited)
        val customEdit = chatViewerActions(res, FakeMedia(), { true }, { null }, maxPhotos = 7).first { it.label == "编辑" }
        assertEquals("一次最多 7 张", customEdit.onClick(0, picture))
    }
}
