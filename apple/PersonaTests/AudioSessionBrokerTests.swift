import AVFoundation
import Foundation
import os
import PersonaModuleAPI
import Testing
@testable import Persona

/// `AudioSessionBroker` (#13849, part of #13431 and #13432): lease priority ranking, active/declared lease tracking,
/// idempotent release, backend forwarding with no behavioral change, and notification routing.
@MainActor
struct AudioSessionBrokerTests {
    private nonisolated final class RecordedCalls: @unchecked Sendable {
        private let lock = OSAllocatedUnfairLock(initialState: [String]())
        var items: [String] { lock.withLock { $0 } }
        func add(_ entry: String) { lock.withLock { $0.append(entry) } }
    }

    private nonisolated struct SampleError: Error, Equatable {}

    private func fakeBroker(recorded: RecordedCalls = RecordedCalls(),
                            otherAudio: Bool = false,
                            inputAvailable: Bool = true,
                            throwOnSetCategory: Bool = false,
                            center: NotificationCenter = NotificationCenter()) -> AudioSessionBroker {
        let backend = AudioSessionBroker.Backend(
            setCategory: { category, mode, options in
                recorded.add("setCategory:\(category.rawValue):\(mode.rawValue):\(options.rawValue)")
                if throwOnSetCategory { throw SampleError() }
            },
            setActive: { active, options in
                recorded.add("setActive:\(active):\(options.rawValue)")
            },
            category: { .playback },
            currentRoute: { AVAudioSession.sharedInstance().currentRoute },
            isOtherAudioPlaying: { otherAudio },
            availableInputs: { [] },
            setPreferredInput: { _ in recorded.add("setPreferredInput") },
            isInputAvailable: { inputAvailable },
            outputLatency: { 0.025 },
            ioBufferDuration: { 0.01 },
            overrideOutputAudioPort: { port in
                recorded.add("override:\(port.rawValue)")
            }
        )
        return AudioSessionBroker(backend: backend, notificationCenter: center)
    }

    @Test func leaseRankingOrdersCallVoiceRecordingPlayback() {
        #expect(AudioSessionBroker.LeaseKind.allCases == [.call, .voiceConversation, .meetingRecording, .playback])
        #expect(AudioSessionBroker.LeaseKind.call > .voiceConversation)
        #expect(AudioSessionBroker.LeaseKind.voiceConversation > .meetingRecording)
        #expect(AudioSessionBroker.LeaseKind.meetingRecording > .playback)
        #expect(AudioSessionBroker.LeaseKind.call.outranks(.voiceConversation))
        #expect(AudioSessionBroker.LeaseKind.voiceConversation.outranks(.meetingRecording))
        #expect(AudioSessionBroker.LeaseKind.meetingRecording.outranks(.playback))
        #expect(!AudioSessionBroker.LeaseKind.playback.outranks(.meetingRecording))
        #expect(!AudioSessionBroker.LeaseKind.voiceConversation.outranks(.voiceConversation))

        // Maps 1-to-1 with PersonaModuleAPI.AudioLeaseKind.
        for apiKind in AudioLeaseKind.allCases {
            let brokerKind = AudioSessionBroker.LeaseKind(apiKind)
            #expect(brokerKind.rawValue == apiKind.rawValue)
            #expect(brokerKind.rank == apiKind.rank)
        }
    }

