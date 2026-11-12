package persona.platform.phonetools

import androidx.compose.ui.res.stringResource
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import persona.core.designsystem.*
import persona.core.designsystem.dense

/**
 * Health Connect's "why does this app want health data" page. Its permission dialog does not open at all for an app
 * without one (`androidx.health.ACTION_SHOW_PERMISSIONS_RATIONALE`; Android 14+ looks for the
 * `VIEW_PERMISSION_USAGE` alias in the manifest instead).
 */
class HealthRationaleActivity : ComponentActivity() {
    /** Text in the language of Settings › Language (docs/protocol/i18n.md). */
    override fun attachBaseContext(base: android.content.Context) = super.attachBaseContext(persona.core.designsystem.AppLanguages.wrap(base))

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            PersonaTheme {
                JListPage(title = stringResource(R.string.phonetools_health_rationale_title), onBack = { finish() }) {
                    item {
                        Paragraphs(stringResource(R.string.phonetools_health_rationale_how), listOf(
                            stringResource(R.string.phonetools_health_rationale_read),
                            stringResource(R.string.phonetools_health_rationale_store),
                        ))
                    }
                    item {
                        Paragraphs(stringResource(R.string.phonetools_health_rationale_off), listOf(
                            stringResource(R.string.phonetools_health_rationale_off_detail),
                        ))
                    }
                }
            }
        }
    }
}

@Composable
private fun Paragraphs(header: String, lines: List<String>) {
    JGroupItem(GroupPosition.Single, Modifier.padding(bottom = 12.dp.dense)) {
        Text(header, style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink,
            modifier = Modifier.padding(horizontal = 16.dp.dense, vertical = 12.dp.dense))
        lines.forEach { t ->
            Text(t, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2,
                modifier = Modifier.padding(horizontal = 16.dp.dense, vertical = 8.dp.dense))
        }
    }
}
