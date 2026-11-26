package persona.feature.chat

import android.app.DownloadManager
import android.content.Context
import android.content.res.Resources
import android.net.Uri
import android.os.Environment
import android.webkit.CookieManager
import android.webkit.URLUtil
import androidx.compose.runtime.staticCompositionLocalOf
import persona.core.designsystem.FeedbackStyle
import java.io.ByteArrayOutputStream
import java.net.URI
import java.nio.charset.Charset
import java.nio.charset.StandardCharsets

/** A file download emitted by an active, visible in-app WebView. */
internal data class ChatWebDownloadRequest(
    val url: String,
    val userAgent: String,
    val contentDisposition: String,
    val mimeType: String,
    val contentLength: Long,
    val referer: String? = null,
)

/** Outcome of handling an in-app WebView file download. */
internal sealed interface ChatWebDownloadResult {
    data class Started(val fileName: String, val downloadId: Long) : ChatWebDownloadResult
    data class OpenedInBrowser(val url: String) : ChatWebDownloadResult
    data object LoopbackUnsupported : ChatWebDownloadResult
    data object Refused : ChatWebDownloadResult
    data class Failed(val fileName: String?) : ChatWebDownloadResult
}

/** Strategy that starts a WebView file download; production uses [Platform], tests may substitute a recording stub. */
internal fun interface ChatWebDownloader {
    fun download(context: Context, request: ChatWebDownloadRequest, isForwarded: Boolean): ChatWebDownloadResult

    companion object {
        val Platform = ChatWebDownloader { context, request, isForwarded ->
            startWebDownload(context, request, isForwarded)
        }
    }
}

/** Which downloader [ChatLinkHost] invokes for in-app WebView downloads. */
internal val LocalChatWebDownloader = staticCompositionLocalOf { ChatWebDownloader.Platform }

/**
 * Validated request data prepared for Android's [DownloadManager]. Separated from system service invocation so
 * URL validation, filename resolution, and header sanitization can be tested deterministically.
 */
internal data class PreparedWebDownload(
    val url: String,
    val fileName: String,
    val mimeType: String?,
    val cookie: String?,
    val userAgent: String?,
    val referer: String?,
)

/**
 * Enqueues an `http(s)` WebView download into Android's system [DownloadManager] targeting public
 * [Environment.DIRECTORY_DOWNLOADS] with completion notifications, carrying the WebView's cookies, user agent,
 * and safe referer. Rejects non-web schemes and loopback / forwarded-port URLs; falls back to the system browser
 * when [DownloadManager] is unavailable or rejects the request.
 */
internal fun startWebDownload(
    context: Context,
    request: ChatWebDownloadRequest,
    isForwarded: Boolean = false,
    cookieForUrl: (String) -> String? = ::platformCookieForUrl,
    enqueue: (Context, PreparedWebDownload) -> Long? = ::platformEnqueueDownload,
    openInBrowser: (Context, String) -> Boolean = ::openInSystemBrowser,
): ChatWebDownloadResult {
    val safeUrl = ChatLinks.webUrl(request.url) ?: return ChatWebDownloadResult.Refused
    if (isLoopbackWebUrl(safeUrl)) return ChatWebDownloadResult.LoopbackUnsupported
    val baseMimeType = sanitizeHeaderValue(request.mimeType)?.substringBefore(';')?.trim()?.takeIf { it.isNotEmpty() }
    val fileName = resolveDownloadFileName(safeUrl, request.contentDisposition, baseMimeType.orEmpty())
    val prepared = PreparedWebDownload(
        url = safeUrl,
        fileName = fileName,
        mimeType = baseMimeType,
        cookie = sanitizeHeaderValue(runCatching { cookieForUrl(safeUrl) }.getOrNull()),
        userAgent = sanitizeHeaderValue(request.userAgent),
        referer = if (isForwarded) null else safeRefererHeader(request.referer, safeUrl),
    )
    val downloadId = runCatching { enqueue(context, prepared) }.getOrNull()
    if (downloadId != null && downloadId >= 0L) {
        return ChatWebDownloadResult.Started(fileName, downloadId)
    }
    return if (openInBrowser(context, safeUrl)) {
        ChatWebDownloadResult.OpenedInBrowser(safeUrl)
    } else {
        ChatWebDownloadResult.Failed(fileName)
    }
}

