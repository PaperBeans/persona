package persona.android.platform

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioManager
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import persona.core.moduleapi.AudioBroker
import persona.core.moduleapi.AudioUse

/** Android's audio focus behind the core's broker (#13847): the request each use makes, and giving it back. */
@RunWith(AndroidJUnit4::class)
class AndroidAudioFocusTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val manager = context.getSystemService(AudioManager::class.java)

    @Test fun aConversationTakesTransientVoiceCommunicationFocusAsTheVoicePageDid() {
        val request = AndroidAudioFocus.focusRequest(AudioUse.Conversation)
        assertEquals(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT, request.focusGain)
        assertEquals(AudioAttributes.USAGE_VOICE_COMMUNICATION, request.audioAttributes.usage)
        assertEquals(AudioAttributes.CONTENT_TYPE_SPEECH, request.audioAttributes.contentType)
        assertEquals(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT_MAY_DUCK, AndroidAudioFocus.focusRequest(AudioUse.Playback).focusGain)
    }

    @Test fun theBrokerTakesAndGivesBackTheSystemsFocus() {
        val broker = AudioBroker(AndroidAudioFocus(context))
        val grant = broker.request("voice", AudioUse.Conversation) {}!!
        val taken = shadowOf(manager).lastAudioFocusRequest.audioFocusRequest
        assertEquals(AudioAttributes.USAGE_VOICE_COMMUNICATION, taken.audioAttributes.usage)
        grant.release()
        assertSame(taken, shadowOf(manager).lastAbandonedAudioFocusRequest)
    }
}
