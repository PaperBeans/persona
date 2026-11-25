package persona.feature.settings

import android.annotation.SuppressLint
import android.content.res.Resources
import android.graphics.Bitmap
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import persona.core.contracts.api.BindPrompt
import persona.core.contracts.api.LarkSyncAuthorize
import persona.core.contracts.api.LarkSyncBinding
import persona.core.contracts.api.LarkSyncBot
import persona.core.contracts.api.LarkSyncView
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/** "03:00" in the page's language and clock. */
internal fun larkHourText(res: Resources, hour: Int, zone: ZoneId = ZoneId.systemDefault()): String =
    LocalizedFormats.format(LocalDate.now(zone).atTime(hour, 0).atZone(zone).toInstant(), LocalizedFormats.TIME, res.configuration.locales[0], zone)

/** The next run: its time today, else its day and time (web `clock`). */
private fun larkClockText(res: Resources, at: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
    val today = at.atZone(zone).toLocalDate() == LocalDate.now(zone)
    return LocalizedFormats.format(at, if (today) LocalizedFormats.TIME else LocalizedFormats.MONTH_DAY_TIME, res.configuration.locales[0], zone)
}

/** "Synced 3 hours ago · 12 messages · 4 documents · Next: 03:17", only the parts the hub knows. */
internal fun larkStatusLine(res: Resources, b: LarkSyncBinding, now: Instant = Instant.now()): String {
    val parts = mutableListOf<String>()
    val last = larkTime(b.lastRunAt)
    if (last != null) {
        parts += res.getString(R.string.settings_lark_sync_last_sync, agoText(res, last, now))
        val messages = b.lastMessages ?: 0
        val docs = b.lastDocs ?: 0
        parts += res.getQuantityString(R.plurals.settings_lark_sync_count_messages, messages, messages)
        parts += res.getQuantityString(R.plurals.settings_lark_sync_count_docs, docs, docs)
    } else parts += res.getString(R.string.settings_lark_sync_never_synced)
    val next = larkTime(b.nextRunAt)?.takeIf { b.enabled && !needsAuthorize(b) }
    if (next != null) parts += res.getString(R.string.settings_lark_sync_next_sync, larkClockText(res, next))
    return parts.joinToString(" · ")
}

@Composable
private fun LarkPill.Draw() {
    val words = stringResource(text)
    when (tone) {
        LarkTone.Neutral -> JTag(words, Tone.Neutral)
        LarkTone.Positive -> JTag(words, Tone.Positive)
        LarkTone.Caution -> JTag(words, Tone.Caution)
        LarkTone.Danger -> JTag(words, Tone.Danger)
        LarkTone.Working -> JTag(words, status = Status.Working)
    }
}

@Composable
private fun botTitle(b: LarkSyncBot): String = when {
    b.primary -> stringResource(R.string.settings_lark_sync_chat_bot)
    else -> b.label?.takeIf { it.isNotBlank() } ?: stringResource(R.string.settings_lark_sync_sync_bot)
}

@Composable
private fun bindingTitle(v: LarkSyncView, b: LarkSyncBinding): String =
    bindingName(v, b) ?: stringResource(R.string.settings_lark_sync_account_fallback, bindingNumber(v, b))

/** What is to be removed, once confirmed: a binding's key or `bot:<id>`, with its name. */
private data class LarkRemoval(val key: String, val name: String)

/**
 * Settings › Lark bot › "Daily sync to your knowledge base" (#15392, web `LarkSyncSection.tsx`) as items of the bot's
 * page: nothing on a hub without the sync. Its calls run in [scope], the page's.
 */
