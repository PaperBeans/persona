import Foundation
import SwiftUI
import Testing
@testable import Persona

/// The voice orb (`VoiceOrb`, docs/assistant/voice.md「Design system」): its motion is a pure function of phase, time and level
/// (`VoiceOrbPose`), held here to the tokens: Android's pace, a still frame with Reduce Motion, and inside its box.
@MainActor struct VoiceOrbTests {
    /// The breath and the beat are Android's (`VoiceComponents.kt` in `:module:voice`), so the two apps' orbs move alike.
    @Test func paceMatchesAndroid() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let kotlin = try String(contentsOf: root.appending(path: "android/module/voice/src/main/kotlin/persona/module/voice/VoiceComponents.kt"),
                                encoding: .utf8)
        func millis(_ name: String) throws -> Double {
            let re = try NSRegularExpression(pattern: "\(name) = ([0-9]+)")
            let m = try #require(re.firstMatch(in: kotlin, range: NSRange(kotlin.startIndex..., in: kotlin)))
            return try #require(Double(kotlin[Range(m.range(at: 1), in: kotlin)!]))
        }
        #expect(try millis("ListeningBreath") == (Theme.Motion.voiceBreath * 1000).rounded())
        #expect(try millis("SpeakingBeat") == (Theme.Motion.voiceBeat * 1000).rounded())
    }

    /// Only a call that is on moves; the rest are still whatever the clock says.
    @Test func onlyALiveCallMoves() {
        let moving = Set(VoiceOrb.Phase.allCases.filter(\.moves))
        #expect(moving == [.connecting, .listening, .hearing, .speaking, .working])
        #expect(Set(VoiceOrb.Phase.allCases.filter(\.hearsLevel)) == [.listening, .hearing, .speaking, .working])
    }

    /// Reduce Motion (and a phase that does not move) draws one frame: the same at any level.
    @Test func stillFramesAreStill() {
        for phase in VoiceOrb.Phase.allCases {
            #expect(VoiceOrbPose.make(phase, at: nil, level: 0) == VoiceOrbPose.make(phase, at: nil, level: 1), "\(phase)")
        }
    }

    /// −1 at the start of a breath, 1 after one way, back after two, eased in between.
    @Test func swingGoesThereAndBack() {
        let T = Theme.Motion.voiceBreath
        #expect(abs(VoiceOrbPose.swing(0, oneWay: T) + 1) < 1e-9)
        #expect(abs(VoiceOrbPose.swing(T, oneWay: T) - 1) < 1e-9)
        #expect(abs(VoiceOrbPose.swing(T / 2, oneWay: T)) < 1e-9)
        let t = 812_345_678.123 // a date's worth of seconds, as the clock gives it
        #expect(abs(VoiceOrbPose.swing(t, oneWay: T) - VoiceOrbPose.swing(t + 2 * T, oneWay: T)) < 1e-6)
        #expect(abs(VoiceOrbPose.turn(t, period: Theme.Motion.voiceLap) - VoiceOrbPose.turn(t + Theme.Motion.voiceLap, period: Theme.Motion.voiceLap)) < 1e-6)
        for i in 0..<200 {
            let s = VoiceOrbPose.swing(Double(i) * 0.037, oneWay: Theme.Motion.voiceBeat)
            #expect((-1...1).contains(s))
        }
    }

    /// A louder voice swells the orb and lifts the meter.
    @Test func levelShows() {
        let t = 100.0
        #expect(VoiceOrbPose.make(.hearing, at: t, level: 1).scale > VoiceOrbPose.make(.hearing, at: t, level: 0).scale)
        #expect(VoiceOrbPose.make(.listening, at: t, level: 1).glow > VoiceOrbPose.make(.listening, at: t, level: 0).glow)
        let loud = VoiceOrbPose.make(.speaking, at: t, level: 1).bars.reduce(0, +)
        let soft = VoiceOrbPose.make(.speaking, at: t, level: 0).bars.reduce(0, +)
        #expect(loud > soft)
        // Out of range levels are clamped.
        #expect(VoiceOrbPose.make(.hearing, at: t, level: 7) == VoiceOrbPose.make(.hearing, at: t, level: 1))
    }

    /// Whatever the time and level, and in the still frames: the sphere stays in its box and inside the lap ring, the
    /// halo and the pulse ring in the box, bars and glow in 0…1.
    @Test func staysInsideItsBox() {
        let ringInside = VoiceOrb.ring - Theme.Size.rule / (Theme.Size.voiceOrb * VoiceOrb.sphere)
        let times: [TimeInterval?] = [nil] + (0..<240).map { 50 + Double($0) * 0.021 }
        for phase in VoiceOrb.Phase.allCases {
            for t in times {
                for level in [0.0, 0.5, 1] {
                    let p = VoiceOrbPose.make(phase, at: t, level: level)
                    #expect(p.scale * VoiceOrb.sphere <= 1, "\(phase) sphere \(p.scale)")
                    #expect(p.pulseRing * VoiceOrb.sphere <= 1, "\(phase) pulse ring \(p.pulseRing)")
                    // The halo is drawn unscaled in the box: its glow fades out by the box's edge, and starts inside it.
                    let halo = VoiceOrb.halo(p)
                    #expect(halo.end <= 0.5 && halo.start >= 0 && halo.start < halo.end, "\(phase) halo \(halo)")
                    if p.lap != nil { #expect(p.scale < ringInside, "\(phase) sphere into its ring \(p.scale)") }
                    #expect((0...1).contains(p.glow) && p.bars.allSatisfy { (0...1).contains($0) })
                    #expect(p.bars.isEmpty || p.bars.count == VoiceOrbPose.profile.count)
                    if let lap = p.lap { #expect((0..<1).contains(lap)) }
                }
            }
        }
        #expect(VoiceOrb.ring * VoiceOrb.sphere + 2 * Theme.Size.rule / Theme.Size.voiceOrb <= 1, "the lap ring leaves the box")
    }

    /// The meter fits the default disc with room around it, and a finger fits the orb.
    @Test func meterFitsTheDisc() {
        let disc = Theme.Size.voiceOrb * VoiceOrb.sphere
        let bars = CGFloat(VoiceOrbPose.profile.count)
        #expect(bars * Theme.Size.voiceLevelBar + (bars - 1) * Theme.Size.voiceLevelGap < disc * 0.75)
        #expect(Theme.Size.voiceLevelHeight < disc * 0.6)
        #expect(Theme.Size.voiceOrb >= Theme.Size.tapTarget)
    }

    /// A voice that is off says why with a symbol; the moving phases draw their meter instead.
    @Test func glyphsAreTheVocabulary() {
        #expect(VoiceOrb.Phase.muted.glyph == Theme.Icon.voiceMute)
        #expect(VoiceOrb.Phase.paused.glyph == Theme.Icon.pause)
        #expect(VoiceOrb.Phase.allCases.filter { $0.glyph != nil } == [.paused, .muted])
        for phase in [VoiceOrb.Phase.paused, .muted] { #expect(VoiceOrbPose.make(phase, at: 3, level: 1).bars.isEmpty) }
    }

    /// The paused and muted spheres are gray with a white glyph on them, and no sheen to wash it out: ≥ 3:1 (a graphic)
    /// against every stop of the gray, in light and in dark.
    @Test func glyphReadsOnTheGraySphere() {
        func contrast(_ a: Color, _ b: Color, _ env: EnvironmentValues) -> Double {
            func l(_ c: Color) -> Double {
                let r = c.resolve(in: env)
                return 0.2126 * Double(r.linearRed) + 0.7152 * Double(r.linearGreen) + 0.0722 * Double(r.linearBlue)
            }
            let (x, y) = (l(a), l(b))
            return (max(x, y) + 0.05) / (min(x, y) + 0.05)
        }
        var light = EnvironmentValues(), dark = EnvironmentValues()
        light.colorScheme = .light
        dark.colorScheme = .dark
        // The two appearances do resolve apart, so the check covers both grays.
        #expect(Theme.Hue.gray.resolve(in: light) != Theme.Hue.gray.resolve(in: dark))
        for phase in [VoiceOrb.Phase.paused, .muted] {
            #expect(VoiceOrb.sheen(phase) == 0)
            for env in [light, dark] {
                for stop in VoiceOrb.litColors(phase) {
                    let c = contrast(Theme.onSolid, stop, env)
                    #expect(c >= 3, "\(phase) \(env.colorScheme): \(c)")
                }
            }
        }
    }

    /// The speaking BeautifulUI disc is the persona's color: its bars are white, or near black on a light color.
    @Test func meterInkReadsOnThePersona() {
        var env = EnvironmentValues()
        env.colorScheme = .light
        #expect(VoiceOrb.ink(on: ColorSpec.blue.accent, in: env) == Theme.onSolid)
        #expect(VoiceOrb.ink(on: Color(light: 0xF2C230, dark: 0xF2C230), in: env) != Theme.onSolid)
        env.colorScheme = .dark
        #expect(VoiceOrb.ink(on: ColorSpec.coral.accent, in: env) == Theme.onSolid)
    }
}
