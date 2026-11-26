import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

// The avatar sheet's two entries for a hub that lists `look_uploads_states` (#15449, docs/apple/persona.md "Uploading an
// image"), one level down so the sheet itself stays as it was: Theme Color (automatic from the picture, or one the
// owner picks) and Pictures for Each State (automatic, or a picture of the state's own with its mark on or off).

/// The pages the avatar sheet pushes.
enum AvatarSheetPage: Hashable {
    case color
    case states
    /// A state's picture, its square.
    case crop(PersonaState)
}

/// The two entries under the fields, each saying what it is set to.
struct AvatarMoreSection: View {
    var model: AvatarUploadModel

    var body: some View {
        Section {
            NavigationLink(value: AvatarSheetPage.color) {
                LabeledContent {
                    HStack(spacing: Theme.Space.s) {
                        AvatarColorSwatch(color: model.shownColor)
                        Text(model.colorName).lineLimit(1)
                    }
                } label: {
                    Text("Theme Color").lineLimit(1)
                }
            }
            .accessibilityIdentifier("avatar-theme-color")
            NavigationLink(value: AvatarSheetPage.states) {
                LabeledContent {
                    Text(model.ownStates == 0 ? String(localized: "Automatic") : String(localized: "Custom")).lineLimit(1)
                } label: {
                    Text("Pictures for Each State").lineLimit(1).minimumScaleFactor(0.8)
                }
            }
            .accessibilityIdentifier("avatar-state-pictures")
        } footer: {
            Text("Both come from the picture unless you choose your own.")
        }
        .task { await model.estimateColor() }
    }
}

/// Theme Color: the automatic one, estimated from the picture as the hub will take it, or a color the owner picks.
struct AvatarColorPage: View {
    @Bindable var model: AvatarUploadModel

    var body: some View {
        Form {
            Section {
                Button { model.color = .automatic } label: {
                    HStack(spacing: Theme.Space.m) {
                        AvatarColorSwatch(color: model.automaticSeed)
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text("Automatic").foregroundStyle(Color.primary).lineLimit(1)
                            Text("From the avatar's picture").font(Theme.Font.label).foregroundStyle(Color.secondary)
                        }
                        Spacer(minLength: 0)
                        if model.color == .automatic {
                            ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent)
                        }
                    }
                    .contentShape(.rect)
                }
                .accessibilityAddTraits(model.color == .automatic ? .isSelected : [])
                .accessibilityIdentifier("avatar-color-automatic")
                ColorPicker(selection: chosen, supportsOpacity: false) {
                    HStack(spacing: Theme.Space.m) {
                        Text("Your Color").lineLimit(1)
                        Spacer(minLength: 0)
                        if case .chosen = model.color {
                            ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent)
                        }
                    }
                }
                .accessibilityIdentifier("avatar-color-chosen")
            } footer: {
                Text("With Theme Color set to Match Avatar in Settings › Appearance, the app's buttons, links and your messages take this color.")
            }
            if model.color == nil {
                Section {
                    Text("This avatar has no theme color yet. Choose one to give it one.")
                        .font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
        }
        .themedPage()
        .navigationTitle(String(localized: "Theme Color"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.estimateColor() }
    }

    /// The color well: the chosen color, else the automatic one; a color picked there becomes the chosen one.
    private var chosen: Binding<Color> {
        Binding {
            if case .chosen(let c) = model.color { return Color(hex: c) }
            return Color(hex: model.automaticSeed ?? AvatarColor.gray)
        } set: { color in
            model.color = .chosen(AvatarColorSwatch.rgb(color))
        }
    }
}

/// Pictures for Each State: a row per state with its picture as it will move. A row's menu picks a picture of its own
/// (Photos or a file, then its square), adjusts that square, or goes back to automatic; an own picture's mark (not
/// idle's) is a switch under it.
struct AvatarStatesPage: View {
    @Bindable var model: AvatarUploadModel
    @Binding var path: [AvatarSheetPage]
    @State private var choosingPhoto = false
    @State private var choosingFile = false
    /// The state the picture being picked is for.
    @State private var choosingFor: PersonaState?

