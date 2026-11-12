package persona.feature.assistant

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.AssistantRepository
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

/**
 * What the conversation history keeps of its own edits, for the Persona page's list and the app's sidebar alike (iOS
 * `AssistantSidebar`): the titles renamed here, shown until the hub's list carries them, and the archived conversations as
 * last read, which "Archived (N)" counts and the Archived sheet lists, so the count is what the sheet shows.
 */
@Stable
internal class ConversationHistory(private val management: AssistantManagement?, private val repository: AssistantRepository) {
    /** The archived conversations as the hub last listed them; null before the first read and where the hub can't list them. */
    var archived: List<Session>? by mutableStateOf(null)
        private set
    /** Titles renamed here, by conversation, until the hub's list has them. */
    var titles: Map<SessionKey, String> by mutableStateOf(emptyMap())
        private set
    /** Brought back here and not confirmed yet: off the archived list at once. */
    private var restoring: Set<SessionKey> by mutableStateOf(emptySet())
    /** The conversation the rename dialog is open on. */
    var renaming: Session? by mutableStateOf(null)
    var archivedOpen: Boolean by mutableStateOf(false)

    /** The archived conversations to show: the last read, less those being brought back and those [open] lists again. */
    fun shownArchived(open: Set<SessionKey>): List<Session> =
        archived.orEmpty().filter { it.key !in restoring && it.key !in open }

    /** [sessions] with the titles renamed here. */
    fun titled(sessions: List<Session>): List<Session> =
        if (titles.isEmpty()) sessions else sessions.map { s -> titles[s.key]?.let { s.copy(title = it) } ?: s }

    /** Forgets the titles the hub's [sessions] carry now, so a later rename (by Persona itself) shows. */
    fun settle(sessions: List<Session>) {
        if (titles.isEmpty()) return
        val now = sessions.associate { it.key to it.title }
        titles.filter { (key, title) -> now[key] == title }.keys.takeIf { it.isNotEmpty() }?.let { titles = titles - it }
    }

    /** Reads the archived conversations again; a failed read keeps the list shown, a hub that can't list them has none. */
    suspend fun loadArchived() {
        val api = management ?: return
        archived = try {
            api.archivedConversations()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: UnsupportedOperationException) {
            null
        } catch (_: Exception) {
            archived
        }
    }

    /** The hub's rename, then the list read again; the new title shows until that list carries it. */
    suspend fun rename(key: SessionKey, title: String): Result<Unit> {
        val result = assistantAttempt { requireNotNull(management).renameConversation(key, title) }
        if (result.isSuccess) {
            titles = titles + (key to title)
            assistantAttempt { repository.refresh() }
        }
        return result
    }

    /** Off the archived list at once; back on it when the hub refuses. */
    suspend fun unarchive(key: SessionKey): Result<Unit> {
        restoring = restoring + key
        val result = assistantAttempt { requireNotNull(management).unarchiveConversation(key) }
        if (result.isSuccess) {
            assistantAttempt { repository.refresh() }
            loadArchived()
        }
        restoring = restoring - key
        return result
    }
}

/** The conversations [conversations] lists open (not archived): an archived one listed again has been brought back. */
internal fun openKeys(conversations: List<Session>): Set<SessionKey> = conversations.filter { !it.archived }.mapTo(HashSet()) { it.key }

@Composable
internal fun rememberConversationHistory(management: AssistantManagement?, repository: AssistantRepository): ConversationHistory =
    remember(management, repository) { ConversationHistory(management, repository) }

/**
 * The history's rename dialog and Archived sheet (iOS `renameSessionAlert`, `ArchivedConversationsView`), and the reads
 * that keep "Archived (N)" current: when the list shows, and whenever a conversation joins or leaves it. [conversations]
 * is the list as the hub has it; [onOpen] opens an archived one; [notice] and [failure] say how an edit went.
 */
@Composable
internal fun ConversationHistoryDialogs(
    history: ConversationHistory, conversations: List<Session>, scope: CoroutineScope, onOpen: (SessionKey) -> Unit,
    notice: (String) -> Unit, failure: (Exception) -> Unit,
) {
    val resources = androidx.compose.ui.platform.LocalResources.current
    val haptics = rememberHaptics()
    val open = remember(conversations) { openKeys(conversations) }
    LaunchedEffect(history, open) { history.loadArchived() }
    LaunchedEffect(history, conversations) { history.settle(conversations) }
    history.renaming?.let { session ->
        RenameConversationDialog(session, onDismiss = { history.renaming = null }) { title ->
            history.renaming = null
            scope.launch {
                history.rename(session.key, title)
                    .onSuccess { notice(resources.getString(R.string.assistant_renamed)) }
                    .onFailure { (it as? Exception)?.let(failure) }
            }
        }
    }
    if (history.archivedOpen) ArchivedConversationsSheet(history.shownArchived(open),
        onDismiss = { history.archivedOpen = false },
        onOpen = { history.archivedOpen = false; onOpen(it.key) },
        onUnarchive = { session ->
            // As iOS: a tap now, "Unarchived" once the hub has it (DESIGN_SYSTEM.md 4.3).
            haptics(Haptic.Tap)
            scope.launch {
                history.unarchive(session.key)
                    .onSuccess { notice(resources.getString(R.string.assistant_unarchived)) }
                    .onFailure { (it as? Exception)?.let(failure) }
            }
        })
}

