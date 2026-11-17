#if os(iOS)
import Foundation
import SwiftUI
import Testing
@testable import Persona

/// iPhone Duo's tab bar in the Separate layout (#4231, `TabSwitchLayout`): the same items on both displays (the page's
/// symbol over its name), down the system's side column, centred on the status bar and in its lower middle, or along the
/// foot of the inner display upright, where the pages keep its room as for the system's tab bar. Settings › Appearance ›
/// Sidebar (Merged / Separate) chooses it, kept on this device.
@MainActor
struct TabSwitchLayoutTests {
    /// Where the tab bar stands in each posture of the open Duo, and nowhere on the iPad, which keeps the system's tab bar.
    @Test func itStandsWhereTheSystemsBarsAre() {
        #expect(TabSwitchLayout.placement(phone: true, barEdge: nil) == .bottom) // open, upright: bars across the top
        #expect(TabSwitchLayout.placement(phone: true, barEdge: .trailing) == .rail(.trailing)) // open, on its side
        #expect(TabSwitchLayout.placement(phone: true, barEdge: .leading) == .rail(.leading))
        #expect(TabSwitchLayout.placement(phone: false, barEdge: nil) == nil) // the iPad
        #expect(TabSwitchLayout.placement(phone: false, barEdge: .trailing) == nil)
    }

    /// An item is a tap target and more (its symbol over its name), at every text size (a bar of fixed items,
    /// DESIGN_SYSTEM.md 6.2); the side bar fits the side column the status bar is centred on.
    @Test func theItemsAndTheBarsSizes() {
        #expect(Theme.Shell.tabBarItemWidth >= Theme.Size.tapTarget)
        #expect(Theme.Shell.tabBarItemHeight >= Theme.Size.tapTarget)
        #expect(Theme.Shell.switchTypeSize == .large) // the default text size, as the system's tab bar keeps
        #expect(TabSwitchLayout.sideWidth == 64)
        #expect(TabSwitchLayout.sideHeight(items: 3) == 184)
        #expect(TabSwitchLayout.sideHeight(items: 2) == 128)
        #expect(TabSwitchLayout.bottomHeight == 64)
        #expect(TabSwitchLayout.bottomWidth(items: 3) == 184)
        #expect(TabSwitchLayout.items(messages: true) == 3)
        #expect(TabSwitchLayout.items(messages: false) == 2)
        // The Duo's 84 pt inset (both displays) less the screen edge's strip: 70 pt, the bar within it.
        #expect(TabSwitchLayout.sideColumn(inset: 84) == 70)
        #expect(TabSwitchLayout.sideColumn(inset: 84) >= TabSwitchLayout.sideWidth)
        #expect(TabSwitchLayout.sideColumn(inset: 0) == TabSwitchLayout.sideWidth) // never narrower than the bar
    }

    /// Down the side it is centred on the status bar: measured on both displays, the clock 49 pt from the screen's edge.
    @Test func theSideBarIsCentredOnTheStatusBar() {
        let inset: CGFloat = 84
        let column = TabSwitchLayout.sideColumn(inset: inset)
        let centreFromEdge = (inset - column) + column / 2
        #expect(centreFromEdge == 49)
    }

    /// In the lower middle of the column, not a small block at its foot: a quarter of the column under it.
    @Test func theSideBarStandsInTheLowerMiddle() {
        let height: CGFloat = 644 // the outer display's 678 pt less its 34 pt foot
        let foot = TabSwitchLayout.sideFoot(height: height)
        #expect(foot == height * Theme.Shell.sideTabBarFoot)
        let top = height - foot - TabSwitchLayout.sideHeight(items: 3)
        #expect(top > height * 0.4 && height - foot < height * 0.8) // its middle in the column's lower half
        #expect(TabSwitchLayout.sideFoot(height: 0) == Theme.Space.l) // never on the foot itself
    }

    /// Along the foot upright the pages keep the bar's room, as the system's tab bar makes them keep; with the keyboard up
    /// the bar is away and the room goes; on its side there is none.
    @Test func theRoomUnderTheBottomBar() {
        #expect(TabSwitchLayout.bottomRoom(bottomBar: true, keyboardUp: false) == TabSwitchLayout.bottomHeight + TabSwitchLayout.bottomGap)
        #expect(TabSwitchLayout.bottomRoom(bottomBar: true, keyboardUp: true) == 0)
        #expect(TabSwitchLayout.bottomRoom(bottomBar: false, keyboardUp: false) == 0)
    }

    /// The choice is Settings › Appearance › Sidebar's, this device's own: picking Separate is written at once and read
    /// back by a new reader of the same defaults, as the app's root after a relaunch; nothing picked stays Merged on the
    /// open Duo and Separate on its outer display (#4206's default, unchanged).
    @Test func theChoiceIsKept() throws {
        let suite = "persona.tests.tab-switch.\(UUID().uuidString)"
        let store = try #require(UserDefaults(suiteName: suite))
        defer { store.removePersistentDomain(forName: suite) }
        @StoredValue(NavigationLayout.key, store: store) var picked = ""
        #expect(NavigationLayout.effective(picked: picked, regular: true) == .sidebar) // the open Duo: Merged
        #expect(NavigationLayout.effective(picked: picked, regular: false) == .separate) // its outer display
        picked = NavigationLayout.separate.rawValue
        #expect(store.string(forKey: NavigationLayout.key) == "separate")
        let relaunched = try #require(UserDefaults(suiteName: suite))
        @StoredValue(NavigationLayout.key, store: relaunched) var again = ""
        #expect(again == "separate")
        // A pick holds on both displays: folding and unfolding keep the tabs.
        #expect(NavigationLayout.effective(picked: again, regular: true) == .separate)
        #expect(NavigationLayout.effective(picked: again, regular: false) == .separate)
        picked = NavigationLayout.sidebar.rawValue
        #expect(store.string(forKey: NavigationLayout.key) == "sidebar")
    }

    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    /// The tabs hide the system's tab bar on a phone (the open Duo) and the Duo's stands in its place; every tab's page
    /// hides it and keeps the bottom bar's room (`PhoneTabBarHidden`), or the system's bar comes back on that tab, or its
    /// composer goes under the bar.
    @Test func everyTabHidesTheSystemsBarOnAPhone() throws {
        let source = try String(contentsOf: Self.apple.appending(path: "Persona/Views/RootView.swift"), encoding: .utf8)
        #expect(source.contains(".modifier(TabSwitchOverlay())"))
        for tab in ["struct AssistantRootTab", "struct AgentsRootTab", "struct FeishuRootTab"] {
            let start = try #require(source.range(of: tab), "\(tab) not in RootView.swift")
            let rest = source[start.upperBound...]
            let body = rest[..<(rest.range(of: "\n}\n")?.upperBound ?? rest.endIndex)]
            #expect(body.contains("PhoneTabBarHidden()"), "\(tab) shows the system's tab bar under the Duo's")
        }
    }
}
#endif
