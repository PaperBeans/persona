package persona.feature.settings

import androidx.navigation3.runtime.NavKey
import persona.core.contracts.Session
import persona.core.contracts.api.BindPrompt
import persona.core.contracts.api.ChannelCapabilities
import persona.core.contracts.api.ChannelChange
import persona.core.contracts.api.ChannelDetail
import persona.core.contracts.api.ChannelManagement
import persona.core.contracts.api.ChannelTarget
import persona.core.contracts.api.HubChannel
import persona.core.contracts.parseTime
import persona.core.designsystem.Status
import persona.core.designsystem.failureReasonText
import persona.core.navigation.ExtensionRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import android.content.res.Resources
import androidx.annotation.StringRes
import java.time.Instant
import java.time.ZoneId

// Settings › Extensions › Channels (docs/extensions/channels.md "UI"): the words, the list's sections, where messages go, and the models of the
// list, a channel's page and its binding a step at a time. The pages are in ChannelsScreen.kt.

/** What a channel is called and says in the settings (the same words as the web's lib/channels.ts). */
internal object ChannelText {
    fun state(res: Resources, c: HubChannel): String = res.getString(when (c.state) {
        "connected" -> R.string.settings_src_state_connected
        "error" -> R.string.settings_src_channel_state_error
        "disabled" -> R.string.settings_src_channel_state_disabled
        "not_installed" -> R.string.settings_src_channel_state_installable
        else -> R.string.settings_src_state_not_connected
    })

    /** Broken: drawn in the danger colour with its one-line reason. */
    fun isProblem(c: HubChannel): Boolean = c.state == "error"

    /** The dot beside the state: green connected, grey otherwise (darker grey when it is off). */
    fun status(c: HubChannel): Status = when (c.state) {
        "connected" -> Status.Unread
        "disabled", "not_installed" -> Status.Offline
        else -> Status.Idle
    }

    /** The row's second line: the error, the app's ends, the account, else what it is. */
    fun subtitle(c: HubChannel): String? = when {
        c.state == "error" && !c.error.isNullOrBlank() -> c.error
        c.kind == "app" && c.surfaces.isNotEmpty() -> c.surfaces.joinToString(" · ")
        c.source != "market" && !c.account?.name.isNullOrBlank() -> c.account?.name
        else -> c.description?.takeIf { it.isNotBlank() }
    }

    /** Settings' row: how many are connected (the apps are always), and how many went wrong; null before the list is in. */
    fun listSummary(res: Resources, list: List<HubChannel>?): String? {
        list ?: return null
        val on = list.count { it.kind != "app" && it.state == "connected" }
        val bad = list.count(::isProblem)
        val connected = res.getQuantityString(R.plurals.settings_src_channels_connected, on, on)
        return if (bad > 0) res.getString(R.string.settings_src_channels_summary_with_errors, connected,
            res.getQuantityString(R.plurals.settings_src_channels_errors, bad, bad)) else connected
    }

    fun hasProblem(list: List<HubChannel>?): Boolean = list.orEmpty().any(::isProblem)

    fun targetText(res: Resources, t: ChannelTarget?): String = when (t?.kind) {
        "new" -> res.getString(R.string.settings_src_channel_target_new)
        "conversation" -> t.title?.takeIf { it.isNotBlank() } ?: res.getString(R.string.settings_src_channel_a_chat)
        "agent" -> t.title?.takeIf { it.isNotBlank() }?.let { res.getString(R.string.settings_src_channel_target_agent_named, it) }
            ?: res.getString(R.string.settings_src_channel_target_agent)
        else -> res.getString(R.string.settings_src_channel_target_main)
    }

