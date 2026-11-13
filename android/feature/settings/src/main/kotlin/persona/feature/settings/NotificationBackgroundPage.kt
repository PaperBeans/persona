package persona.feature.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.FloatingCapsuleSettings
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JIcon
import persona.core.designsystem.JRow
import persona.core.designsystem.JSectionHeader
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons

/**
 * The unified page's header, followed by the existing phone checklist rather than a second implementation.
 * Pass NotificationPreferences as [header]; it retains its computer-muting controls and onDesktops callback.
 * The host supplies the localized [backgroundTitle] and opens CapsuleSettingsPage through [onCapsule].
 * Reading or opening this row never changes the capsule's mode or requests a system permission.
 */
@Composable
internal fun NotificationBackgroundHeader(
    capsule: FloatingCapsuleSettings?,
    onCapsule: () -> Unit,
    backgroundTitle: String,
    header: LazyListScope.() -> Unit = {},
): LazyListScope.() -> Unit {
    val mode = capsule?.mode?.collectAsState()?.value
    val capsuleTitle = stringResource(R.string.settings_capsule_title)
    val capsuleSubtitle = stringResource(R.string.settings_row_capsule_subtitle)
    val modeLabel = mode?.let { CapsuleSettingsText.label(it) }
    return {
        header()
        if (capsule != null) item("notification-background-capsule") {
            JGroupItem(GroupPosition.Single) {
                JRow(onClick = onCapsule, onClickLabel = capsuleTitle) {
                    JIcon(PersonaIcons.message, null, decorative = true)
                    Spacer(Modifier.width(Tokens.Space.m))
                    // Keep the current mode on its own line so long translations and large text can wrap.
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                        Text(capsuleTitle, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                        Text(capsuleSubtitle, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                        modeLabel?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
                    }
                }
            }
        }
        item("notification-background-checks") { JSectionHeader(backgroundTitle) }
    }
}
