import PersonaWire
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// The map itself (#14576, docs/server/machine-usage-map.md "The map"; docs/design/DESIGN_SYSTEM.md "Usage treemap"): one
/// `Canvas` per side draws every box from the scene the model laid out off the main actor, one more draws the ribbons and
/// rings of what is hovered, focused or selected (so a hover redraws only that), and a custom `Layout` of clear hit
/// targets over the boxes of at least 24 pt carries the platform's own interactions: tap (zoom, or select a leaf), the
/// context menu (its preview is the long-press details), drag and drop between the sides, hover, keyboard focus and the
/// VoiceOver element with the menu's actions. A zoom animates one `Animatable` progress (P9: nothing runs at rest);
/// Reduce Motion cross-fades.
struct UsageTreemapView: View {
    var model: UsageMapModel
    var scene: UsageMapScene
    /// Show in Files: opens the existing file browser at a folder.
    var showInFiles: (String) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @FocusState private var focus: String?
    @State private var progress: CGFloat = 1
    @State private var dropSide: TreemapBox.Side?

    var body: some View {
        let zoom = model.zoom
        ZStack(alignment: .topLeading) {
            UsageSideCanvas(boxes: scene.local, frame: scene.split.local, tint: model.tint, selected: model.selectedIDs,
                            suggested: Set(model.suggestions.map(\.id)), badges: scene.badges)
                .modifier(UsageZoomGrow(progress: zoom.side == .local ? progress : 1, from: zoom.from, frame: scene.split.local))
            Rectangle().fill(Theme.UsageMap.cloudGround)
                .frame(width: scene.split.cloud.width, height: scene.split.cloud.height)
                .clipShape(.rect(cornerRadius: Theme.Radius.thumbnail))
                .offset(x: scene.split.cloud.minX, y: scene.split.cloud.minY)
                .accessibilityHidden(true)
            UsageSideCanvas(boxes: scene.cloud, frame: scene.split.cloud, tint: model.tint, selected: model.selectedIDs,
                            suggested: [], badges: [:])
                .modifier(UsageZoomGrow(progress: zoom.side == .cloud ? progress : 1, from: zoom.from, frame: scene.split.cloud))
            UsageLinksCanvas(scene: scene, active: active, dropSide: dropSide)
            UsageBoxLayout {
                ForEach(scene.targets) { box in target(box) }
            }
        }
        .frame(width: scene.size.width, height: scene.size.height, alignment: .topLeading)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String(localized: "Disk usage map"))
        .accessibilityIdentifier("usage.map")
        .dropDestination(for: String.self) { items, location in drop(items, at: location) } isTargeted: { on in
            dropSide = on ? dropSide ?? .cloud : nil
        }
        .onKeyPress(keys: [.upArrow, .downArrow, .leftArrow, .rightArrow, .space, .return, .escape, .delete, .deleteForward]) { press in
            key(press.key)
        }
        .onChange(of: zoom.serial) { _, _ in
            guard !reduceMotion, zoom.from != .zero else { return }
            progress = 0
            withAnimation(Theme.UsageMap.zoom) { progress = 1 }
        }
        .onChange(of: scene.targets.first?.id) { _, first in
            // Focus returns to the first box after a zoom.
            if focus != nil, let first, scene.box(focus!) == nil { focus = first }
        }
        .animation(reduceMotion ? Theme.UsageMap.zoom : nil, value: zoom.serial)
    }

    /// What lights a ribbon: the hovered box, the focused one and the selection.
    private var active: Set<String> {
        var s = model.selectedIDs
        if let h = model.hovered { s.insert(h) }
        if let focus { s.insert(focus) }
        return s
    }

    // MARK: Hit targets

    private func target(_ box: TreemapBox) -> some View {
        let selected = model.selectedIDs.contains(box.id)
        let sel = model.selected(from: box)
        return Color.clear
            .contentShape(.rect)
            .layoutValue(key: UsageBoxRect.self, value: box.rect)
            .onTapGesture { tap(box) }
            .onHover { inside in
                if inside { model.hover(box.id) } else if model.hovered == box.id { model.hover(nil) }
            }
            .contextMenu { menu(box) } preview: { UsageBoxPreview(box: box, detail: model.detail(box)) }
            .modifier(UsageDraggable(payload: sel.map { (box.side == .local ? "local:" : "cloud:") + $0.id }, title: box.title))
            .focusable()
            .focused($focus, equals: box.id)
            .onChange(of: focus == box.id) { _, on in if on { model.hover(box.id) } }
            .accessibilityElement()
            .accessibilityLabel(model.accessibilityLabel(box))
            .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
            .accessibilityHint(box.zoomable ? String(localized: "Opens it on the map") : "")
            .accessibilityActions { menu(box) }
            .accessibilityIdentifier("usage.box.\(box.id)")
    }

    private func tap(_ box: TreemapBox) {
        model.hover(box.id)
        if box.zoomable {
            Task { await model.zoom(into: box) }
        } else if model.selectable(box) {
            model.toggle(box)
        }
    }

    /// The items an action takes: the whole selection when the box is part of it, else the box alone.
    private func items(_ box: TreemapBox) -> [UsageMapModel.Selected] {
        if model.selectedIDs.contains(box.id) { return model.selection }
        return model.selected(from: box).map { [$0] } ?? []
    }

    @ViewBuilder private func menu(_ box: TreemapBox) -> some View {
        let these = items(box)
        let one = these.count == 1 ? these.first : nil
        if box.zoomable {
            Button { Task { await model.zoom(into: box) } } label: { Label(String(localized: "Zoom In"), systemImage: Theme.Icon.zoomIn) }
        }
        if let s = model.selected(from: box) {
            Button { model.toggle(box) } label: {
                Label(model.selectedIDs.contains(s.id) ? String(localized: "Deselect") : String(localized: "Select"), systemImage: Theme.Icon.checkedBox)
            }
        }
        if box.side == .local, box.node.path != nil, box.node.kind != .free {
            localActions(box, these: these, one: one)
        }
        if box.side == .cloud, let s = model.selected(from: box) {
            Button { model.askRestore(these) } label: {
                Label(String(localized: "Restore…"), systemImage: Theme.Icon.cloudRestore)
                if !s.restorable { Text(String(localized: "This item can't be brought back from here.")) }
            }
            .disabled(!s.restorable)
        }
        if let n = scene.badges[box.id], n > 0 {
            Button { model.hover(box.id) } label: {
                Label(String(localized: "Show \(n) Offloaded Items"), systemImage: Theme.Icon.offloaded)
            }
        }
    }

    @ViewBuilder private func localActions(_ box: TreemapBox, these: [UsageMapModel.Selected], one: UsageMapModel.Selected?) -> some View {
        let n = box.node
        if n.cacheID != nil {
            Button { model.askClean(these) } label: {
                Label(String(localized: "Clean Cache…"), systemImage: Theme.Icon.cleanCache)
                if n.cacheInUse == true { Text(String(localized: "In use, can't be cleaned")) }
            }
            .disabled(!these.contains(where: \.cleanable))
        } else {
            Button(role: .destructive) { model.askTrash(these) } label: {
                Label(String(localized: "Move to Trash…"), systemImage: Theme.Icon.delete)
                if !n.canTrash { Text(UsageMapText.refusal(n.refusal)) }
            }
            .disabled(!these.contains(where: \.trashable))
        }
        ForEach(model.providers) { p in
            let ok = these.contains { $0.canOffload && p.takes($0.kind) }
            Button { model.askOffload(these, to: p) } label: {
                Label(String(localized: "Offload to \(p.name)…"), systemImage: Theme.Icon.cloudOffload)
                if !ok { Text(n.canOffload ? String(localized: "\(p.name) doesn't take this kind of item.") : UsageMapText.refusal(n.refusal)) }
            }
            .disabled(!ok)
        }
        if let path = n.path {
            if n.kind == .dir || n.kind == .mount {
                Button { showInFiles(path) } label: { Label(String(localized: "Show in Files"), systemImage: Theme.Icon.folder) }
            }
            #if canImport(UIKit)
            Button { UIPasteboard.general.string = path } label: { Label(String(localized: "Copy Path"), systemImage: Theme.Icon.copy) }
            #endif
        }
    }

    // MARK: Drag and drop, keys

    private func drop(_ payloads: [String], at location: CGPoint) -> Bool {
        dropSide = nil
        guard let payload = payloads.first else { return false }
        let onCloud = scene.split.stacked ? location.y >= scene.split.channel.minY : location.x >= scene.split.channel.minX
        if payload.hasPrefix("local:"), onCloud, let box = scene.box(String(payload.dropFirst("local:".count))) {
            let these = items(box)
            if let p = model.offloadProviders(for: these).first { model.askOffload(these, to: p); return true }
        }
        if payload.hasPrefix("cloud:"), !onCloud, let box = scene.box(String(payload.dropFirst("cloud:".count))) {
            model.askRestore(items(box))
            return true
        }
        return false
    }

    private func key(_ key: KeyEquivalent) -> KeyPress.Result {
        switch key {
        case .escape:
            return model.zoomOut() ? .handled : .ignored
        case .upArrow, .downArrow, .leftArrow, .rightArrow:
            let d: UsageMapModel.Direction = key == .upArrow ? .up : key == .downArrow ? .down : key == .leftArrow ? .left : .right
            guard let next = model.neighbor(of: focus, d) else { return .ignored }
            focus = next
            model.hover(next)
            return .handled
        default:
            guard let id = focus, let box = scene.box(id) else { return .ignored }
            switch key {
            case .return: if box.zoomable { Task { await model.zoom(into: box) } }
            case .space: model.toggle(box)
            case .delete, .deleteForward: model.askTrash(items(box))
            default: return .ignored
            }
            return .handled
        }
    }
}

