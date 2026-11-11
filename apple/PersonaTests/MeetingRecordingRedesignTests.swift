import Foundation
import Testing
@testable import Persona

/// The pure layer under the meeting recording page (#4240, docs/design/proposals/meeting-recording.md sections 5 and 9):
/// the phase table, the wrap-up's steps, the timeline's order and ids, following and unread, and the level meter's pose.
struct RecordingPhaseTests {
    private func held(meeting: Bool = true, active: Bool = false, receivedAll: Bool = true, noAudio: Bool = false,
                       transcribedAll: Bool = true, transcribing: Bool = false, error: String? = nil,
                       sync: RecordingPhase.Sync = .synced, hub: Bool = true, minutes: RecordingPhase.Minutes? = nil) -> RecordingPhase.Held {
        RecordingPhase.Held(isMeeting: meeting, active: active, receivedAll: receivedAll, noAudio: noAudio, transcribedAll: transcribedAll,
                            transcribing: transcribing, transcriptionError: error, sync: sync, hubAvailable: hub, minutes: minutes,
                            syncRejectedReason: "Refused", minutesFailedReason: "No minutes")
    }

    private func phase(_ held: RecordingPhase.Held? = nil, capture: RecordingPhase.Capture = .idle, requested: Bool = false,
                       interrupted: Bool = false, error: String? = nil) -> RecordingPhase {
        RecordingPhase.derive(.init(startRequested: requested, capture: capture, interrupted: interrupted, captureError: error, held: held))
    }

    // MARK: The table, one case per row

    @Test func idleWithNothingHeld() { #expect(phase() == .idle) }

    @Test func preparingCoversTheCalendarReadAndTheStartingState() {
        #expect(phase(requested: true) == .preparing(cancellable: false))
        #expect(phase(capture: .starting) == .preparing(cancellable: true))
        #expect(phase(capture: .starting, requested: true) == .preparing(cancellable: true))
    }

    @Test func recordingAndPaused() {
        #expect(phase(capture: .recording) == .recording)
        #expect(phase(capture: .paused) == .paused(byCall: false))
        #expect(phase(capture: .paused, interrupted: true) == .paused(byCall: true))
        // A live capture wins over a recording held from before and over a start flag.
        #expect(phase(held(), capture: .recording, requested: true) == .recording)
    }

    @Test func endingIsAWatchRecordingNotAllInYet() {
        #expect(phase(held(receivedAll: false)) == .ending)
        #expect(phase(held(active: true, receivedAll: false)) == .ending)
    }

    @Test func aRecordingWithNoAudioIsDoneAtOnce() {
        #expect(phase(held(receivedAll: false, noAudio: true, transcribedAll: false)) == .done(hasMinutes: false))
    }

    @Test func transcribingCurrentOrQueued() {
        #expect(phase(held(transcribedAll: false)) == .transcribing)
        #expect(phase(held(transcribedAll: false, transcribing: true)) == .transcribing)
        // The transcriber is on it again: an old error does not show.
        #expect(phase(held(transcribedAll: false, transcribing: true, error: "Old")) == .transcribing)
    }

    @Test func aTranscriptionErrorOffersResume() {
        #expect(phase(held(transcribedAll: false, error: "Speech model missing"))
                == .failed(.init(step: .transcribed, reason: "Speech model missing", action: .resumeTranscribing)))
    }

    @Test func syncingWhilePendingOrUploadingAndWaitingForTheHub() {
        #expect(phase(held(sync: .pending)) == .syncing(waitingForHub: false))
        #expect(phase(held(sync: .uploading)) == .syncing(waitingForHub: false))
        #expect(phase(held(sync: .pending, hub: false)) == .syncing(waitingForHub: true))
        #expect(RecordingPhase.syncing(waitingForHub: true).isSettled)
        #expect(!RecordingPhase.syncing(waitingForHub: false).isSettled)
    }

    @Test func aRejectedSyncShowsItsReasonAndNoAction() {
        #expect(phase(held(sync: .rejected)) == .failed(.init(step: .synced, reason: "Refused", action: nil)))
    }

    @Test func aNoteIsDoneOnceSyncedOrWhenThereIsNothingToSend() {
        #expect(phase(held(meeting: false, sync: .synced)) == .done(hasMinutes: false))
        #expect(phase(held(meeting: false, sync: .waiting)) == .done(hasMinutes: false))
        #expect(phase(held(meeting: false, sync: .expired)) == .done(hasMinutes: false))
    }

