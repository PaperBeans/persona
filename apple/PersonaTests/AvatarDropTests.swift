import CoreGraphics
import Foundation
import ImageIO
import Testing
import UniformTypeIdentifiers
@testable import Persona
import PersonaWire

/// Drag-and-drop and Photos/Files choosing for every avatar picture slot (#15612, docs/apple/persona.md "Uploading an
/// image"): every slot offers Photos and Files, the drop importer keeps animated GIF bytes/frames, unsupported data
/// goes through the same failure path as picking, and drop routing covers the main picture (new and edit), all five
/// states, the picker's New from Photo card and editable uploaded avatar cards.
@MainActor
struct AvatarDropTests {
    private static let id = "u-mikan001"
    private static let states = [AvatarUploadsModel.feature, AvatarUploadsModel.statesFeature]

    // MARK: Fixtures

    private static let source = ##"{"kind":"upload","uploaded_at":"2026-11-25T08:00:00Z","color":{"seed":"#3A7BD5","auto":false},"states":{"working":{"own":true,"mark":true},"offline":{"own":true,"mark":false}}}"##

    private static func manifest(_ id: String = id, name: String = "Mikan", source: String = source) -> String {
        let dots = ["idle", "thinking", "working", "needs_you", "offline"].map { #""\#($0)":{"file":"dot/\#($0).png"}"# }.joined(separator: ",")
        return #"""
        {"format":1,"id":"\#(id)","name":"\#(name)","subtitle":"Orange","avatar":{"sizes":{"64":"avatar/64.png","128":"avatar/128.png","256":"avatar/256.png","512":"avatar/512.png"},"safe":1},
         "dot":{"size":144,"states":{\#(dots)}},"app_icon_fallback":"persona","source":\#(source)}
        """#
    }

    private static func persona(rev: String = "r1") throws -> Persona {
        Persona(info: PersonaInfo(id: id, rev: rev, bytes: 1,
                                  manifest: try JSONDecoder().decode(PersonaManifest.self, from: Data(manifest().utf8))))
    }

    private static func serve(rev: String, on hub: FakeHub, chosen: String = id) throws {
        let m = manifest()
        hub.on("GET", "/api/v1/looks", json: #"{"looks":[{"id":"\#(id)","rev":"\#(rev)","bytes":10,"manifest":\#(m),"uploaded":true}],"current":{"id":"\#(chosen)","effective":"\#(chosen)"}}"#)
        hub.on("PUT", "/api/v1/looks/current", json: #"{"id":"\#(chosen)","effective":"\#(chosen)"}"#)
        for f in try JSONDecoder().decode(PersonaManifest.self, from: Data(m.utf8)).files {
            hub.on("GET", "/api/v1/personas/\(id)/\(f)", json: "png:\(f)")
        }
    }

    private func store() -> PersonaStore {
        PersonaStore(root: FileManager.default.temporaryDirectory.appending(path: "avatar-drop-\(UUID().uuidString)"),
                     defaults: UserDefaults(suiteName: "avatar-drop-\(UUID().uuidString)")!)
    }

    private func uploads(_ hub: FakeHub, _ store: PersonaStore, features: [String] = states, demo: Bool = false) -> AvatarUploadsModel {
        let deps = AvatarUploadModel.Deps(api: { hub.client }, store: { store })
        return AvatarUploadsModel(deps: .init(api: { hub.client }, features: { Set(features) }, isDemo: { demo }, store: { store },
                                              crop: { AvatarUploadModel(picture: $0, states: $1, deps: deps) },
                                              edit: { AvatarUploadModel(editing: $0, deps: deps) }))
    }

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

    private static func gif(_ w: Int, _ h: Int, frames: Int, delay: Double = 0.2) -> Data {
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

    private static func frameCount(_ data: Data) -> Int {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil) else { return 0 }
        return CGImageSourceGetCount(src)
    }

    private static func loadTransferred(_ provider: NSItemProvider) async throws -> DroppedAvatarImage {
        try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadTransferable(type: DroppedAvatarImage.self) { result in
                continuation.resume(with: result)
            }
        }
    }

    // MARK: Photos and Files on every slot

    @Test func everyPictureSlotOffersPhotosAndFiles() throws {
        // Main (new + edit) and each of the five states in both new and edit sheets: 12 slots total.
        let slots = AvatarPictureSlot.all
        #expect(slots.count == 2 + PersonaState.allCases.count * 2)
        #expect(slots.contains(.mainNew) && slots.contains(.mainEdit))
        for state in PersonaState.allCases {
            #expect(slots.contains(.state(state, editing: false)))
            #expect(slots.contains(.state(state, editing: true)))
        }
        for slot in slots {
            #expect(slot.pickSources == [.photos, .files], "slot \(slot) must offer both Photos and Files")
        }
        #expect(AvatarPictureSource.photos.icon == Theme.Icon.photos)
        #expect(AvatarPictureSource.files.icon == Theme.Icon.file)
        #expect(!AvatarPictureSource.photos.title.isEmpty && !AvatarPictureSource.files.title.isEmpty)

        // Pin the view wiring so Photos, Files, original-encoding preservation and drop stay attached on every host.
        let settings = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .appending(path: "Persona/Views/Settings")
        let statesSrc = try String(contentsOf: settings.appending(path: "AvatarStatesPages.swift"), encoding: .utf8)
        let cropSrc = try String(contentsOf: settings.appending(path: "AvatarCropView.swift"), encoding: .utf8)
        let sourcesSrc = try String(contentsOf: settings.appending(path: "AvatarSources.swift"), encoding: .utf8)
        let pickerSrc = try String(contentsOf: settings.appending(path: "PersonaSettingsView.swift"), encoding: .utf8)
        #expect(statesSrc.contains("preferredItemEncoding: .current"))
        #expect(statesSrc.contains(".fileImporter(isPresented: $choosingFile, allowedContentTypes: [.image])"))
        #expect(sourcesSrc.contains("AvatarPicturePicking("))
        #expect(cropSrc.contains("AvatarPicturePicking(") && cropSrc.contains("AvatarPictureSourceButtons(slot: .mainEdit)") && cropSrc.contains("AvatarPictureDrop("))
        #expect(statesSrc.contains("AvatarPicturePicking(") && statesSrc.contains("AvatarPictureSourceButtons(slot: .state(") && statesSrc.contains("AvatarPictureDrop("))
        #expect(pickerSrc.contains("AvatarPictureSourceButtons(slot: .mainNew)") && pickerSrc.contains("AvatarPictureDrop("))
    }

    // MARK: Drop importer

    @Test func importerPreservesAnimatedGifBytesAndFrames() async throws {
        let original = Self.gif(200, 200, frames: 4)
        #expect(Self.frameCount(original) == 4)

        // 1. File-backed drop (Finder on Mac / Files on iPad).
        let dir = FileManager.default.temporaryDirectory.appending(path: "avatar-drop-src-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let file = dir.appending(path: "wave.gif")
        try original.write(to: file)

        let fileProvider = try #require(NSItemProvider(contentsOf: file))
        let fromFile = try await Self.loadTransferred(fileProvider)
        let fileBytes = try await fromFile.read()
        #expect(fileBytes == original)
        #expect(Self.frameCount(fileBytes) == 4)
        #expect(try AvatarPicture.load(fileBytes).animated)

        // 2. Multi-representation provider (original GIF first, flattened PNG fallback second): prefers the original GIF.
        let stillFallback = Self.png(Self.image(200, 200))
        let multiProvider = NSItemProvider()
        multiProvider.registerDataRepresentation(forTypeIdentifier: UTType.gif.identifier, visibility: .all) { completion in
            completion(original, nil)
            return nil
        }
        multiProvider.registerDataRepresentation(forTypeIdentifier: UTType.png.identifier, visibility: .all) { completion in
            completion(stillFallback, nil)
            return nil
        }
        let fromMulti = try await Self.loadTransferred(multiProvider)
        let multiBytes = try await fromMulti.read()
        #expect(multiBytes == original)
        #expect(Self.frameCount(multiBytes) == 4)

        // 3. Direct file import cleans up its temporary copy after read(), and discard() cleans up unused copies.
        let direct = try DroppedAvatarImage.importFile(file)
        guard case .file(let staged) = direct.payload else {
            Issue.record("expected staged temporary file")
            return
        }
        #expect(FileManager.default.fileExists(atPath: staged.path))
        let directBytes = try await direct.read()
        #expect(directBytes == original)
        #expect(!FileManager.default.fileExists(atPath: staged.path))

        let unused = try DroppedAvatarImage.importFile(file)
        guard case .file(let unusedStaged) = unused.payload else {
            Issue.record("expected staged temporary file for discard")
            return
        }
        #expect(FileManager.default.fileExists(atPath: unusedStaged.path))
        unused.discard()
        #expect(!FileManager.default.fileExists(atPath: unusedStaged.path))

        // 4. Direct data import preserves animated bytes verbatim.
        let fromData = DroppedAvatarImage.importData(original)
        let dataBytes = try await fromData.read()
        #expect(dataBytes == original)
        #expect(Self.frameCount(dataBytes) == 4)
    }

    @Test func unsupportedDataReportsFailureThroughSamePath() async throws {
        let hub = FakeHub()
        let s = store()
        let up = uploads(hub, s)
        let mikan = try Self.persona()
        let corrupt = Data("not an image".utf8)
        let tiny = Self.png(Self.image(32, 32))

        // Non-image provider is rejected by Transferable (.image only).
        let textProvider = NSItemProvider(object: "hello" as NSString)
        await #expect(throws: (any Error).self) {
            _ = try await Self.loadTransferred(textProvider)
        }

        // Oversized data and file payloads fail with AvatarImage.Problem without copying the file.
        let oversizedData = DroppedAvatarImage.importData(Data(repeating: 0xAA, count: 64), limit: 32)
        await #expect(throws: AvatarImage.Problem.self) {
            _ = try await oversizedData.read()
        }
        let dir = FileManager.default.temporaryDirectory.appending(path: "avatar-drop-big-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let bigFile = dir.appending(path: "huge.png")
        try Data(repeating: 0xBB, count: 64).write(to: bigFile)
        let oversizedFile = try DroppedAvatarImage.importFile(bigFile, limit: 32)
        if case .tooLarge = oversizedFile.payload {
            // expected
        } else {
            Issue.record("expected oversized file to be rejected before copying")
        }
        await #expect(throws: AvatarImage.Problem.self) {
            _ = try await oversizedFile.read()
        }

        // Picker cards report corrupt / too-small drops via uploads.error.
        #expect(await up.dropOnNew(corrupt) == nil)
        #expect(up.error?.isEmpty == false)
        #expect(await up.drop(tiny, on: mikan) == nil)
        #expect(up.error?.isEmpty == false)

        // New sheet and edit sheet report failures and leave existing pictures intact.
        let newSheet = AvatarUploadModel(picture: AvatarPicture(image: Self.image(200, 200)), states: true,
                                         deps: .init(api: { hub.client }, store: { s }))
        let originalMain = newSheet.main
        #expect(!(await newSheet.receiveMain(corrupt)))
        #expect(newSheet.error?.isEmpty == false && newSheet.main === originalMain)
        #expect(!(await newSheet.receiveState(.thinking, tiny)))
        #expect(newSheet.error?.isEmpty == false && newSheet.states[.thinking]?.source == .automatic)

        let editSheet = try #require(up.edit(mikan))
        #expect(!(await editSheet.receiveMain(corrupt)))
        #expect(editSheet.error?.isEmpty == false && editSheet.main == nil && !editSheet.changed)
        #expect(!(await editSheet.receiveState(.working, tiny)))
        #expect(editSheet.error?.isEmpty == false && editSheet.states[.working]?.source == .kept && !editSheet.changed)

        // Unreadable drop fallback uses the same failedToRead path as picking.
        editSheet.failedToRead(nil)
        #expect(editSheet.error?.isEmpty == false)
        up.failedToRead(nil)
        #expect(up.error?.isEmpty == false)
    }

    // MARK: Drop routing

    @Test func dropRoutingForMainInNewAndEditSheets() async throws {
        let hub = FakeHub()
        let s = store()
        let gif = Self.gif(300, 200, frames: 4)
        let blue = Self.png(Self.image(300, 300, rgb: (58.0 / 255, 123.0 / 255, 213.0 / 255)))

        // New sheet with states: replaces main picture, keeps GIF frames and updates automatic color swatch.
        let newSheet = AvatarUploadModel(picture: AvatarPicture(image: Self.image(200, 200)), states: true,
                                         deps: .init(api: { hub.client }, store: { s }))
        #expect(await newSheet.receiveMain(gif))
        #expect(newSheet.error == nil)
        #expect(newSheet.main?.picture.animated == true && newSheet.main?.picture.data == gif)
        #expect(Self.frameCount(try #require(newSheet.main?.picture.data)) == 4)

        #expect(await newSheet.receiveMain(blue))
        #expect(newSheet.main?.picture.animated == false)
        #expect(newSheet.automaticSeed.map(AvatarColor.hex) == "#3A7BD5")

        // New sheet on older hub (no look_uploads_states): keeps first frame as still, matching picking.
        let oldSheet = AvatarUploadModel(picture: AvatarPicture(image: Self.image(200, 200)), states: false,
                                         deps: .init(api: { hub.client }, store: { s }))
        #expect(await oldSheet.receiveMain(gif))
        #expect(oldSheet.main?.picture.animated == false)

        // Edit sheet: starts with package face (main == nil), drop sets main without saving; keepMain undoes it.
        let editSheet = try #require(uploads(hub, s).edit(try Self.persona()))
        #expect(editSheet.main == nil && !editSheet.changed && !editSheet.canUpload)
        #expect(await editSheet.receiveMain(gif))
        #expect(editSheet.main?.picture.animated == true && editSheet.main?.picture.data == gif)
        #expect(editSheet.changed && editSheet.canUpload)
        #expect(hub.sent("PATCH", "/api/v1/looks/uploads/\(Self.id)").isEmpty)

        // Dropping again replaces the pending main picture; Keep Current Picture reverts to unchanged.
        #expect(await editSheet.receiveMain(blue))
        #expect(editSheet.main?.picture.animated == false)
        editSheet.keepMain()
        #expect(editSheet.main == nil && !editSheet.changed)
    }

    @Test func dropRoutingForAllFiveStatesInNewAndEditSheets() async throws {
        let hub = FakeHub()
        let s = store()
        let animated = Self.gif(160, 160, frames: 5)
        let replacement = Self.png(Self.image(200, 200, rgb: (0.1, 0.8, 0.3)))

        for editing in [false, true] {
            for target in PersonaState.allCases {
                let model = editing
                    ? try #require(uploads(hub, s).edit(try Self.persona()))
                    : AvatarUploadModel(picture: AvatarPicture(image: Self.image(200, 200)), states: true,
                                        deps: .init(api: { hub.client }, store: { s }))
                let beforeSources = Dictionary(uniqueKeysWithValues: PersonaState.allCases.map { ($0, model.states[$0]?.source) })

                // Drop animated GIF onto this state row.
                #expect(await model.receiveState(target, animated), "editing=\(editing) state=\(target)")
                #expect(model.states[target]?.source == .picked)
                #expect(model.states[target]?.picked?.picture.animated == true)
                #expect(model.states[target]?.picked?.picture.data == animated)
                #expect(Self.frameCount(try #require(model.states[target]?.picked?.picture.data)) == 5)
                // No other state was modified.
                for other in PersonaState.allCases where other != target {
                    #expect(model.states[other]?.source == beforeSources[other])
                }

                // Drop replacement onto this state's crop page: replaces only this state's picture in place.
                #expect(await model.receiveState(target, replacement))
                #expect(model.states[target]?.source == .picked)
                #expect(model.states[target]?.picked?.picture.animated == false)
                #expect(model.states[target]?.picked?.picture.data == replacement)
            }
        }

        // Older hub (no states): receiveState refuses cleanly.
        let old = AvatarUploadModel(picture: AvatarPicture(image: Self.image(200, 200)), states: false,
                                    deps: .init(api: { hub.client }, store: { s }))
        #expect(!(await old.receiveState(.working, animated)))
        #expect(old.states[.working]?.source == .automatic)
    }

    @Test func dropRoutingForPickerNewCardAndUploadedCard() async throws {
        let hub = FakeHub()
        let s = store()
        try Self.serve(rev: "r1", on: hub)
        await s.refresh(hub.client)
        let listed = try #require(s.offered.first { $0.id == Self.id })
        let gif = Self.gif(300, 200, frames: 4)

        // New from Photo card on a hub with look_uploads_states: opens new crop sheet with animated image.
        let up = uploads(hub, s)
        let newSheet = try #require(await up.dropOnNew(gif))
        #expect(newSheet.editing == nil && newSheet.offersStates)
        #expect(newSheet.main?.picture.animated == true && newSheet.main?.picture.data == gif)

        // New from Photo card on an older hub (look_uploads only): opens single-still crop sheet.
        let oldUp = uploads(hub, s, features: [AvatarUploadsModel.feature])
        let oldSheet = try #require(await oldUp.dropOnNew(gif))
        #expect(oldSheet.editing == nil && !oldSheet.offersStates && oldSheet.main?.picture.animated == false)

        // Demo mode or hub without look_uploads: New from Photo ignores drops.
        #expect(await uploads(hub, s, features: []).dropOnNew(gif) == nil)
        #expect(await uploads(hub, s, demo: true).dropOnNew(gif) == nil)

        // Uploaded avatar card with Edit: opens Edit sheet with dropped image as new main; nothing saved until Save.
        let editSheet = try #require(await up.drop(gif, on: listed))
        #expect(editSheet.editing?.id == Self.id)
        #expect(editSheet.main?.picture.animated == true && editSheet.main?.picture.data == gif)
        #expect(editSheet.changed && editSheet.canUpload)
        #expect(hub.sent("PATCH", "/api/v1/looks/uploads/\(Self.id)").isEmpty)
        #expect(hub.sent("POST", "/api/v1/looks/uploads").isEmpty)
        #expect(hub.sent("PUT", "/api/v1/looks/current").isEmpty)

        // Saving sends PATCH with the dropped animated image and its square.
        hub.on("PATCH", "/api/v1/looks/uploads/\(Self.id)") { _ in
            try Self.serve(rev: "r2", on: hub)
            return Data(#"{"look":{"id":"\#(Self.id)","rev":"r2","uploaded":true}}"#.utf8)
        }
        #expect(await editSheet.upload())
        let sent = try #require(hub.sent("PATCH", "/api/v1/looks/uploads/\(Self.id)").last?.body)
        let body = try #require(JSONSerialization.jsonObject(with: sent) as? [String: Any])
        #expect(Set(body.keys) == ["image", "crop"])
        #expect((body["image"] as? String).flatMap { Data(base64Encoded: $0) } == gif)

        // Built-in avatars and older hubs without Edit do not react and set no error.
        let placed = Persona(info: PersonaInfo(id: "nwverse", rev: "r1", bytes: 1, manifest: try PersonaTests.decode()))
        #expect(await up.drop(gif, on: .builtin) == nil && up.error == nil)
        #expect(await up.drop(gif, on: placed) == nil && up.error == nil)
        #expect(await oldUp.drop(gif, on: listed) == nil && oldUp.error == nil)
    }
}