    @Test func activeLeasesSortByRankThenAcquisitionOrderWithoutEnforcingPreemption() {
        let recorded = RecordedCalls()
        let broker = fakeBroker(recorded: recorded)

        let playback = broker.acquire(id: "screen-share.pip", module: "screen-share", kind: .playback)
        let rec1 = broker.acquire(id: "recording.capture", module: "recording", kind: .meetingRecording)
        let voice = broker.acquire(id: "voice.conversation", module: "voice", kind: .voiceConversation)
        let rec2 = broker.acquire(id: "core.dictation", kind: .meetingRecording)
        let call = broker.acquire(id: "call.system", module: "voice", kind: .call)

        #expect(broker.activeLeases.map(\.id) == [
            "call.system",
            "voice.conversation",
            "recording.capture",
            "core.dictation",
            "screen-share.pip",
        ])
        #expect(broker.highestActiveLease?.id == "call.system")
        #expect(broker.highestActiveKind == .call)
        #expect(broker.hasActiveLease(of: .call))
        #expect(broker.hasActiveLease(id: "screen-share.pip"))
        #expect(!broker.hasActiveLease(id: "unknown"))
        #expect(!broker.outranksActive(.voiceConversation))
        #expect(broker.wouldPreempt(for: .call).map(\.id) == [
            "voice.conversation",
            "recording.capture",
            "core.dictation",
            "screen-share.pip",
        ])

        // Releasing leases never deactivates AVAudioSession directly (no behavioral change).
        call.release()
        call.release() // idempotent
        #expect(broker.highestActiveLease?.id == "voice.conversation")
        #expect(broker.outranksActive(.call))
        #expect(!broker.outranksActive(.meetingRecording))

        voice.release()
        rec1.release()
        rec2.release()
        playback.release()
        #expect(broker.activeLeases.isEmpty)
        #expect(broker.highestActiveLease == nil)
        #expect(broker.outranksActive(.playback))
        #expect(recorded.items.isEmpty)
    }

    @Test func deinitializingLeaseReleasesItAutomatically() {
        let broker = fakeBroker()
        do {
            let temporary = broker.acquire(id: "recording.replay", module: "recording", kind: .playback)
            #expect(broker.hasActiveLease(id: temporary.record.id))
        }
        #expect(broker.activeLeases.isEmpty)
    }

    @Test func forwardingMethodsDelegateToBackendUnchanged() throws {
        let recorded = RecordedCalls()
        let broker = fakeBroker(recorded: recorded, otherAudio: true, inputAvailable: false)

        try broker.setCategory(.playAndRecord, mode: .voiceChat, options: [.defaultToSpeaker])
        try broker.setActive(true)
        try broker.overrideOutputAudioPort(.speaker)
        try broker.setPreferredInput(nil)
        try broker.setActive(false, options: .notifyOthersOnDeactivation)

        #expect(broker.category == .playback)
        #expect(broker.isOtherAudioPlaying)
        #expect(!broker.isInputAvailable)
        #expect(broker.availableInputs.isEmpty)
        #expect(broker.outputLatency == 0.025)
        #expect(broker.ioBufferDuration == 0.01)
        #expect(recorded.items == [
            "setCategory:\(AVAudioSession.Category.playAndRecord.rawValue):\(AVAudioSession.Mode.voiceChat.rawValue):\(AVAudioSession.CategoryOptions.defaultToSpeaker.rawValue)",
            "setActive:true:0",
            "override:\(AVAudioSession.PortOverride.speaker.rawValue)",
            "setPreferredInput",
            "setActive:false:\(AVAudioSession.SetActiveOptions.notifyOthersOnDeactivation.rawValue)",
        ])

        let failing = fakeBroker(throwOnSetCategory: true)
        #expect(throws: SampleError.self) {
            try failing.setCategory(.record, mode: .measurement, options: .duckOthers)
        }
    }

    @Test func notificationHelpersRouteThroughBrokerNotificationCenter() {
        let center = NotificationCenter()
        let broker = fakeBroker(center: center)
        let seen = RecordedCalls()

        let routeToken = broker.observeRouteChanges(queue: nil) { reason in
            seen.add("route:\(reason?.rawValue ?? 999)")
        }
        let interruptToken = broker.observeInterruptions(queue: nil) { event in
            switch event {
            case .began(let reason): seen.add("begin:\(reason?.rawValue ?? 999)")
            case .ended(let shouldResume): seen.add("end:\(shouldResume)")
            }
        }
        let resetToken = broker.observeMediaServicesReset(queue: nil) {
            seen.add("reset")
        }

        center.post(name: AVAudioSession.routeChangeNotification, object: nil, userInfo: [
            AVAudioSessionRouteChangeReasonKey: AVAudioSession.RouteChangeReason.newDeviceAvailable.rawValue,
        ])
        center.post(name: AVAudioSession.interruptionNotification, object: nil, userInfo: [
            AVAudioSessionInterruptionTypeKey: AVAudioSession.InterruptionType.began.rawValue,
            AVAudioSessionInterruptionReasonKey: AVAudioSession.InterruptionReason.default.rawValue,
        ])
        center.post(name: AVAudioSession.interruptionNotification, object: nil, userInfo: [
            AVAudioSessionInterruptionTypeKey: AVAudioSession.InterruptionType.ended.rawValue,
            AVAudioSessionInterruptionOptionKey: AVAudioSession.InterruptionOptions.shouldResume.rawValue,
        ])
        center.post(name: AVAudioSession.mediaServicesWereResetNotification, object: nil)

        broker.removeObserver(routeToken)
        broker.removeObserver(interruptToken)
        broker.removeObserver(resetToken)

        // Removed observers receive nothing further.
        center.post(name: AVAudioSession.mediaServicesWereResetNotification, object: nil)

        #expect(seen.items == [
            "route:\(AVAudioSession.RouteChangeReason.newDeviceAvailable.rawValue)",
            "begin:\(AVAudioSession.InterruptionReason.default.rawValue)",
            "end:true",
            "reset",
        ])
    }

    @Test func moduleStateSyncsDeclaredAudioLeasesAndFollowsHubFlags() {
        let suiteName = "AudioSessionBrokerTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let broker = fakeBroker()
        let state = ModuleState(group: "home", defaults: defaults,
                                isEnabled: { $0.descriptor.id == "screen-share" },
                                audioBroker: broker)
        #expect(broker.declaredLease(id: "screen-share.pip") == AudioSessionBroker.DeclaredLease(
            id: "screen-share.pip", module: "screen-share", kind: .playback
        ))

        if let declared = state.registry.audioLease(id: "screen-share.pip") {
            let lease = broker.acquire(declared)
            #expect(lease.record.id == "screen-share.pip")
            #expect(lease.record.module == "screen-share")
            #expect(lease.record.kind == .playback)
            #expect(broker.hasActiveLease(id: "screen-share.pip"))
            lease.release()
        } else {
            Issue.record("expected screen-share.pip audio lease declaration")
        }

        // Hub turns screen-share off: declared lease disappears.
        state.heard(["modules"], from: "home", isHub: true, defaults: defaults)
        #expect(broker.declaredLeases.isEmpty)
        #expect(broker.declaredLease(id: "screen-share.pip") == nil)

        // Hub turns screen-share back on: declared lease returns.
        state.heard(["modules", "module.screen-share"], from: "home", isHub: true, defaults: defaults)
        #expect(broker.declaredLease(id: "screen-share.pip")?.kind == .playback)
    }

    @Test func liveCallGateRecordsAndReleasesSystemCallLease() {
        let wasLive = LiveCallGate.liveNow
        defer { LiveCallGate.callLiveChanged(wasLive) }

        LiveCallGate.callLiveChanged(false)
        #expect(!AudioSessionBroker.shared.hasActiveLease(id: "call.system"))

        LiveCallGate.callLiveChanged(true)
        #expect(AudioSessionBroker.shared.hasActiveLease(id: "call.system"))
        #expect(AudioSessionBroker.shared.highestActiveKind == .call)

        LiveCallGate.callLiveChanged(false)
        #expect(!AudioSessionBroker.shared.hasActiveLease(id: "call.system"))
    }
}
