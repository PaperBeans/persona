package persona.core.designsystem

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.CoreText
import persona.core.contracts.FleetItem
import persona.core.contracts.FleetState
import persona.core.contracts.Interaction
import persona.core.contracts.api.HubNotice
import persona.core.domain.answer.RequestExpiry
import persona.core.domain.fleet.FleetText
import persona.core.domain.lines.Compaction
import persona.core.domain.tasks.AssistantLines
import persona.core.domain.tasks.TaskState
import persona.core.hub.HubError
import persona.core.hub.endpointResultLabel
import java.io.File
import java.time.Instant
import java.time.LocalDate

/**
 * The words of the pure-JVM core modules ([CoreText], strings_core.xml) and the experience mode's data in each of the
 * app's languages, as the agents list, notifications, the widget and the inbox show them (docs/protocol/i18n.md). Written as
 * PNGs to `build/i18n-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CoreTextShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1400dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", headline = "2 个需要你", demo = "Demo.json", demoTitle = "今天要处理的事")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1400dp-xhdpi")
    @Test fun english() = shots("en", headline = "2 need you", demo = "Demo.en.json", demoTitle = null)

    @Config(sdk = [35], qualifiers = "ja-w400dp-h1400dp-xhdpi")
    @Test fun japanese() = shots("ja", headline = "2件が要対応", demo = "Demo.ja.json", demoTitle = null)

    @Config(sdk = [35], qualifiers = "en")
    @Test fun pluralsAndCountsFollowTheLanguage() {
        val context = rule.activity
        assertEquals("1 needs you", FleetText.headline(FleetState("needsYou", needsYou = 1)).resolve(context))
        assertEquals("3 working · 1 done", FleetText.rest(FleetState("needsYou", needsYou = 2, working = 3, unread = 1)).resolve(context))
        assertEquals("Context above compacted · Manual · 180.9K tokens before", Compaction("manual", 180_902).label.resolve(context))
        assertEquals("Context above compacted · Manual · 999.9K tokens before", Compaction("manual", 999_949).label.resolve(context))
        assertEquals("Context above compacted · Manual · 1M tokens before", Compaction("manual", 999_950).label.resolve(context))
        assertEquals("Context above compacted · Manual · 1.2M tokens before", Compaction("manual", 1_200_000).label.resolve(context))
        assertEquals("Expires in 5 min", RequestExpiry.label(Instant.EPOCH.plusSeconds(300).toString(), Instant.EPOCH)!!.resolve(context))
    }

    @Config(sdk = [35], qualifiers = "ja")
    @Test fun japaneseCountsHaveNoSpaces() {
        val context = rule.activity
        assertEquals("承認：Bash: go test", FleetText.line(FleetItem("a/b", kind = "permission", detail = "Bash: go test")).resolve(context))
        assertEquals("ここまでのコンテキストを圧縮しました · 手動 · 圧縮前18.1万トークン", Compaction("manual", 180_902).label.resolve(context))
        assertEquals("ここまでのコンテキストを圧縮しました · 手動 · 圧縮前120万トークン", Compaction("manual", 1_200_000).label.resolve(context))
    }

    private val fleet = FleetState("needsYou", needsYou = 2, working = 3, unread = 1, items = listOf(
        FleetItem("mac/w2", "Migrate the database", "MacBook · weather › api", "permission", "Bash: go test ./...", "ix1", 100.0),
        FleetItem("srv/q1", "Pick a store", "Server", "question", "", "ix2", 130.0),
        FleetItem("mac/w3", "Write the release notes", "MacBook", "working"),
    ))

    @Composable
    private fun Words(demo: JsonObject) {
        val type = PersonaTheme.type
        Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
            // The ongoing notification and the widget: the fleet's headline, the other counts, one line per agent.
            JSectionHeader(FleetText.headline(fleet).text(), trailing = FleetText.rest(fleet).text())
            fleet.items.forEach { JRow(FleetText.row(it).text(), subtitle = FleetText.anonymous(it).text()) }
            JRow(FleetText.more(4).text(), subtitle = FleetText.anonymousCount("plan", 3).text())
            // A request card and the hub's notice over the Agents list.
            val request = Interaction("r", kind = "permission", toolName = "Bash", summary = "go test ./...", agent = "codex")
            JRow(request.headline.text(), subtitle = RequestExpiry.label(Instant.EPOCH.plusSeconds(7200).toString(), Instant.EPOCH)?.text())
            JNotice(HubNotice.text(HubNotice.Available("0.9.3", 93)).text(), title = HubNotice.text(HubNotice.HubTooOld).text())
            // Task states, a day divider, a compaction and a probe result.
            val states = TaskState.entries.map { it.label.text() }.joinToString(" · ")
            JRow(states,
                subtitle = AssistantLines.dayLabel(LocalDate.of(2026, 11, 3), LocalDate.of(2026, 11, 3)).text())
            JRow(Compaction("manual", 180_902).label.text(), subtitle = endpointResultLabel(HubError.Timeout).text())
            // Experience mode's data: the assistant's conversations and the inbox.
            JSectionHeader(CoreText.of(CoreText.Key.InboxAction).text())
            val conversations = demo.getValue("assistant").jsonObject.getValue("conversations").jsonArray.take(3)
            conversations.forEach { c ->
                val o = c.jsonObject
                JRow(o.getValue("title").jsonPrimitive.content, subtitle = o["last_text"]?.jsonPrimitive?.content)
            }
            demo.getValue("inbox").jsonArray.take(2).forEach { i ->
                val o = i.jsonObject
                JRow(o["title"]?.jsonPrimitive?.content.orEmpty(), subtitle = o["summary"]?.jsonPrimitive?.content ?: o["preview"]?.jsonPrimitive?.content)
            }
            androidx.compose.material3.Text(CoreText.of(CoreText.Key.AddressHttpPublic).text(), style = type.caption, color = PersonaTheme.palette.ink2)
        }
    }

    private fun shots(language: String, headline: String, demo: String, demoTitle: String?) {
        val data = Json.parseToJsonElement(File(demoFile(demo)).readText()).jsonObject
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent { PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) { Words(data) } }
        for (s in listOf(AppStyle.Glass, AppStyle.entries.last())) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onNodeWithText(headline).assertExists()
            demoTitle?.let { rule.onNodeWithText(it).assertExists() }
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val name = "core-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png"
            File(out(), name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    private fun demoFile(name: String): String = generateSequence(File("").absoluteFile) { it.parentFile }
        .map { File(it, "core/demo/src/main/assets/demo/$name") }.first { it.exists() }.path
}
