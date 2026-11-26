import CoreGraphics
import Foundation
import ImageIO
import Observation
import UIKit
import UniformTypeIdentifiers
import PersonaWire

// Settings › Appearance › Avatar › New from Photo (#2405, docs/apple/persona.md "Uploading an image"): the user picks an
// image, crops it square (a circle shows what the avatar keeps), names it, and the hub makes a package of it
// (POST /api/v1/looks/uploads, only when the hub lists `look_uploads`). The phone then downloads the package and chooses
// it, as when a package is chosen in the picker; the hub chooses it too, for the other devices. Avatars made this way
// can be deleted again (DELETE /api/v1/looks/uploads/{id}).
//
// A hub that also lists `look_uploads_states` (#15449) takes more, all of it optional behind two entries of the same
// sheet: a theme color (automatic, from the picture, or one the owner picks), a picture of its own for any state (with
// or without the state's mark), and animated pictures, sent as picked. Such an avatar can be edited later (Edit on its
// card, PATCH /api/v1/looks/uploads/{id}), sending only what changed.

// MARK: The hub

/// POST /api/v1/looks/uploads's body. Only `name`, `subtitle`, `image` and `choose` go to a hub without
/// `look_uploads_states`, as before.
nonisolated struct LookUpload: Encodable, Sendable, Equatable {
    var name: String
    var subtitle: String?
    /// Base64 of a PNG or JPEG: the crop, square and upright; or of an animated picture as picked, with `crop`.
    var image: String
    /// The square to keep of an animated picture, in its pixels.
    var crop: LookCrop?
    /// Chosen on the hub too, so the other devices follow.
    var choose: Bool
    /// The states with a picture of their own, by raw value.
    var states: [String: LookStateChange]?
    /// "#RRGGBB"; left out for the automatic one (the hub's default).
    var color: String?
}

/// A state's part of an upload or an edit: a picture of its own (with its mark on or off), only its mark, or back to
/// automatic.
nonisolated struct LookStateChange: Encodable, Sendable, Equatable {
    var image: String?
    var crop: LookCrop?
    /// Left out for idle, which has no mark.
    var mark: Bool?
    var auto: Bool?

    static func picture(_ p: LookPicture, mark: Bool?) -> LookStateChange {
        LookStateChange(image: p.image, crop: p.crop, mark: mark)
    }
}

/// PATCH /api/v1/looks/uploads/{id}'s body: only what changed (at least one field).
nonisolated struct LookEdit: Encodable, Sendable, Equatable {
    var name: String?
    /// "" clears the description.
    var subtitle: String?
    var image: String?
    var crop: LookCrop?
    var states: [String: LookStateChange]?
    /// "auto" or "#RRGGBB".
    var color: String?

    var isEmpty: Bool { self == LookEdit() }
}

/// The avatar the hub made, as GET /api/v1/looks lists it (only what the phone uses).
nonisolated struct UploadedLook: Decodable, Sendable, Hashable {
    var id: String
    var name: String?
    var rev: String?
    var bytes: Int64?
    var manifest: PersonaManifest?
    var uploaded: Bool?
}

nonisolated extension APIClient {
    /// POST /api/v1/looks/uploads: makes an avatar from the image (docs/protocol/protocol.md). A refusal is an APIError
    /// whose message is the hub's, in the user's language, and whose `code` says which (no_name, too_large…).
    func uploadLook(_ upload: LookUpload) async throws -> UploadedLook {
        struct Answer: Decodable { var look: UploadedLook }
        // A photo of a megabyte or two in base64: more time than a plain request.
        let data = try await request("POST", "/api/v1/looks/uploads", body: upload, timeout: 120)
        return try JSON.decoder.decode(Answer.self, from: data).look
    }

    /// PATCH /api/v1/looks/uploads/{id}: changes an uploaded avatar in place (#15449); it keeps its id, its rev changes.
    func editUploadedLook(_ id: String, _ edit: LookEdit) async throws -> UploadedLook {
        struct Answer: Decodable { var look: UploadedLook }
        let data = try await request("PATCH", "/api/v1/looks/uploads/\(id)", body: edit, timeout: 120)
        return try JSON.decoder.decode(Answer.self, from: data).look
    }

    /// DELETE /api/v1/looks/uploads/{id}: only an avatar made from an upload (409 `not_uploaded` for any other).
    func deleteUploadedLook(_ id: String) async throws {
        _ = try await request("DELETE", "/api/v1/looks/uploads/\(id)")
    }
}

