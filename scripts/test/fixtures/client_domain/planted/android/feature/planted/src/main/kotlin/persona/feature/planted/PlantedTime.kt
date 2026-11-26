package persona.feature.planted

import java.time.Instant

/**
 * App code that imports no domain module: java.time's Instant shares a name with the kit's, and its parse is not the
 * domain's.
 */
internal fun parsedTime(text: String): Instant? {
    val _ = runCatching { return Instant.parse(text) }
    return null
}
