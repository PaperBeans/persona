package persona.feature.settings

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasText
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.AppUpdate
import persona.core.contracts.api.AppUpdates
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.LocalAppStyleStore
import persona.core.designsystem.LocalLanguageHost
import persona.core.designsystem.PersonaTheme
import java.io.File

/** The real settings screen renders an available release without starting installation. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AppUpdateShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun availableReleaseOffersUserInitiatedInstallation() {
        val services = demoSettingsServices(rule.activity.resources).copy(
            demo = false,
            updates = object : AppUpdates {
                override suspend fun check() = AppUpdate(
                    2_000_000_000, "1.0.2000000000", "/api/v1/app/android/apk",
                    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
                    "自动发布版本 · 下载校验后由系统确认安装",
                )
            },
        )
        rule.setContent {
            val store = remember { AppStyleStore(rule.activity) }
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                CompositionLocalProvider(LocalLanguageHost provides rule.activity, LocalAppStyleStore provides store) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("update-screen")) {
                        SettingsScreen(services)
                    }
                }
            }
        }
        rule.waitForIdle()
        val about = rule.activity.getString(R.string.settings_home_about)
        rule.onNode(hasScrollAction()).performScrollToNode(hasText(about))
        rule.onNodeWithText(about).performClick()
        val install = rule.activity.getString(R.string.settings_update_install)
        rule.onNode(hasScrollAction()).performScrollToNode(hasText(install))
        rule.onNodeWithText(install).assertExists()
        val image = rule.onNodeWithTag("update-screen").captureToImage().asAndroidBitmap()
        val output = File("build/update-shots/settings-available-update.png")
        check(output.parentFile!!.mkdirs() || output.parentFile!!.isDirectory)
        output.outputStream().use { check(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }
}
