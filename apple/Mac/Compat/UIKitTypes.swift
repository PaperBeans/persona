#if os(macOS)
import AppKit
import SwiftUI
import UniformTypeIdentifiers
import IOKit.ps

// The UIKit names the shared code uses, as thin AppKit stand-ins (docs/apple/mac.md「Approach」). Only what the shared code
// actually calls is here; anything a page needs beyond this is a sign the page wants its own Mac version.

typealias UIColor = NSColor
typealias UIImage = NSImage
typealias UIFont = NSFont
typealias UIEdgeInsets = NSEdgeInsets

extension NSEdgeInsets {
    nonisolated static let zero = NSEdgeInsets(top: 0, left: 0, bottom: 0, right: 0)
}

// MARK: Colors

nonisolated enum UIUserInterfaceStyle: Sendable { case unspecified, light, dark }

/// What `UIColor { traits in … }` gets: only the appearance is asked for in this app.
nonisolated struct UITraitCollection: Sendable {
    var userInterfaceStyle: UIUserInterfaceStyle = .light
    var preferredContentSizeCategory: UIContentSizeCategory = .large

    init(userInterfaceStyle: UIUserInterfaceStyle = .light) { self.userInterfaceStyle = userInterfaceStyle }
    init(preferredContentSizeCategory: UIContentSizeCategory) { self.preferredContentSizeCategory = preferredContentSizeCategory }

    @MainActor static var current: UITraitCollection {
        UITraitCollection(userInterfaceStyle: NSApp?.effectiveAppearance.isDark == true ? .dark : .light)
    }
}

extension NSAppearance {
    nonisolated var isDark: Bool { bestMatch(from: [.aqua, .darkAqua, .vibrantDark, .accessibilityHighContrastDarkAqua]).map { [.darkAqua, .vibrantDark, .accessibilityHighContrastDarkAqua].contains($0) } ?? false }
}

extension NSColor {
    /// `UIColor { traits in … }`: resolved against the appearance it is drawn in.
    nonisolated convenience init(dynamicProvider: @escaping @Sendable (UITraitCollection) -> NSColor) {
        self.init(name: nil) { appearance in
            dynamicProvider(UITraitCollection(userInterfaceStyle: appearance.isDark ? .dark : .light))
        }
    }

    nonisolated func resolvedColor(with traits: UITraitCollection) -> NSColor {
        var out = self
        let appearance = NSAppearance(named: traits.userInterfaceStyle == .dark ? .darkAqua : .aqua) ?? NSAppearance.currentDrawing()
        appearance.performAsCurrentDrawingAppearance { out = self.usingColorSpace(.sRGB) ?? self }
        return out
    }

    // UIKit's semantic colors, mapped to the closest AppKit role.
    nonisolated static var systemBackground: NSColor { .windowBackgroundColor }
    nonisolated static var secondarySystemBackground: NSColor { .controlBackgroundColor }
    nonisolated static var tertiarySystemBackground: NSColor { .underPageBackgroundColor }
    nonisolated static var systemGroupedBackground: NSColor { .windowBackgroundColor }
    nonisolated static var secondarySystemGroupedBackground: NSColor { .controlBackgroundColor }
    nonisolated static var tertiarySystemGroupedBackground: NSColor { .underPageBackgroundColor }
    nonisolated static var label: NSColor { .labelColor }
    nonisolated static var secondaryLabel: NSColor { .secondaryLabelColor }
    nonisolated static var tertiaryLabel: NSColor { .tertiaryLabelColor }
    nonisolated static var quaternaryLabel: NSColor { .quaternaryLabelColor }
    nonisolated static var separator: NSColor { .separatorColor }
    nonisolated static var opaqueSeparator: NSColor { .gridColor }
    nonisolated static var link: NSColor { .linkColor }
    nonisolated static var placeholderText: NSColor { .placeholderTextColor }
    nonisolated static var tintColor: NSColor { .controlAccentColor }
    // iOS's fills: translucent grays over the content, UIKit's own values in each appearance (#1169). They used to be
    // the label color at 18–36 %, two to four times the phone's: every chip, well and avatar looked pressed. Each is
    // still stronger than AppKit's fill of the same rank (black or white at 3–10 %), so none goes faint.
    nonisolated static var systemFill: NSColor { phoneFill(120, light: 0.2, dark: 0.36) }
    nonisolated static var secondarySystemFill: NSColor { phoneFill(120, light: 0.16, dark: 0.32) }
    nonisolated static var tertiarySystemFill: NSColor { phoneFill(118, light: 0.12, dark: 0.24) }
    nonisolated static var quaternarySystemFill: NSColor { phoneFill(116, 118, light: 0.08, dark: 0.18) }
    nonisolated static var systemGray6: NSColor { .init(dynamicProvider: { $0.userInterfaceStyle == .dark ? NSColor(white: 0.11, alpha: 1) : NSColor(white: 0.95, alpha: 1) }) }
    nonisolated static var systemGray5: NSColor { .init(dynamicProvider: { $0.userInterfaceStyle == .dark ? NSColor(white: 0.17, alpha: 1) : NSColor(white: 0.9, alpha: 1) }) }
    nonisolated static var systemGray4: NSColor { .init(dynamicProvider: { $0.userInterfaceStyle == .dark ? NSColor(white: 0.23, alpha: 1) : NSColor(white: 0.82, alpha: 1) }) }