// MARK: The image

/// The picked image made ready to crop, and the crop made ready for the hub.
nonisolated enum AvatarImage {
    /// Pictures are decoded no larger than this: a 48-megapixel photo would take 190 MB, and the crop sent is 1024 px.
    static let loadSide = 4096
    /// The largest crop sent (the hub keeps 512 px at most; docs/apple/persona.md "Uploading an image").
    static let outputSide = 1024
    /// The largest state picture sent: a state is a 144 px dot.
    static let stateSide = 512
    /// The hub refuses a smaller side (`too_small`).
    static let minSide = 64
    /// A photo whose PNG is larger than this goes as JPEG instead.
    static let pngLimit = 1_500_000
    /// A file larger than this is not read at all.
    static let fileLimit = 100_000_000

    /// Why a picked file can't become an avatar.
    struct Problem: LocalizedError, Equatable {
        var errorDescription: String?
    }

    /// Decodes a picked image upright (its orientation applied), the first frame of an animated one, at most `loadSide`.
    static func load(_ data: Data) throws -> CGImage {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil), CGImageSourceGetCount(src) > 0 else {
            throw Problem(errorDescription: String(localized: "This file isn't an image Persona can read. Choose a photo, PNG, JPEG, WebP or GIF."))
        }
        let opts: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: loadSide,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(src, 0, opts as CFDictionary) else {
            throw Problem(errorDescription: String(localized: "This file isn't an image Persona can read. Choose a photo, PNG, JPEG, WebP or GIF."))
        }
        guard min(image.width, image.height) >= minSide else {
            throw Problem(errorDescription: String(localized: "This image is too small. Choose one at least \(minSide) pixels on each side."))
        }
        return image
    }

    /// Reads an image file the user picked (the Mac's open panel, the Files app), within its security scope.
    static func readPickedFile(_ url: URL) throws -> Data {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        if let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, size > fileLimit {
            throw Problem(errorDescription: String(localized: "This file is too large to be an avatar. Choose a smaller image."))
        }
        return try Data(contentsOf: url)
    }

    /// The part of `image` inside `rect` (its pixels), square, at most `side`, in sRGB: the hub reads no color profile.
    static func render(_ image: CGImage, rect: CGRect, side maxSide: Int = outputSide) -> CGImage? {
        guard let cut = image.cropping(to: rect.integral) else { return nil }
        let side = min(maxSide, min(cut.width, cut.height))
        let opaque = isOpaque(image)
        guard side > 0, let space = CGColorSpace(name: CGColorSpace.sRGB),
              let ctx = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                                  bitmapInfo: (opaque ? CGImageAlphaInfo.noneSkipLast : CGImageAlphaInfo.premultipliedLast).rawValue)
        else { return nil }
        ctx.interpolationQuality = .high
        ctx.draw(cut, in: CGRect(x: 0, y: 0, width: side, height: side))
        return ctx.makeImage()
    }

    /// PNG; a photo whose PNG is large goes as JPEG (an image with transparency stays PNG: JPEG would fill it black).
    static func encode(_ image: CGImage) -> Data? {
        let png = encoded(image, as: .png)
        if let png, png.count <= pngLimit || !isOpaque(image) { return png }
        return encoded(image, as: .jpeg, quality: 0.9) ?? png
    }

    static func isOpaque(_ image: CGImage) -> Bool {
        switch image.alphaInfo {
        case .none, .noneSkipFirst, .noneSkipLast: true
        default: false
        }
    }

    private static func encoded(_ image: CGImage, as type: UTType, quality: Double? = nil) -> Data? {
        let out = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(out, type.identifier as CFString, 1, nil) else { return nil }
        let props = quality.map { [kCGImageDestinationLossyCompressionQuality: $0] as CFDictionary }
        CGImageDestinationAddImage(dest, image, props)
        return CGImageDestinationFinalize(dest) ? out as Data : nil
    }
}

// MARK: The crop

