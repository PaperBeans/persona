package persona.feature.settings

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import persona.core.contracts.api.BindPrompt
import persona.core.contracts.api.ConnectorsManagement
import persona.core.contracts.api.ManagerRoster
import persona.core.contracts.api.HubConnector
import persona.core.contracts.api.LarkStatus
import persona.core.contracts.api.MailAccount
import persona.core.designsystem.Status
import persona.core.designsystem.failureReasonText
import android.content.res.Resources
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalResources

/** What Settings › Connectors says on each row (iOS `ConnectorText`). Pure given [Resources]: unit-tested. */
object ConnectorText {
    fun mail(res: Resources, accounts: List<MailAccount>?): String = when {
        accounts == null -> ""
        accounts.isEmpty() -> res.getString(R.string.settings_src_connector_none_added)
        accounts.any { it.isError } -> res.getQuantityString(R.plurals.settings_src_connector_mail_errors, accounts.size, accounts.size)
        else -> res.getQuantityString(R.plurals.settings_src_connector_mail_accounts, accounts.size, accounts.size)
    }

    fun lark(res: Resources, st: LarkStatus?): String = when {
        st == null -> ""
        !st.configured -> res.getString(R.string.settings_src_state_not_configured)
        st.connected -> st.name?.takeIf { it.isNotBlank() } ?: res.getString(R.string.settings_src_state_connected)
        else -> res.getString(R.string.settings_src_state_unreachable)
    }

    fun device(res: Resources, on: Int): String =
        if (on == 0) res.getString(R.string.settings_src_connector_device_none) else res.getQuantityString(R.plurals.settings_src_connector_device_on, on, on)

    fun state(res: Resources, c: HubConnector): String = res.getString(when (c.status) {
        "ok" -> R.string.settings_src_state_connected
        "not_configured" -> R.string.settings_src_state_not_configured
        "not_signed_in" -> R.string.settings_src_state_signed_out
        "no_permission" -> R.string.settings_src_state_no_permission
        "rate_limited" -> R.string.settings_src_state_rate_limited
        "disabled" -> R.string.settings_src_state_off
        "error" -> R.string.settings_src_state_error
        else -> R.string.settings_src_state_unknown
    })

    fun summary(res: Resources, c: HubConnector): String =
        if (c.status == "ok") (c.account ?: state(res, c)) else state(res, c)

    @Composable fun mail(accounts: List<MailAccount>?): String = mail(resources(), accounts)
    @Composable fun lark(st: LarkStatus?): String = lark(resources(), st)
    @Composable fun device(on: Int): String = device(resources(), on)
    @Composable fun state(c: HubConnector): String = state(resources(), c)
    @Composable fun summary(c: HubConnector): String = summary(resources(), c)

    @Composable
    private fun resources(): Resources = LocalResources.current

    fun status(c: HubConnector): Status = when (c.status) {
        "ok" -> Status.Idle
        "rate_limited", "error", "no_permission" -> Status.NeedsYou
        else -> Status.Offline
    }

    /** The user signs in to it here, a step at a time (its capabilities say `login`; the web's `signsInHere`). */
    fun signsInHere(c: HubConnector): Boolean = c.capabilities?.contains("login") == true

    /** Signed in as far as the hub knows (a rate limit is still signed in). */
    fun isSignedIn(c: HubConnector): Boolean = c.status == "ok" || c.status == "rate_limited"
}

/** Signing in to a connector: the step on screen ([bind]), and who signed in once it finished (said once). */
internal data class ConnectorSignIn(val bind: BindState = BindState(), val signedInAs: String? = null)

/**
 * Signing in to a hub connector a step at a time (`POST /api/v1/connectors/{id}/login`, the same `connector.Prompt` as a
 * channel's binding): the fields the hub names, a QR code or a device code asked about again every interval, or done;
 * and signing out. What a step asks for is the hub's, nothing per connector.
 */
