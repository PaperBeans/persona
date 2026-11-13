import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

// Files in and out of a folder the file browser shows (docs/protocol/protocol.md "Changing files", #4858): uploads keep their names in
// that folder (only folders in the machine's home take them), one after another with progress; a taken name asks
// Replace / Keep Both before anything is sent. A download lands in a temporary folder of its own and goes on to Save to Files.

@MainActor @Observable
final class FolderTransfers {
    static let limit: Int64 = 2 << 30

    struct Item: Identifiable {
        enum Kind { case upload, download }
        let id = UUID()
        var kind: Kind
        var name: String
        var size: Int64
        var done: Int64 = 0
        var waiting = true // behind another transfer
        var asking = false // the name is taken: waiting for Replace / Keep Both
        var zipping = false // several items (or a folder) zipped on the computer before the download (#5828)
    }

    /// A name already in the folder, waiting for the user's answer.
    struct Conflict: Identifiable {
        let id = UUID()
        var name: String
        fileprivate var answer: (Choice) -> Void
    }

    enum Choice { case replace, keep, cancel }

    private(set) var items: [Item] = []
    var conflict: Conflict? {
        didSet { if let conflict { conflictName = conflict.name } }
    }
    /// The last conflict's name, kept after the answer so the alert's title does not go blank as it closes.
    private(set) var conflictName = ""
    /// A finished download, for Save to Files; set back to nil when the picker is done.
    var saving: URL?
    @ObservationIgnored private var tasks: [UUID: Task<Void, Never>] = [:]
    @ObservationIgnored private var last: Task<Void, Never>?

