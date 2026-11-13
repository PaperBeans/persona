import SwiftUI

/// Marks a drag while it is in progress and briefly after it ends, so a Button's action can require a deliberate tap.
/// SwiftUI Buttons can otherwise treat a short flick as a tap even when the finger moved far enough to scroll.
public struct DeliberateTap: ViewModifier {
    @Binding var dragging: Bool
    let enabled: Bool
    @State private var resetTask: Task<Void, Never>?

    public init(dragging: Binding<Bool>, enabled: Bool = true) {
        _dragging = dragging
        self.enabled = enabled
    }

    public func body(content: Content) -> some View {
        if enabled {
            content.simultaneousGesture(
                DragGesture(minimumDistance: 10)
                    .onChanged { _ in
                        resetTask?.cancel()
                        dragging = true
                    }
                    .onEnded { _ in
                        resetTask = Task {
                            try? await Task.sleep(for: .milliseconds(250))
                            guard !Task.isCancelled else { return }
                            dragging = false
                        }
                    }
            )
        } else {
            content
        }
    }
}

public extension View {
    /// Use the binding in the Button's action: skip picking while it is true. The ScrollView still receives the drag.
    func deliberateTap(_ dragging: Binding<Bool>, enabled: Bool = true) -> some View {
        modifier(DeliberateTap(dragging: dragging, enabled: enabled))
    }
}