    /// One of UIKit's fills: a blue-tinted gray (`gray`, or `darkGray` in dark mode, of 255; blue 128) at `light` / `dark`.
    private nonisolated static func phoneFill(_ gray: CGFloat, _ darkGray: CGFloat? = nil, light: CGFloat, dark: CGFloat) -> NSColor {
        .init(dynamicProvider: { traits in
            let isDark = traits.userInterfaceStyle == .dark
            let g = (isDark ? darkGray ?? gray : gray) / 255
            return NSColor(srgbRed: g, green: g, blue: 128 / 255, alpha: isDark ? dark : light)
        })
    }
}

extension Color {
    nonisolated init(uiColor: NSColor) { self.init(nsColor: uiColor) }
}

// MARK: Fonts

nonisolated enum UIContentSizeCategory: Sendable {
    case extraSmall, small, medium, large, extraLarge, extraExtraLarge, extraExtraExtraLarge
    case accessibilityMedium, accessibilityLarge, accessibilityExtraLarge, accessibilityExtraExtraLarge, accessibilityExtraExtraExtraLarge

    init(_ size: DynamicTypeSize) {
        switch size {
        case .xSmall: self = .extraSmall
        case .small: self = .small
        case .medium: self = .medium
        case .large: self = .large
        case .xLarge: self = .extraLarge
        case .xxLarge: self = .extraExtraLarge
        case .xxxLarge: self = .extraExtraExtraLarge
        case .accessibility1: self = .accessibilityMedium
        case .accessibility2: self = .accessibilityLarge
        case .accessibility3: self = .accessibilityExtraLarge
        case .accessibility4: self = .accessibilityExtraExtraLarge
        case .accessibility5: self = .accessibilityExtraExtraExtraLarge
        @unknown default: self = .large
        }
    }

    /// The Mac's body size at this category (`Theme.Level.body` is 13 at the default, .large): a point a step, as the
    /// Mac's text sizes go (#779, #833). Small / Standard / Large are .medium / .large / .extraLarge.
    var macBody: CGFloat {
        switch self {
        case .extraSmall: 10
        case .small: 11
        case .medium: 12
        case .large: 13
        case .extraLarge: 14
        case .extraExtraLarge: 15
        case .extraExtraExtraLarge: 16
        case .accessibilityMedium: 18
        case .accessibilityLarge: 20
        case .accessibilityExtraLarge: 22
        case .accessibilityExtraExtraLarge: 24
        case .accessibilityExtraExtraExtraLarge: 26
        }
    }
}

/// macOS has no Dynamic Type; the app's own Small / Standard / Large still moves the size category, and this scales by the Mac's
/// body at it (`macBody`: a point a step around 13), not iOS's table, which made a step 6–12 % on the Mac (#833).
/// The variant without traits takes the category from the app's text size setting (`TextSize.current` applied to
/// `.large`), so the fonts made with it — the code/file viewer's — follow Small / Standard / Large (#3612).
nonisolated struct UIFontMetrics {
    init(forTextStyle: NSFont.TextStyle) {}
    func scaledValue(for value: CGFloat, compatibleWith traits: UITraitCollection?) -> CGFloat {
        value * (traits?.preferredContentSizeCategory.macBody ?? 13) / 13
    }
    @MainActor func scaledValue(for value: CGFloat) -> CGFloat {
        let size = TextSize.current.applied(to: .large)
        let traits = UITraitCollection(preferredContentSizeCategory: UIContentSizeCategory(size))
        return scaledValue(for: value, compatibleWith: traits)
    }
}

extension NSFontDescriptor.SymbolicTraits {
    // UIKit's names for the traits.
    nonisolated static var traitItalic: Self { .italic }
    nonisolated static var traitBold: Self { .bold }
    nonisolated static var traitMonoSpace: Self { .monoSpace }
}

// MARK: Images

extension NSImage {
    convenience init?(systemName: String) {
        self.init(systemSymbolName: systemName, accessibilityDescription: nil)
    }

    nonisolated convenience init(cgImage: CGImage) {
        self.init(cgImage: cgImage, size: NSSize(width: cgImage.width, height: cgImage.height))
    }

