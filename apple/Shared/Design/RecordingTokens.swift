import SwiftUI

// The meeting recording page's tokens (docs/apple/recording.md, docs/design/proposals/meeting-recording.md section 7): how
// often the level meter is drawn, how large it is, and how a timeline row arrives. A file of their own, away from
// Theme.swift. Shared/, so the watch and the widgets compile them; they draw no recording UI.

nonisolated extension Theme.Motion {
    /// How often the level meter is drawn: 30 a second, as the voice orb and the avatar's halo. The smoothed input level
    /// changes about 20 times a second, so more frames add nothing, and a meeting can run for hours with the screen on.
    /// A clock drives it, not a repeating animation (a repeating animation leaks into the layout around it, `StatusDot`).
    static let recordingLevelFrame: TimeInterval = 1.0 / 30
}

nonisolated extension Theme.Size {
    /// The level meter's bars at the default text size: a bar's width, the gap between two, and the height they all
    /// share. Every bar has this height and is drawn with a vertical scale, so the meter moves only a transform and never
    /// relays out (docs/apple/ios.md, red line for loops).
    static let recordingMeterBar: CGFloat = 3
    static let recordingMeterGap: CGFloat = 3
    static let recordingMeterHeight: CGFloat = 44
}

nonisolated extension Theme {
    /// The recording page's numbers that are not sizes, spaces or times.
    enum Recording {
        /// How many bars the level meter has.
        static let meterBars = 40
        /// The shortest a bar gets (a share of the meter's height): a silent microphone is a flat row of dots, so a
        /// meter that does not move still says it is there.
        static let meterFloor: Double = 0.08
        /// The one still frame of a meter that does not move (Reduce Motion, Low Power Mode): how tall the middle bar
        /// stands, as a share of the meter's height. The same at any level, so it says nothing about the microphone.
        static let meterStill: Double = 0.45
        /// How much of a bar's height the meter's ripple moves (the rest follows the level), and how fast it moves, in
        /// radians a second.
        static let meterRipple: Double = 0.35
        static let meterRippleSpeed: Double = 9
        /// A paused or ended meter and recording mark: dimmed, never gone.
        static let dimmed: Double = 0.35
        /// A row arriving in the timeline starts this far below its place (the `reveal` curve brings it up).
        static let arrivalOffset: CGFloat = Theme.Space.s
        /// The largest text size the big timer grows to: past it the time stays on one line, readable, instead of
        /// shrinking text to hide that it does not fit.
        static let timerLargestSize = DynamicTypeSize.accessibility2
        /// How many seconds of audio waiting for its text are worth a "Transcribing the last N s" marker, and the step
        /// the number moves in (so it changes a few times a minute, not each second).
        static let gapMinimum = 5
        static let gapStep = 5
        /// How often the spoken time (VoiceOver's value for the timer) is refreshed, and the marker's number.
        static let spokenTimeRefresh: TimeInterval = 60
        static let gapRefresh: TimeInterval = 5
    }
}
