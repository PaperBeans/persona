import Accessibility
import AVFoundation
import Foundation
import Observation
import WatchConnectivity
import WatchKit
import WidgetKit

/// All-day recording on the watch (docs/assistant/day-recap.md): the one place that starts, continues, pauses and
/// stops it for the home card, the Record page, its own page, the complication, notifications and the phone's switch,
/// and that tells the user when it pauses or stops by itself.
///
/// The capture (`RecordingCapture`, day mode) records and says what happened through its day hooks; this decides what
/// that means for the wrist, Notification Center, the face and the phone (`AllDayAlertPolicy`) and carries it out.
/// Only the app in front can open the microphone, so whatever asks from outside (a link, a notification, the phone)
/// waits here until the app is in front. Whether it may start at all is the iPhone's to say: the user agrees to its
/// consent sheet there (`recordingDay`, docs/assistant/day-recap.md「Watch and phone capabilities」), and until then the
/// watch explains and points to the phone (`AllDayAlertPolicy.access`).
@MainActor @Observable
final class WatchAllDay {
    static let shared = WatchAllDay()
    /// The longest a WatchConnectivity wake waits for the phone's delivery to be handled: our own cap, not a promise of
    /// the system's.
    private static let connectivityWait: TimeInterval = 20
    /// The longest the midnight close waits for WatchConnectivity to activate on a cold launch before handing off.
    private static let activationWait: TimeInterval = 10
    private static let sessionPoll: Duration = .milliseconds(250)
    /// The longest the app holds off suspension to get a notice and the face out.
    private static let awakeLimit: TimeInterval = 10
    private static let feedbackSeconds = 4.0

    /// A line confirming the tap just made (and read out by VoiceOver).
    enum Feedback: Equatable {
        case meetingMarked, bookmarked, erased
        /// The erase couldn't be saved: nothing was erased.
        case notErased
        /// Another recording holds the microphone.
        case busy

        var text: String {
            switch self {
            case .meetingMarked: String(localized: "Meeting marked")
            case .bookmarked: String(localized: "Bookmark added")
            case .erased: String(localized: "Erased the last 10 minutes")
            case .notErased: String(localized: "The last 10 minutes weren't erased. Try again.")
            case .busy: String(localized: "Stop the other recording first.")
            }
        }

        var symbol: String {
            switch self {
            case .meetingMarked: Theme.Icon.meetingMark
            case .bookmarked: Theme.Icon.pin
            case .erased: Theme.Icon.delete
            case .notErased, .busy: Theme.Icon.error
            }
        }

        /// Something didn't happen: the failure haptic and the caution color.
        var failed: Bool { self == .notErased || self == .busy }
    }

    /// When the state shown began: a recording's time counts from here.
    private(set) var since: Date?
    private(set) var feedback: Feedback?
    /// Something outside the page asked for it (a link, a notification): the home list presents it.
    var pageRequested = false
    /// The phone deleted the recording this watch was making, and nothing started since: the page says so.
    private(set) var deletedOnPhone = false
    /// Notifications are off for the app (the user said no): a pause, a stop or the phone's start won't reach the
    /// wrist while the screen is off, so the page says so. Learned when the page or the card asks (`askForNotifications`).
    private(set) var notificationsOff = false
    /// The user wants all-day recording on (`RecordingCapture.allDayIntentKey`, kept by the capture so it outlives the
    /// app): on from a start until Stop for Today. Mirrored here because UserDefaults isn't observed, so the card and
    /// the page redraw when it goes (`refreshWish`).
    private var wish: Bool

