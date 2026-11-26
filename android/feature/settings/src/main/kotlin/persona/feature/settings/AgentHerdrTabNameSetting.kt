package persona.feature.settings

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JNotice
import persona.core.designsystem.JRow
import persona.core.designsystem.JSwitch
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.operationFeedback
import persona.core.domain.sort.AgentListDisplaySettings

/**
 * Settings › Agent › "Use the herdr tab name as the session name" (#13899): mirrors Web `AgentsDisplayRow`.
 * Hidden until the paired hub reports `use_herdr_tab_name` (older hubs omit it and stay off).
 */
@Composable
internal fun AgentHerdrTabNameSetting(settings: AgentListDisplaySettings) {
    val enabled by settings.useHerdrTabName.collectAsState(null)
    val scope = rememberCoroutineScope()
    val res = LocalResources.current
    var saving by remember { mutableStateOf(false) }
    var pending by remember { mutableStateOf<Boolean?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(settings) { settings.refreshHerdrTabName() }
    val current = enabled ?: return
    fun save(next: Boolean) {
        if (saving) return
        saving = true
        pending = next
        error = null
        scope.launch {
            try {
                settings.setUseHerdrTabName(next)
                pending = null
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                error = operationFeedback(failure).text.resolve(res)
            } finally {
                saving = false
            }
        }
    }
    Column(Modifier.padding(top = Tokens.Space.l)) {
        JGroupItem(GroupPosition.Single) {
            JRow(
                stringResource(R.string.settings_use_herdr_tab_name),
                subtitle = stringResource(R.string.settings_use_herdr_tab_name_hint),
                subtitleLines = 3,
                onClick = { save(!current) },
                chevron = false,
                role = Role.Switch,
                enabled = !saving,
                modifier = Modifier.testTag("settings-use-herdr-tab-name"),
                trailing = { JSwitch(current, null, enabled = !saving) },
            )
        }
        if (saving) {
            SettingsCaption(stringResource(R.string.settings_herdr_tab_name_saving), Modifier.padding(top = Tokens.Space.xs))
        }
        error?.let { message ->
            JNotice(
                message,
                Modifier.padding(top = Tokens.Space.xs),
                status = Status.NeedsYou,
                actionLabel = stringResource(R.string.settings_herdr_tab_name_retry),
                onAction = { pending?.let(::save) },
            )
        }
    }
}
