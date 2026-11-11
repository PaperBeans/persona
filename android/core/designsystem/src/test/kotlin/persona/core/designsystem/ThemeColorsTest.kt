package persona.core.designsystem

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.PersonaManifest

/** Settings › Appearance › Theme color and Light/Dark (#3048): the defaults change nothing, every accent reads on every style's page. */
class ThemeColorsTest {
    @Test fun defaultsAreTheOldLook() {
        assertEquals(ThemeColor.Style, ThemeColor.fromKey(null))
        assertEquals(ThemeColor.Style, ThemeColor.fromKey("nonsense"))
        assertEquals(ColorSchemeChoice.System, ColorSchemeChoice.fromKey(null))
        assertNull(ColorSchemeChoice.System.dark)
        assertNull(ThemeColors.accent(ThemeColor.Style, null))
        // "Follow persona" without a package's colours is the style's own.
        assertNull(ThemeColors.accent(ThemeColor.Persona, null))
        assertNull(ThemeColors.accent(ThemeColor.Persona, PersonaManifest.Colors(light = mapOf("primary" to "#F2C230"))))
    }

    @Test fun everyAccentMeetsTheLinkFloorOnEveryStyle() {
        val accents = ThemeColor.presets.mapNotNull { it.preset } +
            // A hard one: a pale yellow a package might bring.
            ThemeAccent(LightDark(Color(0xFFF2E030), Color(0xFF3A3000)), fromPackage = true)
        for (accent in accents) for (style in AppStyle.entries) for (dark in listOf(false, true)) {
            val base = Palettes.of(style, dark)
            val p = ThemeColors.palette(base, accent)
            for (surface in listOf(p.background, p.surface.compositeOn(p.background), p.surfaceOpaque)) {
                val ratio = ExtensionThemeColors.contrast(p.accent, surface)
                assertTrue("${style.key} dark=$dark ${accent.of(dark)} on $surface: $ratio", ratio >= 3.0)
            }
            // The words on the user's own bubble read: the page's ink on a pale one, the bubble's own on a deep one.
            assertTrue(ExtensionThemeColors.contrast(p.userBubbleText ?: p.ink, p.userBubble) >= 4.5)
        }
    }

    @Test fun personaColorsFallBackAcrossAppearances() {
        val one = ThemeColors.persona(PersonaManifest.Colors(light = mapOf("accent" to "#94600A")))!!
        assertEquals(one.of(false), one.of(true))
        val both = ThemeColors.persona(PersonaManifest.Colors(light = mapOf("accent" to "#94600A"), dark = mapOf("accent" to "#B47A0C")))!!
        assertNotEquals(both.of(false), both.of(true))
        assertNull(ThemeColors.persona(PersonaManifest.Colors(light = mapOf("accent" to "not a colour"))))
    }

    @Test fun coralIsASolidDeepBubbleWithWhiteWordsLikeIOS() {
        for (dark in listOf(false, true)) {
            val p = ThemeColors.palette(Palettes.of(AppStyle.Glass, dark), ThemeColor.Coral.preset!!)
            assertEquals(if (dark) Tokens.Semantic.Claude.Dark.userBubble else Tokens.Semantic.Claude.Light.userBubble, p.userBubble)
            assertEquals(Color.White, p.userBubbleText)
            assertTrue(ExtensionThemeColors.contrast(Color.White, p.userBubble) >= 4.5)
        }
        // "System blue" is iOS's pale blue with the page's own ink.
        val blue = ThemeColors.palette(Palettes.of(AppStyle.Glass, false), ThemeColor.Blue.preset!!)
        assertEquals(Tokens.Semantic.Glass.Light.userBubble, blue.userBubble)
        assertNull(blue.userBubbleText)
    }

    @Test fun aPackWithOnlyABubbleStillOffersFollowingThePersona() {
        val colors = PersonaManifest.Colors(light = mapOf("bubble" to "#F2C230", "bubble_text" to "#FFFFFF"))
        val accent = ThemeColors.accent(ThemeColor.Persona, colors)!!
        assertNull(accent.accent)
        val base = Palettes.of(AppStyle.Board, false)
        val p = ThemeColors.palette(base, accent)
        // The style's accent stays; the bubble is the pack's, pulled darker until its white words read.
        assertEquals(base.accent, p.accent)
        assertEquals(Color.White, p.userBubbleText)
        assertTrue(ExtensionThemeColors.contrast(Color.White, p.userBubble) >= 4.5)
        // bubble_text alone is not a colour scheme.
        assertNull(ThemeColors.persona(PersonaManifest.Colors(light = mapOf("bubble_text" to "#FFFFFF"))))
    }
}