internal fun LazyListScope.larkSyncItems(model: LarkSyncModel, state: LarkSyncUiState, scope: CoroutineScope) {
    val view = state.view
    if (view == null) {
        val failed = state.loadError ?: return
        item("lark-sync-header") { JSectionHeader(stringResource(R.string.settings_lark_sync_title), Modifier.padding(top = Tokens.Space.l)) }
        item("lark-sync-failed") {
            JNotice(failed.text(), status = Status.NeedsYou, actionLabel = stringResource(R.string.settings_lark_sync_retry),
                onAction = { scope.launch { model.load() } })
        }
        return
    }
    item("lark-sync-header") { JSectionHeader(stringResource(R.string.settings_lark_sync_title), Modifier.padding(top = Tokens.Space.l)) }
    val bots = orderedBots(view)
    bots.forEach { bot ->
        item("lark-sync-bot-${bot.botId}") { LarkBotGroup(model, state, view, bot, scope) }
    }
    item("lark-sync-schedule") { LarkSchedule(model, view.scheduleHour, state.busy == "schedule", scope) }
    item("lark-sync-footer") {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            state.error?.let { JNotice(it.text(), status = Status.NeedsYou) }
            if (state.flow == null) {
                JButton(stringResource(R.string.settings_lark_sync_add), model::startAdding, Modifier.fillMaxWidth(),
                    kind = JButtonKind.Secondary, icon = PersonaIcons.add)
            }
            SettingsCaption(stringResource(R.string.settings_lark_sync_footer, larkHourText(LocalResources.current, view.scheduleHour)))
        }
    }
    when (val flow = state.flow) {
        is LarkSyncFlow.Choose -> item("lark-sync-choose") { LarkChooseStep(model, flow, bots.firstOrNull { it.primary }, scope) }
        is LarkSyncFlow.Register -> item("lark-sync-register-${flow.bot}") { LarkRegisterStep(model, flow, scope) }
        is LarkSyncFlow.Authorize -> item("lark-sync-authorize-${flow.bot}") { LarkAuthorizeStep(model, flow, scope) }
        null -> {}
    }
}

/** Polls what the section has under way while it is shown: a sync running, a step's QR code, the device flow. */
@Composable
internal fun LarkSyncEffects(model: LarkSyncModel, state: LarkSyncUiState) {
    val res = LocalResources.current
    val feedback = LocalAppFeedback.current
    val scope = rememberCoroutineScope()
    LaunchedEffect(state.running) {
        while (state.running) {
            delay(LarkSync.RunningPollMillis)
            model.load()
        }
    }
    val register = state.flow as? LarkSyncFlow.Register
    LaunchedEffect(register?.bot, register?.prompt?.error, register?.prompt?.kind) { if (register != null) model.pollRegistration() }
    val device = (state.flow as? LarkSyncFlow.Authorize)?.takeIf { it.answer?.mode == "device" }
    LaunchedEffect(device?.bot, device?.prompt?.error, device?.prompt?.interval) { if (device != null) model.pollDevice() }
    var said by rememberSaveable { mutableStateOf(state.connected) }
    LaunchedEffect(state.connected) {
        if (state.connected > said) feedback?.notice(res.getString(R.string.settings_lark_sync_connected_toast), FeedbackStyle.Success)
        said = state.connected
    }
    (state.flow as? LarkSyncFlow.Authorize)?.browser?.let { url ->
        LarkAuthorizeBrowser(url, onNavigate = { address -> model.returnOf(address) }, onReturn = { answer ->
            scope.launch { model.finish(answer) }
        }, onDismiss = model::closeBrowser)
    }
}

// ── the list ───────────────────────────────────────────

