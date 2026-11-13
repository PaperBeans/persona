package persona.feature.settings

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.HubDesktop
import persona.core.contracts.api.FloatingCapsuleSettings
import persona.core.designsystem.*
import persona.core.navigation.SettingsPage
import java.io.File

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1800dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class NotificationGroupShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private class Capsule : FloatingCapsuleSettings {
        override val mode = MutableStateFlow("active")
        override val permissionGranted = MutableStateFlow(false)
        override fun setMode(mode: String) = error("Opening a settings row must not change capsule mode")
        override fun refreshPermission() = error("Opening a settings row must not refresh permissions")
        override fun requestPermission() = error("Opening a settings row must not request permissions")
    }

    @Test fun groupedRowsKeepTheirDestinationsAcrossCapabilitiesAndThemes() {
        val demo = demoSettingsServices(rule.activity.resources)
        val capsule = Capsule()
        val desktops = MutableStateFlow(listOf(HubDesktop("desktop", "Desktop")))
        val variants = listOf(
            "capsule" to demo.copy(demo = false, capsule = capsule),
            "capsule-desktop" to demo.copy(demo = false, capsule = capsule, desktops = desktops),
            "no-capsule" to demo.copy(demo = false),
            "no-capsule-desktop" to demo.copy(demo = false, desktops = desktops),
        )
        val services = mutableStateOf(variants.first().second)
        val style = mutableStateOf(AppStyle.Glass)
        val dark = mutableStateOf(false)
        val opened = mutableListOf<SettingsPage>()
        val initialPage = mutableStateOf("settings")
        rule.setContent {
            val context = LocalContext.current
            val store = remember { AppStyleStore(context) }
            PersonaTheme(darkTheme = dark.value, reduceMotion = true, style = style.value) {
                CompositionLocalProvider(LocalLanguageHost provides rule.activity, LocalAppStyleStore provides store) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1700.dp).background(PersonaTheme.palette.background)) {
                        SettingsScreen(services.value, initialPage = initialPage.value, onOpen = { opened.add(it) })
                    }
                }
            }
        }
        val output = File("build/notification-group-shots").also { assertTrue(it.mkdirs() || it.isDirectory) }
        for ((name, service) in variants) {
            val styles = if (name == "capsule") AppStyle.entries else listOf(AppStyle.Glass)
            for (appStyle in styles) {
                val captures = mutableListOf<Bitmap>()
                for (isDark in listOf(false, true)) {
                    rule.runOnIdle { services.value = service; style.value = appStyle; dark.value = isDark; initialPage.value = "settings" }
                    rule.mainClock.advanceTimeBy(2_000)
                    rule.waitForIdle()
                    opened.clear()
                    rule.onNodeWithText(rule.activity.getString(R.string.settings_notification_background_title)).performClick()
                    assertEquals(listOf(SettingsPage.NotificationBackground), opened)
                    rule.runOnIdle { initialPage.value = "notification-background" }
                    rule.waitForIdle()
                    val destinations = buildList {
                        if (service.capsule != null) add(R.string.settings_capsule_title to SettingsPage.Capsule)
                        if (service.desktops != null) add(R.string.settings_desktops_banner to SettingsPage.Desktops)
                    }
                    if (service.capsule == null) rule.onNodeWithText(rule.activity.getString(R.string.settings_capsule_title)).assertDoesNotExist()
                    opened.clear()
                    for ((label, _) in destinations) rule.onNodeWithText(rule.activity.getString(label)).performClick()
                    assertEquals(destinations.map { it.second }, opened)
                    assertEquals("active", capsule.mode.value)
                    assertEquals(false, capsule.permissionGranted.value)
                    val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                    captures.add(bitmap)
                    val file = File(output, "$name-${appStyle.name.lowercase()}-${if (isDark) "dark" else "light"}.png")
                    file.outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
                }
                assertTrue("Light and dark screenshots must differ", !captures[0].sameAs(captures[1]))
            }
        }
    }
}
