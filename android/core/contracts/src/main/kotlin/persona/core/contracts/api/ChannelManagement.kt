package persona.core.contracts.api

import persona.core.contracts.CoreTextException
import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * Settings › Extensions › Channels (docs/extensions/channels.md "API" "UI"): every way of talking to Persona — its own apps, the Feishu bot,
 * WeChat, /agent in Feishu, voice, the chat app bots extensions contribute — with each one's switch, its binding (a step
 * at a time), where its messages go, what it does and a test message. Bound to the active pairing's selected hub.
 */
interface ChannelManagement {
    /** Whether the selected hub has channels (`channels` in its features); the settings row shows only then. */
    val available: Flow<Boolean>

    /**
     * The assistant's conversations of the selected hub, in that hub's own ids: what "Send messages to" can pick. Empty
     * where there is no hub to ask.
     */
    val conversations: Flow<List<Session>> get() = flowOf(emptyList())

    /** `GET /api/v1/channels`; a hub without the feature is a [ChannelsUnsupported]. */
    suspend fun channels(): List<HubChannel>

    /** `GET /api/v1/channels/{id}`. */
    suspend fun channel(id: String): ChannelDetail

    /** `PUT /api/v1/channels/{id}` with only what [change] holds; a value the hub refuses is a [ChannelRejected]. */
    suspend fun update(id: String, change: ChannelChange): ChannelDetail

    /** `POST /api/v1/channels/{id}/bind`: [step] null starts over; the answer is the next step, or done. */
    suspend fun bind(id: String, step: String? = null, values: Map<String, String>? = null): BindPrompt

    /** `DELETE /api/v1/channels/{id}/binding`. */
    suspend fun unbind(id: String): ChannelDetail

    /** `POST /api/v1/channels/{id}/test`: a line to the bound owner only. */
    suspend fun test(id: String)
}

/** The channels the conversations know (names and marks for "From WeChat"), cached per hub and read again on reconnect. */
interface ChannelDirectory {
    val channels: StateFlow<List<HubChannel>>
}

/** The hub has no channels (404 on the list: a hub from before them). */
class ChannelsUnsupported : CoreTextException(CoreText.of(Key.ChannelsUnsupported))

/** The hub has no channel of that id (404 `not_found`): gone with its extension. */
class ChannelNotFound : CoreTextException(CoreText.of(Key.ChannelNotFound))

/** The hub refused the change (400 `invalid`): [message] says why, for people. */
class ChannelRejected(message: String) : Exception(message)

/** The channel is set up elsewhere (409 `read_only`): hub.json, its extension's page. */
class ChannelReadOnly(message: String) : Exception(message)

/** `PUT /api/v1/channels/{id}`: null and empty are left out, so only what changed is sent. */
data class ChannelChange(
    val enabled: Boolean? = null,
    val target: ChannelTarget? = null,
    val features: Map<String, Boolean> = emptyMap(),
)

/** One of `GET /api/v1/channels` (`channel.View`). */
@Serializable
data class HubChannel(
    /** Also the prefix of the `client_msg_id` of every message it sends into a conversation (`wechat-…`). */
    val id: String = "",
    val name: String = "",
    /** `app | im | mention | voice`. */
    val kind: String = "",
    /** `builtin | plugin | market` (the hub's words: `plugin` is an extension's contribution). */
    val source: String = "",
    /** The extension that contributes it (or would, from a market). */
    @SerialName("plugin") val extension: String? = null,
    /** For a market's: the market's id. */
    val market: String? = null,
    val installed: Boolean = false,
    val enabled: Boolean = false,
    /** `connected | not_connected | error | disabled | not_installed`. */
    val state: String = "",
    val error: String? = null,
    val account: ChannelAccount? = null,
    val description: String? = null,
    val capabilities: ChannelCapabilities = ChannelCapabilities(),
    val target: ChannelTarget? = null,
    val features: List<ChannelFeature> = emptyList(),
    /** `none | qr | code | fields | config`. */
    val bind: String = "none",
    /** The app channel's ends (iPhone, Mac, …), for people. */
    val surfaces: List<String> = emptyList(),
    @SerialName("last_event_at") val lastEventAt: String? = null,
    /** Its settings are elsewhere (hub.json, the extension's page): only shown here. */
    @SerialName("read_only") val readOnly: Boolean = false,
) {
    val label: String get() = name.ifBlank { id }
}

/** Who a channel is bound to, for people only (never a phone number or an address). */
@Serializable
data class ChannelAccount(val id: String? = null, val name: String = "")

/** What a channel can do (docs/extensions/channels.md "Capabilities"); taking text is a given. */
@Serializable
data class ChannelCapabilities(
    @SerialName("images_in") val imagesIn: Boolean = false,
    @SerialName("files_in") val filesIn: Boolean = false,
    @SerialName("voice_in") val voiceIn: Boolean = false,
    /** `markdown | plain`. */
    val format: String? = null,
    /** Characters a message may hold; 0 is no limit. */
    @SerialName("max_text") val maxText: Int = 0,
    /** `none | text | buttons`. */
    val asks: String? = null,
    val push: Boolean = false,
    val typing: Boolean = false,
    val progress: Boolean = false,
    val threads: Boolean = false,
)