@Composable
private fun LarkBotGroup(model: LarkSyncModel, state: LarkSyncUiState, view: LarkSyncView, bot: LarkSyncBot, scope: CoroutineScope) {
    val bindings = bindingsOf(view, bot.botId)
    val ready = botReady(bot)
    val resumable = !ready && !bot.primary && bot.registering
    val rows = 1 + (if (ready || resumable) 1 else 0) + bindings.size
    var removing by remember { mutableStateOf<LarkRemoval?>(null) }
    val title = botTitle(bot)
    val hint = when {
        !ready && bot.primary -> stringResource(R.string.settings_lark_sync_chat_bot_first)
        !ready && bot.registering -> stringResource(R.string.settings_lark_sync_bot_setup_open)
        !ready -> stringResource(R.string.settings_lark_sync_bot_unfinished)
        bindings.isNotEmpty() -> stringResource(tierText(bot.tier))
        else -> stringResource(R.string.settings_lark_sync_no_accounts)
    }
    Column(Modifier.padding(bottom = Tokens.Space.m)) {
        JGroupItem(GroupPosition.of(0, rows)) {
            JRow(title, subtitle = hint, leadingIcon = if (bot.primary) PersonaIcons.channels else PersonaIcons.link, trailing = {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                    approvalOf(bot)?.Draw()
                    if (!bot.primary) JIconButton(PersonaIcons.delete, stringResource(R.string.settings_lark_sync_remove_bot_named, title),
                        { removing = LarkRemoval("bot:${bot.botId}", title) }, enabled = state.busy != "bot:${bot.botId}")
                }
            })
        }
        var at = 1
        if (ready) {
            JGroupItem(GroupPosition.of(at++, rows)) {
                JRow(stringResource(R.string.settings_lark_sync_connect_account), leadingIcon = PersonaIcons.add,
                    onClick = { scope.launch { model.authorize(bot.botId, bot.tier?.takeIf { it.isNotEmpty() } ?: LarkSync.TierDocs, again = false) } })
            }
        } else if (resumable) {
            JGroupItem(GroupPosition.of(at++, rows)) {
                JRow(stringResource(R.string.settings_lark_sync_continue_setup), leadingIcon = PersonaIcons.scanCode,
                    trailing = { if (state.busy == "bot:${bot.botId}") JSpinner() },
                    onClick = { scope.launch { model.resume(bot) } }, enabled = state.busy.isEmpty())
            }
        }
        bindings.forEach { b ->
            JGroupItem(GroupPosition.of(at++, rows)) {
                LarkBindingRow(model, state, view, bot, b, scope) { removing = it }
            }
        }
    }
    removing?.let { r ->
        JDialog(onDismiss = { removing = null }, title = stringResource(R.string.settings_lark_sync_remove_bot_named, r.name),
            confirm = stringResource(if (r.key.startsWith("bot:")) R.string.settings_lark_sync_remove_bot else R.string.settings_lark_sync_remove) to {
                removing = null
                val binding = bindings.firstOrNull { it.key == r.key }
                scope.launch {
                    if (binding == null) { val _ = model.removeBot(bot) } else { val _ = model.remove(binding) }
                }
            }, destructive = true)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LarkBindingRow(
    model: LarkSyncModel, state: LarkSyncUiState, view: LarkSyncView, bot: LarkSyncBot, b: LarkSyncBinding, scope: CoroutineScope,
    onRemove: (LarkRemoval) -> Unit,
) {
    val res = LocalResources.current
    val feedback = LocalAppFeedback.current
    val name = bindingTitle(view, b)
    val busy = state.busy == b.key
    val authorize = needsAuthorize(b)
    val soon = reauthSoon(b)
    Column(Modifier.testTag("lark-sync-binding-${b.key}")) {
        JRow(name, subtitle = larkStatusLine(res, b), subtitleLines = 3, trailing = {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                healthOf(b).Draw()
                JSwitch(b.enabled, { on -> scope.launch { model.setEnabled(b, on) } }, enabled = !busy && (!authorize || b.health == "scope_missing"))
            }
        })
        Column(Modifier.padding(horizontal = rowPadding).padding(bottom = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            b.error?.takeIf { it.isNotBlank() && !b.running }?.let { JInlineStatus(it, tone = InlineTone.Caution) }
            soon?.let { JInlineStatus(pluralStringResource(R.plurals.settings_lark_sync_reauth_soon, it, it), tone = InlineTone.Caution) }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                if (authorize || soon != null) {
                    JButton(stringResource(R.string.settings_lark_sync_authorize_again), {
                        scope.launch { model.authorize(b.botId, reauthTier(b, bot), again = true) }
                    }, kind = if (authorize) JButtonKind.Primary else JButtonKind.Secondary, size = JButtonSize.Small, icon = PersonaIcons.credentials)
                }
                if (!authorize) {
                    JButton(stringResource(if (b.running) R.string.settings_lark_sync_running else R.string.settings_lark_sync_run_now),
                        { scope.launch { if (model.run(b)) feedback?.notice(res.getString(R.string.settings_lark_sync_run_started), FeedbackStyle.Success) } },
                        kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PersonaIcons.retry, enabled = b.enabled && !b.running && !busy,
                        loading = busy)
                }
                JButton(stringResource(R.string.settings_lark_sync_remove), { onRemove(LarkRemoval(b.key, name)) },
                    kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = !busy)
            }
        }
    }
}