    /** What the channel can do, in a few words each for its page. */
    fun capabilities(res: Resources, c: ChannelCapabilities): List<String> = buildList {
        // What comes in, in the language's list: "text, images" in English, with the Chinese list separator in Chinese.
        val kinds = listOfNotNull(R.string.settings_src_channel_in_text, R.string.settings_src_channel_in_images.takeIf { c.imagesIn },
            R.string.settings_src_channel_in_files.takeIf { c.filesIn }, R.string.settings_src_channel_in_voice.takeIf { c.voiceIn })
            .map(res::getString).joinToString(res.getString(R.string.settings_src_list_separator))
        add(res.getString(R.string.settings_src_channel_receives, kinds))
        add(res.getString(if (c.format == "plain") R.string.settings_src_channel_plain else R.string.settings_src_channel_markdown))
        if (c.maxText > 0) add(res.getQuantityString(R.plurals.settings_src_channel_max_text, c.maxText, c.maxText))
        when (c.asks) {
            "text" -> add(res.getString(R.string.settings_src_channel_asks_text))
            "buttons" -> add(res.getString(R.string.settings_src_channel_asks_buttons))
        }
        if (c.typing) add(res.getString(R.string.settings_src_channel_typing))
        if (c.progress) add(res.getString(R.string.settings_src_channel_progress))
    }

    fun bindTitle(res: Resources, c: HubChannel): String = res.getString(when (c.bind) {
        "qr" -> R.string.settings_src_channel_bind_qr
        "code" -> R.string.settings_src_channel_pair
        else -> R.string.settings_src_channel_bind
    })

    /** Set up on this page: not hub.json's or an extension page's, not a market's. */
    fun editable(c: HubChannel): Boolean = !c.readOnly && c.source != "market"

    /** It has a binding step of its own (scan, pair, fill in). */
    fun binds(c: HubChannel): Boolean = editable(c) && c.bind !in setOf("none", "config", "")

    fun isBound(c: HubChannel): Boolean = c.state == "connected" || (c.state == "error" && !c.account?.name.isNullOrBlank())

    /** The page offers "Link": it binds here, it is on, and it is not bound. */
    fun canBind(c: HubChannel): Boolean = binds(c) && c.enabled && !isBound(c)

    /**
     * The page the hub links to (`settings_url`), where Android has it: an extension's page (of [manager]'s), "Model configuration". The Feishu bot's own
     * settings page is iOS's and the web's only; without a page here the row is left out.
     */
    fun settingsDestination(url: String?, manager: String? = null): NavKey? {
        val path = url?.trim()?.trim('/') ?: return null
        return when {
            // The extension of the assistant whose channel it is ([manager]; null: the default's), not the default's copy.
            path.startsWith("settings/plugins/") -> path.removePrefix("settings/plugins/").takeIf { it.matches(ExtensionId) }
                ?.let { ExtensionRoute(it, manager = manager?.takeIf(String::isNotBlank)) }
            path == "settings/model-config" -> SettingsPageRoute(SettingsPage.ModelConfig)
            path == "settings/connectors/feishu-bot" -> SettingsPageRoute(SettingsPage.FeishuBot)
            else -> null
        }
    }

    /**
     * The header's line: the account, the time of the last message; empty when it has neither. The state is on the
     * header's right and the description under it, so neither is repeated here.
     */
    fun headerLine(res: Resources, c: HubChannel, now: Instant = Instant.now(), zone: ZoneId = ZoneId.systemDefault()): String =
        listOfNotNull(
            c.account?.name?.takeIf { it.isNotBlank() },
            parseTime(c.lastEventAt)?.let { res.getString(R.string.settings_src_channel_last_message, ago(res, it, now, zone)) },
        ).joinToString(" · ")

    fun ago(res: Resources, at: Instant, now: Instant = Instant.now(), zone: ZoneId = ZoneId.systemDefault()): String = agoText(res, at, now, zone)

    /** Recent's Waiting line: the answers and questions the channel waits on; null when it waits on nothing. */
    fun waiting(res: Resources, d: ChannelDetail): String? = listOfNotNull(
        d.waiting.answers.takeIf { it > 0 }?.let { res.getQuantityString(R.plurals.settings_src_channel_waiting_answers, it, it) },
        d.waiting.asks.takeIf { it > 0 }?.let { res.getQuantityString(R.plurals.settings_src_channel_waiting_asks, it, it) },
    ).joinToString(" · ").ifEmpty { null }

