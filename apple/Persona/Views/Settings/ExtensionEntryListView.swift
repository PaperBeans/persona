import SwiftUI
import PersonaWire

/// Captured target for an entry-list removal confirmation dialog so dismissal never blanks its title.
struct PendingEntryRemoval: Equatable {
    var listName: String
    var key: String
    var title: String
}

/// A declared extension entry list (#9504): one lazy List row per entry with its title, secret state, write-only
/// Secret button (saved entries only), confirmed Remove action, and 1–8 flat text fields, followed by Add entry.
struct ExtensionEntryListSection: View {
    var field: ExtensionField
    @Bindable var page: ExtensionModel
    var templates: [ExtensionTextTemplate]
    var numberFocus: FocusState<String?>.Binding
    var onSecret: (ExtensionSecret, String) -> Void
    var onRequestRemove: (PendingEntryRemoval) -> Void

    private struct EntryRowItem: Identifiable {
        var id: String
        var index: Int
        var entry: [String: JSONValue]
    }

    private var title: String {
        field.required == true && field.default == nil
            ? String(localized: "\(field.label) (required)")
            : field.label
    }

    private var maxItems: Int { field.maxItems ?? ExtensionModel.defaultMaxEntries }
    private var minItems: Int { field.minItems ?? 0 }
    private var subFields: [ExtensionField] { field.items?.fields ?? [] }
    private var clearsOnRebind: Bool { subFields.contains { $0.clearsSecret == true } }

    var body: some View {
        let entries = ExtensionModel.entryItems(in: page.draft, at: [field.name])
        let rows = entries.enumerated().map { idx, entry in
            EntryRowItem(id: page.entryKey(for: field.name, at: idx, entry: entry), index: idx, entry: entry)
        }
        let canRemove = !page.saving && !(field.required == true && entries.count <= minItems)
        let listError = page.fieldError([field.name])
        let description = field.description?.isEmpty == false ? field.description : nil
        Section {
            if description != nil || listError != nil {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    if let description {
                        Text(description)
                            .font(Theme.Font.label)
                            .foregroundStyle(.secondary)
                    }
                    if let listError {
                        Text(listError)
                            .font(Theme.Font.label)
                            .foregroundStyle(Theme.danger)
                            .accessibilityIdentifier("plugin-field-\(field.name)")
                    }
                }
                .padding(.vertical, Theme.Space.xxs)
            }
            ForEach(rows) { row in
                ExtensionEntryCardRow(
                    listField: field,
                    rowKey: row.id,
                    index: row.index,
                    entry: row.entry,
                    subFields: subFields,
                    canRemove: canRemove,
                    page: page,
                    templates: templates,
                    numberFocus: numberFocus,
                    onSecret: onSecret,
                    onRequestRemove: onRequestRemove
                )
            }
            let canAdd = !page.saving && entries.count < maxItems
            Button {
                page.addEntry(to: field.name)
            } label: {
                Label {
                    Text("Add entry").lineLimit(1).minimumScaleFactor(0.8)
                } icon: {
                    ThemedIcon(Theme.Icon.add)
                }
            }
            .themedButton()
            .disabled(!canAdd)
            .opacity(canAdd ? 1 : Theme.Fade.unavailable)
            .accessibilityIdentifier("plugin-entry-add-\(field.name)")
        } header: {
            SettingsHeading(title)
                .accessibilityIdentifier("plugin-entry-list-\(field.name)")
        } footer: {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text("Each entry has its own secret, stored separately and never shown again.")
                if clearsOnRebind {
                    Text("Saving a changed address clears that entry's secret.")
                }
            }
            .accessibilityIdentifier("plugin-entry-hint-\(field.name)")
        }
    }
}

/// One entry inside an extension entry list: its display title, secret state, write-only Secret button, confirmed
/// remove button, and declared flat text fields. Host-owned `_id` and local keys never render as editable fields.
private struct ExtensionEntryCardRow: View {
    var listField: ExtensionField
    var rowKey: String
    var index: Int
    var entry: [String: JSONValue]
    var subFields: [ExtensionField]
    var canRemove: Bool
    @Bindable var page: ExtensionModel
    var templates: [ExtensionTextTemplate]
    var numberFocus: FocusState<String?>.Binding
    var onSecret: (ExtensionSecret, String) -> Void
    var onRequestRemove: (PendingEntryRemoval) -> Void

    var body: some View {
        let entryTitle = ExtensionText.entryTitle(list: listField, entry: entry, index: index)
        let savedID = ExtensionModel.entryID(of: entry)
        let secret = savedID.flatMap { id in
            page.detail?.secrets.first { $0.name == id && $0.list == listField.name }
        }
        let secretStatus = secret.map(ExtensionText.secretState)
            ?? String(localized: "Save this entry before setting its secret.")
        let refDetail = secret?.reference.map { $0.title?.isEmpty == false ? $0.title! : $0.reference }
        let secretNeedsAttention = secret.map { !$0.set && $0.required == true } ?? false
        let idError = page.fieldError([listField.name, String(index), "_id"])

        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .top, spacing: Theme.Space.s) {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(entryTitle)
                        .font(Theme.Font.bodyStrong)
                        .lineLimit(2)
                    Text(secretStatus)
                        .font(Theme.Font.label)
                        .foregroundStyle(secretNeedsAttention ? Theme.cautionText : Color.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    if let refDetail {
                        Text(verbatim: refDetail)
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)

                Button(role: .destructive) {
                    onRequestRemove(PendingEntryRemoval(listName: listField.name, key: rowKey, title: entryTitle))
                } label: {
                    ThemedIcon(Theme.Icon.close)
                        .foregroundStyle(canRemove ? Theme.danger : Color.secondary)
                        .tapTargetFrame()
                        .contentShape(.rect)
                }
                .buttonStyle(.borderless)
                .disabled(!canRemove)
                .opacity(canRemove ? 1 : Theme.Fade.unavailable)
                .accessibilityLabel(String(localized: "Remove \(entryTitle)"))
                .accessibilityIdentifier("plugin-entry-remove-\(rowKey)")
            }

            let canEditSecret = secret != nil && !page.saving
            Button {
                if let secret { onSecret(secret, entryTitle) }
            } label: {
                Label {
                    Text("Secret").lineLimit(1).minimumScaleFactor(0.8)
                } icon: {
                    ThemedIcon(Theme.Icon.credentials)
                }
            }
            .themedButton()
            .disabled(!canEditSecret)
            .opacity(canEditSecret ? 1 : Theme.Fade.unavailable)
            .accessibilityIdentifier("plugin-entry-secret-\(rowKey)")

            if let idError {
                Text(idError)
                    .font(Theme.Font.label)
                    .foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("plugin-field-\(listField.name)[\(index)]._id")
            }

            Divider()

            ForEach(subFields) { subField in
                ExtensionFieldRow(
                    field: subField,
                    path: [listField.name, String(index), subField.name],
                    page: page,
                    templates: templates,
                    numberFocus: numberFocus
                )
            }
        }
        .padding(.vertical, Theme.Space.xs)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("plugin-entry-card-\(rowKey)")
    }
}
