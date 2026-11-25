package persona.core.designsystem

import androidx.compose.runtime.compositionLocalOf
import persona.core.contracts.ImageLimitsTable
import persona.core.contracts.LarkFeatures

/**
 * What the selected hub offers of Feishu through the Lark extension ([LarkFeatures.of] on its hello features). The
 * App's shell provides it; everything is shown until a hello says otherwise, and in the demo.
 */
val LocalLarkFeatures = compositionLocalOf { LarkFeatures.All }

/**
 * The selected hub's features (its hello's `features`, docs/protocol/protocol.md): what a page offers only on hubs that can do it
 * (`assistant_compact`, `assistant_main_new`). Empty until a hello says otherwise, and in the demo.
 */
val LocalHubFeatures = compositionLocalOf { emptySet<String>() }

/**
 * The selected hub's image limits (its hello's `image_limits`, docs/protocol/protocol.md "Pictures in a message").
 * Standard defaults until a hello says otherwise, and in the demo.
 */
val LocalHubImageLimits = compositionLocalOf { ImageLimitsTable.Standard }
