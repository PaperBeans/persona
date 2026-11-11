import AppKit
import SwiftUI
import Testing
@testable import Persona

/// A Mac sheet ignores the detents that size it on iOS and opens at its content's minimum size. The to-do picker is a
/// list in a navigation stack, which has no height before its rows are measured: it came up as a strip with no to-do in
/// view (#4404). Both of its entries, an agent's Link to To-do… and a project's Add to To-Do…, open at a form sheet's size.
@MainActor struct MacTodoLinkSheetTests {
    private func agent() throws -> Session {
        try JSON.decoder.decode(Session.self, from: Data(#"{"machine_id":"m","session_id":"s1","title":"Agent 1","state":"working","waiting":false}"#.utf8))
    }

    /// The size the sheet opens at, as `MacFileViewerSheetTests` measures it.
    private func openingSize(_ sheet: TodoLinkAgentSheet) -> CGSize {
        NSHostingView(rootView: sheet.environment(AppModel())).fittingSize
    }

    @Test func anAgentsPickerOpensAtAFormSheetsSize() throws {
        let size = openingSize(TodoLinkAgentSheet(session: try agent()))
        #expect(size.width >= Theme.Size.macSheetMinWidth, "opening size \(size)")
        #expect(size.height >= Theme.Size.macSheetMinHeight, "opening size \(size)")
    }

    @Test func aProjectsPickerOpensAtAFormSheetsSize() {
        let size = openingSize(TodoLinkAgentSheet(space: TodoItem.Space(machineID: "m", space: "app")))
        #expect(size.width >= Theme.Size.macSheetMinWidth, "opening size \(size)")
        #expect(size.height >= Theme.Size.macSheetMinHeight, "opening size \(size)")
    }
}
