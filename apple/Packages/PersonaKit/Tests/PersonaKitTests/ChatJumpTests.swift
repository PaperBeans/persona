import CoreGraphics
import Testing
@testable import PersonaKit

/// ↓ Jump to Latest's plan (#3027): how far the newest end is decides how the list gets there.
struct ChatJumpTests {
    private let screen: CGFloat = 700

    @Test func atTheNewestNothingMoves() {
        #expect(ChatJump.plan(gap: 0, screen: screen) == .none)
        #expect(ChatJump.plan(gap: 0.4, screen: screen) == .none)
        #expect(ChatJump.plan(gap: -3, screen: screen) == .none) // rubber band past the end
    }

    @Test func closeByTheWholeWayAnimates() {
        guard case .glide(let s) = ChatJump.plan(gap: 120, screen: screen) else { Issue.record("a short hop glides"); return }
        #expect(s >= ChatJump.shortest && s < ChatJump.longest)
        guard case .glide = ChatJump.plan(gap: screen * ChatJump.leapBeyond, screen: screen) else {
            Issue.record("a screen and a half still glides"); return
        }
    }

    @Test func farAwayLeapsToAScreenShortThenAnimates() {
        for gap in [screen * ChatJump.leapBeyond + 1, screen * 3, 250_000] {
            guard case .leap(let near, let s) = ChatJump.plan(gap: gap, screen: screen) else {
                Issue.record("\(gap) pt away leaps"); continue
            }
            #expect(near == screen * ChatJump.leapTo)
            #expect(near < gap)
            #expect(s == ChatJump.longest, "the last screen takes as long as any screen")
        }
    }

    /// However long the chat, the animated part is the same length: never a scroll of seconds.
    @Test func theAnimationNeverOutlastsTheLongest() {
        for gap: CGFloat in [1, 50, 300, 700, 1_049, 1_051, 10_000, 1_000_000] {
            switch ChatJump.plan(gap: gap, screen: screen) {
            case .none: Issue.record("\(gap) pt away moves")
            case .glide(let s), .leap(_, let s): #expect(s >= ChatJump.shortest && s <= ChatJump.longest)
            }
        }
    }

    @Test func aLongerHopTakesLonger() {
        #expect(ChatJump.seconds(100, screen: screen) < ChatJump.seconds(400, screen: screen))
        #expect(ChatJump.seconds(screen, screen: screen) == ChatJump.longest)
        #expect(ChatJump.seconds(screen * 4, screen: screen) == ChatJump.longest)
        #expect(ChatJump.seconds(0, screen: screen) == ChatJump.shortest)
    }

    /// The animated part starts at speed, never goes back or past the newest end, and ends exactly there.
    @Test func theCurveStartsFastAndSettles() {
        #expect(ChatJump.progress(0) == 0)
        #expect(ChatJump.progress(1) == 1)
        #expect(ChatJump.progress(-1) == 0 && ChatJump.progress(2) == 1)
        var last = 0.0
        for i in 1...100 {
            let p = ChatJump.progress(Double(i) / 100)
            #expect(p >= last && p <= 1)
            last = p
        }
        // Fast at first: a tenth of the time covers more than a quarter of the way.
        #expect(ChatJump.progress(0.1) > 0.25)
    }

    /// Before the list has measured itself: the whole way animates, never a leap to a guessed place.
    @Test func anUnmeasuredScreenGlides() {
        #expect(ChatJump.plan(gap: 5_000, screen: 0) == .glide(seconds: ChatJump.longest))
    }
}