// MARK: - Drawing

/// One side's boxes: fills, separators, title strips, the labels that fit whole, hatched ghosts for the selection, dashed
/// twins of what is on its way, suggestion rings, the cold glyph and the cloud badges.
private struct UsageSideCanvas: View {
    var boxes: [TreemapBox]
    var frame: CGRect
    var tint: UsageMapModel.Tint
    var selected: Set<String>
    var suggested: Set<String>
    var badges: [String: Int]

    var body: some View {
        Canvas { ctx, _ in
            let now = Date.now
            for box in boxes { draw(box, in: &ctx, now: now) }
        } symbols: {
            ThemedIcon(Theme.Icon.cold).font(Theme.Font.label).foregroundStyle(Theme.UsageMap.ink.color).tag(Symbol.cold)
            ThemedIcon(Theme.Icon.offloaded).font(Theme.Font.label).foregroundStyle(Theme.UsageMap.ink.color).tag(Symbol.cloud)
        }
        .clipShape(UsageRectShape(rect: frame))
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    private enum Symbol: Hashable { case cold, cloud }

    private func fill(_ box: TreemapBox, now: Date) -> Color {
        if box.node.kind == .free { return Theme.UsageMap.free.color }
        switch tint {
        case .kind: return Theme.UsageMap.fill(box.node.category)
        case .age: return TreemapLayout.ageStep(usedAt: box.node.usedAt, now: now)?.color ?? Theme.UsageMap.ageUnknown.color
        }
    }

    private func draw(_ box: TreemapBox, in ctx: inout GraphicsContext, now: Date) {
        let line = Theme.Size.line
        let r = box.rect.insetBy(dx: line / 2, dy: line / 2)
        let shape = Path(roundedRect: r, cornerRadius: Theme.Radius.thumbnail)
        let incoming = box.node.key == UsageMapSceneBuilder.incomingKey
        ctx.fill(shape, with: .color(fill(box, now: now).opacity(incoming ? Theme.UsageMap.ghostHatch : 1)))
        ctx.stroke(shape, with: .color(Theme.UsageMap.separator), lineWidth: line)
        if box.node.kind == .free || incoming {
            ctx.stroke(Path(roundedRect: r.insetBy(dx: line * 2, dy: line * 2), cornerRadius: Theme.Radius.thumbnail),
                       with: .color(incoming ? Theme.accent : Theme.UsageMap.ink.color.opacity(Theme.UsageMap.ribbonFill)),
                       style: StrokeStyle(lineWidth: line, dash: Theme.UsageMap.dash))
        }
        // The hatch first: the words stay readable over a selected box.
        if selected.contains(box.id), box.side == .local { hatch(r, in: &ctx) }
        labels(box, in: &ctx)
        if suggested.contains(box.id) {
            ctx.stroke(Path(roundedRect: r.insetBy(dx: Theme.UsageMap.ring / 2, dy: Theme.UsageMap.ring / 2), cornerRadius: Theme.Radius.thumbnail),
                       with: .color(Theme.caution), lineWidth: Theme.UsageMap.ring)
        }
        let least = Theme.UsageMap.glyphLeast
        if tint == .age, !box.framed, r.width >= least, r.height >= least, TreemapLayout.ageStep(usedAt: box.node.usedAt, now: now) == .year,
           let g = ctx.resolveSymbol(id: Symbol.cold) {
            ctx.draw(g, at: CGPoint(x: r.maxX - Theme.UsageMap.labelInset - g.size.width / 2, y: r.maxY - Theme.UsageMap.labelInset - g.size.height / 2))
        }
        if let n = badges[box.id], n > 0, r.width >= Theme.UsageMap.badgeLeast, r.height >= Theme.UsageMap.titleStrip {
            badge(n, in: r, framed: box.framed, &ctx)
        }
    }

    private func labels(_ box: TreemapBox, in ctx: inout GraphicsContext) {
        let ink = Theme.UsageMap.ink.color
        let r = box.rect
        if box.framed {
            let pad = Theme.UsageMap.labelInset
            let name = ctx.resolve(Text(box.title).font(Theme.Font.labelStrong).foregroundStyle(ink))
            let size = ctx.resolve(Text(box.sizeText).font(Theme.Font.label).foregroundStyle(ink))
            let inner = r.width - 2 * pad - badgeRoom(box, in: &ctx)
            let nw = name.measure(in: .init(width: CGFloat.infinity, height: .infinity))
            guard nw.width <= inner, nw.height <= Theme.UsageMap.titleStrip else { return }
            let mid = r.minY + Theme.UsageMap.titleStrip / 2
            ctx.draw(name, at: CGPoint(x: r.minX + pad, y: mid), anchor: .leading)
            let sw = size.measure(in: .init(width: CGFloat.infinity, height: .infinity))
            if nw.width + Theme.UsageMap.labelGap + sw.width <= inner {
                ctx.draw(size, at: CGPoint(x: r.minX + pad + nw.width + Theme.UsageMap.labelGap, y: mid), anchor: .leading)
            }
            return
        }
        let pad = box.big ? Theme.UsageMap.labelInsetBig : Theme.UsageMap.labelInset
        let name = ctx.resolve(Text(box.title).font(box.big ? Theme.Font.secondaryStrong : Theme.Font.labelStrong).foregroundStyle(ink))
        let size = ctx.resolve(Text(box.sizeText).font(Theme.Font.label).foregroundStyle(ink))
        let inner = r.width - 2 * pad
        // The top line shares its row with the cloud badge.
        let first = inner - badgeRoom(box, in: &ctx)
        let nw = name.measure(in: .init(width: CGFloat.infinity, height: .infinity))
        let sw = size.measure(in: .init(width: CGFloat.infinity, height: .infinity))
        // A name or a size is drawn only where it fits whole: a cut-off size is worse than none.
        guard nw.width <= first, nw.height + 2 * pad <= r.height else { return }
        let top = CGPoint(x: r.minX + pad, y: r.minY + pad)
        ctx.draw(name, at: top, anchor: .topLeading)
        if nw.height + Theme.UsageMap.labelGap + sw.height + 2 * pad <= r.height, sw.width <= inner {
            ctx.draw(size, at: CGPoint(x: top.x, y: top.y + nw.height + Theme.UsageMap.labelGap), anchor: .topLeading)
        } else if nw.width + Theme.UsageMap.labelGap + sw.width <= first {
            ctx.draw(size, at: CGPoint(x: top.x + nw.width + Theme.UsageMap.labelGap, y: top.y + (nw.height - sw.height) / 2), anchor: .topLeading)
        }
    }

    /// The width the cloud badge takes from a box's top line (with a gap before it); zero without one.
    private func badgeRoom(_ box: TreemapBox, in ctx: inout GraphicsContext) -> CGFloat {
        guard let n = badges[box.id], n > 0, box.rect.width >= Theme.UsageMap.badgeLeast, box.rect.height >= Theme.UsageMap.titleStrip,
              let icon = ctx.resolveSymbol(id: Symbol.cloud) else { return 0 }
        let count = ctx.resolve(Text("\(n)").font(Theme.Font.labelStrong)).measure(in: .init(width: CGFloat.infinity, height: .infinity))
        let pad = Theme.UsageMap.labelGap
        return icon.size.width + count.width + 4 * pad
    }

    /// A selected box's ghost: hatched in the free color, as the space it would leave.
    private func hatch(_ r: CGRect, in ctx: inout GraphicsContext) {
        ctx.drawLayer { layer in
            layer.clip(to: Path(roundedRect: r, cornerRadius: Theme.Radius.thumbnail))
            var lines = Path()
            let step = Theme.UsageMap.hatchStep
            var x = r.minX - r.height
            while x < r.maxX {
                lines.move(to: CGPoint(x: x, y: r.maxY))
                lines.addLine(to: CGPoint(x: x + r.height, y: r.minY))
                x += step
            }
            layer.stroke(lines, with: .color(Theme.UsageMap.free.color.opacity(Theme.UsageMap.ghostHatch * 2)), lineWidth: Theme.Size.lineMedium)
        }
        ctx.stroke(Path(roundedRect: r.insetBy(dx: Theme.UsageMap.ring / 2, dy: Theme.UsageMap.ring / 2), cornerRadius: Theme.Radius.thumbnail),
                   with: .color(Theme.accent), style: StrokeStyle(lineWidth: Theme.UsageMap.ring, dash: Theme.UsageMap.dash))
    }

    private func badge(_ n: Int, in r: CGRect, framed: Bool, _ ctx: inout GraphicsContext) {
        guard let icon = ctx.resolveSymbol(id: Symbol.cloud) else { return }
        let count = ctx.resolve(Text("\(n)").font(Theme.Font.labelStrong).foregroundStyle(Theme.UsageMap.ink.color))
        let cs = count.measure(in: .init(width: CGFloat.infinity, height: .infinity))
        let pad = Theme.UsageMap.labelGap
        let w = icon.size.width + pad + cs.width + 2 * pad
        let h = max(icon.size.height, cs.height) + pad
        let y = framed ? r.minY + (Theme.UsageMap.titleStrip - h) / 2 : r.minY + pad
        let pill = CGRect(x: r.maxX - w - pad, y: y, width: w, height: h)
        guard pill.minX > r.minX + pad else { return }
        ctx.fill(Path(roundedRect: pill, cornerRadius: h / 2), with: .color(Theme.UsageMap.cloudGround))
        ctx.draw(icon, at: CGPoint(x: pill.minX + pad + icon.size.width / 2, y: pill.midY))
        ctx.draw(count, at: CGPoint(x: pill.maxX - pad - cs.width / 2, y: pill.midY))
    }
}

/// The ribbons across the channel between a cloud box and the local box it came from, and the rings of what is hovered,
/// focused or selected: redrawn on a hover without touching the boxes.
private struct UsageLinksCanvas: View {
    var scene: UsageMapScene
    var active: Set<String>
    var dropSide: TreemapBox.Side?

