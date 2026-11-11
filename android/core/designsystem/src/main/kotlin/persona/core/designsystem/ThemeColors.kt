package persona.core.designsystem

import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.PersonaManifest

/**
 * Settings › Appearance › Light/Dark (iOS `ColorSchemeChoice`): follow the system (the default), or always light or dark, on this phone.
 * [dark] is null while it follows the system.
 */
enum class ColorSchemeChoice(val key: String, @StringRes val labelRes: Int, val dark: Boolean?) {
    System("system", R.string.designsystem_scheme_system, null),
    Light("light", R.string.designsystem_scheme_light, false),
    Dark("dark", R.string.designsystem_scheme_dark, true);

    val label: String @Composable get() = stringResource(labelRes)

    companion object {
        val Default = System
        fun fromKey(key: String?): ColorSchemeChoice = entries.firstOrNull { it.key == key } ?: Default
    }
}

/**
 * Settings › Appearance › Theme color (iOS `ThemeColor`): each style's own colors (the default, every style exactly as before), the
 * persona's (only offered when its package brings colors), or one of five fixed accents on any style. An extension's
 * role theme (extension theme), when one is chosen, still goes over all of these.
 */
enum class ThemeColor(val key: String, @StringRes val labelRes: Int) {
    Style("style", R.string.designsystem_color_style),
    Persona("persona", R.string.designsystem_color_persona),
    Blue("blue", R.string.designsystem_color_blue),
    Coral("coral", R.string.designsystem_color_coral),
    Lavender("lavender", R.string.designsystem_color_lavender),
    Teal("teal", R.string.designsystem_color_teal),
    Lark("lark", R.string.designsystem_color_lark);

    val label: String @Composable get() = stringResource(labelRes)

    /** The fixed accent, with its own bubble as iOS's ColorSpec has it; null for the two that follow something. */
    val preset: ThemeAccent? get() = when (this) {
        Style, Persona -> null
        // Pale bubbles keep the page's ink (iOS `.primary`); the deep ones carry their own light words.
        Blue -> ThemeAccent(LightDark(Tokens.Semantic.Glass.Light.accent, Tokens.Semantic.Glass.Dark.accent),
            bubble = LightDark(Tokens.Semantic.Glass.Light.userBubble, Tokens.Semantic.Glass.Dark.userBubble))
        Coral -> ThemeAccent(LightDark(Tokens.Semantic.Claude.Light.accent, Tokens.Semantic.Claude.Dark.accent),
            bubble = LightDark(Tokens.Semantic.Claude.Light.userBubble, Tokens.Semantic.Claude.Dark.userBubble),
            bubbleText = LightDark(Tokens.Semantic.Claude.Light.userBubbleText, Tokens.Semantic.Claude.Dark.userBubbleText))
        Lavender -> ThemeAccent(LightDark(Tokens.Semantic.Clay.Light.accent, Tokens.Semantic.Clay.Dark.accent),
            bubble = LightDark(Tokens.Semantic.Clay.Light.userBubble, Tokens.Semantic.Clay.Dark.userBubble),
            bubbleText = LightDark(Tokens.Semantic.Clay.Light.userBubbleText, Tokens.Semantic.Clay.Dark.userBubbleText))
        Teal -> ThemeAccent(LightDark(Tokens.Semantic.Island.Light.accent, Tokens.Semantic.Island.Dark.accent),
            bubble = LightDark(Tokens.Semantic.Island.Light.userBubble, Tokens.Semantic.Island.Dark.userBubble),
            bubbleText = LightDark(Tokens.Semantic.Island.Light.userBubbleText, Tokens.Semantic.Island.Dark.userBubbleText))
        Lark -> ThemeAccent(LightDark(Tokens.Semantic.Lark.Light.accent, Tokens.Semantic.Lark.Dark.accent),
            bubble = LightDark(Tokens.Semantic.Lark.Light.userBubble, Tokens.Semantic.Lark.Dark.userBubble))
    }

    companion object {
        val Default = Style
        /** The fixed ones, in the picker's order after the two that follow something. */
        val presets = listOf(Blue, Coral, Lavender, Teal, Lark)
        fun fromKey(key: String?): ThemeColor = entries.firstOrNull { it.key == key } ?: Default
    }
}

