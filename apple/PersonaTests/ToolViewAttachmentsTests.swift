import Foundation
import PersonaWire
import Testing
@testable import Persona

/// Out-of-band `ToolViews` extraction (`ToolResult.tool_views` and `Fold.tool_views`) and inline chat placement
/// between the collapsed work summary and final reply (#14730).
@MainActor
struct ToolViewAttachmentsTests {
    private static func mapView(title: String = "Route to Home", routeID: String = "r1") -> ExtensionView {
        ExtensionView(
            title: title,
            sections: [
                ExtensionSection(items: [
                    .map(ExtensionMapComponent(
                        key: routeID,
                        alt: title,
                        markers: [
                            ExtensionMapMarker(id: "o", lat: 39.9, lon: 116.4, kind: ExtensionMapMarkerKind.origin, label: "Start"),
                            ExtensionMapMarker(id: "d", lat: 39.95, lon: 116.45, kind: ExtensionMapMarkerKind.destination, label: "Home"),
                        ],
                        routes: [
                            ExtensionMapRoute(
                                id: routeID,
                                label: "Driving",
                                mode: ExtensionMapTravelMode.driving,
                                distanceMeters: 6200,
                                durationSeconds: 900,
                                selected: true,
                                points: [
                                    ExtensionMapCoordinate(lat: 39.9, lon: 116.4),
                                    ExtensionMapCoordinate(lat: 39.95, lon: 116.45),
                                ]
                            ),
                        ]
                    )),
                ]),
            ],
            fallback: "\(title) fallback"
        )
    }

    private func item(_ seq: Int, _ kind: ItemKind, role: String = "assistant", text: String? = nil,
                      tool: String? = nil, id: String? = nil, toolViews: [ExtensionView]? = nil,
                      fold: Fold? = nil) -> Item {
        var it = Item(id: "\(seq)#0", seq: seq, ts: nil, role: role, kind: kind, text: text,
                      toolUseID: id, toolName: tool)
        it.toolViews = toolViews
        it.fold = fold
        return it
    }

    private func blocks(_ items: [Item]) -> [ChatBlock] { ChatStore.rebuild(items, settled: nil).blocks }

    private func kinds(_ lines: [ChatLine]) -> [String] {
        lines.map { line in
            switch line.content {
            case .row(.user): "user"
            case .row(.assistant): "answer"
            case .work: "work"
            case .viewed: "viewed"
            case .toolViews: "toolViews"
            default: "other"
            }
        }
    }

    private func views(in lines: [ChatLine]) -> [ExtensionView] {
        lines.compactMap { if case .toolViews(_, let v) = $0.content { v } else { nil } }.flatMap { $0 }
    }

    @Test func liveToolResultViewsSitBetweenWorkAndAnswerInOrderAndDeduplicate() {
        let v1 = Self.mapView(title: "First", routeID: "r1")
        let v2 = Self.mapView(title: "Second", routeID: "r2")
        let items = [
            item(1, .text, role: "user", text: "Plan route home"),
            item(2, .toolUse, tool: "mcp__amap__maps_direction_driving", id: "c1"),
            item(3, .toolResult, role: "user", text: "ok", id: "c1", toolViews: [v1]),
            item(4, .toolUse, tool: "mcp__amap__maps_direction_transit", id: "c2"),
            item(5, .toolResult, role: "user", text: "ok", id: "c2", toolViews: [v1, v2]),
            item(6, .text, text: "Here are the routes."),
        ]

        let builtBlocks = blocks(items)
        let lines = ChatLines(openWork: []).build(builtBlocks, live: nil)
        #expect(kinds(lines) == ["user", "work", "toolViews", "answer"])
        let extracted = views(in: lines)
        #expect(extracted == [v1, v2])
    }

    @Test func foldCarryingToolViewsMatchesUnfoldedWorkAndPreservesLiveLineID() {
        let v1 = Self.mapView(title: "Folded Map", routeID: "r-fold")
        let running = [
            item(1, .text, role: "user", text: "Check commute"),
            item(2, .toolUse, tool: "mcp__amap__maps_direction_driving", id: "c1"),
            item(3, .toolResult, role: "user", text: "done", id: "c1", toolViews: [v1]),
            item(4, .text, text: "Working on alternatives..."),
            item(5, .toolUse, tool: "mcp__amap__maps_direction_walking", id: "c2"),
        ]
        let runningBlocks = blocks(running)
        guard case .turn(let t)? = runningBlocks.last else { Issue.record("missing turn"); return }
        let liveLines = ChatLines(openWork: []).build(runningBlocks, live: t.id)
        #expect(kinds(liveLines) == ["user", "work", "toolViews", "answer"])

        let v2 = Self.mapView(title: "Walk Option", routeID: "r-walk")
        let settled = running + [
            item(6, .toolResult, role: "user", text: "done", id: "c2", toolViews: [v2]),
            item(7, .text, text: "Both routes are ready."),
        ]
        let updatedLines = ChatLines(openWork: []).build(blocks(settled), live: t.id)
        #expect(views(in: updatedLines) == [v1, v2])
        #expect(updatedLines.first { if case .toolViews = $0.content { true } else { false } }?.id
                == liveLines.first { if case .toolViews = $0.content { true } else { false } }?.id)

        // Folded transcript produces the same extracted views.
        let fold = Fold(items: 5, tools: 2, thoughts: 0, until: nil, throughSeq: 6, throughID: "6#0",
                        toolViews: [
                            FoldToolView(itemID: "3#0", toolUseID: "c1", tool: "mcp__amap__maps_direction_driving", view: v1),
                            FoldToolView(itemID: "6#0", toolUseID: "c2", tool: "mcp__amap__maps_direction_walking", view: v2),
                        ])
        let foldedItem = item(2, .fold, fold: fold)
        let foldedLines = ChatLines(openWork: []).build(blocks([settled[0], foldedItem, settled.last!]), live: nil)
        #expect(kinds(foldedLines) == ["user", "work", "toolViews", "answer"])
        #expect(views(in: foldedLines) == [v1, v2])
    }

    @Test func emptyOrUnknownOnlyViewsAreFilteredFromDrawing() {
        let unknownOnly = ExtensionView(
            sections: [ExtensionSection(items: [.unknown(type: "future_widget")])],
            fallback: "Unknown only"
        )
        #expect(!ToolViews.hasDrawableContent(unknownOnly))
        #expect(ToolViews.hasDrawableContent(Self.mapView()))
    }
}