    @ObservationIgnored private let capture = RecordingCapture.shared
    @ObservationIgnored private let notices = WatchAllDayNotices()
    @ObservationIgnored private let face = AllDayFacePublisher()
    /// The state the policy last acted on: a hook and the capture's own change can report the same move.
    @ObservationIgnored private var published: AllDaySnapshot.State = .off
    @ObservationIgnored private var installed = false
    @ObservationIgnored private var pending: Ask?
    @ObservationIgnored private var startTask: Task<Void, Never>?
    @ObservationIgnored private var feedbackTask: Task<Void, Never>?
    @ObservationIgnored private var deadManArmedAt: Date?
    @ObservationIgnored private var heartbeatAt: Date?
    /// What the face last said a tap may do while nothing records: a change of the phone's `recordingDay` rewrites it.
    @ObservationIgnored private var offered: AllDaySnapshot.Access?
    @ObservationIgnored private var defaultsObserver: NSObjectProtocol?
    /// The user's stop is under way (`stopCapture`).
    @ObservationIgnored private var stopping = false
    #if DEBUG
    @ObservationIgnored private var sampleIntent: Bool?
    @ObservationIgnored private var sampleShown = false
    private var sampleWaiting: Int?
    /// The phone's `recordingDay` a sample shows, over the stored one.
    private var samplePhoneDay: Int?
    #endif

    /// What something outside the page asked for, and whether it was the phone (which speaks for its consent).
    private struct Ask {
        let action: AllDayLink.Action
        let byPhone: Bool
    }

    private init() {
        wish = UserDefaults.standard.bool(forKey: RecordingCapture.allDayIntentKey)
    }

    /// What all-day recording is doing now, from the capture and whether the user still wants it on: a capture that
    /// stopped by itself leaves that wish, so it reads as stopped (a tap continues it) rather than off.
    var state: AllDaySnapshot.State {
        if capture.allDay {
            switch capture.state {
            case .recording: return .recording
            case .paused: return capture.interrupted ? .interrupted : .paused
            case .idle, .starting: break
            }
        }
        return wish ? .stopped : .off
    }

    /// A day recording is still open with the microphone off (after Stop for Today, or a stop by itself): its last
    /// minutes can still be erased. Reads the library: for the page, not for anything always on screen.
    var dayOpen: Bool { !RecordingLibrary.shared.openDays(source: .watch).isEmpty }

    /// What the watch offers now. `phoneDay`: the stored `recordingDay` (`WatchSync.phoneDayKey`), which the views
    /// read through `@AppStorage` so they redraw when the phone changes it (UserDefaults isn't observed here).
    func access(phoneDay: Int?) -> AllDayAlertPolicy.Access {
        AllDayAlertPolicy.access(phoneDay: shown(phoneDay: phoneDay), state: state)
    }

    private func shown(phoneDay: Int?) -> Int? {
        #if DEBUG
        if let samplePhoneDay { return samplePhoneDay }
        #endif
        return phoneDay
    }

    /// The phone's `recordingDay` as last stored, read when a start is asked for.
    private static var storedPhoneDay: Int? { UserDefaults.standard.object(forKey: WatchSync.phoneDayKey) as? Int }

    /// UserDefaults Data: the last gap said (`AllDayAlertPolicy.Gap`), so a later launch that finds the same day still
    /// open doesn't say it again. Written once per halt.
    private static let reportedGapKey = "recording.allDay.reportedGap"

    private static var reportedGap: AllDayAlertPolicy.Gap? {
        get {
            UserDefaults.standard.data(forKey: reportedGapKey).flatMap { try? JSONDecoder().decode(AllDayAlertPolicy.Gap.self, from: $0) }
        }
        set {
            guard newValue != reportedGap else { return }
            UserDefaults.standard.set(newValue.flatMap { try? JSONEncoder().encode($0) }, forKey: reportedGapKey)
        }
    }

    /// Chunks the iPhone hasn't acknowledged yet.
    var waiting: Int {
        #if DEBUG
        if let sampleWaiting { return sampleWaiting }
        #endif
        return WatchDayTransfer.shared.pending
    }

    private static var inFront: Bool { WKApplication.shared().applicationState == .active }

    // MARK: - Launch

