#if os(iOS)
import AVFoundation
import BeautifulUI
import Photos
import SwiftUI
import UIKit

// The composer's 「+」 on the iPhone (docs/apple/ios.md, New shell, "Attach panel"): a system sheet that opens at half
// height and drags to full (detents medium and large, the system's grabber), like the system's photo picker. Recent photos
// in three equal square columns (the camera one cell like the others) under a header, and a bar at the sheet's foot
// (Photos, Files, More) that the keyboard pushes up. Photos picked turn the bar into a caption (the draft) and Send.

/// What was picked in the panel: the photos and videos (by `PHAsset.localIdentifier`) in the order picked, and those of
/// them edited there, which go as edited.
struct AttachPicks {
    var ids: [String] = []
    var edited: [String: EditedImage] = [:]
}

/// What the composer gets back once the panel has gone (`AttachSheet`).
enum AttachOutcome {
    /// Closed: what was picked goes into the composer, nothing is sent.
    case closed(AttachPicks)
    /// Send: what was picked goes into the composer and the message is sent.
    case send(AttachPicks)
    /// A row asked for something the panel cannot show itself (the camera, Files, a voice call): what was picked goes
    /// into the composer, and the action runs once the panel is gone.
    case then(AttachPicks, () -> Void)
}

/// The panel's tabs, in the bar's order.
enum AttachTab: String, CaseIterable, Identifiable {
    case photos, files, more
    var id: String { rawValue }

    var title: String {
        switch self {
        case .photos: String(localized: "Photos")
        case .files: String(localized: "Files")
        case .more: String(localized: "More")
        }
    }

    var symbol: String {
        switch self {
        case .photos: Theme.Icon.photos
        case .files: Theme.Icon.file
        case .more: Theme.Icon.more
        }
    }
}

/// The panel's state the composer shares: on screen, what is picked, and what to hand back once it has gone.
@Observable
final class AttachPanel {
    /// The sheet is on screen.
    var shown = false
    /// The photos and videos picked, in order, and the photos edited here (picked or not, by id: they go as edited once
    /// picked, and open again as they were left). Kept here, not in the sheet, so a swipe down hands them back too.
    var picked: [String] = []
    var edited: [String: EditedImage] = [:]
    /// What the composer gets once the sheet is down; nil: closed by the system (a swipe down, a tap outside).
    @ObservationIgnored fileprivate var outcome: AttachOutcome?

    /// What was picked, with the edits of those of them edited here.
    var picks: AttachPicks { AttachPicks(ids: picked, edited: edited.filter { picked.contains($0.key) }) }

    /// A row of the composer's (the More tab) closes the sheet, then acts.
    func close(then action: @escaping () -> Void) { leave(.then(picks, action)) }

    fileprivate func leave(_ outcome: AttachOutcome) {
        guard shown else { return }
        self.outcome = outcome
        shown = false
    }

    /// The panel's photo library and its image manager, kept from one opening to the next. Made on first use: the
    /// chat's `@State` makes an `AttachPanel` on every init of the view, and only the first is kept.
    @ObservationIgnored private(set) lazy var library = AttachLibrary()
}

/// The photo library as the grid needs it: access, the newest photos and videos, thumbnails and full pictures.
@Observable
final class AttachLibrary {
    enum Access { case unknown, full, limited, denied }
    private(set) var access: Access = .unknown
    private(set) var assets: [PHAsset] = []
    @ObservationIgnored private let manager = PHCachingImageManager()
    private static var warmed = false

    /// Opens the Photos library once, off the main thread, so the first 「+」 doesn't wait for it: the first query in a
    /// process loads the Photos database, which took 50 ms to over a second on the main thread (#4250). Only when access
    /// was already given: this never asks.
    static func prewarm() {
        guard !warmed else { return }
        warmed = true
        Task.detached(priority: .utility) {
            let status = PHPhotoLibrary.authorizationStatus(for: .readWrite)
            guard status == .authorized || status == .limited else { return }
            _ = newest(limit: 1)
        }
    }

    func load() async {
        var status = PHPhotoLibrary.authorizationStatus(for: .readWrite)
        if status == .notDetermined { status = await PHPhotoLibrary.requestAuthorization(for: .readWrite) }
        switch status {
        case .authorized: access = .full
        case .limited: access = .limited
        default: access = .denied; assets = []; return
        }
        await fetch()
    }

    /// Lets go of the photos when the panel goes: the next opening draws its light loading state first and the grid a
    /// moment later, rather than the whole grid in the frame the panel presents in (#4250).
    func forget() { access = .unknown; assets = [] }

    /// The newest photos and videos, fetched off the main thread (#4250).
    func fetch() async {
        let limit = Theme.Attach.fetchLimit
        let fetched = await Task.detached(priority: .userInitiated) { Self.newest(limit: limit) }.value
        // The panel went while it was fetching (`forget()` has run): keep nothing for the next opening.
        guard !Task.isCancelled else { return }
        assets = fetched.assets
    }

