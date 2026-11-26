import PersonaWire
import SwiftUI

/// Opens a computer's disk usage map (#14576): from the Disk almost full chip on its card, its Disk tile and the banner
/// in its details. `reclaim`: the disk level is raised, so the biggest reclaimable items are marked.
struct MachineUsageMapRoute: Hashable {
    var ref: MachineRef
    var reclaim = false
}

/// The disk usage map's page (docs/server/machine-usage-map.md "The map", "Low-disk guidance"; docs/design/DESIGN_SYSTEM.md
/// "Usage treemap"): in reclaim mode the headline and the suggestion chips, the breadcrumb, the tint, the map (local
/// left, cloud right on one scale; stacked on a phone), the detail line, and the tray with the "what if", the selection
/// as rows (the map's accessible list), its actions, the free-up goal and an offload's progress. Every change goes
/// through a confirmation that names what it touches.
struct UsageMapView: View {
    @Environment(AppModel.self) private var app
    @State private var model: UsageMapModel
    @State private var filesPath: String?
    let ref: MachineRef

    init(route: MachineUsageMapRoute) {
        ref = route.ref
        _model = State(initialValue: UsageMapModel(machine: route.ref.id, name: route.ref.id, reclaim: route.reclaim,
                                                   deps: .live(group: route.ref.group)))
    }

    /// Tests and previews hand in their own model.
    init(ref: MachineRef, model: UsageMapModel) {
        self.ref = ref
        _model = State(initialValue: model)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.l) {
                if model.reclaim { UsageReclaimHeader(model: model) }
                UsageMapToolbar(model: model)
                if let problem = model.problem {
                    EmptyStateView(symbol: Theme.Icon.storage, title: String(localized: "Can't Show the Disk"), message: problem.text,
                                   action: problem.retryable ? (String(localized: "Try Again"), { Task { await model.retry() } }) : nil)
                } else {
                    UsageMapCanvasArea(model: model, showInFiles: { filesPath = $0 })
                    UsageDetailLine(model: model)
                    UsageMapTray(model: model)
                }
            }
            .padding(Theme.Space.l)
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Disk Usage"))
        .navigationBarTitleDisplayMode(.inline)
        // Keyed on the hub's client: opened at launch (a link, a launch switch) the page can come before the connection.
        .tabActiveTask(id: app.api(ref.group) != nil) {
            model.machineName = app.machine(ref)?.name ?? ref.id
            await model.start()
        }
        .tabActiveTask(id: model.pollKey) { await model.poll() }
        .modifier(UsageConfirmations(model: model, machineName: app.machine(ref)?.name ?? ref.id))
        .navigationDestination(item: $filesPath) { path in
            FilesPane(machineID: ref.id, group: ref.group, cwd: path)
                .detailTitle(String(localized: "Files"))
                .navigationBarTitleDisplayMode(.inline)
        }
        .accessibilityIdentifier("usage.page")
    }
}

/// The headline and the suggestions of reclaim mode: activating a chip zooms to the item and selects it.
private struct UsageReclaimHeader: View {
    var model: UsageMapModel

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let h = model.headline {
                Text(h).font(Theme.Font.secondary).foregroundStyle(.primary).fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("usage.headline")
            }
            if !model.suggestions.isEmpty {
                FlowLayoutRow {
                    ForEach(model.suggestions) { c in
                        Button { Task { await model.activate(c) } } label: {
                            Chip(text: "\(c.node.name) · \(UsageMapText.size(c.node.bytes))", symbol: symbol(c.action),
                                 look: .review(.caution))
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "\(c.node.name), \(UsageMapText.size(c.node.bytes)), \(verb(c.action))"))
                        .accessibilityIdentifier("usage.suggestion.\(c.id)")
                    }
                }
            }
        }
    }

    private func symbol(_ a: TreemapLayout.Candidate.Action) -> String {
        switch a {
        case .clean: Theme.Icon.cleanCache
        case .trash: Theme.Icon.delete
        case .offload: Theme.Icon.cloudOffload
        }
    }

    private func verb(_ a: TreemapLayout.Candidate.Action) -> String {
        switch a {
        case .clean: String(localized: "Clean Cache")
        case .trash: String(localized: "Move to Trash")
        case .offload: String(localized: "Offload")
        }
    }
}

