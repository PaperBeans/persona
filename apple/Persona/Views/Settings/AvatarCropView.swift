import SwiftUI

/// Settings › Appearance › Avatar › New from Photo (#2405): the picked image in a square, a circle over it showing what the
/// avatar keeps; drag to move it, pinch (or, on the Mac, the slider) to zoom. A name (required) and a description, then
/// Upload (`AvatarUploadModel`): the hub makes the avatar and this device chooses it.
///
/// With a hub that lists `look_uploads_states` (#15449) two entries follow the fields, one level down: **Theme Color**
/// (`AvatarColorPage`) and **Pictures for Each State** (`AvatarStatesPage`). Left alone, the sheet does what it did. The
/// same sheet edits an uploaded avatar (Edit on its card): filled from its package, Save sends only what changed.
struct AvatarCropView: View {
    @Bindable var model: AvatarUploadModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.displayScale) private var scale
    @State private var path: [AvatarSheetPage] = []
    /// An edit's new main picture: Photos or a file.
    @State private var choosingPhoto = false
    @State private var choosingFile = false
    @FocusState private var field: Field?

    private enum Field { case name, subtitle }

    var body: some View {
        NavigationStack(path: $path) {
            Form {
                pictureSection
                // Made already: the picture and the names would not be sent again.
                    .disabled(model.made != nil)
                Section {
                    TextField(String(localized: "Name"), text: $model.name)
                        .focused($field, equals: .name)
                        .submitLabel(.next)
                        .onSubmit { field = .subtitle }
                        .accessibilityIdentifier("avatar-upload-name")
                    TextField(String(localized: "Description (Optional)"), text: $model.subtitle)
                        .focused($field, equals: .subtitle)
                        .submitLabel(.done)
                        .accessibilityIdentifier("avatar-upload-subtitle")
                } footer: {
                    Text("Persona goes by this name in the top bar and the chat. The description shows only here in Settings.")
                }
                .disabled(model.made != nil)
                if model.offersStates {
                    AvatarMoreSection(model: model)
                        .disabled(model.made != nil)
                }
                if let error = model.error {
                    Section { LoadProblem(text: error) }
                        .accessibilityIdentifier("avatar-upload-error")
                }
            }
            .themedPage()
            .navigationTitle(model.editing == nil ? String(localized: "New Avatar") : String(localized: "Edit Avatar"))
            .navigationBarTitleDisplayMode(.inline)
            .navigationDestination(for: AvatarSheetPage.self) { page in
                switch page {
                case .color: AvatarColorPage(model: model)
                case .states: AvatarStatesPage(model: model, path: $path)
                case .crop(let state): AvatarStateCropPage(model: model, state: state)
                }
            }
            // No Cancel while it uploads: the avatar would be made all the same. Once made, closing loses nothing: it is
            // in the list.
            .sheetCancel(unsaved: unsaved, when: !model.uploading) { dismiss() }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        field = nil
                        Task {
                            if await model.upload() {
                                Haptics.success()
                                dismiss()
                            } else {
                                Haptics.error()
                            }
                        }
                    } label: {
                        if model.uploading {
                            ProgressView()
                        } else if model.made != nil {
                            // Made but not downloaded here: the download again, not another upload.
                            Text("Try Again").lineLimit(1)
                        } else if model.editing != nil {
                            Text("Save").lineLimit(1)
                        } else {
                            // Its own key: Japanese says Create, short enough to leave the title whole.
                            Text(String(localized: "avatar.upload", defaultValue: "Upload")).lineLimit(1)
                        }
                    }
                    .disabled(!model.canUpload)
                    .accessibilityIdentifier("avatar-upload")
                }
            }
            .interactiveDismissDisabled(model.uploading)
            .modifier(AvatarPicturePicking(choosingPhoto: $choosingPhoto, choosingFile: $choosingFile, picked: { data in
                _ = await model.receiveMain(data)
            }, failed: model.failedToRead))
            .task { await model.prepare(facePixels: Int(Theme.Size.avatarCrop * scale)) }
            #if DEBUG
            .task { debugOpen() }
            #endif
        }
    }

    /// What closing would lose: a name typed for a new avatar, or an edit's changes, before the hub has them.
    private var unsaved: Bool {
        guard model.made == nil else { return false }
        return model.editing == nil ? !model.trimmedName.isEmpty : model.changed
    }

    /// The main picture: a new avatar's crop; an edit's face as it is, or the crop of the new picture picked for it.
    @ViewBuilder private var pictureSection: some View {
        let canDrop = model.made == nil && !model.uploading
        if let main = model.main {
            Section {
                AvatarCropArea(framing: main) { Task { await model.estimateColor() } }
                    .modifier(AvatarPictureDrop(enabled: canDrop, radius: Theme.Radius.card, targetID: "main",
                                                dropped: { _ = await model.receiveMain($0) },
                                                failed: model.failedToRead))
                    .frame(maxWidth: .infinity)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                if model.editing != nil { changePicture }
            } footer: {
                Text("Drag to move the photo. Pinch to zoom.")
            }
        } else {
            Section {
                AvatarCurrentFace(face: model.packageFace)
                    .modifier(AvatarPictureDrop(enabled: canDrop, radius: Theme.Radius.card, targetID: "main",
                                                dropped: { _ = await model.receiveMain($0) },
                                                failed: model.failedToRead))
                    .frame(maxWidth: .infinity)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                changePicture
            }
        }
    }

    /// An edit's new main picture (Photos, a file), or back to the one it has.
    private var changePicture: some View {
        Menu {
            AvatarPictureSourceButtons(slot: .mainEdit) { source in
                if source == .files { choosingFile = true } else { choosingPhoto = true }
            }
            if model.main != nil {
                Button {
                    model.keepMain()
                    Task { await model.estimateColor() }
                } label: { Label(String(localized: "Keep Current Picture"), systemImage: Theme.Icon.undo) }
            }
        } label: {
            Text("Change Picture").lineLimit(1)
        }
        .accessibilityIdentifier("avatar-change-picture")
    }

    #if DEBUG
    /// `-showAvatarCropPage color|states` opens that page of the sheet once it is up (`PersonaPicker.debugOpen`).
    private func debugOpen() {
        switch UserDefaults.standard.string(forKey: "showAvatarCropPage") {
        case "color": path = [.color]
        case "states": path = [.states]
        default: break
        }
    }
    #endif
}

