package persona.feature.planted

import android.net.Uri
import persona.core.domainkit.Instant
import persona.core.domainkit.Now
import persona.core.planted.domain.Item

/** App code using the planted domain: it keeps the next item a snooze returns, or the item when the snooze is refused. */
internal fun snooze(item: Item, until: Instant, now: Now): Item = item.snoozed(until, now).fold({ item }, { it })

/** App code dropping what Uri's parse returns: the kit declares a parse too, but Uri is not the domain's. */
internal fun forget(raw: String) {
    val _ = runCatching { Uri.parse(raw) }
}
