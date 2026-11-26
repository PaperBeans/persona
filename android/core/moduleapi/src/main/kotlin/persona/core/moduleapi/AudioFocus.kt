package persona.core.moduleapi

/**
 * What may own the phone's audio, highest first: a call, a voice conversation, a meeting recording, playback (the order
 * #13431 gives Apple's `AudioSessionBroker`). A use outranks every later one.
 */
enum class AudioUse {
    Call, Conversation, Recording, Playback;

    fun outranks(other: AudioUse): Boolean = ordinal < other.ordinal
}

/**
 * The shell's audio broker, which modules ask before they open the microphone or start playing (#13430, #13847). The
 * shell hands each module its own view ([AudioBroker.forModule]) in [ModuleNavContext.audioFocus].
 */
interface AudioFocus {
    /**
     * Asks for the audio for [use] on behalf of module [owner]; null while a use that outranks it holds the audio. [onLost]
     * runs when such a use takes it later.
     */
    fun request(owner: String, use: AudioUse, onLost: () -> Unit): AudioGrant?
}

/** The audio, held until [release]. */
fun interface AudioGrant {
    fun release()
}

/**
 * The system's side of the audio (Android's audio focus in the app): [acquire] takes it for [AudioUse], [abandon] gives
 * back what [acquire] took. The broker calls them one at a time, in order.
 */
interface SystemAudioFocus {
    fun acquire(use: AudioUse)
    fun abandon()
}

/**
 * The core's audio broker (#13847 item 2): one holder at a time across every module. A request is refused while the
 * holder's use outranks it ([AudioUse.outranks]); otherwise it takes the audio, and the previous holder's `onLost` runs
 * once. The system's focus follows the holder: taken for the new use, given back when the last grant
 * is released. Losing the system's focus to another app is not reported to modules.
 */
class AudioBroker(private val system: SystemAudioFocus, private val warn: (String) -> Unit = ModuleRegistry::logWarning) : AudioFocus {
    private class Holder(val owner: String, val use: AudioUse, val onLost: () -> Unit)

    private val lock = Any()
    private var holder: Holder? = null

    /** Who holds the audio now, for tests and logs: the owner and its use, or null. */
    val held: Pair<String, AudioUse>? get() = synchronized(lock) { holder?.let { it.owner to it.use } }

    override fun request(owner: String, use: AudioUse, onLost: () -> Unit): AudioGrant? {
        val next = Holder(owner, use, onLost)
        val previous = synchronized(lock) {
            val current = holder
            if (current != null && current.use.outranks(use)) return null
            holder = next
            if (current != null) system.abandon()
            system.acquire(use)
            current
        }
        // Outside the lock: a module that stops on loss releases its grant, which is a no-op by then.
        previous?.onLost?.invoke()
        return AudioGrant { release(next) }
    }

    private fun release(grant: Holder) = synchronized(lock) {
        if (holder !== grant) return@synchronized // lost to another use, or released before
        holder = null
        system.abandon()
    }

    /**
     * [module]'s view of the broker: requests go under its id, and only for the uses it declares
     * ([PersonaModule.audioUses]); an undeclared use is refused and said loudly.
     */
    fun forModule(module: PersonaModule): AudioFocus = object : AudioFocus {
        override fun request(owner: String, use: AudioUse, onLost: () -> Unit): AudioGrant? {
            if (use !in module.audioUses) {
                warn("Module '${module.id}' asked for audio as $use, which it does not declare; refused")
                return null
            }
            return this@AudioBroker.request(module.id, use, onLost)
        }
    }

    companion object {
        /** No system behind it (JVM tests, previews): only the ranking between modules. */
        fun withoutSystem(): AudioBroker = AudioBroker(object : SystemAudioFocus {
            override fun acquire(use: AudioUse) {}
            override fun abandon() {}
        })
    }
}
