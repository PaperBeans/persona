package persona.feature.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.time.Instant

/** A hub's daily sync in memory: what the page asked for, in order, and what it answers. */
internal class FakeLarkSync(var view: LarkSyncView?) : LarkSyncSettings {
    val calls = mutableListOf<String>()
    /** Asked again with nothing queued: still waiting for the owner. */
    private val Waiting = BindPrompt(kind = "qr", qr = BindQrCode(2, listOf("10", "01")), interval = 3)
    val registerAnswers = ArrayDeque<BindPrompt>()
    val pollAnswers = ArrayDeque<BindPrompt>()
    var authorizeAnswer = LarkSyncAuthorize(mode = "browser", url = "https://open.feishu.cn/authorize?state=s1")
    var redirect: String? = "https://hub.example.com/lark-oauth"
    var refuse: String? = null
    private fun refused() {
        val reason = refuse
        if (reason != null) throw HubFailure(HubError.Server, reason, status = 400)
    }

    override suspend fun view(): LarkSyncView? { calls += "view"; refused(); return view }
    override suspend fun setSchedule(hour: Int): LarkSyncView? {
        calls += "schedule $hour"; refused(); view = view?.copy(scheduleHour = hour); return view
    }
    override suspend fun createBot(label: String, tier: String, region: String): LarkSyncCreated {
        calls += "create $label $tier $region"; refused()
        return LarkSyncCreated("b2", BindPrompt(kind = "qr", qr = BindQrCode(2, listOf("10", "01")), interval = 3))
    }
    override suspend fun register(botId: String, step: String): BindPrompt { calls += "register $botId"; return registerAnswers.removeFirstOrNull() ?: Waiting }
    override suspend fun addScopes(botId: String, tier: String): BindPrompt { calls += "scopes $botId $tier"; return BindPrompt(done = true) }
    override suspend fun removeBot(botId: String): LarkSyncView? { calls += "remove bot $botId"; return view }
    override suspend fun authorize(botId: String, redirectUri: String, tier: String?): LarkSyncAuthorize {
        calls += "authorize $botId $redirectUri $tier"; refused(); return authorizeAnswer
    }
    override suspend fun pollAuthorize(botId: String): BindPrompt { calls += "poll $botId"; return pollAnswers.removeFirstOrNull() ?: Waiting }
    override suspend fun finishOAuth(state: String, code: String): LarkSyncView? { calls += "oauth $state $code"; refused(); return view }
    override suspend fun setBindingEnabled(key: String, enabled: Boolean): LarkSyncView? {
        calls += "enable $key $enabled"; refused()
        view = view?.let { v -> v.copy(bindings = v.bindings.map { if (it.key == key) it.copy(enabled = enabled) else it }) }
        return view
    }
    override suspend fun removeBinding(key: String): LarkSyncView? { calls += "remove $key"; return view }
    override suspend fun runBinding(key: String): LarkSyncView? { calls += "run $key"; refused(); return view }
    override suspend fun oauthRedirectUri(): String? = redirect
}

internal object LarkSyncFixtures {
    val ok = LarkSyncBinding(key = "primary:a1", botId = "primary", accountId = "a1", enabled = true, health = "ok", reauthDueDays = 300,
        lastRunAt = "2026-11-24T03:12:00Z", nextRunAt = "2026-11-25T03:12:00Z", lastMessages = 12, lastDocs = 1)
    val view = LarkSyncView(available = true, scheduleHour = 3,
        accounts = listOf(LarkSyncAccount("a1", label = "Work"), LarkSyncAccount("a2")),
        bots = listOf(LarkSyncBot("b1", label = "Second company", tier = LarkSync.TierDocs, hasSecret = true),
            LarkSyncBot("primary", appId = "cli_a1", domain = "https://open.feishu.cn", tier = LarkSync.TierFull, primary = true, hasSecret = true)),
        bindings = listOf(ok, LarkSyncBinding(key = "b1:a2", botId = "b1", accountId = "a2", health = "needs_reauth")))
}