/**
 * Rename… (iOS `RenameSessionAlert` for a conversation with no tab): one name, the conversation's title (empty while it
 * has only Claude Code's placeholder), saved when it is not empty and differs.
 */
@Composable
internal fun RenameConversationDialog(session: Session, onDismiss: () -> Unit, onSave: (String) -> Unit) {
    var name by rememberSaveable(session.key.path) { mutableStateOf(session.title.takeUnless { it == "Claude Code" }.orEmpty()) }
    val title = name.trim().takeIf { it.isNotEmpty() && it != session.title.trim() }
    JDialog(onDismiss = onDismiss, title = stringResource(R.string.assistant_rename_title),
        confirmEnabled = title != null,
        confirm = stringResource(R.string.assistant_save) to { title?.let(onSave) },
        dismiss = stringResource(R.string.assistant_cancel) to onDismiss) {
        JTextField(name, { name = it }, Modifier.fillMaxWidth().testTag("rename-conversation"), singleLine = true,
            label = stringResource(R.string.assistant_rename_field))
    }
}

/**
 * Archived (iOS `ArchivedConversationsView`): the conversations archived from the list (or by Persona), newest first. A tap
 * opens one; a swipe left, its Unarchive or a long press brings it back into the list. Nothing here deletes. The rows are
 * the Agents archive's (`AgentArchivesScreen`): the title, when, and the action at the end.
 */
@Composable
internal fun ArchivedConversationsSheet(
    sessions: List<Session>, onDismiss: () -> Unit, onOpen: (Session) -> Unit, onUnarchive: (Session) -> Unit,
) {
    val words = conversationWords()
    val locale = currentLocale
    val unarchive = stringResource(R.string.assistant_unarchive)
    var menu by remember { mutableStateOf<Session?>(null) }
    JSheet(onDismiss, title = stringResource(R.string.assistant_archived)) {
        if (sessions.isEmpty()) JEmptyState(stringResource(R.string.assistant_archived_empty),
            stringResource(R.string.assistant_archived_empty_detail), icon = PersonaIcons.archive)
        else LazyColumn(Modifier.weight(1f, fill = false).testTag("archived-conversations")) {
            items(sessions, key = { it.key.path }) { session ->
                JSwipeActions(end = SwipeAction(unarchive, PersonaIcons.unarchive, Status.Working, { onUnarchive(session) })) {
                    JRow(session.conversationTitle(words), subtitle = conversationTime(session.updated, locale),
                        onClick = { onOpen(session) }, onLongClick = { menu = session }, trailing = {
                            JButton(unarchive, { onUnarchive(session) }, kind = JButtonKind.Quiet, size = JButtonSize.Small)
                        })
                }
            }
        }
    }
    menu?.let { session ->
        JActionSheet(listOf(JAction(unarchive, PersonaIcons.unarchive, onClick = { onUnarchive(session) })),
            onDismiss = { menu = null }, title = session.conversationTitle(words))
    }
}

/**
 * What Copy diagnostics copies: the conversation's structure as this app has it, never a word of it (its title, its
 * messages). iOS copies its chat trace there (`ChatTrace`), which Android doesn't record.
 */
internal fun conversationDiagnostics(session: Session): String = listOfNotNull(
    "Persona 对话诊断（Android，只有结构，没有内容）", // i18n-ignore: diagnostics for a bug report, as iOS ChatTrace.text
    "session=${session.key.path}",
    "status=${session.status} state=${session.state ?: "-"} since=${session.stateSince ?: "-"}",
    "updated_at=${session.updatedAt ?: "-"} rev=${session.rev ?: "-"}",
    "origin=${session.origin ?: "-"} manager=${session.manager ?: "-"} archived=${session.archived}",
    "launcher=${session.launcher ?: "-"} model=${session.model ?: "-"}",
    "pending=${session.pendingInteractions} queued=${session.queued?.size ?: 0} awaits_reply=${session.awaitsReply} starting=${session.starting}",
    session.turnErrorCode?.let { "turn_error_code=$it" },
).joinToString("\n")