    @Test func aMeetingFollowsItsMinutes() {
        // Not asked yet: writing, so `done` never flashes before the first answer.
        #expect(phase(held(minutes: nil)) == .writingMinutes)
        #expect(phase(held(minutes: .writing)) == .writingMinutes)
        // The hub has not got the text: that is syncing (and with no hub, held up), not writing.
        #expect(phase(held(minutes: .waitingForSync)) == .syncing(waitingForHub: false))
        #expect(phase(held(hub: false, minutes: .waitingForSync)) == .syncing(waitingForHub: true))
        #expect(phase(held(minutes: .ready)) == .done(hasMinutes: true))
        #expect(phase(held(minutes: .unsupported)) == .done(hasMinutes: false))
        #expect(phase(held(minutes: .absent)) == .done(hasMinutes: false))
        #expect(phase(held(minutes: .failed)) == .failed(.init(step: .writing, reason: "No minutes", action: .tryAgainMinutes)))
    }

    @Test func aMeetingWithNothingToSendIsDoneWithoutMinutes() {
        #expect(phase(held(sync: .waiting, minutes: nil)) == .done(hasMinutes: false))
    }

    @Test func aCaptureErrorAfterTheEndIsAFailureOfSaving() {
        #expect(phase(held(), error: "Not enough storage") == .failed(.init(step: .saved, reason: "Not enough storage", action: nil)))
        // Not while capturing: the error then belongs to the start or the live view.
        #expect(phase(capture: .recording, error: "Not enough storage") == .recording)
        // An empty error says nothing.
        #expect(phase(held(minutes: .ready), error: "") == .done(hasMinutes: true))
    }

    @Test func whichPhasesShowWhere() {
        let live: [RecordingPhase] = [.recording, .paused(byCall: false), .paused(byCall: true)]
        let wrap: [RecordingPhase] = [.ending, .transcribing, .syncing(waitingForHub: false), .writingMinutes, .done(hasMinutes: true),
                                      .failed(.init(step: .saved, reason: "", action: nil))]
        #expect(live.allSatisfy { $0.isLive && !$0.isWrapUp })
        #expect(wrap.allSatisfy { !$0.isLive && $0.isWrapUp })
        #expect(!RecordingPhase.idle.isLive && !RecordingPhase.idle.isWrapUp)
        #expect(!RecordingPhase.preparing(cancellable: true).isLive && !RecordingPhase.preparing(cancellable: false).isWrapUp)
    }

    // MARK: The steps

    private func states(_ phase: RecordingPhase, meeting: Bool = true) -> [RecordingPhase.Step.State] {
        RecordingPhase.steps(for: phase, isMeeting: meeting).map(\.state)
    }

    @Test func stepsAdvanceInOrder() {
        #expect(states(.ending) == [.current, .waiting, .waiting, .waiting, .waiting])
        #expect(states(.transcribing) == [.done, .current, .waiting, .waiting, .waiting])
        #expect(states(.syncing(waitingForHub: false)) == [.done, .done, .current, .waiting, .waiting])
        #expect(states(.writingMinutes) == [.done, .done, .done, .current, .waiting])
        #expect(states(.done(hasMinutes: true)) == [.done, .done, .done, .done, .done])
    }

    @Test func aHeldUpSyncHasNoSpinnerAndTheRestStaysUnstarted() {
        #expect(states(.syncing(waitingForHub: true)) == [.done, .done, .waitingForHub, .waiting, .waiting])
    }

    @Test func aFailedStepNamesItsReasonAndEarlierOnesAreDone() {
        let failed = RecordingPhase.failed(.init(step: .writing, reason: "Why", action: .tryAgainMinutes))
        #expect(states(failed) == [.done, .done, .done, .failed("Why"), .waiting])
        let storage = RecordingPhase.failed(.init(step: .saved, reason: "Full", action: nil))
        #expect(states(storage).first == .failed("Full"))
    }

