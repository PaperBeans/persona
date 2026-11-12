#if os(iOS)
import BackgroundTasks
import Foundation
import os

/// All-day recording's catch-up (docs/assistant/day-recap.md): a `BGProcessingTask` that transcribes, uploads and applies
/// retention to what the watch sent while the app sat in the background, run by the system while the phone charges.
/// It doesn't wait for a network: transcription is on the device and retention needs none, and without one the upload
/// only finds the hub unreachable and goes with the next run. Submitted when the app goes to the background and from
/// the receiver as chunks arrive (a day the app is only ever woken by WatchConnectivity never goes through that
/// transition), and again after a run that left work. Submitting replaces the pending request, so asking often costs
/// nothing.
enum RecordingDayCatchUp {
    /// In `BGTaskSchedulerPermittedIdentifiers` (apple/project.yml).
    nonisolated static let identifier = "persona.recording.day-catchup"
    /// The earliest a submitted request may start: chunks keep arriving while the user is about, and the receiver asks at
    /// most this often anyway.
    nonisolated static let delay: TimeInterval = 15 * 60

    private static var registered = false

    /// From `application(_:didFinishLaunchingWithOptions:)`, once: the system ends an app that registers twice, and
    /// accepts a registration only before launching finishes.
    static func register() {
        guard !registered else { return }
        registered = true
        _ = BGTaskScheduler.shared.register(forTaskWithIdentifier: identifier, using: .main) { @Sendable task in
            // Called on the main queue (`using: .main`), which owns the task from here on.
            nonisolated(unsafe) let task = task
            MainActor.assumeIsolated { run(task) }
        }
    }

    /// Submits (or replaces) the request. Fails quietly where the system has no background tasks (the simulator).
    /// Callable from any thread (the task's expiration handler).
    nonisolated static func schedule() {
        let request = BGProcessingTaskRequest(identifier: identifier)
        request.requiresExternalPower = true
        request.requiresNetworkConnectivity = false
        request.earliestBeginDate = Date(timeIntervalSinceNow: delay)
        try? BGTaskScheduler.shared.submit(request)
    }

    /// Going to the background: submitted while a day recording from the last week is on the phone (its audio, text or
    /// upload may still need the task); nothing for anyone who never used all-day recording.
    static func scheduleIfNeeded(library: RecordingLibrary = .shared) {
        let since = Date.now.addingTimeInterval(-(RecordingDayContract.audioMaxDays + 1) * RecordingDayRetention.day)
        guard library.entries.contains(where: { $0.isDay && $0.createdAt >= since }) else { return }
        schedule()
    }

    /// The task's work: transcription first (the upload sends its text), then the upload, then retention. True when work
    /// is left for another run. Also the DEBUG `-openDayCatchUp YES` launch, where the simulator never runs the task.
    static func catchUp() async -> Bool {
        await RecordingDayTranscriber.shared.drain()
        guard !Task.isCancelled else { return true }
        let unsent = await RecordingDaySync.shared.drain()
        guard !Task.isCancelled else { return true }
        await RecordingDayKeeper.shared.run(force: true)
        return unsent || RecordingDayTranscriber.shared.hasWork
    }

    private static func run(_ task: BGTask) {
        let work = Task { await catchUp() }
        let completion = Completion(task)
        // Time is up (the user picked up the phone, or the system needs it back): stop; nothing done so far is lost, and
        // an interrupted run counts no try against any chunk. The task is completed here and now, not when the work has
        // wound down: an await that is slow to notice the cancellation must not keep it open past its deadline (the
        // system would end the app, and give the catch-up fewer chances). The work's own completion is a no-op then.
        task.expirationHandler = { @Sendable in
            work.cancel()
            schedule()
            completion.complete(success: false)
        }
        Task {
            let remains = await work.value
            let finished = !work.isCancelled
            if remains || !finished { schedule() }
            completion.complete(success: finished)
        }
    }

    /// Completes a background task once: from its work, or from its expiration handler, whichever comes first.
    private nonisolated final class Completion: Sendable {
        nonisolated(unsafe) private let task: BGTask
        private let done = OSAllocatedUnfairLock(initialState: false)

        init(_ task: BGTask) { self.task = task }

        func complete(success: Bool) {
            let first = done.withLock { (done: inout Bool) -> Bool in
                defer { done = true }
                return !done
            }
            guard first else { return }
            task.setTaskCompleted(success: success)
        }
    }
}
#endif
