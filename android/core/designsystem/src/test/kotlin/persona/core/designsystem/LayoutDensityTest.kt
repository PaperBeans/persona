package persona.core.designsystem

import android.content.Context
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** Settings › Appearance › Layout density (#5564): Compact tightens the spacing tokens, radii and row heights, Standard draws the spec's values. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class LayoutDensityTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @After
    fun reset() {
        context.getSharedPreferences("persona-appearance", Context.MODE_PRIVATE).edit().clear().commit()
        LayoutDensity.apply(LayoutDensity.Default)
    }

    @Test
    fun standardIsTheDefaultAndKeepsTheSpecValues() {
        assertEquals(LayoutDensity.Standard, LayoutDensity.fromKey(null))
        assertEquals(LayoutDensity.Standard, LayoutDensity.fromKey("unknown"))
        val store = AppStyleStore(context)
        assertEquals(LayoutDensity.Standard, store.density.value)
        assertEquals(16.dp, Tokens.Space.l)
        assertEquals(Tokens.Space.Standard.m, Tokens.Space.m)
    }

    @Test
    fun compactTightensSpacingAndIsKept() {
        AppStyleStore(context).setDensity(LayoutDensity.Compact)
        assertEquals(8.dp, Tokens.Space.l)
        assertEquals(4.dp, Tokens.Space.s)
        assertEquals(14.dp * 0.6f, Tokens.Radius.card)
        assertEquals(14.dp, Tokens.Radius.Standard.card)
        assertEquals(32.dp, 40.dp.row)
        // what sizes things with a spacing token keeps its size
        assertEquals(16.dp, Tokens.Space.Standard.l)

        LayoutDensity.apply(LayoutDensity.Default)
        val reopened = AppStyleStore(context)
        assertEquals(LayoutDensity.Compact, reopened.density.value)
        assertEquals(8.dp, Tokens.Space.l)

        reopened.setDensity(LayoutDensity.Standard)
        assertEquals(16.dp, Tokens.Space.l)
    }
}
