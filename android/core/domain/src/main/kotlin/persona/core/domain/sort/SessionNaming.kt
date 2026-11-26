package persona.core.domain.sort

import persona.core.contracts.Session

/**
 * The live herdr tab label for an Agent in herdr or Herdr App (matches server `Session.HerdrTabName` and web
 * `herdrTabName`): null for assistant chats, sessions without a tab/slot association, non-herdr hosts, or blank labels.
 */
val Session.herdrTabName: String?
    get() {
        if (assistant || (slotId.isNullOrBlank() && tabId.isNullOrBlank())) return null
        val kind = spaceKind?.takeIf(String::isNotEmpty)
            ?: host?.takeIf(String::isNotEmpty)
            ?: hostKind.takeIf(String::isNotEmpty)
        if (kind != null && kind != "herdr" && kind != "herdr-core") return null
        return slotName?.trim()?.takeIf(String::isNotEmpty)
    }

/**
 * The primary herdr tab name when [preferTab] is enabled.
 * When [preferTab] is false, returns null so default-off rendering remains unchanged (#13899).
 */
fun Session.displayTabName(preferTab: Boolean): String? =
    if (preferTab) herdrTabName else null

/**
 * Main session name: uses the herdr tab name when [preferTab] is enabled and available, else [Session.displayTitle].
 */
fun Session.displayTitle(preferTab: Boolean): String =
    displayTabName(preferTab) ?: displayTitle

/**
 * Secondary CLI session title when [preferTab] promotes the herdr tab name to the primary title and the CLI title
 * differs (web `sessionTitleDetail`). Null when [preferTab] is false, no herdr tab name applies, or the titles match.
 */
fun Session.displayTitleDetail(preferTab: Boolean): String? {
    val tab = displayTabName(preferTab) ?: return null
    val cli = title.trim()
    return cli.takeIf { it.isNotEmpty() && it != tab }
}

/**
 * Whether the Rename dialog merges into a single "Name" field (web `RenameDialog` with `use_herdr_tab_name` on).
 */
fun Session.usesUnifiedHerdrRename(preferTab: Boolean): Boolean =
    preferTab && herdrTabName != null
