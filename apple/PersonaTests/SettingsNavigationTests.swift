import SwiftUI
import Testing
@testable import Persona

@MainActor
struct SettingsNavigationTests {
    @Test func searchIsEmptyUntilAQueryIsEntered() {
        #expect(SettingsCatalog.search("").isEmpty)
        #expect(SettingsCatalog.search("  \n ").isEmpty)
    }

    @Test func everyEntryHasAUniqueStableIDAndSearchesByItsName() {
        let entries = SettingsCatalog.entries
        #expect(Set(entries.map(\.id)).count == entries.count)
        for entry in entries {
            #expect(SettingsCatalog.search(entry.title).contains { $0.id == entry.id })
        }
    }

    @Test func commonAliasesFindTheirDestination() {
        for (query, id) in [("API Key", "models"), ("Claude Code", "agents"), ("Codex", "agents"),
                            ("commit", "about"), ("Watch", "devices")] {
            #expect(SettingsCatalog.search(query).contains { $0.id == id })
        }
    }

    @Test func multipleWordsMustAllMatchAndUnknownWordsDoNotMatch() {
        let entry = SettingsEntry(id: "test", title: "Model Choices", symbol: Theme.Icon.model,
                                  group: .assistant, page: .models, aliases: "API Key")
        #expect(entry.matches("model key"))
        #expect(entry.matches("MODEL KEY"))
        #expect(!entry.matches("model nonexistent"))
        #expect(SettingsCatalog.search("not-a-setting-5236").isEmpty)
    }

    @Test func onlyAssistantPagesAreBlockedByTheCurrentAssistantsOfflineState() {
        for page in [SettingsPage.models, .agents, .assistant, .connectors, .plugins, .channels, .triage, .computers] {
            #expect(page.belongsToCurrentAssistant)
        }
        for page in [SettingsPage.appearance, .language, .notifications, .health, .phoneData, .about, .persona, .pairing, .managers, .hub] {
            #expect(!page.belongsToCurrentAssistant)
        }
        #expect(SettingsPage.plugin("example").belongsToCurrentAssistant)
        #expect(!SettingsPage.plugin("example", manager: "m-example").belongsToCurrentAssistant)
        #expect(!SettingsPage.connector("example", manager: "m-example").belongsToCurrentAssistant)
        #expect(!SettingsPage.channel("example", manager: "m-example").belongsToCurrentAssistant)
    }

    @Test func oldRoutesRemainIndependentFromNewContainers() {
        #expect(SettingsPage.modelConfiguration != .models)
        #expect(SettingsPage.claude != .agents)
        #expect(SettingsPage.codex != .agents)
        #expect(SettingsPage.triage != .agents)
        #expect(SettingsPage.devices != .pairing)
        #expect(SettingsPage.sentPath == NavigationPath([SettingsPage.connectors, .sent]))
    }
}