@Composable
private fun LarkSchedule(model: LarkSyncModel, hour: Int, busy: Boolean, scope: CoroutineScope) {
    val res = LocalResources.current
    var open by remember { mutableStateOf(false) }
    Box(Modifier.padding(bottom = Tokens.Space.s)) {
        JGroupItem(GroupPosition.Single) {
            JRow(stringResource(R.string.settings_lark_sync_schedule_label), subtitle = stringResource(R.string.settings_lark_sync_schedule_hint),
                leadingIcon = PersonaIcons.schedule, value = larkHourText(res, hour), onClick = { open = true }, enabled = !busy)
        }
        JMenu(open, { open = false }, (0..23).map { h ->
            JAction(larkHourText(res, h), checked = h == hour) { open = false; if (h != hour) scope.launch { model.setSchedule(h) } }
        })
    }
}

// ── adding: step 1 ──────────────────────────────────────

@Composable
private fun LarkChooseStep(model: LarkSyncModel, flow: LarkSyncFlow.Choose, primary: LarkSyncBot?, scope: CoroutineScope) {
    Column(Modifier.padding(top = Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JSectionHeader(stringResource(R.string.settings_lark_sync_add_title))
        SettingsCaption(stringResource(R.string.settings_lark_sync_add_intro))
        Text(stringResource(R.string.settings_lark_sync_tier_label), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2, maxLines = 1)
        Column {
            LarkSync.Tiers.forEachIndexed { i, tier ->
                JGroupItem(GroupPosition.of(i, LarkSync.Tiers.size)) {
                    JRow(stringResource(tierText(tier)), subtitle = stringResource(if (tier == LarkSync.TierDocs) R.string.settings_lark_sync_tier_docs_hint
                        else R.string.settings_lark_sync_tier_full_hint), subtitleLines = 4, chevron = false,
                        trailing = { JRadio(flow.tier == tier, null) }, onClick = { model.editChoice(tier = tier) })
                }
            }
        }
        JTextField(flow.label, { model.editChoice(label = it) }, Modifier.fillMaxWidth(), label = stringResource(R.string.settings_lark_sync_name_label),
            placeholder = stringResource(R.string.settings_lark_sync_name_placeholder), supportingText = stringResource(R.string.settings_lark_sync_name_hint),
            singleLine = true, keyboardOptions = KeyboardOptions(autoCorrectEnabled = false))
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            Text(stringResource(R.string.settings_lark_sync_region_label), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2, maxLines = 1)
            val regions = listOf("feishu", "lark")
            JSegmented(listOf(JSegment(stringResource(R.string.settings_lark_sync_region_feishu)), JSegment(stringResource(R.string.settings_lark_sync_region_lark))),
                regions.indexOf(flow.region).coerceAtLeast(0), { model.editChoice(region = regions[it]) })
        }
        flow.error?.let { JInlineStatus(it.text(), tone = InlineTone.Failed) }
        JButton(stringResource(R.string.settings_lark_sync_create_bot), { scope.launch { model.choose(chatBot = false) } }, Modifier.fillMaxWidth(),
            icon = PersonaIcons.scanCode, loading = flow.busy == "new", enabled = flow.busy.isEmpty())
        if (primary != null && botReady(primary)) {
            JButton(stringResource(R.string.settings_lark_sync_use_chat_bot), { scope.launch { model.choose(chatBot = true) } }, Modifier.fillMaxWidth(),
                kind = JButtonKind.Secondary, loading = flow.busy == "chat", enabled = flow.busy.isEmpty())
        }
        JButton(stringResource(R.string.settings_src_cancel), model::cancel, Modifier.fillMaxWidth(), kind = JButtonKind.Quiet, enabled = flow.busy.isEmpty())
        SettingsCaption(stringResource(R.string.settings_lark_sync_add_footer))
    }
}

