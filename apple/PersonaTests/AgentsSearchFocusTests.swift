import SwiftUI
import Testing
import UIKit
@testable import Persona

/// UIKit focus probes are not user intent; explicit activation remains a supported search entry point.
@MainActor struct AgentsSearchFocusTests {
    @Test func passiveFocusProbeDoesNotBeginSearch() {
        var began = 0
        let field = AgentsSearchField(text: .constant(""), active: false, begin: { began += 1 })
        let coordinator = AgentsSearchField.Coordinator(field)
        #expect(!coordinator.textFieldShouldBeginEditing(UISearchTextField()))
        #expect(began == 0)
    }

    @Test func activeInputCanEditWithoutBeginningAnotherSearch() {
        var began = 0
        let field = AgentsSearchField(text: .constant("query"), active: true, begin: { began += 1 })
        let coordinator = AgentsSearchField.Coordinator(field)
        #expect(coordinator.textFieldShouldBeginEditing(UISearchTextField()))
        #expect(began == 0)
    }

    /// Search In (#7443): the scope's token deleted in the open search sets the search back to Full Text; a field set
    /// by the page (not edited), the field at rest and Full Text itself change nothing.
    @Test func deletingTheScopeTokenSearchesFullText() {
        var cleared = 0
        let empty = UISearchTextField()
        let open = AgentsSearchField.Coordinator(AgentsSearchField(text: .constant(""), active: true, scope: .title,
                                                                    clearScope: { cleared += 1 }))
        open.applying = true
        open.tokensEdited(empty)
        #expect(cleared == 0)
        open.applying = false
        let scoped = UISearchTextField()
        scoped.tokens = [AgentsSearchField.token(.title)]
        open.tokensEdited(scoped)
        #expect(cleared == 0)
        open.tokensEdited(empty)
        #expect(cleared == 1)
        let rest = AgentsSearchField.Coordinator(AgentsSearchField(text: .constant(""), active: false, scope: .title,
                                                                    clearScope: { cleared += 1 }))
        rest.tokensEdited(empty)
        let fullText = AgentsSearchField.Coordinator(AgentsSearchField(text: .constant(""), active: true,
                                                                        clearScope: { cleared += 1 }))
        fullText.tokensEdited(empty)
        #expect(cleared == 1)
    }

    /// Delete pressed before the token, in a real field with the keyboard: UIKit tells the field's delegate, and the
    /// scope goes back to Full Text.
    @Test func deleteBeforeTheTokenReachesTheField() throws {
        var cleared = 0
        let coordinator = AgentsSearchField.Coordinator(AgentsSearchField(text: .constant(""), active: true, scope: .title,
                                                                           clearScope: { cleared += 1 }))
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 400, height: 200))
        window.rootViewController = UIViewController()
        let field = UISearchTextField(frame: CGRect(x: 0, y: 0, width: 300, height: 44))
        window.rootViewController?.view.addSubview(field)
        window.makeKeyAndVisible()
        field.tokens = [AgentsSearchField.token(.title)]
        field.delegate = coordinator
        #expect(field.becomeFirstResponder())
        for _ in 0..<2 where !field.tokens.isEmpty { field.deleteBackward() }
        #expect(field.tokens.isEmpty)
        #expect(cleared >= 1)
        field.resignFirstResponder()
        window.isHidden = true
    }

    /// The token names its scope as the menu does, and × with words typed takes only the words.
    @Test func theTokenNamesTheScopeAndClearKeepsIt() {
        let token = AgentsSearchField.token(.errors)
        #expect(token.representedObject as? String == AgentSearchScope.errors.rawValue)
        var text = "timeout"
        let coordinator = AgentsSearchField.Coordinator(AgentsSearchField(text: Binding(get: { text }, set: { text = $0 }),
                                                                           active: true, scope: .errors))
        let field = UISearchTextField()
        field.tokens = [token]
        field.text = "timeout"
        #expect(!coordinator.textFieldShouldClear(field))
        #expect(text.isEmpty && (field.text ?? "").isEmpty)
        #expect(coordinator.textFieldShouldClear(field)) // nothing typed: the system's ×, the token with it
    }

    @Test func explicitAccessibilityActivationBeginsSearchOnce() {
        let bar = UISearchBar()
        let holder = AgentsSearchField.Holder(field: bar.searchTextField, bar: bar)
        var began = 0
        holder.activateSearch = { began += 1 }
        #expect(holder.accessibilityActivate())
        #expect(began == 1)
    }

    @Test func retainedAccessibilityClearDoesNotBeginSearch() throws {
        let bar = UISearchBar()
        let holder = AgentsSearchField.Holder(field: bar.searchTextField, bar: bar)
        var text = "query"
        var began = 0
        let binding = Binding(get: { text }, set: { text = $0 })
        let field = AgentsSearchField(text: binding, active: false, begin: { began += 1 })
        let coordinator = AgentsSearchField.Coordinator(field)
        coordinator.updateClearAction(for: holder)
        let action = try #require(holder.accessibilityCustomActions?.first)
        #expect(action.name == String(localized: "Clear Search"))
        // Editing within a nonempty query reuses the action rather than allocating one per update.
        coordinator.updateClearAction(for: holder)
        #expect(holder.accessibilityCustomActions?.first === action)
        #expect(coordinator.clearSearch(action))
        #expect(text.isEmpty)
        #expect(began == 0)
        coordinator.updateClearAction(for: holder)
        #expect(holder.accessibilityCustomActions == nil)
    }
}
