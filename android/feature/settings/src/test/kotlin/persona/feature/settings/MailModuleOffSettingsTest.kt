package persona.feature.settings

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalMailAccess
import persona.core.designsystem.MailAccess
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.lang.reflect.Proxy

/** A hub's connectors that names every call asked of it; mail accounts answer [accounts], the rest is not there. */
private class RecordingConnectors(private val accounts: List<MailAccount> = listOf(MailAccount(id = "a1", email = "me@example.com"))) {
    val asked = mutableListOf<String>()
    val api: ConnectorsManagement = Proxy.newProxyInstance(ConnectorsManagement::class.java.classLoader, arrayOf(ConnectorsManagement::class.java)) { proxy, method, args ->
        when (method.name) {
            "hashCode" -> System.identityHashCode(proxy)
            "equals" -> proxy === args?.get(0)
            "toString" -> "connectors"
            "mailAccounts" -> { asked += method.name; accounts }
            "connectors" -> { asked += method.name; emptyList<HubConnector>() }
            else -> { asked += method.name; throw UnsupportedOperationException(method.name) }
        }
    } as ConnectorsManagement
    /** The calls that reach a route under `/api/v1/mail`. */
    val mail get() = asked.filter { it.contains("mail", ignoreCase = true) || it.contains("google", ignoreCase = true) || it.contains("microsoft", ignoreCase = true) }
}

private fun services(connectors: ConnectorsManagement) = SettingsServices(
    pairing = object : PairingService {
        override val isPaired = MutableStateFlow(true)
        override suspend fun pair(request: PairingRequest) = error("not used")
        override suspend fun unpair() = error("not used")
    },
    connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = error("not used")
        override fun send(message: JsonObject) = false
    },
    sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() {}
    },
    updates = object : AppUpdates { override suspend fun check(): AppUpdate? = null },
    connectors = connectors,
)

