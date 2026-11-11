package persona.core.designsystem

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.CurrentAssistant
import persona.core.contracts.api.HubManager
import persona.core.contracts.parseTime
import java.time.Instant

// The current assistant while it is offline (docs/assistant/managers.md "Clients"): the hub's merged lists (the inbox,
// to-dos) leave out a manager that does not answer, so a page listing its things would read as empty, or as a first run.

/**
 * The current assistant when it is offline, with several assistants; null otherwise (one assistant, online, still
 * installing, the roster not read yet). A page shows [JAssistantOffline] in place of its empty state then.
 */
val CurrentAssistant.offline: HubManager?
    get() = manager?.takeIf { roster?.isMulti == true && !it.online && !it.isInstalling }

/**
 * In place of a list's empty state, or its first-run hint, while the current assistant is offline ([offline]): its
 * monogram with the offline mark, faded as in the switcher, "<name> is offline", and when it last synced and that the
 * page updates once it is back (the same words on every client).
 */
@Composable
fun JAssistantOffline(assistant: CurrentAssistant, modifier: Modifier = Modifier, now: Instant = Instant.now()) {
    val manager = assistant.offline ?: return
    val res = LocalResources.current
    val synced = parseTime(manager.lastSeen)?.let { managerAgo(res, it, now) }
    JEmptyState(
        stringResource(R.string.designsystem_assistant_offline_title, manager.shownName),
        if (synced != null) stringResource(R.string.designsystem_assistant_offline_text, synced)
        else stringResource(R.string.designsystem_assistant_offline_text_never),
        modifier,
        leading = {
            JManagerAvatar(manager, manager.id == assistant.roster?.defaultManagerId,
                Modifier.stableAlpha(HomeTokens.assistantOfflineAlpha), presence = true)
        },
    )
}