/// Where the square is on the image, in its pixels. Zoom 1 is the shorter side across; zooming in stops where the square
/// would be under 64 px (the hub's smallest) or an eighth of the shorter side.
nonisolated struct AvatarCrop: Equatable, Sendable {
    static let zoomLimit = 8.0

    let width: Double
    let height: Double
    private(set) var zoom: Double = 1
    /// The square's center.
    private(set) var center: CGPoint

    init(width: Int, height: Int) {
        self.width = Double(width)
        self.height = Double(height)
        center = CGPoint(x: Double(width) / 2, y: Double(height) / 2)
    }

    var shorter: Double { min(width, height) }
    var maxZoom: Double { max(1, min(Self.zoomLimit, shorter / Double(AvatarImage.minSide))) }
    /// The square's side in the image's pixels.
    var side: Double { shorter / zoom }

    /// The square in whole pixels, inside the image.
    var rect: CGRect {
        let s = max(1, min(shorter, side.rounded()))
        let x = min(max(0, (center.x - s / 2).rounded()), width - s)
        let y = min(max(0, (center.y - s / 2).rounded()), height - s)
        return CGRect(x: x, y: y, width: s, height: s)
    }

    /// Zoomed about the square's center, within 1…maxZoom.
    func zoomed(to z: Double) -> AvatarCrop {
        var c = self
        c.zoom = min(max(1, z.isFinite ? z : 1), maxZoom)
        c.clamp()
        return c
    }

    /// Moved by a drag of `translation` points over a square `viewSide` points wide: the picture follows the finger, so
    /// the square goes the other way. It stops at the image's edges.
    func panned(by translation: CGSize, viewSide: Double) -> AvatarCrop {
        guard viewSide > 0 else { return self }
        var c = self
        let k = side / viewSide
        c.center = CGPoint(x: center.x - translation.width * k, y: center.y - translation.height * k)
        c.clamp()
        return c
    }

    private mutating func clamp() {
        let h = side / 2
        center = CGPoint(x: min(max(center.x, h), width - h), y: min(max(center.y, h), height - h))
    }
}

/// A picture picked in the sheet, where its square is, and the square's preview once made (#15449).
@MainActor @Observable
final class AvatarFraming {
    let picture: AvatarPicture
    var crop: AvatarCrop
    /// The square as it will move, small: an animated picture's frames, a still one's one frame. Made off the main
    /// actor (`makePreview`).
    private(set) var preview: PersonaFrames?
    @ObservationIgnored private var previewed: CGRect?

    init(_ picture: AvatarPicture) {
        self.picture = picture
        crop = AvatarCrop(width: picture.image.width, height: picture.image.height)
    }

    var image: CGImage { picture.image }

    /// Makes the preview of the square as it is now, `side` pixels wide (nothing when it is made already).
    func makePreview(side: Int) async {
        let rect = crop.rect, picture = picture
        guard previewed != rect else { return }
        let frames = await Task.detached(priority: .userInitiated) { picture.frames(rect: rect, side: side) }.value
        // Moved meanwhile: the next call makes that one.
        guard crop.rect == rect else { return }
        previewed = rect
        preview = frames
    }

    /// What is sent for it: a still picture's square drawn at most `side` pixels, an animated one as picked with its
    /// square. Encoded off the main actor.
    func payload(side: Int) async -> LookPicture? {
        let rect = crop.rect, picture = picture
        return await Task.detached(priority: .userInitiated) { picture.payload(rect: rect, side: side) }.value
    }

    /// The automatic theme color's seed for the square (frame 0), estimated off the main actor.
    func estimatedSeed() async -> UInt32? {
        let rect = crop.rect, image = picture.image
        return await Task.detached(priority: .utility) { AvatarImage.render(image, rect: rect, side: AvatarColor.side).map(AvatarColor.seed) }.value
    }
}

// MARK: The sheet

/// The avatar sheet (`AvatarCropView`), for a new avatar (New from Photo) or to edit an uploaded one (Edit on its card,
/// with a hub that lists `look_uploads_states`): the picture and where its square is, the name and description, and,
/// with such a hub, the theme color and a picture per state. A new avatar is uploaded, then downloaded and chosen on this
/// phone (the hub chose it for the others); an edit sends only what changed, and the list is read again.
@MainActor @Observable
final class AvatarUploadModel: Identifiable {
    struct Deps {
        var api: () -> APIClient?
        var store: () -> PersonaStore
        static var live: Deps { .init(api: { AppModel.shared.api }, store: { AppModel.shared.personas }) }
    }

    /// The theme color the owner chose: the hub's automatic one, from the main picture, or a color of their own.
    enum LookColor: Equatable {
        case automatic
        case chosen(UInt32)