    @Test func aNoteHasNoMinutesSteps() {
        #expect(RecordingPhase.steps(for: .transcribing, isMeeting: false).map(\.kind) == [.saved, .transcribed, .synced])
        // A meeting that ends without minutes (an older hub) drops them too.
        #expect(RecordingPhase.steps(for: .done(hasMinutes: false), isMeeting: true).map(\.kind) == [.saved, .transcribed, .synced])
        #expect(states(.done(hasMinutes: false), meeting: true) == [.done, .done, .done])
    }

    @Test func stepIdsAreStable() {
        #expect(RecordingPhase.steps(for: .transcribing, isMeeting: true).map(\.id) == [0, 1, 2, 3, 4])
    }
}

struct RecordingTimelineTests {
    private func sentence(_ segment: Int, _ start: Double, _ text: String = "Line") -> RecordingTimeline.Sentence {
        .init(segment: segment, start: start, text: text)
    }

    private func ids(_ items: [RecordingTimeline.Item]) -> [RecordingTimeline.Item.ID] { items.map(\.id) }

    @Test func sentencesBookmarksAndAsksInterleaveByTime() {
        let ask = UUID()
        let items = RecordingTimeline.make(sentences: [sentence(0, 1), sentence(0, 20), sentence(1, 31)], marks: [10, 25],
                                           asks: [.init(id: ask, at: 22)], transcribedUntil: 60, now: 62)
        #expect(ids(items) == [.sentence(segment: 0, millis: 1000), .bookmark(0), .sentence(segment: 0, millis: 20_000),
                               .exchange(ask), .bookmark(1), .sentence(segment: 1, millis: 31_000)])
    }

    @Test func atTheSameTimeASentenceThenABookmarkThenAnAsk() {
        let ask = UUID()
        let items = RecordingTimeline.make(sentences: [sentence(0, 5)], marks: [5], asks: [.init(id: ask, at: 5)], transcribedUntil: 30, now: 31)
        #expect(ids(items) == [.sentence(segment: 0, millis: 5000), .bookmark(0), .exchange(ask)])
    }

    @Test func whatIsNewerThanTheTranscribedTextSitsBelowTheGapMarker() {
        let ask = UUID()
        let items = RecordingTimeline.make(sentences: [sentence(0, 2), sentence(0, 20)], marks: [15, 40], asks: [.init(id: ask, at: 50)],
                                           transcribedUntil: 30, now: 62)
        #expect(ids(items) == [.sentence(segment: 0, millis: 2000), .bookmark(0), .sentence(segment: 0, millis: 20_000), .gap,
                               .bookmark(1), .exchange(ask)])
        #expect(items.first { $0.id == .gap }?.at == 30)
    }

    @Test func noMarkerWhenTheSegmentIsAboutToLand() {
        let items = RecordingTimeline.make(sentences: [sentence(0, 2)], marks: [], asks: [], transcribedUntil: 30, now: 33)
        #expect(!ids(items).contains(.gap))
        #expect(RecordingTimeline.gapSeconds(now: 33, transcribedUntil: 30) == nil)
    }

    @Test func noMarkerBeforeTheFirstSentence() {
        let items = RecordingTimeline.make(sentences: [], marks: [4], asks: [], transcribedUntil: 0, now: 40)
        #expect(ids(items) == [.bookmark(0)])
    }

    @Test func theGapCountsInSteps() {
        #expect(RecordingTimeline.gapSeconds(now: 40, transcribedUntil: 30) == 10)
        #expect(RecordingTimeline.gapSeconds(now: 43.9, transcribedUntil: 30) == 10)
        #expect(RecordingTimeline.gapSeconds(now: 45, transcribedUntil: 30) == 15)
        #expect(RecordingTimeline.gapSeconds(now: 10, transcribedUntil: 30) == nil)
    }

    /// A chunk landing: its sentences arrive as one group above the marker, what the marker held re-sorts by time, and what
    /// was already there keeps its id.
    @Test func aLandingSegmentInsertsOneGroupAndKeepsIds() {
        let before = RecordingTimeline.make(sentences: [sentence(0, 2), sentence(0, 20)], marks: [40], asks: [], transcribedUntil: 30, now: 62)
        let after = RecordingTimeline.make(sentences: [sentence(0, 2), sentence(0, 20), sentence(1, 33), sentence(1, 45)], marks: [40], asks: [],
                                           transcribedUntil: 60, now: 62)
        #expect(ids(before) == [.sentence(segment: 0, millis: 2000), .sentence(segment: 0, millis: 20_000), .gap, .bookmark(0)])
        // The bookmark at 40 s now sits between the two new sentences.
        #expect(ids(after) == [.sentence(segment: 0, millis: 2000), .sentence(segment: 0, millis: 20_000), .sentence(segment: 1, millis: 33_000),
                               .bookmark(0), .sentence(segment: 1, millis: 45_000)])
        #expect(Set(ids(before)).subtracting([.gap]).isSubset(of: Set(ids(after))))
    }

    @Test func idsAreStableAcrossAppends() {
        let one = RecordingTimeline.make(sentences: [sentence(0, 2)], marks: [1], asks: [], transcribedUntil: 30, now: 31)
        let two = RecordingTimeline.make(sentences: [sentence(0, 2), sentence(1, 33)], marks: [1, 35], asks: [], transcribedUntil: 60, now: 61)
        #expect(Set(ids(one)).isSubset(of: Set(ids(two))))
        #expect(Set(ids(two)).count == two.count)
    }

    @Test func entrySentencesCarryTheirSegmentAndTheTranscribedBoundary() {
        var entry = RecordingEntry(id: UUID(), createdAt: .now, source: .phone, title: "T", active: true)
        entry.segments = [
            RecordingSegment(index: 1, start: 30, duration: 30, sentences: [RecordingSentence(start: 31, end: 33, text: "B")]),
            RecordingSegment(index: 0, start: 0, duration: 30, sentences: [RecordingSentence(start: 1, end: 3, text: "A")]),
            RecordingSegment(index: 2, start: 60, duration: 30, sentences: nil),
        ]
        let lines = RecordingTimeline.sentences(of: entry)
        #expect(lines.map(\.text) == ["A", "B"])
        #expect(lines.map(\.segment) == [0, 1])
        #expect(RecordingTimeline.transcribedUntil(of: entry) == 60)
        // An empty transcript of a silent segment counts as transcribed.
        entry.segments[2].sentences = []
        #expect(RecordingTimeline.transcribedUntil(of: entry) == 90)
    }
}

