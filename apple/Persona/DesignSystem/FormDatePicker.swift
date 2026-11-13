import SwiftUI

/// A form's date row: a date, or a date and a time, with its label (#6108). At the usual text sizes it is the system's
/// compact picker beside its label, as a plain `DatePicker`. At the accessibility sizes that picker's date and time are
/// wider than the row, and the label and both ends were cut off: there the label has a line of its own and the date is
/// a wheel under it, across the row. The wheel's words don't grow with the text size; that is the system's wheel.
/// A sheet that holds one opens full height at those sizes (`formSheetDetents`). `identifier` is the picker's
/// accessibility identifier, on the picker itself at every size.
struct FormDatePicker: View {
    @Environment(\.dynamicTypeSize) private var typeSize
    private let title: LocalizedStringKey
    @Binding private var selection: Date
    private let range: PartialRangeFrom<Date>?
    private let components: DatePickerComponents
    private let identifier: String

    init(_ title: LocalizedStringKey, selection: Binding<Date>, in range: PartialRangeFrom<Date>? = nil,
         displayedComponents: DatePickerComponents, identifier: String) {
        self.title = title
        _selection = selection
        self.range = range
        self.components = displayedComponents
        self.identifier = identifier
    }

    var body: some View {
        #if os(iOS)
        if typeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                // the wheel says it to VoiceOver
                Text(title)
                    .accessibilityHidden(true)
                picker
                    .labelsHidden()
                    .datePickerStyle(.wheel)
                    .frame(maxWidth: .infinity)
                    .accessibilityIdentifier(identifier)
            }
        } else {
            picker.accessibilityIdentifier(identifier)
        }
        #else
        picker.accessibilityIdentifier(identifier)
        #endif
    }

    @ViewBuilder private var picker: some View {
        if let range {
            DatePicker(title, selection: $selection, in: range, displayedComponents: components)
        } else {
            DatePicker(title, selection: $selection, displayedComponents: components)
        }
    }
}

extension View {
    /// A form sheet's detents when it holds a `FormDatePicker`: `detents` at the usual text sizes, full height at the
    /// accessibility sizes, where the wheel and the large words need it.
    func formSheetDetents(_ detents: Set<PresentationDetent>) -> some View {
        modifier(FormSheetDetents(detents: detents))
    }
}

private struct FormSheetDetents: ViewModifier {
    @Environment(\.dynamicTypeSize) private var typeSize
    let detents: Set<PresentationDetent>

    func body(content: Content) -> some View {
        content.presentationDetents(typeSize.isAccessibilitySize ? [.large] : detents)
    }
}