        /// What is sent (`color`).
        var wire: String {
            switch self {
            case .automatic: "auto"
            case .chosen(let c): AvatarColor.hex(c)
            }
        }
    }

    /// A state's picture in the sheet.
    struct StatePicture {
        enum Source: Equatable {
            /// Made from the main picture.
            case automatic
            /// Its own picture as the package has it (an edit).
            case kept
            /// A picture picked here.
            case picked
        }

        var source: Source = .automatic
        var picked: AvatarFraming?
        /// The hub draws the state's mark on its own picture (idle has none).
        var mark = true
    }

    /// What an edit started from, to send only what changed.
    private struct Initial {
        var name = ""
        var subtitle = ""
        /// The states with their own picture, and whether each is marked.
        var states: [PersonaState: Bool] = [:]
        var color: LookColor?
    }

    /// The hub's bounds (`bad_name`, `bad_subtitle`), in characters as it counts them (Unicode scalars).
    static let maxName = 40
    static let maxSubtitle = 80
    /// A state preview's side in pixels: the dot's.
    static let previewSide = 144

    let id = UUID()
    /// The uploaded avatar being edited; nil for a new one.
    let editing: Persona?
    /// The hub takes a theme color, a picture per state and animated pictures (`look_uploads_states`).
    let offersStates: Bool
    /// The main picture: always a new avatar's; in an edit, a new one once it is picked.
    private(set) var main: AvatarFraming?
    var name = "" {
        didSet { if let cut = Self.clipped(name, to: Self.maxName) { name = cut } }
    }
    var subtitle = "" {
        didSet { if let cut = Self.clipped(subtitle, to: Self.maxSubtitle) { subtitle = cut } }
    }
    /// Each state's picture; automatic unless the owner gave it one.
    var states: [PersonaState: StatePicture]
    /// The theme color; nil for an avatar edited that has none yet (made before the hub wrote colors).
    var color: LookColor?
    /// The automatic color's seed as the hub will take it: estimated from the main picture's square, or the package's own
    /// while its picture is unchanged.
    private(set) var automaticSeed: UInt32?
    /// An edit: the package's dots as they are now, and its face, decoded off the main actor (`prepare`).
    private(set) var packageFrames: [PersonaState: PersonaFrames] = [:]
    private(set) var packageFace: UIImage?
    private(set) var uploading = false
    /// Why the last upload failed: the hub's own words when it refused.
    private(set) var error: String?
    /// The avatar the hub made or changed (after a successful request). Kept when it could not be downloaded here yet:
    /// trying again downloads it, without sending the pictures again (the hub would make a second one).
    private(set) var made: UploadedLook?
    /// The new pack is on this device.
    private(set) var done = false
    @ObservationIgnored private var receipt: PersonaStore.SelectionReceipt?
    @ObservationIgnored private var editRequest: PersonaStore.Mark?
    @ObservationIgnored private let initial: Initial
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var packageSeed: UInt32?

    /// A new avatar from the picked picture; `states`: the hub lists `look_uploads_states`.
    init(picture: AvatarPicture, states: Bool = false, deps: Deps = .live) {
        editing = nil
        offersStates = states
        main = AvatarFraming(picture)
        self.states = Dictionary(uniqueKeysWithValues: PersonaState.allCases.map { ($0, StatePicture()) })
        color = .automatic
        initial = Initial(color: .automatic)
        self.deps = deps
    }

    /// A new avatar from an image already decoded upright (a still picture).
    convenience init(image: CGImage, deps: Deps = .live) {
        self.init(picture: AvatarPicture(image: image), deps: deps)
    }

