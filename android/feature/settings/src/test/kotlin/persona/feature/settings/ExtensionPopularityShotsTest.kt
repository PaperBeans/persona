package persona.feature.settings

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.Density
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.network.httpHeaders
import coil3.request.ErrorResult
import coil3.request.allowHardware
import coil3.request.SuccessResult
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.CatalogExtension
import persona.core.contracts.api.ExtensionCatalog
import persona.core.contracts.api.ExtensionDetail
import persona.core.contracts.api.ExtensionListing
import persona.core.contracts.api.ExtensionMarket
import persona.core.contracts.api.HubExtension
import persona.core.designsystem.AppStyle
import persona.core.designsystem.JRow
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import java.io.File
import java.util.concurrent.CopyOnWriteArrayList

/** #4780: real catalog/list/detail pages in Native JVM graphics, not emulator or live-network acceptance. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-xhdpi")
class ExtensionPopularityShotsTest {
    @get:Rule val rule = createComposeRule()
    private val requests = CopyOnWriteArrayList<String>()
    private val decodeFailures = CopyOnWriteArrayList<Throwable>()
    private val loading = CompletableDeferred<Unit>()
    // Optional acceptance input: a public signed-catalog PNG downloaded separately, decoded by Coil rather than painted by the fixture.
    private val realLogo = System.getenv("PERSONA_CATALOG_LOGO_FILE")?.let(::File)?.also { require(it.isFile) }
    private val pictures by lazy {
        ImageLoader.Builder(ApplicationProvider.getApplicationContext<Context>()).coroutineContext(Dispatchers.Unconfined)
            .components {
                // Robolectric's StaticImageDecoder rejects real PNG input; Coil's BitmapFactory decoder supports native JVM graphics.
                add(coil3.decode.BitmapFactoryDecoder.Factory())
                add(Interceptor { chain ->
                    val url = chain.request.data.toString()
                    requests += url
                    assertTrue("Catalog images carry no hub authentication", chain.request.httpHeaders["Authorization"] == null)
                    if (url.contains("loading")) loading.await()
                    if (realLogo != null && url.endsWith("/notes.png")) {
                        try {
                            val result = chain.withRequest(chain.request.newBuilder().data(realLogo).allowHardware(false).build()).proceed()
                            if (result is ErrorResult) decodeFailures += result.throwable
                            result
                        } catch (failure: Throwable) { decodeFailures += failure; throw failure }
                    } else if (url.contains("missing")) ErrorResult(null, chain.request, IllegalStateException("Synthetic image failure"))
                    else SuccessResult(Bitmap.createScaledBitmap(ShowcasePictures.bitmap(url), 64, 64, false).asImage(), chain.request, DataSource.MEMORY)
                })
            }.build()
    }

    @Before fun installPictures() { SingletonImageLoader.setUnsafe(pictures) }
    @After fun releasePictures() {
        loading.complete(Unit)
        SingletonImageLoader.reset()
        pictures.shutdown()
    }

    private val timestamp = "2026-11-11T08:00:00Z"
    private val entries = listOf(
        CatalogExtension("official", id = "popular", name = "GitHub", version = "1.0.0", description = "Repository activity",
            iconUrl = "https://extensions.example.com/notes.png", downloads = 20, downloadsUpdatedAt = timestamp),
        CatalogExtension("official", id = "lower", name = "Alpha tools", version = "1.0.0", description = "A logo that cannot load",
            iconUrl = "https://extensions.example.com/missing.png", downloads = 10),
        CatalogExtension("official", id = "zero", name = "Beta zero", version = "1.0.0", description = "A supplied count of zero",
            iconUrl = "http://extensions.example.com/invalid.png", downloads = 0),
        CatalogExtension("official", id = "older", name = "Older extension", version = "1.0.0", description = "No logo or count on an older hub"),
    )
    private val installed = entries.map {
        HubExtension(it.id, it.name, "ui", "running", source = "installed", enabled = true, version = it.version,
            iconUrl = it.iconUrl, downloads = it.downloads, downloadsUpdatedAt = it.downloadsUpdatedAt)
    }
    private val api = object : ExtensionApiStub() {
        override suspend fun listing() = ExtensionListing(installed, canInstall = true, markets = true)
        override suspend fun extensions() = installed
        override suspend fun extension(id: String) = ExtensionDetail(installed.first { it.id == id }, origin = "market:official/$id")
        override suspend fun catalog(query: String, market: String?, refresh: Boolean) = ExtensionCatalog(
            listOf(ExtensionMarket("official", "https://extensions.example.com/catalog/", "Official extensions", official = true, downloadsUpdatedAt = timestamp)),
            entries,
        )
    }

    private enum class Page { Market, Installed, CatalogDetail, InstalledDetail }

    private fun ready(page: Page) {
        rule.waitUntil(timeoutMillis = 10_000) {
            if (decodeFailures.isNotEmpty()) throw AssertionError("Actual catalog PNG decode failed", decodeFailures.first())
            rule.onAllNodesWithTag("extension-logo-loaded", useUnmergedTree = true).fetchSemanticsNodes().size == 1
        }
        if (page == Page.Market || page == Page.Installed) {
            val tops = entries.map { rule.onAllNodesWithText(it.name)[0].fetchSemanticsNode().boundsInRoot.top }
            assertEquals("Hub popularity order is retained, not replaced with alphabetical order", tops.sorted(), tops)
            assertEquals(3, rule.onAllNodesWithTag("extension-logo-fallback", useUnmergedTree = true).fetchSemanticsNodes().size)
            for (count in listOf(20, 10, 0)) assertTrue(rule.onAllNodesWithText("下载 $count 次", substring = true).fetchSemanticsNodes().isNotEmpty())
            val oldText = rule.onAllNodesWithText("Older extension")[0].fetchSemanticsNode().config.toString()
            assertFalse("Missing count is never made into zero", oldText.contains("下载"))
        } else {
            assertTrue(rule.onAllNodesWithText("下载 20 次", substring = true).fetchSemanticsNodes().isNotEmpty())
        }
        assertFalse("HTTP or malformed metadata must not reach the image loader", requests.any { it.startsWith("http:") })
    }

    private fun capture(name: String): IntArray {
        rule.waitForIdle()
        val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        if (realLogo != null) {
            val bounds = rule.onNodeWithTag("extension-logo-loaded", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertEquals("Transparent catalog PNG has a light backing in every style and appearance",
                Tokens.System.Light.page.toArgb(), image.getPixel(bounds.left.toInt() + 1, bounds.top.toInt() + 1))
        }
        val pixels = IntArray(image.width * image.height)
        image.getPixels(pixels, 0, image.width, 0, 0, image.width, image.height)
        assertTrue("The real page rendered visible content", pixels.distinct().size > 100)
        val output = File("build/extension-popularity-shots").also { it.mkdirs() }
        File(output, "$name.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
        return pixels
    }

    @Test fun catalogAndInstalledLogosCountsAndDetailsInEveryStyleAndTextSize() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var fontScale by mutableStateOf(1f)
        var page by mutableStateOf(Page.Market)
        rule.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) {
                        key(page) {
                            when (page) {
                                Page.Market -> ExtensionMarketPage(api, {}, {}, {})
                                Page.Installed -> ExtensionsPage(api, {}, { _, _ -> }, {})
                                Page.CatalogDetail -> ExtensionCatalogPage(api, "official", "popular", {}, {})
                                Page.InstalledDetail -> ExtensionPage(api, "popular", {})
                            }
                        }
                    }
                }
            }
        }
        for (p in Page.entries) for (s in AppStyle.entries) {
            var defaultLight: IntArray? = null
            for (scale in listOf(1f, 2f)) {
                var light: IntArray? = null
                for (d in listOf(false, true)) {
                    rule.runOnIdle { page = p; style = s; dark = d; fontScale = scale }
                    ready(p)
                    val pixels = capture("${p.name.lowercase()}-${s.key}-${if (d) "dark" else "light"}-${if (scale == 1f) "default" else "large"}")
                    if (!d) {
                        light = pixels
                        if (scale == 1f) defaultLight = pixels
                        else assertFalse("Compose large fonts must change the actual pixels", pixels.contentEquals(defaultLight!!))
                    } else assertFalse("Light and dark must differ", pixels.contentEquals(light!!))
                }
            }
        }
    }

    /** Primary market evidence pairs the real public logo with its brand; metadata and counts remain a synthetic fixture. */
    @Test fun marketBrandMatchesCatalogLogoInEveryStyle() {
        org.junit.Assume.assumeTrue("An actual catalog PNG was supplied for brand evidence", realLogo != null)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) { ExtensionMarketPage(api, {}, {}, {}) }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            ready(Page.Market)
            assertTrue(rule.onAllNodesWithText("GitHub").fetchSemanticsNodes().isNotEmpty())
            assertTrue(rule.onAllNodesWithText("Repository activity", substring = true).fetchSemanticsNodes().isNotEmpty())
            capture("brand-market-${s.key}-${if (d) "dark" else "light"}-default")
        }
    }

    @Test fun legacyGlyphAlignmentAndLongSubtitleStayPixelIdenticalWithoutMetadata() {
        var custom by mutableStateOf(false)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val subtitle = "A legacy extension with a long status description that wraps past two lines and must keep its original layout without a download count."
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("legacy-row").fillMaxSize().background(PersonaTheme.palette.background)) {
                    if (custom) JRow("Legacy extension", subtitle = subtitle, subtitleLines = installedSubtitleLines(null), leading = { ExtensionLogo(null) })
                    else JRow("Legacy extension", subtitle = subtitle, leadingIcon = PhIcons.PuzzlePiece)
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d; custom = false }
            val before = rule.onNodeWithTag("legacy-row").captureToImage().asAndroidBitmap()
            rule.runOnIdle { custom = true }
            val after = rule.onNodeWithTag("legacy-row").captureToImage().asAndroidBitmap()
            assertTrue("Missing metadata must retain the old glyph, gap, tint and two-line subtitle in ${s.key}/$d", before.sameAs(after))
        }
    }

    @Test fun loadingFailureInvalidAndMissingLogoAllUseTheOriginalGenericIcon() {
        var url by mutableStateOf<String?>("https://extensions.example.com/loading.png")
        rule.setContent {
            PersonaTheme(darkTheme = true, reduceMotion = true) {
                Box(Modifier.testTag("logo-test").size(Tokens.Size.iconS).background(PersonaTheme.palette.background)) { ExtensionLogo(url) }
            }
        }
        rule.waitUntil { requests.any { it.contains("loading") } }
        rule.onNodeWithTag("extension-logo-fallback", useUnmergedTree = true).assertExists()
        loading.complete(Unit)
        rule.waitUntil { rule.onAllNodesWithTag("extension-logo-loaded", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        rule.runOnIdle { url = "https://extensions.example.com/missing.png" }
        rule.waitForIdle()
        rule.waitUntil { requests.any { it.contains("missing") } }
        rule.onNodeWithTag("extension-logo-fallback", useUnmergedTree = true).assertExists()
        for (value in listOf(null, "https:///logo.png", "http://extensions.example.com/logo.png")) {
            rule.runOnIdle { url = value }
            rule.onNodeWithTag("extension-logo-fallback", useUnmergedTree = true).assertExists()
        }
        assertEquals("Failure removes the light logo backing", Tokens.System.Dark.page.toArgb(),
            rule.onNodeWithTag("logo-test").captureToImage().asAndroidBitmap().getPixel(0, 0))
        assertEquals(2, requests.size)
    }
}
