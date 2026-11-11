package persona.feature.chat

import androidx.compose.runtime.*
import android.content.res.Resources
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Session
import persona.core.contracts.api.ChatActions
import persona.core.designsystem.JAction
import persona.core.designsystem.JActionSheet
import persona.core.designsystem.rememberCopy
import persona.core.designsystem.QueueStrip
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.operationFeedback
import persona.core.domain.sort.canSend
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** "2 queued, Claude will handle them in order": how many wait, and for whom. */
internal fun queueTitle(count: Int, agent: String, resources: Resources): String =
    resources.getQuantityString(R.plurals.chat_queue_title, count, count, agent)

/** What to put back when one queued message is pulled out: every other index, in order (an empty list is meaningful). */
internal fun queueKeep(size: Int, index: Int): List<Int> = (0 until size).filter { it != index }

/**
 * Queue strip (iOS QueueStrip): what was typed while the agent works and not taken yet, right above the composer. Tapping one
 * offers "Pull back to edit" (back into the box, after the draft on a new line), "Copy" and "Delete". Editing goes through the node's
 * session.queue.edit (POST …/queue {keep}): it pulls the whole queue out of the agent and puts the rest back in order,
 * and answers with what was queued, so the pulled-back message is read from that answer. Only for a session the phone
 * may send to.
 */
@Composable
internal fun ChatQueueStrip(
    session: Session?,
    actions: ChatActions?,
    composer: ChatComposerState,
    onNotice: (String) -> Unit,
    refresh: suspend () -> Unit,
) {
    val queued = session?.queued.orEmpty()
    if (session == null || queued.isEmpty()) return
    val scope = rememberCoroutineScope()
    val copy = rememberCopy()
    val resources = LocalResources.current
    var busy by remember { mutableStateOf(false) }
    // The row picked, with its words: if the queue moves under the open menu (the agent took one), the menu closes.
    var picked by remember { mutableStateOf<Pair<Int, String>?>(null) }
    LaunchedEffect(queued) { picked?.let { (index, text) -> if (queued.getOrNull(index) != text) picked = null } }
    val enabled = session.canSend && actions != null
    fun edit(index: Int, delete: Boolean) {
        val service = actions ?: return
        if (busy || !session.canSend) return
        busy = true
        scope.launch {
            try {
                val pulled = service.editQueue(session, queueKeep(queued.size, index))
                if (delete) onNotice(resources.getString(R.string.chat_queue_deleted))
                else pulled.getOrNull(index)?.let { composer.restoreQueued(it); onNotice(resources.getString(R.string.chat_queue_restored)) }
                    ?: onNotice(resources.getString(R.string.chat_queue_not_found))
            } catch (e: CancellationException) { throw e }
            catch (e: Exception) { onNotice(resources.getString(R.string.chat_queue_failed, operationFeedback(e).text.resolve(resources))) }
            finally {
                // Stay busy until the queue on screen is the new one, so a stale row cannot be picked.
                try { refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
                busy = false
            }
        }
    }
    QueueStrip(queueTitle(queued.size, session.agentLabel, resources), queued.map { composerMessagePreview(it) },
        onItem = { index -> queued.getOrNull(index)?.let { picked = index to it } }, busy = busy, enabled = enabled)
    val (index, text) = picked ?: return
    JActionSheet(listOf(
        JAction(stringResource(R.string.chat_queue_edit), PhIcons.ArrowUUpLeft, enabled = enabled && !busy) { edit(index, delete = false) },
        JAction(stringResource(R.string.chat_queue_copy), PhIcons.Copy) { copy(text, null) },
        JAction(stringResource(R.string.chat_queue_delete), PhIcons.Trash, destructive = true, enabled = enabled && !busy) { edit(index, delete = true) },
    ), onDismiss = { picked = null }, title = composerMessagePreview(text))
}
