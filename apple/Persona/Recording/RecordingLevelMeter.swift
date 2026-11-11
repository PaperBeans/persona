#if os(iOS)
import SwiftUI

/// How tall each bar of the level meter stands (docs/design/proposals/meeting-recording.md section 4C, item 3). A pure
/// function of the input level, the clock and whether the recording is paused, like `VoiceOrbPose`, so Reduce Motion's
/// still frame and the paused look are tested without a view. Every value is a share of the meter's height, 0...1, which
/// the view applies as a vertical scale to bars that all keep the same frame.
nonisolated enum RecordingLevelPose {
    /// The bell the bars follow: tallest in the middle, 0.3 at the ends.
    static func profile(_ index: Int, of count: Int) -> Double {
        guard count > 1 else { return 1 }
        let x = (Double(index) + 0.5) / Double(count)
        return 0.3 + 0.7 * sin(.pi * x)
    }

    /// The bars for `level` (0...1, the writer's smoothed input level) at `time` (seconds, any steady clock). A paused
    /// recording is flat. `time == nil` is the one still frame (Reduce Motion, Low Power Mode, an off-screen meter): the
    /// bell at rest, the same at every level.
    static func bars(level: Double, at time: TimeInterval?, paused: Bool, count: Int = Theme.Recording.meterBars) -> [Double] {
        let floor = Theme.Recording.meterFloor
        guard !paused else { return Array(repeating: floor, count: count) }
        guard let time else {
            return (0..<count).map { max(floor, profile($0, of: count) * Theme.Recording.meterStill) }
        }
        let level = level.isFinite ? min(1, max(0, level)) : 0
        let ripple = Theme.Recording.meterRipple
        return (0..<count).map { index in
            // The ripple only ever takes away from the level's height, so a loud room is never taller than the bell.
            let wave = 0.5 + 0.5 * sin(time * Theme.Recording.meterRippleSpeed + Double(index) * 0.9)
            let share = level * profile(index, of: count) * (1 - ripple + ripple * wave)
            return min(1, max(floor, share))
        }
    }
}

/// The level meter: a row of bars of one height, scaled by the pose, drawn by a clock of 30 frames a second that stops
/// whenever nothing moves. The level is read inside the clock's closure (the `VoiceOrb` pattern), so a new level redraws
/// the meter and nothing around it. Paused, the bars flatten and dim. With Reduce Motion or Low Power Mode, or while the
/// app is not in front, it is one still frame. VoiceOver skips it: it shows what the microphone hears, which the time
/// and the text already say in words.
struct RecordingLevelMeter: View {
    /// The microphone's level, 0...1; read only while the meter draws.
    var level: () -> Double
    var paused: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var scenePhase

    private var moving: Bool { !paused && !reduceMotion && scenePhase == .active && !ProcessInfo.processInfo.isLowPowerModeEnabled }

    var body: some View {
        TimelineView(.animation(minimumInterval: Theme.Motion.recordingLevelFrame, paused: !moving)) { context in
            let bars = RecordingLevelPose.bars(level: moving ? level() : 0, at: moving ? context.date.timeIntervalSinceReferenceDate : nil,
                                               paused: paused)
            HStack(spacing: Theme.Size.recordingMeterGap) {
                ForEach(bars.indices, id: \.self) { index in
                    Capsule()
                        .fill(paused ? Color.secondary : Palette.recordingRed)
                        .frame(width: Theme.Size.recordingMeterBar)
                        .frame(maxHeight: .infinity)
                        .scaleEffect(y: bars[index], anchor: .center)
                }
            }
            .opacity(paused ? Theme.Recording.dimmed : 1)
        }
        .scaledFrame(height: Theme.Size.recordingMeterHeight)
        .accessibilityHidden(true)
    }
}

/// The recording's mark: a red dot that blinks on a one-second beat (the held microphone's), or stays lit with Reduce
/// Motion; a pause glyph when the recording is paused, so the state is never the color alone.
struct RecordingStateDot: View {
    var paused: Bool

    var body: some View {
        Group {
            if paused {
                ThemedIcon(Theme.Icon.pause)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(Color.secondary)
            } else {
                RecordingBlinkingDot()
            }
        }
        .accessibilityHidden(true)
    }
}

/// The blinking dot owns its own dim state, so every time it is put on screen (Continue after a pause) it starts lit and
/// blinks again, instead of inheriting a state from the one that left.
private struct RecordingBlinkingDot: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var dim = false

    var body: some View {
        Circle()
            .fill(Palette.recordingRed)
            .scaledFrame(width: Theme.Size.dotXL, height: Theme.Size.dotXL)
            .opacity(dim ? Theme.Recording.dimmed : 1)
            .task(id: reduceMotion) {
                guard !reduceMotion else { dim = false; return }
                withAnimation(Theme.Motion.holdBlink.repeatForever(autoreverses: true)) { dim = true }
            }
    }
}
#endif
