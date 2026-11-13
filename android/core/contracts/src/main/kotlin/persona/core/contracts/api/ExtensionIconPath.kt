package persona.core.contracts.api

/** Unnormalized relative paths only. No authority, encoded path segment, traversal, fragment or extra query. */
object ExtensionIconPath {
    private val installed = Regex("/api/v1/extensions/[A-Za-z0-9_-]{1,64}/-/icon\\?v=[A-Za-z0-9_.~%+\\-]+")
    private val catalog = Regex("/api/v1/extensions/-/catalog/[A-Za-z0-9_-]{1,64}/[A-Za-z0-9_-]{1,64}/icon\\?sha256=[a-fA-F0-9]{64}")
    fun matches(value: String): Boolean = installed.matches(value) || catalog.matches(value)
}
