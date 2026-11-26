package persona.core.contracts.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Settings › Lark bot › "Daily sync to your knowledge base" (web `lib/lark-sync.ts`, hub `extmgr/larksync_routes.go`):
 * the owner lets a bot's app read Feishu / Lark as them, and the hub reads what changed every day into the knowledge
 * base. Every route lives under the bot's own `/api/v1/bots/feishu/sync`. A hub without the sync (an older one: 404,
 * 405 or 501; or one without the Lark module: `available` false) answers [view] with null, and the section hides.
 */
interface LarkSyncSettings {
    /** `GET …/sync`; null on a hub without it. */
    suspend fun view(): LarkSyncView?

    /** `PUT …/sync {schedule_hour}`: the local hour (0–23) the daily run starts at. */
    suspend fun setSchedule(hour: Int): LarkSyncView?

    /** `POST …/sync/bots`: starts creating a sync-only bot's app by scanning; the prompt carries the QR code. */
    suspend fun createBot(label: String, tier: String, region: String): LarkSyncCreated

    /** `POST …/bots/{id}/register {step}`: asks a registration again; done once the owner confirmed it. */
    suspend fun register(botId: String, step: String = "register"): BindPrompt

    /** `POST …/bots/{id}/scopes {tier}`: adds a tier's read permissions to an existing app (the chat bot's), by scanning. */
    suspend fun addScopes(botId: String, tier: String): BindPrompt

    /** `DELETE …/bots/{id}`: a sync bot and its accounts. */
    suspend fun removeBot(botId: String): LarkSyncView?

    /** `POST …/bots/{id}/authorize {redirect_uri, tier}`: how the owner allows the app to read as them. */
    suspend fun authorize(botId: String, redirectUri: String, tier: String?): LarkSyncAuthorize

    /** `POST …/bots/{id}/authorize/poll`: the device flow's state (done, an error, or still waiting). */
    suspend fun pollAuthorize(botId: String): BindPrompt

    /** `POST …/sync/oauth {state, code}`: the open platform's code, handed back after the browser authorization. */
    suspend fun finishOAuth(state: String, code: String): LarkSyncView?

    /** `PUT …/bindings/{key} {enabled}`. */
    suspend fun setBindingEnabled(key: String, enabled: Boolean): LarkSyncView?

    /** `DELETE …/bindings/{key}`. */
    suspend fun removeBinding(key: String): LarkSyncView?

    /** `POST …/bindings/{key}/run`: syncs the account now. */
    suspend fun runBinding(key: String): LarkSyncView?

    /**
     * Where the open platform sends the owner back after authorizing: this hub's web address with `/lark-oauth` (the
     * hub accepts only an http(s) address of that page). The app catches the return itself; null while no address is known.
     */
    suspend fun oauthRedirectUri(): String?
}

@Serializable data class LarkSyncAccount(
    val id: String = "",
    val label: String? = null,
    val brand: String? = null,
    val name: String? = null,
)

@Serializable data class LarkSyncBot(
    @SerialName("bot_id") val botId: String = "",
    val label: String? = null,
    @SerialName("app_id") val appId: String? = null,
    val domain: String? = null,
    /** chat_and_sync (the chat bot) or sync_only. */
    val role: String? = null,
    val tier: String? = null,
    /** ready | awaiting_approval | degraded; absent: nothing known. */
    val approval: String? = null,
    /** The chat bot of bots.feishu, listed first. */
    val primary: Boolean = false,
    /** Absent on an older answer: taken as set. */
    @SerialName("has_secret") val hasSecret: Boolean? = null,
    /** A registration (its QR step) is under way: asked again with [LarkSyncSettings.register]. */
    val registering: Boolean = false,
)

@Serializable data class LarkSyncBinding(
    val key: String = "",
    @SerialName("bot_id") val botId: String = "",
    @SerialName("account_id") val accountId: String = "",
    val label: String? = null,
    val tier: String? = null,
    val enabled: Boolean = false,
    /** ok | unauthorized | needs_reauth | rate_limited | scope_missing | failing | awaiting_approval. */
    val health: String = "",
    val error: String? = null,
    @SerialName("granted_scopes") val grantedScopes: List<String>? = null,
    /** RFC 3339 times; absent when never. */
    @SerialName("authorized_at") val authorizedAt: String? = null,
    /** Days left of the authorization; absent when unknown. */
    @SerialName("reauth_due_days") val reauthDueDays: Int? = null,
    @SerialName("last_run_at") val lastRunAt: String? = null,
    @SerialName("next_run_at") val nextRunAt: String? = null,
    @SerialName("last_messages") val lastMessages: Int? = null,
    @SerialName("last_docs") val lastDocs: Int? = null,
    val running: Boolean = false,
)

@Serializable data class LarkSyncView(
    val available: Boolean = false,
    /** The hub's local hour the daily run starts at. */
    @SerialName("schedule_hour") val scheduleHour: Int = 3,
    val accounts: List<LarkSyncAccount> = emptyList(),
    val bots: List<LarkSyncBot> = emptyList(),
    val bindings: List<LarkSyncBinding> = emptyList(),
)

/** `POST …/sync/bots`'s answer: the new bot and its first registration step. */
@Serializable data class LarkSyncCreated(@SerialName("bot_id") val botId: String = "", val prompt: BindPrompt = BindPrompt())

/**
 * How the hub lets the owner authorize: a browser redirect ([url]), a code on screen ([prompt], the device flow), or
 * the [redirectUri] to add by hand in the app's console ([consoleUrl]) first, when the hub could not configure it.
 */
@Serializable data class LarkSyncAuthorize(
    val mode: String = "",
    val url: String? = null,
    val prompt: BindPrompt? = null,
    @SerialName("redirect_uri") val redirectUri: String? = null,
    @SerialName("console_url") val consoleUrl: String? = null,
)

/** Each call made on what [source] gives at the time: the selected hub's sync, chosen again for every request. */
class LarkSyncThrough(private val source: suspend () -> LarkSyncSettings) : LarkSyncSettings {
    override suspend fun view() = source().view()
    override suspend fun setSchedule(hour: Int) = source().setSchedule(hour)
    override suspend fun createBot(label: String, tier: String, region: String) = source().createBot(label, tier, region)
    override suspend fun register(botId: String, step: String) = source().register(botId, step)
    override suspend fun addScopes(botId: String, tier: String) = source().addScopes(botId, tier)
    override suspend fun removeBot(botId: String) = source().removeBot(botId)
    override suspend fun authorize(botId: String, redirectUri: String, tier: String?) = source().authorize(botId, redirectUri, tier)
    override suspend fun pollAuthorize(botId: String) = source().pollAuthorize(botId)
    override suspend fun finishOAuth(state: String, code: String) = source().finishOAuth(state, code)
    override suspend fun setBindingEnabled(key: String, enabled: Boolean) = source().setBindingEnabled(key, enabled)
    override suspend fun removeBinding(key: String) = source().removeBinding(key)
    override suspend fun runBinding(key: String) = source().runBinding(key)
    override suspend fun oauthRedirectUri() = source().oauthRedirectUri()
}