struct RecordingFollowTests {
    private func items(_ count: Int) -> [RecordingTimeline.Item] {
        (0..<count).map { .init(id: .bookmark($0), at: Double($0), kind: .bookmark($0 + 1)) }
    }

    @Test func followsUntilTheReaderDrags() {
        var follow = RecordingFollow()
        #expect(follow.following && !follow.showsJump)
        #expect(follow.unread(in: items(5)) == 0)
        follow.detach(items: items(5))
        #expect(!follow.following && follow.showsJump)
    }

    @Test func countsWhatArrivedWhileAway() {
        var follow = RecordingFollow()
        follow.detach(items: items(3))
        #expect(follow.unread(in: items(3)) == 0)
        #expect(follow.unread(in: items(5)) == 2)
        // A second drag while away changes nothing: the count is from the first.
        follow.detach(items: items(5))
        #expect(follow.unread(in: items(5)) == 2)
    }

    @Test func theGapMarkerIsNotAnArrival() {
        var follow = RecordingFollow()
        follow.detach(items: items(2))
        let withGap = items(2) + [.init(id: .gap, at: 9, kind: .gap)]
        #expect(follow.unread(in: withGap) == 0)
    }

    @Test func reachingTheEndOrJumpFollowsAgainAndClearsTheCount() {
        var follow = RecordingFollow()
        follow.detach(items: items(3))
        follow.attach()
        #expect(follow.following && !follow.showsJump)
        #expect(follow.unread(in: items(9)) == 0)
        // Detaching again counts from there.
        follow.detach(items: items(9))
        #expect(follow.unread(in: items(10)) == 1)
    }
}

@MainActor struct RecordingLevelPoseTests {
    private let count = Theme.Recording.meterBars

    @Test func pausedIsFlat() {
        for level in [0.0, 0.5, 1] {
            let bars = RecordingLevelPose.bars(level: level, at: 12.3, paused: true)
            #expect(bars.count == count)
            #expect(bars.allSatisfy { $0 == Theme.Recording.meterFloor })
        }
        #expect(RecordingLevelPose.bars(level: 1, at: nil, paused: true) == RecordingLevelPose.bars(level: 0, at: 5, paused: true))
    }

