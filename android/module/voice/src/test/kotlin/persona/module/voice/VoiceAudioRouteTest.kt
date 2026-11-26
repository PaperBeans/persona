package persona.module.voice

import android.content.Context
import android.media.AudioManager
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.moduleapi.AudioBroker
import persona.core.moduleapi.AudioUse
import persona.core.moduleapi.SystemAudioFocus

/** The core's audio broker around a conversation (#13847), and routing it on the oldest Android the app installs on (API 30) and on one with communication devices. */
@RunWith(AndroidJUnit4::class)
class VoiceAudioRouteTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val manager = context.getSystemService(AudioManager::class.java)

    private val system = mutableListOf<String>()
    private val broker = AudioBroker(object : SystemAudioFocus {
        override fun acquire(use: AudioUse) { system += "acquire $use" }
        override fun abandon() { system += "abandon" }
    })
    private var lost = 0
    private val listener = object : VoiceAudio.Listener {
        override fun onFrame(pcm: ByteArray, length: Int, level: Float) {}
        override fun onSpeaking(speaking: Boolean) {}
        override fun onMicrophoneLost() { lost++ }
    }

    @Test fun theConversationHoldsTheCoresAudioUntilItStops() {
        val audio = AndroidVoiceAudio(context, broker.forModule(VoiceModule))
        assertTrue(audio.start(16_000, 24_000, listener))
        assertEquals("voice" to AudioUse.Conversation, broker.held)
        audio.stop()
        assertNull(broker.held)
        assertEquals(listOf("acquire Conversation", "abandon"), system)
    }

    @Test fun itDoesNotStartWhileACallHoldsTheAudio() {
        val call = broker.request("calls", AudioUse.Call) {}
        val audio = AndroidVoiceAudio(context, broker.forModule(VoiceModule))
        assertFalse(audio.start(16_000, 24_000, listener))
        assertEquals("Nothing of the phone's audio was touched", AudioManager.MODE_NORMAL, manager.mode)
        audio.stop()
        assertEquals("calls" to AudioUse.Call, broker.held)
        call!!.release()
    }

    @Test fun aCallTakingTheAudioEndsTheConversationLikeALostMicrophone() {
        val audio = AndroidVoiceAudio(context, broker.forModule(VoiceModule))
        assertTrue(audio.start(16_000, 24_000, listener))
        val _ = broker.request("calls", AudioUse.Call) {}
        assertEquals(1, lost)
        audio.stop()
        assertEquals("calls" to AudioUse.Call, broker.held)
    }

    @Config(sdk = [30])
    @Test fun onApi30TheCallGoesToTheLoudspeakerAndEverythingIsPutBack() {
        @Suppress("DEPRECATION")
        assertFalse(manager.isSpeakerphoneOn)
        val audio = AndroidVoiceAudio(context, broker.forModule(VoiceModule))
        audio.start(16_000, 24_000, listener)
        assertEquals(AudioManager.MODE_IN_COMMUNICATION, manager.mode)
        @Suppress("DEPRECATION")
        assertTrue(manager.isSpeakerphoneOn)
        audio.stop()
        assertEquals(AudioManager.MODE_NORMAL, manager.mode)
        @Suppress("DEPRECATION")
        assertFalse(manager.isSpeakerphoneOn)
    }

    @Config(sdk = [31])
    @Test fun fromApi31TheModeIsPutBackAfterTheCall() {
        val audio = AndroidVoiceAudio(context, broker.forModule(VoiceModule))
        audio.start(16_000, 24_000, listener)
        assertEquals(AudioManager.MODE_IN_COMMUNICATION, manager.mode)
        audio.stop()
        assertEquals(AudioManager.MODE_NORMAL, manager.mode)
    }
}