internal class ConnectorSignInModel(
    private val stepCall: suspend (String, Map<String, String>) -> BindPrompt,
    private val logoutCall: suspend () -> Unit,
) {
    constructor(api: ConnectorsManagement, id: String) : this({ step, values -> api.connectorLogin(id, step, values) }, { api.connectorLogout(id) })

    private val mutable = MutableStateFlow(ConnectorSignIn())
    val state = mutable.asStateFlow()

    /** "Sign in": the first step. */
    suspend fun start(): Boolean = step("", emptyMap())

    fun setValue(name: String, value: String) = update { it.copy(values = it.values + (name to value)) }

    /** "Next": the step's fields as typed (trimmed; empty ones left out). */
    suspend fun submit(): Boolean {
        val bind = state.value.bind
        val prompt = bind.prompt ?: return false
        if (!bind.canSubmit) return false
        val values = prompt.fields.mapNotNull { f -> bind.values[f.name]?.trim()?.takeIf { it.isNotEmpty() }?.let { f.name to it } }.toMap()
        return step(prompt.step.orEmpty(), values)
    }

    /** "Cancel": the step goes away. */
    fun cancel() = update { BindState() }

    /**
     * While a code is on screen, asks the hub again every interval until the answer is another step or done; a failed
     * ask is tried again next time. Returns when there is nothing to wait on.
     */
    suspend fun poll(wait: suspend (Long) -> Unit = { delay(it) }) {
        while (true) {
            val bind = state.value.bind
            val prompt = bind.prompt?.takeIf { bind.polled } ?: return
            wait(bind.pollMillis)
            // Cancelled or replaced while waiting: whatever is on screen now is the one to ask about.
            if (state.value.bind.prompt !== prompt) continue
            step(prompt.step.orEmpty(), emptyMap(), quiet = true, asked = prompt)
        }
    }

    /**
     * Back in front after the page was away (the user scanned with another app or approved in a browser): asks the code
     * on screen once at once, so a sign-in finished meanwhile shows without waiting an interval. Nothing while a step the
     * user sent is on its way; true once the hub answered.
     */
    suspend fun catchUp(): Boolean {
        val bind = state.value.bind
        val prompt = bind.prompt?.takeIf { bind.polled && !bind.sending } ?: return false
        return step(prompt.step.orEmpty(), emptyMap(), quiet = true, asked = prompt)
    }

    /** "Sign out"; why it did not go, null when it did. */
    suspend fun logout(): ResText? = try {
        logoutCall()
        update { BindState() }
        null
    } catch (cancelled: CancellationException) { throw cancelled }
    catch (error: Exception) { ResText.of(R.string.settings_src_connector_not_signed_out, failureReasonText(error)) }

    /** The page has said that signing in finished. */
    fun signedInShown() = mutable.update { it.copy(signedInAs = null) }

    private fun update(change: (BindState) -> BindState) = mutable.update { it.copy(bind = change(it.bind)) }

    /** [asked]: the step a quiet ask was about; a step that is not done any more is dropped when that one has gone meanwhile. */
    private suspend fun step(step: String, values: Map<String, String>, quiet: Boolean = false, asked: BindPrompt? = null): Boolean {
        if (!quiet) {
            if (state.value.bind.sending) return false
            update { it.copy(sending = true, error = null) }
        }
        return try {
            val prompt = stepCall(step, values)
            // Cancelled or replaced while the ask was on its way: only a finished sign-in still counts.
            if (asked != null && !prompt.done && state.value.bind.prompt !== asked) return false
            if (prompt.done) mutable.update { ConnectorSignIn(signedInAs = prompt.account.orEmpty()) }
            // A new step starts empty; the same step asked again (a mistake) keeps what was typed.
            else update { BindState(prompt, if (it.prompt?.step == prompt.step) it.values else emptyMap()) }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            if (!quiet) update { it.copy(sending = false, error = ResText.of(R.string.settings_src_connector_not_signed_in, failureReasonText(error))) }
            false
        }
    }
}

/** A row of Settings › Connectors, in order (iOS `ConnectorRow`). */
internal sealed interface ConnectorRow {
    data object Phone : ConnectorRow
    data object Mail : ConnectorRow
    /** Lark: its page reads `/api/v1/lark/...`, the Lark extension's (`LarkFeatures`, #1530). */
    data object Lark : ConnectorRow
    data class Hub(val connector: HubConnector) : ConnectorRow
}

/**
 * Settings › Connectors' rows: this phone, mail, Lark while the hub has Feishu chats ([lark], `LarkFeatures.chats`), then the
 * hub's other connectors (mail and Feishu have rows of their own).
 */
internal fun connectorRows(hubConnectors: List<HubConnector>, lark: Boolean): List<ConnectorRow> =
    listOfNotNull(ConnectorRow.Phone, ConnectorRow.Mail, ConnectorRow.Lark.takeIf { lark }) +
        hubConnectors.filter { it.id != "mail" && it.id != "lark" }.map(ConnectorRow::Hub)

/** A merged list can contain the same provider under independent managers; name the owner when they collide. */
internal fun connectorTitle(connector: HubConnector, connectors: List<HubConnector>, roster: ManagerRoster?): String {
    val owner = roster.ownerOrDefault(connector.manager)
    val ambiguous = connectors.any { it.name == connector.name && roster.ownerOrDefault(it.manager) != owner }
    if (!ambiguous) return connector.name
    val name = roster?.manager(owner)?.name?.takeIf { it.isNotBlank() } ?: owner
    val label = if (roster?.managers?.count { it.name == name }?.let { it > 1 } == true) "$name ($owner)" else name
    return "${connector.name} · $label"
}