    /// The launch bootstrap, run before WatchConnectivity activates so a command it delivers finds its handler: takes
    /// the capture's day hooks and the phone's commands, then reports what the last run left behind (a day the app
    /// ended in). Idempotent.
    func install() {
        guard !installed else { return }
        installed = true
        // Chained onto whatever was there, never replacing it.
        let halted = capture.onDayHalted, resumed = capture.onDayResumed, saved = capture.onDayChunk
        let deleted = capture.onDayDeletedRemotely
        capture.onDayHalted = { [weak self] reason in
            halted?(reason)
            self?.halted(reason)
        }
        capture.onDayResumed = { [weak self] in
            resumed?()
            self?.enterRecording()
        }
        capture.onDayChunk = { [weak self] id in
            saved?(id)
            self?.chunkSaved()
        }
        capture.onDayDeletedRemotely = { [weak self] id in
            deleted?(id)
            self?.deletedRemotely()
        }
        WatchSync.shared.onDayCommand = { [weak self] command in self?.phoneCommand(command) }
        WatchAllDayNotices.registerCategories()
        refreshWish()
        published = state
        observeCapture()
        observePhoneDay()
        #if DEBUG
        guard !sampleShown else { return }
        #endif
        // The face says what a tap may do now, whatever an earlier build or the phone's last word left there.
        refreshOffer(force: true)
        if let halt = capture.takeHaltedOnLaunch() { reportLaunchHalt(halt) }
        // Next turn: this runs while the app is still being set up (`WatchModel.init`).
        Task { scheduleMidnightClose() }
    }

    /// The app ended while the user wanted all-day recording on. It says so only for an exit this launch found while
    /// the day was recording, which nothing said yet (the dead-man's switch may have, minutes ago): every later launch
    /// finds the same day open until Continue, Stop for Today or midnight, and a stop the system made while the app ran
    /// had its own notice (`AllDayAlertPolicy.alertsOnLaunch`). The face and the phone follow either way.
    private func reportLaunchHalt(_ halt: (id: UUID, reason: RecordingDayHalt)) {
        let entry = RecordingLibrary.shared.entry(halt.id)
        let gap = entry.map(AllDayAlertPolicy.gap(of:)) ?? AllDayAlertPolicy.Gap(recording: halt.id, event: nil)
        let reported = Self.reportedGap
        Task {
            // The dead-man's switch spoke when its notice is out, or it no longer waits (it fired and was dismissed).
            var fired = await notices.isDelivered(AllDayNotice.stoppedRequest)
            if !fired { fired = await !notices.isPending(AllDayNotice.stoppedRequest) }
            let alerts = AllDayAlertPolicy.alertsOnLaunch(gapReason: entry?.gapReason, gap: gap, reported: reported,
                                                          alreadyAlerted: fired)
            Self.reportedGap = gap
            refreshWish()
            handle(.halted(halt.reason, now: state), alerts: alerts)
        }
    }

    /// The capture paused or stopped by itself: the wrist, the notice and the face follow the state it is really in.
    /// Its gap is said now, so a launch that finds the day still open later doesn't say it again; only once its event
    /// is saved, though (`AllDayAlertPolicy.haltMark`).
    private func halted(_ reason: RecordingDayHalt) {
        refreshWish()
        handle(.halted(reason, now: state))
        let library = RecordingLibrary.shared
        // A stop has let the recording go: the newest day still open is the one it left.
        let day = capture.dayRecordingID.flatMap { library.entry($0) }
            ?? library.openDays(source: .watch).max { $0.createdAt < $1.createdAt }
        if let mark = day.flatMap(AllDayAlertPolicy.haltMark(of:)) { Self.reportedGap = mark }
    }

    /// The phone's `recordingDay` arrives in the defaults (`WatchPhoneCapabilities.store`), which nothing observes: the
    /// face follows it here, so it never offers a start the page would refuse, nor hides one it would take.
    private func observePhoneDay() {
        defaultsObserver = NotificationCenter.default.addObserver(forName: UserDefaults.didChangeNotification, object: nil,
                                                                  queue: .main) { @Sendable [weak self] _ in
            MainActor.assumeIsolated { self?.refreshOffer(force: false) }
        }
    }