    var body: some View {
        Canvas { ctx, _ in
            let accent = Theme.accent
            var lit: Set<String> = []
            for (cloudID, localID) in scene.links where active.contains(cloudID) || active.contains(localID) {
                guard let c = scene.box(cloudID), let l = scene.box(localID) else { continue }
                ctx.fill(ribbon(from: l.rect, to: c.rect), with: .color(accent.opacity(Theme.UsageMap.ribbonFill)))
                lit.insert(cloudID)
                lit.insert(localID)
            }
            for id in active.union(lit) {
                guard let b = scene.box(id) else { continue }
                let r = b.rect.insetBy(dx: Theme.UsageMap.ring / 2, dy: Theme.UsageMap.ring / 2)
                ctx.stroke(Path(roundedRect: r, cornerRadius: Theme.Radius.thumbnail), with: .color(accent), lineWidth: Theme.UsageMap.ring)
            }
            if let dropSide {
                let r = (dropSide == .cloud ? scene.split.cloud : scene.split.local).insetBy(dx: Theme.UsageMap.ring, dy: Theme.UsageMap.ring)
                ctx.stroke(Path(roundedRect: r, cornerRadius: Theme.Radius.thumbnail), with: .color(accent),
                           style: StrokeStyle(lineWidth: Theme.UsageMap.ring, dash: Theme.UsageMap.dash))
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    /// A band from the local box's edge facing the channel to the cloud box's, as thick as the thinner of the two.
    private func ribbon(from l: CGRect, to c: CGRect) -> Path {
        var p = Path()
        if scene.split.stacked {
            let w = min(l.width, c.width) / 2
            let a = CGPoint(x: l.midX, y: l.maxY), b = CGPoint(x: c.midX, y: c.minY)
            let m = (a.y + b.y) / 2
            p.move(to: CGPoint(x: a.x - w, y: a.y))
            p.addCurve(to: CGPoint(x: b.x - w, y: b.y), control1: CGPoint(x: a.x - w, y: m), control2: CGPoint(x: b.x - w, y: m))
            p.addLine(to: CGPoint(x: b.x + w, y: b.y))
            p.addCurve(to: CGPoint(x: a.x + w, y: a.y), control1: CGPoint(x: b.x + w, y: m), control2: CGPoint(x: a.x + w, y: m))
        } else {
            let h = min(l.height, c.height) / 2
            let a = CGPoint(x: l.maxX, y: l.midY), b = CGPoint(x: c.minX, y: c.midY)
            let m = (a.x + b.x) / 2
            p.move(to: CGPoint(x: a.x, y: a.y - h))
            p.addCurve(to: CGPoint(x: b.x, y: b.y - h), control1: CGPoint(x: m, y: a.y - h), control2: CGPoint(x: m, y: b.y - h))
            p.addLine(to: CGPoint(x: b.x, y: b.y + h))
            p.addCurve(to: CGPoint(x: a.x, y: a.y + h), control1: CGPoint(x: m, y: b.y + h), control2: CGPoint(x: m, y: a.y + h))
        }
        p.closeSubpath()
        return p
    }
}

/// The long-press details: the box's name and what the detail line says.
private struct UsageBoxPreview: View {
    var box: TreemapBox
    var detail: String

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(box.title).font(Theme.Font.heading).lineLimit(2)
            Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(4)
            if let d = box.node.display { Text(d).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(2) }
        }
        .padding(Theme.Space.l)
        .scaledFrame(minWidth: Theme.Size.cardColumn, alignment: .leading)
    }
}

// MARK: - Layout, shapes, motion

private nonisolated struct UsageBoxRect: LayoutValueKey {
    static let defaultValue: CGRect = .zero
}

/// Places each hit target on its box.
private nonisolated struct UsageBoxLayout: Layout {
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        proposal.replacingUnspecifiedDimensions()
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        for s in subviews {
            let r = s[UsageBoxRect.self]
            s.place(at: CGPoint(x: bounds.minX + r.minX, y: bounds.minY + r.minY), proposal: ProposedViewSize(r.size))
        }
    }
}

private nonisolated struct UsageRectShape: Shape {
    var rect: CGRect
    func path(in _: CGRect) -> Path { Path(roundedRect: rect, cornerRadius: Theme.Radius.thumbnail) }
}

/// A zoom's one animated value: the side's new level grows out of the box it was and fades in.
private struct UsageZoomGrow: ViewModifier, Animatable {
    var progress: CGFloat
    /// The box zoomed into, in the side's coordinates (0…1); zero for a zoom out (a fade only).
    var from: CGRect
    var frame: CGRect