/// The picture under a square window, dimmed outside the circle the avatar keeps: drag to move it, pinch to zoom (VoiceOver
/// swipes up and down). `settled`: a drag or pinch ended, or VoiceOver zoomed (what follows the square, such as the
/// automatic color, is worked out then, not on every frame of a drag).
struct AvatarCropArea: View {
    @Bindable var framing: AvatarFraming
    var settled: () -> Void = {}
    /// The drag so far: each change moves the square by what was added.
    @State private var dragged: CGSize = .zero
    /// The zoom when the pinch began.
    @State private var pinchedFrom: Double?

    var body: some View {
        GeometryReader { geo in
            let side = min(geo.size.width, geo.size.height)
            let crop = framing.crop
            let k = side / crop.side // points per pixel of the image
            Theme.Fill.well
                .overlay {
                    Image(decorative: framing.image, scale: 1)
                        .resizable()
                        .interpolation(.high)
                        .frame(width: crop.width * k, height: crop.height * k)
                        .offset(x: (crop.width / 2 - crop.center.x) * k, y: (crop.height / 2 - crop.center.y) * k)
                }
                .overlay { CropShade().fill(Theme.Crop.outside, style: FillStyle(eoFill: true)) }
                .overlay { Circle().strokeBorder(Theme.Crop.edge, lineWidth: Theme.Size.lineMedium) }
                .frame(width: side, height: side)
                .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .contentShape(.rect)
                .gesture(drag(side).simultaneously(with: pinch))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .aspectRatio(1, contentMode: .fit)
        .frame(maxWidth: Theme.Size.avatarCrop)
        .accessibilityElement()
        .accessibilityLabel(String(localized: "Crop"))
        .accessibilityValue(String(localized: "Zoom \(framing.crop.zoom.formatted(.percent.precision(.fractionLength(0))))"))
        .accessibilityAdjustableAction { direction in
            switch direction {
            case .increment: framing.crop = framing.crop.zoomed(to: framing.crop.zoom * Self.zoomStep)
            case .decrement: framing.crop = framing.crop.zoomed(to: framing.crop.zoom / Self.zoomStep)
            @unknown default: break
            }
            settled()
        }
        .accessibilityIdentifier("avatar-crop")
    }

    /// A step of the adjustable action (VoiceOver's swipe up and down).
    private static let zoomStep = 1.25

    /// The picture follows the finger; each change moves it by what the drag added (stopping at the image's edges).
    private func drag(_ side: CGFloat) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { v in
                let step = CGSize(width: v.translation.width - dragged.width, height: v.translation.height - dragged.height)
                dragged = v.translation
                framing.crop = framing.crop.panned(by: step, viewSide: side)
            }
            .onEnded { _ in
                dragged = .zero
                settled()
            }
    }

    private var pinch: some Gesture {
        MagnifyGesture()
            .onChanged { v in
                let from = pinchedFrom ?? framing.crop.zoom
                pinchedFrom = from
                framing.crop = framing.crop.zoomed(to: from * v.magnification)
            }
            .onEnded { _ in
                pinchedFrom = nil
                settled()
            }
    }
}

/// An edit's face as the package has it, in the crop's place, until a new picture is picked.
private struct AvatarCurrentFace: View {
    var face: UIImage?

    var body: some View {
        Theme.Fill.well
            .overlay {
                if let face {
                    Image(uiImage: face).resizable().interpolation(.high).scaledToFill().clipShape(.circle)
                } else {
                    ProgressView()
                }
            }
            .aspectRatio(1, contentMode: .fit)
            .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .frame(maxWidth: Theme.Size.avatarCrop)
            .accessibilityElement()
            .accessibilityLabel(String(localized: "Current picture"))
            .accessibilityIdentifier("avatar-current-face")
    }
}

/// The square with the circle cut out (even-odd): what the avatar leaves out.
private nonisolated struct CropShade: Shape {
    func path(in rect: CGRect) -> Path {
        var p = Path(rect)
        p.addEllipse(in: rect)
        return p
    }
}