    private struct Fetched: @unchecked Sendable { let assets: [PHAsset] }

    private nonisolated static func newest(limit: Int) -> Fetched {
        let options = PHFetchOptions()
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        options.predicate = NSPredicate(format: "mediaType == %d || mediaType == %d", PHAssetMediaType.image.rawValue, PHAssetMediaType.video.rawValue)
        options.fetchLimit = limit
        let result = PHAsset.fetchAssets(with: options)
        var list: [PHAsset] = []
        list.reserveCapacity(result.count)
        result.enumerateObjects { asset, _, _ in list.append(asset) }
        return Fetched(assets: list)
    }

    func thumbnail(_ asset: PHAsset, side: CGFloat) async -> UIImage? {
        let options = PHImageRequestOptions()
        options.deliveryMode = .highQualityFormat
        options.resizeMode = .fast
        options.isNetworkAccessAllowed = true
        let size = CGSize(width: side * Theme.Attach.thumbnailScale, height: side * Theme.Attach.thumbnailScale)
        return await withCheckedContinuation { c in
            manager.requestImage(for: asset, targetSize: size, contentMode: .aspectFill, options: options) { @Sendable image, _ in
                c.resume(returning: image)
            }
        }
    }

    /// The whole picture, downloaded from iCloud if it has to be.
    static func picture(_ asset: PHAsset) async -> UIImage? {
        let options = PHImageRequestOptions()
        options.isNetworkAccessAllowed = true
        options.deliveryMode = .highQualityFormat
        return await withCheckedContinuation { c in
            PHImageManager.default().requestImageDataAndOrientation(for: asset, options: options) { @Sendable data, _, _, _ in
                c.resume(returning: data.flatMap(UIImage.init(data:)))
            }
        }
    }

    /// The picture to edit (`ImageEditor`): downloaded from iCloud if it has to be, decoded off the main thread at the
    /// size it is sent (`MessagePhotos.maxSide`) and upright.
    static func editable(_ asset: PHAsset) async -> UIImage? {
        let options = PHImageRequestOptions()
        options.isNetworkAccessAllowed = true
        options.deliveryMode = .highQualityFormat
        let data: Data? = await withCheckedContinuation { c in
            PHImageManager.default().requestImageDataAndOrientation(for: asset, options: options) { @Sendable data, _, _, _ in
                c.resume(returning: data)
            }
        }
        guard let data else { return nil }
        let side = MessagePhotos.maxSide
        return await Task.detached(priority: .userInitiated) { LarkMedia.decode(data, maxPixel: side) }.value
    }

    /// A copy of the video in a directory of its own under the app's temporary uploads, named as in Photos, for the
    /// upload (`VideoAttachments.start(staged:)`, which deletes it). A slow-motion or edited video is a composition with
    /// no file: its original goes instead.
    static func movie(_ asset: PHAsset) async -> URL? {
        var found = await movieFile(asset, version: .current)
        if found == nil { found = await movieFile(asset, version: .original) }
        guard let source = found else { return nil }
        let name = PHAssetResource.assetResources(for: asset).first?.originalFilename ?? source.lastPathComponent
        return await Task.detached(priority: .userInitiated) { () -> URL? in
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("uploads").appendingPathComponent(UUID().uuidString)
            let copy = dir.appendingPathComponent(name)
            do {
                try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                try FileManager.default.copyItem(at: source, to: copy)
                return copy
            } catch {
                try? FileManager.default.removeItem(at: dir)
                return nil
            }
        }.value
    }

    private static func movieFile(_ asset: PHAsset, version: PHVideoRequestOptionsVersion) async -> URL? {
        let options = PHVideoRequestOptions()
        options.isNetworkAccessAllowed = true
        options.version = version
        return await withCheckedContinuation { c in
            PHImageManager.default().requestAVAsset(forVideo: asset, options: options) { @Sendable av, _, _ in
                c.resume(returning: (av as? AVURLAsset)?.url)
            }
        }
    }

    static func asset(_ id: String) -> PHAsset? {
        PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject
    }
}