    /// Rewrites the face when what a tap may do changed (`force`: at launch, compared with what is on file instead).
    /// A move not published yet is left to `captureChanged`, which writes the offer with the new state; a running
    /// recording's offer doesn't change with the phone's word.
    private func refreshOffer(force: Bool) {
        #if DEBUG
        // A sample's made-up state never reaches the real face.
        guard !sampleShown else { return }
        #endif
        let access = access(phoneDay: Self.storedPhoneDay)
        guard force || access != offered, published == state else { return }
        publishFace(published, group: DispatchGroup(), onlyIfRedrawn: true)
    }

    // MARK: - What the user does

    /// Starts or continues all-day recording, as a tap on All-Day Recording or Continue does. False when it may not
    /// start yet (the user hasn't agreed on the iPhone): the page says so instead. `byPhone`: the phone asked for this
    /// start, which stands for its consent (`AllDayAlertPolicy.mayStart`).
    @discardableResult func requestStart(byPhone: Bool = false) -> Bool {
        guard AllDayAlertPolicy.mayStart(access(phoneDay: Self.storedPhoneDay), askedByPhone: byPhone) else { return false }
        bringToRecording()
        return true
    }

    func pause() {
        guard state == .recording else { return }
        capture.togglePause()
        handle(.pausedByUser)
    }

    /// Stop for Today: the microphone goes off and stays off until the user starts again. The day stays open (a start
    /// later today continues it) and closes at midnight. From the phone's switch it also takes back what its start
    /// asked for: a start of ours still under way, or its notice.
    func stopForToday() {
        let was = state
        // A start of ours not recording yet goes too: asking for notifications (it checks once the answer comes), or
        // waiting for the microphone.
        let underWay = startTask != nil
        if underWay {
            startTask?.cancel()
            if capture.state == .starting { capture.cancelStarting() }
        }
        let event = AllDayAlertPolicy.stopEvent(state: was, startUnderWay: underWay)
        switch was {
        case .off:
            guard event == .stoppedByUser else {
                handle(event)
                return
            }
        case .recording, .paused, .interrupted:
            stopCapture()
        case .stopped:
            // Nothing records: only the wish to record goes (a user stop with nothing running clears it).
            capture.stop(reason: .user)
            #if DEBUG
            if sampleIntent != nil { sampleIntent = false }
            #endif
        }
        refreshWish()
        handle(.stoppedByUser)
    }

    /// The capture saves the last chunk inside its stop, while it still reads as recording: that chunk must not renew
    /// the dead-man's switch or the heartbeat that `stoppedByUser` ends a moment later (an add and a removal of the same
    /// notification request, which the system doesn't promise to carry out in that order).
    private func stopCapture() {
        stopping = true
        defer { stopping = false }
        capture.stop(reason: .user)
    }

    /// A meeting starts now: a mark in the day (one microphone, so not a second recording).
    func markMeeting() {
        guard capture.allDay else { return }
        capture.markMeeting()
        show(.meetingMarked)
    }

    func addBookmark() {
        guard capture.allDay else { return }
        capture.addMark()
        show(.bookmarked)
    }

    /// Erases the last 10 minutes, also once the microphone is off while a day is still open (`dayOpen`): what was
    /// just said can be taken back after a stop too. The page asks first; this confirms only an erase that was saved,
    /// since one that wasn't deletes nothing and the audio still goes to the phone.
    func eraseRecent() {
        guard capture.allDay || capture.state == .idle else { return }
        if capture.eraseRecent() { show(.erased) } else { show(.notErased) }
    }

