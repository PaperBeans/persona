import Foundation
import WatchKit

/// The watch app's one background refresh request. watchOS keeps a single request per app, and scheduling another
/// cancels the one before (WKBackgroundTask.h), so the Milk Frog's half-hourly refresh (docs/apple/persona.md) and
/// all-day recording's close after midnight (docs/assistant/day-recap.md) share it: each job says when it next wants the
/// app woken, and only the earliest is requested, under that job's identifier.
///
/// A wake for the frog also closes a day left open (cheap when there is none), so the close is never lost behind the
/// frog's more frequent request. A wake for the close refreshes the frog too when its time has come, or when the frog
/// hasn't said yet in this run of the app (a cold launch): otherwise its half-hourly chain would end there.
@MainActor
enum WatchBackgroundRefresh {
    /// What a wake is for. The raw value is the request's user info, which SwiftUI matches to the
    /// `.backgroundTask(.appRefresh(_:))` handler of the same identifier.
    enum Job: String {
        case pet
        case allDay = "allday"
    }

    private static var wanted: [Job: Date] = [:]
    /// The jobs that said what they want since launch.
    private static var told: Set<Job> = []
    private static var requested: (job: Job, date: Date)?

    /// `job` wants the app woken at `date`, or no longer (nil). Requests the earliest wake still wanted, unless that is
    /// the one already requested.
    static func want(_ job: Job, at date: Date?) {
        told.insert(job)
        guard wanted[job] != date else { return }
        wanted[job] = date
        request()
    }

    /// A refresh woke the app for `job`. Its request is used up, so the next wake still wanted by a job not running now
    /// is requested first; then the jobs run, each asking for its own next wake.
    static func run(_ job: Job, model: WatchModel) async {
        let frog = job == .pet || dueOrUnknown(.pet)
        requested = nil
        wanted[job] = nil
        if frog { wanted[.pet] = nil }
        request()
        if frog { await model.backgroundRefresh() }
        await WatchAllDay.shared.closeStaleDays()
    }

    private static func dueOrUnknown(_ job: Job) -> Bool {
        guard told.contains(job) else { return true }
        return wanted[job].map { $0 <= .now } ?? false
    }

    private static func request() {
        guard let next = wanted.min(by: { $0.value < $1.value }) else { return }
        if let requested, requested.job == next.key, requested.date == next.value { return }
        requested = (next.key, next.value)
        // The completion may come on another queue.
        WKApplication.shared().scheduleBackgroundRefresh(withPreferredDate: next.value,
                                                         userInfo: next.key.rawValue as NSString) { @Sendable _ in }
    }
}
