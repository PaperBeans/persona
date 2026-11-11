package persona.core.data

import persona.core.contracts.CoreText
import persona.core.contracts.CoreText.Key
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import java.util.logging.Level
import java.util.logging.Logger

class RealAssistantRepository(
    private val connection: InboxAssistantConnection,
    private val sessions: SessionsRepository,
    scope: CoroutineScope,
    private val snapshots: ListSnapshotStore? = null,
    private val preferences: HubPreferences? = null,
) : AssistantConversations {
    private val current = MutableStateFlow(AssistantSnapshot())
    override val assistant = current.asStateFlow()
    private val currentPins = MutableStateFlow<Set<SessionKey>>(emptySet())
    override val pinned = currentPins.asStateFlow()
    private val lock = Mutex()
    private val creationLock = Mutex()
    private var restoredScope: String? = null
    private val cached = mutableMapOf<String, AssistantSnapshot>()
    private var refreshVersion = 0L

    private data class Request(
        val transport: InboxAssistantTransport,
        val choice: AssistantManagerChoice.Choice,
    ) {
        val manager get() = choice.manager
        val owner get() = choice.shown
        // Preserve the builtin assistant's existing disk snapshot; all other owners have explicit identities.
        val key get() = "${transport.scope}:assistant" + if (owner == ManagerRoster.ASSISTANT_MANAGER) "" else "@$owner"
    }

    private fun request(): Request = Request(
        requireNotNull(connection.current.value) { CoreText.of(Key.PairHubFirst).shown },
        AssistantManagerChoice.current.value,
    )

    private fun selected(request: Request): Boolean = connection.current.value === request.transport &&
        AssistantManagerChoice.current.value == request.choice

    private fun decode(data: JsonObject, request: Request): AssistantSnapshot {
        val machine = data.string("machine_id")
        val main = data.string("main")
        val conversations = data.array("conversations").map { JsonCodec.decodeFromJsonElement<Session>(it) }
        (conversations.map { it.sessionId } + main).filter { it.isNotEmpty() }
            .forEach { noteConversation(it, request) }
        return AssistantSnapshot(
            if (machine.isNotEmpty() && main.isNotEmpty()) SessionKey(machine, main) else null,
            conversations, request.manager, request.owner,
            leaders = leadersOf(data),
        )
    }

    /** The directions' names for [leaders] (none without leaders); a hub that cannot list them keeps [held]. */
    private suspend fun directionNames(request: Request, leaders: List<String>, held: Map<String, String>): Map<String, String> {
        if (leaders.isEmpty()) return emptyMap()
        return try {
            directionNamesOf(request.transport.assistantLeadersOf(request.owner))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            held
        }
    }

    private fun noteConversation(id: String, request: Request) {
        AssistantManagerChoice.noteItem(listOf("assistant", "conversations", id), request.choice.shown, request.choice.hubId)
    }

    private suspend fun restore(request: Request) {
        if (!selected(request) || restoredScope == request.key) return
        // The selected page changes before disk or network work, so old conversations cannot stay actionable.
        restoredScope = request.key
        current.value = cached[request.key]?.copy(manager = request.manager) ?: AssistantSnapshot(manager = request.manager, managerId = request.owner)
        currentPins.value = preferences?.preferences?.value?.pins?.sessions?.mapNotNull(::sessionPath)?.toSet().orEmpty()
        if (request.key !in cached) {
            val data = snapshots.restore<JsonObject>(request.key)
            if (data != null && selected(request)) {
                val snapshot = decode(data, request)
                cached[request.key] = snapshot
                current.value = snapshot
            }
        }
    }

    init {
        if (preferences != null) scope.launch {
            preferences.preferences.collect { state ->
                currentPins.value = state.pins.sessions.mapNotNull(::sessionPath).toSet()
            }
        }
        scope.launch {
            var refreshJob: Job? = null
            combine(connection.current, AssistantManagerChoice.current) { transport, choice ->
                transport?.let { Request(it, choice) }
            }.collect { request ->
                refreshJob?.cancel()
                if (request == null) lock.withLock {
                    restoredScope = null
                    current.value = AssistantSnapshot()
                    currentPins.value = emptySet()
                } else {
                    lock.withLock { restore(request) }
                    refreshJob = launch {
                        // Refresh errors leave the selected manager's cached page available offline.
                        try {
                            refresh(request)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            Logger.getLogger(RealAssistantRepository::class.java.name).log(
                                Level.WARNING, "Assistant refresh failed; retaining cached conversations", e,
                            )
                        }
                    }
                }
            }
        }
        scope.launch {
            sessions.sessions.collect { updates ->
                lock.withLock {
                    val transport = connection.current.value ?: return@withLock
                    val choice = AssistantManagerChoice.current.value
                    val request = Request(transport, choice)
                    if (restoredScope != request.key || current.value.managerId != request.owner) return@withLock
                    val owned = updates.filter { choice.owns(it.manager) }
                    val byKey = owned.associateBy { it.key }
                    current.update { snapshot ->
                        snapshot.copy(conversations = (snapshot.conversations.map { byKey[it.key] ?: it } +
                            owned.filter { it.assistant == true }).distinctBy { it.key })
                    }
                    cached[request.key] = current.value
                }
            }
        }
    }

    override suspend fun refresh() = refresh(request())

    private suspend fun refresh(request: Request) {
        val version = lock.withLock {
            if (!selected(request)) return
            restore(request)
            ++refreshVersion
        }
        // Network calls never hold the page/cache mutex: a slow manager cannot block switching away.
        val data = request.transport.assistantOf(request.owner)
        // the names are asked for again only when the leaders or their titles change (as the web does)
        val fresh = decode(data, request)
        val held = current.value.takeIf { it.managerId == request.owner }
        fun AssistantSnapshot.leaderTitles() = leaders.map { id -> conversations.firstOrNull { it.sessionId == id }?.title }
        val names = if (held != null && held.directionNames.isNotEmpty() && held.leaders == fresh.leaders &&
            held.leaderTitles() == fresh.leaderTitles()) held.directionNames
            else directionNames(request, fresh.leaders, held?.directionNames.orEmpty())
        val pins = if (preferences != null) preferences.preferences.value.pins.sessions.mapNotNull(::sessionPath).toSet()
            else request.transport.pins().array("sessions").mapNotNull { (it as? JsonPrimitive)?.contentOrNull?.let(::sessionPath) }.toSet()
        lock.withLock {
            if (!selected(request) || version != refreshVersion) return
            val snapshot = fresh.copy(directionNames = names)
            cached[request.key] = snapshot
            current.value = snapshot
            currentPins.value = pins
            snapshots.save(request.key, data)
        }
    }

    override suspend fun startConversation(): SessionKey = start(null)

    override suspend fun startConversation(launcher: String): SessionKey = start(launcher)

    override suspend fun mainConversationOf(manager: String): SessionKey {
        val captured = request()
        val request = captured.copy(choice = captured.choice.copy(
            manager = manager.takeUnless { it == captured.choice.defaultId },
        ))
        return creationLock.withLock {
            val config = request.transport.assistantOf(manager)
            check(connection.current.value === request.transport) { CoreText.of(Key.ConnectionSwitched).shown }
            val machine = config.string("machine_id")
            val main = config.string("main")
            if (machine.isNotEmpty() && main.isNotEmpty()) {
                noteConversation(main, request)
                return@withLock SessionKey(machine, main)
            }
            create(request, true, null, config)
        }
    }

    private suspend fun start(launcher: String?): SessionKey {
        // Capture identity before waiting behind another creation.
        val request = request()
        return creationLock.withLock {
            val config = request.transport.assistantOf(request.owner)
            check(connection.current.value === request.transport) { CoreText.of(Key.ConnectionSwitched).shown }
            create(request, config.string("main").isEmpty(), launcher, config)
        }
    }

    private suspend fun create(request: Request, isMain: Boolean, launcher: String?, config: JsonObject): SessionKey {
        val result = request.transport.createConversationOf(request.owner, isMain, launcher)
        val session = JsonCodec.decodeFromJsonElement<Session>(result.getValue("session"))
        check(connection.current.value === request.transport) { CoreText.of(Key.ConnectionSwitched).shown }
        noteConversation(session.sessionId, request)
        lock.withLock {
            val previous = cached[request.key]?.copy(manager = request.manager) ?: decode(config, request)
            val snapshot = previous.copy(
                mainSession = if (isMain) session.key else previous.mainSession,
                conversations = (listOf(session) + previous.conversations).distinctBy { it.key },
            )
            cached[request.key] = snapshot
            if (selected(request)) {
                ++refreshVersion
                current.value = snapshot
            }
            snapshots.save(request.key, buildJsonObject {
                put("machine_id", snapshot.mainSession?.machineId ?: session.machineId)
                put("main", snapshot.mainSession?.sessionId.orEmpty())
                put("conversations", JsonArray(snapshot.conversations.map { JsonCodec.encodeToJsonElement(it) }))
            })
        }
        return session.key
    }

}

