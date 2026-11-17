import SwiftUI

extension EnvironmentValues {
    /// The edge the system puts its bars on, down the side, under the shell (iPhone Duo's outer display, and its inner
    /// display on its side, docs/apple/ios.md「iPhone Duo」): the shell's top bars become a rail there (`ShellSideRail`). nil:
    /// bars across the top, as on every other iPhone and on the inner display upright.
    @Entry var shellRailEdge: HorizontalEdge?
}

#if os(iOS) || os(visionOS)
extension View {
    /// Where the system presents bars vertically (`toolbarVerticalEdge`, iOS 27.1), nil when it does not: always nil
    /// before iOS 27.1 and when built with Xcode 26 (Swift ≤ 6.3 has no such key path).
    func readsVerticalBarEdge(_ edge: Binding<HorizontalEdge?>) -> some View { modifier(VerticalBarEdgeReader(edge: edge)) }
}

private struct VerticalBarEdgeReader: ViewModifier {
    @Binding var edge: HorizontalEdge?

    func body(content: Content) -> some View {
        #if compiler(>=6.4) && os(iOS)
        if #available(iOS 27.1, *) {
            content.modifier(SystemVerticalBarEdge(edge: $edge))
        } else {
            content
        }
        #else
        content
        #endif
    }
}

#if compiler(>=6.4) && os(iOS)
@available(iOS 27.1, *)
private struct SystemVerticalBarEdge: ViewModifier {
    @Environment(\.toolbarVerticalEdge) private var system
    @Binding var edge: HorizontalEdge?

    func body(content: Content) -> some View {
        content.onChange(of: system, initial: true) { _, e in edge = e }
    }
}
#endif

/// The shell's top bar where the system puts its bars down the side (iPhone Duo, `shellRailEdge`): the column beside the
/// status bar, with the switch as the Duo's tab bar (#4231) in the column's lower middle, as a tab bar down the side of the
/// outer display stands, not a small block at the foot (the owner's reference), and the page's buttons (the face, the
/// page's action) under it at the foot, where the thumb of the hand holding the phone reaches them (#1057). Where there
/// are no buttons (the open Duo's tabs) the tab bar's foot is a share of the column above its foot
/// (`Theme.Shell.sideTabBarFoot`). All of it is centred on the status bar (the column less the strip at the screen's edge,
/// `Theme.Shell.sideColumnEdge`). The column is the side's safe area, so the content stays clear of it; its top is the
/// status bar's and the camera's.
struct ShellSideRail<Buttons: View, Switcher: View>: View {
    let edge: HorizontalEdge
    @ViewBuilder var buttons: Buttons
    @ViewBuilder var switcher: Switcher

    var body: some View {
        // The column is what the page gives up on that side: its width with the side's safe area, less its width
        // without (a reader that ignores the safe area reports no inset of its own there).
        GeometryReader { inside in
            GeometryReader { whole in
                let column = max(0, whole.size.width - inside.size.width)
                VStack(spacing: Theme.Space.m) {
                    switcher.padding(.bottom, Theme.Space.s)
                    buttons
                }
                .frame(width: TabSwitchLayout.sideColumn(inset: column))
                // Off the strip at the screen's edge, as the status bar is.
                .padding(edge == .trailing ? .trailing : .leading, max(0, column - TabSwitchLayout.sideColumn(inset: column)))
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: edge == .trailing ? .bottomTrailing : .bottomLeading)
                .padding(.bottom, Buttons.self == EmptyView.self ? TabSwitchLayout.sideFoot(height: whole.size.height) : Theme.Space.l)
            }
            .ignoresSafeArea(.container, edges: edge == .trailing ? .trailing : .leading)
        }
    }
}
#endif
