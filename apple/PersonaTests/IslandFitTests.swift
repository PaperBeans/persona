import SwiftUI
import Testing
@testable import Persona

/// The Dynamic Island's pieces fit the room the system gives them, with one, two, three and four digits
/// (docs/apple/live-activity.md「Sharing the Dynamic Island with other apps」). Each view is drawn at its own size (outside limited mode the island
/// offers no width: it grows with what it shows, up to the region) in the middle of a larger canvas: whatever it draws
/// must stay inside the region. The text never shortens or shrinks, so a number that would be cut spills out instead.
@MainActor
struct IslandFitTests {
    nonisolated static let counts: [Int] = [1, 12, 123, 1234]
    static func statusCells(_ n: Int) -> [IslandCell] { [.needsYou(n), .working(n), .unread(n)] }

    /// The drawn pixels' bounds, in points from the region's center, for `view` given `region`.
    private func ink(_ view: some View, region: CGSize) throws -> (rect: CGRect, radius: CGFloat) {
        let scale: CGFloat = 2
        let canvas = CGSize(width: region.width * 3, height: region.height * 2)
        let renderer = ImageRenderer(content: view
            .fixedSize()
            .frame(width: region.width, height: region.height)
            .frame(width: canvas.width, height: canvas.height)
            .environment(\.colorScheme, .dark))
        renderer.scale = scale
        let w = Int(ceil(canvas.width * scale)), h = Int(ceil(canvas.height * scale))
        var px = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try #require(CGContext(data: &px, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                         space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        // Keep the SwiftUI layout, but draw it into our bitmap instead of waiting for a cgImage provider pipeline.
        renderer.render(rasterizationScale: scale) { size, draw in
            #expect(size == canvas)
            ctx.saveGState()
            ctx.scaleBy(x: scale, y: scale)
            draw(ctx)
            ctx.restoreGState()
        }
        var minX = w, minY = h, maxX = -1, maxY = -1
        var radius: CGFloat = 0
        let cx = CGFloat(w) / 2, cy = CGFloat(h) / 2
        for y in 0..<h {
            for x in 0..<w where px[(y * w + x) * 4 + 3] > 40 {
                minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
                radius = max(radius, hypot(CGFloat(x) + 0.5 - cx, CGFloat(y) + 0.5 - cy))
            }
        }
        #expect(maxX >= 0, "nothing drawn")
        let rect = CGRect(x: (CGFloat(minX) - cx) / scale, y: (CGFloat(minY) - cy) / scale,
                          width: CGFloat(maxX - minX + 1) / scale, height: CGFloat(maxY - minY + 1) / scale)
        return (rect, radius / scale)
    }

    /// Inside a region of `size` centered on the canvas (half a point for antialiasing).
    private func inside(_ r: CGRect, _ size: CGSize) -> Bool {
        r.minX >= -size.width / 2 - 0.5 && r.maxX <= size.width / 2 + 0.5 && r.minY >= -size.height / 2 - 0.5 && r.maxY <= size.height / 2 + 0.5
    }

    /// A side limited in width (iPhone 18 Pro beside another app's activity, landscape, iPhone Duo): the glyph stays in
    /// the system's 28 pt circle.
    @Test(arguments: counts)
    func glyphFitsTheLimitedCircle(_ n: Int) throws {
        let side = CGSize(width: IslandMetrics.limited, height: IslandMetrics.limited)
        for cell in Self.statusCells(n) {
            let drawn = try ink(IslandGlyph(cell: cell, limited: true), region: side)
            #expect(drawn.radius <= IslandMetrics.limited / 2 + 0.5, "\(cell) spills out of the 28 pt circle: \(drawn.rect)")
        }
    }

    /// The minimal island (another app's activity beside ours on the other iPhones): within its widest.
    @Test(arguments: counts)
    func glyphFitsTheMinimal(_ n: Int) throws {
        let minimal = CGSize(width: IslandMetrics.minimalContent, height: IslandMetrics.limited)
        #expect(IslandGlyph.look(n, limited: false) != .ring, "a ring only where the width is limited")
        for cell in Self.statusCells(n) {
            let drawn = try ink(IslandGlyph(cell: cell, limited: false), region: minimal)
            #expect(inside(drawn.rect, minimal), "\(cell) is wider than the minimal island: \(drawn.rect)")
        }
    }

    /// Each side of the compact island alone, on the narrowest iPhone: counts and the numbers one may choose.
    @Test(arguments: counts)
    func compactSidesFit(_ n: Int) throws {
        let side = CGSize(width: IslandMetrics.compactContent, height: IslandMetrics.limited)
        // Load is per core ("0.1" … "12.3" … "99.9", #12519), and past 1 when threads wait: not capped at 100%.
        let numbers: [IslandCell] = [.tpm(n * 1000), .tokens(n * 1_000_000), .requests(n * 10), .load(name: "", percent: min(n * 10, 9990))]
        for cell in Self.statusCells(n) + numbers {
            let drawn = try ink(IslandCellView(cell: cell), region: side)
            #expect(inside(drawn.rect, side), "\(cell) is wider than a compact side: \(drawn.rect)")
        }
    }