    /// Editing an uploaded avatar, filled from its package: its name, description, theme color and states.
    init(editing p: Persona, deps: Deps = .live) {
        editing = p
        offersStates = true
        let source = p.manifest?.source
        var own: [PersonaState: Bool] = [:]
        for (key, state) in source?.states ?? [:] where state.own {
            if let s = PersonaState(rawValue: key) { own[s] = s == .idle || state.mark }
        }
        states = Dictionary(uniqueKeysWithValues: PersonaState.allCases.map { s in
            (s, own[s].map { StatePicture(source: .kept, mark: $0) } ?? StatePicture())
        })
        let color: LookColor? = switch (source?.color, p.manifest?.colors) {
        case let (c?, _) where !c.auto: c.seed.flatMap(PersonaPalette.hex).map(LookColor.chosen) ?? .automatic
        case (_?, _), (nil, _?): .automatic
        case (nil, nil): nil // made before the hub wrote colors: none yet
        }
        let seed = source?.color.flatMap { $0.auto ? $0.seed.flatMap(PersonaPalette.hex) : nil }
        packageSeed = seed
        self.color = color
        automaticSeed = seed
        let name = p.manifest?.name ?? p.name
        let subtitle = (p.manifest?.subtitle ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        self.name = name
        self.subtitle = subtitle
        initial = Initial(name: name, subtitle: subtitle, states: own, color: color)
        self.deps = deps
    }

    var trimmedName: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }
    var trimmedSubtitle: String { subtitle.trimmingCharacters(in: .whitespacesAndNewlines) }
    /// The sheet's button can act: Upload (a name is required), Save (something changed), or Try Again.
    var canUpload: Bool {
        guard !uploading, !done else { return false }
        if made != nil { return true }
        return !trimmedName.isEmpty && (editing == nil || changed)
    }

    /// The states with a picture of their own (picked here or kept).
    var ownStates: Int { states.values.filter { $0.source != .automatic }.count }

    /// The theme color the swatch shows: the chosen one, the automatic one as estimated, or none.
    var shownColor: UInt32? {
        switch color {
        case .chosen(let c): c
        case .automatic: automaticSeed
        case nil: nil
        }
    }

    /// What the theme color is set to, in a word.
    var colorName: String {
        switch color {
        case .chosen: String(localized: "Custom")
        case .automatic: String(localized: "Automatic")
        case nil: String(localized: "Not set")
        }
    }

    /// Something differs from the package (an edit).
    var changed: Bool {
        trimmedName != initial.name || trimmedSubtitle != initial.subtitle || main != nil || color != initial.color
            || PersonaState.allCases.contains { stateChange($0, picture: nil) != nil }
    }

    /// `text` cut to `max` Unicode scalars without splitting a character; nil when it fits.
    static func clipped(_ text: String, to max: Int) -> String? {
        guard text.unicodeScalars.count > max else { return nil }
        var out = "", n = 0
        for ch in text {
            n += ch.unicodeScalars.count
            if n > max { break }
            out.append(ch)
        }
        return out
    }

    // MARK: Choosing

