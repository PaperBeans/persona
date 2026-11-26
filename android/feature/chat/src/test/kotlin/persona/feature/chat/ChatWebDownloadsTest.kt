package persona.feature.chat

import android.app.DownloadManager
import android.content.Context
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.widget.FrameLayout
import androidx.compose.foundation.clickable
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatWebDownloadsTest {
    @get:Rule val rule = createComposeRule()

    private val pageUrl = "https://example.invalid/releases/latest"
    private val apkUrl = "https://cdn.example.invalid/downloads/persona-release.apk"
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val created = mutableListOf<RecordingWebView>()
    private lateinit var pool: ChatWebViewPool

    @Before fun setUp() {
        CookieManager.getInstance().removeAllCookies(null)
        pool = ChatWebViewPool(context, NoWebFeatures) { viewContext ->
            RecordingWebView(viewContext).also(created::add)
        }
    }

    @After fun tearDown() {
        pool.destroy()
    }

    @Test fun foregroundMountedWebViewForwardsValidDownloadWithMetadataAndReferer() {
        val view = pool.acquire(pageUrl) as RecordingWebView
        pool.activate(view)
        val container = FrameLayout(context)
        container.addView(view)

        val received = mutableListOf<ChatWebDownloadRequest>()
        view.onDownload = { received += it }

        simulateDownloadStart(
            view = view,
            url = apkUrl,
            userAgent = "Mozilla/5.0 PersonaAndroid",
            contentDisposition = "attachment; filename=\"persona-21.apk\"",
            mimeType = "application/vnd.android.package-archive",
            contentLength = 42_000L,
        )

        assertEquals(1, received.size)
        val req = received.single()
        assertEquals(apkUrl, req.url)
        assertEquals("Mozilla/5.0 PersonaAndroid", req.userAgent)
        assertEquals("attachment; filename=\"persona-21.apk\"", req.contentDisposition)
        assertEquals("application/vnd.android.package-archive", req.mimeType)
        assertEquals(42_000L, req.contentLength)
        assertEquals(pageUrl, req.referer)
    }

    @Test fun backgroundPreloadedDetachedPausedAndReleasedViewsNeverTriggerDownloads() {
        val extensionlessDownload = "https://example.invalid/export?id=99"
        pool.preload(extensionlessDownload)
        val preloaded = pool.existing(extensionlessDownload) as RecordingWebView
        val preloadedEvents = mutableListOf<ChatWebDownloadRequest>()
        preloaded.onDownload = { preloadedEvents += it }

        // Background preloaded view is paused, detached, and inactive: must suppress drive-by downloads.
        simulateDownloadStart(preloaded, extensionlessDownload)
        assertTrue(preloadedEvents.isEmpty())
        assertTrue(preloaded.suppressedBackgroundDownload)

        // Acquiring the preloaded download URL in the foreground reloads it cleanly.
        val acquired = pool.acquire(extensionlessDownload) as RecordingWebView
        assertEquals(listOf(extensionlessDownload, extensionlessDownload), acquired.loads)
        assertFalse(acquired.suppressedBackgroundDownload)

        // Active but detached (parent == null) still cannot start a download.
        pool.activate(acquired)
        acquired.onDownload = { preloadedEvents += it }
        simulateDownloadStart(acquired, apkUrl)
        assertTrue(preloadedEvents.isEmpty())

        // Mounted, then paused via pool.pause() cannot start a download.
        val container = FrameLayout(context)
        container.addView(acquired)
        pool.pause()
        simulateDownloadStart(acquired, apkUrl)
        assertTrue(preloadedEvents.isEmpty())

        // Resumed and mounted succeeds.
        pool.resume()
        simulateDownloadStart(acquired, apkUrl)
        assertEquals(1, preloadedEvents.size)

        // After release, callback is cleared and downloads are suppressed.
        pool.release(acquired)
        assertNull(acquired.onDownload)
        simulateDownloadStart(acquired, apkUrl)
        assertEquals(1, preloadedEvents.size)
    }

    @Test fun directDownloadExtensionsSkipBackgroundPreload() {
        pool.preload("https://example.invalid/files/persona.apk")
        pool.preload("https://example.invalid/files/archive.zip")
        pool.preload("https://example.invalid/files/report.pdf")
        assertTrue(created.isEmpty())

        // Explicit foreground acquire still creates and loads normally.
        val view = pool.acquire("https://example.invalid/files/persona.apk") as RecordingWebView
        assertEquals(listOf("https://example.invalid/files/persona.apk"), view.loads)
    }

    @Test fun nonHttpSchemesAndCredentialUrlsAreRefusedInPoolAndDownloader() {
        val view = pool.acquire(pageUrl) as RecordingWebView
        pool.activate(view)
        FrameLayout(context).addView(view)

        val received = mutableListOf<ChatWebDownloadRequest>()
        view.onDownload = { received += it }

        val rejectedUrls = listOf(
            "file:///etc/passwd",
            "content://com.example.provider/secret",
            "javascript:alert(1)",
            "data:text/plain;base64,SGVsbG8=",
            "blob:https://example.invalid/1234",
            "persona://session/honor/abc",
            "https://user:pass@example.invalid/file.apk",
            "",
        )

        var enqueueCalls = 0
        var browserCalls = 0
        for (badUrl in rejectedUrls) {
            simulateDownloadStart(view, badUrl)
            val result = startWebDownload(
                context = context,
                request = ChatWebDownloadRequest(badUrl, "UA", "", "application/octet-stream", -1L, pageUrl),
                enqueue = { _, _ -> enqueueCalls++; 1L },
                openInBrowser = { _, _ -> browserCalls++; true },
            )
            assertEquals("Expected Refused for $badUrl", ChatWebDownloadResult.Refused, result)
        }
        assertTrue(received.isEmpty())
        assertEquals(0, enqueueCalls)
        assertEquals(0, browserCalls)
    }

    @Test fun loopbackAndForwardedUrlsAreRejectedWithoutDownloadManagerOrBrowserFallback() {
        val loopbackUrls = listOf(
            "http://localhost:4173/build.apk",
            "https://localhost:8443/report.pdf",
            "http://127.0.0.1:8080/bundle.zip",
            "http://127.0.0.42:9000/file.bin",
            "http://[::1]:3000/output.tar.gz",
            "http://dev.localhost:5173/app.apk",
        )
        var enqueueCalls = 0
        var browserCalls = 0
        for (loopback in loopbackUrls) {
            val result = startWebDownload(
                context = context,
                request = ChatWebDownloadRequest(loopback, "UA", "", "application/octet-stream", -1L, loopback),
                isForwarded = true,
                enqueue = { _, _ -> enqueueCalls++; 1L },
                openInBrowser = { _, _ -> browserCalls++; true },
            )
            assertEquals("Expected LoopbackUnsupported for $loopback", ChatWebDownloadResult.LoopbackUnsupported, result)
        }
        assertEquals(0, enqueueCalls)
        assertEquals(0, browserCalls)
    }

    @Test fun startWebDownloadPassesSanitizedCookiesUserAgentRefererAndFilename() {
        var captured: PreparedWebDownload? = null
        val result = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(
                url = apkUrl,
                userAgent = "Mozilla/5.0\r\nInjected: bad",
                contentDisposition = "attachment; filename=\"fallback.apk\"; filename*=UTF-8''Persona%20Build+21.apk",
                mimeType = "application/vnd.android.package-archive; charset=binary",
                contentLength = 1024L,
                referer = "https://example.invalid/releases/latest#secret-fragment",
            ),
            cookieForUrl = { "sid=abc123; theme=dark\r\nX-Bad: 1" },
            enqueue = { _, prepared -> captured = prepared; 77L },
            openInBrowser = { _, _ -> error("should not fall back") },
        )

        assertEquals(ChatWebDownloadResult.Started("Persona Build+21.apk", 77L), result)
        val prepared = checkNotNull(captured)
        assertEquals(apkUrl, prepared.url)
        assertEquals("Persona Build+21.apk", prepared.fileName)
        assertEquals("application/vnd.android.package-archive", prepared.mimeType)
        assertEquals("sid=abc123; theme=darkX-Bad: 1", prepared.cookie)
        assertEquals("Mozilla/5.0Injected: bad", prepared.userAgent)
        assertEquals("https://example.invalid/releases/latest", prepared.referer)

        // HTTPS referer is stripped on HTTP download downgrade; loopback referer is stripped on forwarded pages.
        var downgraded: PreparedWebDownload? = null
        val httpResult = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(
                url = "http://cdn.example.invalid/file.zip",
                userAgent = "UA",
                contentDisposition = "",
                mimeType = "application/zip",
                contentLength = 10L,
                referer = "https://example.invalid/private",
            ),
            cookieForUrl = { null },
            enqueue = { _, preparedReq -> downgraded = preparedReq; 78L },
            openInBrowser = { _, _ -> false },
        )
        assertTrue(httpResult is ChatWebDownloadResult.Started)
        assertNull(downgraded?.referer)

        var fromForwarded: PreparedWebDownload? = null
        val fwdResult = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(
                url = apkUrl,
                userAgent = "UA",
                contentDisposition = "",
                mimeType = "application/vnd.android.package-archive",
                contentLength = 10L,
                referer = "http://localhost:4173/index.html",
            ),
            isForwarded = true,
            cookieForUrl = { null },
            enqueue = { _, preparedReq -> fromForwarded = preparedReq; 79L },
            openInBrowser = { _, _ -> false },
        )
        assertTrue(fwdResult is ChatWebDownloadResult.Started)
        assertNull(fromForwarded?.referer)
    }

    @Test fun resolveDownloadFileNameSanitizesTraversalAndHandlesRfc5987AndApkExtension() {
        assertEquals(
            "evil.apk",
            resolveDownloadFileName(apkUrl, "attachment; filename=\"../../evil.apk\"", "application/vnd.android.package-archive"),
        )
        assertEquals(
            "a_b_c_.txt",
            resolveDownloadFileName("https://example.invalid/dl", "attachment; filename=\"sub\\dir/a:b*c?.txt\"", "text/plain"),
        )
        assertEquals(
            "测试+v1.apk",
            resolveDownloadFileName(
                "https://example.invalid/dl",
                "attachment; filename*=UTF-8''%E6%B5%8B%E8%AF%95+v1.apk",
                "application/vnd.android.package-archive",
            ),
        )
        assertEquals(
            "dl.apk",
            resolveDownloadFileName("https://example.invalid/dl?id=42", "", "application/vnd.android.package-archive"),
        )
        assertEquals(
            "downloadfile.apk",
            resolveDownloadFileName("https://example.invalid/?id=42", "", "application/vnd.android.package-archive"),
        )
        val longName = "a".repeat(160) + ".apk"
        val truncated = resolveDownloadFileName("https://example.invalid/dl", "attachment; filename=\"$longName\"", "application/vnd.android.package-archive")
        assertTrue(truncated.length <= 120)
        assertTrue(truncated.endsWith(".apk"))
    }

    @Test fun platformDownloadManagerEnqueuesPublicDownloadRequest() {
        CookieManager.getInstance().setCookie(apkUrl, "token=live-cookie")
        val result = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(
                url = apkUrl,
                userAgent = "PersonaAndroid/1.0",
                contentDisposition = "attachment; filename=\"persona-test.apk\"",
                mimeType = "application/vnd.android.package-archive",
                contentLength = 512L,
                referer = pageUrl,
            ),
            openInBrowser = { _, _ -> error("DownloadManager should succeed in Robolectric") },
        )

        assertTrue("Expected Started, got $result", result is ChatWebDownloadResult.Started)
        val started = result as ChatWebDownloadResult.Started
        assertEquals("persona-test.apk", started.fileName)
        val manager = context.getSystemService(DownloadManager::class.java)
        assertNotNull(manager)
        val request = shadowOf(manager).getRequest(started.downloadId)
        assertNotNull(request)
        val shadowRequest = shadowOf(request)
        assertEquals(apkUrl, shadowRequest.uri.toString())
        assertEquals("persona-test.apk", shadowRequest.title.toString())
        assertEquals(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED, shadowRequest.notificationVisibility)
        assertTrue(shadowRequest.destination.toString().endsWith("/Download/persona-test.apk"))
    }

    @Test fun downloadManagerFailureFallsBackToExternalBrowserOrReportsFailure() {
        val openedUrls = mutableListOf<String>()
        val fallbackResult = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(apkUrl, "UA", "", "application/vnd.android.package-archive", -1L, pageUrl),
            enqueue = { _, _ -> throw SecurityException("DownloadManager disabled") },
            openInBrowser = { _, url -> openedUrls += url; true },
        )
        assertEquals(ChatWebDownloadResult.OpenedInBrowser(apkUrl), fallbackResult)
        assertEquals(listOf(apkUrl), openedUrls)

        val failedResult = startWebDownload(
            context = context,
            request = ChatWebDownloadRequest(apkUrl, "UA", "", "application/vnd.android.package-archive", -1L, pageUrl),
            enqueue = { _, _ -> null },
            openInBrowser = { _, _ -> false },
        )
        assertEquals(ChatWebDownloadResult.Failed("persona-release.apk"), failedResult)
    }

    @Test fun handleWebDownloadEmitsLocalizedNoticesForEveryOutcome() {
        val resources = context.resources
        val req = ChatWebDownloadRequest(apkUrl, "UA", "", "application/vnd.android.package-archive", -1L, pageUrl)
        val notices = mutableListOf<Pair<String, FeedbackStyle>>()

        val outcomes = listOf(
            ChatWebDownloadResult.Started("app.apk", 1L),
            ChatWebDownloadResult.OpenedInBrowser(apkUrl),
            ChatWebDownloadResult.LoopbackUnsupported,
            ChatWebDownloadResult.Refused,
            ChatWebDownloadResult.Failed("app.apk"),
        )
        for (outcome in outcomes) {
            val _ = handleWebDownload(context, resources, ChatWebDownloader { _, _, _ -> outcome }, req, false) { msg, style ->
                notices += msg to style
            }
        }

        assertEquals(
            listOf(
                resources.getString(R.string.chat_web_download_started, "app.apk") to FeedbackStyle.Success,
                resources.getString(R.string.chat_web_download_opened_browser) to FeedbackStyle.Info,
                resources.getString(R.string.chat_web_download_loopback_unsupported) to FeedbackStyle.Error,
                resources.getString(R.string.chat_web_cannot_open) to FeedbackStyle.Error,
                resources.getString(R.string.chat_web_download_failed) to FeedbackStyle.Error,
            ),
            notices,
        )
    }

    @Test fun chatLinkHostRoutesForegroundDownloadAndShowsFeedbackNotice() {
        lateinit var composeView: View
        val notices = mutableListOf<Pair<String, FeedbackStyle>>()
        val captured = mutableListOf<Pair<ChatWebDownloadRequest, Boolean>>()
        val feedback = object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean) = true
            override fun notice(message: String): Boolean { notices += message to FeedbackStyle.Info; return true }
            override fun notice(message: String, style: FeedbackStyle): Boolean { notices += message to style; return true }
        }
        val downloader = ChatWebDownloader { _, req, forwarded ->
            captured += req to forwarded
            ChatWebDownloadResult.Started("persona-build.apk", 42L)
        }

        rule.setContent {
            composeView = LocalView.current
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(
                    LocalChatWebFeatures provides NoWebFeatures,
                    LocalChatWebDownloader provides downloader,
                    LocalAppFeedback provides feedback,
                ) {
                    ChatLinkHost(onContext = {}) { _ ->
                        val uri = LocalUriHandler.current
                        Text("Open download page", Modifier.clickable { uri.openUri(pageUrl) })
                    }
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("Open download page").performClick()
        rule.waitForIdle()

        val webView = rule.runOnIdle { findWebViews(composeView).single() }
        rule.runOnIdle {
            simulateDownloadStart(
                view = webView,
                url = apkUrl,
                contentDisposition = "attachment; filename=\"persona-build.apk\"",
                mimeType = "application/vnd.android.package-archive",
            )
        }

        assertEquals(1, captured.size)
        assertEquals(apkUrl, captured.single().first.url)
        assertFalse(captured.single().second)
        assertEquals(
            listOf(context.resources.getString(R.string.chat_web_download_started, "persona-build.apk") to FeedbackStyle.Success),
            notices,
        )
    }

    private fun findWebViews(root: View): List<ChatSelectionWebView> {
        val found = mutableListOf<ChatSelectionWebView>()
        fun visit(v: View) {
            if (v is ChatSelectionWebView) found += v
            if (v is ViewGroup) repeat(v.childCount) { visit(v.getChildAt(it)) }
        }
        visit(root)
        return found
    }
}