/**
 * Where a channel's messages go: `main` (the main conversation), `new` (the next message opens one, which becomes the
 * target), `conversation` (one of the assistant's, by [machineId] and [sessionId]), `agent` (the Feishu bot's /agents).
 */
@Serializable
data class ChannelTarget(
    val kind: String = "main",
    @SerialName("machine_id") val machineId: String? = null,
    @SerialName("session_id") val sessionId: String? = null,
    /** The hub's name for it, on the way to the app only. */
    val title: String? = null,
) {
    companion object {
        val Main = ChannelTarget("main")
        val New = ChannelTarget("new")
        fun conversation(machineId: String, sessionId: String) = ChannelTarget("conversation", machineId, sessionId)
    }
}

/** One of a channel's switches; the app draws every one it is given, known or not. */
@Serializable
data class ChannelFeature(val id: String = "", val title: String = "", val description: String? = null, val on: Boolean = false)

/** How many answers and questions a channel waits on. */
@Serializable
data class ChannelWaiting(val answers: Int = 0, val asks: Int = 0)

/** A conversation the channel's messages went to lately. */
@Serializable
data class ChannelConversation(
    @SerialName("machine_id") val machineId: String = "",
    @SerialName("session_id") val sessionId: String = "",
    val title: String? = null,
    val at: String? = null,
)

/** `GET /api/v1/channels/{id}` (`channel.Detail`): the channel with more keys beside it, in one object. */
data class ChannelDetail(
    val channel: HubChannel,
    val waiting: ChannelWaiting = ChannelWaiting(),
    val conversations: List<ChannelConversation> = emptyList(),
    /** The channel's own full page: an app route (`settings/extensions/<id>`, `settings/model-config`), not a web address. */
    val settingsUrl: String? = null,
) {
    @Serializable
    private data class Extra(
        val waiting: ChannelWaiting = ChannelWaiting(),
        val conversations: List<ChannelConversation> = emptyList(),
        @SerialName("settings_url") val settingsUrl: String? = null,
    )

    companion object {
        fun decode(json: JsonElement): ChannelDetail {
            val channel = JsonCodec.decodeFromJsonElement(HubChannel.serializer(), json)
            val extra = JsonCodec.decodeFromJsonElement(Extra.serializer(), json)
            return ChannelDetail(channel, extra.waiting, extra.conversations, extra.settingsUrl?.takeIf { it.isNotBlank() })
        }
    }
}

/**
 * Where binding stands (`connector.Prompt`, docs/extensions/connectors.md): fields to fill in ([kind] empty), a QR code to scan
 * with the chat app (`qr`, drawn from [qr]), a code to enter on a page (`device_code`) or to send to the bot from the
 * chat app (`pair_code`); for those three the app asks the same [step] again every [interval] seconds until the
 * answer is another step or [done].
 */
@Serializable
data class BindPrompt(
    val step: String? = null,
    val fields: List<BindField> = emptyList(),
    val kind: String? = null,
    val url: String? = null,
    @SerialName("user_code") val userCode: String? = null,
    val interval: Int = 0,
    val qr: BindQrCode? = null,
    val hint: String? = null,
    /** Where what the step asks is done, opened outside the app (the Lark bot's registration page), and its label. */
    val link: String? = null,
    @SerialName("link_text") val linkText: String? = null,
    /** What was wrong with the values just given: the same step is asked again. */
    val error: String? = null,
    val done: Boolean = false,
    /** Who is bound, once [done]. */
    val account: String? = null,
) {
    /**
     * A step the app asks again by itself (a code on screen), not one with fields. Only by its [kind]: the pages do not
     * use it; they poll by `StepView` (feature/settings `SignInSteps.kt`), which also leaves out a step of a known kind
     * that came without what it shows (#4488). Kept for older callers.
     */
    val polled: Boolean get() = !done && kind in setOf("qr", "pair_code", "device_code")
}

/** One thing a bind step asks for; [secret] ones are typed in hidden. */
@Serializable
data class BindField(val name: String = "", val label: String = "", val secret: Boolean = false, val placeholder: String? = null)

/** A QR code's modules: [size] rows of [size] "0" / "1", without the quiet zone; the app draws them. */
@Serializable
data class BindQrCode(val size: Int = 0, val rows: List<String> = emptyList())

/** Which channel a message or a conversation came from (docs/extensions/channels.md "Source markers"). */
object ChannelSources {
    /**
     * The channel a message came from, by its `client_msg_id`: `<channel id>-<message id>`. The app's own messages
     * have UUIDs (eight hex digits first, never a channel id) and the app channel itself is never marked.
     */
    fun of(clientMsgId: String?, channels: List<HubChannel>): HubChannel? {
        val dash = clientMsgId?.indexOf('-') ?: return null
        if (dash <= 0) return null
        val head = clientMsgId.substring(0, dash)
        return channels.firstOrNull { it.id == head && it.kind != "app" }
    }

    /** The latest channel of a conversation ([Session.channels], newest first), when the hub lists it. */
    fun latest(ids: List<String>, channels: List<HubChannel>): HubChannel? {
        val id = ids.firstOrNull { it.isNotBlank() } ?: return null
        return channels.firstOrNull { it.id == id && it.kind != "app" }
    }

    /** Several hubs' lists as one: the first one's channel wins for an id they share. */
    fun merge(lists: List<List<HubChannel>>): List<HubChannel> = lists.flatten().distinctBy { it.id }
}