    private func bringToRecording() {
        // A start answers the deletion's line: what is recorded now goes into a new recording.
        if deletedOnPhone { deletedOnPhone = false }
        switch state {
        case .recording: break
        case .paused: capture.togglePause()
        case .interrupted: capture.resumeAfterInterruption()
        case .off, .stopped: startCapture()
        }
    }

    /// A new capture in day mode: it continues today's open day if there is one.
    private func startCapture() {
        guard startTask == nil else { return }
        guard capture.state == .idle else { return show(.busy) }
        startTask = Task {
            let off = await notices.askOnce()
            if notificationsOff != off { notificationsOff = off }
            // The phone's switch turned off (or Stop for Today) while the permission prompt was up: no microphone.
            guard !Task.isCancelled else {
                startTask = nil
                return
            }
            await capture.start(locale: WatchRecordingView.defaultLocale, day: true)
            startTask = nil
            refreshWish()
            if state == .recording {
                enterRecording()
            } else if capture.error != nil, Self.inFront {
                // The capture's error line says why. A start the user cancelled says nothing.
                WKInterfaceDevice.current().play(.failure)
            }
        }
    }

    /// Reads the wish back from the capture's key; the capture writes it on a start and on the user's Stop.
    private func refreshWish() {
        #if DEBUG
        if let sampleIntent {
            if wish != sampleIntent { wish = sampleIntent }
            return
        }
        #endif
        let now = UserDefaults.standard.bool(forKey: RecordingCapture.allDayIntentKey)
        if wish != now { wish = now }
    }

    private func show(_ next: Feedback) {
        feedback = next
        if Self.inFront { WKInterfaceDevice.current().play(next.failed ? .failure : .success) }
        AccessibilityNotification.Announcement(next.text).post()
        feedbackTask?.cancel()
        feedbackTask = Task {
            try? await Task.sleep(for: .seconds(Self.feedbackSeconds))
            guard !Task.isCancelled else { return }
            feedback = nil
        }
    }

    // MARK: - From outside

    /// A link (the complication) or a notification's answer (`WatchAppDelegate`): runs once the app is in front, and
    /// shows the page. `byPhone`: the answer carries out the phone's request (`AllDayNotice.askedByPhone`).
    func open(_ action: AllDayLink.Action, byPhone: Bool = false) {
        pending = Ask(action: action, byPhone: byPhone)
        runPending()
    }

    /// The app came to the front: the microphone can open now. A recording a call paused picks up again (any
    /// recording, as on the iPhone), unless the call's audio still holds the session; then what a link or a
    /// notification asked for runs.
    func becameActive() {
        if pending == nil, capture.interrupted, capture.state == .paused, !AVAudioSession.sharedInstance().isOtherAudioPlaying {
            capture.resumeAfterInterruption()
        }
        runPending()
        // Back from Settings: the page's line about notifications being off goes once they are on.
        if notificationsOff {
            Task {
                let off = await notices.askOnce()
                if notificationsOff != off { notificationsOff = off }
            }
        }
    }

    /// The page or the home card is showing All-Day Recording in front: ask for notifications once when it is offered
    /// (`AllDayAlertPolicy.asksForNotifications`), so the phone's start and a pause can reach the wrist before the first
    /// start on the watch, and learn whether the user turned them off.
    func askForNotifications(phoneDay: Int?, onPage: Bool) async {
        guard Self.inFront, AllDayAlertPolicy.asksForNotifications(access(phoneDay: phoneDay), onPage: onPage) else { return }
        #if DEBUG
        // Screenshots (`-showAllDaySample`): the system's prompt would cover the page.
        if UserDefaults.standard.string(forKey: "showAllDaySample") != nil { return }
        #endif
        let off = await notices.askOnce()
        if notificationsOff != off { notificationsOff = off }
    }

    private func runPending() {
        guard let ask = pending, Self.inFront else { return }
        pending = nil
        pageRequested = true
        guard ask.action != .open else { return }
        // A start that may not run yet leaves the page to say why (agree on the iPhone first).
        requestStart(byPhone: ask.byPhone)
    }

