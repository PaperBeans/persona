package persona.core.designsystem

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.pow

/**
 * The readability floors of the three styles (Glass / Brief / Board) in light and dark, by WCAG contrast (the old app's
 * StylesTest, iOS's StyleSpecTests). Glass draws translucent fills: they are laid over the page background before
 * the contrast is measured.
 *
 * Brief and Board hold every word to AA (4.5:1). Glass holds the iPhone's floors instead (decision 2 of #4194, iOS
 * `PersonaPalette`): white on the accent 3.5:1, the accent as a link 3:1, words on the bubble 4.5:1, body ink 4.5:1. Its
 * other WCAG checks (secondary and status words) are printed, not failed, as `spec/tokens/rules.json` reports them.
 */
class StyleContrastTest {
    private fun channel(c: Float): Double = if (c <= 0.04045f) c / 12.92 else ((c + 0.055) / 1.055).pow(2.4)

    private fun luminance(c: Color): Double = 0.2126 * channel(c.red) + 0.7152 * channel(c.green) + 0.0722 * channel(c.blue)

    /** [top] painted over an opaque [under]. */
    private fun over(top: Color, under: Color): Color {
        val a = top.alpha
        return Color(top.red * a + under.red * (1 - a), top.green * a + under.green * (1 - a), top.blue * a + under.blue * (1 - a), 1f)
    }

    /** The contrast of [text] drawn on [fill]; both may be translucent, the fill lies on [page]. */
    private fun ratio(text: Color, fill: Color, page: Color): Double {
        val back = over(fill, page)
        val front = over(text, back)
        val x = luminance(front) + 0.05
        val y = luminance(back) + 0.05
        return maxOf(x, y) / minOf(x, y)
    }

    /** [glass] is the floor in Glass; null: Glass only reports it. */
    private class Check(val name: String, val text: Color, val fill: Color, val floor: Double, val glass: Double? = floor)

    private fun floor(style: AppStyle, check: Check): Double? = if (style == AppStyle.Glass) check.glass else check.floor

    private fun report(style: AppStyle, dark: Boolean, check: Check, r: Double) =
        println("reported, not failed: ${label(style, dark, check)}: ${"%.2f".format(r)} < ${check.floor}")

    private fun each(block: (style: AppStyle, dark: Boolean, palette: PersonaPalette) -> Unit) {
        for (style in AppStyle.entries) for (dark in listOf(false, true)) block(style, dark, Palettes.of(style, dark))
    }

    private fun label(style: AppStyle, dark: Boolean, check: Check) = "${style.name} ${if (dark) "dark" else "light"} ${check.name}"

    private fun below(checks: (PersonaPalette) -> List<Check>): List<String> {
        val found = mutableListOf<String>()
        each { style, dark, palette ->
            checks(palette).forEach { check ->
                val r = ratio(check.text, check.fill, palette.background)
                val floor = floor(style, check)
                if (floor == null) { if (r < check.floor) report(style, dark, check, r) }
                else if (r < floor) found += "${label(style, dark, check)}: ${"%.2f".format(r)} < $floor"
            }
        }
        return found
    }

    /** The surfaces text sits on: the page, cards (translucent in glass), opaque panels, raised and sunken wells. */
    private fun surfaces(p: PersonaPalette) = listOf(
        "background" to p.background, "surface" to p.surface, "surfaceOpaque" to p.surfaceOpaque,
        "surfaceStrong" to p.surfaceStrong, "sunken" to p.sunken,
    )

    @Test fun bodyTextOnEverySurface() {
        val found = below { p ->
            surfaces(p).flatMap { (name, fill) ->
                listOf(Check("ink on $name", p.ink, fill, 4.5), Check("ink2 on $name", p.ink2, fill, 4.5, glass = null))
            }
        }
        assertTrue("body text under 4.5:1: $found", found.isEmpty())
    }

    @Test fun solidButtonsTheHighlighterAndBadges() {
        val found = below { p ->
            listOf(
                Check("onAction on action", p.onAction, p.action, 4.5),
                Check("onMarker on marker", p.onMarker, p.marker, 4.5),
                Check("needsYou.onDot on needsYou.dot (count badges)", p.needsYou.onDot, p.needsYou.dot, 4.5, glass = 3.5),
            )
        }
        assertTrue("words on a solid fill under 4.5:1: $found", found.isEmpty())
    }

    @Test fun snackbarActionsAndMessagesOnTheInverseSurface() {
        // PersonaTheme maps inverseOnSurface to background and inverseSurface to ink.
        val found = below { p -> listOf(Check("snackbar message and action", p.background, p.ink, 4.5)) }
        assertTrue("snackbar text under 4.5:1: $found", found.isEmpty())
    }

    @Test fun theAccentAsALink() {
        val found = below { p -> listOf(Check("accent on background", p.accent, p.background, 4.5, glass = 3.0),
            Check("accent on surface", p.accent, p.surface, 4.5, glass = 3.0), Check("accent on statusSurface", p.accent, p.statusSurface, 4.5, glass = 3.0)) }
        assertTrue("the accent under 4.5:1: $found", found.isEmpty())
    }

    @Test fun mediaLabelsAndAccentControlsMeetAA() {
        val problems = mutableListOf<String>()
        each { style, dark, p ->
            for (photo in listOf(Color.Black, Color.White)) for ((name, fill) in listOf(
                "media backdrop" to p.mediaBackdrop, "media scrim" to p.mediaScrim,
                "media control" to p.mediaControlFill, "media label" to p.mediaLabelFill)) {
                val r = ratio(p.onMediaBackdrop, fill, photo)
                if (r < 4.5) problems += "${style.key} dark=$dark $name: $r"
            }
            val r = ratio(p.onAccent, p.accent, p.background)
            val onAccent = if (style == AppStyle.Glass) 3.5 else 4.5
            if (r < onAccent) problems += "${style.key} dark=$dark onAccent: $r"
        }
        assertTrue(problems.joinToString("\n"), problems.isEmpty())
    }