/**
 * Settings follows the hub's mail module (#15995): stored-only, Settings › Connectors has no Mail row and asks no mail
 * route, and a link to the mail pages lands on one line; live, the row and the pages are today's.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1500dp-xhdpi")
class MailModuleOffSettingsTest {
    @get:Rule val rule = createComposeRule()

    private fun text(id: Int): String = ApplicationProvider.getApplicationContext<Context>().getString(id)
    private val offLine get() = text(R.string.settings_mail_module_off)
    private fun shown(text: String) = rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty()
    private fun waitFor(text: String) = rule.waitUntil(5_000) { shown(text) }

    private fun show(access: MailAccess, content: @Composable () -> Unit) = rule.setContent {
        PersonaTheme(reduceMotion = true) {
            CompositionLocalProvider(LocalMailAccess provides access) { Box(Modifier.width(400.dp).height(1500.dp)) { content() } }
        }
    }

    @Test fun theMailRowIsLeftOutOnlyWhileMailIsStoredOnly() {
        val hub = listOf(HubConnector(id = "mail", name = "Mail"), HubConnector(id = "github", name = "GitHub"))
        val github = ConnectorRow.Hub(hub[1])
        assertEquals(listOf(ConnectorRow.Phone, ConnectorRow.Mail, ConnectorRow.Lark, github), connectorRows(hub, lark = true))
        assertEquals(listOf(ConnectorRow.Phone, ConnectorRow.Mail, ConnectorRow.Lark, github), connectorRows(hub, lark = true, mail = MailAccess.Live))
        assertEquals(listOf(ConnectorRow.Phone, ConnectorRow.Lark, github), connectorRows(hub, lark = true, mail = MailAccess.StoredOnly))
        assertEquals(listOf(ConnectorRow.Phone, github), connectorRows(hub, lark = false, mail = MailAccess.StoredOnly))
    }

    @Test fun storedOnlyConnectorsHasNoMailRowAndAsksNoMailRoute() {
        val hub = RecordingConnectors()
        show(MailAccess.StoredOnly) { SettingsScreen(services(hub.api), initialPage = "connectors", onBack = {}) }
        waitFor(text(R.string.settings_diag_phone))
        rule.waitUntil(5_000) { "connectors" in hub.asked }
        rule.waitForIdle()
        rule.onAllNodes(hasText(text(R.string.settings_row_mail)) and hasClickAction()).assertCountEquals(0)
        rule.onAllNodesWithText(text(R.string.settings_row_mail_subtitle)).assertCountEquals(0)
        // The other rows are as they were, and the list needs no explanation.
        rule.onAllNodes(hasText(text(R.string.settings_row_lark)) and hasClickAction()).assertCountEquals(1)
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
        assertEquals(emptyList<String>(), hub.mail)
    }

    @Test fun liveConnectorsHasTheMailRowAndReadsItsAccounts() {
        val hub = RecordingConnectors()
        show(MailAccess.Live) { SettingsScreen(services(hub.api), initialPage = "connectors", onBack = {}) }
        rule.waitUntil(5_000) { "mailAccounts" in hub.asked }
        rule.onAllNodes(hasText(text(R.string.settings_row_mail)) and hasClickAction()).assertCountEquals(1)
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
    }

    @Test fun storedOnlyALinkToTheMailPagesSaysMailIsOffAndAsksNothing() {
        val hub = RecordingConnectors()
        var page by mutableStateOf("mail")
        show(MailAccess.StoredOnly) { key(page) { SettingsScreen(services(hub.api), initialPage = page, onBack = {}) } }
        for (next in listOf("mail", "mail-add")) {
            page = next
            waitFor(offLine)
            rule.onAllNodesWithText(text(R.string.settings_src_mail_title)).assertCountEquals(1)
            rule.onAllNodesWithText(text(R.string.settings_src_mail_empty_title)).assertCountEquals(0)
            rule.waitForIdle()
        }
        assertEquals(emptyList<String>(), hub.asked)
    }

    @Test fun liveTheMailPageIsTodays() {
        val hub = RecordingConnectors()
        show(MailAccess.Live) { SettingsScreen(services(hub.api), initialPage = "mail", onBack = {}) }
        waitFor("me@example.com")
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
        assertTrue("mailAccounts" in hub.asked)
    }

    // Captures: Settings › Connectors with mail live and stored-only, and the page a link to Mail lands on, in every style,
    // light and dark, in each language. PNGs go to `build/mail-module-shots/` to look at; none is committed.
    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/mail-module-shots").also { it.mkdirs() }

    private enum class Scene(val access: MailAccess, val page: String) {
        ConnectorsOn(MailAccess.Live, "connectors"), ConnectorsOff(MailAccess.StoredOnly, "connectors"), MailPageOff(MailAccess.StoredOnly, "mail")
    }

    private fun shots(language: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf(Scene.ConnectorsOn)
        val hub = RecordingConnectors()
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalMailAccess provides scene.access) {
                    key(style, dark, scene) {
                        Box(Modifier.testTag("shot").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                            SettingsScreen(services(hub.api), initialPage = scene.page, onBack = {})
                        }
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) for (next in Scene.entries) {
            style = s; dark = d; scene = next
            rule.waitUntil(5_000) { shown(if (next == Scene.MailPageOff) offLine else text(R.string.settings_diag_phone)) }
            if (next == Scene.ConnectorsOn) rule.waitUntil(5_000) { rule.onAllNodesWithText("me@example.com", substring = true).fetchSemanticsNodes().isNotEmpty() ||
                rule.onAllNodes(hasText(text(R.string.settings_row_mail)) and hasClickAction()).fetchSemanticsNodes().isNotEmpty() }
            rule.waitForIdle()
            val name = next.name.replace(Regex("([a-z])([A-Z])"), "$1-$2").lowercase()
            File(out(), "settings-$name-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi") @Test fun capturesInEnglish() = shots("en")
    @Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi") @Test fun capturesInChinese() = shots("zh")
    @Config(sdk = [35], qualifiers = "ja-w400dp-h900dp-xhdpi") @Test fun capturesInJapanese() = shots("ja")
}
