package persona.feature.settings

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import coil3.ImageLoader
import persona.core.contracts.api.ExtensionManagement

/** A page-owned, credential/manager-scoped Coil loader; never shares private artwork with the public loader. */
@Composable
internal fun rememberExtensionLogoLoader(api: ExtensionManagement?): ImageLoader? {
    val context = LocalContext.current
    val loader = remember(context, api) {
        api?.let {
            extensionPictureLoader(context, it::icon, maxMemoryBytes = 8L * 1024 * 1024)
        }
    }
    DisposableEffect(loader) { onDispose { loader?.shutdown() } }
    return loader
}