/// The panel: the content of the composer's sheet (`AttachSheetHost`), which sets its detents.
struct AttachSheet<More: View>: View {
    let panel: AttachPanel
    @Binding var caption: String
    /// How many more photos the composer takes (it holds four), and videos.
    var photoRoom: Int
    var videoRoom: Int
    /// Takes a photo with the camera (nil: no camera); opens Files; opens the system's photo picker.
    var camera: (() -> Void)?
    var files: () -> Void
    var allPhotos: () -> Void
    var showsMore: Bool
    @ViewBuilder var more: () -> More

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.buiTheme) private var theme
    private var library: AttachLibrary { panel.library }
    @State private var tab: AttachTab = .photos
    @State private var detent: PresentationDetent = .medium
    /// The photo open in the editor (a tap on its picture), and what runs once the editor has gone (Send leaves the panel).
    @State private var previewing: AttachPreview?
    @State private var afterPreview: (() -> Void)?
    @FocusState private var captionFocused: Bool

    private var tabs: [AttachTab] { showsMore ? AttachTab.allCases : [.photos, .files] }
    private var picked: [String] { panel.picked }
    private var picks: AttachPicks { panel.picks }
    private var surface: Color { AppStyle.current.usesBeautifulUI ? theme.colors.surface : Theme.Shell.composer }
    #if DEBUG
    /// Simulator automation: `-showAttachCamera YES` shows the camera cell on a simulator, which has no camera, so
    /// screenshots and UI tests see the grid as a phone draws it (a tap does nothing).
    private var cameraCell: (() -> Void)? { camera ?? (AttachDebug.showsCameraCell ? {} : nil) }
    #else
    private var cameraCell: (() -> Void)? { camera }
    #endif

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachSheet")
        #endif
        GeometryReader { outer in
            let width = outer.size.width
            VStack(spacing: Theme.Space.flush) {
                header
                page(width: width)
            }
            // The bar sits on the sheet's safe area: over the home indicator, or over the keyboard while the caption is
            // typed, so the field and what is typed stay in sight (#3704, #3004); the grid scrolls under it to its end.
            .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) { bar(width: width) }
        }
        // Typing the caption opens the sheet to full, as the panel always did: the keyboard takes the half it rested
        // in, and the sheet stays open when the keyboard goes (#3004).
        .onChange(of: captionFocused) { _, focused in if focused { detent = .large } }
        .presentationDetents([.medium, .large], selection: $detent)
        .presentationDragIndicator(.visible)
        .presentationBackground(surface)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("attach-sheet")
        .task { await library.load() }
        .onDisappear { library.forget() }
        .fullScreenCover(item: $previewing, onDismiss: {
            let then = afterPreview
            afterPreview = nil
            then?()
        }) { preview in
            AttachPhotoEditor(asset: preview.asset, resume: panel.edited[preview.id], picked: picked.contains(preview.id), caption: $caption,
                              done: { keep(preview.asset, $0, sending: false) },
                              send: { keep(preview.asset, $0, sending: true) })
        }
    }

    // MARK: The editor

    /// The editor's Select / Done (`sending` false) or Send on `asset`: the edit remembered (an edit undone to nothing is
    /// forgotten, so the photo goes from the library as it is), the photo picked if there is room, and Send leaves the
    /// panel with everything picked once the editor has gone.
    private func keep(_ asset: PHAsset, _ edit: EditedImage, sending: Bool) {
        let id = asset.localIdentifier
        panel.edited[id] = edit.edits.isEmpty ? nil : edit
        if !panel.picked.contains(id) {
            guard AttachGrid.hasRoom(for: asset, picked: panel.picked, photoRoom: photoRoom, videoRoom: videoRoom) else {
                AppModel.shared.toast = Toast(String(localized: "Up to 4 at a time"), style: .error)
                return
            }
            panel.picked.append(id)
            Haptics.tap()
        }
        if sending { afterPreview = { leave(.send(picks)) } }
    }

    private var header: some View {
        ZStack {
            Text(tab == .photos ? String(localized: "Recents") : tab.title)
                .font(Theme.Font.heading)
                .lineLimit(1)
                .contentTransition(.opacity)
                .accessibilityAddTraits(.isHeader)
            HStack {
                Button { leave(.closed(picks)) } label: {
                    ThemedIcon(Theme.Icon.close).font(Theme.Font.heading)
                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close")
                .accessibilityIdentifier("attach-close")
                Spacer()
                if tab == .photos {
                    Button { leave(.then(picks, allPhotos)) } label: {
                        Text("All Photos").font(Theme.Font.secondaryMedium).lineLimit(1)
                            .scaledFrame(minHeight: Theme.Size.tapTarget)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Theme.accent)
                    .accessibilityIdentifier("attach-all-photos")
                }
            }
            .padding(.horizontal, Theme.Space.m)
        }
        // Under the system's grabber.
        .padding(.top, Theme.Space.l)
        .padding(.bottom, Theme.Space.xs)
        .frame(maxWidth: .infinity)
        // Chrome, like the shell's top bars: at the accessibility sizes the centered title ran into All Photos.
        .dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize)
    }

    // MARK: Pages

    @ViewBuilder private func page(width: CGFloat) -> some View {
        ZStack(alignment: .top) {
            switch tab {
            case .photos: photos(width: width).transition(.asymmetric(insertion: .identity, removal: .opacity))
            case .files: filesPage.transition(.asymmetric(insertion: .identity, removal: .opacity))
            case .more: morePage.transition(.asymmetric(insertion: .identity, removal: .opacity))
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }

    @ViewBuilder private func photos(width: CGFloat) -> some View {
        switch library.access {
        case .unknown:
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        case .denied:
            AttachNoAccess(settings: openSettings, allPhotos: { leave(.then(picks, allPhotos)) })
        case .full, .limited:
            AttachGrid(library: library, picked: Bindable(panel).picked, edited: panel.edited.mapValues(\.result), width: width,
                       camera: cameraCell.map { camera in { leave(.then(picks, camera)) } },
                       limited: library.access == .limited, photoRoom: photoRoom, videoRoom: videoRoom,
                       open: { previewing = AttachPreview(asset: $0) })
        }
    }

    private var filesPage: some View {
        List {
            Section {
                Button { leave(.then(picks, files)) } label: {
                    Label { Text("Browse Files").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.file) }
                }
                .accessibilityIdentifier("attach-browse-files")
            } footer: {
                Text("A file goes up to this conversation's computer, and its path into your message.")
            }
        }
        .scrollContentBackground(.hidden)
    }

    private var morePage: some View {
        List { more() }
            .scrollContentBackground(.hidden)
    }

    // MARK: The bar

    /// The tabs, or once photos are picked the caption and Send.
    @ViewBuilder private func bar(width: CGFloat) -> some View {
        ZStack(alignment: .bottom) {
            if picked.isEmpty {
                // A fixed pill: its labels stop growing where the shell's top bars do.
                AttachTabBar(tabs: tabs, selected: tab, width: AttachLayout.barWidth(tabs: tabs.count, width: width), pick: pick)
                    .dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize)
                    .transition(.opacity)
            } else {
                captionBar
                    .transition(.opacity)
            }
        }
        .padding(.bottom, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .animation(reduceMotion ? nil : Theme.Motion.attachBarSwap, value: picked.isEmpty)
    }

    private var captionBar: some View {
        HStack(alignment: .bottom, spacing: Theme.Space.xs) {
            TextField("Add a caption…", text: $caption, axis: .vertical)
                .lineLimit(1...4)
                .font(Theme.Font.body)
                .focused($captionFocused)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .modifier(ComposerSurface())
                .accessibilityIdentifier("attach-caption")
            HStack(spacing: Theme.Space.flush) {
                ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "attach-send", pressScales: true) {
                    leave(.send(picks))
                }
            }
            .transition(.composerSend)
        }
        .padding(.horizontal, Theme.Space.l)
    }

    // MARK: Actions

    /// The sheet goes down (the system's motion), then the composer gets `outcome` (`AttachSheetHost`).
    private func leave(_ outcome: AttachOutcome) {
        captionFocused = false
        panel.leave(outcome)
    }

    private func pick(_ next: AttachTab) {
        if next == tab {
            detent = .large // the tab already shown: open all the way
            return
        }
        withAnimation(reduceMotion ? nil : Theme.Motion.attachTabFade) { tab = next }
    }

    private func openSettings() {
        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
    }
}

