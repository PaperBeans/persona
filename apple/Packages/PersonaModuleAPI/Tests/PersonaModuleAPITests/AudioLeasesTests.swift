import Foundation
import Testing
@testable import PersonaModuleAPI

private enum AudioHost: ModuleHost {
    typealias Attachment = String
}

@MainActor
private struct VoiceLike: PersonaModule {
    typealias Host = AudioHost
    var descriptor = ModuleDescriptor(id: "voice", hubGated: true, slots: [.audioLeases])
    let isAvailable = true
    var leases: [(String, AudioLeaseKind)] = [("voice.call", .call), ("voice.conversation", .voiceConversation)]

    func register(into registrar: inout ModuleRegistrar<AudioHost>) {
        for (id, kind) in leases {
            registrar.audioLease(id: id, kind: kind)
        }
    }
}

@MainActor
private struct RecordingLike: PersonaModule {
    typealias Host = AudioHost
    var descriptor = ModuleDescriptor(id: "recording", hubGated: true, slots: [.audioLeases])
    let isAvailable = true
    var leases: [(String, AudioLeaseKind)] = [("recording.capture", .meetingRecording), ("recording.replay", .playback)]

    func register(into registrar: inout ModuleRegistrar<AudioHost>) {
        for (id, kind) in leases {
            registrar.audioLease(id: id, kind: kind)
        }
    }
}

@MainActor
private struct UndeclaredAudio: PersonaModule {
    typealias Host = AudioHost
    let descriptor = ModuleDescriptor(id: "undeclared", slots: [])
    let isAvailable = true

    func register(into registrar: inout ModuleRegistrar<AudioHost>) {
        registrar.audioLease(id: "undeclared.playback", kind: .playback)
    }
}

@MainActor
struct AudioLeasesTests {
    @Test func rankingOrdersCallOverVoiceOverRecordingOverPlayback() {
        #expect(AudioLeaseKind.allCases == [.call, .voiceConversation, .meetingRecording, .playback])
        #expect(AudioLeaseKind.call > .voiceConversation)
        #expect(AudioLeaseKind.voiceConversation > .meetingRecording)
        #expect(AudioLeaseKind.meetingRecording > .playback)
        #expect(AudioLeaseKind.call.outranks(.voiceConversation))
        #expect(AudioLeaseKind.voiceConversation.outranks(.meetingRecording))
        #expect(AudioLeaseKind.meetingRecording.outranks(.playback))
        #expect(!AudioLeaseKind.playback.outranks(.call))
        #expect(!AudioLeaseKind.call.outranks(.call))
        let unsorted: [AudioLeaseKind] = [.playback, .meetingRecording, .call, .voiceConversation]
        #expect(unsorted.sorted(by: >) == [.call, .voiceConversation, .meetingRecording, .playback])
    }

    @Test func activeModulesRegisterAudioLeasesInOrder() {
        let registry = ModuleRegistry<AudioHost>(modules: [VoiceLike(), RecordingLike()])
        #expect(registry.problems.isEmpty)
        #expect(registry.audioLeases.map(\.id) == ["voice.call", "voice.conversation", "recording.capture", "recording.replay"])
        #expect(registry.audioLease(id: "voice.call") == ModuleAudioLease(id: "voice.call", module: "voice", kind: .call))
        #expect(registry.audioLease(id: "recording.replay") == ModuleAudioLease(id: "recording.replay", module: "recording", kind: .playback))
        #expect(registry.audioLease(id: "missing") == nil)
        #expect(registry.audioLeases(of: .call).map(\.id) == ["voice.call"])
        #expect(registry.audioLeases(of: .playback).map(\.id) == ["recording.replay"])
    }

    @Test func inactiveOrHubDisabledModulesRegisterNoAudioLeases() {
        let disabled = ModuleRegistry<AudioHost>(modules: [VoiceLike()], isEnabled: { _ in false })
        #expect(disabled.audioLeases.isEmpty)
        #expect(disabled.audioLease(id: "voice.call") == nil)

        let offByHub = ModuleRegistry<AudioHost>(modules: [VoiceLike()], hub: HubModules(hello: ["modules"]))
        #expect(offByHub.audioLeases.isEmpty)
        #expect(offByHub.offByHub == ["voice"])
        #expect(offByHub.rebuilt(hub: HubModules(hello: ["modules", "module.voice"])).audioLease(id: "voice.call")?.kind == .call)
    }

    @Test func undeclaredSlotDuplicateAndEmptyIDsAreRefused() {
        let undeclared = ModuleRegistry<AudioHost>(modules: [UndeclaredAudio()])
        #expect(undeclared.audioLeases.isEmpty)
        #expect(undeclared.problems == ["undeclared: registers into audio_leases, which its descriptor does not declare"])

        var colliding = RecordingLike()
        colliding.leases = [("voice.call", .meetingRecording), ("", .playback), ("recording.capture", .meetingRecording)]
        let both = ModuleRegistry<AudioHost>(modules: [VoiceLike(), colliding])
        #expect(both.audioLeases.map(\.id) == ["voice.call", "voice.conversation", "recording.capture"])
        #expect(both.audioLease(id: "voice.call")?.module == "voice")
        #expect(both.problems == [
            "recording: audio lease voice.call is already taken",
            "recording: audio lease id is empty",
        ])
    }
}