    /** How a failed change reads: what did not happen ([verb]), then the hub's words when it gave any. */
    fun failure(error: Throwable, @StringRes verb: Int = R.string.settings_src_channel_not_changed): ResText = ResText.of(verb, failureReasonText(error))

    private val ExtensionId = Regex("[A-Za-z0-9_-]{1,64}")
}

/** A section of the list: the apps, the installed ones, those a market has; [id] stays the same in every language. */
internal data class ChannelSection(val id: String, @StringRes val title: Int, @StringRes val footer: Int, val channels: List<HubChannel>)

internal fun channelSections(list: List<HubChannel>): List<ChannelSection> = listOf(
    ChannelSection("app", R.string.settings_src_channels_section_app, R.string.settings_src_channels_section_app_footer, list.filter { it.kind == "app" }),
    ChannelSection("installed", R.string.settings_src_channels_section_installed, R.string.settings_src_channels_section_installed_footer,
        list.filter { it.kind != "app" && it.source != "market" }),
    ChannelSection("market", R.string.settings_src_channel_state_installable, R.string.settings_src_channels_section_market_footer,
        list.filter { it.source == "market" }),
).filter { it.channels.isNotEmpty() }

/** One choice of "Send messages to"; [label] null: the target's own words ([ChannelText.targetText]). */
internal data class TargetOption(val target: ChannelTarget, val label: String?, val selected: Boolean)

/**
 * "Send messages to"'s choices: the main conversation, a new one, then the conversations this channel's messages went to
 * lately, then the assistant's other ones (newest first, [limit] in all). [assistant] is in the hub's own ids.
 */
internal fun targetOptions(detail: ChannelDetail, assistant: List<Session>, limit: Int = 20): List<TargetOption> {
    val current = detail.channel.target
    fun isCurrent(machine: String, session: String) =
        current?.kind == "conversation" && current.machineId == machine && current.sessionId == session
    val recent = detail.conversations.filter { it.machineId.isNotBlank() && it.sessionId.isNotBlank() }
        .map { Triple(it.machineId, it.sessionId, it.title?.takeIf(String::isNotBlank)) }
    val others = assistant.filter { it.assistant }.sortedByDescending { it.updated }
        .map { Triple(it.machineId, it.sessionId, it.title.takeIf(String::isNotBlank) ?: it.lastText?.takeIf(String::isNotBlank)?.take(40)) }
    // The current conversation is always a choice, even when it is neither recent nor listed (another machine's).
    val chosen = current?.takeIf { it.kind == "conversation" && !it.machineId.isNullOrBlank() && !it.sessionId.isNullOrBlank() }
        ?.let { listOf(Triple(it.machineId!!, it.sessionId!!, it.title?.takeIf(String::isNotBlank))) }.orEmpty()
    val conversations = (chosen + recent + others)
        .groupBy { it.first to it.second }.values.map { same -> same.first().copy(third = same.firstNotNullOfOrNull { it.third }) }
        .take(limit)
    return buildList {
        add(TargetOption(ChannelTarget.Main, null, current == null || current.kind == "main"))
        add(TargetOption(ChannelTarget.New, null, current?.kind == "new"))
        conversations.forEach { (machine, session, title) ->
            add(TargetOption(ChannelTarget.conversation(machine, session), title, isCurrent(machine, session)))
        }
    }
}

internal data class ChannelListState(
    val channels: List<HubChannel>? = null,
    val loading: Boolean = false,
    val error: ResText? = null,
) {
    val sections: List<ChannelSection> get() = channelSections(channels.orEmpty())
}

/** Settings › Extensions › Channels: the list. */
internal class ChannelListModel(private val api: ChannelManagement) {
    private val mutable = MutableStateFlow(ChannelListState())
    val state = mutable.asStateFlow()

