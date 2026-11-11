package persona.feature.chat

import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.designsystem.ModeTone
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.sort.canChangeModes

/**
 * The Agent header's permission mode (#3957), the rules of iOS `SessionModes` (#3719, HeaderModeTests): each mode's tone
 * and symbol, never a guessed Ask First; when the header shows it; Allow Everything offered only where the agent can take
 * it while it runs; what a switch asks first.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class HeaderModeTest {
    private fun claude(mode: String?, bypass: Boolean = false, status: String = "idle") =
        Session(machineId = "m", sessionId = "s", agent = "claude", status = status, permissionMode = mode, bypassAvailable = bypass)

    @Test fun eachModeHasItsToneAndSymbol() {
        assertEquals(ModeTone.Asks, SessionModes.headerTone("default"))
        assertEquals(ModeTone.Plans, SessionModes.headerTone("plan"))
        assertEquals(ModeTone.Acts, SessionModes.headerTone("acceptEdits"))
        assertEquals(ModeTone.Acts, SessionModes.headerTone("auto"))
        assertEquals(ModeTone.AllowsAll, SessionModes.headerTone("bypassPermissions"))
        assertEquals(PhIcons.HandPalm, SessionModes.headerIcon("default"))
        assertEquals(PhIcons.ShieldWarning, SessionModes.headerIcon("bypassPermissions"))
        // not reported yet, or a mode this app does not know: the plain symbol, never Ask First
        for (id in listOf(null, "", "dontAsk")) {
            assertEquals(ModeTone.Unknown, SessionModes.headerTone(id))
            assertEquals(PhIcons.Shield, SessionModes.headerIcon(id))
        }
    }

    @Test fun theHeaderShowsAModeToSeeOrToChange() {
        assertTrue(claude("default").canChangeModes)
        assertTrue(SessionModes.showsInHeader(claude("default")))
        // before its first prompt: nothing reported, but it can be set
        assertTrue(SessionModes.showsInHeader(claude(null)))
        // ended, with its last mode: still shown, not changeable
        assertTrue(SessionModes.showsInHeader(claude("plan", status = "offline")))
        assertFalse(SessionModes.showsInHeader(claude(null, status = "offline")))
        // Codex has none; an assistant conversation has its own bar
        assertFalse(SessionModes.showsInHeader(Session(machineId = "m", sessionId = "c", agent = "codex", status = "idle")))
        assertFalse(SessionModes.showsInHeader(claude("default").copy(assistant = true)))
    }

    @Test fun allowEverythingIsOfferedOnlyWhereTheAgentCanTakeIt() {
        assertFalse(SessionModes.switchable(claude("default")).any { it.id == SessionModes.BYPASS })
        assertEquals(SessionModes.BYPASS, SessionModes.bypassUnavailable(claude("default"))?.id)
        val can = claude("default", bypass = true)
        assertTrue(SessionModes.switchable(can).any { it.id == SessionModes.BYPASS })
        assertNull(SessionModes.bypassUnavailable(can))
        // already in it: listed as the current one, not greyed again
        assertNull(SessionModes.bypassUnavailable(claude("bypassPermissions")))
        // not running: nothing to offer
        assertNull(SessionModes.bypassUnavailable(claude("default", status = "offline")))
    }

    @Test fun switchingAsksFirstIntoAllowEverythingAndOutOfItWithNoWayBack() {
        assertEquals(SessionModes.Confirmation.AllowEverything, SessionModes.confirmation(claude("default", bypass = true), "bypassPermissions"))
        assertEquals(SessionModes.Confirmation.LeaveAllowEverything, SessionModes.confirmation(claude("bypassPermissions"), "default"))
        // it can come back: no question
        assertNull(SessionModes.confirmation(claude("bypassPermissions", bypass = true), "default"))
        assertNull(SessionModes.confirmation(claude("default"), "plan"))
        assertNull(SessionModes.confirmation(claude("plan"), "plan"))
        // an assistant conversation switches without asking
        assertNull(SessionModes.confirmation(claude("default").copy(assistant = true), "bypassPermissions"))
    }

    @Test fun bypassAvailableIsReadOffTheSession() {
        val json = """{"machine_id":"m","session_id":"s","permission_mode":"bypassPermissions","bypass_available":true}"""
        assertTrue(JsonCodec.decodeFromString<Session>(json).bypassAvailable)
        assertFalse(JsonCodec.decodeFromString<Session>("""{"machine_id":"m","session_id":"s"}""").bypassAvailable)
    }
}
