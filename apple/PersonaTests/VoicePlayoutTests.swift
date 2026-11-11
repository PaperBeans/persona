import Testing
@testable import Persona

/// The voice's jitter buffer (#4430): nothing plays until the lead has gathered or the wait ran out, then everything held
/// goes at once; while the voice plays, each piece goes straight behind it; a player that ran dry gathers again.
struct VoicePlayoutTests {
    /// 24 kHz: the lead is 4800 frames, a 40 ms piece 960.
    private let piece = 960

    @Test func theLeadIsTwoHundredMillisecondsAtTheVoicesRate() {
        #expect(VoicePlayout<Int>(rate: 24000).leadFrames == 4800)
        #expect(VoicePlayout<Int>(rate: 16000).leadFrames == 3200)
        #expect(VoicePlayout<Int>.wait > VoicePlayout<Int>.lead, "a voice sent as fast as it plays fills the lead before the wait runs out")
    }

    @Test func piecesAreHeldUntilTheLeadThenPlayTogetherInOrder() {
        var playout = VoicePlayout<Int>(rate: 24000)
        for n in 1...4 {
            #expect(playout.add(n, frames: piece, playing: false).isEmpty)
            #expect(playout.isHolding)
        }
        #expect(playout.add(5, frames: piece, playing: false) == [1, 2, 3, 4, 5])
        #expect(!playout.isHolding)
    }

    @Test func oneLongPieceFromAFastServicePlaysAtOnce() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 24000, playing: false) == [1])
        #expect(!playout.isHolding)
    }

    @Test func whileTheVoicePlaysEachPieceGoesStraightBehindIt() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: piece, playing: true) == [1])
        #expect(playout.add(2, frames: piece, playing: true) == [2])
        #expect(!playout.isHolding)
    }

    @Test func aPlayerThatRanDryGathersAgain() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 4800, playing: false) == [1])
        #expect(playout.add(2, frames: piece, playing: true) == [2])
        // The player caught up with the stream: the next piece waits for a lead of its own.
        #expect(playout.add(3, frames: piece, playing: false).isEmpty)
        #expect(playout.add(4, frames: 3840, playing: false) == [3, 4])
    }

    @Test func aShortReplyPlaysWhenTheWaitRunsOut() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: piece, playing: false).isEmpty)
        #expect(playout.add(2, frames: piece, playing: false).isEmpty)
        #expect(playout.release() == [1, 2])
        #expect(!playout.isHolding)
        #expect(playout.release().isEmpty)
        // The count starts over: the next reply gathers a whole lead again.
        #expect(playout.add(3, frames: 3840, playing: false).isEmpty)
    }

    @Test func clearDropsWhatIsHeld() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 3840, playing: false).isEmpty)
        playout.clear()
        #expect(!playout.isHolding)
        #expect(playout.add(2, frames: piece, playing: false).isEmpty, "what was cleared does not count toward the lead")
    }
}
