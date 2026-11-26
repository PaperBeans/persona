import SwiftUI

/// The existing grouped form and native move handles keep selection and order together.
struct HealthItemsEditor: View {
    let page: HealthItemsModel

    var body: some View {
        NavigationStack {
            SettingsForm {
                Section {
                    ForEach(page.editOrder, id: \.self) { id in
                        Toggle(isOn: Binding(get: { page.selected.contains(id) }, set: { page.setAdded(id, $0) })) {
                            Text(HealthItemTitle.key(id))
                        }
                        .accessibilityIdentifier("health-item-" + id)
                        .accessibilityAction(named: Text("Move Up")) { page.moveUp(id) }
                        .themedRow()
                    }
                    .onMove { page.move(from: $0, to: $1) }
                } footer: {
                    Text("Choose what appears in Health. Removing an item keeps its records.")
                }
                if let error = page.error {
                    Section { LoadProblem(text: error).accessibilityIdentifier("health-items-error") }
                }
            }
            .environment(\.editMode, .constant(.active))
            .disabled(page.busy)
            .themedPage()
            .navigationTitle(Text("Health Items"))
            .toolbar {
                SheetClose(disabled: page.busy) { page.closeEditor() }
                SheetConfirm(id: "health-items-save", name: String(localized: "Save"), disabled: page.busy, busy: page.busy) {
                    Task { await page.saveEditor() }
                }
            }
        }
        .interactiveDismissDisabled(page.busy)
    }
}

enum HealthItemTitle {
    static func key(_ id: String) -> LocalizedStringKey {
        switch id {
        case "water": "Water"
        case "protein": "Protein"
        case "activity": "Activity"
        case "strength": "Strength Training"
        default: "Health Trends"
        }
    }
}

struct HealthItemsQuestion: View {
    @Bindable var page: HealthItemsModel
    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("What would you like to track?").font(Theme.Font.bodyStrong)
            Toggle("Water", isOn: $page.wantsWater).tapTargetHeight().accessibilityIdentifier("health-question-water")
            Toggle("Protein", isOn: $page.wantsProtein).tapTargetHeight().accessibilityIdentifier("health-question-protein")
            if page.canShowStrength {
                Toggle("Strength Training", isOn: $page.wantsStrength).tapTargetHeight().accessibilityIdentifier("health-question-strength")
            }
            StackingButtonRow {
                Button { Task { await page.answerQuestion(add: true) } } label: {
                    Text("Add").buttonLabelLine().frame(maxWidth: .infinity).scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton(prominent: true)
                .disabled(!page.wantsWater && !page.wantsProtein && !(page.canShowStrength && page.wantsStrength))
                .accessibilityIdentifier("health-question-add")
                Button { Task { await page.answerQuestion(add: false) } } label: {
                    Text("Not Now [Health Items]").buttonLabelLine().frame(maxWidth: .infinity).scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton()
                .accessibilityIdentifier("health-question-skip")
            }
            if page.busy { RowProgress() }
            if let error = page.error { LoadProblem(text: error) }
        }
        .disabled(page.busy)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("health-items-question")
    }
}