/** One colour for light and one for dark. */
@Immutable
data class LightDark(val light: Color, val dark: Color) {
    fun of(dark: Boolean) = if (dark) this.dark else light
}

/**
 * What a theme color lays over the style: the [accent] (what can be tapped, the selection), and the user's own [bubble] with
 * its [bubbleText] (null: the page's ink). Without a bubble the bubble is a pale wash of the accent; without an accent
 * (a package that only brings a bubble) the style's accent stays.
 */
@Immutable
data class ThemeAccent(
    val accent: LightDark?, val bubble: LightDark? = null, val bubbleText: LightDark? = null,
    /** From a persona package (not one of the presets): iOS adjusts only these to its floors. */
    val fromPackage: Boolean = false,
) {
    constructor(light: Color, dark: Color) : this(LightDark(light, dark))
    /** The accent in this appearance; null when only the bubble changes. */
    fun of(dark: Boolean): Color? = accent?.of(dark)
    /** The colour that stands for it in the picker. */
    fun swatch(dark: Boolean): Color? = of(dark) ?: bubble?.of(dark)
}

/** The accent the chosen theme color lays over the style's palette; null: the style's own ("Follow interface style", or a persona without colors). */
val LocalThemeAccent = staticCompositionLocalOf<ThemeAccent?> { null }

/**
 * Light/dark and theme color for every window of the app, not only the main one: the share sheet, the floating capsule, the
 * approve dialog and the permission pages draw with [PersonaTheme]'s defaults, which read these. The app sets [accent]
 * from the chosen theme color and package; [AppStyleStore] sets [scheme].
 */
object AppAppearance {
    var scheme by mutableStateOf(ColorSchemeChoice.Default)
    var accent by mutableStateOf<ThemeAccent?>(null)
}

/** Dark or not as Settings › Appearance › Light/Dark says, the system's while it follows the system. */
@Composable
fun appDarkTheme(): Boolean = AppAppearance.scheme.dark ?: androidx.compose.foundation.isSystemInDarkTheme()

object ThemeColors {
    private val hex = Regex("#[0-9a-fA-F]{6}")
    /** The floor an accent holds as a link on every style's page (docs/apple/persona.md "Contrast floor"). */
    private const val AccentFloor = 4.5
    /** Words on the user's own bubble. */
    private const val TextFloor = 4.5
    /** iOS's floors (`PersonaPalette`): white words on the accent, the accent as a link. */
    internal const val OnAccentFloorIos = 3.5
    internal const val LinkFloorIos = 3.0
    private const val SoftLight = 0.12f
    private const val SoftDark = 0.16f
    private const val BubbleLight = 0.16f
    private const val BubbleDark = 0.28f

    private fun parse(value: String?): Color? =
        value?.takeIf { it.matches(hex) }?.let { Color(0xFF000000L or it.drop(1).toLong(16)) }

    /** What [choice] lays over the style; [persona] is the chosen package's colors ("Follow persona" without them: the style's). */
    fun accent(choice: ThemeColor, persona: PersonaManifest.Colors?): ThemeAccent? = when (choice) {
        ThemeColor.Style -> null
        ThemeColor.Persona -> persona(persona)
        else -> choice.preset
    }

    /**
     * A package's colours (`accent`, `bubble`, `bubble_text`), each appearance falling back to the other's; null when it
     * brings none of them (iOS `ColorSpec(persona:over:)`). `bubble_text` only counts with a `bubble`.
     */
    fun persona(colors: PersonaManifest.Colors?): ThemeAccent? {
        colors ?: return null
        fun pair(key: String): LightDark? {
            val light = parse(colors.light[key])
            val dark = parse(colors.dark[key])
            if (light == null && dark == null) return null
            return LightDark(light ?: dark!!, dark ?: light!!)
        }
        val accent = pair("accent")
        val bubble = pair("bubble")
        if (accent == null && bubble == null) return null
        return ThemeAccent(accent, bubble, bubble?.let { pair("bubble_text") }, fromPackage = true)
    }

