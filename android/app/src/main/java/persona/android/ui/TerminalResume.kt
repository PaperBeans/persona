package persona.android.ui

import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentCreationService
import persona.core.contracts.api.AgentStartRequest
import persona.core.contracts.api.AgentStartResult
import persona.core.domain.sort.hasSource
import persona.feature.terminal.TerminalResumeFailed
import persona.feature.terminal.TerminalResumer
import java.util.UUID

/**
 * Resume session on the terminal of a session that isn't running (#4345), as on iOS (#4328): `POST /api/v1/sessions` with
 * `resume_session_id` and what the session ran with; a node from #4328 on fills in what is left out.
 */
internal class SessionTerminalResumer(
    private val sessions: () -> List<Session>,
    private val creation: () -> AgentCreationService?,
) : TerminalResumer {
    override fun canResume(key: SessionKey): Boolean =
        creation() != null && sessions().firstOrNull { it.key == key }?.let(::resumableInTerminal) == true

    override suspend fun resume(key: SessionKey) {
        val session = checkNotNull(sessions().firstOrNull { it.key == key })
        val request = AgentStartRequest(key.machineId, session.cwd, session.launcher.orEmpty(),
            permissionMode = session.permissionMode, effort = session.effort, resumeSessionId = key.sessionId)
        when (val result = checkNotNull(creation()).start(request, UUID.randomUUID().toString())) {
            is AgentStartResult.Created -> Unit
            is AgentStartResult.Rejected -> throw TerminalResumeFailed(result.message.shown)
            AgentStartResult.Pending, AgentStartResult.Unknown -> error("No answer from the hub")
        }
    }
}

/** A Claude Code session in a terminal engine that isn't being started; others (Codex) resume with their next message. */
internal fun resumableInTerminal(session: Session): Boolean = !session.hasSource && !session.assistant && !session.starting &&
    session.cwd.isNotBlank() && (session.agent.isNullOrEmpty() || session.agent == "claude")
