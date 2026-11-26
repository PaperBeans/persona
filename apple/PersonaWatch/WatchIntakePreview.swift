#if DEBUG
import SwiftUI
import WidgetKit

/// Synthetic selection only: the same complication view as WidgetKit, without pairing or cache fixtures.
struct WatchIntakePreview: View {
    private let entry = WatchIntakeEntry(date: .now,
        reading: WatchIntakeReading(water: .init(today: 750, goal: 2000), protein: .init(today: 36, goal: 60),
                                    nextDay: .distantFuture, zone: "UTC", items: []), paired: true)
    var body: some View {
        List {
            ForEach(WatchIntakeKind.allCases, id: \.rawValue) { kind in
                Section {
                    WatchIntakeComplicationView(entry: entry, kind: kind, family: .accessoryRectangular)
                        .padding(.vertical, Theme.Space.m)
                } header: { Text(kind.title) }
            }
        }
        .navigationTitle(Text("Health"))
    }
}
#endif