@Composable
private fun LarkRegisterStep(model: LarkSyncModel, flow: LarkSyncFlow.Register, scope: CoroutineScope) {
    Column(Modifier.padding(top = Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JSectionHeader(stringResource(if (flow.scopes) R.string.settings_lark_sync_step_scopes else R.string.settings_lark_sync_step_create))
        LarkPromptBody(flow.prompt, onCancel = model::cancel, onRestart = { scope.launch { model.restartRegistration() } })
        SettingsCaption(stringResource(R.string.settings_lark_sync_step_create_footer))
    }
}

// ── adding: step 2 ──────────────────────────────────────

@Composable
private fun LarkAuthorizeStep(model: LarkSyncModel, flow: LarkSyncFlow.Authorize, scope: CoroutineScope) {
    val answer = flow.answer
    Column(Modifier.padding(top = Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JSectionHeader(stringResource(if (flow.again) R.string.settings_lark_sync_authorize_title else R.string.settings_lark_sync_step_authorize))
        when {
            flow.error != null -> {
                JInlineStatus(flow.error.text(), tone = InlineTone.Failed)
                Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    JButton(stringResource(R.string.settings_lark_sync_retry), { scope.launch { model.ask() } }, Modifier.weight(1f),
                        kind = JButtonKind.Secondary, icon = PersonaIcons.retry, loading = flow.busy)
                    JButton(stringResource(R.string.settings_src_cancel), model::cancel, Modifier.weight(1f), kind = JButtonKind.Quiet)
                }
            }
            flow.finishing -> LarkWaiting(stringResource(R.string.settings_lark_sync_return_working))
            answer == null -> LarkWaiting(stringResource(R.string.settings_lark_sync_authorize_preparing))
            answer.mode == "device" && flow.prompt != null -> {
                SettingsCaption(stringResource(R.string.settings_lark_sync_authorize_device))
                LarkPromptBody(flow.prompt, onCancel = model::cancel, onRestart = { scope.launch { model.ask() } })
            }
            answer.mode == "manual" -> LarkManualStep(answer, flow, flow.busy, onContinue = { scope.launch { model.continueInBrowser() } }, onCancel = model::cancel)
            else -> {
                SettingsCaption(stringResource(R.string.settings_lark_sync_authorize_browser))
                JButton(stringResource(R.string.settings_lark_sync_authorize_continue), { scope.launch { model.continueInBrowser() } }, Modifier.fillMaxWidth(),
                    icon = PersonaIcons.openExternal, loading = flow.busy)
                JButton(stringResource(R.string.settings_src_cancel), model::cancel, Modifier.fillMaxWidth(), kind = JButtonKind.Quiet)
            }
        }
    }
}

/** The app could not be set up for the return address: the owner adds it in the app's settings, then goes on. */
@Composable
private fun LarkManualStep(answer: LarkSyncAuthorize, flow: LarkSyncFlow.Authorize, busy: Boolean, onContinue: () -> Unit, onCancel: () -> Unit) {
    val copy = rememberCopy()
    val uri = LocalUriHandler.current
    val address = answer.redirectUri?.takeIf { it.isNotBlank() } ?: flow.redirect.orEmpty()
    SettingsCaption(stringResource(R.string.settings_lark_sync_manual_intro))
    Row(Modifier.padding(start = rowPadding), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JSelectableText(address, PersonaTheme.type.codeSmall, PersonaTheme.palette.ink, Modifier.weight(1f), maxLines = 3)
        JIconButton(PersonaIcons.copy, stringResource(R.string.settings_lark_sync_copy_address), { copy(address, null) })
    }
    answer.consoleUrl?.takeIf { StepView.isWebLink(it) }?.let { console ->
        JButton(stringResource(R.string.settings_lark_sync_open_app_settings), { val _ = runCatching { uri.openUri(console) } }, Modifier.fillMaxWidth(),
            kind = JButtonKind.Secondary, icon = PersonaIcons.openExternal)
    }
    JButton(stringResource(R.string.settings_lark_sync_manual_continue), onContinue, Modifier.fillMaxWidth(), loading = busy)
    JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.fillMaxWidth(), kind = JButtonKind.Quiet)
}