    /**
     * [base] with [accent]: the accent and its soft tint, and the user's own bubble (its own colours, else a pale wash
     * of the accent). Brief keeps its ink buttons and highlighter (its accent only colours links and selections). A colour
     * that would not read on this style's page is pulled toward black (light) or white (dark) until it does, and a bubble
     * is pulled away from its words until they read, so a package cannot make links or its messages unreadable.
     */
    fun palette(base: PersonaPalette, accent: ThemeAccent): PersonaPalette {
        val raw = accent.of(base.dark)
        var p = base
        if (raw != null) {
            // Glass as iOS: a preset as it is, a package's colour held to the iPhone's floors; the other styles hold 4.5:1.
            val color = when {
                !base.whiteOnAccent -> meeting(raw, base)
                accent.fromPackage -> iosMeeting(raw, base)
                else -> raw
            }
            val soft = color.copy(alpha = if (base.dark) SoftDark else SoftLight)
            val wash = color.copy(alpha = if (base.dark) BubbleDark else BubbleLight).compositeOn(base.surfaceOpaque)
            p = base.copy(accent = color, accentSoft = soft, userBubble = wash,
                marker = if (base.marker == base.accentSoft) soft else base.marker)
        }
        val fill = accent.bubble?.of(base.dark) ?: return p
        val text = accent.bubbleText?.of(base.dark)
        return p.copy(userBubble = readable(fill, text ?: p.ink), userBubbleText = text)
    }

    /** [fill] moved away from [text] (toward black under light words, toward white under dark) until they read at 4.5:1. */
    internal fun readable(fill: Color, text: Color): Color {
        val toward = if (text.luminance() > 0.5f) Color.Black else Color.White
        var c = fill.copy(alpha = 1f)
        repeat(20) {
            if (ExtensionThemeColors.contrast(text, c) >= TextFloor) return c
            c = mix(c, toward, 0.08f)
        }
        return c
    }

    /** [color] moved toward black (light) or white (dark) in steps until it meets the accent floor on [base]'s surfaces. */
    internal fun meeting(color: Color, base: PersonaPalette): Color {
        val backgrounds = listOf(base.background) + if (base.surface.alpha < 1f) listOf(base.needsYou, base.working, base.done, base.danger).map {
            it.dot.copy(alpha = if (base.dark) 0.12f else 0.06f).compositeOn(base.background)
        } else emptyList()
        val surfaces = backgrounds.flatMap { background -> listOf(background, base.surface.compositeOn(background), base.surfaceOpaque,
            base.surfaceStrong.compositeOn(background), base.statusSurface, base.sunken.compositeOn(background)) }
        val toward = if (base.dark) Color.White else Color.Black
        var c = color
        repeat(20) {
            if (surfaces.all { ExtensionThemeColors.contrast(c, it) >= AccentFloor }) return c
            c = mix(c, toward, 0.08f)
        }
        return c
    }

    /**
     * iOS `PersonaPalette.meeting`: white words read on it at 3.5:1 and it reads as a link at 3:1, toward black when white
     * doesn't read, else toward white. Android also checks the link on Glass's cards, not only the page.
     */
    internal fun iosMeeting(color: Color, base: PersonaPalette): Color {
        val white = Tokens.Semantic.Glass.Light.onAccent
        val surfaces = listOf(base.background, base.surface.compositeOn(base.background), base.surfaceOpaque)
        fun ok(c: Color) = ExtensionThemeColors.contrast(white, c) >= OnAccentFloorIos &&
            surfaces.all { ExtensionThemeColors.contrast(c, it) >= LinkFloorIos }
        if (ok(color)) return color
        // Black and white: the system's text in light and in dark.
        val toward = if (ExtensionThemeColors.contrast(white, color) < OnAccentFloorIos) Tokens.Semantic.Glass.Light.text
            else Tokens.Semantic.Glass.Dark.text
        for (i in 1..50) mix(color, toward, i / 50f).let { if (ok(it)) return it }
        return color
    }

    private fun mix(a: Color, b: Color, t: Float) =
        Color(a.red + (b.red - a.red) * t, a.green + (b.green - a.green) * t, a.blue + (b.blue - a.blue) * t, 1f)
}
