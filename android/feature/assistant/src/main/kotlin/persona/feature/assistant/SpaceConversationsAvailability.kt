package persona.feature.assistant

import androidx.compose.runtime.compositionLocalOf
import kotlinx.coroutines.flow.StateFlow

/** The enabled extension's domain-space surface. Null/false keeps the core
 * Assistant free of domain Spaces, including demos and tests without a hub. */
val LocalSpaceConversationsEnabled = compositionLocalOf<StateFlow<Boolean>?> { null }
