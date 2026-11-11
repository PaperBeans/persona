#if os(iOS) || os(visionOS)
import PersonaKit
import UIKit

/// ↓ Jump to Latest, run on the chat's scroll view itself (#3027). It stops a glide where it is, puts the list a
/// screen short of the newest end when it is far (`ChatJump`), and moves it the rest of the way frame by frame, so
/// it starts the moment the tap lands, takes as long as `ChatJump` says and says when it is over. SwiftUI's
/// `ScrollPosition` is not asked: during a glide it may still hold the newest end from the last time the app went
/// there, and asking for it again changed nothing (docs/lessons/ios.md「Jump to Latest during a glide」).
/// A finger taking the list ends it where it is.
@MainActor final class ChatJumpRun: NSObject {
    private weak var list: UIScrollView?
    private var link: CADisplayLink?
    private var from: CGFloat = 0
    private var began: CFTimeInterval = 0
    private var seconds: Double = 0
    private var done: ((Bool) -> Void)?

    /// A jump is moving the list.
    var running: Bool { link != nil }

    /// The newest end of the flipped list (offset 0 of its content, under its inset).
    static func newest(_ list: UIScrollView) -> CGFloat { -list.adjustedContentInset.top }

    /// Stops `list` where it is (a glide too) and takes it to the newest end. `done(true)` once there, `done(false)` if
    /// a finger took the list first or another jump replaced this one. Reduce Motion: there at once.
    func start(_ list: UIScrollView, done: @escaping (Bool) -> Void) {
        end(arrived: false)
        // Setting the offset it already has, through the animated-scroll API, is UIKit's way to stop a glide.
        list.setContentOffset(list.contentOffset, animated: false)
        let newest = Self.newest(list)
        let plan = ChatJump.plan(gap: list.contentOffset.y - newest, screen: list.bounds.height)
        switch plan {
        case .none:
            put(list, newest)
            return done(true)
        case .glide(let s):
            seconds = s
        case .leap(let gap, let s):
            put(list, newest + gap)
            seconds = s
        }
        if Theme.Motion.reduced {
            put(list, newest)
            return done(true)
        }
        self.list = list
        self.done = done
        from = list.contentOffset.y
        began = CACurrentMediaTime()
        let link = CADisplayLink(target: self, selector: #selector(tick))
        link.add(to: .main, forMode: .common)
        self.link = link
    }

    /// Ends a running jump where it is.
    func cancel() { end(arrived: false) }

    @objc private func tick() {
        guard let list else { return end(arrived: false) }
        if list.isTracking { return end(arrived: false) } // the user has the list
        let t = seconds > 0 ? (CACurrentMediaTime() - began) / seconds : 1
        // The newest end again each frame: an inset that changed (the keyboard, the composer growing) moves it.
        let newest = Self.newest(list)
        put(list, from + (newest - from) * CGFloat(ChatJump.progress(t)))
        if t >= 1 { end(arrived: true) }
    }

    private func put(_ list: UIScrollView, _ y: CGFloat) {
        list.contentOffset = CGPoint(x: list.contentOffset.x, y: y)
    }

    private func end(arrived: Bool) {
        link?.invalidate()
        link = nil
        list = nil
        let done = self.done
        self.done = nil
        done?(arrived)
    }
}
#endif
