package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.Status
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import persona.core.navigation.ExtensionRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import java.time.Instant
import java.time.ZoneOffset

private val app = HubChannel(id = "app", name = "Persona", kind = "app", source = "builtin", installed = true, enabled = true,
    state = "connected", bind = "none", readOnly = true, surfaces = listOf("iPhone", "Android", "网页"),
    capabilities = ChannelCapabilities(imagesIn = true, filesIn = true, voiceIn = true, format = "markdown", asks = "buttons", typing = true, progress = true))
private val wechat = HubChannel(id = "wechat", name = "微信", kind = "im", source = "plugin", extension = "wechat", installed = true, enabled = true,
    state = "connected", account = ChannelAccount(name = "微信用户 a1b2c3"), bind = "qr", description = "微信里和 Persona 说话",
    capabilities = ChannelCapabilities(imagesIn = true, format = "plain", maxText = 3500, asks = "text", push = true, typing = true),
    target = ChannelTarget.Main,
    features = listOf(ChannelFeature("asks", "提问和批准", on = true), ChannelFeature("pushes", "推送也发到这里", on = false)))
private val feishu = HubChannel(id = "feishu", name = "飞书机器人", kind = "im", source = "builtin", installed = true, enabled = true,
    state = "not_connected", error = "还没配对", bind = "fields", description = "私聊飞书机器人")
private val broken = HubChannel(id = "examplechat", name = "Example Chat", kind = "im", source = "plugin", installed = true, enabled = true,
    state = "error", error = "连不上 Example Chat", account = ChannelAccount(name = "me"), bind = "qr")
private val market = HubChannel(id = "slackish", name = "Slackish", kind = "im", source = "market", extension = "slackish", market = "m1",
    state = "not_installed", bind = "none", description = "在 Slackish 里和 Persona 说话")
private val larkAgent = HubChannel(id = "lark_agent", name = "飞书 /agent", kind = "mention", source = "builtin", installed = true,
    enabled = false, state = "disabled", bind = "config", readOnly = true)

private class FakeChannels(var detail: ChannelDetail) : ChannelManagement {
    override val available = flowOf(true)
    var list = listOf(app, wechat, feishu, broken, market, larkAgent)
    val changes = mutableListOf<ChannelChange>()
    val steps = mutableListOf<Pair<String?, Map<String, String>?>>()
    var prompts = ArrayDeque<BindPrompt>()
    var fail: Exception? = null
    var tests = 0
    var unbinds = 0

    override suspend fun channels(): List<HubChannel> { fail?.let { throw it }; return list }
    override suspend fun channel(id: String): ChannelDetail { fail?.let { throw it }; return detail }
    override suspend fun update(id: String, change: ChannelChange): ChannelDetail {
        changes += change
        fail?.let { throw it }
        var c = detail.channel
        change.enabled?.let { c = c.copy(enabled = it, state = if (it) "connected" else "disabled") }
        change.target?.let { c = c.copy(target = it) }
        c = c.copy(features = c.features.map { f -> change.features[f.id]?.let { f.copy(on = it) } ?: f })
        detail = detail.copy(channel = c)
        return detail
    }
    override suspend fun bind(id: String, step: String?, values: Map<String, String>?): BindPrompt {
        steps += step to values
        fail?.let { throw it }
        val next = prompts.removeFirst()
        if (next.done) detail = detail.copy(channel = detail.channel.copy(state = "connected", account = ChannelAccount(name = next.account.orEmpty())))
        return next
    }
    override suspend fun unbind(id: String): ChannelDetail {
        unbinds++
        fail?.let { throw it }
        detail = detail.copy(channel = detail.channel.copy(state = "not_connected", account = null))
        return detail
    }
    override suspend fun test(id: String) { tests++; fail?.let { throw it } }
}

