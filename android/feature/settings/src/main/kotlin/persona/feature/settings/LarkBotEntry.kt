package persona.feature.settings

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.ChannelManagement
import persona.core.contracts.api.HubChannel
import persona.core.designsystem.JListPage
import persona.core.designsystem.JNotice
import persona.core.designsystem.LocalHubFeatures
import persona.core.moduleapi.HubModule
import persona.core.moduleapi.ModuleRegistry

/**
 * Whether Settings offers the Lark bot on the selected hub (#15995): its channel in Settings › Channels (the switch,
 * linking, where its messages go, the test message) and its own page (the app's credentials, the daily sync). The hub
 * connects the bot only while its Lark module is on ([HubModule.Lark], `module.lark`); with it off those controls
 * write settings that do nothing or are refused, so they are left out, not disabled (#13430).
 *
 * This is not `LarkFeatures` (the hello's `lark`, the Lark extension's): that decides what the extension's surfaces can
 * do (Settings › Connectors › Lark, Messages, the Inbox's Lark chats and readers), which read the extension's own
 * `/api/v1/lark/…` routes and are what the user already has. They do not read this, and stay.
 */
internal enum class LarkBotEntry {
    /** The module is on, or the hub says nothing about modules: everything as it was. */
    Offered,
    /** A hub that publishes module flags without `module.lark`: no channel row, and a page reached anyway says so. */
    ModuleOff;

    companion object {
        /** The bot's id among the hub's channels (`channel.Feishu` on the hub). `lark_agent`, /agent in Lark, is the extension's. */
        const val CHANNEL = "feishu"

        /** From the hub's hello [features], by the rule every module follows ([ModuleRegistry.isActive]). */
        fun of(features: Set<String>): LarkBotEntry = if (ModuleRegistry.isActive(HubModule.Lark, features)) Offered else ModuleOff
    }

    /** [channels] as Settings lists them: without the bot's while the module is off. */
    fun listed(channels: List<HubChannel>): List<HubChannel> = when (this) {
        Offered -> channels
        ModuleOff -> channels.filterNot { it.id == CHANNEL }
    }

    /** Whether the page of channel [id] is one this takes away. */
    fun hides(id: String): Boolean = this == ModuleOff && id == CHANNEL

    /** [source] with its list as [listed]; the same object while nothing is taken away. */
    fun listing(source: ChannelManagement): ChannelManagement = when (this) {
        Offered -> source
        ModuleOff -> WithoutLarkBot(source, this)
    }
}

private class WithoutLarkBot(private val source: ChannelManagement, private val entry: LarkBotEntry) : ChannelManagement by source {
    override suspend fun channels(): List<HubChannel> = entry.listed(source.channels())
}

/** The one place Settings reads the Lark module's flag: where a settings page is put on screen, then handed down. */
@Composable
internal fun rememberLarkBotEntry(): LarkBotEntry {
    val features = LocalHubFeatures.current
    return remember(features) { LarkBotEntry.of(features) }
}

/**
 * What a Lark bot page shows when a link, a saved page or a hub that just switched the module off lands on it: the page's
 * own title and way back, and one calm line instead of controls that cannot work. The same shape these pages already
 * have where there is no hub to ask (the demo).
 */
@Composable
internal fun LarkBotOffPage(title: String, onBack: () -> Unit) = ModuleOffPage(title, stringResource(R.string.settings_lark_bot_module_off), onBack)

/** A settings page of a hub module that is off there: its [title], its way back and [line], nothing to press. */
@Composable
internal fun ModuleOffPage(title: String, line: String, onBack: () -> Unit) {
    JListPage(title = title, onBack = onBack) {
        item("module-off") { JNotice(line) }
    }
}