    /// The phone's All-Day Recording switch (`WatchSync.onDayCommand`, newest commands only).
    private func phoneCommand(_ command: String) {
        switch command {
        case RecordingDayContract.Command.start:
            guard state != .recording else { return }
            // `WatchSync` acted on it just now, so the newest stamp is this command's: its notice keeps it.
            let sentAt = UserDefaults.standard.double(forKey: WatchDayCommandOrder.lastKey)
            if handle(.phoneAskedToStart(sentAt: sentAt)).startsNow { open(.start, byPhone: true) }
        case RecordingDayContract.Command.stop:
            stopForToday()
        default:
            break
        }
    }

    /// WatchConnectivity woke (or launched) the app to deliver what the phone sent while it was suspended: with
    /// the handlers in place, wait until the session has handed everything over and the notices it led to are posted,
    /// at most `connectivityWait`. Never opens the microphone: a start waits for the user's tap on its notice.
    func receiveConnectivity() async {
        install()
        let deadline = Date.now.addingTimeInterval(Self.connectivityWait)
        guard await sessionActivated(by: deadline) else { return }
        let session = WCSession.default
        var settled = 0
        while Date.now < deadline, !Task.isCancelled {
            let idle = !session.hasContentPending && notices.inFlight == 0
            // Twice in a row: the main-actor work the last delivery queued has had its turn.
            settled = idle ? settled + 1 : 0
            if settled >= 2 { return }
            try? await Task.sleep(for: Self.sessionPoll)
        }
    }

    /// A background refresh (`WatchBackgroundRefresh`; the one after midnight, or the frog's): a day recording left
    /// open while nothing recorded gets its `closed` event, so the phone can finish it and the hub write its final
    /// recap without the user opening the app. The close hands its event off to the system at once, which needs an
    /// activated session: a cold launch has only just asked for one. A session that doesn't activate in time leaves the
    /// day open, and the wake is asked for again (`AllDayAlertPolicy.closeRefreshDate`), rather than closing it into
    /// a `closed` event nothing could hand to the system.
    func closeStaleDays() async {
        install()
        var retrying = false
        // A pause can span several midnights: the next close is asked for whatever happens here.
        defer { scheduleMidnightClose(retrying: retrying) }
        guard !RecordingLibrary.shared.openDays(source: .watch).isEmpty else { return }
        guard await sessionActivated(by: .now.addingTimeInterval(Self.activationWait)) else {
            retrying = true
            return
        }
        WatchDayTransfer.shared.closeStaleDays()
        WatchDayTransfer.shared.publishState()
    }

    /// While a day recording is open and nothing records, wake just after its midnight (in the recording's own zone)
    /// to close it; otherwise no wake of ours. watchOS decides when exactly, and the request is shared with the frog's.
    private func scheduleMidnightClose(retrying: Bool = false) {
        let open = state == .recording ? [] : RecordingLibrary.shared.openDays(source: .watch)
        // Every day entry keeps its zone; the watch's own is only the fallback for one whose zone is unreadable.
        let days = open.compactMap { entry in
            entry.day.map { AllDayAlertPolicy.OpenDay(day: $0, zone: entry.dayTimeZone ?? .autoupdatingCurrent) }
        }
        WatchBackgroundRefresh.want(.allDay, at: AllDayAlertPolicy.closeRefreshDate(openDays: days, now: .now, retrying: retrying))
    }

    /// Waits until WatchConnectivity has activated (`WatchModel` asks for it at launch), at most until `deadline`.
    private func sessionActivated(by deadline: Date) async -> Bool {
        guard WCSession.isSupported() else { return false }
        let session = WCSession.default
        while session.activationState != .activated, Date.now < deadline, !Task.isCancelled {
            try? await Task.sleep(for: Self.sessionPoll)
        }
        return session.activationState == .activated
    }

    // MARK: - What the capture reports

