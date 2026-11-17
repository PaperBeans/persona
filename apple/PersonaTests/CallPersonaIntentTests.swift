import AppIntents
import Foundation
import Testing
@testable import Persona

/// Siri's and Shortcuts' ways into a call to Persona (#9290, docs/apple/siri.md「Calling Persona」): the App Shortcut Call
/// Persona, Talk to Persona with no car, and the opt-in SiriKit extension's switch as the app sees it.
struct CallPersonaIntentTests {
    /// The call's Start needs the app in front, so Call Persona opens it before it runs.
    @Test func callPersonaOpensTheApp() {
        #expect(CallPersonaIntent.supportedModes == .foreground(.immediate))
    }

    /// The car's conversation still starts in the background (the phone may stay locked in the car); only the phone's
    /// call comes to the front.
    @Test func talkToPersonaStaysInTheBackgroundUntilItCalls() {
        #expect(TalkToPersonaIntent.supportedModes.contains(.background))
        #expect(TalkToPersonaIntent.supportedModes.contains(.foreground(.dynamic)))
        #expect(!TalkToPersonaIntent.supportedModes.contains(.foreground(.immediate)))
    }

    /// Apple allows ten App Shortcuts; more and the build's App Intents metadata is refused.
    @Test func atMostTenAppShortcuts() {
        #expect(PersonaShortcuts.appShortcuts.count == 10)
    }

    /// Siri's, Shortcuts' or Recents' call with the shell's Settings sheet up (#9290): the sheet closes first, since the
    /// call page and a refusal's alert go over the app's root, which presents nothing over a sheet (as a notification's
    /// chat closes it, #3818). The page under it stays as it was.
    @MainActor @Test func siriClosesTheSettingsSheetForTheCallPage() async {
        let model = AppModel()
        model.selectedTab = .assistant
        let shell = ShellState()
        shell.attach(to: model)
        defer { shell.detach(from: model) }
        shell.select(1)
        model.selectedTab = .settings
        #expect(model.shellSettings)
        await PersonaCalls.clearTheWay(model)
        #expect(!model.shellSettings)
        #expect(shell.page == 1, "the page under Settings changed")
        await PersonaCalls.clearTheWay(model) // nothing up: nothing changes
        #expect(!model.shellSettings && shell.page == 1)
    }

    /// Talk to Persona promises a call only where it places one: the phone's description and keywords; elsewhere
    /// (Apple Vision Pro, where `PersonaCalls` does not exist and it only says to connect CarPlay) CarPlay's alone.
    @Test func talkToPersonaPromisesACallOnlyWhereItPlacesOne() throws {
        let path = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .appending(path: "Persona/Intents/TalkToPersonaIntent.swift")
        // The code alone: a comment may say what it does not promise.
        let source = try String(contentsOf: path, encoding: .utf8).components(separatedBy: "\n")
            .filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("//") }.joined(separator: "\n")
        let phone = try #require(source.range(of: "#if os(iOS)"))
        let other = try #require(source.range(of: "#else", range: phone.upperBound..<source.endIndex))
        let end = try #require(source.range(of: "#endif", range: other.upperBound..<source.endIndex))
        let elsewhere = String(source[other.upperBound..<end.lowerBound])
        #expect(String(source[phone.upperBound..<other.lowerBound]).contains("a call to Persona on this device"))
        #expect(elsewhere.contains("IntentDescription(") && !elsewhere.contains("call to Persona") && !elsewhere.contains(#""call""#))
    }

    @Test func siriSaysItIsCalling() {
        #expect(CallPersonaIntent.calling == String(localized: "Calling Persona"))
        #expect(CallPersonaIntent.calling != CarPlayTalk.notConnected.sentence)
    }

    /// A build has the SiriKit extension exactly when apple/siri-calls.yml was merged in, which brings Siri's purpose
    /// string with it: off by default, and then the app never asks for Siri. The raw Info.plist: the translations sit in
    /// InfoPlist.strings either way, and `object(forInfoDictionaryKey:)` would find them there.
    @MainActor @Test func siriCallsFollowTheBuildsSwitch() {
        let purpose = Bundle.main.infoDictionary?["NSSiriUsageDescription"] as? String
        #expect(SiriCalls.builtIn == (purpose != nil))
        let plugins = Bundle.main.builtInPlugInsURL?.appending(path: "PersonaCallIntents.appex").path ?? ""
        #expect(SiriCalls.builtIn == FileManager.default.fileExists(atPath: plugins))
    }
}
