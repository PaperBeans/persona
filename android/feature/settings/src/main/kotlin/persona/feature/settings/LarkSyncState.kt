package persona.feature.settings

import android.net.Uri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import persona.core.contracts.api.BindPrompt
import persona.core.contracts.api.LarkSyncAuthorize
import persona.core.contracts.api.LarkSyncBinding
import persona.core.contracts.api.LarkSyncBot
import persona.core.contracts.api.LarkSyncSettings
import persona.core.contracts.api.LarkSyncView
import persona.core.designsystem.UiText
import persona.core.designsystem.failureReasonText
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.time.Instant

// Settings › Lark bot › "Daily sync to your knowledge base" (web lib/lark-sync.ts and LarkSyncSection.tsx): which
// Feishu / Lark accounts Persona reads every day, through which bot's app, and how each is doing; then adding one in
// two steps: (1) an app that may read as the owner, created by scanning or the chat bot's app given that permission,
// (2) the owner allows it, in the in-app browser (the return to /lark-oauth is caught there), with a code on screen,
// or after adding the return address to the app's settings by hand.

internal object LarkSync {
    /** What a bot's app may read as the owner (larksyncwire TierDocs / TierFull). */
    const val TierDocs = "docs_standard"
    const val TierFull = "full_with_im"
    val Tiers = listOf(TierDocs, TierFull)

    /** Re-authorizing is suggested once this many days or fewer are left of the platform's one-year authorization. */
    const val ReauthSoonDays = 14

    /** How often the page looks again while a sync runs, so its counts and state follow. */
    const val RunningPollMillis = 5_000L

    /** The fewest seconds between two asks of a step on screen (the web's useStepPolling floor). */
    const val MinPollSeconds = 3
}

/** A pill's colour: the web's green, red, orange and accent; [Neutral] is no tone. */
internal enum class LarkTone { Neutral, Positive, Danger, Caution, Working }

internal data class LarkPill(val text: Int, val tone: LarkTone)

/** The bots in the order the page lists them: the chat bot first, then the sync bots as the hub sent them. */
internal fun orderedBots(v: LarkSyncView): List<LarkSyncBot> = v.bots.filter { it.primary } + v.bots.filterNot { it.primary }

internal fun bindingsOf(v: LarkSyncView, botId: String): List<LarkSyncBinding> = v.bindings.filter { it.botId == botId }

/** A binding's name: its own label, else its account's, else the account's name; null: the numbered fallback ([bindingNumber]). */
internal fun bindingName(v: LarkSyncView, b: LarkSyncBinding): String? {
    val account = v.accounts.firstOrNull { it.id == b.accountId }
    return b.label?.takeIf { it.isNotBlank() } ?: account?.label?.takeIf { it.isNotBlank() } ?: account?.name?.takeIf { it.isNotBlank() }
}

/** The number of "Account n": the account's place, else the binding's. */
internal fun bindingNumber(v: LarkSyncView, b: LarkSyncBinding): Int {
    val at = v.accounts.indexOfFirst { it.id == b.accountId }
    return maxOf(1, if (at >= 0) at + 1 else v.bindings.indexOf(b) + 1)
}

/** The plain words and the pill tone of a binding's state; a running sync wins over the last run's outcome. */
internal fun healthOf(b: LarkSyncBinding): LarkPill = when {
    b.running -> LarkPill(R.string.settings_lark_sync_health_running, LarkTone.Working)
    else -> when (b.health) {
        "ok" -> if (b.enabled) LarkPill(R.string.settings_lark_sync_health_ok, LarkTone.Positive)
            else LarkPill(R.string.settings_lark_sync_health_paused, LarkTone.Neutral)
        "unauthorized" -> LarkPill(R.string.settings_lark_sync_health_unauthorized, LarkTone.Neutral)
        "needs_reauth" -> LarkPill(R.string.settings_lark_sync_health_needs_reauth, LarkTone.Danger)
        "rate_limited" -> LarkPill(R.string.settings_lark_sync_health_rate_limited, LarkTone.Caution)
        "scope_missing" -> LarkPill(R.string.settings_lark_sync_health_scope_missing, LarkTone.Caution)
        "awaiting_approval" -> LarkPill(R.string.settings_lark_sync_health_awaiting, LarkTone.Caution)
        "failing" -> LarkPill(R.string.settings_lark_sync_health_failing, LarkTone.Danger)
        else -> LarkPill(R.string.settings_lark_sync_health_unknown, LarkTone.Neutral)
    }
}

