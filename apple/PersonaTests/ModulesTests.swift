import BeautifulUI
import Foundation
import PersonaModuleAPI
import Testing
@testable import Persona

/// The module list and its switch (#13432, docs/apple/ios.md "Modules"): a module that is switched off leaves no entry in
/// any of the composer's attach sources, and one that is on appears where Glasses Photo always has.
@MainActor
struct ModulesTests {
    private func registry(glasses: Bool) -> ModuleRegistry<PersonaHostApp> {
        ModuleRegistry(modules: Modules.builtIn, isEnabled: { glasses && $0.descriptor.id == "glasses" })
    }

    @Test func theListRegistersWithoutProblems() {
        let all = ModuleRegistry<PersonaHostApp>(modules: Modules.builtIn, isEnabled: { _ in true })
        #expect(all.problems.isEmpty)
        #expect(all.active.map(\.id) == ["glasses"])
        #expect(Modules.registry.problems.isEmpty)
    }

    /// This build has no Meta Wearables SDK (CI never has it): glasses are off unless a DEBUG switch says otherwise.
    @Test func theDefaultFollowsTheBuild() {
        let built = ModuleRegistry<PersonaHostApp>(modules: Modules.builtIn)
        #expect(built.isActive("glasses") == MetaGlasses.isBuiltIn)
    }

    @Test func glassesOffLeavesNoAttachEntry() {
        let off = registry(glasses: false)
        #expect(off.composerActions.isEmpty)
        #expect(off.composerAction(id: "glasses") == nil)
        let sources = PromptItems.attachments(camera: true, modules: off.composerActions).map(\.id)
        #expect(sources == ["photo", "camera", "file"])
        let rows = AttachMoreSection.common(camera: {}, scan: {}, modules: off.composerActions, open: { _ in }, mention: {}).flatMap(\.rows).map(\.id)
        #expect(!rows.contains("attach-glasses"))
        #expect(rows == ["composer-menu-camera", "composer-menu-scan", "composer-menu-mention"])
    }

    @Test func glassesOnAddsTheEntryWhereItWas() {
        let on = registry(glasses: true)
        #expect(on.composerActions.map(\.id) == ["glasses"])
        let sources = PromptItems.attachments(camera: true, modules: on.composerActions)
        #expect(sources.map(\.id) == ["photo", "camera", "glasses", "file"])
        if case .symbol(let name) = sources[2].icon { #expect(name == Theme.Icon.glasses) } else { Issue.record("not a symbol") }
        #expect(sources[2].name == String(localized: "Glasses Photo"))
        var opened: [String] = []
        let rows = AttachMoreSection.common(camera: {}, scan: {}, modules: on.composerActions, open: { opened.append($0.id) }, mention: {}).flatMap(\.rows)
        #expect(rows.map(\.id) == ["composer-menu-camera", "composer-menu-scan", "attach-glasses", "composer-menu-mention"])
        let glasses = rows[2]
        #expect(glasses.title == String(localized: "Glasses Photo"))
        #expect(glasses.symbol == Theme.Icon.glasses)
        glasses.action()
        #expect(opened == ["glasses"])
    }

    @Test func glassesOffHandlesNoURL() {
        let url = URL(string: "persona://?metaWearablesAction=register")!
        #expect(!registry(glasses: false).handle(url))
    }
}