    var body: some View {
        Form {
            Section {
                ForEach(PersonaState.allCases) { s in
                    row(s)
                    if s != .idle, let state = model.states[s], state.source != .automatic {
                        mark(s, on: state.mark)
                    }
                }
            } footer: {
                Text("A state without a picture of its own is made from the avatar's picture, with its mark. The mark shows which state it is: turn it off only when your picture shows that by itself.")
            }
            if let error = model.error {
                Section { LoadProblem(text: error) }
            }
        }
        .themedPage()
        .navigationTitle(String(localized: "Pictures for Each State"))
        .navigationBarTitleDisplayMode(.inline)
        .modifier(AvatarPicturePicking(choosingPhoto: $choosingPhoto, choosingFile: $choosingFile, picked: { data in
            guard let s = choosingFor, await model.receiveState(s, data) else { return }
            path.append(.crop(s))
        }, failed: model.failedToRead))
    }

    private func row(_ s: PersonaState) -> some View {
        let source = model.states[s]?.source ?? .automatic
        return Menu {
            AvatarPictureSourceButtons(slot: .state(s, editing: model.editing != nil)) { choose(s, file: $0 == .files) }
            if source == .picked {
                Button { path.append(.crop(s)) } label: { Label(String(localized: "Adjust Crop"), systemImage: Theme.Icon.crop) }
            }
            if source != .automatic {
                Button { model.useAutomatic(s) } label: { Label(String(localized: "Use Automatic"), systemImage: Theme.Icon.undo) }
            }
        } label: {
            HStack(spacing: Theme.Space.m) {
                AvatarStatePreview(model: model, state: s)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(s.title).foregroundStyle(Color.primary).lineLimit(1)
                    // A plain gray: inside a menu's label `.secondary` is the accent's.
                    Text(source == .automatic ? String(localized: "Automatic") : String(localized: "Your Picture"))
                        .font(Theme.Font.label).foregroundStyle(Color.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
                ThemedIcon(Theme.Icon.more).foregroundStyle(Theme.accent)
            }
            .contentShape(.rect)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(s.title))
        .accessibilityValue(source == .automatic ? String(localized: "Automatic") : String(localized: "Your Picture"))
        .accessibilityAddTraits(.isButton)
        .accessibilityIdentifier("avatar-state-\(s.rawValue)")
        .modifier(AvatarPictureDrop(enabled: model.made == nil && !model.uploading, radius: Theme.Radius.control,
                                    inset: -Theme.Space.xs, targetID: s.rawValue,
                                    dropped: { data in
                                        if await model.receiveState(s, data) { path.append(.crop(s)) }
                                    },
                                    failed: model.failedToRead))
    }

    /// The switch for an own picture's mark, saying what the mark is.
    private func mark(_ s: PersonaState, on: Bool) -> some View {
        Toggle(isOn: Binding { on } set: { model.states[s]?.mark = $0 }) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("State Mark").lineLimit(1)
                Text(Self.markName(s)).font(Theme.Font.label).foregroundStyle(.secondary)
            }
        }
        .accessibilityIdentifier("avatar-state-mark-\(s.rawValue)")
    }

    private func choose(_ s: PersonaState, file: Bool) {
        choosingFor = s
        if file { choosingFile = true } else { choosingPhoto = true }
    }

    /// What the hub draws on a state's picture to say which state it is.
    static func markName(_ s: PersonaState) -> String {
        switch s {
        case .idle: ""
        case .thinking: String(localized: "A thought bubble at the top")
        case .working: String(localized: "An orange arc along the edge")
        case .needsYou: String(localized: "A red ring around it")
        case .offline: String(localized: "Gray, with z's rising")
        }
    }
}

