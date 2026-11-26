import CoreGraphics
import Foundation
import ImageIO
import Testing
import UniformTypeIdentifiers
@testable import Persona
import PersonaWire

/// Avatars from images with a picture per state, animated pictures and a theme color, editable later (#15449,
/// docs/apple/persona.md "Uploading an image"): offered only by hubs that list `look_uploads_states` (an older hub keeps
/// the single picture, its request as before), animated pictures sent as picked with their square, still ones cropped
/// here, the automatic color estimated as `spec/fixtures/look-colors` pins it, and an edit sending only what changed.
@MainActor
struct AvatarStatesTests {
    private static let id = "u-mikan001"
    private static let states = [AvatarUploadsModel.feature, AvatarUploadsModel.statesFeature]

    // MARK: Fixtures

    /// An uploaded avatar's persona.json; `source` is the part that says what its owner chose.
    private static func manifest(_ id: String = id, name: String = "Mikan", subtitle: String? = "Orange", source: String) -> String {
        let dots = ["idle", "thinking", "working", "needs_you", "offline"].map { #""\#($0)":{"file":"dot/\#($0).png"}"# }.joined(separator: ",")
        let sub = subtitle.map { #""subtitle":"\#($0)","# } ?? ""
        return #"""
        {"format":1,"id":"\#(id)","name":"\#(name)",\#(sub)"avatar":{"sizes":{"64":"avatar/64.png","128":"avatar/128.png","256":"avatar/256.png","512":"avatar/512.png"},"safe":1},
         "dot":{"size":144,"states":{\#(dots)}},"app_icon_fallback":"persona","source":\#(source)}
        """#
    }

    private static let chosenSource = ##"{"kind":"upload","uploaded_at":"2026-11-25T08:00:00Z","color":{"seed":"#3A7BD5","auto":false},"states":{"working":{"own":true,"mark":true},"offline":{"own":true,"mark":false}}}"##
    private static let legacySource = #"{"kind":"upload","uploaded_at":"2026-11-09T08:00:00Z"}"#

    private static func persona(_ source: String = chosenSource, rev: String = "r1") throws -> Persona {
        Persona(info: PersonaInfo(id: id, rev: rev, bytes: 1,
                                  manifest: try JSONDecoder().decode(PersonaManifest.self, from: Data(manifest(source: source).utf8))))
    }

    /// A hub that keeps the choice: GET /api/v1/looks lists the avatar at `rev`, chosen, and serves its files.
    private static func serve(_ source: String = chosenSource, rev: String, on hub: FakeHub, chosen: String = id) throws {
        let m = manifest(source: source)
        hub.on("GET", "/api/v1/looks", json: #"{"looks":[{"id":"\#(id)","rev":"\#(rev)","bytes":10,"manifest":\#(m),"uploaded":true}],"current":{"id":"\#(chosen)","effective":"\#(chosen)"}}"#)
        hub.on("PUT", "/api/v1/looks/current", json: #"{"id":"\#(chosen)","effective":"\#(chosen)"}"#)
        for f in try JSONDecoder().decode(PersonaManifest.self, from: Data(m.utf8)).files {
            hub.on("GET", "/api/v1/personas/\(id)/\(f)", json: "png:\(f)")
        }
    }

    private func store() -> PersonaStore {
        PersonaStore(root: FileManager.default.temporaryDirectory.appending(path: "avatar-states-\(UUID().uuidString)"),
                     defaults: UserDefaults(suiteName: "avatar-states-\(UUID().uuidString)")!)
    }

    private func uploads(_ hub: FakeHub, _ store: PersonaStore, features: [String] = states) -> AvatarUploadsModel {
        let deps = AvatarUploadModel.Deps(api: { hub.client }, store: { store })
        return AvatarUploadsModel(deps: .init(api: { hub.client }, features: { Set(features) }, isDemo: { false }, store: { store },
                                              crop: { AvatarUploadModel(picture: $0, states: $1, deps: deps) },
                                              edit: { AvatarUploadModel(editing: $0, deps: deps) }))
    }

    /// A w×h picture in one color, opaque.
    private static func image(_ w: Int, _ h: Int, rgb: (Double, Double, Double) = (0.9, 0.5, 0.1)) -> CGImage {
        let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                            bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
        ctx.setFillColor(CGColor(srgbRed: rgb.0, green: rgb.1, blue: rgb.2, alpha: 1))
        ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
        return ctx.makeImage()!
    }

    private static func png(_ image: CGImage) -> Data {
        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dest, image, nil)
        #expect(CGImageDestinationFinalize(dest))
        return out as Data
    }

    /// An animated GIF of `frames` w×h frames, each `delay` seconds, each a different shade.
    private static func gif(_ w: Int, _ h: Int, frames: Int, delay: Double) -> Data {
        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, UTType.gif.identifier as CFString, frames, nil)!
        CGImageDestinationSetProperties(dest, [kCGImagePropertyGIFDictionary: [kCGImagePropertyGIFLoopCount: 0]] as CFDictionary)
        for i in 0..<frames {
            let shade = Double(i) / Double(max(1, frames))
            CGImageDestinationAddImage(dest, image(w, h, rgb: (0.2 + 0.6 * shade, 0.4, 0.8)),
                                       [kCGImagePropertyGIFDictionary: [kCGImagePropertyGIFDelayTime: delay]] as CFDictionary)
        }
        #expect(CGImageDestinationFinalize(dest))
        return out as Data
    }

    private static func body(_ hub: FakeHub, _ method: String, _ path: String) throws -> [String: Any] {
        let sent = try #require(hub.sent(method, path).last?.body)
        return try #require(JSONSerialization.jsonObject(with: sent) as? [String: Any])
    }

    // MARK: Offered

    @Test func onlyAHubWithStatesOffersThemAndEdit() throws {
        let hub = FakeHub()
        let s = store()
        let mikan = try Self.persona()
        #expect(uploads(hub, s).offersStates && uploads(hub, s).canEdit(mikan))
        #expect(uploads(hub, s).edit(mikan)?.offersStates == true)
        // An older hub (look_uploads only): New from Photo as before, no states, no color, no Edit.
        let old = uploads(hub, s, features: [AvatarUploadsModel.feature])
        #expect(old.available && !old.offersStates && !old.canEdit(mikan) && old.edit(mikan) == nil)
        #expect(old.canDelete(mikan))
        // Not uploaded: never editable.
        let placed = Persona(info: PersonaInfo(id: "nwverse", rev: "r1", bytes: 1, manifest: try PersonaTests.decode()))
        #expect(!uploads(hub, s).canEdit(placed) && !uploads(hub, s).canEdit(.builtin))
        // Without look_uploads at all, look_uploads_states alone offers nothing.
        #expect(!uploads(hub, s, features: [AvatarUploadsModel.statesFeature]).offersStates)
    }

    /// The old hub's flow is today's: an animated picture is its first frame cropped here, and the request has only
    /// name, subtitle, image and choose.
    @Test func anOlderHubGetsTheSingleStillPictureAsBefore() async throws {
        let hub = FakeHub()
        let s = store()
        hub.on("GET", "/api/v1/personas", json: #"{"personas":[]}"#)
        let model = try #require(await uploads(hub, s, features: [AvatarUploadsModel.feature]).picked(Self.gif(300, 200, frames: 4, delay: 0.2)))
        #expect(!model.offersStates && model.main?.picture.animated == false)
        model.name = "Mikan"
        model.color = .chosen(0x3A_7B_D5) // not offered here: never sent
        hub.on("POST", "/api/v1/looks/uploads") { _ in throw APIError(status: 409, message: "no", fields: ["code": "too_many"]) }
        _ = await model.upload()
        let body = try Self.body(hub, "POST", "/api/v1/looks/uploads")
        #expect(Set(body.keys) == ["name", "image", "choose"])
        let image = try #require((body["image"] as? String).flatMap { Data(base64Encoded: $0) })
        let src = try #require(CGImageSourceCreateWithData(image as CFData, nil))
        #expect(CGImageSourceGetCount(src) == 1 && (CGImageSourceGetType(src) as String?) == UTType.png.identifier)
        let props = try #require(CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any])
        #expect(props[kCGImagePropertyPixelWidth] as? Int == 200 && props[kCGImagePropertyPixelHeight] as? Int == 200)
    }

    // MARK: Pictures

    @Test func animatedPicturesKeepTheirBytesAndSendTheirSquare() async throws {
        let gif = Self.gif(300, 200, frames: 6, delay: 0.25)
        let picture = try AvatarPicture.load(gif)
        #expect(picture.animated && picture.pixelWidth == 300 && picture.pixelHeight == 200)
        var crop = AvatarCrop(width: picture.image.width, height: picture.image.height)
        crop = crop.zoomed(to: 2).panned(by: CGSize(width: 1000, height: 0), viewSide: 100) // the left edge
        let payload = try #require(picture.payload(rect: crop.rect, side: AvatarImage.outputSide))
        #expect(Data(base64Encoded: payload.image) == gif) // as picked: the hub makes the frames
        #expect(payload.crop == LookCrop(x: 0, y: 50, side: 100))
        // A still one is the crop already: no square sent with it.
        let still = try AvatarPicture.load(Self.png(Self.image(300, 200)))
        #expect(!still.animated)
        #expect(still.payload(rect: AvatarCrop(width: 300, height: 200).rect, side: AvatarImage.stateSide)?.crop == nil)
        // A state's still picture is at most 512 px.
        let big = AvatarPicture(image: Self.image(2000, 1500))
        let sent = try #require(big.payload(rect: AvatarCrop(width: 2000, height: 1500).rect, side: AvatarImage.stateSide))
        let src = try #require(Data(base64Encoded: sent.image).flatMap { CGImageSourceCreateWithData($0 as CFData, nil) })
        let props = try #require(CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any])
        #expect(props[kCGImagePropertyPixelWidth] as? Int == 512)
    }

    @Test func aPreviewPlaysAsTheHubWillKeepIt() throws {
        // 6 frames of 250 ms: all six, over 1.5 s.
        let picture = try AvatarPicture.load(Self.gif(200, 200, frames: 6, delay: 0.25))
        let frames = try #require(picture.frames(rect: CGRect(x: 0, y: 0, width: 200, height: 200), side: AvatarUploadModel.previewSide))
        #expect(frames.images.count == 6 && abs(frames.duration - 1.5) < 0.01)
        #expect(frames.images.first?.size.width == CGFloat(AvatarUploadModel.previewSide))
        // At most 10 a second and 36 in all, evenly over the loop.
        let fast = AvatarMotion.samples(Array(repeating: 0.0625, count: 32)) // 16 a second, 2 s
        #expect(fast.frames.count == 20 && fast.duration == 2)
        #expect(fast.frames.first == 0 && fast.frames.last == 30)
        let long = AvatarMotion.samples(Array(repeating: 0.1, count: 80))
        #expect(long.frames.count == AvatarMotion.maxFrames)
        #expect(AvatarMotion.samples([0.5]).frames == [0])
        // A still picture: one frame.
        let still = AvatarPicture(image: Self.image(100, 100))
        #expect(still.frames(rect: CGRect(x: 0, y: 0, width: 100, height: 100), side: 64)?.images.count == 1)
    }

    /// The automatic color as `spec/fixtures/look-colors` pins it for the hub, the web and the apps.
    @Test func theAutomaticColorMatchesTheSharedFixtures() throws {
        let dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().appending(path: "spec/fixtures/look-colors")
        struct Cases: Decodable {
            struct Case: Decodable {
                var name: String
                var file: String
                var expected_seed: String
            }
            var cases: [Case]
        }
        let cases = try JSONDecoder().decode(Cases.self, from: Data(contentsOf: dir.appending(path: "cases.json"))).cases
        #expect(cases.count >= 5)
        for c in cases {
            let data = try Data(contentsOf: dir.appending(path: c.file))
            let src = try #require(CGImageSourceCreateWithData(data as CFData, nil))
            let image = try #require(CGImageSourceCreateImageAtIndex(src, 0, nil))
            #expect(AvatarColor.hex(AvatarColor.seed(image)) == c.expected_seed, "\(c.name)")
        }
        #expect(AvatarColor.hex(AvatarColor.gray) == "#787878")
    }

    // MARK: A new avatar

    @Test func aNewAvatarSendsItsStatesColorAndAnimatedSquare() async throws {
        let hub = FakeHub()
        let s = store()
        let up = uploads(hub, s)
        let model = try #require(await up.picked(Self.gif(300, 200, frames: 4, delay: 0.2)))
        #expect(model.offersStates && model.main?.picture.animated == true)
        // Left alone: automatic everything, as today.
        #expect(model.ownStates == 0 && model.color == .automatic && model.colorName == String(localized: "Automatic"))
        model.name = "Mikan"
        let thinking = try #require(await model.load(Self.png(Self.image(800, 600))))
        model.give(.thinking, thinking)
        model.give(.idle, try #require(await model.load(Self.gif(120, 120, frames: 3, delay: 0.3))))
        model.give(.offline, thinking)
        model.states[.offline]?.mark = false
        model.give(.working, thinking)
        model.useAutomatic(.working) // changed its mind
        model.color = .chosen(0x3A_7B_D5)
        #expect(model.ownStates == 3 && model.colorName == String(localized: "Custom"))
        hub.on("POST", "/api/v1/looks/uploads") { _ in throw APIError(status: 400, message: "太长了", fields: ["code": "animation_too_long"]) }
        #expect(!(await model.upload()))
        #expect(model.error == "太长了" && model.canUpload) // the hub's words; try again

        let body = try Self.body(hub, "POST", "/api/v1/looks/uploads")
        #expect(body["color"] as? String == "#3A7BD5")
        #expect((body["crop"] as? [String: Int]) == ["x": 50, "y": 0, "side": 200])
        #expect(body["choose"] as? Bool == true)
        let states = try #require(body["states"] as? [String: [String: Any]])
        #expect(Set(states.keys) == ["thinking", "idle", "offline"])
        #expect(states["thinking"]?["mark"] as? Bool == true && states["thinking"]?["crop"] == nil)
        #expect(states["offline"]?["mark"] as? Bool == false)
        #expect(states["idle"]?["mark"] == nil) // idle has no mark
        #expect((states["idle"]?["crop"] as? [String: Int])?["side"] == 120) // animated: as picked, with its square
        let still = try #require((states["thinking"]?["image"] as? String).flatMap { Data(base64Encoded: $0) })
        let src = try #require(CGImageSourceCreateWithData(still as CFData, nil))
        let props = try #require(CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any])
        #expect(props[kCGImagePropertyPixelWidth] as? Int == 512) // a state is at most 512 px

        // The automatic color is the hub's default: not sent.
        model.color = .automatic
        _ = await model.upload()
        #expect(try Self.body(hub, "POST", "/api/v1/looks/uploads")["color"] == nil)
    }

    @Test func theAutomaticSwatchFollowsTheSquare() async throws {
        let model = AvatarUploadModel(picture: AvatarPicture(image: Self.image(400, 400, rgb: (58.0 / 255, 123.0 / 255, 213.0 / 255))), states: true,
                                      deps: .init(api: { nil }, store: { store() }))
        #expect(model.automaticSeed == nil)
        await model.estimateColor()
        #expect(model.automaticSeed.map(AvatarColor.hex) == "#3A7BD5" && model.shownColor == model.automaticSeed)
        model.color = .chosen(0xFF_0000)
        #expect(model.shownColor == 0xFF_0000)
    }

    // MARK: Editing

    @Test func anEditIsFilledFromThePackage() throws {
        let model = AvatarUploadModel(editing: try Self.persona(), deps: .init(api: { nil }, store: { store() }))
        #expect(model.name == "Mikan" && model.subtitle == "Orange" && model.main == nil && model.offersStates)
        #expect(model.color == .chosen(0x3A_7B_D5))
        #expect(model.states[.working]?.source == .kept && model.states[.working]?.mark == true)
        #expect(model.states[.offline]?.source == .kept && model.states[.offline]?.mark == false)
        #expect(model.states[.thinking]?.source == .automatic && model.ownStates == 2)
        #expect(!model.changed && !model.canUpload) // nothing to save yet

        // One made before the hub wrote colors: all automatic, no color yet.
        let legacy = AvatarUploadModel(editing: try Self.persona(Self.legacySource), deps: .init(api: { nil }, store: { store() }))
        #expect(legacy.color == nil && legacy.ownStates == 0 && legacy.colorName == String(localized: "Not set"))
        // An automatic one: its seed is the package's.
        let auto = AvatarUploadModel(editing: try Self.persona(##"{"kind":"upload","color":{"seed":"#E68C28","auto":true}}"##),
                                     deps: .init(api: { nil }, store: { store() }))
        #expect(auto.color == .automatic && auto.automaticSeed.map(AvatarColor.hex) == "#E68C28")
    }

    /// Save sends only what changed, then reads the list again: the new rev comes down and stays chosen.
    @Test func savingSendsOnlyTheChangesAndKeepsTheChoice() async throws {
        let hub = FakeHub()
        let s = store()
        try Self.serve(rev: "r1", on: hub)
        await s.refresh(hub.client)
        #expect(s.current.id == Self.id && s.isOnPhone(s.current))
        let listed = try #require(s.offered.first { $0.id == Self.id })
        let model = try #require(uploads(hub, s).edit(listed))

        model.name = "Mikan 2"
        model.states[.offline]?.mark = true // only the mark
        model.useAutomatic(.working) // back to automatic
        model.color = .automatic
        #expect(model.changed && model.canUpload)
        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            try Self.serve(rev: "r2", on: hub) // the hub has swapped it by now
            return Data(#"{"look":{"id":"\#(Self.id)","rev":"r2","uploaded":true}}"#.utf8)
        }
        #expect(await model.upload())
        #expect(hub.sent("POST", "/api/v1/looks/uploads").isEmpty && hub.sent("PATCH", "/api/v1/looks/uploads/\(Self.id)").count == 1)
        let body = try Self.body(hub, "PATCH", "/api/v1/looks/uploads/\(Self.id)")
        #expect(Set(body.keys) == ["name", "states", "color"]) // no subtitle, no image: unchanged
        #expect(body["name"] as? String == "Mikan 2" && body["color"] as? String == "auto")
        let states = try #require(body["states"] as? [String: [String: Bool]])
        #expect(states == ["offline": ["mark": true], "working": ["auto": true]])
        // The new package, still the one chosen, no PUT.
        #expect(s.current.id == Self.id && s.current.rev == "r2" && s.isOnPhone(s.current))
        #expect(hub.sent("PUT", "/api/v1/looks/current").isEmpty)
        #expect(model.done && !model.canUpload)
    }

    @Test func aClearedDescriptionIsSentAlone() async throws {
        let hub = FakeHub()
        let s = store()
        try Self.serve(Self.legacySource, rev: "r1", on: hub)
        await s.refresh(hub.client)
        let listed = try #require(s.offered.first { $0.id == Self.id })
        let model = try #require(uploads(hub, s).edit(listed))
        model.subtitle = ""
        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            try Self.serve(Self.legacySource, rev: "r2", on: hub)
            return Data(#"{"look":{"id":"\#(Self.id)","rev":"r2"}}"#.utf8)
        }
        #expect(await model.upload())
        let body = try Self.body(hub, "PATCH", "/api/v1/looks/uploads/\(Self.id)")
        #expect(body as? [String: String] == ["subtitle": ""]) // cleared; no color for one that had none
    }

    @Test func aNewMainPictureAndAStatePictureGoWithTheirSquares() async throws {
        let hub = FakeHub()
        let s = store()
        try Self.serve(rev: "r1", on: hub)
        await s.refresh(hub.client)
        let listed = try #require(s.offered.first { $0.id == Self.id })
        let model = try #require(uploads(hub, s).edit(listed))
        model.replaceMain(try #require(await model.load(Self.gif(300, 200, frames: 3, delay: 0.2))))
        model.give(.thinking, try #require(await model.load(Self.png(Self.image(256, 256)))))
        model.states[.thinking]?.mark = false
        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            try Self.serve(rev: "r2", on: hub)
            return Data(#"{"look":{"id":"\#(Self.id)","rev":"r2"}}"#.utf8)
        }
        #expect(await model.upload())
        let body = try Self.body(hub, "PATCH", "/api/v1/looks/uploads/\(Self.id)")
        #expect(Set(body.keys) == ["image", "crop", "states"])
        #expect((body["crop"] as? [String: Int]) == ["x": 50, "y": 0, "side": 200])
        let thinking = try #require((body["states"] as? [String: [String: Any]])?["thinking"])
        #expect(thinking["image"] is String && thinking["mark"] as? Bool == false && thinking["crop"] == nil)
    }

    /// A refused edit changes nothing; saved but not downloaded: Try Again downloads without a second PATCH.
    @Test func aRefusedOrUndownloadedEditIsTriedAgainSafely() async throws {
        let hub = FakeHub()
        let s = store()
        try Self.serve(rev: "r1", on: hub)
        await s.refresh(hub.client)
        let listed = try #require(s.offered.first { $0.id == Self.id })
        let model = try #require(uploads(hub, s).edit(listed))
        model.name = "Mikan 2"
        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            throw APIError(status: 400, message: "颜色不对", fields: ["code": "bad_color"])
        }
        #expect(!(await model.upload()))
        #expect(model.error == "颜色不对" && model.made == nil && model.canUpload)
        #expect(s.current.rev == "r1")

        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            try Self.serve(rev: "r2", on: hub)
            hub.on("GET", "/api/v1/personas/\(Self.id)/avatar/512.png") { _ in throw URLError(.networkConnectionLost) }
            return Data(#"{"look":{"id":"\#(Self.id)","rev":"r2"}}"#.utf8)
        }
        #expect(!(await model.upload())) // saved, but a file did not come
        #expect(model.made?.rev == "r2" && model.canUpload)
        #expect(model.error == String(localized: "The avatar was saved, but this device couldn't download it. Try again."))
        try Self.serve(rev: "r2", on: hub)
        #expect(await model.upload())
        #expect(hub.sent("PATCH", "/api/v1/looks/uploads/\(Self.id)").count == 2) // the refused one and the saved one
        #expect(s.current.rev == "r2" && s.isOnPhone(s.current))
    }

    @Test func sourceDecodesLenientlyAndRoundTrips() throws {
        let m = try JSONDecoder().decode(PersonaManifest.self, from: Data(Self.manifest(source: Self.chosenSource).utf8))
        #expect(m.source?.color == .init(seed: "#3A7BD5", auto: false))
        #expect(m.source?.states?["offline"] == .init(own: true, mark: false))
        let again = try JSONDecoder().decode(PersonaManifest.self, from: JSONEncoder().encode(m))
        #expect(again == m && again.isUploaded)
        // A color or states of another shape leave them out, not the package.
        let odd = try JSONDecoder().decode(PersonaManifest.self, from: Data(Self.manifest(source: #"{"kind":"upload","color":"auto","states":[1]}"#).utf8))
        #expect(odd.isUploaded && odd.source?.color == nil && odd.source?.states == nil)
    }
}
