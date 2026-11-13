package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import java.io.File
import java.time.Instant

/**
 * Settings › Hub version drawn on the JVM with Robolectric's native graphics (no emulator), in every style, light and dark: the
 * settings row, a newer release that auto-update holds back, an update waiting for two busy agents, a Docker hub with a
 * pinned image, and a hub that is up to date with its history. Each is checked to draw something and written as a PNG to
 * `build/hub-update-shots/` (or `-Dpersona.shots=<dir>`) to look at (docs/android/android.md "Hub version").
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2100dp-xhdpi")
class HubUpdateShotsTest {
    @get:Rule val rule = createComposeRule()

    private val ago = { m: Long -> Instant.now().minusSeconds(m * 60).toString() }
    private val machines = listOf(
        Machine("hub", "家里的服务器", online = true, nodeVersion = "1.42.0"),
        Machine("m1", "工作站", online = true, nodeVersion = "1.41.0", nodeMismatch = true),
        Machine("m2", "笔记本", online = false),
    )
    private val release = HubUpdateStatus(version = "1.42.0", build = 420, channel = "release", canUpdate = true, auto = true,
        lastCheck = ago(12), state = "available",
        latest = HubRelease("1.43.0", 431, released = ago(180), newer = true, notes = "同步更快；收件箱的分拣规则可以按来源设置。", held = "这一版的代码不含 hub 部署时的提交"))
    private val waiting = release.copy(state = "waiting_idle", latest = release.latest!!.copy(held = null),
        busy = listOf(HubUpdateBusy("m1", "工作站", "s1", "重构登录流程"), HubUpdateBusy("hub", "家里的服务器", null, "Persona 的这一轮")))
    private val docker = HubUpdateStatus(version = "1.42.0", channel = "docker", canUpdate = false, state = "available", lastCheck = ago(3),
        latest = HubRelease("1.43.0", newer = true), image = HubImage("ghcr.io/example/persona", "1.42.0", pinned = true))
    private val history = listOf(
        HubUpdateRecord("1.41.0", "1.42.0", 420, ended = ago(60 * 20), result = "updated", auto = true),
        HubUpdateRecord("1.41.0", "1.41.2", 412, ended = ago(60 * 30), result = "rolled_back", reason = "新版本起来后 60 秒内没连上数据库"),
        HubUpdateRecord("1.40.0", "1.41.0", 410, ended = ago(60 * 50), result = "held", reason = "这一版的代码不含 hub 部署时的提交", auto = true),
    )
    private val upToDate = release.copy(state = "idle", latest = null, auto = false, autoNotice = true)

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/hub-update-shots").also { it.mkdirs() }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val background = bitmap.getPixel(1, 1)
        var other = 0
        for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
        assertTrue("$name drew nothing", other > 100)
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private enum class Shot { Settings, Held, Waiting, Docker, Current }

    @Test fun drawsTheHubPageInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var shot by mutableStateOf(Shot.Settings)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).then(if (shot == Shot.Settings) Modifier else Modifier.height(1500.dp))
                    .background(PersonaTheme.palette.background)) {
                    when (shot) {
                        Shot.Settings -> LazyColumn(Modifier.fillMaxWidth(), contentPadding = PaddingValues(Tokens.Space.l)) {
                            item { JSectionHeader("连接") }
                            item { JGroupItem(GroupPosition.of(0, 3)) { JRow("hub", leadingIcon = PhIcons.HardDrives, subtitle = "添加、改名和断开 hub", onClick = {}) } }
                            item { JGroupItem(GroupPosition.of(1, 3)) { JRow("hub 版本", leadingIcon = PhIcons.ArrowsClockwise, subtitle = "版本、检查和安装更新、自动更新", value = HubUpdateText.summary(release), onClick = {}) } }
                            item { JGroupItem(GroupPosition.of(2, 3)) { JRow("hub 版本", leadingIcon = PhIcons.ArrowsClockwise, subtitle = "版本、检查和安装更新、自动更新", value = HubUpdateText.summary(upToDate), onClick = {}) } }
                        }
                        Shot.Held -> HubUpdateContent(HubUpdateState(release), machines, {}, {}, null)
                        Shot.Waiting -> HubUpdateContent(HubUpdateState(waiting), machines, {}, {}, null)
                        Shot.Docker -> HubUpdateContent(HubUpdateState(docker), machines, {}, {}, null)
                        Shot.Current -> HubUpdateContent(HubUpdateState(upToDate, history), machines, {}, {}, null)
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            val suffix = "${s.name.lowercase()}-${if (d) "dark" else "light"}"
            for (next in Shot.entries) {
                shot = next
                capture("hub-update-${next.name.lowercase()}-$suffix")
            }
        }
    }

    /** The page reads the hub, installs with the button, asks before "Update now", and follows the hub's pushes. */
    @Test fun drivesAnUpdateFromThePage() {
        val pushes = MutableSharedFlow<HubUpdateStatus>(extraBufferCapacity = 4)
        val calls = mutableListOf<String>()
        val api = object : HubUpdates {
            var status = release.copy(auto = false, latest = release.latest!!.copy(held = null))
            override val events = pushes
            override suspend fun status() = status
            override suspend fun check() = status
            override suspend fun start(now: Boolean): HubUpdateStatus { calls += "start now=$now"; status = waiting; return status }
            override suspend fun settings(auto: Boolean?, noticeSeen: Boolean?) = status.also { calls += "settings auto=$auto" }
            override suspend fun history() = emptyList<HubUpdateRecord>()
        }
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.width(400.dp).height(1500.dp)) { HubUpdatePage(api, machines, onBack = {}) }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("新版本 1.43.0").assertIsDisplayed()
        rule.onNode(hasText("更新 Hub") and hasClickAction()).performClick()
        rule.waitForIdle()
        rule.onNodeWithText("等 2 个在忙的结束").assertIsDisplayed()
        rule.onNode(hasText("现在就更新…") and hasClickAction()).performClick()
        rule.waitForIdle()
        rule.onNode(hasText("现在就更新") and hasClickAction()).performClick()
        rule.waitForIdle()
        assertEquals(listOf("start now=false", "start now=true"), calls)
        // A push moves the page on without asking the hub.
        pushes.tryEmit(waiting.copy(state = "restarting", busy = emptyList()))
        rule.waitForIdle()
        rule.onNodeWithText("正在重启，会断开一会儿").assertIsDisplayed()
        // The push said auto-update is on: the switch turns it off.
        rule.onNode(hasText("自动更新 Hub") and hasClickAction()).performClick()
        rule.waitForIdle()
        assertEquals("settings auto=false", calls.last())
    }
}