/** The pill for a bot's app, when it says something: waiting for the admin, or set up without some permissions. */
internal fun approvalOf(b: LarkSyncBot): LarkPill? = when (b.approval) {
    "awaiting_approval" -> LarkPill(R.string.settings_lark_sync_approval_awaiting, LarkTone.Caution)
    "degraded" -> LarkPill(R.string.settings_lark_sync_approval_degraded, LarkTone.Caution)
    else -> null
}

internal fun tierText(tier: String?): Int =
    if (tier == LarkSync.TierFull) R.string.settings_lark_sync_tier_full else R.string.settings_lark_sync_tier_docs

/** The binding has to be authorized (again) before it can sync. */
internal fun needsAuthorize(b: LarkSyncBinding): Boolean = b.health == "needs_reauth" || b.health == "unauthorized" || b.health == "scope_missing"

/** Days left when the authorization ends soon enough to say so; null otherwise. */
internal fun reauthSoon(b: LarkSyncBinding): Int? {
    val days = b.reauthDueDays ?: return null
    if (days > LarkSync.ReauthSoonDays || needsAuthorize(b)) return null
    return maxOf(0, days)
}

/** The tier an account is authorized again for: its own, else its bot's. */
internal fun reauthTier(b: LarkSyncBinding, bot: LarkSyncBot?): String =
    b.tier?.takeIf { it.isNotEmpty() } ?: bot?.tier?.takeIf { it.isNotEmpty() } ?: LarkSync.TierDocs

/** A bot's app exists (the hub keeps its secret); an older answer without the field counts as set. */
internal fun botReady(b: LarkSyncBot): Boolean = b.hasSecret != false

/** An RFC 3339 time from the hub; null when absent or unreadable. */
internal fun larkTime(raw: String?): Instant? =
    raw?.takeIf { it.isNotBlank() }?.let { runCatching { java.time.OffsetDateTime.parse(it).toInstant() }.getOrNull() }

/** The platform's answer on the return page: its `code` and `state`, or its `error` when the owner declined. */
internal data class LarkOAuthReturn(val code: String, val state: String, val error: String)

/**
 * The return to [redirect] the in-app browser is about to load, or null for any other address: same scheme, host,
 * port and path (a trailing slash aside). Its query carries the platform's answer.
 */
internal fun larkOAuthReturn(url: String, redirect: String): LarkOAuthReturn? {
    val target = runCatching { Uri.parse(url) }.getOrNull() ?: return null
    val expected = runCatching { Uri.parse(redirect) }.getOrNull() ?: return null
    fun Uri.portOrDefault() = if (port != -1) port else if (scheme.equals("https", true)) 443 else 80
    if (!target.scheme.equals(expected.scheme, true) || !target.host.equals(expected.host, true) ||
        target.portOrDefault() != expected.portOrDefault() || target.path.orEmpty().trimEnd('/') != expected.path.orEmpty().trimEnd('/')) return null
    return LarkOAuthReturn(target.getQueryParameter("code").orEmpty(), target.getQueryParameter("state").orEmpty(),
        target.getQueryParameter("error").orEmpty())
}

/** Where the add / authorize flow is: choosing, step 1 (an app that may read as the owner), or step 2 (allowing it). */
internal sealed interface LarkSyncFlow {
    data class Choose(
        val tier: String = LarkSync.TierDocs,
        val label: String = "",
        val region: String = "feishu",
        /** "new" or "chat" while that start is under way. */
        val busy: String = "",
        val error: UiText? = null,
    ) : LarkSyncFlow

    data class Register(val bot: String, val tier: String, val prompt: BindPrompt, val scopes: Boolean) : LarkSyncFlow