/// A state's picture, its square: the crop area as the sheet's. The square is kept as it is moved; ‹ goes back to the
/// states. Dropping a picture on the square replaces this state's picture (#15612).
struct AvatarStateCropPage: View {
    var model: AvatarUploadModel
    var state: PersonaState

    var body: some View {
        Form {
            if let framing = model.states[state]?.picked {
                Section {
                    AvatarCropArea(framing: framing) {
                        Task { await framing.makePreview(side: AvatarUploadModel.previewSide) }
                    }
                    .modifier(AvatarPictureDrop(enabled: model.made == nil && !model.uploading, radius: Theme.Radius.card,
                                                targetID: "state-crop",
                                                dropped: { data in
                                                    if await model.receiveState(state, data) {
                                                        await model.states[state]?.picked?.makePreview(side: AvatarUploadModel.previewSide)
                                                    }
                                                },
                                                failed: model.failedToRead))
                    .frame(maxWidth: .infinity)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                } footer: {
                    Text("Drag to move the photo. Pinch to zoom.")
                }
            }
            if let error = model.error {
                Section { LoadProblem(text: error) }
            }
        }
        .themedPage()
        .navigationTitle(state.title)
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// A state's picture as it will move: its own picture's square, the package's dot as it is (an edit), or the main
/// picture's square for an automatic state. Frame 0 alone with Reduce Motion or in Low Power Mode.
private struct AvatarStatePreview: View {
    var model: AvatarUploadModel
    var state: PersonaState

    var body: some View {
        let s = model.states[state] ?? .init()
        let framing: AvatarFraming? = s.source == .picked ? s.picked : s.source == .automatic ? model.main : nil
        AvatarFramesPreview(frames: framing?.preview ?? (framing == nil ? model.packageFrames[state] : nil))
            .task(id: framing.map(ObjectIdentifier.init)) {
                await framing?.makePreview(side: AvatarUploadModel.previewSide)
            }
    }
}

/// Frames in the dot's circle, playing as `PersonaDot` plays a package's (Core Animation swaps them).
struct AvatarFramesPreview: View {
    var frames: PersonaFrames?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Theme.Fill.well
            .overlay {
                if let frames {
                    VisibleWork { eligible in
                        PersonaFramesView(frames: frames, animating: !reduceMotion && !ProcessInfo.processInfo.isLowPowerModeEnabled,
                                          eligible: eligible)
                    }
                }
            }
            .scaledFrame(width: Theme.AvatarSheet.preview, height: Theme.AvatarSheet.preview)
            .clipShape(.circle)
            .accessibilityHidden(true)
    }
}

/// A theme color's round swatch; an empty ring while there is none yet.
struct AvatarColorSwatch: View {
    var color: UInt32?

    var body: some View {
        Circle()
            .fill(color.map { Color(hex: $0) } ?? Color.clear)
            .overlay(Circle().strokeBorder(Theme.AvatarSheet.swatchEdge, lineWidth: Theme.Size.line))
            .scaledFrame(width: Theme.AvatarSheet.swatch, height: Theme.AvatarSheet.swatch)
            .accessibilityHidden(true)
    }

    /// A color the color picker gave, in sRGB as the hub takes it (a wider color is brought inside sRGB).
    static func rgb(_ color: Color) -> UInt32 {
        let cg = UIColor(color).cgColor
        let srgb = CGColorSpace(name: CGColorSpace.sRGB).flatMap { cg.converted(to: $0, intent: .defaultIntent, options: nil) } ?? cg
        let c = srgb.components ?? []
        let rgb = c.count >= 3 ? Array(c.prefix(3)) : Array(repeating: c.first ?? 0, count: 3)
        return rgb.reduce(0) { ($0 << 8) | UInt32((min(1, max(0, $1)) * 255).rounded()) }
    }
}

/// Where a picture comes from (#2405, #15449): Photos or an image file, as data, so an animated one keeps moving.
/// Photos hands over the file as it is (`.current`), not a copy turned into JPEG.
struct AvatarPicturePicking: ViewModifier {
    @Binding var choosingPhoto: Bool
    @Binding var choosingFile: Bool
    var picked: (Data) async -> Void
    var failed: ((any Error)?) -> Void = { _ in }
    @State private var photo: PhotosPickerItem?