    /// Queues files picked on this device for the folder `dir` (as the browser knows it, relative to cwd or ~/);
    /// uploaded gets each file's path on the machine. owned: the files are our own temporary copies (from the photo
    /// library), removed with their folder once sent.
    func upload(_ urls: [URL], into dir: String, cwd: String, machine: MachineRef, model: AppModel, owned: Bool = false,
                uploaded: @escaping (String) -> Void) {
        let features = model.features(machine.group)
        if !features.isEmpty, !features.contains("upload_dir") {
            model.toast = Toast(String(localized: "The hub is too old to upload into folders. Update the hub and try again."), style: .error)
            return
        }
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            let size = Int64((try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
            if size > Self.limit {
                if scoped { url.stopAccessingSecurityScopedResource() }
                if owned { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
                model.toast = Toast(String(localized: "“\(url.lastPathComponent)” is \(size.storageText), over 2 GB, and can’t be uploaded"), style: .error)
                continue
            }
            let item = Item(kind: .upload, name: url.lastPathComponent, size: size)
            queue(item) { [weak self] in
                defer {
                    if scoped { url.stopAccessingSecurityScopedResource() }
                    if owned { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
                }
                await self?.send(item.id, url: url, dir: dir, cwd: cwd, machine: machine, model: model, uploaded: uploaded)
            }
        }
    }

    /// Fetches a machine's file for Save to Files: `saving` is set once it is all here.
    func download(_ ref: FileRef, name: String, size: Int64, model: AppModel) {
        let item = Item(kind: .download, name: name, size: size)
        queue(item) { [weak self] in await self?.fetch(item.id, ref: ref, name: name, model: model) }
    }

    /// Several items, or a folder, as one zip the computer makes in a temporary folder (`files/zip`), then downloaded
    /// for Save to Files like one file (#5828).
    func downloadZip(_ paths: [String], name: String, cwd: String, machine: MachineRef, model: AppModel) {
        var item = Item(kind: .download, name: name, size: 0)
        item.zipping = true
        queue(item) { [weak self] in await self?.zipThenFetch(item.id, paths: paths, name: name, cwd: cwd, machine: machine, model: model) }
    }

    func cancel(_ id: UUID) {
        tasks.removeValue(forKey: id)?.cancel()
        items.removeAll { $0.id == id }
    }

    /// When the browser goes away: a question nobody will answer is a no, downloads nobody will save stop. Uploads
    /// go on (the files end up in the folder all the same).
    func stop() {
        answer(.cancel)
        for item in items where item.kind == .download { cancel(item.id) }
        if let saving { MachineFileDownload.remove(saving) }
        saving = nil
    }

    /// After Save to Files (moved, or not): the temporary folder goes.
    func saved() {
        MachineFileDownload.remove(saving)
        saving = nil
    }

    private func queue(_ item: Item, _ work: @escaping () async -> Void) {
        items.append(item)
        let prev = last
        let task = Task { [weak self] in
            await prev?.value
            guard !Task.isCancelled else { return }
            await work()
            self?.tasks[item.id] = nil
            self?.items.removeAll { $0.id == item.id }
        }
        tasks[item.id] = task
        last = task
    }

    private func send(_ id: UUID, url: URL, dir: String, cwd: String, machine: MachineRef, model: AppModel,
                      uploaded: (String) -> Void) async {
        update(id) { $0.waiting = false }
        guard let api = model.api(machine.group) else { return }
        let name = url.lastPathComponent
        do {
            let staged = try await FileUploads.stage(url)
            defer { try? FileManager.default.removeItem(at: staged.deletingLastPathComponent()) }
            update(id) { $0.size = (try? staged.resourceValues(forKeys: [.fileSizeKey]).fileSize).map(Int64.init) ?? $0.size }
            let out = try await Self.send(staged, name: name, dir: dir, cwd: cwd, machine: machine.id, api: api,
                                          ask: { [weak self] name in
                                              self?.update(id) { $0.asking = true }
                                              return await self?.ask(name) ?? .cancel
                                          },
                                          restart: { [weak self] in self?.update(id) { $0.done = 0; $0.asking = false } }) { sent in
                Task { @MainActor [weak self] in self?.update(id) { $0.done = max($0.done, sent) } }
            }
            guard let out, !Task.isCancelled else { return }
            Haptics.tap()
            uploaded(out.path)
        } catch {
            if let msg = Self.message(error, name: name) { model.toast = Toast(msg, style: .error) }
        }
    }

    /// One file into the folder: sent with no conflict policy first; a taken name asks, and the answer sends it again
    /// with it (nil: the user said Cancel).
    static func send(_ file: URL, name: String, dir: String, cwd: String, machine: String, api: APIClient,
                     ask: (String) async -> Choice, restart: () -> Void = {},
                     progress: @escaping @Sendable (Int64) -> Void) async throws -> APIClient.UploadedFile? {
        var policy: String?
        while true {
            do {
                var query = ["dir": dir, "cwd": cwd]
                if let policy { query["conflict"] = policy }
                return try await api.upload(file: file, name: name, machine: machine, dest: "dir", query: query, progress: progress)
            } catch let e as APIError where e.code == "exists" && policy == nil {
                switch await ask(name) {
                case .replace: policy = "replace"
                case .keep: policy = "keep"
                case .cancel: return nil
                }
                restart()
            }
        }
    }

    private func zipThenFetch(_ id: UUID, paths: [String], name: String, cwd: String, machine: MachineRef, model: AppModel) async {
        update(id) { $0.waiting = false }
        guard let api = model.api(machine.group) else { return }
        do {
            let zip = try await api.zipFiles(machine: machine.id, paths: paths, cwd: cwd, dir: nil, name: name)
            try Task.checkCancellation()
            update(id) {
                $0.zipping = false
                $0.size = zip.size
            }
            await fetch(id, ref: FileRef(group: machine.group, machineID: machine.id, path: zip.path, cwd: cwd), name: name, model: model)
        } catch {
            if !error.isCancellation {
                model.toast = Toast(String(localized: "Couldn’t download “\(name)”: \(FolderEdits.message(error))"), style: .error)
            }
        }
    }

    private func fetch(_ id: UUID, ref: FileRef, name: String, model: AppModel) async {
        update(id) { $0.waiting = false }
        guard let api = model.api(ref.group) else { return }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("save-" + UUID().uuidString, isDirectory: true)
        let url = directory.appendingPathComponent((name as NSString).lastPathComponent)
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try await api.download("/api/v1/machines/\(ref.machineID)/file/download",
                                   query: ["path": ref.path, "cwd": ref.cwd ?? ""], to: url) { received, total in
                Task { @MainActor [weak self] in
                    self?.update(id) { $0.done = received; if total > 0 { $0.size = total } }
                }
            }
            try Task.checkCancellation()
            Haptics.tap()
            if let saving { MachineFileDownload.remove(saving) } // one at a time: a newer one wins
            saving = url
        } catch {
            try? FileManager.default.removeItem(at: directory)
            if !error.isCancellation {
                model.toast = Toast(String(localized: "Couldn’t download “\(name)”: \(error.localizedDescription)"), style: .error)
            }
        }
    }

    private func ask(_ name: String) async -> Choice {
        await withCheckedContinuation { (c: CheckedContinuation<Choice, Never>) in
            var answered = false
            conflict = Conflict(name: name) { choice in
                guard !answered else { return }
                answered = true
                c.resume(returning: choice)
            }
        }
    }

    /// The answer to the "already exists" question.
    func answer(_ choice: Choice) {
        let c = conflict
        conflict = nil
        c?.answer(choice)
    }

    private func update(_ id: UUID, _ change: (inout Item) -> Void) {
        if let i = items.firstIndex(where: { $0.id == id }) { change(&items[i]) }
    }

    /// What to tell the user when a file did not make it into the folder; nil when they cancelled it.
    nonisolated static func message(_ error: Error, name: String) -> String? {
        if error.isCancellation { return nil }
        if let e = error as? APIError {
            switch e.code {
            case "too_large": return String(localized: "“\(name)” is over 2 GB and can’t be uploaded")
            case "read_only": return String(localized: "This folder can’t be changed from your phone. Only folders in the home folder can (except ~/.persona and ~/Library).")
            case "node_outdated": return e.message
            default: break
            }
            if e.status == 404 || (e.status == 400 && e.message.contains("dest")) {
                return String(localized: "The hub is too old to upload into folders. Update the hub and try again.")
            }
        }
        return String(localized: "“\(name)” wasn’t uploaded: \(error.localizedDescription)")
    }
}

/// The transfers of a folder under its list: name, progress, cancel.
struct FolderTransfersBar: View {
    var transfers: FolderTransfers

    var body: some View {
        VStack(spacing: Theme.Space.sm) {
            ForEach(transfers.items) { item in
                AttachmentRow(symbol: item.kind == .upload ? Theme.Icon.upload : Theme.Icon.save, name: item.name,
                              status: item.asking ? String(localized: "Already exists, waiting for you") : item.waiting ? String(localized: "Waiting [File Attach 26c8cf]")
                                  : item.zipping ? String(localized: "Zipping on the computer…") : "\(item.done.storageText) / \(item.size.storageText)",
                              progress: item.asking || item.zipping ? nil : item.size > 0 ? Double(min(item.done, item.size)) / Double(item.size) : 0,
                              removeLabel: item.kind == .upload ? String(localized: "Cancel upload of \(item.name)") : String(localized: "Cancel download of \(item.name)")) { transfers.cancel(item.id) }
            }
        }
    }
}

/// A picture or video from the photo library as a file of its own (its original format and name where it has one), or
/// any file dragged in from another app.
nonisolated struct PickedLibraryFile: Transferable {
    let url: URL

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .movie) { try copy($0.file) }
        FileRepresentation(importedContentType: .image) { try copy($0.file) }
        FileRepresentation(importedContentType: .data) { try copy($0.file) }
    }

    private static func copy(_ file: URL) throws -> PickedLibraryFile {
        let dir = FileManager.default.temporaryDirectory.appending(path: "uploads/" + UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let dest = dir.appending(path: file.lastPathComponent)
        try FileManager.default.copyItem(at: file, to: dest)
        return PickedLibraryFile(url: dest)
    }

    /// The picked items as files, in the order picked; the ones that could not be read are counted.
    static func load(_ items: [PhotosPickerItem]) async -> (urls: [URL], failed: Int) {
        var urls: [URL] = [], failed = 0
        for item in items {
            if let f = try? await item.loadTransferable(type: PickedLibraryFile.self) { urls.append(f.url) } else { failed += 1 }
        }
        return (urls, failed)
    }
}
