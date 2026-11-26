package persona.core.moduleapi

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/** The core's audio broker (#13847): one holder, ranked call > conversation > recording > playback. */
class AudioBrokerTest {
    private val system = mutableListOf<String>()
    private val broker = AudioBroker(object : SystemAudioFocus {
        override fun acquire(use: AudioUse) { system += "acquire $use" }
        override fun abandon() { system += "abandon" }
    }, warn = {})

    @Test fun theSystemsFocusFollowsTheOnlyHolder() {
        val grant = broker.request("voice", AudioUse.Conversation) {}
        assertNotNull(grant)
        assertEquals("voice" to AudioUse.Conversation, broker.held)
        grant!!.release()
        grant.release()
        assertNull(broker.held)
        assertEquals(listOf("acquire Conversation", "abandon"), system)
    }

    @Test fun aLowerUseIsRefusedWhileAHigherHoldsTheAudio() {
        val call = broker.request("calls", AudioUse.Call) {}
        assertNull(broker.request("voice", AudioUse.Conversation) {})
        assertNull(broker.request("player", AudioUse.Playback) {})
        assertEquals("calls" to AudioUse.Call, broker.held)
        call!!.release()
        assertNotNull(broker.request("voice", AudioUse.Conversation) {})
    }

    @Test fun aHigherOrEqualUseTakesTheAudioAndTheHolderHearsOnce() {
        var lost = 0
        val playback = broker.request("player", AudioUse.Playback) { lost++ }
        val conversation = broker.request("voice", AudioUse.Conversation) {}
        assertEquals(1, lost)
        assertEquals("voice" to AudioUse.Conversation, broker.held)
        // The loser releasing late gives nothing back: the conversation keeps the audio and the system's focus.
        playback!!.release()
        assertEquals("voice" to AudioUse.Conversation, broker.held)
        assertEquals(listOf("acquire Playback", "abandon", "acquire Conversation"), system)
        var replaced = 0
        broker.request("other", AudioUse.Conversation) { replaced++ }
        conversation!!.release()
        assertEquals("The newest of equal uses wins", "other" to AudioUse.Conversation, broker.held)
        assertEquals(0, replaced)
    }

    @Test fun aModuleGetsOnlyTheUsesItDeclares() {
        val warnings = mutableListOf<String>()
        val warned = AudioBroker(object : SystemAudioFocus {
            override fun acquire(use: AudioUse) {}
            override fun abandon() {}
        }, warnings::add)
        val voice = object : PersonaModule {
            override val id = "voice"
            override val audioUses = setOf(AudioUse.Conversation)
        }
        val focus = warned.forModule(voice)
        assertNull(focus.request("someone-else", AudioUse.Call) {})
        assertEquals(1, warnings.size)
        assertNotNull(focus.request("someone-else", AudioUse.Conversation) {})
        assertEquals("Requests go under the module's own id", "voice" to AudioUse.Conversation, warned.held)
    }

    @Test fun aHolderThatReleasesWhenItLosesDoesNotDeadlock() {
        lateinit var grant: AudioGrant
        grant = broker.request("voice", AudioUse.Conversation) { grant.release() }!!
        assertNotNull(broker.request("calls", AudioUse.Call) {})
        assertEquals("calls" to AudioUse.Call, broker.held)
    }
}
