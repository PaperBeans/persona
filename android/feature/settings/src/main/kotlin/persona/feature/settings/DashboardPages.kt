package persona.feature.settings

// Dashboards (#15017): the pages the assistant keeps for the owner are drawn as the reserved extension "-pages"'s, through the
// ordinary extension page (ExtensionUiPage) and the ordinary sidebar rows. What differs for them is here: the hub owns every
// extension id that starts with "-", which is drawn over HTTP only; the header names the page by its own title, never its
// id; and every page but the index says, in the app's own words, who keeps it, which the page itself cannot change.

internal object DashboardPages {
    /** The extension id the hub reserves for the assistant's pages. */
    const val EXTENSION = "-pages"

    /** The list of every kept page: the Dashboards place itself. */
    const val INDEX = "index"

    /** An id the hub reserves for itself ("-pages", "-"): drawn over HTTP only, never through the native page stream. */
    fun httpOnly(extensionId: String): Boolean = extensionId.startsWith("-")

    /** Every Dashboards page but the index shows the ownership line under its header. */
    fun showsOwner(extensionId: String, uiId: String): Boolean = extensionId == EXTENSION && uiId != INDEX

    /**
     * The page header: the drawn View's title; until there is one (or when it is blank), the title the page was opened
     * with (its row's). A Dashboards page never shows its raw id there (a deep link opens it with no title, an unlisted
     * page's row would fall back to it): it is named [fallback], the place's name, until its View says otherwise.
     */
    fun headerTitle(extensionId: String, uiId: String, viewTitle: String?, title: String, fallback: () -> String): String {
        viewTitle?.takeIf { it.isNotBlank() }?.let { return it }
        if (extensionId != EXTENSION) return title
        return title.takeIf { it.isNotBlank() && it != uiId } ?: fallback()
    }
}