    /// Picked bytes as a picture for the sheet (an animated one keeps moving); nil, with `error`, when they can't be one.
    func load(_ data: Data) async -> AvatarPicture? {
        do {
            let picture = try await AvatarUploadsModel.load(data, animated: offersStates)
            error = nil
            return picture
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    /// The picked item could not be read at all.
    func failedToRead(_ error: (any Error)?) {
        self.error = error?.shown ?? String(localized: "Couldn't open this image. Try another one.")
    }

    /// A new main picture (an edit or a replacement dropped on the crop): the square starts centered.
    func replaceMain(_ picture: AvatarPicture) {
        main = AvatarFraming(picture)
        automaticSeed = nil
    }

    /// Loads picked or dropped bytes as the new main picture (new or edit sheet) and refreshes the automatic theme
    /// color estimate (#15612). On failure sets `error` and leaves the current main picture unchanged.
    @discardableResult
    func receiveMain(_ data: Data) async -> Bool {
        guard made == nil, !uploading else { return false }
        guard let picture = await load(data) else { return false }
        replaceMain(picture)
        if offersStates { await estimateColor() }
        return true
    }

    /// Back to the package's main picture (an edit).
    func keepMain() {
        guard editing != nil else { return }
        main = nil
        automaticSeed = packageSeed
    }

    /// A picture of its own for `state`; its square starts centered, its mark as it was.
    func give(_ state: PersonaState, _ picture: AvatarPicture) {
        states[state, default: StatePicture()].source = .picked
        states[state, default: StatePicture()].picked = AvatarFraming(picture)
    }

    /// Loads picked or dropped bytes as `state`'s own picture (#15612). On failure sets `error` and leaves `state`
    /// unchanged.
    @discardableResult
    func receiveState(_ state: PersonaState, _ data: Data) async -> Bool {
        guard offersStates, made == nil, !uploading else { return false }
        guard let picture = await load(data) else { return false }
        give(state, picture)
        return true
    }

    /// `state` made from the main picture again.
    func useAutomatic(_ state: PersonaState) {
        states[state] = StatePicture(source: .automatic, picked: nil, mark: true)
    }

    /// Estimates the automatic color from the main picture's square as it is now (the package's while unchanged).
    func estimateColor() async {
        if let main {
            let rect = main.crop.rect
            let seed = await main.estimatedSeed()
            guard self.main === main, main.crop.rect == rect else { return }
            automaticSeed = seed
        } else if packageSeed == nil, let face = packageFace?.cgImage {
            let seed = await Task.detached(priority: .utility) { AvatarColor.seed(face) }.value
            packageSeed = seed
            if main == nil { automaticSeed = seed }
        }
    }

    // MARK: An edit's package

    /// An edit: downloads the package when it is not on this phone, then reads its dots and face off the main actor, so
    /// the sheet shows the states as they are now.
    func prepare(facePixels: Int) async {
        guard let p = editing, packageFrames.isEmpty, let manifest = p.manifest else { return }
        let store = deps.store()
        if !store.isOnPhone(p), let api = deps.api() { await store.download(p, api) }
        let folder = store.directory(p)
        let files = PersonaState.allCases.compactMap { s in
            manifest.dotState(s).map { PackageDot(state: s, url: folder.appending(path: $0.file), fps: $0.fps) }
        }
        let face = manifest.avatarFile(points: Double(facePixels), scale: 1).map { folder.appending(path: $0) }
        let read = await Task.detached(priority: .userInitiated) { PackageDot.read(files, face: face) }.value
        packageFrames = read.frames
        packageFace = read.face
        if packageSeed == nil { await estimateColor() }
    }

    // MARK: Sending

    /// The request's state changes: a new avatar's own pictures; an edit's pictures, marks and states back to automatic.
    /// Nil when a picture could not be prepared.
    private func stateChanges() async -> [String: LookStateChange]? {
        var out: [String: LookStateChange] = [:]
        for s in PersonaState.allCases {
            var picture: LookPicture?
            if let framing = states[s]?.picked, states[s]?.source == .picked {
                guard let p = await framing.payload(side: AvatarImage.stateSide) else { return nil }
                picture = p
            }
            if let change = stateChange(s, picture: picture) { out[s.rawValue] = change }
        }
        return out
    }

    /// What is sent for `state` (nil: nothing), given its picked picture's payload.
    private func stateChange(_ s: PersonaState, picture: LookPicture?) -> LookStateChange? {
        guard let now = states[s] else { return nil }
        let mark = s == .idle ? nil : now.mark
        let had = initial.states[s]
        switch now.source {
        case .picked:
            // Counted as a change before its payload is made (`changed`); sent with it.
            return picture.map { .picture($0, mark: mark) } ?? LookStateChange(image: "", mark: mark)
        case .kept:
            guard s != .idle, let had, had != now.mark else { return nil }
            return LookStateChange(mark: now.mark)
        case .automatic:
            return had == nil ? nil : LookStateChange(auto: true)
        }
    }

    /// POST /api/v1/looks/uploads's body for the main picture's payload and the states' changes.
    func uploadBody(main: LookPicture, states: [String: LookStateChange] = [:]) -> LookUpload {
        var body = LookUpload(name: trimmedName, subtitle: trimmedSubtitle.isEmpty ? nil : trimmedSubtitle, image: main.image, choose: true)
        guard offersStates else { return body }
        body.crop = main.crop
        body.states = states.isEmpty ? nil : states
        if case .chosen = color { body.color = color?.wire }
        return body
    }

    /// PATCH's body: only what changed.
    func editBody(main: LookPicture?, states: [String: LookStateChange]) -> LookEdit {
        var edit = LookEdit()
        if trimmedName != initial.name { edit.name = trimmedName }
        if trimmedSubtitle != initial.subtitle { edit.subtitle = trimmedSubtitle }
        if let main {
            edit.image = main.image
            edit.crop = main.crop
        }
        edit.states = states.isEmpty ? nil : states
        if let color, color != initial.color { edit.color = color.wire }
        return edit
    }

    /// Upload (or, for an edit, Save) and download the avatar. A retry after the hub accepted it finishes getting the
    /// pack. One at a time: a second tap while it runs does nothing.
    func upload() async -> Bool {
        guard canUpload else { return false }
        if editing != nil { return await save() }
        let store = deps.store()
        guard let api = deps.api(), let request = receipt?.started ?? store.mark(for: api), store.isCurrent(request), let main else {
            error = String(localized: "Not connected to the hub yet")
            return false
        }
        uploading = true
        defer { uploading = false }
        error = nil
        let accepted: PersonaStore.SelectionReceipt
        if let receipt {
            accepted = receipt
        } else {
            guard let picture = await main.payload(side: AvatarImage.outputSide), let changes = offersStates ? await stateChanges() : [:] else {
                error = String(localized: "Couldn't prepare this image. Try another one.")
                return false
            }
            do {
                guard store.isCurrent(request) else { return false }
                let look = try await api.uploadLook(uploadBody(main: picture, states: changes))
                accepted = store.receipt(for: look.id, since: request)
                receipt = accepted
                made = look
            } catch {
                self.error = Self.message(for: error)
                return false
            }
        }
        guard await store.adopt(accepted, api: api) else {
            error = String(localized: "The avatar was made, but this device couldn't download it. Try again.")
            return false
        }
        done = true
        return true
    }

    /// Sends the edit (only what changed), then reads the list again, which brings the new package to this phone when
    /// it is the chosen one; the choice stays as it was. Saved but not downloaded: trying again only downloads it.
    private func save() async -> Bool {
        guard let p = editing else { return false }
        let store = deps.store()
        guard let api = deps.api(), let request = editRequest ?? store.mark(for: api), store.isCurrent(request) else {
            error = String(localized: "Not connected to the hub yet")
            return false
        }
        uploading = true
        defer { uploading = false }
        error = nil
        if made == nil {
            var picture: LookPicture?
            if let main {
                picture = await main.payload(side: AvatarImage.outputSide)
                if picture == nil {
                    error = String(localized: "Couldn't prepare this image. Try another one.")
                    return false
                }
            }
            guard let changes = await stateChanges() else {
                error = String(localized: "Couldn't prepare this image. Try another one.")
                return false
            }
            let edit = editBody(main: picture, states: changes)
            guard !edit.isEmpty else { return false }
            do {
                guard store.isCurrent(request) else { return false }
                made = try await api.editUploadedLook(p.id, edit)
                editRequest = request
            } catch {
                self.error = Self.message(for: error)
                return false
            }
        }
        // Read after the hub answered: a refresh under way may have asked before the edit.
        await store.reload(api, since: request)
        let listed = store.offered.first { $0.id == p.id }
        guard store.isCurrent(request), let listed, listed.rev != p.rev || made?.rev == p.rev,
              store.chosenID != p.id || store.isOnPhone(listed) else {
            error = String(localized: "The avatar was saved, but this device couldn't download it. Try again.")
            return false
        }
        done = true
        return true
    }

    /// The hub's message for a refusal as it is (it is in the user's language already); for no answer at all, a line
    /// that says so.
    static func message(for error: any Error) -> String {
        error.shown ?? String(localized: "Couldn't reach the hub. Check the connection and try again.")
    }
}

/// One state's dot file of the package being edited, read off the main actor.
private nonisolated struct PackageDot: Sendable {
    var state: PersonaState
    var url: URL
    var fps: Double?

    struct Read: Sendable {
        var frames: [PersonaState: PersonaFrames]
        var face: UIImage?
    }

    static func read(_ dots: [PackageDot], face: URL?) -> Read {
        var byFile: [URL: PersonaFrames] = [:]
        var frames: [PersonaState: PersonaFrames] = [:]
        for dot in dots {
            guard let f = byFile[dot.url] ?? PersonaFrames(url: dot.url, fps: dot.fps) else { continue }
            byFile[dot.url] = f
            frames[dot.state] = f
        }
        let image = face.flatMap { url -> UIImage? in
            guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
                  let cg = CGImageSourceCreateImageAtIndex(src, 0, [kCGImageSourceShouldCacheImmediately: true] as CFDictionary) else { return nil }
            return UIImage(cgImage: cg)
        }
        return Read(frames: frames, face: image)
    }
}

// MARK: The picker

/// Settings › Appearance › Avatar's part in this: whether the hub makes avatars from images (`look_uploads`) and takes
/// pictures per state, animated pictures and theme colors and edits (`look_uploads_states`), the picked image turned
/// into a sheet, and deleting an avatar made that way.
@MainActor @Observable
final class AvatarUploadsModel {
    static let feature = "look_uploads"
    static let statesFeature = "look_uploads_states"

    struct Deps {
        var api: () -> APIClient?
        var features: () -> Set<String>
        var isDemo: () -> Bool
        var store: () -> PersonaStore
        var crop: (AvatarPicture, Bool) -> AvatarUploadModel = { AvatarUploadModel(picture: $0, states: $1) }
        var edit: (Persona) -> AvatarUploadModel = { AvatarUploadModel(editing: $0) }
        static var live: Deps {
            .init(api: { AppModel.shared.api }, features: { AppModel.shared.hubFeatures }, isDemo: { AppModel.shared.isDemo },
                  store: { AppModel.shared.personas })
        }
    }

    /// Ids being deleted.
    private(set) var deleting: Set<String> = []
    /// Why the last pick or delete failed.
    private(set) var error: String?
    /// Reading the picked image.
    private(set) var loading = false
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    /// New from Photo is offered: the hub makes avatars from images (older hubs don't; Demo Mode has only the default).
    var available: Bool { !deps.isDemo() && deps.features().contains(Self.feature) }
    /// The sheet offers a theme color and a picture per state, keeps animated pictures moving, and uploaded avatars
    /// have Edit: the hub lists `look_uploads_states` too (an older hub: the single picture, as before).
    var offersStates: Bool { available && deps.features().contains(Self.statesFeature) }

    func canDelete(_ p: Persona) -> Bool { available && p.isUploaded }
    func canEdit(_ p: Persona) -> Bool { offersStates && p.isUploaded }

    /// The crop sheet for the picked bytes; nil (with `error`) when they are not an image that can be one. With an older
    /// hub an animated picture is its first frame, as before.
    func picked(_ data: Data) async -> AvatarUploadModel? {
        loading = true
        defer { loading = false }
        let states = offersStates
        do {
            let picture = try await Self.load(data, animated: states)
            error = nil
            return deps.crop(picture, states)
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    /// The sheet for editing an uploaded avatar, filled from its package (nil when it can't be edited here).
    func edit(_ p: Persona) -> AvatarUploadModel? {
        guard canEdit(p) else { return nil }
        error = nil
        return deps.edit(p)
    }

    /// Opens the New from Photo crop sheet for an image dropped on the New from Photo card (#15612).
    func dropOnNew(_ data: Data) async -> AvatarUploadModel? {
        guard available, !loading else { return nil }
        return await picked(data)
    }

    /// Opens the Edit sheet for an uploaded avatar with a dropped image as its new main picture (#15612); nothing is
    /// saved until Save. Built-in avatars and older hubs without `look_uploads_states` do not react.
    func drop(_ data: Data, on p: Persona) async -> AvatarUploadModel? {
        guard canEdit(p), !deleting.contains(p.id), !loading else { return nil }
        loading = true
        defer { loading = false }
        do {
            let picture = try await Self.load(data, animated: true)
            error = nil
            let sheet = deps.edit(p)
            sheet.replaceMain(picture)
            return sheet
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    /// Decodes picked bytes off the main actor: an animated picture kept as it moves when the hub takes one.
    static func load(_ data: Data, animated: Bool) async throws -> AvatarPicture {
        try await Task.detached(priority: .userInitiated) {
            animated ? try AvatarPicture.load(data) : AvatarPicture(image: try AvatarImage.load(data))
        }.value
    }

    /// The picked item could not be read at all (Photos had no data for it, the file was unreadable).
    func failedToRead(_ error: (any Error)?) {
        self.error = error?.shown ?? String(localized: "Couldn't open this image. Try another one.")
    }

    /// Deletes an avatar made from an upload. When it was chosen here, the default is chosen (the hub does the same for
    /// the other devices). One already gone from the hub counts as deleted.
    func delete(_ p: Persona) async -> Bool {
        guard canDelete(p), !deleting.contains(p.id), let api = deps.api() else { return false }
        let store = deps.store()
        guard let asked = store.mark(for: api) else { return false }
        deleting.insert(p.id)
        defer { deleting.remove(p.id) }
        do {
            try await api.deleteUploadedLook(p.id)
        } catch let e as APIError where e.status == 404 {
            // Already gone: carry on as deleted.
        } catch {
            self.error = AvatarUploadModel.message(for: error)
            return false
        }
        error = nil
        guard store.isCurrent(asked) else { return true }
        if store.chosenID == p.id {
            let receipt = store.receipt(for: Persona.builtinID, since: asked)
            _ = await store.adopt(receipt, api: api)
        }
        // Read after the delete: a refresh under way may still list it.
        await store.reload(api, since: asked)
        return true
    }
}