    func body(content: Content) -> some View {
        content
            .photosPicker(isPresented: $choosingPhoto, selection: $photo, matching: .images, preferredItemEncoding: .current)
            .onChange(of: photo) { _, item in
                guard let item else { return }
                photo = nil
                Task { await open(item) }
            }
            .fileImporter(isPresented: $choosingFile, allowedContentTypes: [.image]) { result in
                Task { await open(result) }
            }
    }

    /// The photo picked in Photos.
    private func open(_ item: PhotosPickerItem) async {
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else { return failed(nil) }
            await picked(data)
        } catch {
            failed(error)
        }
    }

    /// The image file picked.
    private func open(_ result: Result<URL, any Error>) async {
        do {
            let url = try result.get()
            let data = try await Task.detached(priority: .userInitiated) { try AvatarImage.readPickedFile(url) }.value
            await picked(data)
        } catch let e as CocoaError where e.code == .userCancelled {
            return
        } catch {
            failed(error)
        }
    }
}

/// How an avatar picture can be chosen from the device (#15612): Photos or an image file, always both.
enum AvatarPictureSource: String, CaseIterable, Identifiable {
    case photos
    case files

    var id: String { rawValue }

    var title: String {
        switch self {
        case .photos: String(localized: "Choose from Photos")
        case .files: String(localized: "Choose Image File")
        }
    }

    var icon: String {
        switch self {
        case .photos: Theme.Icon.photos
        case .files: Theme.Icon.file
        }
    }
}

/// Every picture slot in the avatar upload and edit flows (#15612).
enum AvatarPictureSlot: Hashable {
    case mainNew
    case mainEdit
    case state(PersonaState, editing: Bool)

    static var all: [AvatarPictureSlot] {
        [.mainNew, .mainEdit]
            + PersonaState.allCases.map { .state($0, editing: false) }
            + PersonaState.allCases.map { .state($0, editing: true) }
    }

    /// Every slot offers choosing from Photos and from Files.
    var pickSources: [AvatarPictureSource] { AvatarPictureSource.allCases }
}

/// The two pick buttons every picture slot's menu starts with: Photos and Files.
struct AvatarPictureSourceButtons: View {
    var slot: AvatarPictureSlot
    var choose: (AvatarPictureSource) -> Void

    var body: some View {
        ForEach(slot.pickSources) { source in
            Button { choose(source) } label: { Label(source.title, systemImage: source.icon) }
        }
    }
}

