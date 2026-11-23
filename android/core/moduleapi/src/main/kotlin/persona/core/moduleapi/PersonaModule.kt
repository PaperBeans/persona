package persona.core.moduleapi

import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.navigation3.runtime.EntryProviderScope
import androidx.navigation3.runtime.NavKey
import persona.core.designsystem.icons.PhIcon
import persona.core.navigation.Navigator

/**
 * One module of the app (#13430 "Persona Core and modules", docs/android/android.md "Modules"): a feature the owner can
 * go without while the assistant, Agents, to-dos, computers and connectors keep working. A module reaches the core only
 * through this contract and the other `:core:*` modules; the core names no module. The app's `Modules.kt` is the only
 * list of modules, and a module that is not in it, or not enabled on the paired hub ([feature]), is absent: no page, no
 * menu item, nothing left over.
 *
 * Names follow the shared module contract (#13430 section 4): [id] is `id`, [feature] is `feature`, [navEntries] serves
 * `shell_places`, [menuActions] serves `composer_actions` and the other action slots ([MenuSlot]), [settingsPages] is
 * `settings`, [services] is the client half of `hub_routes`. Declared but not yet served by the Android shell:
 * [settingsPages], [audioUses] and every [MenuSlot] but [MenuSlot.AssistantTitle].
 */
interface PersonaModule {
    /** `id`: lower case and hyphenated, the same on every platform (`voice`, `knowledge-ui`). */
    val id: String

    /**
     * `feature`: the hub hello feature this module needs; on a hub without it the module is inactive (absent, as if not
     * built in). Null: it needs none and is active on every hub, demo mode included.
     */
    val feature: String? get() = null

    /**
     * The client half of `hub_routes`: what the module keeps for one pairing, built once per pairing (never in demo mode).
     * Null: it keeps nothing. The shell hands it back as [ModuleNavContext.services] and [ModuleUiContext.services].
     */
    fun services(hub: ModuleHub): ModuleServices? = null

    /**
     * `shell_places`: registers the module's pages. [ModuleNavContext.services] is null in demo mode, where a page that
     * needs the hub shows [ModuleNavContext.unavailable] instead.
     */
    fun navEntries(entries: EntryProviderScope<NavKey>, context: ModuleNavContext) {}

    /** The module's actions in [slot], read while that surface is composed; none by default. */
    @Composable
    fun menuActions(slot: MenuSlot, context: ModuleUiContext): List<ModuleMenuAction> = emptyList()

    /** `settings`: pages Settings lists for the module. Declared; the Android shell does not show them yet. */
    fun settingsPages(): List<ModuleSettingsPage> = emptyList()

    /** What the module plays or records ([AudioFocus]). Declared; the Android shell has no broker yet. */
    val audioUses: Set<AudioUse> get() = emptySet()

    /** Debug builds: pages `debug.openPage` can open by name, for captures. */
    fun debugPages(): Map<String, NavKey> = emptyMap()

    /** Debug builds: takes (removes) the `debug.*` launch arguments the module owns from [arguments]. */
    fun debugArguments(arguments: MutableMap<String, String>) {}
}

/** What a module keeps for one pairing ([PersonaModule.services]); [close] runs when the pairing ends. */
interface ModuleServices {
    fun close() {}
}

/** The places a module can add actions to. Only [AssistantTitle] is served on Android so far (#13433). */
enum class MenuSlot {
    /** The assistant's title menu, after Details (a place the module opens: `shell_places`). */
    AssistantTitle,
    /** `composer_actions`: buttons beside the composer. Declared, not yet served. */
    ComposerActions,
    /** `session_action` (#13400): a conversation's menu. Declared, not yet served. */
    SessionActions,
    /** `message_action` (#13400): a message's menu. Declared, not yet served. */
    MessageActions,
}

/**
 * One action a module adds to a [MenuSlot]: its words (resolved in [PersonaModule.menuActions], so they follow the
 * language), the icon Glass draws, and what it does.
 */
data class ModuleMenuAction(val id: String, val label: String, val icon: PhIcon, val onClick: () -> Unit)

/** A Settings page of a module ([PersonaModule.settingsPages]): its title and the page it opens. */
data class ModuleSettingsPage(val id: String, @StringRes val title: Int, val route: NavKey)

/** What [PersonaModule.navEntries] gets from the shell. */
class ModuleNavContext(
    val navigator: Navigator,
    /** The module's [PersonaModule.services] for the paired hub; null in demo mode or when it keeps none. */
    val services: ModuleServices?,
    /** The shell's page for a place that cannot work here (demo mode): the [message] and a way back. */
    val unavailable: @Composable (message: String) -> Unit,
)

/** What [PersonaModule.menuActions] gets from the shell. */
class ModuleUiContext(
    val navigator: Navigator,
    /** The module's [PersonaModule.services] for the paired hub; null in demo mode or when it keeps none. */
    val services: ModuleServices?,
    /** The surface with the slot is on screen and the hub reachable: the time to ask the hub what to offer. */
    val active: Boolean,
)
