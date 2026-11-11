package persona.feature.settings

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import android.os.PersistableBundle
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import persona.core.contracts.api.AppPairCode
import persona.core.contracts.api.DeviceSettings
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

// Settings › Devices › Connect a new device (docs/assistant/onboarding.md 4.2, after the web's #2731 and iOS's #3278): a one-time pairing code from the
// hub (POST /api/v1/auth/pair-codes, docs/protocol/protocol.md "Temporary pairing") for another app (its QR code and the pairing link,
// which the Mac and Android paste) or for a browser (the sign-in link and its QR code, which a phone's camera opens).
// The code lasts ten minutes and is traded once; the page says which device took it.

/** What the code is for: another Persona App, or a browser. */
internal enum class ConnectTarget { App, Web }

internal sealed interface ConnectPhase {
    data object Loading : ConnectPhase
    data class Failed(val reason: UiText) : ConnectPhase
    data class Showing(val code: AppPairCode) : ConnectPhase
    data class Paired(val name: String) : ConnectPhase
}

internal data class ConnectDeviceState(val phase: ConnectPhase = ConnectPhase.Loading, val now: Long = 0) {
    val code: AppPairCode? get() = (phase as? ConnectPhase.Showing)?.code
    /** Whole seconds the code has left; 0 once it has expired or when its time could not be read. */
    val secondsLeft: Int get() = code?.let { secondsLeft(it.expiresAt, now) } ?: 0
    val expired: Boolean get() = code != null && secondsLeft == 0
    /** Counting down and waiting for the other device. */
    val live: Boolean get() = code != null && secondsLeft > 0
}

/** A code lasts ten minutes (docs/protocol/protocol.md "Temporary pairing"). */
internal const val PairCodeLifeMs = 10 * 60_000L

/**
 * When a code just received runs out by this phone's clock: the hub's time, unless it is unreadable (0) or the two
 * clocks disagree so much that it reads as already gone or as longer than a code lasts; then ten minutes from now.
 */
internal fun localExpiry(expiresAt: Long, now: Long): Long = if (expiresAt <= now || expiresAt - now > PairCodeLifeMs) now + PairCodeLifeMs else expiresAt

internal fun secondsLeft(expiresAt: Long, now: Long): Int = if (expiresAt <= 0) 0 else maxOf(0L, (expiresAt - now + 999) / 1000).toInt()

/** m:ss, for the countdown. */
internal fun clock(seconds: Int): String = "${maxOf(0, seconds) / 60}:${(maxOf(0, seconds) % 60).toString().padStart(2, '0')}"

/** Makes the code, counts it down and watches the device list for the one that used it. */
internal class ConnectDeviceModel(private val api: DeviceSettings, private val clock: () -> Long = System::currentTimeMillis) {
    private val mutable = MutableStateFlow(ConnectDeviceState(now = clock()))
    val state = mutable.asStateFlow()
    private var known: Set<String> = emptySet()

    /** A new code (also after it expired, and for another device after one paired). */
    suspend fun make() {
        mutable.update { it.copy(phase = ConnectPhase.Loading) }
        try {
            // The credentials there are now: the one that shows up later is the device that used this code.
            known = api.devices().devices.map { it.id }.toSet()
            val code = api.pairCode()
            val now = clock()
            mutable.update { it.copy(phase = ConnectPhase.Showing(code.copy(expiresAt = localExpiry(code.expiresAt, now))), now = now) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(phase = ConnectPhase.Failed(failureReasonText(error))) } }
    }

    fun tick() = mutable.update { it.copy(now = clock()) }

    /** Reads the device list once; true when a new device has taken the code. */
    suspend fun poll(): Boolean {
        if (!state.value.live) return false
        val list = try { api.devices().devices } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { return false }
        val device = list.firstOrNull { it.id !in known } ?: return false
        if (!state.value.live) return false
        mutable.update { it.copy(phase = ConnectPhase.Paired(device.name.ifBlank { device.id })) }
        return true
    }

    /**
     * Back in front: reads the device list once, even when the code has expired meanwhile, since a device may have
     * taken it while the page was stopped (and nothing was polling). Only while a code is shown; true (and [onPaired])
     * when a new device took it.
     */
    suspend fun catchUp(onPaired: () -> Unit): Boolean {
        val shown = state.value.phase as? ConnectPhase.Showing ?: return false
        val list = try { api.devices().devices } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { return false }
        val device = list.firstOrNull { it.id !in known } ?: return false
        // A new code was asked for while the list was on its way: the device is not this page's answer any more.
        if (state.value.phase !== shown) return false
        mutable.update { it.copy(phase = ConnectPhase.Paired(device.name.ifBlank { device.id })) }
        onPaired()
        return true
    }