/** `assistant.leaders`: the leader conversations' ids in the hub's order, never the main one. */
internal fun leadersOf(data: JsonObject): List<String> {
    val main = data.string("main")
    return data.array("leaders").mapNotNull { (it as? JsonPrimitive)?.contentOrNull?.takeIf { id -> id.isNotEmpty() && id != main } }
}

/** `GET /api/v1/assistant/leaders`'s names by leader conversation id: current directions with a name only. */
internal fun directionNamesOf(data: JsonObject): Map<String, String> = data.array("leaders").mapNotNull { it as? JsonObject }
    .filter { (it["ended"] as? JsonPrimitive)?.booleanOrNull != true }
    .mapNotNull { l -> l.string("id").takeIf { it.isNotEmpty() }?.let { id -> l.string("name").trim().takeIf { it.isNotEmpty() }?.let { id to it } } }
    .toMap()

internal fun JsonObject.string(key: String): String = (this[key] as? JsonPrimitive)?.contentOrNull.orEmpty()
internal fun JsonObject.optional(key: String): String? = string(key).takeIf { it.isNotEmpty() }
internal fun JsonObject.array(key: String): JsonArray = this[key] as? JsonArray ?: JsonArray(emptyList())
internal fun JsonObject.obj(key: String): JsonObject? = this[key] as? JsonObject
internal fun JsonObject.flag(key: String): Boolean = (this[key] as? JsonPrimitive)?.booleanOrNull == true
internal fun JsonObject.number(key: String): Long? = (this[key] as? JsonPrimitive)?.longOrNull

private fun sessionPath(path: String): SessionKey? = path.split('/', limit = 2).let {
    if (it.size == 2) SessionKey(it[0], it[1]) else null
}
