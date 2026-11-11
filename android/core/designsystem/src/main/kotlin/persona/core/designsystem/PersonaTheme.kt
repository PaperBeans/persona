package persona.core.designsystem

import android.database.ContentObserver
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import dev.chrisbanes.haze.HazePerformanceMode
import dev.chrisbanes.haze.LocalHazePerformanceMode
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp

/** Corner radii of one style. Buttons and chips are pills in glass and brief, nearly square on the board. */
@Immutable
data class PersonaShapes(
    val card: Dp,
    val panel: Dp,
    val control: Dp,
    val chip: Dp,
    val button: Dp,
    val sheet: Dp,
) {
    val cardShape get() = RoundedCornerShape(card)
    val panelShape get() = RoundedCornerShape(panel)
    val controlShape get() = RoundedCornerShape(control)
    val chipShape get() = RoundedCornerShape(chip)
    val sheetShape get() = RoundedCornerShape(topStart = sheet, topEnd = sheet)
}

/**
 * Durations and curves; [PersonaTheme.reduceMotion] collapses them to instant changes. [count] is how long a number
 * that changed takes to count to its new value, and a chart's marks to move (#2811); [breathe] one half of a breathing
 * mark's cycle (a running workflow's chip, #2935).
 */
@Immutable
data class PersonaMotion(val quick: Int, val standard: Int, val springy: Boolean, val count: Int, val breathe: Int = 1300) {
    fun <T> enter(): FiniteAnimationSpec<T> =
        if (springy) spring(dampingRatio = 0.82f, stiffness = Spring.StiffnessMediumLow) else tween(standard, easing = FastOutSlowInEasing)
    fun size(): FiniteAnimationSpec<IntSize> =
        if (springy) spring(dampingRatio = 0.9f, stiffness = Spring.StiffnessMediumLow) else tween(standard, easing = FastOutSlowInEasing)
}

internal object Shapes {
    val Glass = PersonaShapes(
        card = Tokens.Radius.card, panel = Tokens.Radius.panel, control = Tokens.Radius.control,
        chip = 50.dp, button = 50.dp, sheet = Tokens.Radius.panel,
    )
    val Brief = PersonaShapes(card = 24.dp, panel = 28.dp, control = 16.dp, chip = 50.dp, button = 50.dp, sheet = 28.dp)
    val Board = PersonaShapes(card = 10.dp, panel = 10.dp, control = 8.dp, chip = 6.dp, button = 8.dp, sheet = 14.dp)
    fun of(style: AppStyle) = when (style) { AppStyle.Glass -> Glass; AppStyle.Brief -> Brief; AppStyle.Board -> Board }
}

internal object Motions {
    fun of(style: AppStyle) = when (style) {
        AppStyle.Glass -> PersonaMotion(quick = 160, standard = 320, springy = true, count = 600)
        AppStyle.Brief -> PersonaMotion(quick = 140, standard = 240, springy = false, count = 520)
        AppStyle.Board -> PersonaMotion(quick = 100, standard = 160, springy = false, count = 400)
    }
}

val LocalAppStyle = compositionLocalOf { AppStyle.Default }
val LocalGlassLevel = compositionLocalOf { GlassLevel.Liquid }
val LocalReduceMotion = compositionLocalOf { false }
internal val LocalPersonaDarkTheme = compositionLocalOf { false }
internal val LocalPalette = compositionLocalOf { Palettes.GlassLight }
internal val LocalType = compositionLocalOf { Types.Glass }

object PersonaTheme {
    val darkTheme: Boolean @Composable get() = LocalPersonaDarkTheme.current
    val reduceMotion: Boolean @Composable get() = LocalReduceMotion.current
    val style: AppStyle @Composable get() = LocalAppStyle.current
    val palette: PersonaPalette @Composable get() = LocalPalette.current
    val type: PersonaType @Composable get() = LocalType.current
    val shapes: PersonaShapes @Composable get() = Shapes.of(LocalAppStyle.current)
    val motion: PersonaMotion @Composable get() = Motions.of(LocalAppStyle.current)
    val glass: GlassLevel @Composable get() = LocalGlassLevel.current
    /** The older colour names, mapped onto the active style. */
    val colors: PersonaColors @Composable get() = LocalPalette.current.legacy()
}

/**
 * The theme. [style] defaults to the enclosing one, so nested calls (pages that pin dark or light) keep the
 * user's style. [dynamicColor] is ignored: every style has a fixed palette.
 */
@Composable
fun PersonaTheme(
    darkTheme: Boolean = appDarkTheme(),
    @Suppress("UNUSED_PARAMETER") dynamicColor: Boolean = false,
    reduceMotion: Boolean = systemReduceMotion(),
    style: AppStyle = LocalAppStyle.current,
    content: @Composable () -> Unit,
) {
    val roleTheme = LocalExtensionRoleTheme.current
    // The main window provides its own; the app's other windows (share, capsule, dialogs) take the app-wide one.
    val accent = LocalThemeAccent.current ?: AppAppearance.accent
    val palette = roleTheme?.let { ExtensionThemeColors.palette(it, style, darkTheme) }
        ?: accent?.let { ThemeColors.palette(Palettes.of(style, darkTheme), it) } ?: Palettes.of(style, darkTheme)
    val type = Types.of(style)
    val shapes = Shapes.of(style)
    CompositionLocalProvider(
        LocalAppStyle provides style,
        LocalPersonaDarkTheme provides darkTheme,
        LocalReduceMotion provides reduceMotion,
        LocalPalette provides palette,
        LocalType provides type,
        // Glass blurs a quarter-resolution copy of what is behind it: measured on a phone, full-resolution input
        // doubled the frame time of a scrolling conversation (docs/android/android.md, interface style performance).
        LocalHazePerformanceMode provides HazePerformanceMode.Performance,
    ) {
        MaterialTheme(colorScheme = palette.materialScheme(style), typography = type.material(), shapes = shapes.material()) {
            CompositionLocalProvider(LocalContentColor provides palette.ink, content = content)
        }
    }
}