    /** The countdown each second and the device list every three, until the code is used or expires. */
    suspend fun run(onPaired: () -> Unit) {
        var n = 0
        while (true) {
            delay(1_000)
            tick()
            if (!state.value.live) continue
            if (++n % 3 == 0 && poll()) onPaired()
        }
    }
}

/** "Connect a new device": the target, its QR code, the link to copy, the countdown; then which device took it. */
@Composable
internal fun ConnectDevicePage(api: DeviceSettings, onBack: () -> Unit, onPaired: () -> Unit) {
    val model = remember(api) { ConnectDeviceModel(api) }
    val state by model.state.collectAsState()
    var target by rememberSaveable { mutableStateOf(ConnectTarget.App) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(model) { model.make() }
    // Only while the page is shown: a hidden tab, the app in the background or the screen off asks the hub nothing.
    // Back in front, the countdown catches up at once and the device list is read right away, so a device that paired
    // while the page was stopped is shown even if the code has expired since.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(model, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { model.tick(); model.catchUp(onPaired); model.run(onPaired) }
    }
    BackHandler(onBack = onBack)
    val context = LocalContext.current
    val feedback = LocalAppFeedback.current
    JListPage(title = stringResource(R.string.settings_onb_connect_title), onBack = onBack, largeTitle = false, bottomBar = {
        Row(Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (state.phase is ConnectPhase.Paired) JButton(stringResource(R.string.settings_onb_connect_another), { target = ConnectTarget.App; scope.launch { model.make() } },
                Modifier.weight(1f), kind = JButtonKind.Secondary)
            JButton(stringResource(R.string.settings_onb_finish), onBack, Modifier.weight(1f))
        }
    }) {
        if (state.phase !is ConnectPhase.Paired) item("target") {
            JSegmented(listOf(JSegment(stringResource(R.string.settings_onb_connect_target_app)), JSegment(stringResource(R.string.settings_onb_connect_target_web))),
                target.ordinal, { target = ConnectTarget.entries[it] }, Modifier.fillMaxWidth().padding(top = Tokens.Space.s))
        }
        when (val phase = state.phase) {
            ConnectPhase.Loading -> item("loading") { JLoading() }
            is ConnectPhase.Failed -> item("failed") {
                JEmptyState(stringResource(R.string.settings_onb_connect_failed), phase.reason.text(), icon = PhIcons.WarningCircle,
                    action = { JButton(stringResource(R.string.settings_onb_retry), { scope.launch { model.make() } }, kind = JButtonKind.Secondary) })
            }
            is ConnectPhase.Paired -> item("paired") {
                JEmptyState(stringResource(R.string.settings_onb_connect_paired, phase.name), stringResource(R.string.settings_onb_connect_paired_hint), icon = PhIcons.CheckCircle)
            }
            is ConnectPhase.Showing -> if (state.expired) item("expired") {
                JEmptyState(stringResource(R.string.settings_onb_connect_expired), stringResource(R.string.settings_onb_connect_expired_hint), icon = PhIcons.ClockCountdown,
                    action = { JButton(stringResource(R.string.settings_onb_connect_new_code), { scope.launch { model.make() } }) })
            } else item("code") {
                val code = phase.code
                val qr = if (target == ConnectTarget.App) code.qr else code.webQr
                val link = if (target == ConnectTarget.App) code.pairUrl else code.webUrl
                Column(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.l), horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    if (qr != null) JQrCode(qr.rows, label = stringResource(if (target == ConnectTarget.App) R.string.settings_onb_connect_qr_app else R.string.settings_onb_connect_qr_web))
                    SettingsCaption(stringResource(if (target == ConnectTarget.App) R.string.settings_onb_connect_hint_app else R.string.settings_onb_connect_hint_web))
                    if (target == ConnectTarget.Web && link == null) SettingsCaption(stringResource(R.string.settings_onb_connect_web_old))
                    if (target == ConnectTarget.Web && link != null && code.loopbackOnly) JNotice(stringResource(R.string.settings_onb_connect_loopback), status = Status.NeedsYou, icon = PhIcons.Warning)
                    if (link != null) {
                        val copied = stringResource(R.string.settings_onb_copied)
                        val clipLabel = stringResource(if (target == ConnectTarget.App) R.string.settings_onb_connect_copy_app else R.string.settings_onb_connect_copy_web)
                        JButton(clipLabel, { copySecret(context, clipLabel, link); feedback?.notice(copied) }, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.Copy)
                    }
                    SettingsCaption(stringResource(R.string.settings_onb_connect_expires, clock(state.secondsLeft)))
                }
            }
        }
    }
}

/** Copies a link that carries a one-time code; Android 13+ keeps it out of the clipboard preview. */
internal fun copySecret(context: Context, label: String, text: String) {
    val clip = ClipData.newPlainText(label, text)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
    context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(clip)
}
