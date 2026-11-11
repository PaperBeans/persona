package persona.core.contracts.api

import persona.core.contracts.ProtocolCompatibility

import persona.core.contracts.CoreTextException
import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The persona packages on the hub (docs/apple/persona.md "Pack format"): GET /api/v1/personas lists the ones that check out,
 * and each file of a package comes from GET /api/v1/personas/<id>/<file>.
 */
interface PersonaPacks {
    /** Throws [PersonaPacksUnsupported] when the hub has no such route (an older hub). */
    suspend fun list(): List<PersonaInfo>
    suspend fun file(id: String, file: String): ByteArray

    /**
     * GET /api/v1/looks (#3200, docs/extensions/extensions.md "Avatars"): the built-in looks, the hub's packages and the extensions'
     * in one list, and the look the user chose, kept on the hub for every device. Throws [PersonaPacksUnsupported] on
     * a hub without it.
     */
    suspend fun looks(): LookListing = throw PersonaPacksUnsupported()

    /** PUT /api/v1/looks/current: chooses a look for every device; [ifUnset] only when the hub has none yet (moving this phone's old choice up). */
    suspend fun choose(id: String, ifUnset: Boolean = false): LookCurrent = throw PersonaPacksUnsupported()

    /** The hub makes avatars from images (`look_uploads` in its features, #2404): the apps offer New from Photo only then. */
    val uploads: Boolean get() = false

    /**
     * POST /api/v1/looks/uploads (docs/apple/persona.md "Uploading an image"): makes an avatar from [image] (PNG or JPEG
     * bytes, cropped square and upright), named [name] with an optional [subtitle]; [choose] chooses it for every device.
     */
    suspend fun upload(name: String, subtitle: String?, image: ByteArray, choose: Boolean = true): LookUploaded = throw PersonaPacksUnsupported()

    /** DELETE /api/v1/looks/uploads/{id}: deletes an avatar made from an image; the hub's choice after it (the default when it was the chosen one). */
    suspend fun deleteUpload(id: String): LookCurrent = throw PersonaPacksUnsupported()
}

/** The answer to an upload: the new look as GET /api/v1/looks lists it, and the choice when it was chosen. */
@Serializable
data class LookUploaded(val look: LookView, val current: LookCurrent? = null)

/** The answer to deleting an upload: the hub's choice after it. */
@Serializable
data class LookDeleted(val current: LookCurrent = LookCurrent())

/** The body of GET /api/v1/looks. */
@Serializable
data class LookListing(val looks: List<LookView> = emptyList(), val current: LookCurrent = LookCurrent())

/** One look: a built-in one the app draws (no manifest), a hub package or an extension's. */
@Serializable
data class LookView(
    val id: String,
    val source: String,
    val name: String = "",
    val names: Map<String, String> = emptyMap(),
    @SerialName("app_icon") val appIcon: String = "persona",
    @SerialName("extension_name") val extensionName: String? = null,
    val rev: String? = null,
    val bytes: Long = 0,
    val manifest: PersonaManifest? = null,
    /** Made from an image in the apps (#2404): the only looks the apps can delete. */
    val uploaded: Boolean = false,
) {
    /** The package to download and show, for the hub's and the extensions' looks. */
    val pack: PersonaInfo? get() = if (source != "builtin" && manifest != null && rev != null) PersonaInfo(id, rev, bytes, manifest) else null
}

/** The chosen look ([id] null: never chosen on this hub) and the one to show. */
@Serializable
data class LookCurrent(val id: String? = null, val effective: String = "persona")

/** The hub predates persona packages (404 on the list). */
class PersonaPacksUnsupported : CoreTextException(CoreText.of(Key.PersonaPacksUnsupported))

/** The body of GET /api/v1/personas. */
@Serializable
data class PersonaListing(val personas: List<PersonaInfo> = emptyList())

/** One entry of GET /api/v1/personas: the manifest and a rev that changes with any byte of the package. */
@Serializable
data class PersonaInfo(val id: String, val rev: String, val bytes: Long = 0, val manifest: PersonaManifest) {
    /** Usable on the phone: the manifest checks out and is this entry's, and the rev is the hub's 16 hex digits (it names a directory). */
    val usable: Boolean get() = manifest.problem == null && manifest.id == id && PERSONA_REV.matches(rev)
}

