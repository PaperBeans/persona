import Foundation
import Testing
@testable import Persona

@MainActor
struct AssistantIdentityTests {
    private func model(_ managers: [HubManager]?) -> AppModel {
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "assistant-identity-" + UUID().uuidString))
        model.managerRosters[""] = managers.map { ManagerRoster(managers: $0) }
        return model
    }

    @Test(arguments: [true, false])
    func oneAssistantHidesIdentityWhetherOnlineOrOffline(online: Bool) {
        let model = model([HubManager(id: "assistant", name: "Only assistant", online: online, isDefault: true)])
        #expect(!model.showsAssistantIdentity)
        #expect(model.assistantDisplayName == String(localized: "Persona"))
        #expect(model.assistantManager == nil)
        #expect(model.assistantManagerOffline == nil)
        #expect(model.assistantShown == nil)
        model.selectAssistantManager("assistant")
        #expect(model.assistantManagerID == nil)
    }

    @Test(arguments: [true, false])
    func multipleAssistantsKeepIdentityIncludingOfflineEntries(online: Bool) {
        let model = model([
            HubManager(id: "assistant", name: "First assistant", online: true, isDefault: true),
            HubManager(id: "second", name: "Second assistant", online: online),
        ])
        #expect(model.showsAssistantIdentity)
        #expect(model.assistantDisplayName == model.persona.name)
        #expect(model.assistantManager?.id == "assistant")
        #expect(model.assistantShown != nil)
    }

    @Test func unknownEmptyAndInstallingOnlyRostersHideIdentity() {
        #expect(!model(nil).showsAssistantIdentity)
        #expect(!model([]).showsAssistantIdentity)
        let installing = HubManager(id: "next", name: "Installing assistant", online: false, installing: Date.now + 3600)
        let model = model([HubManager(id: "assistant", name: "Only assistant", online: true), installing])
        #expect(!model.showsAssistantIdentity)
        #expect(model.managerRoster?.installed.count == 1)
    }

    @Test func theMainChatLabelDoesNotNameASingleAssistant() {
        #expect(AssistantSidebar.shownMainTitle(false) == String(localized: "Main Chat"))
        #expect(AssistantSidebar.shownMainTitle(true) == "Persona")
    }

    @Test func conversationTitlesRemainWhileTheSoleAssistantsNameIsHidden() {
        #expect(AssistantTopBar.visibleTitle(showsIdentity: false, persona: "Persona", conversation: nil, isMain: true) == nil)
        #expect(AssistantTopBar.visibleTitle(showsIdentity: false, persona: "Persona", conversation: "Notes", isMain: false) == "Notes")
        #expect(AssistantTopBar.visibleTitle(showsIdentity: false, persona: "Persona", conversation: "Claude Code", isMain: false) == nil)
        #expect(AssistantTopBar.visibleTitle(showsIdentity: true, persona: "Persona", conversation: nil, isMain: true) == "Persona")
        #expect(AssistantTopBar.visibleTitle(showsIdentity: true, persona: "Persona", conversation: "Notes", isMain: false) == "Notes")
    }

    @Test func identityFollowsTheRosterWithoutChangingOwnership() {
        let first = HubManager(id: "assistant", name: "First assistant", online: true, isDefault: true)
        let second = HubManager(id: "second", name: "Second assistant", online: false)
        let model = model([first])
        #expect(!model.showsAssistantIdentity)
        model.managerRosters[""] = ManagerRoster(managers: [first, second])
        #expect(model.showsAssistantIdentity)
        #expect(!model.sameManager(first.id, second.id))
        model.managerRosters[""] = ManagerRoster(managers: [first])
        #expect(!model.showsAssistantIdentity)
        #expect(model.sameManager(first.id, second.id))
    }
}