    suspend fun load() {
        if (state.value.loading) return
        mutable.update { it.copy(loading = true) }
        try {
            val list = api.channels()
            mutable.update { it.copy(channels = list, error = null) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ChannelText.failure(error, R.string.settings_src_read_failed)) } }
        finally { mutable.update { it.copy(loading = false) } }
    }
}

/** What the page is doing right now; one change at a time. */
internal enum class ChannelBusy { Enabled, Feature, Target, Unbind, Test }

/** Binding a step at a time: the step on screen, what was typed into it, and the hub's word on it. */
internal data class BindState(
    val prompt: BindPrompt? = null,
    val values: Map<String, String> = emptyMap(),
    val sending: Boolean = false,
    /** Why the last step did not go (the network, the hub); the hub's word on the values is the prompt's own error. */
    val error: ResText? = null,
) {
    val active: Boolean get() = prompt != null
    /** A code or QR code on screen, asked again by itself; never a step this app cannot draw (#4488). */
    val polled: Boolean get() = prompt?.let { !it.done && StepView.of(it).asksAgain } == true
    /** Fields to fill in, at least one filled (the hub says which it still needs). */
    val canSubmit: Boolean get() {
        val p = prompt ?: return false
        return !sending && StepView.of(p) == StepView.Fields && p.fields.any { values[it.name].orEmpty().isNotBlank() }
    }
    /** How long to wait before asking the hub again, at least a second. */
    val pollMillis: Long get() = (prompt?.interval ?: 0).coerceAtLeast(MinPollSeconds) * 1000L

    private companion object { const val MinPollSeconds = 1 }
}

internal data class ChannelState(
    val detail: ChannelDetail? = null,
    val loading: Boolean = false,
    val busy: ChannelBusy? = null,
    val error: ResText? = null,
    val bind: BindState = BindState(),
    /** Binding just finished, as whom (empty when the hub did not say): the page says so once. */
    val boundAs: String? = null,
) {
    val channel: HubChannel? get() = detail?.channel
    val editable: Boolean get() = channel?.let(ChannelText::editable) == true
}

/** Settings › Extensions › Channels › one: its switch, its binding, where its messages go, its features, a test message. */
internal class ChannelModel(private val api: ChannelManagement, private val id: String) {
    private val mutable = MutableStateFlow(ChannelState())
    val state = mutable.asStateFlow()

