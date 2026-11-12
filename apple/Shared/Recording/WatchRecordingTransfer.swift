#if canImport(WatchConnectivity)
import Foundation
import WatchConnectivity

nonisolated struct RecordingReceipt: Codable, Sendable {
    var id: UUID
    var index: Int?
    var total: Int?
    static let key = "personaRecordingReceipt"
    var dictionary: [String: Any] {
        let data = (try? JSONEncoder().encode(self)) ?? Data()
        return [Self.key: data, LegacyNames.recordingReceiptKey: data]
    }
    init(id: UUID, index: Int? = nil, total: Int? = nil) { self.id = id; self.index = index; self.total = total }
    init?(dictionary: [String: Any]) {
        guard let data = (dictionary[Self.key] ?? dictionary[LegacyNames.recordingReceiptKey]) as? Data, let value = try? JSONDecoder().decode(Self.self, from: data) else { return nil }
        self = value
    }
}

#if os(watchOS)
/// Adapter for Hourhand's upload queue: the index is persisted first, audio is deleted only after the phone confirms it is on disk, failures back off.
@Observable
final class WatchRecordingTransfer {
    static let shared = WatchRecordingTransfer()
    private(set) var error: String?
    var reachable: Bool { WCSession.default.activationState == .activated && WCSession.default.isReachable }
    @ObservationIgnored private var ticker: Task<Void, Never>?
    @ObservationIgnored private var attempts: [String: Int] = [:]
    @ObservationIgnored private var retryAt: [String: Date] = [:]
    @ObservationIgnored private var lastBatch = Date.distantPast
    @ObservationIgnored private var lastPrune = Date.distantPast

    func start() {
        guard ticker == nil else { return }
        // These slots have one owner, this transfer. All-day recording rides on the same events (activation, reachability,
        // a saved chunk) through this fan-out instead of taking a slot over, which would silently stop clips and rolling
        // recordings from being sent (docs/assistant/day-recap.md「Watch to phone」). Its own slots (acknowledgements, the
        // phone's erase and delete requests) are taken in `WatchDayTransfer.start()`, here before WatchSync activates.
        WatchSync.shared.onRecordingEvent = { [weak self] in
            self?.pump(force: true)
            WatchDayTransfer.shared.pump()
        }
        WatchSync.shared.onRecordingReceipt = { [weak self] in self?.received($0) }
        RecordingCapture.shared.onSaved = { [weak self] _ in
            self?.pump(force: false)
            WatchDayTransfer.shared.pump()
        }
        WatchDayTransfer.shared.start()
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(5)) } catch { return }
                self?.pruneRolling()
                self?.pump(force: false)
            }
        }
        pump(force: true)
    }

    /// The watch can't read health data, so it only goes by age (kept is never set here); see `RecordingRolling.expiredOnWatch`.
    /// Rolling only: an all-day chunk goes once the phone acknowledges it, or as a reported loss past the phone's
    /// `recordingDayKeepDays` (`WatchDayTransfer`), never by this window.
    private func pruneRolling() {
        let now = Date()
        guard now.timeIntervalSince(lastPrune) >= RecordingRolling.pruneInterval else { return }
        lastPrune = now
        let library = RecordingLibrary.shared
        for entry in library.entries where entry.isRolling {
            library.prune(entry.id, indexes: RecordingRolling.expiredOnWatch(entry, now: now))
            if !entry.active, entry.endDelivered, library.entry(entry.id)?.segments.isEmpty ?? true {
                try? library.remove(entry.id)
            }
        }
    }

    func pending(_ id: UUID) -> Int {
        RecordingLibrary.shared.entry(id)?.segments.filter { !$0.delivered }.count ?? 0
    }

    func pump(force: Bool) {
        let session = WCSession.default
        guard session.activationState == .activated, session.isCompanionAppInstalled else { return }
        let now = Date()
        // Low Power Mode spaces the batches of a recording still under way further apart.
        let batch = ProcessInfo.processInfo.isLowPowerModeEnabled ? RecordingAudioSpec.lowPowerTransferBatchSeconds
            : RecordingAudioSpec.transferBatchSeconds
        let batchDue = reachable || force || now.timeIntervalSince(lastBatch) >= batch
        // All-day recordings go their own way (`WatchDayTransfer`): chunk by chunk and event by event, acknowledged by
        // seq and `n`.
        for entry in RecordingLibrary.shared.entries where entry.source == .watch && !entry.isDay {
            guard !entry.active || batchDue else { continue }
            for segment in entry.segments where !segment.delivered {
                let key = "\(entry.id)/\(segment.index)"
                guard now >= retryAt[key] ?? .distantPast else { continue }
                let already = session.outstandingFileTransfers.contains {
                    let transfer = RecordingTransfer(dictionary: $0.file.metadata)
                    return transfer?.id == entry.id && transfer?.segment?.index == segment.index
                }
                let url = RecordingLibrary.shared.audio(entry.id, segment.index)
                guard !already, FileManager.default.fileExists(atPath: url.path) else { continue }
                session.transferFile(url, metadata: RecordingTransfer(entry: entry, segment: segment).dictionary)
                retryAt[key] = now.addingTimeInterval(60)
            }
            if !entry.active, !entry.endDelivered, entry.expectedSegments != nil {
                let key = "\(entry.id)/end"
                guard now >= retryAt[key] ?? .distantPast else { continue }
                let already = session.outstandingUserInfoTransfers.contains { RecordingTransfer(dictionary: $0.userInfo)?.id == entry.id }
                if !already { session.transferUserInfo(RecordingTransfer(entry: entry).dictionary) }
                retryAt[key] = now.addingTimeInterval(60)
            }
        }
        if batchDue { lastBatch = now }
    }

    func finished(_ transfer: RecordingTransfer, failed: Bool) {
        let key = "\(transfer.id)/" + (transfer.segment.map { String($0.index) } ?? "end")
        let count = failed ? (attempts[key] ?? 0) + 1 : 0
        attempts[key] = count
        retryAt[key] = Date().addingTimeInterval(failed ? min(300, 5 * pow(2, Double(min(count, 6)))) : 60)
        if failed { error = String(localized: "Recording not sent to iPhone yet; it'll retry automatically") }
    }

    private func received(_ receipt: RecordingReceipt) {
        let library = RecordingLibrary.shared
        guard var entry = library.entry(receipt.id) else { return }
        if let index = receipt.index, let position = entry.segments.firstIndex(where: { $0.index == index }) {
            entry.segments[position].delivered = true
            do {
                try library.save(entry)
                try? FileManager.default.removeItem(at: library.audio(entry.id, index))
            } catch { self.error = String(localized: "The delivery confirmation wasn't saved; the audio stays on the watch"); return }
        }
        if let total = receipt.total, entry.expectedSegments == total { library.change(entry.id) { $0.endDelivered = true } }
        error = nil
    }
}
#endif
#endif