private fun PersonaPalette.materialScheme(style: AppStyle): ColorScheme {
    val primary = if (style == AppStyle.Brief) action else accent
    val onPrimary = if (customRoles) ExtensionThemeColors.on(primary)
        else if (style == AppStyle.Brief) onAction else if (dark) Palettes.GlassDark.onAction else androidx.compose.ui.graphics.Color.White
    val base = if (dark) darkColorScheme() else lightColorScheme()
    return base.copy(
        primary = primary, onPrimary = onPrimary, primaryContainer = accentSoft, onPrimaryContainer = ink,
        // A snackbar's button ("Undo") on its inverse ground: brief's accent is its ink, the ground's own color (#2045).
        inversePrimary = if (style == AppStyle.Brief) background else accent,
        secondary = ink2, onSecondary = background, secondaryContainer = surfaceStrong.compositeOn(surfaceOpaque), onSecondaryContainer = ink,
        tertiary = needsYou.dot, onTertiary = needsYou.onDot, tertiaryContainer = needsYou.tint.compositeOn(surfaceOpaque), onTertiaryContainer = ink,
        background = background, onBackground = ink,
        surface = background, onSurface = ink, surfaceVariant = surfaceStrong.compositeOn(surfaceOpaque), onSurfaceVariant = ink2,
        surfaceTint = androidx.compose.ui.graphics.Color.Transparent,
        surfaceContainerLowest = surfaceOpaque, surfaceContainerLow = surfaceOpaque, surfaceContainer = surfaceOpaque,
        surfaceContainerHigh = surfaceOpaque, surfaceContainerHighest = surfaceStrong.compositeOn(surfaceOpaque),
        surfaceBright = surfaceOpaque, surfaceDim = background,
        inverseSurface = ink, inverseOnSurface = background,
        outline = ink3, outlineVariant = line,
        error = danger.dot, onError = danger.onDot, errorContainer = danger.tint.compositeOn(surfaceOpaque), onErrorContainer = danger.text,
        scrim = scrim,
    )
}

/** Blend a translucent token over an opaque one, for Material surfaces that cannot be translucent. */
internal fun androidx.compose.ui.graphics.Color.compositeOn(under: androidx.compose.ui.graphics.Color): androidx.compose.ui.graphics.Color {
    if (alpha >= 1f) return this
    val a = alpha
    return androidx.compose.ui.graphics.Color(
        red = red * a + under.red * (1 - a), green = green * a + under.green * (1 - a), blue = blue * a + under.blue * (1 - a), alpha = 1f,
    )
}

private fun PersonaType.material() = Typography(
    displayLarge = largeTitle, displayMedium = largeTitle, displaySmall = largeTitle,
    headlineLarge = largeTitle, headlineMedium = title, headlineSmall = title,
    titleLarge = title, titleMedium = headline, titleSmall = labelSmall,
    bodyLarge = body, bodyMedium = callout, bodySmall = caption,
    labelLarge = label, labelMedium = caption, labelSmall = micro,
)

private fun PersonaShapes.material() = Shapes(
    extraSmall = RoundedCornerShape(minOf(chip, 8.dp)), small = RoundedCornerShape(control),
    medium = RoundedCornerShape(card), large = RoundedCornerShape(panel), extraLarge = RoundedCornerShape(sheet),
)

/** The older text styles, now resolved from the active style so existing pages follow it. Prefer [PersonaTheme.type]. */
object PersonaTypography {
    private val type: PersonaType get() = Types.of(ActiveStyle.value)
    val body: TextStyle get() = type.body
    val cardBody: TextStyle get() = type.callout
    val chip: TextStyle get() = type.caption
    val code: TextStyle get() = type.code
    val codeSmall: TextStyle get() = type.codeSmall
    val greeting: TextStyle get() = type.largeTitle
    val meta: TextStyle get() = type.caption
    val micro: TextStyle get() = type.micro
    val panelTitle: TextStyle get() = type.title
    val preview: TextStyle get() = type.headline
    val rowTitle: TextStyle get() = type.headline
}

@Composable
private fun systemReduceMotion(): Boolean {
    val resolver = LocalContext.current.contentResolver
    fun read() = Settings.Global.getFloat(resolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f
    var reduced by remember(resolver) { mutableStateOf(read()) }
    DisposableEffect(resolver) {
        val observer = object : ContentObserver(Handler(Looper.getMainLooper())) {
            override fun onChange(selfChange: Boolean) { reduced = read() }
        }
        resolver.registerContentObserver(
            Settings.Global.getUriFor(Settings.Global.ANIMATOR_DURATION_SCALE), false, observer,
        )
        reduced = read()
        onDispose { resolver.unregisterContentObserver(observer) }
    }
    return reduced
}