/// An image dragged onto an avatar picture slot or picker card (#15612), imported in its original format (`.image`) so
/// GIF, APNG and animated WebP keep all their frames. Files are copied out of the system's temporary drop scope and
/// read off the main actor (`read()`).
nonisolated struct DroppedAvatarImage: Transferable, Sendable {
    enum Payload: Sendable {
        case file(URL)
        case data(Data)
        case tooLarge
    }

    let payload: Payload

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .image) { received in
            try importFile(received.file)
        }
        DataRepresentation(importedContentType: .image) { data in
            importData(data)
        }
    }

    static func importFile(_ file: URL, limit: Int = AvatarImage.fileLimit) throws -> DroppedAvatarImage {
        if let size = try? file.resourceValues(forKeys: [.fileSizeKey]).fileSize, size > limit {
            return DroppedAvatarImage(payload: .tooLarge)
        }
        let dir = FileManager.default.temporaryDirectory.appending(path: "avatar-drops/" + UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let dest = dir.appending(path: file.lastPathComponent.isEmpty ? "image" : file.lastPathComponent)
        try FileManager.default.copyItem(at: file, to: dest)
        return DroppedAvatarImage(payload: .file(dest))
    }

    static func importData(_ data: Data, limit: Int = AvatarImage.fileLimit) -> DroppedAvatarImage {
        if data.count > limit {
            return DroppedAvatarImage(payload: .tooLarge)
        }
        return DroppedAvatarImage(payload: .data(data))
    }

    /// Reads the imported bytes off the main actor and removes any temporary copy.
    func read() async throws -> Data {
        let payload = self.payload
        return try await Task.detached(priority: .userInitiated) {
            switch payload {
            case .tooLarge:
                throw AvatarImage.Problem(errorDescription: String(localized: "This file is too large to be an avatar. Choose a smaller image."))
            case .data(let data):
                return data
            case .file(let url):
                defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
                return try AvatarImage.readPickedFile(url)
            }
        }.value
    }

    /// Removes any temporary copy held by an unused dropped image.
    func discard() {
        if case .file(let url) = payload {
            try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
        }
    }
}

/// Accepts an image dragged onto an avatar picture slot or picker card (#15612), highlights the target while dragged
/// over with the design system's accent border and tint, and routes the imported bytes through the same path as picking.
struct AvatarPictureDrop: ViewModifier {
    /// Whether this view accepts drops at all (false on built-in cards and older hubs: registers no `dropDestination`).
    var accepts: Bool = true
    /// Whether a drop is allowed right now (false while uploading, loading or deleting).
    var enabled: Bool = true
    var radius: CGFloat = Theme.Radius.card
    /// Padding applied to the highlight overlay (negative outsets around tight Form rows so the stroke has room).
    var inset: CGFloat = 0
    /// VoiceOver hint added when this target accepts drops (nil preserves a view's own hint).
    var accessibilityHint: String? = String(localized: "Drop an image here")
    /// Identifies this drop target for DEBUG screenshot switches (`-showAvatarDropTarget <id>`).
    var targetID: String? = nil
    #if DEBUG
    private static let forcedTarget = UserDefaults.standard.string(forKey: "showAvatarDropTarget")
    #endif
    var dropped: (Data) async -> Void
    var failed: ((any Error)?) -> Void = { _ in }
    @State private var targeted = false

    private var highlighted: Bool {
        guard accepts, enabled else { return false }
        #if DEBUG
        if let targetID, Self.forcedTarget == targetID { return true }
        #endif
        return targeted
    }

    func body(content: Content) -> some View {
        if accepts {
            dropView(content)
        } else {
            content
        }
    }

    @ViewBuilder private func dropView(_ content: Content) -> some View {
        let base = content
            .dropDestination(for: DroppedAvatarImage.self) { items, _ in
                guard enabled else {
                    discard(items)
                    return false
                }
                guard let first = items.first else {
                    failed(nil)
                    return false
                }
                discard(Array(items.dropFirst()))
                Task { await open(first) }
                return true
            } isTargeted: { on in
                targeted = enabled && on
            }
            .overlay {
                if highlighted {
                    let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
                    shape
                        .fill(Theme.AvatarSheet.dropTint)
                        .overlay(shape.strokeBorder(Theme.AvatarSheet.dropStroke, lineWidth: Theme.AvatarSheet.dropStrokeWidth))
                        .padding(inset)
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                }
            }
        if let accessibilityHint {
            base.accessibilityHint(accessibilityHint)
        } else {
            base
        }
    }

    private func open(_ item: DroppedAvatarImage) async {
        do {
            let data = try await item.read()
            await dropped(data)
        } catch {
            failed(error)
        }
    }

    private func discard(_ items: [DroppedAvatarImage]) {
        guard !items.isEmpty else { return }
        Task.detached(priority: .utility) {
            for item in items { item.discard() }
        }
    }
}