#if DEBUG
/// The panel's simulator switches, read once.
private enum AttachDebug {
    static let showsCameraCell = UserDefaults.standard.bool(forKey: "showAttachCamera")
}
#endif

/// The floating tab bar: a pill of tabs, a highlight sliding under the one shown.
struct AttachTabBar: View {
    var tabs: [AttachTab]
    var selected: AttachTab
    var width: CGFloat
    var pick: (AttachTab) -> Void
    @Namespace private var lens

    var body: some View {
        HStack(spacing: Theme.Space.flush) {
            ForEach(tabs) { tab in
                Button { pick(tab) } label: {
                    VStack(spacing: Theme.Space.xxs) {
                        ThemedIcon(tab.symbol)
                            .font(Theme.Font.heading)
                            .frame(width: Theme.Attach.barIcon, height: Theme.Attach.barIcon)
                        Text(tab.title).font(Theme.Font.labelMedium).lineLimit(1)
                    }
                    .foregroundStyle(tab == selected ? Theme.accent : Color.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background {
                        if tab == selected {
                            Capsule().fill(Theme.Fill.chip).matchedGeometryEffect(id: "lens", in: lens)
                        }
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(tab.title)
                .accessibilityAddTraits(tab == selected ? .isSelected : [])
                .accessibilityIdentifier("attach-tab-\(tab.rawValue)")
            }
        }
        .padding(Theme.Attach.barLensInset)
        .frame(width: width, height: Theme.Attach.barHeight)
        .modifier(ComposerSurface())
        .animation(Theme.Motion.attachLens, value: selected)
    }
}

/// The grid of recent photos: three equal square columns, the camera first as one cell like the others.
private struct AttachGrid: View {
    let library: AttachLibrary
    @Binding var picked: [String]
    /// The pictures of the photos edited in the panel, drawn in their cells instead of the library's.
    var edited: [String: UIImage]
    var width: CGFloat
    var camera: (() -> Void)?
    var limited: Bool
    var photoRoom: Int
    var videoRoom: Int
    /// A tap on a photo's picture (not its ring): it opens in the editor.
    var open: (PHAsset) -> Void
    @Environment(\.displayScale) private var displayScale

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachGrid")
        #endif
        let cell = AttachLayout.cell(width: width, scale: displayScale)
        let spacing = Theme.Attach.gridSpacing
        let columns = Array(repeating: GridItem(.fixed(cell), spacing: spacing), count: Theme.Attach.columns)
        ScrollView {
            VStack(spacing: Theme.Space.flush) {
                if limited { AttachLimitedBanner(refetch: { await library.fetch() }) }
                LazyVGrid(columns: columns, spacing: spacing) {
                    if let camera {
                        AttachCameraCell(action: camera).frame(width: cell, height: cell)
                    }
                    ForEach(library.assets, id: \.localIdentifier) { photo($0, cell: cell) }
                }
            }
        }
        .scrollIndicators(.hidden)
        // Typing the caption, a drag of the grid puts the keyboard away and the caption back down. Not
        // `scrollDismissesKeyboard`: it leaves the caption's keyboard up, the field being outside the grid.
        .onScrollPhaseChange { _, phase in if phase == .interacting { Keyboard.dismiss() } }
        .accessibilityIdentifier("attach-grid")
    }

    private func photo(_ asset: PHAsset, cell: CGFloat) -> some View {
        let id = asset.localIdentifier
        return AttachPhotoCell(library: library, asset: asset, side: cell, number: picked.firstIndex(of: id).map { $0 + 1 },
                               edited: edited[id], toggle: { toggle(asset) },
                               open: { asset.mediaType == .video ? toggle(asset) : open(asset) })
    }

    private func toggle(_ asset: PHAsset) {
        let id = asset.localIdentifier
        if let i = picked.firstIndex(of: id) { picked.remove(at: i); return }
        guard Self.hasRoom(for: asset, picked: picked, photoRoom: photoRoom, videoRoom: videoRoom) else {
            AppModel.shared.toast = Toast(String(localized: "Up to 4 at a time"), style: .error)
            return
        }
        picked.append(id)
        Haptics.tap()
    }

    /// The composer takes one more of `asset`'s kind (photos and videos are counted apart).
    static func hasRoom(for asset: PHAsset, picked: [String], photoRoom: Int, videoRoom: Int) -> Bool {
        let video = asset.mediaType == .video
        let same = picked.compactMap(AttachLibrary.asset).filter { ($0.mediaType == .video) == video }.count
        return same < (video ? videoRoom : photoRoom)
    }
}

/// One photo or video in the grid: its thumbnail (an edited photo's edit), a video's length, and the mark that says it
/// is picked and in what order. The mark's corner picks (`toggle`); the rest of a photo opens it in the editor (`open`;
/// a video's picks too). VoiceOver's activation picks, as before; Edit is an action.
private struct AttachPhotoCell: View {
    let library: AttachLibrary
    let asset: PHAsset
    var side: CGFloat
    var number: Int?
    var edited: UIImage?
    var toggle: () -> Void
    var open: () -> Void
    @State private var image: UIImage?

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachPhotoCell")
        #endif
        ZStack(alignment: .topTrailing) {
            Button(action: open) {
                ZStack(alignment: .bottomLeading) {
                    Group {
                        if let shown = edited ?? image { Image(uiImage: shown).resizable().scaledToFill() } else { Theme.Fill.well }
                    }
                    .frame(width: side, height: side)
                    .clipped()
                    if asset.mediaType == .video {
                        Text(Duration.seconds(asset.duration).formatted(.time(pattern: .minuteSecond)))
                            .font(Theme.Font.labelStrong)
                            .foregroundStyle(.white)
                            .shadow(radius: Theme.Space.xxs)
                            .padding(Theme.Space.xs)
                    } else if edited != nil {
                        ThemedIcon(Theme.Icon.markup).font(Theme.Font.labelStrong).foregroundStyle(.white)
                            .shadow(radius: Theme.Space.xxs)
                            .padding(Theme.Space.xs)
                    }
                }
                // Taps where it is drawn: the thumbnail filling the square is laid out past it (a landscape photo, wider
                // than tall), and the cell to the right, drawn over it, took the taps on this one's ring (#3702).
                .frame(width: side, height: side)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            // The ring, and a finger's room round it in the corner.
            Button(action: toggle) {
                AttachPickMark(number: number)
                    .padding(Theme.Attach.badgeInset)
                    .frame(width: Theme.Attach.badgeTarget, height: Theme.Attach.badgeTarget, alignment: .topTrailing)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
        }
        .frame(width: side, height: side)
        .task(id: asset.localIdentifier) { image = await library.thumbnail(asset, side: side) }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(asset.mediaType == .video ? String(localized: "Video") : edited != nil ? String(localized: "Photo, edited") : String(localized: "Photo"))
        .accessibilityValue(number.map { String(localized: "Picked, \($0)") } ?? "")
        .accessibilityAddTraits(number != nil ? [.isButton, .isSelected] : .isButton)
        .accessibilityAction { toggle() }
        .accessibilityActions {
            if asset.mediaType != .video { Button(String(localized: "Edit [Attach Sheet 7d3c90]"), action: open) }
        }
        .accessibilityIdentifier("attach-photo")
    }
}

/// The photo a tap on its picture opened (`AttachSheet.previewing`).
private struct AttachPreview: Identifiable {
    let asset: PHAsset
    var id: String { asset.localIdentifier }
}

/// A photo from the grid in the image editor, with the panel's caption and Send under its tools (`ImageEditorSend`):
/// Select (Done once it is picked) picks it as edited, Send picks it and sends, Cancel changes nothing. The picture is
/// fetched first (from iCloud if it has to be); until then a spinner and Cancel.
private struct AttachPhotoEditor: View {
    let asset: PHAsset
    /// How it was left when it was edited in the panel before.
    let resume: EditedImage?
    var picked: Bool
    @Binding var caption: String
    var done: (EditedImage) -> Void
    var send: (EditedImage) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var source: UIImage?

    var body: some View {
        if let start = resume ?? source.map({ EditedImage(source: $0) }) {
            ImageEditor(start, doneTitle: picked ? String(localized: "Done [Extension System Presentation c0b3fb]") : String(localized: "Select"),
                        send: ImageEditorSend(caption: $caption, send: send), onDone: done)
        } else {
            NavigationStack {
                ZStack {
                    Color.black.ignoresSafeArea()
                    ProgressView().tint(.white)
                }
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { dismiss() }.accessibilityIdentifier("image-edit-cancel")
                    }
                }
                .toolbarBackground(.black, for: .navigationBar)
                .toolbarColorScheme(.dark, for: .navigationBar)
            }
            .environment(\.colorScheme, .dark) // as the editor's: never `preferredColorScheme`, which reaches the panel
            .task {
                if let picture = await AttachLibrary.editable(asset) {
                    source = picture
                } else {
                    AppModel.shared.toast = Toast(String(localized: "Couldn't open this photo. It may still be downloading from iCloud."), style: .error)
                    dismiss()
                }
            }
        }
    }
}

/// The round mark in a cell's corner: an empty ring, or filled with the photo's place in the order, bouncing as it fills.
private struct AttachPickMark: View {
    var number: Int?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ZStack {
            Circle().fill(number == nil ? Color.black.opacity(Theme.Attach.dim) : Theme.accent)
            Circle().strokeBorder(.white, lineWidth: Theme.Attach.badgeRing)
            if let number {
                Text(verbatim: "\(number)").font(Theme.Font.labelStrong).foregroundStyle(.white).monospacedDigit()
            }
        }
        .frame(width: Theme.Attach.badge, height: Theme.Attach.badge)
        .keyframeAnimator(initialValue: CGFloat(1), trigger: number) { mark, scale in
            mark.scaleEffect(scale)
        } keyframes: { _ in
            if number != nil, !reduceMotion {
                CubicKeyframe(Theme.Motion.attachPickSqueeze, duration: Theme.Motion.attachPickSqueezeTime)
                CubicKeyframe(Theme.Motion.attachPickBounce, duration: Theme.Motion.attachPickBounceTime)
                CubicKeyframe(1, duration: Theme.Motion.attachPickSettleTime)
            } else {
                CubicKeyframe(1, duration: Theme.Motion.attachPickSettleTime)
            }
        }
        .animation(number == nil ? Theme.Motion.attachUnpick : Theme.Motion.attachPick, value: number)
        .shadow(color: .black.opacity(Theme.Attach.dim), radius: Theme.Space.xxs)
    }
}