    private func enterRecording() {
        guard published != .recording else { return }
        handle(published == .off ? .started : .resumed)
    }

    /// The phone deleted the day recording being made (docs/assistant/day-recap.md「Deleting」); the capture has stopped
    /// it, deleted its chunks and ended the wish to record with it. All-Day Recording is off now rather than stopped,
    /// since nothing failed and there is nothing to continue: a start later today opens a new recording. The watch says
    /// so, since the delete came from elsewhere and the microphone went off without a tap here.
    private func deletedRemotely() {
        refreshWish()
        deletedOnPhone = true
        handle(.deletedOnPhone)
    }

    private func chunkSaved() {
        let recording = state == .recording && !stopping
        // Recording with no start reported yet: that start comes first, with its alerts.
        if recording { enterRecording() }
        let now = Date.now
        handle(.chunkSaved(deadManLeft: deadManArmedAt.map { AllDayAlertPolicy.deadManSeconds - now.timeIntervalSince($0) },
                           sinceHeartbeat: heartbeatAt.map { now.timeIntervalSince($0) },
                           recording: recording))
    }

    /// A backstop for a move no hook reported: the face and the phone follow the capture whatever moved it. A start or
    /// resume gets the whole treatment; a halt without its reason can't say why, so the dead-man's switch stays armed
    /// and speaks if it isn't explained in time. It runs on a later main-actor turn: the capture's pause and stop are
    /// synchronous, so `pause()` and `stopForToday()` (and the hooks) have published their move by then and it finds
    /// nothing left to do.
    private func observeCapture() {
        withObservationTracking {
            _ = state
        } onChange: { [weak self] in
            Task { @MainActor in self?.captureChanged() }
        }
    }

    private func captureChanged() {
        observeCapture()
        // A start writes the wish and a Stop clears it, with the capture's own change.
        refreshWish()
        let now = state
        guard now != published else { return }
        if now == .recording { return enterRecording() }
        let quiet = now == .interrupted || now == .stopped ? AllDayAlertPolicy.DeadMan.keep : .disarm
        // Out of a recording the app may be suspended next, whatever moved it: the waiting chunks go now, as on a pause.
        carryOut(AllDayAlertPolicy.Output(snapshot: now, deadMan: quiet, handOff: published == .recording))
    }

    // MARK: - Carrying out the policy

    /// `alerts` false: the face and the phone follow; the wrist and Notification Center stay as they are (a notice
    /// already there stays too).
    @discardableResult private func handle(_ event: AllDayAlertPolicy.Event, alerts: Bool = true) -> AllDayAlertPolicy.Output {
        var out = AllDayAlertPolicy.decide(event, appActive: Self.inFront)
        if !alerts {
            out.notify = nil
            out.haptic = nil
            out.withdraw = false
        }
        carryOut(out)
        return out
    }

    private func carryOut(_ out: AllDayAlertPolicy.Output) {
        let changed = out.snapshot.map { $0 != published } ?? false
        if changed, let next = out.snapshot {
            published = next
            since = .now
        }
        if out.handOff || out.notify != nil {
            whileAwake { finish in apply(out, changed: changed, done: finish) }
        } else {
            apply(out, changed: changed, done: nil)
        }
    }

    /// Withdrawals and disarming go first, so a notice or a dead-man's switch posted next is not taken back with them.
    private func apply(_ out: AllDayAlertPolicy.Output, changed: Bool, done: (@Sendable () -> Void)?) {
        let group = DispatchGroup()
        if let haptic = out.haptic { WKInterfaceDevice.current().play(haptic.watch) }
        if out.withdraw { notices.withdraw() }
        if out.withdrawStart { notices.withdrawStart() }
        switch out.deadMan {
        case .arm:
            notices.armDeadMan(group: group)
            deadManArmedAt = .now
        case .disarm:
            notices.disarmDeadMan()
            deadManArmedAt = nil
        case .keep:
            break
        }
        if let notice = out.notify {
            notices.post(notice, group: group)
            // A stopped notice took the dead-man's request id.
            if case .stopped = notice { deadManArmedAt = nil }
        }
        if out.handOff { WatchDayTransfer.shared.handOff() }
        if let state = out.snapshot { publishFace(state, group: group) }
        if changed {
            WatchDayTransfer.shared.publishState()
            scheduleMidnightClose()
        }
        if let done { group.notify(queue: .main, execute: done) }
    }

