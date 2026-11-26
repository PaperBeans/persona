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
 * How much of mail the selected hub serves (#15995). The App's shell decides it once from the hub's mail module flag
 * and provides it; pages read it here and never look at the flag themselves.
 */
enum class MailAccess {
    /** The mail module is on, or the hub says nothing about modules: accounts, whole messages, attachments, sending. */
    Live,
    /**
     * The hub's mail module is off: its routes answer nothing, so nothing that sets mail up, sends, flags or downloads is
     * offered. A mail already in the Inbox still opens, on the text the hub stored for it.
     */
    StoredOnly,
}

/** [MailAccess] for the selected hub; [MailAccess.Live] until a hello says otherwise, and in the demo. */
val LocalMailAccess = compositionLocalOf { MailAccess.Live }

/**
 * The selected hub's image limits (its hello's `image_limits`, docs/protocol/protocol.md "Pictures in a message").
 * Standard defaults until a hello says otherwise, and in the demo.
 */
val LocalHubImageLimits = compositionLocalOf { ImageLimitsTable.Standard }
