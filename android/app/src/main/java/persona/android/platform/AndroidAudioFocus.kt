package persona.android.platform

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import persona.core.moduleapi.AudioUse
import persona.core.moduleapi.SystemAudioFocus

/**
 * Android's audio focus behind the core's audio broker (`AudioBroker`, #13847): one request at a time, for the use that
 * holds the audio. A call or a conversation takes transient focus as voice communication (other apps' music pauses, as
 * the voice page always did); a recording takes transient focus; playback takes it so others duck.
 */
internal class AndroidAudioFocus(context: Context) : SystemAudioFocus {
    private val manager = context.applicationContext.getSystemService(AudioManager::class.java)
    private var request: AudioFocusRequest? = null

    override fun acquire(use: AudioUse) {
        request = focusRequest(use).also { manager.requestAudioFocus(it) }
    }

    override fun abandon() {
        request?.let { manager.abandonAudioFocusRequest(it) }
        request = null
    }

    companion object {
        /** The focus request for [use]: its gain and what it plays. */
        fun focusRequest(use: AudioUse): AudioFocusRequest {
            val (gain, attributes) = when (use) {
                AudioUse.Call, AudioUse.Conversation -> AudioManager.AUDIOFOCUS_GAIN_TRANSIENT to
                    attributes(AudioAttributes.USAGE_VOICE_COMMUNICATION, AudioAttributes.CONTENT_TYPE_SPEECH)
                AudioUse.Recording -> AudioManager.AUDIOFOCUS_GAIN_TRANSIENT to
                    attributes(AudioAttributes.USAGE_MEDIA, AudioAttributes.CONTENT_TYPE_SPEECH)
                AudioUse.Playback -> AudioManager.AUDIOFOCUS_GAIN_TRANSIENT_MAY_DUCK to
                    attributes(AudioAttributes.USAGE_MEDIA, AudioAttributes.CONTENT_TYPE_UNKNOWN)
            }
            return AudioFocusRequest.Builder(gain).setAudioAttributes(attributes).build()
        }

        private fun attributes(usage: Int, content: Int) = AudioAttributes.Builder().setUsage(usage).setContentType(content).build()
    }
}