    /// Reduce Motion, Low Power Mode and an off-screen meter draw one frame: the same at any level and at any time.
    @Test func theStillFrameIsStill() {
        let still = RecordingLevelPose.bars(level: 0, at: nil, paused: false)
        #expect(still == RecordingLevelPose.bars(level: 1, at: nil, paused: false))
        #expect(still.count == count)
        #expect(still.max() == still[count / 2] || still.max() == still[count / 2 - 1])
        #expect(still.allSatisfy { $0 >= Theme.Recording.meterFloor && $0 <= Theme.Recording.meterStill })
    }

    @Test func silenceIsAFlatRowAndLoudnessLifts() {
        let silent = RecordingLevelPose.bars(level: 0, at: 3, paused: false)
        #expect(silent.allSatisfy { $0 == Theme.Recording.meterFloor })
        let loud = RecordingLevelPose.bars(level: 1, at: 3, paused: false)
        #expect(loud.reduce(0, +) > silent.reduce(0, +))
        let soft = RecordingLevelPose.bars(level: 0.3, at: 3, paused: false)
        #expect(loud.reduce(0, +) > soft.reduce(0, +))
    }

    @Test func barsStayInsideTheirHeightAndLevelsAreClamped() {
        for t in stride(from: 0.0, to: 20, by: 0.37) {
            for level in [-1.0, 0, 0.4, 1, 7, .nan, .infinity] {
                let bars = RecordingLevelPose.bars(level: level, at: t, paused: false)
                #expect(bars.allSatisfy { $0 >= Theme.Recording.meterFloor && $0 <= 1 })
            }
        }
        #expect(RecordingLevelPose.bars(level: 7, at: 1, paused: false) == RecordingLevelPose.bars(level: 1, at: 1, paused: false))
    }

    @Test func theMiddleStandsTallestAtAnyTime() {
        for t in [0.0, 1.1, 4.4] {
            let bars = RecordingLevelPose.bars(level: 1, at: t, paused: false)
            #expect(bars.count == count)
            // The ripple only takes away from the bell, so no bar is taller than the bell's own height at its place.
            for (index, bar) in bars.enumerated() {
                #expect(bar <= RecordingLevelPose.profile(index, of: count) + 1e-9)
            }
        }
        #expect(RecordingLevelPose.profile(0, of: count) < RecordingLevelPose.profile(count / 2, of: count))
        #expect(RecordingLevelPose.profile(count - 1, of: count) < RecordingLevelPose.profile(count / 2, of: count))
    }

    @Test func theMeterIsOneFrameAThirtiethOfASecond() {
        #expect(abs(Theme.Motion.recordingLevelFrame - 1.0 / 30) < 1e-12)
    }
}

struct RecordingStopAndSheetTests {
    private let id = UUID()

    @Test func aResumableErrorBeforeTheStopIsNotASaveFailure() {
        // The microphone changed and recording paused; the user then tapped Stop: the error is the same as when it ran.
        #expect(RecordingSheetMove.stopError(now: "The microphone changed", whileLive: "The microphone changed") == nil)
        let phase = RecordingPhase.derive(.init(captureError: RecordingSheetMove.stopError(now: "The microphone changed", whileLive: "The microphone changed"),
                                                held: .init(isMeeting: true, active: false, receivedAll: true, noAudio: false, transcribedAll: false,
                                                            transcribing: true, transcriptionError: nil, sync: .waiting, hubAvailable: true)))
        #expect(phase == .transcribing)
    }

    @Test func anErrorTheStopCausedIsOne() {
        #expect(RecordingSheetMove.stopError(now: "Storage is full", whileLive: nil) == "Storage is full")
        #expect(RecordingSheetMove.stopError(now: "Storage is full", whileLive: "The microphone changed") == "Storage is full")
        #expect(RecordingSheetMove.stopError(now: nil, whileLive: "The microphone changed") == nil)
        #expect(RecordingSheetMove.stopError(now: "", whileLive: nil) == nil)
    }

    @Test func aRecordingBeginsAndTheSheetHoldsIt() {
        #expect(RecordingSheetMove.move(from: .starting, to: .recording, recordingID: id, held: nil) == .hold(id, announce: true))
        #expect(RecordingSheetMove.move(from: .paused, to: .recording, recordingID: id, held: id) == .none)
        #expect(RecordingSheetMove.move(from: .recording, to: .paused, recordingID: id, held: id) == .none)
        #expect(RecordingSheetMove.move(from: .idle, to: .paused, recordingID: id, held: nil) == .hold(id, announce: false))
    }