// Robolectric: the words come from the app's resources, in Simplified Chinese (robolectric.properties).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChannelsStateTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun statesWordsAndMarks() {
        assertEquals(listOf("已连接", "未连接", "出错", "可以安装", "已停用"), listOf(wechat, feishu, broken, market, larkAgent).map { ChannelText.state(res, it) })
        assertEquals(Status.Unread, ChannelText.status(wechat))
        assertEquals(Status.Idle, ChannelText.status(feishu))
        assertEquals(Status.Offline, ChannelText.status(larkAgent))
        assertTrue(ChannelText.isProblem(broken))
        assertFalse(ChannelText.isProblem(feishu))
    }

    @Test fun eachRowsSecondLine() {
        assertEquals("iPhone · Android · 网页", ChannelText.subtitle(app))
        assertEquals("微信用户 a1b2c3", ChannelText.subtitle(wechat))
        // An error says what went wrong, before the account.
        assertEquals("连不上 Example Chat", ChannelText.subtitle(broken))
        assertEquals("私聊飞书机器人", ChannelText.subtitle(feishu))
        assertEquals("在 Slackish 里和 Persona 说话", ChannelText.subtitle(market))
        assertNull(ChannelText.subtitle(larkAgent))
    }

    @Test fun theListHasThreeSectionsAndLeavesEmptyOnesOut() {
        val sections = channelSections(listOf(app, wechat, feishu, broken, market, larkAgent))
        assertEquals(listOf("Persona", "已接入", "可以安装"), sections.map { res.getString(it.title) })
        assertEquals(listOf("app"), sections[0].channels.map { it.id })
        assertEquals(listOf("wechat", "feishu", "examplechat", "lark_agent"), sections[1].channels.map { it.id })
        assertEquals(listOf("slackish"), sections[2].channels.map { it.id })
        assertEquals(listOf("Persona", "已接入"), channelSections(listOf(app, wechat)).map { res.getString(it.title) })
    }

    @Test fun theSettingsRowCountsTheConnectedOnesAndTheBroken() {
        assertNull(ChannelText.listSummary(res, null))
        // The apps are always connected: not counted.
        assertEquals("1 个已连接", ChannelText.listSummary(res, listOf(app, wechat, feishu)))
        assertEquals("1 个已连接 · 1 个出错", ChannelText.listSummary(res, listOf(app, wechat, broken)))
        assertEquals("0 个已连接", ChannelText.listSummary(res, listOf(app)))
        assertTrue(ChannelText.hasProblem(listOf(broken)))
        assertFalse(ChannelText.hasProblem(null))
    }

    @Test fun whatThePageOffers() {
        assertFalse(ChannelText.editable(app))
        assertFalse(ChannelText.editable(larkAgent))
        assertFalse(ChannelText.editable(market))
        assertTrue(ChannelText.editable(wechat))
        assertTrue(ChannelText.binds(feishu))
        assertFalse(ChannelText.binds(app))
        assertFalse(ChannelText.binds(larkAgent)) // hub.json's
        assertTrue(ChannelText.canBind(feishu))
        assertFalse(ChannelText.canBind(wechat)) // bound
        assertFalse(ChannelText.canBind(feishu.copy(enabled = false)))
        assertTrue(ChannelText.isBound(broken)) // bound, but its connection went wrong
        assertEquals("扫码绑定", ChannelText.bindTitle(res, wechat))
        assertEquals("绑定", ChannelText.bindTitle(res, feishu))
        assertEquals("配对", ChannelText.bindTitle(res, feishu.copy(bind = "code")))
    }

    @Test fun moreSettingsOpensOnlyPagesAndroidHas() {
        assertEquals(ExtensionRoute("wechat"), ChannelText.settingsDestination("settings/plugins/wechat"))
        assertEquals(SettingsPageRoute(SettingsPage.ModelConfig), ChannelText.settingsDestination("settings/model-config"))
        // The Feishu bot's own page is iOS's and the web's: no row.
        assertEquals(SettingsPageRoute(SettingsPage.FeishuBot), ChannelText.settingsDestination("settings/connectors/feishu-bot"))
        assertNull(ChannelText.settingsDestination("settings/plugins/../x"))
        assertNull(ChannelText.settingsDestination(null))
        assertNull(ChannelText.settingsDestination("https://example.com"))
    }

    @Test fun targetsCapabilitiesAndTheHeader() {
        assertEquals("主对话", ChannelText.targetText(res, null))
        assertEquals("主对话", ChannelText.targetText(res, ChannelTarget.Main))
        assertEquals("新对话（下一条消息开）", ChannelText.targetText(res, ChannelTarget.New))
        assertEquals("周报", ChannelText.targetText(res, ChannelTarget("conversation", "m", "s", "周报")))
        assertEquals("一段对话", ChannelText.targetText(res, ChannelTarget.conversation("m", "s")))
        assertEquals("Agent · 修 bug", ChannelText.targetText(res, ChannelTarget("agent", "m", "s", "修 bug")))
        assertEquals(listOf("收文字、图片", "回答是纯文字", "一条最多 3500 字，长的拆开发", "提问和批准回复序号或文字", "处理时显示「正在输入」"),
            ChannelText.capabilities(res, wechat.capabilities))
        assertEquals(listOf("收文字", "回答带格式（Markdown）"), ChannelText.capabilities(res, ChannelCapabilities()))
        val now = Instant.parse("2026-11-04T10:30:00Z")
        assertEquals("微信用户 a1b2c3 · 最近一条 30 分钟前",
            ChannelText.headerLine(res, wechat.copy(lastEventAt = "2026-11-04T10:00:00Z"), now, ZoneOffset.UTC))
        // Nothing to say about the account or the time: no second line (the description is under the header).
        assertEquals("", ChannelText.headerLine(res, app.copy(surfaces = emptyList(), description = "各端"), now, ZoneOffset.UTC))
        assertEquals("刚刚", ChannelText.ago(res, now, now))
        assertEquals("3 小时前", ChannelText.ago(res, Instant.parse("2026-11-04T07:00:00Z"), now))
        assertEquals("11月2日", ChannelText.ago(res, Instant.parse("2026-11-02T07:00:00Z"), now, ZoneOffset.UTC))
        assertEquals("1 个回答 · 2 个提问", ChannelText.waiting(res, ChannelDetail(wechat, ChannelWaiting(1, 2))))
        assertNull(ChannelText.waiting(res, ChannelDetail(wechat)))
    }

    @Test fun messagesGoToChoices() {
        val chosen = wechat.copy(target = ChannelTarget("conversation", "hub", "s2", "周报"))
        val detail = ChannelDetail(chosen, conversations = listOf(
            ChannelConversation("hub", "s2", "周报"), ChannelConversation("hub", "s1", null)))
        val assistant = listOf(
            Session(machineId = "hub", sessionId = "s3", title = "旅行", assistant = true, updatedAt = "2026-11-04T09:00:00Z"),
            Session(machineId = "hub", sessionId = "s1", title = "日程", assistant = true, updatedAt = "2026-11-04T08:00:00Z"),
            Session(machineId = "mac", sessionId = "agent", title = "不是助手的", assistant = false),
        )
        val options = targetOptions(detail, assistant)
        assertEquals(listOf("主对话", "新对话（下一条消息开）", "周报", "日程", "旅行"), options.map { it.label ?: ChannelText.targetText(res, it.target) })
        assertEquals(listOf(false, false, true, false, false), options.map { it.selected })
        assertEquals(ChannelTarget.conversation("hub", "s1"), options[3].target)
        // Main is chosen when the hub names no target; the list is cut at the limit after the two fixed choices.
        val plain = targetOptions(ChannelDetail(wechat.copy(target = null)), assistant, limit = 1)
        assertTrue(plain[0].selected)
        assertEquals(3, plain.size)
    }

    @Test fun theListLoadsAndSaysWhyItCannot() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat))
        val model = ChannelListModel(api)
        model.load()
        assertEquals(6, model.state.value.channels?.size)
        api.fail = ChannelsUnsupported()
        model.load()
        assertEquals("读取失败：这台 hub 还不支持渠道，请先更新 hub", model.state.value.error?.resolve(res))
        // What was read stays on screen.
        assertEquals(6, model.state.value.channels?.size)
    }

    @Test fun switchesFeaturesAndTargetSendOnlyWhatChanged() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat))
        val model = ChannelModel(api, "wechat")
        model.load()
        assertTrue(model.setEnabled(false))
        assertEquals("disabled", model.state.value.channel?.state)
        assertTrue(model.setFeature("pushes", true))
        assertTrue(model.state.value.channel!!.features.first { it.id == "pushes" }.on)
        assertTrue(model.setTarget(ChannelTarget("conversation", "hub", "s1", "周报")))
        assertEquals(listOf(ChannelChange(enabled = false), ChannelChange(features = mapOf("pushes" to true)),
            ChannelChange(target = ChannelTarget.conversation("hub", "s1"))), api.changes)
        // Picking what is already chosen sends nothing.
        assertTrue(model.setTarget(ChannelTarget.conversation("hub", "s1")))
        assertEquals(3, api.changes.size)
        assertNull(model.state.value.busy)
    }

    @Test fun aRefusedChangeSaysWhyAndKeepsThePage() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat))
        val model = ChannelModel(api, "wechat")
        model.load()
        api.fail = ChannelRejected("只能选助手的对话")
        assertFalse(model.setTarget(ChannelTarget.conversation("mac", "agent")))
        assertEquals("没有改成：只能选助手的对话", model.state.value.error?.resolve(res))
        assertEquals(ChannelTarget.Main, model.state.value.channel?.target)
        api.fail = HubFailure(HubError.Server, "微信 还没连上", 502)
        assertFalse(model.test())
        assertEquals("没有发出：微信 还没连上", model.state.value.error?.resolve(res))
        api.fail = null
        assertTrue(model.test())
        assertNull(model.state.value.error?.resolve(res))
        assertEquals(2, api.tests)
    }

    @Test fun aPageThatCannotBeReadSaysSo() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat)).apply { fail = ChannelNotFound() }
        val model = ChannelModel(api, "gone")
        model.load()
        assertNull(model.state.value.detail)
        assertEquals("读取失败：这台 hub 上没有这个渠道了", model.state.value.error?.resolve(res))
    }

    @Test fun bindsWithFieldsThenAPairingCodeUntilDone() = runBlocking {
        val api = FakeChannels(ChannelDetail(feishu))
        val model = ChannelModel(api, "feishu")
        model.load()
        api.prompts += BindPrompt(step = "credentials", fields = listOf(BindField("app_id", "App ID"), BindField("app_secret", "App Secret", secret = true)), hint = "先建应用")
        assertTrue(model.startBind())
        assertEquals(null to null, api.steps.single())
        assertFalse(model.state.value.bind.canSubmit) // nothing typed
        model.setValue("app_id", " cli_x ")
        model.setValue("app_secret", "")
        assertTrue(model.state.value.bind.canSubmit)

        // A mistake comes back as the same step with the hub's word: what was typed stays.
        api.prompts += BindPrompt(step = "credentials", fields = model.state.value.bind.prompt!!.fields, error = "App Secret 不对")
        assertTrue(model.submit())
        assertEquals("credentials" to mapOf("app_id" to "cli_x"), api.steps.last())
        assertEquals("App Secret 不对", model.state.value.bind.prompt?.error)
        assertEquals(" cli_x ", model.state.value.bind.values["app_id"])

        // The next step: a code to send to the bot, asked again every interval until done.
        model.setValue("app_secret", "s")
        api.prompts += BindPrompt(step = "pair", kind = "pair_code", userCode = "123456", interval = 3)
        assertTrue(model.submit())
        val bind = model.state.value.bind
        assertTrue(bind.polled)
        assertTrue(bind.values.isEmpty()) // a new step starts with nothing typed
        assertFalse(bind.canSubmit)
        assertEquals(3000L, bind.pollMillis)

        api.prompts += BindPrompt(step = "pair", kind = "pair_code", userCode = "123456", interval = 3)
        api.prompts += BindPrompt(done = true, account = "陈")
        val waits = mutableListOf<Long>()
        model.poll { waits += it }
        assertEquals(listOf(3000L, 3000L), waits)
        assertEquals(listOf("pair" to null, "pair" to null), api.steps.takeLast(2))
        assertFalse(model.state.value.bind.active)
        assertEquals("陈", model.state.value.boundAs)
        // Done reads the page again: bound now.
        assertEquals("connected", model.state.value.channel?.state)
        model.boundShown()
        assertNull(model.state.value.boundAs)
    }

    @Test fun aQrCodeIsAskedAgainThroughFailuresAndStopsWhenCancelled() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat.copy(state = "not_connected", account = null)))
        val model = ChannelModel(api, "wechat")
        model.load()
        api.prompts += BindPrompt(step = "scan", kind = "qr", interval = 0, qr = BindQrCode(2, listOf("10", "01")))
        assertTrue(model.startBind())
        assertEquals(1000L, model.state.value.bind.pollMillis) // at least a second
        var asked = 0
        model.poll {
            asked++
            when (asked) {
                1 -> api.fail = HubFailure(HubError.Timeout) // a failed ask is tried again, quietly
                2 -> { api.fail = null; api.prompts += BindPrompt(step = "scan", kind = "qr", interval = 2, qr = BindQrCode(2, listOf("11", "00"))) }
                else -> model.cancelBind()
            }
        }
        assertEquals(3, asked)
        assertNull(model.state.value.bind.error?.resolve(res))
        assertFalse(model.state.value.bind.active)
        assertEquals(listOf<Pair<String?, Map<String, String>?>>(null to null, "scan" to null, "scan" to null), api.steps)
    }

    /** Back in front after the page was away (#4133): the QR code on screen is asked once at once. */
    @Test fun backInFrontTheQrCodeIsAskedOnceAtOnce() = runBlocking {
        val api = FakeChannels(ChannelDetail(wechat.copy(state = "not_connected", account = null)))
        val model = ChannelModel(api, "wechat")
        model.load()
        assertFalse(model.catchUp()) // nothing on screen: nothing asked
        assertTrue(api.steps.isEmpty())
        api.prompts += BindPrompt(step = "scan", kind = "qr", interval = 2, qr = BindQrCode(2, listOf("10", "01")))
        assertTrue(model.startBind())

        // Scanned with WeChat while the page was away: one ask, no values, and the binding shows as done.
        api.prompts += BindPrompt(done = true, account = "微信用户 a1b2c3")
        assertTrue(model.catchUp())
        assertEquals(listOf<Pair<String?, Map<String, String>?>>(null to null, "scan" to null), api.steps)
        assertFalse(model.state.value.bind.active)
        assertEquals("微信用户 a1b2c3", model.state.value.boundAs)
        assertEquals("connected", model.state.value.channel?.state)
        assertFalse(model.catchUp()) // done: nothing more to ask
        assertEquals(2, api.steps.size)
    }

    @Test fun aFailedFirstStepSaysWhyAndUnbindingClearsTheBinding() = runBlocking {
        val api = FakeChannels(ChannelDetail(feishu))
        val model = ChannelModel(api, "feishu")
        model.load()
        api.fail = HubFailure(HubError.Server, "飞书 没在运行：先打开它", 502)
        assertFalse(model.startBind())
        assertEquals("没有绑定上：飞书 没在运行：先打开它", model.state.value.bind.error?.resolve(res))
        assertFalse(model.state.value.bind.sending)

        api.fail = null
        api.detail = ChannelDetail(wechat)
        model.load()
        assertTrue(model.unbind())
        assertEquals(1, api.unbinds)
        assertEquals("not_connected", model.state.value.channel?.state)
        assertFalse(model.state.value.bind.active)
    }
}