/** Runs [downloader] and emits a localized user-facing notice describing the outcome. */
internal fun handleWebDownload(
    context: Context,
    resources: Resources,
    downloader: ChatWebDownloader,
    request: ChatWebDownloadRequest,
    isForwarded: Boolean,
    onNotice: (String, FeedbackStyle) -> Unit,
): ChatWebDownloadResult {
    val result = downloader.download(context, request, isForwarded)
    val (notice, style) = when (result) {
        is ChatWebDownloadResult.Started ->
            resources.getString(R.string.chat_web_download_started, result.fileName) to FeedbackStyle.Success
        is ChatWebDownloadResult.OpenedInBrowser ->
            resources.getString(R.string.chat_web_download_opened_browser) to FeedbackStyle.Info
        ChatWebDownloadResult.LoopbackUnsupported ->
            resources.getString(R.string.chat_web_download_loopback_unsupported) to FeedbackStyle.Error
        ChatWebDownloadResult.Refused ->
            resources.getString(R.string.chat_web_cannot_open) to FeedbackStyle.Error
        is ChatWebDownloadResult.Failed ->
            resources.getString(R.string.chat_web_download_failed) to FeedbackStyle.Error
    }
    onNotice(notice, style)
    return result
}

/** True when [url] targets this device's own loopback interface (e.g. a forwarded machine port). */
internal fun isLoopbackWebUrl(url: String): Boolean {
    val rawHost = runCatching { URI(url.trim()).host }.getOrNull()?.lowercase()?.trim('[', ']') ?: return false
    return rawHost in loopbackHosts ||
        rawHost.endsWith(".localhost") ||
        rawHost == "0:0:0:0:0:0:0:1" ||
        rawHost.startsWith("::ffff:127.") ||
        isIpv4Loopback(rawHost)
}

/**
 * Resolves a safe public download filename, preferring RFC 5987 `filename*=` when present, then falling back
 * to [URLUtil.guessFileName], and stripping any path traversal or illegal filesystem characters.
 */
internal fun resolveDownloadFileName(url: String, contentDisposition: String, mimeType: String): String {
    val baseMime = mimeType.substringBefore(';').trim()
    val rfc5987 = parseRfc5987FileName(contentDisposition)?.let(::sanitizeDownloadFileName)
    if (rfc5987 != null) return ensureApkExtension(rfc5987, baseMime)
    val guessed = runCatching {
        URLUtil.guessFileName(url, contentDisposition.takeIf { it.isNotBlank() }, baseMime.takeIf { it.isNotEmpty() })
    }.getOrNull()?.let(::sanitizeDownloadFileName)
    if (guessed != null) {
        val adjusted = if (
            baseMime.equals(APK_MIME_TYPE, ignoreCase = true) &&
            guessed.endsWith(".bin", ignoreCase = true) &&
            !url.substringBefore("?").substringBefore("#").endsWith(".bin", ignoreCase = true) &&
            !contentDisposition.contains(".bin", ignoreCase = true)
        ) {
            guessed.dropLast(4) + ".apk"
        } else {
            guessed
        }
        return ensureApkExtension(adjusted, baseMime)
    }
    return if (baseMime.equals(APK_MIME_TYPE, ignoreCase = true)) "download.apk" else "download"
}

/** Validates and normalizes a `Referer` header, stripping fragments and blocking loopback or HTTPS->HTTP leaks. */
internal fun safeRefererHeader(referer: String?, downloadUrl: String): String? {
    val raw = sanitizeHeaderValue(referer) ?: return null
    val safeReferer = ChatLinks.webUrl(raw) ?: return null
    if (isLoopbackWebUrl(safeReferer)) return null
    val refererScheme = runCatching { URI(safeReferer).scheme?.lowercase() }.getOrNull() ?: return null
    val downloadScheme = runCatching { URI(downloadUrl).scheme?.lowercase() }.getOrNull() ?: return null
    if (refererScheme == "https" && downloadScheme == "http") return null
    return safeReferer.substringBefore('#').trim().takeIf { it.isNotEmpty() }
}

private fun sanitizeHeaderValue(value: String?): String? {
    val cleaned = value?.replace(HEADER_CONTROL_CHARS, "")?.trim()
    return cleaned?.takeIf { it.isNotEmpty() }
}