/// The breadcrumb (each step a button), the tint and what the scan is doing.
private struct UsageMapToolbar: View {
    var model: UsageMapModel

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: Theme.Space.xs) {
                    ForEach(Array(model.breadcrumb.enumerated()), id: \.offset) { i, name in
                        if i > 0 { ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.label).foregroundStyle(.tertiary) }
                        Button { model.zoomTo(i) } label: {
                            Text(name).font(i == model.breadcrumb.count - 1 ? Theme.Font.secondaryStrong : Theme.Font.secondary).lineLimit(1)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(i == model.breadcrumb.count - 1 ? Color.primary : Theme.accent)
                        .disabled(i == model.breadcrumb.count - 1)
                        .accessibilityIdentifier("usage.crumb.\(i)")
                    }
                }
            }
            HStack(spacing: Theme.Space.m) {
                SegmentedChoice(String(localized: "Color By"), [(UsageMapModel.Tint.kind, String(localized: "Kind")),
                                                                (UsageMapModel.Tint.age, String(localized: "Last Used"))],
                                selection: Binding(get: { model.tint }, set: { model.tint = $0 }))
                    .fixedSize()
                    .accessibilityIdentifier("usage.tint")
                Spacer(minLength: 0)
                if model.scene.split.compressed {
                    Chip(text: String(localized: "Cloud 1 : \(Int(model.scene.split.ratio.rounded()))"), symbol: Theme.Icon.offloaded, compact: true)
                        .accessibilityLabel(String(localized: "The cloud side is drawn \(Int(model.scene.split.ratio.rounded())) times smaller"))
                }
                if model.scanning {
                    ProgressView().controlSize(.small)
                    Text("Measuring…").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
            }
        }
    }
}

/// The map at the page's width, with the cloud side's empty state and the legend.
private struct UsageMapCanvasArea: View {
    var model: UsageMapModel
    var showInFiles: (String) -> Void

    var body: some View {
        let scene = model.scene
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            ZStack(alignment: .topLeading) {
                if scene.local.isEmpty {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, Theme.Space.xxl)
                } else {
                    UsageTreemapView(model: model, scene: scene, showInFiles: showInFiles)
                    if scene.cloud.isEmpty {
                        UsageCloudEmpty(model: model)
                            .frame(width: scene.split.cloud.width, height: scene.split.cloud.height)
                            .offset(x: scene.split.cloud.minX, y: scene.split.cloud.minY)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { model.setWidth($0) }
            UsageLegend(tint: model.tint)
        }
    }
}

/// The cloud side before anything is offloaded, or with no provider (then a way to Settings › Extensions).
private struct UsageCloudEmpty: View {
    @Environment(AppModel.self) private var app
    var model: UsageMapModel

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.offloaded).font(Theme.Font.title).foregroundStyle(.secondary)
            Text(model.providers.isEmpty ? String(localized: "Nothing offloaded. An extension can add cloud offload.")
                                         : String(localized: "Nothing offloaded yet. Drag an item here to offload it."))
                .font(Theme.Font.label).foregroundStyle(.secondary).multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
            if model.providersKnown, model.providers.isEmpty {
                Button { _ = app.openHubPage(.settings(tab: "extensions", item: nil), group: app.hub?.group ?? "") } label: {
                    Text("Settings › Extensions").font(Theme.Font.labelStrong).lineLimit(1).minimumScaleFactor(0.8)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Theme.accent)
                .accessibilityIdentifier("usage.cloud.settings")
            }
        }
        .padding(Theme.Space.s)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("usage.cloud.empty")
    }
}

/// What the colors mean: the categories, or the four Last Used steps.
private struct UsageLegend: View {
    var tint: UsageMapModel.Tint

    var body: some View {
        FlowLayoutRow {
            switch tint {
            case .kind:
                ForEach(StorageUsageCategory.allCases, id: \.self) { c in swatch(Theme.UsageMap.fill(c), UsageMapText.category(c)) }
            case .age:
                ForEach(Theme.UsageMap.Age.allCases, id: \.self) { a in swatch(a.color, UsageMapText.age(a)) }
            }
        }
        .accessibilityElement(children: .combine)
    }

    private func swatch(_ color: Color, _ name: String) -> some View {
        HStack(spacing: Theme.Space.xs) {
            RoundedRectangle(cornerRadius: Theme.Radius.thumbnail).fill(color)
                .scaledFrame(width: Theme.Size.dotXL, height: Theme.Size.dotXL)
            Text(name).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
        }
    }
}

/// One line under the map: what the hovered, focused or touched box is.
private struct UsageDetailLine: View {
    var model: UsageMapModel

    var body: some View {
        let box = model.hovered.flatMap { model.scene.box($0) }
        Text(box.map { model.detail($0) } ?? String(localized: "Tap a box to open it. Touch and hold for its actions."))
            .font(Theme.Font.label)
            .foregroundStyle(.secondary)
            .lineLimit(2)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityIdentifier("usage.detail")
    }
}

/// A way into the map: on iPad and the Mac it opens in the right column (`splitPush`), elsewhere it is pushed by value
/// (every stack that shows a machine registers `MachineUsageMapRoute` beside `MachineDetailRoute`). Inside another link
/// (a card that opens the computer) a nested link loses the tap to the outer one, so the page hands in `open`: a plain
/// button, which keeps it, then pushes from the page.
struct MachineUsageMapLink<Label: View>: View {
    @Environment(\.splitPush) private var splitPush
    var route: MachineUsageMapRoute
    var open: ((MachineUsageMapRoute) -> Void)?
    @ViewBuilder var label: Label

    var body: some View {
        if let splitPush {
            Button { splitPush.push(route) } label: { label }
        } else if let open {
            Button { open(route) } label: { label }
        } else {
            NavigationLink(value: route) { label }
        }
    }
}