    data class Authorize(
        val bot: String,
        val tier: String,
        val again: Boolean,
        val answer: LarkSyncAuthorize? = null,
        /** The device flow's QR code or code; its error once the authorization under way failed. */
        val prompt: BindPrompt? = null,
        /** The return address asked with, which the in-app browser watches for. */
        val redirect: String? = null,
        val error: UiText? = null,
        val busy: Boolean = false,
        /** The authorization page open in the in-app browser. */
        val browser: String? = null,
        /** The platform's code is being handed to the hub. */
        val finishing: Boolean = false,
    ) : LarkSyncFlow
}

internal data class LarkSyncUiState(
    /** Null: not read yet, or a hub without the sync (the section hides). */
    val view: LarkSyncView? = null,
    val loaded: Boolean = false,
    val loadError: ResText? = null,
    val error: ResText? = null,
    /** What a change is under way on: a binding's key, `bot:<id>` or `schedule`. */
    val busy: String = "",
    val flow: LarkSyncFlow? = null,
    /** Counts accounts connected here, so the page says so once for each. */
    val connected: Int = 0,
) {
    val running: Boolean get() = view?.bindings?.any { it.running } == true
}

/** The section's state over [api]; the page runs its calls on Main, so the newest request's answer wins. */
internal class LarkSyncModel(
    private val api: LarkSyncSettings,
    /** The page's: where a step that ends inside a poll goes on, after the poll's own effect has ended with it. */
    private val scope: CoroutineScope,
) {
    private val mutable = MutableStateFlow(LarkSyncUiState())
    val state = mutable.asStateFlow()
    // Answers are taken in the order their requests were made: a read that started before a change never undoes it.
    private var issued = 0L
    private var shown = 0L
    // Only the latest authorization request counts: each one prepares a new one and retires the one before.
    private var asked = 0L

    private fun accept(n: Long, v: LarkSyncView?) {
        if (n < shown) return
        shown = n
        mutable.update { it.copy(view = v) }
    }

    suspend fun load() {
        val n = ++issued
        try {
            val v = api.view()
            accept(n, v)
            mutable.update { it.copy(loaded = true, loadError = null) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { it.copy(loaded = true, loadError = ResText.of(R.string.settings_lark_sync_load_failed, failureReasonText(error))) }
        }
    }

    /** A change whose answer is the new view; [what] names it in the error. */
    private suspend fun change(what: Int, key: String, call: suspend () -> LarkSyncView?): Boolean {
        val n = ++issued
        mutable.update { it.copy(busy = key) }
        return try {
            call()?.let { accept(n, it) }
            mutable.update { it.copy(error = null) }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { it.copy(error = ResText.of(R.string.settings_lark_sync_error_with, ResText.of(what), failureReasonText(error))) }
            false
        } finally { mutable.update { it.copy(busy = "") } }
    }

    suspend fun setSchedule(hour: Int) = change(R.string.settings_lark_sync_save_failed, "schedule") { api.setSchedule(hour) }

    suspend fun setEnabled(b: LarkSyncBinding, on: Boolean) =
        change(if (on) R.string.settings_lark_sync_enable_failed else R.string.settings_lark_sync_disable_failed, b.key) { api.setBindingEnabled(b.key, on) }

    suspend fun run(b: LarkSyncBinding) = change(R.string.settings_lark_sync_run_failed, b.key) { api.runBinding(b.key) }

    suspend fun remove(b: LarkSyncBinding) = change(R.string.settings_lark_sync_remove_failed, b.key) { api.removeBinding(b.key) }

    suspend fun removeBot(b: LarkSyncBot) = change(R.string.settings_lark_sync_remove_bot_failed, "bot:${b.botId}") { api.removeBot(b.botId) }

    // ── the flow ──

    fun startAdding() = mutable.update { s ->
        val primary = s.view?.bots?.firstOrNull { it.primary }
        s.copy(flow = LarkSyncFlow.Choose(region = if (primary?.domain?.contains("larksuite") == true) "lark" else "feishu"))
    }

    fun cancel() {
        ++asked
        mutable.update { it.copy(flow = null) }
    }

    fun editChoice(tier: String? = null, label: String? = null, region: String? = null) = mutable.update { s ->
        val f = s.flow as? LarkSyncFlow.Choose ?: return@update s
        s.copy(flow = f.copy(tier = tier ?: f.tier, label = label ?: f.label, region = region ?: f.region))
    }

    /** Step 1: a new app by scanning ([chatBot] false) or the chat bot's app given the tier's permissions. */
    suspend fun choose(chatBot: Boolean) {
        val f = state.value.flow as? LarkSyncFlow.Choose ?: return
        if (f.busy.isNotEmpty()) return
        val primary = state.value.view?.bots?.firstOrNull { it.primary }
        if (chatBot && primary == null) return
        mutable.update { it.copy(flow = f.copy(busy = if (chatBot) "chat" else "new", error = null)) }
        try {
            if (chatBot && primary != null) {
                prompted(primary.botId, f.tier, api.addScopes(primary.botId, f.tier), scopes = true)
            } else {
                val created = api.createBot(f.label.trim(), f.tier, f.region)
                prompted(created.botId, f.tier, created.prompt, scopes = false)
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { s -> (s.flow as? LarkSyncFlow.Choose)?.let { s.copy(flow = it.copy(busy = "", error = failureReasonText(error))) } ?: s }
        }
    }

    private suspend fun prompted(bot: String, tier: String, prompt: BindPrompt, scopes: Boolean) {
        if (prompt.done) authorize(bot, tier, again = false)
        else mutable.update { it.copy(flow = LarkSyncFlow.Register(bot, tier, prompt, scopes)) }
    }

    /** A sync bot whose setup was left half way: its QR step, asked again. */
    suspend fun resume(b: LarkSyncBot) {
        mutable.update { it.copy(busy = "bot:${b.botId}") }
        try {
            val prompt = api.register(b.botId, "register")
            val tier = b.tier?.takeIf { it.isNotEmpty() } ?: LarkSync.TierDocs
            if (prompt.done) { authorize(b.botId, tier, again = false); load() }
            else mutable.update { it.copy(flow = LarkSyncFlow.Register(b.botId, tier, prompt, scopes = false)) }
            mutable.update { it.copy(error = null) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { it.copy(error = ResText.of(R.string.settings_lark_sync_error_with, ResText.of(R.string.settings_lark_sync_resume_failed), failureReasonText(error))) }
        } finally { mutable.update { it.copy(busy = "") } }
    }

    /** Asks the registration on screen once; true once it is over here (done, or the flow moved on). */
    suspend fun registerOnce(): Boolean {
        val f = state.value.flow as? LarkSyncFlow.Register ?: return true
        val p = try { api.register(f.bot, f.prompt.step ?: "register") } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { return false } // a missed poll: the next one asks again
        if (state.value.flow != f) return true
        if (p.done) {
            mutable.update { s -> if (s.flow == f) s.copy(flow = LarkSyncFlow.Authorize(f.bot, f.tier, again = false)) else s }
            scope.launch { ask() }
            return true
        }
        mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(prompt = p)) else s }
        return false
    }

    /** Asks the registration again every interval while its QR code or code is on screen, until it is done or failed. */
    suspend fun pollRegistration() {
        while (true) {
            val f = state.value.flow as? LarkSyncFlow.Register ?: return
            if (!StepView.of(f.prompt).asksAgain || !f.prompt.error.isNullOrBlank()) return
            delay(maxOf(LarkSync.MinPollSeconds, f.prompt.interval) * 1000L)
            if (registerOnce()) return
        }
    }

    /** A failed registration starts again from the choice; a new bot that never got its app is not kept. */
    suspend fun restartRegistration() {
        val f = state.value.flow as? LarkSyncFlow.Register ?: return
        startAdding()
        if (f.scopes) return
        val n = ++issued
        try { accept(n, api.removeBot(f.bot)) } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { } // still listed, with its own Remove
    }

    /** Step 2, or authorizing an account again: the hub prepares it, its answer says which way it goes. */
    suspend fun authorize(bot: String, tier: String, again: Boolean) {
        mutable.update { it.copy(flow = LarkSyncFlow.Authorize(bot, tier, again)) }
        ask()
    }

    private fun patch(n: Long, change: (LarkSyncFlow.Authorize) -> LarkSyncFlow.Authorize) = mutable.update { s ->
        val f = s.flow as? LarkSyncFlow.Authorize
        if (f == null || n != asked) s else s.copy(flow = change(f))
    }

    suspend fun ask() {
        val f = state.value.flow as? LarkSyncFlow.Authorize ?: return
        val n = ++asked
        patch(n) { it.copy(busy = true, error = null) }
        try {
            val redirect = api.oauthRedirectUri() ?: throw HubFailure(HubError.NoNetwork)
            val answer = api.authorize(f.bot, redirect, f.tier)
            patch(n) { it.copy(answer = answer, prompt = answer.prompt, redirect = answer.redirectUri?.takeIf { r -> r.isNotBlank() } ?: redirect, busy = false) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            patch(n) { it.copy(error = failureReasonText(error), busy = false) }
        }
    }

    /** Opens the authorization page in the in-app browser, or asks again when there is none. */
    suspend fun continueInBrowser() {
        val f = state.value.flow as? LarkSyncFlow.Authorize ?: return
        val url = f.answer?.url?.takeIf { StepView.isWebLink(it) }
        if (url == null) { ask(); return }
        mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(browser = url)) else s }
    }

    fun closeBrowser() = mutable.update { s ->
        (s.flow as? LarkSyncFlow.Authorize)?.let { s.copy(flow = it.copy(browser = null)) } ?: s
    }

    /** The platform's answer when the in-app browser is about to load the return address ([url]); null for any other page. */
    fun returnOf(url: String): LarkOAuthReturn? {
        val f = state.value.flow as? LarkSyncFlow.Authorize ?: return null
        return larkOAuthReturn(url, f.redirect ?: return null)
    }

    /** Finishes a browser authorization with the platform's [answer]; true once the account is connected. */
    suspend fun finish(answer: LarkOAuthReturn): Boolean {
        val f = state.value.flow as? LarkSyncFlow.Authorize ?: return false
        if (answer.error.isNotEmpty() || answer.code.isEmpty() || answer.state.isEmpty()) {
            mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(browser = null, error = UiText.of(R.string.settings_lark_sync_return_declined_detail))) else s }
            return false
        }
        mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(browser = null, finishing = true)) else s }
        val n = ++issued
        return try {
            accept(n, api.finishOAuth(answer.state, answer.code))
            mutable.update { s -> if (s.flow is LarkSyncFlow.Authorize) s.copy(flow = null, error = null, connected = s.connected + 1) else s }
            load()
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { s -> (s.flow as? LarkSyncFlow.Authorize)?.let { s.copy(flow = it.copy(finishing = false,
                error = UiText.of(R.string.settings_lark_sync_error_with, UiText.of(R.string.settings_lark_sync_return_failed_title), failureReasonText(error)))) } ?: s }
            false
        }
    }

    /** Asks the device flow once: true once it is over here (done: [LarkSyncUiState.connected] counts it; failed: its error is on screen). */
    suspend fun pollDeviceOnce(): Boolean {
        val f = state.value.flow as? LarkSyncFlow.Authorize ?: return true
        val device = f.prompt ?: return true
        val p = try { api.pollAuthorize(f.bot) } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { return false }
        if (state.value.flow != f) return true
        when {
            p.done -> {
                mutable.update { s -> if (s.flow == f) s.copy(flow = null, error = null, connected = s.connected + 1) else s }
                scope.launch { load() }
                return true
            }
            !p.error.isNullOrBlank() -> { mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(prompt = device.copy(error = p.error))) else s }; return true }
            p.interval > 0 && p.interval != device.interval ->
                mutable.update { s -> if (s.flow == f) s.copy(flow = f.copy(prompt = device.copy(interval = p.interval))) else s }
        }
        return false
    }

    /** The device flow's poll, every interval while its code is on screen. */
    suspend fun pollDevice() {
        while (true) {
            val f = state.value.flow as? LarkSyncFlow.Authorize ?: return
            val device = f.prompt ?: return
            if (f.answer?.mode != "device" || !StepView.of(device).asksAgain || !device.error.isNullOrBlank()) return
            delay(maxOf(LarkSync.MinPollSeconds, device.interval) * 1000L)
            if (pollDeviceOnce()) return
        }
    }
}