    /// The glyph keeps its symbol while it fits: one digit always, limited or not.
    @Test func oneDigitKeepsItsSymbol() throws {
        let side = CGSize(width: IslandMetrics.limited, height: IslandMetrics.limited)
        let pair = try ink(IslandPairView(pair: try #require(IslandPair(.working(3))), small: true), region: side)
        let glyph = try ink(IslandGlyph(cell: .working(3), limited: true), region: side)
        #expect(abs(pair.rect.width - glyph.rect.width) < 1, "the limited glyph dropped the symbol of a one-digit count")
    }

    /// The check itself catches a spill: four digits beside their symbol do not fit the 28 pt circle.
    @Test func aSpillIsCaught() throws {
        let side = CGSize(width: IslandMetrics.limited, height: IslandMetrics.limited)
        let drawn = try ink(IslandPairView(pair: try #require(IslandPair(.working(1234))), small: true), region: side)
        #expect(drawn.radius > IslandMetrics.limited / 2 + 0.5)
        #expect(!inside(drawn.rect, side))
    }

    /// A known shape checks the point-to-pixel scale and origin independently of the island components.
    @Test func bitmapPreservesPointCoordinates() throws {
        let side = CGSize(width: IslandMetrics.limited, height: IslandMetrics.limited)
        let size = CGSize(width: 10, height: 14)
        let offset = CGSize(width: 3, height: 2)
        let drawn = try ink(Rectangle().fill(.white).frame(width: size.width, height: size.height)
            .offset(x: offset.width, y: offset.height), region: side)
        #expect(abs(drawn.rect.width - size.width) < 0.5)
        #expect(abs(drawn.rect.height - size.height) < 0.5)
        #expect(abs(drawn.rect.midX - offset.width) < 0.5)
        #expect(abs(abs(drawn.rect.midY) - offset.height) < 0.5)
        #expect(inside(drawn.rect, side))
    }

    /// The expanded island's top corner beside the camera, where the counts and the stats line go: about 125 pt, as the
    /// corner the persona takes on the other side (FleetLiveActivity), less the line's 6 pt inset from the island's edge.
    nonisolated static let expandedCorner: CGFloat = 125 - 6

    /// A view's ideal width, the one `ViewThatFits` compares with the room it is offered.
    private func idealWidth(_ view: some View) throws -> CGFloat {
        let renderer = ImageRenderer(content: view.fixedSize())
        renderer.scale = 2
        let image = try #require(renderer.cgImage)
        return CGFloat(image.width) / renderer.scale
    }

    /// The stats line's words in `language` (the test host draws zh-Hans), by `FleetText.stat`'s own keys.
    private static func words(_ resource: LocalizedStringResource, _ language: String) -> String {
        var resource = resource
        resource.locale = Locale(identifier: language)
        return String(localized: resource)
    }

    /// The numbers' line on the expanded island and the Lock Screen (#12519). A chosen machine's load in words beside
    /// today's tokens is wider than the expanded island's corner in every language, so there the line draws load as the
    /// CPU symbol and its figure (`FleetStatsLine`'s `ViewThatFits`), which fits whole. Measured from the line's own
    /// pieces and spacing; the Lock Screen's row, after the persona and the counts, is wider than this corner.
    @Test(arguments: ["en", "zh-Hans", "ja"])
    func statsLineFitsTheExpandedCorner(_ language: String) throws {
        let load = Self.words("\("MacBook Pro") \(FleetText.perCore(percent: 140)) per core", language)
        let tokens = Self.words(LocalizedStringResource("\(FleetText.short(42_000_000)) today", comment: "Tokens today"), language)
        if language == "zh-Hans" { // the words the line draws in the test host's language
            #expect(load == FleetText.stat(.load(name: "MacBook Pro", percent: 140)))
            #expect(tokens == FleetText.stat(.tokens(42_000_000)))
        }
        let words = try idealWidth(HStack(spacing: FleetStatsLine.spacing) { FleetStatText(text: load); FleetStatText(text: tokens) })
        let figure = try idealWidth(HStack(spacing: FleetStatsLine.spacing) { FleetLoadFigure(percent: 140); FleetStatText(text: tokens) })
        #expect(words > Self.expandedCorner, "\(language): \"\(load)\" and \"\(tokens)\" fit whole now (\(words) pt)")
        #expect(figure <= Self.expandedCorner, "\(language): the figure beside \"\(tokens)\" takes \(figure) pt")
    }

    @Test func glyphLooks() {
        #expect(IslandGlyph.look(7, limited: true) == .pair)
        #expect(IslandGlyph.look(12, limited: true) == .ring)
        #expect(IslandGlyph.look(12, limited: false) == .pair)
        #expect(IslandGlyph.look(100, limited: false) == .many)
        #expect(IslandGlyph.look(1234, limited: true) == .many)
        #expect(IslandCell.working(3).count == 3 && IslandCell.tpm(3).count == nil)
    }
}
