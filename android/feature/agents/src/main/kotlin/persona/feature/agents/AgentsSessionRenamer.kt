package persona.feature.agents

import dagger.Module
import dagger.Provides
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.SessionsRepository
import persona.core.contracts.api.AgentOperationFailure
import persona.core.domain.sort.SessionRename
import persona.core.domain.sort.SessionRenamer
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Singleton

private data class PendingRenames(val scope: String = "", val edits: Map<SessionKey, SessionRename> = emptyMap())

/** One shared optimistic projection, isolated by pairing and reconciled against authoritative sessions. */
internal class AgentsSessionRenamer(
    private val wiring: AgentsWiring,
    private val repository: SessionsRepository,
) : SessionRenamer {
    private val lock = Mutex()
    private val pending = MutableStateFlow(PendingRenames())
    private val uncertain = mutableMapOf<Pair<String, SessionKey>, SessionRename>()
    private fun reconcile(source: List<Session>, scope: String) {
        pending.update { current ->
            if (current.scope != scope) current else current.copy(edits = current.edits.filterNot { (key, edit) ->
                source.firstOrNull { it.key == key }?.let(edit::matches) == true
            })
        }
    }
    override fun project(source: Flow<List<Session>>) = combine(source, wiring.state, pending) { source, bindings, edits ->
        if (edits.scope != bindings.scope) source else {
            reconcile(source, bindings.scope)
            source.map { session -> edits.edits[session.key]?.apply(session) ?: session }
        }
    }

    override suspend fun rename(session: Session, title: String?, tabName: String?): Boolean =
        applyRename(session, SessionRename.changes(session, title, tabName))

    override suspend fun renameUnified(session: Session, name: String): Boolean =
        applyRename(session, SessionRename.unifiedChanges(session, name))

    private suspend fun applyRename(session: Session, edit: SessionRename): Boolean = lock.withLock {
        if (!edit.changed) return@withLock false
        val bindings = wiring.state.value
        val api = bindings.operations?.operations ?: throw OperationsMissing()
        val operation = bindings.scope to session.key
        uncertain[operation]?.let { previous ->
            repository.refresh()
            if (repository.sessions.value.firstOrNull { it.key == session.key }?.let(previous::matches) != true) throw RenameUnconfirmed()
            uncertain.remove(operation)
            if (edit == previous) return@withLock true
        }
        val before = pending.value.takeIf { it.scope == bindings.scope } ?: PendingRenames(bindings.scope)
        val previous = before.edits[session.key]
        val combined = SessionRename(edit.title ?: previous?.title, edit.tabName ?: previous?.tabName)
        pending.value = before.copy(edits = before.edits + (session.key to combined))
        try {
            api.renameSession(session.key, edit.title, edit.tabName)
        } catch (error: Exception) {
            pending.value = before
            if (error is CancellationException || (error as? AgentOperationFailure)?.uncertain == true)
                uncertain[operation] = edit
            throw error
        } finally {
            // Refresh failures must not turn an acknowledged rename into a duplicate write.
            try {
                repository.refresh()
                // A screen may have left while the request was in flight, leaving no projection collector.
                reconcile(repository.sessions.value, bindings.scope)
            }
            catch (error: CancellationException) { throw error }
            catch (_: Exception) { /* Keep the confirmed local name until a later snapshot arrives. */ }
        }
        true
    }
}

@Module
@InstallIn(SingletonComponent::class)
object AgentsRenameModule {
    @Provides
    @Singleton
    fun renamer(wiring: AgentsWiring, repository: SessionsRepository): SessionRenamer = AgentsSessionRenamer(wiring, repository)
}

@EntryPoint
@InstallIn(SingletonComponent::class)
interface AgentsRenameDependencies {
    fun renamer(): SessionRenamer
}

/**
 * Session actions are not wired for this hub yet; the list says so in its language (AgentsViewModel). The message is
 * what the chat's menu shows through operationFeedback, which does not know this type.
 */
internal class OperationsMissing : IllegalStateException("会话操作尚未接入") // i18n-ignore: chat's operationFeedback fallback text
/** The last rename may or may not have reached the hub; the list asks to refresh and check (AgentsViewModel). */
internal class RenameUnconfirmed : IllegalStateException("上次重命名结果待确认，请刷新后核对") // i18n-ignore: chat's operationFeedback fallback text
