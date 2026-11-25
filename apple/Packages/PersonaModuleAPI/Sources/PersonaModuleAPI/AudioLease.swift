import Foundation

/// What may own the shared audio session, ranked highest first: a system call, an in-app voice conversation,
/// a meeting recording, and playback (#13849, #13431). A higher rank outranks every lower one
/// (`call > voiceConversation > meetingRecording > playback`).
public enum AudioLeaseKind: String, CaseIterable, Hashable, Comparable, Sendable {
    case call = "call"
    case voiceConversation = "voice_conversation"
    case meetingRecording = "meeting_recording"
    case playback = "playback"

    /// Priority rank: `call` (4) > `voiceConversation` (3) > `meetingRecording` (2) > `playback` (1).
    public var rank: Int {
        switch self {
        case .playback: 1
        case .meetingRecording: 2
        case .voiceConversation: 3
        case .call: 4
        }
    }

    public static func < (lhs: AudioLeaseKind, rhs: AudioLeaseKind) -> Bool {
        lhs.rank < rhs.rank
    }

    /// True when this lease kind outranks `other`.
    public func outranks(_ other: AudioLeaseKind) -> Bool {
        rank > other.rank
    }
}

/// A lease a module declares on the shared audio session (`ModuleSlot.audioLeases`): its unique id, owning module
/// and priority kind. The core's `AudioSessionBroker` tracks active leases against these declarations.
public struct ModuleAudioLease: Identifiable, Hashable, Sendable {
    /// Unique among all declared module audio leases (for example `"screen-share.pip"`, `"voice.conversation"`).
    public let id: String
    public let module: ModuleID
    public let kind: AudioLeaseKind

    public init(id: String, module: ModuleID, kind: AudioLeaseKind) {
        self.id = id
        self.module = module
        self.kind = kind
    }
}