    /// The bitmap behind it (UIImage's `cgImage`).
    nonisolated var cgImage: CGImage? { cgImage(forProposedRect: nil, context: nil, hints: nil) }

    /// Mac images are 1× in points here: sizes come from pixels (`init(cgImage:)`).
    nonisolated var scale: CGFloat { 1 }

    nonisolated func jpegData(compressionQuality: CGFloat) -> Data? {
        guard let cg = cgImage else { return nil }
        return NSBitmapImageRep(cgImage: cg).representation(using: .jpeg, properties: [.compressionFactor: compressionQuality])
    }

    nonisolated func pngData() -> Data? {
        guard let cg = cgImage else { return nil }
        return NSBitmapImageRep(cgImage: cg).representation(using: .png, properties: [:])
    }

    /// Decoding ahead of drawing is what AppKit does on its own; the bitmap is forced here so the first draw does not.
    nonisolated func preparingForDisplay() -> NSImage? {
        guard let cg = cgImage else { return nil }
        return NSImage(cgImage: cg)
    }

    nonisolated func preparingThumbnail(of size: CGSize) -> NSImage? {
        guard size.width >= 1, size.height >= 1 else { return nil }
        return UIGraphicsImageRenderer(size: size).image { _ in draw(in: CGRect(origin: .zero, size: size)) }
    }

    /// UIKit draws images upright in its flipped contexts; AppKit only does when asked to respect the flip.
    nonisolated func draw(in rect: CGRect) { draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil) }
}

extension Image {
    init(uiImage: NSImage) { self.init(nsImage: uiImage) }
}

/// `UIGraphicsImageRenderer(size:).image { _ in … }`: a flipped (top-left origin) bitmap context, like UIKit's.
nonisolated final class UIGraphicsImageRendererFormat {
    var scale: CGFloat = 2
    var opaque = false
    nonisolated static func `default`() -> UIGraphicsImageRendererFormat { UIGraphicsImageRendererFormat() }
}

nonisolated struct UIGraphicsImageRendererContext {
    var cgContext: CGContext
}

nonisolated struct UIGraphicsImageRenderer {
    var size: CGSize
    var format: UIGraphicsImageRendererFormat

    nonisolated init(size: CGSize, format: UIGraphicsImageRendererFormat = .default()) {
        self.size = size
        self.format = format
    }

    nonisolated func image(_ draw: (UIGraphicsImageRendererContext) -> Void) -> NSImage {
        let scale = max(format.scale, 1)
        let w = max(Int((size.width * scale).rounded()), 1), h = max(Int((size.height * scale).rounded()), 1)
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8, samplesPerPixel: 4,
                                         hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
              let ns = NSGraphicsContext(bitmapImageRep: rep) else { return NSImage(size: size) }
        rep.size = size
        let flipped = NSGraphicsContext(cgContext: ns.cgContext, flipped: true)
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = flipped
        let cg = flipped.cgContext
        cg.translateBy(x: 0, y: size.height)
        cg.scaleBy(x: 1, y: -1)
        draw(UIGraphicsImageRendererContext(cgContext: cg))
        NSGraphicsContext.restoreGraphicsState()
        let image = NSImage(size: size)
        image.addRepresentation(rep)
        return image
    }
}

// MARK: Pasteboard, application, device

/// `UIPasteboard.general`: the Mac's general pasteboard.
@MainActor final class UIPasteboard {
    static let general = UIPasteboard()
    private var board: NSPasteboard { .general }

    var string: String? {
        get { board.string(forType: .string) }
        set {
            board.clearContents()
            if let newValue { board.setString(newValue, forType: .string) }
        }
    }

    var url: URL? {
        get { board.string(forType: .URL).flatMap(URL.init(string:)) ?? board.string(forType: .string).flatMap(URL.init(string:)) }
        set {
            board.clearContents()
            if let newValue { board.writeObjects([newValue as NSURL]) }
        }
    }

    var image: NSImage? {
        get { NSImage(pasteboard: board) }
        set {
            board.clearContents()
            if let newValue { board.writeObjects([newValue]) }
        }
    }

    var changeCount: Int { board.changeCount }
    var hasStrings: Bool { board.canReadItem(withDataConformingToTypes: [UTType.utf8PlainText.identifier, UTType.plainText.identifier]) }
    var hasImages: Bool { NSImage.canInit(with: board) }
    var hasURLs: Bool { board.canReadObject(forClasses: [NSURL.self], options: nil) }
}

/// `UIApplication.shared`: opening URLs and the app's state.
@MainActor final class UIApplication {
    static let shared = UIApplication()
    nonisolated static let openSettingsURLString = "x-apple.systempreferences:com.apple.preference.security"
    nonisolated static let openNotificationSettingsURLString = "x-apple.systempreferences:com.apple.Notifications-Settings.extension"
    static let didBecomeActiveNotification = NSApplication.didBecomeActiveNotification
    static let didEnterBackgroundNotification = NSApplication.didResignActiveNotification
    static let willEnterForegroundNotification = NSApplication.willBecomeActiveNotification