    @Test func aStopShowsTheWrapUpFromRecordingOrPaused() {
        #expect(RecordingSheetMove.move(from: .recording, to: .idle, recordingID: nil, held: id) == .wrapUp)
        #expect(RecordingSheetMove.move(from: .paused, to: .idle, recordingID: nil, held: id) == .wrapUp)
    }

    @Test func aCancelledStartClosesTheSheetAndNothingElseDoes() {
        #expect(RecordingSheetMove.move(from: .starting, to: .idle, recordingID: nil, held: nil) == .dismiss)
        // The wrap-up is held and capture goes idle again without having run: nothing changes.
        #expect(RecordingSheetMove.move(from: .starting, to: .idle, recordingID: nil, held: id) == .none)
        #expect(RecordingSheetMove.move(from: .idle, to: .starting, recordingID: nil, held: id) == .none)
    }

    @Test func aNewRecordingWhileTheWrapUpIsShownReplacesIt() {
        let next = UUID()
        #expect(RecordingSheetMove.move(from: .starting, to: .recording, recordingID: next, held: id) == .hold(next, announce: true))
    }
}

struct RecordingWrapUpMappingTests {
    @Test func syncStatusesMapOneToOne() {
        #expect(RecordingWrapUp.sync(.waiting) == .waiting)
        #expect(RecordingWrapUp.sync(.pending) == .pending)
        #expect(RecordingWrapUp.sync(.uploading) == .uploading)
        #expect(RecordingWrapUp.sync(.synced) == .synced)
        #expect(RecordingWrapUp.sync(.rejected) == .rejected)
        #expect(RecordingWrapUp.sync(.expired) == .expired)
    }

    @Test func minutesStatesMapOneToOne() {
        #expect(RecordingWrapUp.minutes(.none) == .absent)
        #expect(RecordingWrapUp.minutes(.waitingForText) == .waitingForText)
        #expect(RecordingWrapUp.minutes(.waitingForSync) == .waitingForSync)
        #expect(RecordingWrapUp.minutes(.writing) == .writing)
        #expect(RecordingWrapUp.minutes(.ready(RecordingMinutes())) == .ready)
        #expect(RecordingWrapUp.minutes(.failed) == .failed)
        #expect(RecordingWrapUp.minutes(.unsupported) == .unsupported)
    }

    private func held(minutes: RecordingPhase.Minutes?, active: Bool = false, noAudio: Bool = false) -> RecordingPhase.Held {
        .init(isMeeting: true, active: active, receivedAll: true, noAudio: noAudio, transcribedAll: true, transcribing: false,
              transcriptionError: nil, sync: .synced, hubAvailable: true, minutes: minutes)
    }

    @Test func aSyncedMeetingNotYetAnsweredByTheHubIsWritingMinutes() {
        #expect(RecordingPhase.derive(.init(held: held(minutes: nil))) == .writingMinutes)
    }

    @Test func aStillActiveEntryIsEndingEvenWithNoAudio() {
        #expect(RecordingPhase.derive(.init(held: held(minutes: nil, active: true, noAudio: true))) == .ending)
    }

    @Test func stepsForIdleAndPreparingAreAllUnstarted() {
        for phase in [RecordingPhase.idle, .preparing(cancellable: true), .recording, .paused(byCall: false)] {
            let steps = RecordingPhase.steps(for: phase, isMeeting: true)
            #expect(steps.first?.state == .current)
            #expect(steps.dropFirst().allSatisfy { $0.state == .waiting })
        }
    }
}

struct RecordingTimelineIdTests {
    @Test func twoSentencesAtTheSameStartKeepDistinctIds() {
        let a = RecordingTimeline.Sentence(segment: 0, start: 4, text: "One")
        let b = RecordingTimeline.Sentence(segment: 0, start: 4, text: "Two")
        let items = RecordingTimeline.make(sentences: [a, b], marks: [], asks: [], transcribedUntil: 30, now: 31)
        #expect(Set(items.map(\.id)).count == items.count)
        #expect(items.map(\.id) == [.sentence(segment: 0, millis: 4000, occurrence: 0), .sentence(segment: 0, millis: 4000, occurrence: 1)])
    }
}
