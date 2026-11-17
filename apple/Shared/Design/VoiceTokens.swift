import SwiftUI

// The voice conversation's tokens (docs/assistant/voice.md「Design system」): how the orb in the voice bar moves and how large it is.
// A file of their own, away from Theme.swift (the CarPlay voice PR edits it too). Shared/, so the watch compiles them;
// it draws no voice UI.

nonisolated extension Theme.Motion {
    /// One way of the orb's breath while it listens: it swells for this long and settles for as long, eased at both
    /// ends. The same as Android's `Voice.kt` (`ListeningBreath`, 1600 ms each way).
    static let voiceBreath: TimeInterval = 1.6
    /// One way of the orb's beat while Persona speaks: quick enough to read as a voice. Android's `SpeakingBeat`
    /// (420 ms each way).
    static let voiceBeat: TimeInterval = 0.42
    /// One lap of the ring around the orb while the call connects: a spinner's pace.
    static let voiceSpin: TimeInterval = 1.1
    /// One lap of the ring around the orb while the agent works: slow, a sign that something goes on, not a wait.
    static let voiceLap: TimeInterval = 2.8
    /// How often a moving orb is drawn: 30 a second, as `StatusDot` and the avatar's halo; a 1.6 s breath and a level
    /// that changes about 20 times a second gain nothing from more, and a call can run for many minutes with the screen
    /// on. A clock drives it, not a repeating animation (those leaked into the layout of rows around them, `StatusDot`).
    static let voiceFrame: TimeInterval = 1.0 / 30
}

nonisolated extension Theme.Size {
    /// The voice orb's box at the default text size (`scaledFrame` grows it with the text): the sphere and the room it
    /// swells and glows into, more than a finger's target. The voice bar's middle; a larger orb passes its own size.
    static let voiceOrb: CGFloat = 52
    /// The orb's level meter (the BeautifulUI style's five bars) at the default orb: a bar's width, the gap between
    /// two, and the tallest bar. A larger orb scales them with it.
    static let voiceLevelBar: CGFloat = 3
    static let voiceLevelGap: CGFloat = 2
    static let voiceLevelHeight: CGFloat = 16
    /// How much of the conversation a call keeps in sight over the pending cards (`PendingCards`) where the page is short
    /// (large text): about a line of the call's captions, its last row. The card the user has to answer gets the rest.
    static let voiceThreadRoom: CGFloat = 44
    /// The call screen's round controls (#9290: Mute, Speaker or Audio, Resume, End): a circle a thumb finds without
    /// looking, as the Phone app's, with its one-line label under it. Grows with the text (`scaledFrame`).
    static let callControl: CGFloat = 72
    /// The call screen's orb (`VoiceOrb` at this size), where the Phone app shows the caller's picture: the box, halo
    /// included.
    static let callOrb: CGFloat = 160
    /// The most of the call screen's height the orb takes. It also gives way to the name, the line and the caption (the
    /// controls always keep their room), down to the voice bar's orb; where even that does not fit, it is not drawn.
    static let callOrbShare: CGFloat = 0.22
}

nonisolated extension Theme {
    /// The voice conversation's cues (docs/assistant/voice.md 1.2 A / I / M, 6.6): a short sound through the voice's own player (so
    /// echo cancellation takes it out of the microphone too) and a tap on the hand. Played on a change the user did not
    /// make on the screen: connected, a card that wants a tap, the connection picked up again. Quiet beside the voice, and
    /// short enough not to cover a word of it.
    enum VoiceCue: String, CaseIterable, Sendable {
        /// Connected, Listening: two notes up.
        case ready
        /// A card wants a tap (a plan, a permission, a question): two notes up and back, a little higher.
        case attention
        /// The voice's connection dropped and is being picked up: one low note.
        case reconnecting

        /// The notes, in hertz, one after the other.
        var notes: [Double] {
            switch self {
            case .ready: [659.25, 880]
            case .attention: [880, 1174.66, 880]
            case .reconnecting: [440]
            }
        }

        /// How long each note sounds, and the quiet between two.
        static let note: TimeInterval = 0.085
        static let gap: TimeInterval = 0.035
        /// Each note's ends ramp in and out over this much, so it does not click.
        static let ramp: TimeInterval = 0.012
        /// Loudness, a share of full scale: under the voice's own level.
        static let gain: Double = 0.16

        /// What the hand feels with it.
        var haptic: VoiceHaptic {
            switch self {
            case .ready: .tap
            case .attention: .attention
            case .reconnecting: .none
            }
        }
    }

    /// The taps a voice call gives (`Haptics` in the app): with a cue, and on their own where no sound can play (the call
    /// ended by itself: its player is gone).
    enum VoiceHaptic: String, Sendable {
        case none, tap, attention, failure
    }
}