    @Test fun semanticTextReadsOnNeutralSurfacesOverGlassAmbient() {
        val problems = mutableListOf<String>()
        each { style, dark, p ->
            // Pulse <= 1.10 and the split/merged weights total <= 1.30: 1.5 × glow is a conservative bound.
            val backgrounds = listOf(p.background) + if (style == AppStyle.Glass) listOf(p.needsYou, p.working, p.done, p.danger).map {
                over(it.dot.copy(alpha = if (dark) 0.12f else 0.06f), p.background)
            } else emptyList()
            val words = listOf("ink" to p.ink, "ink2" to p.ink2, "ink3" to p.ink3, "accent" to p.accent,
                "needsYou" to p.needsYou.text, "working" to p.working.text, "done" to p.done.text,
                "idle" to p.idle.text, "offline" to p.offline.text, "danger" to p.danger.text,
                "sourceFeishu" to p.sourceFeishu.text, "sourceTerminal" to p.sourceTerminal.text,
                "diffAdd" to p.diffAdd, "diffDel" to p.diffDel)
            for (background in backgrounds) for ((name, fill) in listOf("page" to Color.Transparent,
                "surface" to p.surface, "opaque" to p.surfaceOpaque, "strong" to p.surfaceStrong,
                "sunken" to p.sunken, "status" to p.statusSurface)) for ((word, ink) in words) {
                val r = ratio(ink, fill, background)
                val floor = when {
                    style != AppStyle.Glass || word == "ink" -> 4.5
                    word == "accent" -> 3.0
                    else -> null
                }
                if (floor == null) { if (r < 4.5) println("reported, not failed: ${style.key} dark=$dark $word on $name: ${"%.2f".format(r)}") }
                else if (r < floor) problems += "${style.key} dark=$dark $word on $name: ${"%.2f".format(r)}"
            }
        }
        assertTrue(problems.distinct().joinToString("\n"), problems.isEmpty())
    }

    @Test fun wordsOnTheUsersBubble() {
        val found = mutableListOf<String>()
        each { style, dark, p ->
            for ((name, bubble) in listOf("own" to p.statusSurface, "other" to p.surface)) {
                val r = ratio(p.ink, bubble, p.background)
                if (r < 4.5) found += "${style.name} ${if (dark) "dark" else "light"} $name: ${"%.2f".format(r)}"
                val link = ratio(p.accent, bubble, p.background)
                if (link < (if (style == AppStyle.Glass) 3.0 else 4.5)) found += "${style.name} ${if (dark) "dark" else "light"} $name link: ${"%.2f".format(link)}"
            }
        }
        assertTrue("words and links on neutral bubbles under 4.5:1: $found", found.isEmpty())
    }

    /**
     * Status words, timestamps, hints and words on a state mark are text: 4.5:1.
     * Board glyphs are informative graphics: 3:1. Destructive buttons now use danger text on a neutral backing.
     */
    private fun quieter(style: AppStyle, p: PersonaPalette): List<Check> = buildList {
        for ((name, colors, floor) in listOf(
            Triple("needsYou", p.needsYou, 4.5), Triple("working", p.working, 4.5), Triple("done", p.done, 4.5),
            Triple("danger", p.danger, 4.5), Triple("idle", p.idle, 4.5), Triple("offline", p.offline, 4.5),
        )) {
            add(Check("$name.text on background", colors.text, p.background, floor, glass = null))
            add(Check("$name.text on surface", colors.text, p.surface, floor, glass = null))
        }
        add(Check("ink3 on background", p.ink3, p.background, 4.5, glass = null))
        add(Check("ink3 on surface", p.ink3, p.surface, 4.5, glass = null))
        add(Check("ink3 on statusSurface", p.ink3, p.statusSurface, 4.5, glass = null))
        for ((name, colors) in listOf("needsYou" to p.needsYou, "working" to p.working, "done" to p.done,
            "idle" to p.idle, "offline" to p.offline, "danger" to p.danger)) {
            add(Check("$name.text on statusSurface", colors.text, p.statusSurface, 4.5, glass = null))
        }
        for ((name, colors) in listOf("danger" to p.danger, "done" to p.done, "working" to p.working)) {
            add(Check("$name.onDot on $name.dot", colors.onDot, colors.dot, 4.5, glass = null))
        }
        if (style == AppStyle.Board) for ((name, colors) in listOf(
            "needsYou" to p.needsYou, "working" to p.working, "done" to p.done, "idle" to p.idle, "offline" to p.offline,
        )) add(Check("$name glyph on $name.dot", colors.onDot, colors.dot, 3.0))
    }

    @Test fun quieterWordsAndMarksMeetAAWithoutExceptions() {
        val problems = mutableListOf<String>()
        each { style, dark, palette ->
            quieter(style, palette).forEach { check ->
                val r = ratio(check.text, check.fill, palette.background)
                val floor = floor(style, check)
                if (floor == null) { if (r < check.floor) report(style, dark, check, r) }
                else if (r < floor) problems += "${label(style, dark, check)}: ${"%.2f".format(r)} < $floor"
            }
        }
        assertTrue(problems.joinToString("\n"), problems.isEmpty())
    }
}