    enum State { case active, inactive, background }
    var applicationState: State {
        guard let app = NSApp else { return .inactive }
        return app.isActive ? .active : (app.isHidden ? .background : .inactive)
    }

    func open(_ url: URL, options: [String: Any] = [:], completionHandler: ((Bool) -> Void)? = nil) {
        completionHandler?(LinkPolicy.open(url))
    }

    @discardableResult
    func open(_ url: URL) async -> Bool { LinkPolicy.open(url) }

    func canOpenURL(_ url: URL) -> Bool { NSWorkspace.shared.urlForApplication(toOpen: url) != nil }

    /// Only `resignFirstResponder` is sent this way: drop the key window's focus.
    @discardableResult
    func sendAction(_ action: Selector, to target: Any?, from sender: Any?, for event: Any?) -> Bool {
        NSApp?.keyWindow?.makeFirstResponder(nil)
        return true
    }
}

nonisolated enum UIUserInterfaceIdiom { case unspecified, phone, pad, mac }

@MainActor final class UIDevice {
    static let current = UIDevice()
    var name: String {
        #if DEBUG
        // Screenshots (`-macSnapshot`) go into PRs: not this Mac's own name, which is often its owner's (it shows in
        // the SSH key's comment, Settings › hub › SSH Tunnel).
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil { return "Mac" }
        #endif
        return Host.current().localizedName ?? "Mac"
    }
    var model: String { "Mac" }
    var systemName: String { "macOS" }
    var systemVersion: String {
        let v = ProcessInfo.processInfo.operatingSystemVersion
        return "\(v.majorVersion).\(v.minorVersion).\(v.patchVersion)"
    }
    var userInterfaceIdiom: UIUserInterfaceIdiom { .mac }

    // Battery: a MacBook's internal battery from IOKit's power sources; a desktop Mac reports none (-1, unknown).
    enum BatteryState: Int { case unknown, unplugged, charging, full }
    var isBatteryMonitoringEnabled = true
    var batteryLevel: Float { MacBattery.read()?.level ?? -1 }
    var batteryState: BatteryState { MacBattery.read()?.state ?? .unknown }
}

private enum MacBattery {
    static func read() -> (level: Float, state: UIDevice.BatteryState)? {
        guard let info = IOPSCopyPowerSourcesInfo()?.takeRetainedValue(),
              let list = IOPSCopyPowerSourcesList(info)?.takeRetainedValue() as? [CFTypeRef] else { return nil }
        for ps in list {
            guard let d = IOPSGetPowerSourceDescription(info, ps)?.takeUnretainedValue() as? [String: Any],
                  d[kIOPSTypeKey] as? String == kIOPSInternalBatteryType,
                  let cur = d[kIOPSCurrentCapacityKey] as? Int, let max = d[kIOPSMaxCapacityKey] as? Int, max > 0 else { continue }
            let charging = d[kIOPSIsChargingKey] as? Bool ?? false
            let onAC = d[kIOPSPowerSourceStateKey] as? String == kIOPSACPowerValue
            let state: UIDevice.BatteryState = cur >= max && onAC ? .full : charging ? .charging : onAC ? .full : .unplugged
            return (Float(cur) / Float(max), state)
        }
        return nil
    }
}

/// Taking a photo is an iPhone's (`UIImagePickerController`): the Mac's composer attaches files and pasted images
/// instead, so the camera is never available and its entry does not show.
@MainActor final class UIImagePickerController {
    enum SourceType { case camera, photoLibrary, savedPhotosAlbum }
    static func isSourceTypeAvailable(_ type: SourceType) -> Bool { false }
}

/// UIResponder only appears as `#selector(UIResponder.resignFirstResponder)`.
typealias UIResponder = NSResponder

// MARK: Haptics: none on a Mac.

final class UIImpactFeedbackGenerator {
    enum FeedbackStyle { case light, medium, heavy, soft, rigid }
    init(style: FeedbackStyle = .medium) {}
    func impactOccurred() {}
    func impactOccurred(intensity: CGFloat) {}
    func prepare() {}
}

final class UINotificationFeedbackGenerator {
    enum FeedbackType { case success, warning, error }
    init() {}
    func notificationOccurred(_ type: FeedbackType) {}
    func prepare() {}
}

final class UISelectionFeedbackGenerator {
    init() {}
    func selectionChanged() {}
    func prepare() {}
}
#endif

extension NSScrollView {
    /// UIKit's "a finger is on it" (ChatView holds the chat still under one): a Mac scroll view has no touch to hold it,
    /// and a scroll in progress shows up in the view's own scroll phase.
    var isTracking: Bool { false }
}
