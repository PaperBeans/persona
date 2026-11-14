import Foundation

// The words around a call that is not on: why the page offers none (the 「+」 menu's Voice Chat, docs/assistant/voice.md 1.3 and 6.1),
// and what stays after one ended by itself (「语音已结束 · 继续」, docs/assistant/voice.md 1.2 L). Pure, like `VoiceBarLine`; the
// extension's or the upstream's own words only go under Details (docs/lessons/voice.md "Implementation reminders").

/// Where Open Settings goes.
nonisolated enum VoiceSettingsPlace: Equatable, Sendable {
    /// Settings › Extensions: there is no voice extension to open (none, switched off, a hub too old to list it).
    case extensions
    /// Settings › Extensions › the voice extension: its address and key.
    case voiceExtension(String)
    /// Model Settings' use `plugin.<id>`: the voice model.
    case model(String)
    /// The system's Settings for this app: the microphone.
    case system
}

/// Why Voice Chat is not offered here, for the 「+」 menu's item (it stays and explains, docs/assistant/voice.md 6.1).
nonisolated struct VoiceUnavailableNote: Equatable, Sendable {
    var title: String
    var message: String
    /// nil: nothing to set (a device that may only answer, a read-only conversation).
    var settings: VoiceSettingsPlace?

    /// nil when there is nothing to explain (available, or not on this platform). `extensionID`: the group's voice
    /// extension, running or not, when the hub lists one.
    static func make(_ availability: VoiceAvailability, extensionID: String?) -> VoiceUnavailableNote? {
        let place = extensionID.map(VoiceSettingsPlace.voiceExtension) ?? .extensions
        switch availability {
        case .available, .unsupported, .noConversation:
            return nil
        case .noExtension, .outdated:
            return VoiceUnavailableNote(title: String(localized: "Realtime voice isn’t on for the hub yet"),
                                        message: String(localized: "Turn on realtime voice in Settings › Extensions. If the hub is too old, update it first."),
                                        settings: availability == .outdated ? .extensions : place)
        case .needsSetup(let reasons):
            let why = reasons.filter { !$0.isEmpty }.joined(separator: "\n")
            return VoiceUnavailableNote(title: String(localized: "Realtime voice service isn’t set up"),
                                        message: why.isEmpty ? String(localized: "Fill in the realtime voice service’s address and key to use it.") : why,
                                        settings: place)
        case .limitedDevice:
            return VoiceUnavailableNote(title: String(localized: "This device can only view and answer"),
                                        message: String(localized: "Voice chat needs a device that can send messages."), settings: nil)
        case .readOnly:
            return VoiceUnavailableNote(title: String(localized: "Voice isn’t available in this chat"),
                                        message: String(localized: "It’s view-only and can’t take messages."), settings: nil)
        case .meetingRoom:
            return VoiceUnavailableNote(title: String(localized: "Voice isn’t available in this chat"),
                                        message: String(localized: "To talk to Persona in this meeting, hold Talk on the meeting’s page while it records."),
                                        settings: nil)
        case .unknown:
            return VoiceUnavailableNote(title: String(localized: "Not sure yet whether the hub supports voice"),
                                        message: String(localized: "Try again once connected to the hub."), settings: nil)
        }
    }
}

/// What stays where the bar was after a call ended by itself, until the page goes (「语音已结束 · 继续」).
nonisolated struct VoiceEndNote: Equatable, Sendable {
    /// The way back on: Continue after an ending that may just pass (the background, a phone call), Retry after a failure.
    enum Again: Equatable, Sendable {
        case resume, retry
    }

    var text: String
    var again: Again?
    var settings: VoiceSettingsPlace?
    /// The extension's or the upstream's own words, behind Details.
    var details: String?
    /// What the hand feels as it ends (no cue can play: the call's player is gone with it).
    var haptic: Theme.VoiceHaptic

    /// nil for the user's own endings (Stop, leaving the page, another call): nothing stays.
    static func make(_ end: VoiceEnd, extensionID: String) -> VoiceEndNote? {
        func note(_ text: String, _ again: Again? = .resume, settings: VoiceSettingsPlace? = nil, details: String? = nil) -> VoiceEndNote {
            VoiceEndNote(text: text, again: again, settings: settings, details: details.flatMap { $0.isEmpty ? nil : $0 },
                         haptic: again == .resume ? .attention : .failure)
        }
        switch end {
        case .user, .left, .replaced:
            return nil
        case .background:
            return note(String(localized: "The app went to the background"))
        case .interrupted:
            return note(String(localized: "A call or another app took the audio"))
        case .microphoneTaken:
            return note(String(localized: "A recording or dictation took the microphone"))
        case .microphoneBusy:
            return note(String(localized: "Finish the recording or dictation first"), .retry)
        case .permissionDenied:
            return note(String(localized: "Microphone access is needed"), .retry, settings: .system)
        case .audioFailed:
            return note(String(localized: "Something went wrong with the microphone or speaker"), .retry)
        case .audioReset:
            return note(String(localized: "The system’s audio services restarted"))
        case .idle:
            return note(String(localized: "No one spoke for ten minutes"))
        case .lost:
            return note(String(localized: "Can't reach the hub"))
        case .rejected(let status):
            switch status {
            case 401: return note(String(localized: "The hub no longer accepts this device; pair again and retry"), nil)
            case 403: return note(String(localized: "This device can only view and answer, not use voice"), nil)
            default: return note(String(localized: "The hub doesn’t have this voice extension"), .retry, settings: .extensions)
            }
        case .incompatible:
            return note(String(localized: "The hub’s voice extension is too old to use here"), nil)
        case .modelUnavailable(let why):
            return note(String(localized: "The chosen voice model can’t be used"), .retry, settings: .model(extensionID), details: why)
        case .noHub:
            return note(String(localized: "This chat’s hub isn’t paired"), nil)
        case .failed(let code, let message):
            switch code {
            case .upstreamForbidden?:
                return note(String(localized: "Can’t reach the realtime voice service (wrong key, or not available in your region)"), .retry,
                            settings: .voiceExtension(extensionID), details: message)
            case .upstreamUnavailable?:
                return note(String(localized: "The realtime voice service is temporarily unavailable"), .retry, details: message)
            case .upstreamFailed?:
                return note(String(localized: "The realtime voice service disconnected"), .retry, details: message)
            case .noUpstream?:
                return note(String(localized: "Realtime voice service isn’t set up"), .retry, settings: .voiceExtension(extensionID), details: message)
            case .targetGone?, .targetReadOnly?, .meetingRoom?, .notMeetingRoom?:
                return note(String(localized: "Voice isn’t available in this chat"), nil, details: message)
            case .targetForbidden?:
                return note(String(localized: "This device can’t use voice in this chat"), nil, details: message)
            case .modelUnavailable?:
                return note(String(localized: "The chosen voice model can’t be used"), .retry, settings: .model(extensionID), details: message)
            case .machineOffline?:
                return note(String(localized: "This computer is offline [Voice Notes 7fefa8]"), .retry, details: message)
            case .other?, nil:
                return note(String(localized: "Something went wrong with voice"), .retry, details: message)
            }
        }
    }
}
