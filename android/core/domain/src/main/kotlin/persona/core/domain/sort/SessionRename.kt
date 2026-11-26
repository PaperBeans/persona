package persona.core.domain.sort

import persona.core.contracts.Session
import kotlinx.coroutines.flow.Flow

/** A blank field leaves its remote value untouched. Tab names require an actual herdr pane. */
data class SessionRename(val title: String? = null, val tabName: String? = null) {
    val changed: Boolean get() = title != null || tabName != null
    fun apply(session: Session): Session = session.copy(title = title ?: session.title, slotName = tabName ?: session.slotName)
    fun matches(session: Session): Boolean = (title == null || session.title == title) &&
        (tabName == null || session.slotName == tabName)

    companion object {
        fun changes(session: Session, title: String?, tabName: String?, unified: Boolean = false): SessionRename {
            if (unified) return unifiedChanges(session, tabName ?: title)
            return SessionRename(
                title?.trim()?.takeIf { it.isNotEmpty() && it != session.title },
                tabName?.trim()?.takeIf { !session.paneId.isNullOrBlank() && it.isNotEmpty() && it != session.slotName },
            )
        }

        /**
         * Unified rename when "Use the herdr tab name as the session name" is enabled (#13899, web `renameBody`):
         * renames the herdr tab and, for Claude Code or Codex sessions, also updates the CLI session title.
         */
        fun unifiedChanges(session: Session, name: String?): SessionRename {
            val next = name?.trim()?.takeIf(String::isNotEmpty) ?: return SessionRename()
            val tab = session.herdrTabName ?: return SessionRename()
            val cli = session.agent?.takeIf(String::isNotEmpty) ?: "claude"
            val syncTitle = cli == "claude" || cli == "codex"
            if (next == tab && (!syncTitle || next == session.title)) return SessionRename()
            return SessionRename(title = if (syncTitle) next else null, tabName = next)
        }
    }
}

/** Shared by the Agents leaves, space detail and chat title for the active pairing. */
interface SessionRenamer {
    fun project(source: Flow<List<Session>>): Flow<List<Session>>
    suspend fun rename(session: Session, title: String?, tabName: String?): Boolean
    suspend fun renameUnified(session: Session, name: String): Boolean {
        val edit = SessionRename.unifiedChanges(session, name)
        return if (edit.changed) rename(session, edit.title, edit.tabName) else false
    }
}