/// The camera's cell: takes a photo with the system camera (the panel goes first). Telegram shows the live camera here.
private struct AttachCameraCell: View {
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            ZStack {
                Theme.Fill.chip
                ThemedIcon(Theme.Icon.camera).font(Theme.Font.title).foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Take Photo")
        .accessibilityIdentifier("attach-camera")
    }
}

/// Limited access: a line over the grid saying so, and Manage to pick more.
private struct AttachLimitedBanner: View {
    var refetch: () async -> Void

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            Text("Persona can see only the photos you chose.")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button("Manage") {
                guard let top = AttachTop.controller() else { return }
                PHPhotoLibrary.shared().presentLimitedLibraryPicker(from: top) { _ in Task { @MainActor in await refetch() } }
            }
            .font(Theme.Font.secondaryStrong)
            .lineLimit(1)
            .accessibilityIdentifier("attach-manage")
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
    }
}

/// No access to the photos: say so, offer Settings, and the system's picker (which needs none).
private struct AttachNoAccess: View {
    var settings: () -> Void
    var allPhotos: () -> Void

    var body: some View {
        VStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.photos).font(Theme.Font.display).foregroundStyle(.secondary)
            Text("Access Your Photos").font(Theme.Font.heading)
            Text("Let Persona see your photos to pick them here, or choose them in the system's picker.")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("Open Settings", action: settings)
                .themedButton(prominent: true)
                .lineLimit(1)
            Button("Choose Photos", action: allPhotos)
                .font(Theme.Font.bodyStrong)
                .lineLimit(1)
                .accessibilityIdentifier("attach-choose-photos")
        }
        .padding(.horizontal, Theme.Space.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Puts the panel in a system sheet from the composer, and hands back what it gave once the sheet is down.
struct AttachSheetHost<More: View>: ViewModifier {
    let panel: AttachPanel
    @Binding var caption: String
    var photoRoom: Int
    var videoRoom: Int
    var camera: (() -> Void)?
    var files: () -> Void
    var allPhotos: () -> Void
    var showsMore: Bool
    @ViewBuilder var more: () -> More
    var finish: (AttachOutcome) -> Void

    func body(content: Content) -> some View {
        content.sheet(isPresented: Binding(get: { panel.shown }, set: { panel.shown = $0 }), onDismiss: {
            // Closed by the system (a swipe down, a tap outside): what was picked goes into the composer.
            let outcome = panel.outcome ?? .closed(panel.picks)
            panel.outcome = nil
            panel.picked = []
            panel.edited = [:]
            // What runs after it (the camera, Files) presents now that the sheet is down.
            finish(outcome)
        }) {
            AttachSheet(panel: panel, caption: $caption, photoRoom: photoRoom, videoRoom: videoRoom, camera: camera, files: files,
                        allPhotos: allPhotos, showsMore: showsMore, more: more)
        }
    }
}

/// The view controller on top, for UIKit sheets the panel presents itself (the limited-library picker).
private enum AttachTop {
    static func controller() -> UIViewController? {
        let window = UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first
        var top = window?.rootViewController
        while let next = top?.presentedViewController, !next.isBeingDismissed { top = next }
        return top
    }
}

/// A row of what + offers besides photos and files (#3225): in the panel's More tab it closes the panel, then acts; in
/// the menu (`AttachMenuItems`) it acts.
struct AttachMoreRow: Identifiable {
    let id: String
    let title: String
    let symbol: String
    /// The "/" word a quick command's title ends with (「压缩上下文 /compact」): the menu draws it as the row's second
    /// line, so the title stays on one.
    var word: String? = nil
    var destructive = false
    var disabled = false
    let action: () -> Void

    /// The title without its trailing `word`, and the word; nil when the title does not end with it.
    var split: (title: String, word: String)? {
        guard let word, title.hasSuffix(" " + word) else { return nil }
        return (String(title.dropLast(word.count + 1)), word)
    }
}

struct AttachMoreSection: Identifiable {
    let id: String
    let title: String?
    let rows: [AttachMoreRow]
    /// The menu folds the section into a submenu with this icon and the section's title; the More tab keeps it a section.
    var symbol: String? = nil
}

/// What + offers besides photos and files as menu items, in their order (the menu puts the first nearest the finger):
/// the iPhone's touch and hold on + and the iPad's + menu, from the same list as the panel's More tab (#3225).
struct AttachMenuItems: View {
    let sections: [AttachMoreSection]

    var body: some View {
        ForEach(sections) { section in
            Section {
                if let symbol = section.symbol, let title = section.title {
                    Menu { rows(section) } label: { Label { Text(title).lineLimit(1) } icon: { Theme.Icon.image(symbol) } }
                        .accessibilityIdentifier("attach-menu-\(section.id)")
                } else {
                    rows(section)
                }
            }
        }
    }

    private func rows(_ section: AttachMoreSection) -> some View {
        ForEach(section.rows) { row in
            // A menu draws a second line only from a second Text right in the label (docs/lessons/ios.md, #1307).
            if let split = row.split {
                item(row) { Text(split.title); Text(split.word); Theme.Icon.image(row.symbol) }
            } else {
                item(row) { Label { Text(row.title).lineLimit(1) } icon: { Theme.Icon.image(row.symbol) } }
            }
        }
    }

    /// The identifier right on the button, as the composer's menus had it; UI tests fall back on the title where UIKit's
    /// menu drops it.
    private func item(_ row: AttachMoreRow, @ViewBuilder label: () -> some View) -> some View {
        Button(role: row.destructive ? .destructive : nil, action: row.action, label: label)
            .accessibilityIdentifier(row.id)
            .disabled(row.disabled)
    }
}

struct AttachMoreRows: View {
    let panel: AttachPanel
    let sections: [AttachMoreSection]

    var body: some View {
        ForEach(sections) { section in
            Section {
                ForEach(section.rows) { row in
                    Button(role: row.destructive ? .destructive : nil) { panel.close(then: row.action) } label: {
                        Label { Text(row.title).lineLimit(1) } icon: { ThemedIcon(row.symbol) }
                    }
                    .disabled(row.disabled)
                    .accessibilityIdentifier(row.id)
                }
            } header: {
                if let title = section.title { Text(title) }
            }
        }
    }
}

extension AttachPanel {
    /// Opens the sheet, nothing picked yet (the last one's `onDismiss` emptied it). Not while the last one is still on
    /// its way out with an outcome to hand back.
    func open() {
        guard outcome == nil else { return }
        shown = true
    }

    /// What the iPhone's 「+」 does on a tap (`ShellComposerBar.openAttach`), in a conversation and a new one alike; nil on
    /// the iPad, whose + is the menu. The composer's keyboard goes down as the sheet comes up.
    func opener(focused: FocusState<Bool>.Binding) -> ((CGRect) -> Void)? {
        guard UIDevice.current.userInterfaceIdiom == .phone else { return nil }
        return { _ in
            focused.wrappedValue = false
            self.open()
        }
    }
}

extension AttachMoreSection {
    /// The rows every composer's + offers besides photos and files, in their order (#3225, #3805): the calls and the
    /// camera, then Mention. A row whose action is nil is not there (no call before a conversation exists, no camera).
    /// `scan`: Scan Document (#3720), next to Take Photo. `meeting`: Persona's main conversation's Meeting Recording
    /// (#3723), the sidebar's row by its words: with a recording under way it says so and opens it. `context`: Add as
    /// Context › Knowledge Note… / To-do… / Inbox Item… (#3721), folded under Mention.
    static func common(voice: (() -> Void)?, camera: (() -> Void)?, scan: (() -> Void)? = nil, glasses: (() -> Void)?,
                       meeting: (() -> Void)? = nil, mention: @escaping () -> Void,
                       context: ((ContextPickKind) -> Void)? = nil) -> [AttachMoreSection] {
        var capture: [AttachMoreRow] = []
        if let voice { capture.append(AttachMoreRow(id: "composer-menu-voice", title: String(localized: "Voice Chat"), symbol: Theme.Icon.voiceChannel, action: voice)) }
        if let camera { capture.append(AttachMoreRow(id: "composer-menu-camera", title: String(localized: "Take Photo [Chat View 6e3a10]"), symbol: Theme.Icon.camera, action: camera)) }
        if let scan { capture.append(AttachMoreRow(id: "composer-menu-scan", title: String(localized: "Scan Document"), symbol: Theme.Icon.scanDocument, action: scan)) }
        if let glasses { capture.append(AttachMoreRow(id: "attach-glasses", title: String(localized: "Glasses Photo"), symbol: Theme.Icon.glasses, action: glasses)) }
        if let meeting {
            let recording = RecordingCapture.shared.state != .idle
            capture.append(AttachMoreRow(id: "composer-menu-meeting", title: recording ? String(localized: "Recording… Open") : String(localized: "Record [Recordings Section ef2821]"),
                                         symbol: Theme.Icon.recording, action: meeting))
        }
        var sections: [AttachMoreSection] = []
        if !capture.isEmpty { sections.append(AttachMoreSection(id: "capture", title: nil, rows: capture)) }
        sections.append(AttachMoreSection(id: "mention", title: nil, rows: [
            AttachMoreRow(id: "composer-menu-mention", title: String(localized: "Mention a File or Session"), symbol: Theme.Icon.mention, action: mention),
        ]))
        if let context {
            sections.append(AttachMoreSection(id: "context", title: String(localized: "Add as Context"), rows: ContextPickKind.allCases.map { kind in
                AttachMoreRow(id: "attach-context-\(kind.rawValue)", title: kind.menuTitle, symbol: kind.symbol) { context(kind) }
            }, symbol: Theme.Icon.addContext))
        }
        return sections
    }

    /// The prompts the hub's extensions declare, folded into a submenu in the menu; nil when there are none.
    static func prompts(_ prompts: [ExtensionTextTemplate], fill: @escaping (ExtensionTextTemplate) -> Void) -> AttachMoreSection? {
        guard !prompts.isEmpty else { return nil }
        return AttachMoreSection(id: "prompts", title: String(localized: "Extension Prompts"), rows: prompts.map { prompt in
            AttachMoreRow(id: "extension-prompt-\(prompt.id)", title: prompt.title, symbol: Theme.Icon.insert) { fill(prompt) }
        }, symbol: Theme.Icon.plugins)
    }
}

extension ComposerAttachments {
    /// What was picked in the panel, into the box: pictures as its photos (at most `MessagePhotos.limit`), the ones
    /// edited there as edited (and still editable in the strip), videos uploaded to `machine` as files; a toast says how
    /// many could not be read.
    func addPicked(_ picks: AttachPicks, machine: MachineRef, model: AppModel) async {
        var failed = 0
        for id in picks.ids {
            if let edit = picks.edited[id] {
                add(edit, model: model)
                continue
            }
            guard let asset = AttachLibrary.asset(id) else { failed += 1; continue }
            if asset.mediaType == .video {
                guard let url = await AttachLibrary.movie(asset) else { failed += 1; continue }
                videos.start(staged: url, machine: machine, model: model)
            } else {
                guard let picture = await AttachLibrary.picture(asset) else { failed += 1; continue }
                MessagePhotos.add(picture, to: &images, model: model)
            }
        }
        if failed > 0 {
            model.toast = Toast(String(localized: "Couldn't read \(failed) photos (they may still be downloading from iCloud) [Chat View 5712c0]"), style: .error)
        }
    }
}
#endif