private fun sanitizeDownloadFileName(raw: String): String? {
    val leaf = raw.replace('\\', '/').substringAfterLast('/')
    val cleaned = buildString(leaf.length) {
        for (ch in leaf) {
            when {
                ch.code < 0x20 || ch.code == 0x7F -> Unit
                ch in ILLEGAL_FILENAME_CHARS -> append('_')
                else -> append(ch)
            }
        }
    }.trim().trimStart('.').trim()
    if (cleaned.isEmpty() || cleaned == "." || cleaned == "..") return null
    if (cleaned.length <= MAX_FILENAME_LENGTH) return cleaned
    val dot = cleaned.lastIndexOf('.')
    return if (dot > 0 && cleaned.length - dot <= 16) {
        val ext = cleaned.substring(dot)
        cleaned.substring(0, MAX_FILENAME_LENGTH - ext.length) + ext
    } else {
        cleaned.substring(0, MAX_FILENAME_LENGTH)
    }
}

private fun ensureApkExtension(fileName: String, baseMime: String): String {
    if (!baseMime.equals(APK_MIME_TYPE, ignoreCase = true)) return fileName
    if (fileName.contains('.')) return fileName
    return "$fileName.apk"
}

private fun parseRfc5987FileName(contentDisposition: String): String? {
    if (contentDisposition.isBlank()) return null
    for (part in contentDisposition.split(';')) {
        val trimmed = part.trim()
        val eq = trimmed.indexOf('=')
        if (eq <= 0) continue
        val key = trimmed.substring(0, eq).trim()
        if (!key.equals("filename*", ignoreCase = true)) continue
        var value = trimmed.substring(eq + 1).trim()
        if (value.length >= 2 && value.first() == '"' && value.last() == '"') {
            value = value.substring(1, value.length - 1)
        }
        val firstQuote = value.indexOf('\'')
        val secondQuote = if (firstQuote >= 0) value.indexOf('\'', firstQuote + 1) else -1
        if (firstQuote < 0 || secondQuote < 0) continue
        val charsetName = value.substring(0, firstQuote).trim()
        val charset = when (charsetName.lowercase()) {
            "", "utf-8", "utf8" -> StandardCharsets.UTF_8
            "iso-8859-1", "latin1" -> StandardCharsets.ISO_8859_1
            "us-ascii", "ascii" -> StandardCharsets.US_ASCII
            else -> runCatching { Charset.forName(charsetName) }.getOrDefault(StandardCharsets.UTF_8)
        }
        val decoded = decodePercentBytes(value.substring(secondQuote + 1), charset)
        if (decoded.isNotBlank()) return decoded
    }
    return null
}

private fun decodePercentBytes(value: String, charset: Charset): String {
    val out = ByteArrayOutputStream(value.length)
    var i = 0
    while (i < value.length) {
        val ch = value[i]
        if (ch == '%' && i + 2 < value.length) {
            val hi = Character.digit(value[i + 1], 16)
            val lo = Character.digit(value[i + 2], 16)
            if (hi >= 0 && lo >= 0) {
                out.write((hi shl 4) or lo)
                i += 3
                continue
            }
        }
        out.write(ch.toString().toByteArray(charset))
        i++
    }
    return String(out.toByteArray(), charset)
}

private fun isIpv4Loopback(host: String): Boolean {
    val parts = host.split('.')
    if (parts.size != 4) return false
    val octets = parts.map { it.toIntOrNull() ?: return false }
    return octets[0] == 127 && octets.all { it in 0..255 }
}

private fun platformCookieForUrl(url: String): String? =
    runCatching { CookieManager.getInstance().getCookie(url) }.getOrNull()

private fun platformEnqueueDownload(context: Context, prepared: PreparedWebDownload): Long? {
    val manager = context.getSystemService(DownloadManager::class.java) ?: return null
    val uri = Uri.parse(prepared.url)
    val dmRequest = DownloadManager.Request(uri).apply {
        setTitle(prepared.fileName)
        setDescription(uri.host?.takeIf { it.isNotBlank() } ?: prepared.url)
        prepared.mimeType?.let { setMimeType(it) }
        prepared.cookie?.let { addRequestHeader("Cookie", it) }
        prepared.userAgent?.let { addRequestHeader("User-Agent", it) }
        prepared.referer?.let { addRequestHeader("Referer", it) }
        setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, prepared.fileName)
    }
    return manager.enqueue(dmRequest)
}

/** The hosts that mean this phone itself (the same set as `PortLinks`, which also needs a port). */
private val loopbackHosts = setOf("localhost", "127.0.0.1", "0.0.0.0", "::1", "::", "[::1]", "[::]")
private val HEADER_CONTROL_CHARS = Regex("[\r\n\u0000]")
private const val ILLEGAL_FILENAME_CHARS = ":*?\"<>|/\\"
private const val MAX_FILENAME_LENGTH = 120
private const val APK_MIME_TYPE = "application/vnd.android.package-archive"
