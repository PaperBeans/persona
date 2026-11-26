package persona.core.moduleapi

/**
 * The modules a build carries, in the order their entries show (the app's `Modules.kt` builds it). A module is active on
 * a hub unless that hub publishes module flags ([MODULES_MARKER]) and lacks the module's [PersonaModule.feature]
 * ([moduleFeature]); everything else is absent. Every
 * carried module gets its [PersonaModule.services] for a pairing, active or not, since a hub can start offering a feature
 * later; its pages and actions come only while it is active, and the shell shows its unavailable page for a module page
 * left open when the module turns inactive.
 */
class ModuleRegistry(val modules: List<PersonaModule>) {
    init {
        modules.forEach { require(it.id.matches(ModuleId)) { "Module id '${it.id}' is not lower-case and hyphenated" } }
        val repeated = modules.groupBy { it.id }.filterValues { it.size > 1 }.keys
        require(repeated.isEmpty()) { "Modules listed twice: $repeated" }
    }

    /**
     * The modules active on a hub whose hello offers [features] (the rule every client follows, #13844): a module with a
     * feature is off only when the hello says the hub publishes module flags ([MODULES_MARKER]) and lacks the module's
     * flag. No hello yet, demo mode (both an empty set) and an older hub without the marker leave every module on, so
     * defaults do not change.
     */
    fun active(features: Set<String>): List<PersonaModule> = modules.filter { isActive(it, features) }

    /** The Settings pages of the modules active on a hub whose hello offers [features], in list order. */
    fun settingsPages(features: Set<String>): List<ModuleSettingsPage> = active(features).flatMap { it.settingsPages() }

    /** Each module's [PersonaModule.services] for one pairing, by id; modules that keep none are left out. */
    fun services(hub: ModuleHub): Map<String, ModuleServices> =
        modules.mapNotNull { module -> module.services(hub)?.let { module.id to it } }.toMap()

    /** The debug pages of every module ([PersonaModule.debugPages]). */
    fun debugPages() = modules.flatMap { it.debugPages().entries }.associate { it.key to it.value }

    companion object {
        /** Android's log, or standard error where there is none (JVM tests). */
        fun logWarning(message: String) {
            try { android.util.Log.w("PersonaModules", message) } catch (_: RuntimeException) { System.err.println("PersonaModules: $message") }
        }

        /** The hello feature a hub announces when it publishes module flags (#13844). */
        const val MODULES_MARKER = "modules"

        /** The hello feature that turns module [id] on, `module.<id>`: what a module's [PersonaModule.feature] names. */
        fun moduleFeature(id: String): String = "module.$id"

        /** Whether [module] is on for a hub whose hello offers [features] ([active]). */
        fun isActive(module: PersonaModule, features: Set<String>): Boolean {
            val feature = module.feature ?: return true
            return flagOn(feature, features)
        }

        /** Whether the hub's own [module] is on for a hub whose hello offers [features]: the same rule as [active]. */
        fun isActive(module: HubModule, features: Set<String>): Boolean = flagOn(module.feature, features)

        /** The one rule (#13844): off only when the hello has [MODULES_MARKER] and lacks [feature]. */
        private fun flagOn(feature: String, features: Set<String>): Boolean = MODULES_MARKER !in features || feature in features

        val Empty = ModuleRegistry(emptyList())
        private val ModuleId = Regex("[a-z][a-z0-9]*(-[a-z0-9]+)*")
    }
}

/**
 * A module of the hub this app follows the flag of without carrying a module for it (#15214): there is no
 * `:module:<id>` project, so it is not in the app's `Modules.kt`, and what it switches lives in pages another module or
 * the core draws. On a hub where it is off ([ModuleRegistry.isActive]) those pages take away the module's new activity
 * and entry points; what it produced earlier stays listed, readable and searchable (#13430).
 */
enum class HubModule(val id: String) {
    /** Meeting recording: minutes, their to-dos and hand-offs are its routes; stored recordings are read through the core's. */
    Recording("recording"),

    /**
     * Feishu / Lark (#15995): the Lark bot, which the hub connects only while this is on. Not the Lark extension's
     * chats, readers and connector (`LarkFeatures`, the hello's `lark`): those are the extension's routes and stay.
     */
    Lark("lark"),

    /**
     * Mail (#15995): accounts, reading a message in full with its attachments, flags and sending are its routes. What
     * the hub stored of a mail (the Inbox item, its text, its place in the knowledge base) is read through the core's.
     */
    Mail("mail");

    /** The hello feature that turns it on, `module.<id>`. */
    val feature: String get() = ModuleRegistry.moduleFeature(id)
}