// Not in a private companion: kotlinx finds a class's serializer through its companion by reflection (a List<PersonaInfo>
// written to index.json), which a private companion refuses.
private val PERSONA_REV = Regex("^[0-9a-f]{16}$")

/** persona.json, format 1. [colors] feed the "Follow persona" theme color (docs/apple/persona.md "Color scheme"); an extension's look adds names in
 *  other languages, a dark avatar and where it comes from (docs/extensions/extensions.md "Avatars"). */
@Serializable
data class PersonaManifest(
    val format: Int,
    val id: String,
    val name: String,
    val names: Map<String, String> = emptyMap(),
    val subtitle: String? = null,
    /** A few words for the look's card in Settings › Appearance › Persona look, before the subtitle (Milk Frog: where its level comes from). */
    val about: String? = null,
    val avatar: Avatar,
    val dot: Dot,
    /** One set of `#RRGGBB` per appearance; absent when the package only changes the face. */
    val colors: Colors? = null,
    val source: Source? = null,
) {
    /** `colors.light` / `colors.dark`: `accent`, `bubble`, `bubble_text`, `primary`, `secondary` (each optional). */
    @Serializable data class Colors(val light: Map<String, String> = emptyMap(), val dark: Map<String, String> = emptyMap()) {
        /** Brings an accent in both appearances: the only colours "Follow persona" lays over the style. */
        val hasAccent: Boolean get() = "accent" in light || "accent" in dark
    }
    /** PNGs by side; [dark]: the same for dark mode (an SVG is the web's, not read here). */
    @Serializable data class Avatar(val sizes: Map<String, String>, val dark: Dark? = null)
    @Serializable data class Dark(val sizes: Map<String, String> = emptyMap())
    @Serializable data class Source(val kind: String = "", @SerialName("extension_name") val extensionName: String? = null)
    @Serializable data class Dot(val size: Int? = null, val states: Map<String, DotState>)
    @Serializable data class DotState(val file: String, val fps: Double? = null)

    /** Made from an image in the apps (`source.kind` `upload`, #2404): the only packages the apps can delete. */
    val uploaded: Boolean get() = source?.kind == "upload"

    /** Every file it names: what the phone downloads. */
    val files: List<String> get() = (avatar.sizes.values + avatar.dark?.sizes.orEmpty().values + dot.states.values.map { it.file }).toSortedSet().toList()

    /** The name in [language] (docs/protocol/i18n.md: zh-Hans, en, ja), else English, else its own. */
    fun displayName(language: String): String =
        names[language]?.takeIf { it.isNotBlank() } ?: names["en"]?.takeIf { it.isNotBlank() && language != "zh-Hans" } ?: name

    /** The smallest avatar at least [pixels] wide, else the largest; [dark]: the dark variant's when it has any. */
    fun avatarFile(pixels: Int, dark: Boolean = false): String? {
        val pick = avatar.dark?.sizes?.takeIf { dark && it.isNotEmpty() } ?: avatar.sizes
        val sizes = pick.mapNotNull { (k, v) -> k.toIntOrNull()?.let { it to v } }.sortedBy { it.first }
        return (sizes.firstOrNull { it.first >= pixels } ?: sizes.lastOrNull())?.second
    }

    /** The state's frames (by its wire name), idle's for a state the package lacks. */
    fun dotState(state: String): DotState? = dot.states[state] ?: dot.states["idle"]

    /** What the hub checks too (server/internal/hub/persona.go), so a package it would leave out is refused here. */
    val problem: String? get() = when {
        format != FORMAT -> "format $format"
        id in RESERVED_IDS || !ID.matches(id) -> "id $id"
        name.isBlank() -> "no name"
        avatar.sizes.isEmpty() -> "no avatar"
        "idle" !in dot.states -> "no idle dot"
        else -> files.firstOrNull { f -> f.isEmpty() || f.startsWith("/") || f.split('/').any { it == ".." || it.startsWith(".") } }
            ?.let { "file $it" }
    }

    companion object {
        const val FORMAT = 1
        /** Retired drawing identities remain reserved so a package cannot revive or impersonate them. */
        val RETIRED_IDS = setOf("persona-visor", "persona-circuit", "persona-pixel", "persona-facet", "persona-blob")
        val RESERVED_IDS = RETIRED_IDS + "persona" + ProtocolCompatibility.legacyBuiltins
        private val ID = Regex("^[a-z0-9][a-z0-9-]{0,31}$")
    }
}