    /// The complication's snapshot, written now: a change of state, or the recording's heartbeat (`since` stays), with
    /// what a tap may do (`onlyIfRedrawn`: only when that or the state differs from what is on file).
    private func publishFace(_ state: AllDaySnapshot.State, group: DispatchGroup, onlyIfRedrawn: Bool = false) {
        let now = Date.now
        heartbeatAt = now
        let access = AllDayAlertPolicy.access(phoneDay: shown(phoneDay: Self.storedPhoneDay), state: state)
        offered = access
        let day = capture.dayRecordingID.flatMap { RecordingLibrary.shared.entry($0)?.day }
        let snapshot = AllDaySnapshot(state: state, since: since, updatedAt: now, day: day, pending: waiting, access: access)
        group.enter()
        face.publish(snapshot, onlyIfRedrawn: onlyIfRedrawn) { group.leave() }
    }

    /// Runs `work` under a short task assertion (not a keep-alive): after a pause, a stop or an interruption watchOS
    /// suspends the app soon, and the notice, the face and the hand-off must be out by then. It ends when `work` calls
    /// `finish`, after `awakeLimit`, or when the system says the time is up.
    private func whileAwake(_ work: (_ finish: @escaping @Sendable () -> Void) -> Void) {
        let done = DispatchSemaphore(value: 0)
        let limit = Self.awakeLimit
        ProcessInfo.processInfo.performExpiringActivity(withReason: "All-day recording state change") { @Sendable expired in
            if expired {
                done.signal()
                return
            }
            _ = done.wait(timeout: .now() + limit)
        }
        work { done.signal() }
    }

    #if DEBUG
    /// `-showAllDaySample` (`WatchAllDaySamples`): a made-up recording's start, its wish to record, the chunks it shows
    /// waiting and the phone's `recordingDay`, with no microphone; `deleted`: the phone just deleted it.
    func showSample(_ state: AllDaySnapshot.State, since: Date, waiting: Int, phoneDay: Int, deleted: Bool = false) {
        sampleShown = true
        sampleIntent = state == .stopped
        refreshWish()
        self.since = since
        sampleWaiting = waiting
        samplePhoneDay = phoneDay
        deletedOnPhone = deleted
    }
    #endif
}

private extension AllDayAlertPolicy.Haptic {
    var watch: WKHapticType {
        switch self {
        case .start: .start
        case .stop: .stop
        case .failure: .failure
        case .notification: .notification
        }
    }
}

/// Writes the complication's snapshot off the main thread, one write at a time in the order asked, so a change of
/// state always lands after a heartbeat asked for before it; then reloads only the all-day face's timeline.
private final class AllDayFacePublisher {
    private let queue = DispatchQueue(label: "persona.allday.face", qos: .utility)

    /// `onlyIfRedrawn`: skips the write and the reload when the face on file already draws the same
    /// (`AllDaySnapshot.redraws`), read on this queue, never on the main thread.
    func publish(_ snapshot: AllDaySnapshot, onlyIfRedrawn: Bool = false, done: @escaping @Sendable () -> Void) {
        queue.async { @Sendable in
            if onlyIfRedrawn, !snapshot.redraws(over: AllDaySnapshot.read()) {
                done()
                return
            }
            if snapshot.write() {
                DispatchQueue.main.async { MainActor.assumeIsolated { WidgetCenter.shared.reloadTimelines(ofKind: AllDaySnapshot.widgetKind) } }
            }
            done()
        }
    }
}