/** Settings › Lark bot › daily sync (web `lark-sync.test.ts`, `LarkSyncSection.test.tsx`): its words, and the model's calls. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh")
class LarkSyncModelTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    // Steps that go on after their poll's effect ends run at once here.
    private val now = CoroutineScope(Dispatchers.Unconfined)

    @Test fun theChatBotComesFirstAndAccountsHaveNames() {
        val v = LarkSyncFixtures.view
        assertEquals(listOf("primary", "b1"), orderedBots(v).map { it.botId })
        assertEquals("Work", bindingName(v, v.bindings[0]))
        assertNull(bindingName(v, v.bindings[1])) // no label, no name: "Account 2"
        assertEquals(2, bindingNumber(v, v.bindings[1]))
        assertEquals(listOf("b1:a2"), bindingsOf(v, "b1").map { it.key })
    }

    @Test fun healthHasTheWebsWordsAndTones() {
        val b = LarkSyncFixtures.ok
        assertEquals(LarkPill(R.string.settings_lark_sync_health_ok, LarkTone.Positive), healthOf(b))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_paused, LarkTone.Neutral), healthOf(b.copy(enabled = false)))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_running, LarkTone.Working), healthOf(b.copy(health = "failing", running = true)))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_needs_reauth, LarkTone.Danger), healthOf(b.copy(health = "needs_reauth")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_awaiting, LarkTone.Caution), healthOf(b.copy(health = "awaiting_approval")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_rate_limited, LarkTone.Caution), healthOf(b.copy(health = "rate_limited")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_scope_missing, LarkTone.Caution), healthOf(b.copy(health = "scope_missing")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_failing, LarkTone.Danger), healthOf(b.copy(health = "failing")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_unauthorized, LarkTone.Neutral), healthOf(b.copy(health = "unauthorized")))
        assertEquals(LarkPill(R.string.settings_lark_sync_health_unknown, LarkTone.Neutral), healthOf(b.copy(health = "newer")))
        assertEquals("已是最新", context.getString(healthOf(b).text))
        assertEquals(LarkPill(R.string.settings_lark_sync_approval_awaiting, LarkTone.Caution), approvalOf(LarkSyncBot(approval = "awaiting_approval")))
        assertNull(approvalOf(LarkSyncBot(approval = "ready")))
    }

    @Test fun reauthorizingIsSuggestedInTheLastTwoWeeks() {
        val b = LarkSyncFixtures.ok
        assertNull(reauthSoon(b))
        assertNull(reauthSoon(b.copy(reauthDueDays = null)))
        assertEquals(14, reauthSoon(b.copy(reauthDueDays = 14)))
        assertEquals(0, reauthSoon(b.copy(reauthDueDays = -2)))
        // One that must be authorized again says so itself, not "ends soon".
        assertNull(reauthSoon(b.copy(reauthDueDays = 3, health = "needs_reauth")))
        assertTrue(needsAuthorize(b.copy(health = "unauthorized")))
        assertTrue(needsAuthorize(b.copy(health = "scope_missing")))
        assertNull(reauthSoon(b.copy(health = "scope_missing", reauthDueDays = 30)))
        assertFalse(needsAuthorize(b))
        assertEquals(LarkSync.TierFull, reauthTier(b.copy(tier = null), LarkSyncBot(tier = LarkSync.TierFull)))
        assertEquals(LarkSync.TierDocs, reauthTier(b.copy(tier = ""), null))
    }

    @Test fun theStatusLineSaysOnlyWhatTheHubKnows() {
        val res = context.resources
        val line = larkStatusLine(res, LarkSyncFixtures.ok, Instant.parse("2026-11-24T06:12:00Z"))
        assertTrue(line, line.startsWith("3 小时前同步过 · 12 条消息 · 1 篇文档 · 下次："))
        assertEquals("还没同步过", larkStatusLine(res, LarkSyncBinding(health = "unauthorized", nextRunAt = "2026-11-25T03:12:00Z")))
    }

    @Test fun theOAuthReturnIsCaughtOnlyAtTheHubsAddress() {
        val redirect = "https://hub.example.com/persona/lark-oauth"
        assertEquals(LarkOAuthReturn("c0de", "s1", ""), larkOAuthReturn("https://hub.example.com/persona/lark-oauth?code=c0de&state=s1", redirect))
        assertEquals(LarkOAuthReturn("", "s1", "access_denied"), larkOAuthReturn("https://HUB.example.com:443/persona/lark-oauth/?error=access_denied&state=s1", redirect))
        assertNull(larkOAuthReturn("https://open.feishu.cn/open-apis/authen/v1/authorize?redirect_uri=x", redirect))
        assertNull(larkOAuthReturn("https://hub.example.com/lark-oauth?code=c", redirect)) // another prefix
        assertNull(larkOAuthReturn("http://hub.example.com/persona/lark-oauth?code=c", redirect)) // another scheme
        assertNull(larkOAuthReturn("https://hub.example.com:8443/persona/lark-oauth?code=c", redirect))
    }

    @Test fun anOlderHubHidesTheSection() = runBlocking {
        val model = LarkSyncModel(FakeLarkSync(null), now)
        model.load()
        assertTrue(model.state.value.loaded)
        assertNull(model.state.value.view)
        assertNull(model.state.value.loadError)
    }

    @Test fun changesGoToTheHubAndItsAnswerIsShown() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view)
        val model = LarkSyncModel(api, now)
        model.load()
        assertTrue(model.setSchedule(6))
        assertEquals(6, model.state.value.view!!.scheduleHour)
        assertTrue(model.setEnabled(LarkSyncFixtures.ok, false))
        assertFalse(model.state.value.view!!.bindings[0].enabled)
        assertTrue(model.run(LarkSyncFixtures.ok))
        assertTrue(model.remove(LarkSyncFixtures.ok))
        assertTrue(model.removeBot(LarkSyncFixtures.view.bots[0]))
        assertEquals(listOf("view", "schedule 6", "enable primary:a1 false", "run primary:a1", "remove primary:a1", "remove bot b1"), api.calls)
        api.refuse = "飞书说不行"
        assertFalse(model.run(LarkSyncFixtures.ok))
        val error = model.state.value.error!!.resolve(context.resources)
        assertTrue(error, error.startsWith("没能开始同步：") && error.contains("飞书说不行"))
        assertEquals("", model.state.value.busy)
    }

    @Test fun addingABotScansThenAuthorizesInTheBrowser() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view)
        val model = LarkSyncModel(api, now)
        model.load()
        model.startAdding()
        model.editChoice(tier = LarkSync.TierFull, label = " Work ", region = "lark")
        model.choose(chatBot = false)
        val register = model.state.value.flow as LarkSyncFlow.Register
        assertEquals("b2", register.bot)
        assertFalse(register.scopes)
        // Still waiting, then confirmed in the app: step 2 starts by itself.
        api.registerAnswers += BindPrompt(kind = "qr", qr = BindQrCode(2, listOf("11", "00")), interval = 3)
        assertFalse(model.registerOnce())
        assertEquals(listOf("11", "00"), (model.state.value.flow as LarkSyncFlow.Register).prompt.qr?.rows)
        api.registerAnswers += BindPrompt(done = true)
        assertTrue(model.registerOnce())
        val authorize = model.state.value.flow as LarkSyncFlow.Authorize
        assertEquals("browser", authorize.answer?.mode)
        assertEquals("https://hub.example.com/lark-oauth", authorize.redirect)
        model.continueInBrowser()
        assertEquals("https://open.feishu.cn/authorize?state=s1", (model.state.value.flow as LarkSyncFlow.Authorize).browser)
        // Pages on the way are left alone; the return is caught and handed to the hub.
        assertNull(model.returnOf("https://open.feishu.cn/login"))
        val back = model.returnOf("https://hub.example.com/lark-oauth?code=c0de&state=s1")!!
        assertTrue(model.finish(back))
        assertNull(model.state.value.flow)
        assertEquals(1, model.state.value.connected)
        assertEquals(listOf("view", "create Work full_with_im lark", "register b2", "register b2",
            "authorize b2 https://hub.example.com/lark-oauth full_with_im", "oauth s1 c0de", "view"), api.calls)
    }

    @Test fun theChatBotGetsThePermissionAndADeclineIsSaid() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view)
        val model = LarkSyncModel(api, now)
        model.load()
        model.startAdding()
        model.choose(chatBot = true)
        assertEquals("scopes primary docs_standard", api.calls[1])
        assertTrue(model.state.value.flow is LarkSyncFlow.Authorize)
        model.continueInBrowser()
        assertFalse(model.finish(model.returnOf("https://hub.example.com/lark-oauth?error=access_denied")!!))
        val flow = model.state.value.flow as LarkSyncFlow.Authorize
        assertNull(flow.browser)
        assertEquals("没有允许 Persona 以你的身份读取。可以回到飞书设置里再试一次。", flow.error!!.resolve(context.resources))
        assertFalse(api.calls.any { it.startsWith("oauth") })
        model.cancel()
        assertNull(model.state.value.flow)
    }

    @Test fun theDeviceFlowPollsUntilDone() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view)
        api.authorizeAnswer = LarkSyncAuthorize(mode = "device", prompt = BindPrompt(kind = "device_code", userCode = "ABCD-1234",
            url = "https://open.feishu.cn/device", interval = 5))
        val model = LarkSyncModel(api, now)
        model.load()
        model.authorize("b1", LarkSync.TierDocs, again = true)
        api.pollAnswers += BindPrompt(interval = 10)
        assertFalse(model.pollDeviceOnce())
        assertEquals(10, (model.state.value.flow as LarkSyncFlow.Authorize).prompt?.interval)
        api.pollAnswers += BindPrompt(done = true)
        assertTrue(model.pollDeviceOnce())
        assertNull(model.state.value.flow)
        assertEquals(1, model.state.value.connected)
        assertEquals(listOf("view", "authorize b1 https://hub.example.com/lark-oauth docs_standard", "poll b1", "poll b1", "view"), api.calls)
    }

    @Test fun aManualStepShowsTheAddressToAdd() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view)
        api.authorizeAnswer = LarkSyncAuthorize(mode = "manual", url = "https://open.feishu.cn/authorize?state=s2",
            redirectUri = "https://hub.example.com/lark-oauth", consoleUrl = "https://open.feishu.cn/app/cli_a1/safe")
        val model = LarkSyncModel(api, now)
        model.load()
        model.authorize("primary", LarkSync.TierDocs, again = false)
        val flow = model.state.value.flow as LarkSyncFlow.Authorize
        assertEquals("manual", flow.answer?.mode)
        assertEquals("https://hub.example.com/lark-oauth", flow.redirect)
        model.continueInBrowser()
        assertEquals("https://open.feishu.cn/authorize?state=s2", (model.state.value.flow as LarkSyncFlow.Authorize).browser)
    }

    @Test fun withoutAnAddressTheAuthorizationSaysWhy() = runBlocking {
        val api = FakeLarkSync(LarkSyncFixtures.view).apply { redirect = null }
        val model = LarkSyncModel(api, now)
        model.authorize("primary", LarkSync.TierDocs, again = false)
        val flow = model.state.value.flow as LarkSyncFlow.Authorize
        assertNotNull(flow.error)
        assertFalse(flow.busy)
        assertFalse(api.calls.any { it.startsWith("authorize") })
    }
}
