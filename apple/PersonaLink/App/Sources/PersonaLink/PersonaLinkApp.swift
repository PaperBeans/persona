import SwiftUI

@main
struct PersonaLinkApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let model = LinkModel.shared

    var body: some Scene {
        MenuBarExtra {
            MenuView(model: model)
        } label: {
            MenuBarIcon(look: model.look)
        }
        .menuBarExtraStyle(.window)

        // A `persona://link?…` link opened anywhere (Notes, AirDrop, Mail, Safari) opens this window with the link filled
        // in (Info.plist CFBundleURLTypes); pairing still waits for the 「配对」 (Pair) button.
        Window(linkText("Pair or Change Connection"), id: "pair") {
            PairView(model: model)
                .onOpenURL { url in
                    model.openLink(url)
                    NSApp.activate(ignoringOtherApps: true)
                }
        }
        .windowResizability(.contentSize)
        .defaultLaunchBehavior(.suppressed)
        .handlesExternalEvents(matching: ["*"])

        // What the hub calls this computer (`persona-link name`, docs/operations/link.md "The computer's name").
        Window(linkText("Rename Computer"), id: "rename") {
            RenameView(model: model)
        }
        .windowResizability(.contentSize)
        .defaultLaunchBehavior(.suppressed)
    }
}

/// The menu bar icon: the Persona helper as a template image, so it reads on light and dark menu bars alike; faded
/// while the link is not up, with a dot when something needs the user (LinkLook), as the Windows tray draws it.
struct MenuBarIcon: View {
    let look: LinkLook

    var body: some View {
        Image(nsImage: Self.image(look))
            .renderingMode(.template)
            .accessibilityLabel(linkText("Persona Link · \(StatePill.title(look))"))
    }

    /// The helper's outline (Resources/MenuBarIcon.png, 1x and 2x; scripts/assets/make_icon.py link).
    static let art: NSImage = Bundle.module.image(forResource: "MenuBarIcon") ?? NSImage()

    /// The art for `look`, drawn when shown so it is sharp at any display's scale.
    static func image(_ look: LinkLook) -> NSImage {
        let side: CGFloat = 18
        let image = NSImage(size: NSSize(width: side, height: side), flipped: false) { rect in
            art.draw(in: rect, from: .zero, operation: .sourceOver, fraction: look.faded ? 0.45 : 1)
            guard look.badge, let context = NSGraphicsContext.current else { return true }
            let r: CGFloat = 3.6, gap: CGFloat = 1.3
            let center = NSPoint(x: rect.maxX - r - 0.5, y: rect.minY + r + 0.5)
            func circle(_ radius: CGFloat) -> NSBezierPath {
                NSBezierPath(ovalIn: NSRect(x: center.x - radius, y: center.y - radius, width: radius * 2, height: radius * 2))
            }
            // A ring cut out of the drawing around the dot, so it reads apart from the outline.
            context.compositingOperation = .clear
            circle(r + gap).fill()
            context.compositingOperation = .sourceOver
            NSColor.black.setFill()
            circle(r).fill()
            return true
        }
        image.isTemplate = true
        return image
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--snapshot"), i + 1 < args.count {
            // Render the menu and the pairing window, light and dark, and quit (scripts/app/mac-link.sh snapshot).
            MainActor.assumeIsolated { Snapshots.render(to: URL(fileURLWithPath: args[i + 1])) }
            exit(0)
        }
        MainActor.assumeIsolated { LinkModel.shared.start() }
    }
}