    suspend fun load() {
        if (state.value.loading) return
        mutable.update { it.copy(loading = true) }
        try {
            val detail = api.channel(id)
            mutable.update { it.copy(detail = detail, error = null) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ChannelText.failure(error, if (it.detail == null) R.string.settings_src_read_failed else R.string.settings_src_channel_not_refreshed)) } }
        finally { mutable.update { it.copy(loading = false) } }
    }

    suspend fun setEnabled(on: Boolean) = change(ChannelBusy.Enabled, ChannelChange(enabled = on))

    suspend fun setFeature(feature: String, on: Boolean) = change(ChannelBusy.Feature, ChannelChange(features = mapOf(feature to on)))

    suspend fun setTarget(target: ChannelTarget): Boolean {
        if (target == state.value.channel?.target?.copy(title = null)) return true
        return change(ChannelBusy.Target, ChannelChange(target = target.copy(title = null)))
    }

    suspend fun unbind(): Boolean = busy(ChannelBusy.Unbind) {
        val detail = api.unbind(id)
        mutable.update { it.copy(detail = detail, bind = BindState(), error = null) }
    }

    /** A line to the bound owner: the caller says it went. */
    suspend fun test(): Boolean = busy(ChannelBusy.Test, verb = R.string.settings_src_channel_not_sent) { api.test(id) }

    private suspend fun change(what: ChannelBusy, change: ChannelChange): Boolean = busy(what) {
        val detail = api.update(id, change)
        mutable.update { it.copy(detail = detail, error = null) }
    }

    private suspend fun busy(what: ChannelBusy, @StringRes verb: Int = R.string.settings_src_channel_not_changed, block: suspend () -> Unit): Boolean {
        if (state.value.busy != null) return false
        mutable.update { it.copy(busy = what, error = null) }
        return try { block(); true }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ChannelText.failure(error, verb)) }; false }
        finally { mutable.update { it.copy(busy = null) } }
    }

    // ---- binding (docs/extensions/channels.md "API": POST .../bind, a connector.Prompt a step at a time)

    /** "Link": the first step. */
    suspend fun startBind(): Boolean = step(null, null)

    fun setValue(name: String, value: String) = mutable.update { it.copy(bind = it.bind.copy(values = it.bind.values + (name to value))) }

    /** "Next": the step's fields as typed (trimmed; empty ones left out). */
    suspend fun submit(): Boolean {
        val bind = state.value.bind
        if (!bind.canSubmit) return false
        val prompt = bind.prompt ?: return false
        val values = prompt.fields.mapNotNull { f -> bind.values[f.name]?.trim()?.takeIf { it.isNotEmpty() }?.let { f.name to it } }.toMap()
        return step(prompt.step.orEmpty(), values)
    }

    /** The page has said that binding finished. */
    fun boundShown() = mutable.update { it.copy(boundAs = null) }

    /** "Cancel": the step goes away (the hub forgets a pairing code by itself when it expires). */
    fun cancelBind() = mutable.update { it.copy(bind = BindState()) }

    /**
     * While a code is on screen (a QR code to scan, a code to send to the bot), asks the hub again every interval until
     * the answer is another step or done; a failed ask is tried again next time. Returns when there is nothing to
     * wait on; cancelled with the page.
     */
    suspend fun poll(wait: suspend (Long) -> Unit = { delay(it) }) {
        while (true) {
            val bind = state.value.bind
            val prompt = bind.prompt?.takeIf { bind.polled } ?: return
            wait(bind.pollMillis)
            // Cancelled or replaced while waiting: whatever is on screen now is the one to ask about.
            if (state.value.bind.prompt !== prompt) continue
            step(prompt.step.orEmpty(), null, quiet = true, asked = prompt)
        }
    }

    /**
     * Back in front after the page was away (the user scanned with another app): asks the code on screen once at once,
     * so a binding finished meanwhile shows without waiting an interval. Nothing while a step the user sent is on its
     * way; true once the hub answered.
     */
    suspend fun catchUp(): Boolean {
        val bind = state.value.bind
        val prompt = bind.prompt?.takeIf { bind.polled && !bind.sending } ?: return false
        return step(prompt.step.orEmpty(), null, quiet = true, asked = prompt)
    }

    /**
     * True once a step went (the next one is on screen, or binding is done). [asked]: the step a quiet ask was about; a
     * step that is not done any more is dropped when that one has gone meanwhile.
     */
    private suspend fun step(step: String?, values: Map<String, String>?, quiet: Boolean = false, asked: BindPrompt? = null): Boolean {
        if (!quiet) {
            if (state.value.bind.sending) return false
            mutable.update { it.copy(bind = it.bind.copy(sending = true, error = null)) }
        }
        return try {
            val prompt = api.bind(id, step, values)
            // Cancelled or replaced while the ask was on its way: only a finished binding still counts.
            if (asked != null && !prompt.done && state.value.bind.prompt !== asked) return false
            if (prompt.done) {
                mutable.update { it.copy(bind = BindState(), boundAs = prompt.account.orEmpty()) }
                load()
            } else mutable.update {
                // A new step starts with nothing typed; the same step asked again (a mistake) keeps what was typed.
                val same = it.bind.prompt?.step == prompt.step
                it.copy(bind = BindState(prompt, if (same) it.bind.values else emptyMap()))
            }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            if (!quiet) mutable.update { it.copy(bind = it.bind.copy(sending = false, error = ChannelText.failure(error, R.string.settings_src_channel_not_bound))) }
            false
        }
    }
}
