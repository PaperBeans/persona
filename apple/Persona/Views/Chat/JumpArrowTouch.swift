#if os(iOS)
import SwiftUI
import UIKit

/// ↓'s touch, taken by a UIKit view of its own laid over the arrow (#3027). On a phone the first tap on ↓'s SwiftUI
/// `Button` while the list still glided did nothing at all: the arrow stayed and the list glided on, so neither the
/// button nor the scroll view had the touch (TestFlight; the simulator's touches always reached it). This view is
/// over the list, not in it, so UIKit hands the touch straight to it and nothing in between can keep it: it acts the
/// moment the finger lands, as a keyboard's key does. The `Button` under it stays for its look and for VoiceOver, Full
/// Keyboard Access and Switch Control, which press it; this view is not an accessibility element. The pointer's hover
/// passes through to the button, so its highlight stays on iPad.
struct JumpArrowTouch: UIViewRepresentable {
    var action: () -> Void

    func makeUIView(context: Context) -> Pad {
        let pad = Pad()
        pad.action = action
        return pad
    }

    func updateUIView(_ pad: Pad, context: Context) { pad.action = action }

    final class Pad: UIView {
        var action: () -> Void = {}

        override init(frame: CGRect) {
            super.init(frame: frame)
            backgroundColor = .clear
            isAccessibilityElement = false
            isMultipleTouchEnabled = false
        }

        @available(*, unavailable)
        required init?(coder: NSCoder) { fatalError("not from a coder") }

        override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
            if event?.type == .hover { return false } // the button's own hover highlight
            return super.point(inside: point, with: event)
        }

        override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) { action() }
        // The rest of the touch is this view's too: nothing under it acts on it.
        override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent?) {}
        override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent?) {}
        override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent?) {}
    }
}
#endif