@Composable
private fun LarkWaiting(text: String) {
    Row(Modifier.padding(horizontal = rowPadding), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JSpinner()
        Text(text, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}

/** A step's prompt from the hub: its words, then the QR code, the link it encodes, or a code to enter (web `PromptBody`). */
@Composable
private fun LarkPromptBody(prompt: BindPrompt, onCancel: () -> Unit, onRestart: () -> Unit) {
    val copy = rememberCopy()
    val uri = LocalUriHandler.current
    val app = stringResource(R.string.settings_lark_sync_lark_app)
    prompt.hint?.takeIf { it.isNotBlank() }?.let {
        Text(it, Modifier.padding(horizontal = rowPadding), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
    }
    // A terminal failure (expired, declined): said, with a way to start again; the old QR code is no use any more.
    prompt.error?.takeIf { it.isNotBlank() }?.let { error ->
        JInlineStatus(error, tone = InlineTone.Failed)
        Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(stringResource(R.string.settings_lark_sync_start_again), onRestart, Modifier.weight(1f), kind = JButtonKind.Secondary, icon = PersonaIcons.retry)
            JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.weight(1f), kind = JButtonKind.Quiet)
        }
        return
    }
    val code = prompt.userCode?.takeIf { it.isNotBlank() }
    val link = (prompt.link ?: prompt.url)?.takeIf { StepView.isWebLink(it) }
    when (StepView.of(prompt)) {
        StepView.QrCode -> Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            code?.let { LarkCode(it, copy) }
            JQrCode(prompt.qr?.rows.orEmpty(), Modifier.align(Alignment.CenterHorizontally), label = stringResource(R.string.settings_lark_sync_qr_label))
            LarkWaiting(stringResource(R.string.settings_lark_sync_waiting_confirm))
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                // On the phone that shows the code, scanning it is not possible: the link (and the code it asks for) instead.
                prompt.link?.takeIf { StepView.isWebLink(it) }?.let { url ->
                    JButton(prompt.linkText?.takeIf { it.isNotBlank() } ?: stringResource(R.string.settings_lark_sync_open_link),
                        { val _ = runCatching { uri.openUri(url) } }, Modifier.weight(1f), kind = JButtonKind.Secondary, icon = PersonaIcons.openExternal)
                }
                JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.weight(1f), kind = JButtonKind.Quiet)
            }
        }
        StepView.QrLink -> {
            QrLinkStep(prompt.url.orEmpty(), app)
            LarkWaiting(stringResource(R.string.settings_lark_sync_waiting_confirm))
            JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.fillMaxWidth(), kind = JButtonKind.Quiet)
        }
        StepView.Code -> {
            code?.let { LarkCode(it, copy) }
            LarkWaiting(stringResource(R.string.settings_lark_sync_waiting_confirm))
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                link?.let { url ->
                    JButton(stringResource(R.string.settings_lark_sync_open_link), { val _ = runCatching { uri.openUri(url) } }, Modifier.weight(1f),
                        kind = JButtonKind.Secondary, icon = PersonaIcons.openExternal)
                }
                JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.weight(1f), kind = JButtonKind.Quiet)
            }
        }
        else -> CannotDrawStep(prompt, busy = false, onRestart = onRestart, onCancel = onCancel)
    }
}

@Composable
private fun LarkCode(code: String, copy: CopyAction) {
    Row(Modifier.padding(start = rowPadding), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JCodeDisplay(code, Modifier.weight(1f))
        JIconButton(PersonaIcons.copy, stringResource(R.string.settings_lark_sync_copy_code), { copy(code, null) })
    }
}

/**
 * The open platform's authorization page in the in-app browser, rising from the bottom (CLAUDE.md "Links open inside
 * the app"). Its return to the hub's /lark-oauth never loads: [onNavigate] recognizes it, and [onReturn] hands the code on.
 */
@SuppressLint("SetJavaScriptEnabled")
@Composable
private fun LarkAuthorizeBrowser(url: String, onNavigate: (String) -> LarkOAuthReturn?, onReturn: (LarkOAuthReturn) -> Unit, onDismiss: () -> Unit) {
    val navigate by rememberUpdatedState(onNavigate)
    val back by rememberUpdatedState(onReturn)
    // A web page over the page: no glass may sample its WebView (LocalWebSheets).
    WebSheetShown(true)
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.settings_lark_sync_browser_title), contentPadding = Tokens.Space.flush) {
        AndroidView(factory = { context ->
            WebView(context).apply {
                settings.javaScriptEnabled = true
                settings.domStorageEnabled = true
                var handled = false
                fun caught(address: String?): Boolean {
                    if (handled || address == null) return handled
                    val answer = navigate(address) ?: return false
                    handled = true
                    back(answer)
                    return true
                }
                webViewClient = object : WebViewClient() {
                    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
                        request.isForMainFrame && caught(request.url.toString())

                    override fun onPageStarted(view: WebView, address: String?, favicon: Bitmap?) {
                        if (caught(address)) view.stopLoading() else super.onPageStarted(view, address, favicon)
                    }
                }
                loadUrl(url)
            }
        }, modifier = Modifier.fillMaxWidth().fillMaxHeight(), onRelease = { it.stopLoading(); it.destroy() })
    }
}
