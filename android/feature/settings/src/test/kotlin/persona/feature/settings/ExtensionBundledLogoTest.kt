package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.PersonaTheme
import java.io.ByteArrayOutputStream
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class ExtensionBundledLogoTest {
    @get:Rule val rule = createComposeRule()
    private val path = "/api/v1/extensions/notes/-/icon?v=1.0.0"
    private val calls = CopyOnWriteArrayList<String>()
    private val stopped = AtomicInteger()
    private val waiting = CompletableDeferred<Unit>()
    private val png by lazy {
        ByteArrayOutputStream().use { output ->
            Bitmap.createBitmap(16, 16, Bitmap.Config.ARGB_8888).apply { eraseColor(android.graphics.Color.BLUE) }
                .compress(Bitmap.CompressFormat.PNG, 100, output)
            output.toByteArray()
        }
    }
    private val api = object : ExtensionApiStub() {
        override suspend fun icon(path: String): ByteArray {
            calls += path
            if (path.endsWith("slow")) try { waiting.await() } finally { stopped.incrementAndGet() }
            if (path.endsWith("bad")) error("Synthetic unavailable artwork")
            if (path.endsWith("malformed")) return "Not a PNG".toByteArray()
            return png
        }
    }
    private fun loaded(count: Int) = rule.waitUntil(10_000) {
        rule.onAllNodesWithTag("extension-logo-loaded", useUnmergedTree = true).fetchSemanticsNodes().size == count
    }

    @Test fun successFailureNoImageUrlRetryMemoryCacheAndOwnerReplacement() {
        var url by mutableStateOf<String?>(null)
        var owner by mutableStateOf<persona.core.contracts.api.ExtensionManagement>(api)
        rule.setContent {
            val pictures = rememberExtensionLogoLoader(owner)
            PersonaTheme(reduceMotion = true) { Box(Modifier.testTag("logo")) { ExtensionLogo(url, owner, pictures) } }
        }
        loaded(0); assertTrue(calls.isEmpty())
        rule.runOnIdle { url = path }; loaded(1)
        assertEquals(listOf(path), calls.toList())
        rule.runOnIdle { url = path + "bad" }; loaded(0)
        rule.waitUntil(10_000) { calls.contains(path + "bad") }
        rule.runOnIdle { url = path + "malformed" }; loaded(0)
        rule.waitUntil(10_000) { calls.contains(path + "malformed") }
        rule.runOnIdle { url = path }; loaded(1)
        assertEquals("The page-owned private cache reuses successful decoded artwork", 1, calls.count { it == path })
        rule.runOnIdle { url = path + "2" }; loaded(1)
        rule.waitUntil(10_000) { calls.contains(path + "2") }
        rule.runOnIdle { url = null }; loaded(0)
        val otherCalls = AtomicInteger()
        rule.runOnIdle {
            owner = object : ExtensionApiStub() { override suspend fun icon(path: String): ByteArray { otherCalls.incrementAndGet(); return png } }
            url = path
        }
        loaded(1)
        assertEquals("A different pairing/manager API cannot reuse the old private cache", 1, otherCalls.get())
    }

    @Test fun marketDefaultOwnerChangesRebuildThePrivateLoaderEvenWithOneSingletonApi() {
        val choice = persona.core.contracts.api.AssistantManagerChoice
        val owners = CopyOnWriteArrayList<String>()
        val sameApi = object : ExtensionApiStub() {
            override suspend fun catalog(query: String, market: String?, refresh: Boolean) = persona.core.contracts.api.ExtensionCatalog(
                markets = listOf(persona.core.contracts.api.ExtensionMarket(id = "official", name = "Official")),
                extensions = listOf(persona.core.contracts.api.CatalogExtension(id = "notes", name = "Notes", market = "official", iconUrl = path)),
            )
            override suspend fun icon(path: String): ByteArray {
                val pinned = kotlinx.coroutines.currentCoroutineContext()[persona.core.contracts.api.ManagerPins]
                    ?.manager(listOf("extensions", "notes", "-", "icon"))
                owners += if (pinned == "") choice.current.value.defaultId else pinned ?: "unpinned"
                return png
            }
        }
        choice.choose("icon-hub", null, "owner-a")
        try {
            rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionMarketPage(sameApi, {}, {}, {}) } }
            loaded(1)
            assertEquals(listOf("owner-a"), owners.toList())
            rule.runOnIdle { choice.choose("icon-hub", null, "owner-b") }
            rule.waitUntil(10_000) { owners.size == 2 }
            loaded(1)
            assertEquals(listOf("owner-a", "owner-b"), owners.toList())
        } finally { choice.reset(); choice.forgetItems() }
    }

    @Test fun pendingDownloadIsCancelledOnUrlChangeAndOnDisposal() {
        var url by mutableStateOf<String?>(path + "slow")
        var visible by mutableStateOf(true)
        rule.setContent {
            if (visible) {
                val pictures = rememberExtensionLogoLoader(api)
                PersonaTheme(reduceMotion = true) { ExtensionLogo(url, api, pictures) }
            }
        }
        rule.waitUntil(10_000) { calls.size == 1 }
        rule.runOnIdle { url = path }; loaded(1)
        rule.waitUntil(10_000) { stopped.get() == 1 }
        rule.runOnIdle { url = path + "2slow" }
        rule.waitForIdle()
        rule.waitUntil(10_000) { calls.contains(path + "2slow") }
        rule.runOnIdle { visible = false }
        rule.waitForIdle()
        rule.waitUntil(10_000) { stopped.get() == 2 }
        assertFalse("Cancellation never completes the pending artwork", waiting.isCompleted)
    }
}