    var animatableData: CGFloat {
        get { progress }
        set { progress = newValue }
    }

    func body(content: Content) -> some View {
        let p = min(1, max(0, progress))
        let f = from == .zero ? CGRect(x: 0, y: 0, width: 1, height: 1) : from
        let start = CGRect(x: frame.minX + f.minX * frame.width, y: frame.minY + f.minY * frame.height,
                           width: f.width * frame.width, height: f.height * frame.height)
        let w = start.width + (frame.width - start.width) * p, h = start.height + (frame.height - start.height) * p
        let x = start.minX + (frame.minX - start.minX) * p, y = start.minY + (frame.minY - start.minY) * p
        let sx = frame.width > 0 ? w / frame.width : 1, sy = frame.height > 0 ? h / frame.height : 1
        content
            .transformEffect(CGAffineTransform(a: sx, b: 0, c: 0, d: sy, tx: x - frame.minX * sx, ty: y - frame.minY * sy))
            .opacity(p)
            .clipShape(UsageRectShape(rect: frame))
    }
}

/// Drag a box to the other side: a local one to offload, a cloud one to restore. The drop only opens the confirmation.
private struct UsageDraggable: ViewModifier {
    var payload: String?
    var title: String

    func body(content: Content) -> some View {
        if let payload {
            content.draggable(payload) {
                Text(title).font(Theme.Font.labelStrong).lineLimit(1)
                    .padding(.horizontal, Theme.Space.s).padding(.vertical, Theme.Space.xs)
                    .background(.regularMaterial, in: .capsule)
            }
        } else {
            content
        }
    }
}
