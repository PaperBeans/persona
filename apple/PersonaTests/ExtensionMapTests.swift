import Foundation
import MapKit
import PersonaWire
import SwiftUI
import Testing
@testable import Persona

/// UI version 12 `map` geometry, formatting, HTTPS link sanitization, companion list rules, and synchronous
/// picture/Share-Card rendering (#14730).
@MainActor
struct ExtensionMapTests {
    private static let sampleMarkers: [ExtensionMapMarker] = [
        ExtensionMapMarker(id: "origin", lat: 39.9087, lon: 116.3975, kind: ExtensionMapMarkerKind.origin, label: "Home"),
        ExtensionMapMarker(id: "dest", lat: 39.9929, lon: 116.3340, kind: ExtensionMapMarkerKind.destination, label: "Office", subtitle: "Gate 2"),
    ]

    private static let sampleRoutes: [ExtensionMapRoute] = [
        ExtensionMapRoute(
            id: "fast",
            label: "Fastest",
            mode: ExtensionMapTravelMode.driving,
            distanceMeters: 12_400,
            durationSeconds: 1380,
            summary: "Via Ring Rd",
            selected: true,
            tone: "positive",
            points: [
                ExtensionMapCoordinate(lat: 39.9087, lon: 116.3975),
                ExtensionMapCoordinate(lat: 39.9500, lon: 116.3650),
                ExtensionMapCoordinate(lat: 39.9929, lon: 116.3340),
            ]
        ),
        ExtensionMapRoute(
            id: "alt",
            label: "Transit",
            mode: ExtensionMapTravelMode.transit,
            distanceMeters: 13_800,
            durationSeconds: 1920,
            summary: "Line 4",
            selected: false,
            points: [
                ExtensionMapCoordinate(lat: 39.9087, lon: 116.3975),
                ExtensionMapCoordinate(lat: 39.9400, lon: 116.3800),
                ExtensionMapCoordinate(lat: 39.9929, lon: 116.3340),
            ]
        ),
    ]

    @Test func coordinateValidationRejectsInvalidOrOutOfRangePoints() {
        #expect(ExtensionMapGeometry.isValidCoordinate(39.9, 116.4))
        #expect(!ExtensionMapGeometry.isValidCoordinate(91, 116.4))
        #expect(!ExtensionMapGeometry.isValidCoordinate(39.9, -181))
        #expect(!ExtensionMapGeometry.isValidCoordinate(.nan, 116.4))
        #expect(!ExtensionMapGeometry.isValidCoordinate(39.9, .infinity))

        let badRoute = ExtensionMapRoute(
            id: "bad",
            points: [
                ExtensionMapCoordinate(lat: 39.9, lon: 116.4),
                ExtensionMapCoordinate(lat: 99, lon: 116.4),
            ]
        )
        #expect(ExtensionMapGeometry.validPoints(badRoute).isEmpty)
    }

    @Test func regionHonorsExplicitViewportAndAutoFitsSingleAndMultiPointMaps() {
        let mapWithViewport = ExtensionMapComponent(
            alt: "Viewport test",
            viewport: ExtensionMapViewport(
                center: ExtensionMapCoordinate(lat: 31.2304, lon: 121.4737),
                spanLat: 0.05,
                spanLon: 0.08
            ),
            markers: Self.sampleMarkers
        )
        let vpRegion = ExtensionMapGeometry.region(for: mapWithViewport)
        #expect(abs(vpRegion.center.latitude - 31.2304) < 0.0001)
        #expect(abs(vpRegion.center.longitude - 121.4737) < 0.0001)
        #expect(abs(vpRegion.span.latitudeDelta - 0.05) < 0.0001)
        #expect(abs(vpRegion.span.longitudeDelta - 0.08) < 0.0001)

        // Single marker clamps minimum street context span.
        let singlePoint = ExtensionMapComponent(
            alt: "One pin",
            markers: [ExtensionMapMarker(id: "only", lat: 39.9, lon: 116.4, label: "Pin")]
        )
        let singleRegion = ExtensionMapGeometry.region(for: singlePoint)
        #expect(singleRegion.span.latitudeDelta >= Theme.Map.minSpanDegrees)
        #expect(singleRegion.span.longitudeDelta >= Theme.Map.minSpanDegrees)

        // Auto-fit across route + markers frames all coordinates inside the fitted region.
        let routeMap = ExtensionMapComponent(
            alt: "Routes",
            markers: Self.sampleMarkers,
            routes: Self.sampleRoutes
        )
        let fitRegion = ExtensionMapGeometry.region(for: routeMap)
        for pt in Self.sampleMarkers {
            let norm = ExtensionMapGeometry.normalizedPoint(
                CLLocationCoordinate2D(latitude: pt.lat, longitude: pt.lon),
                in: fitRegion
            )
            #expect((0...1).contains(norm.x) && (0...1).contains(norm.y))
        }
    }

    @Test func routeDefaultsSelectionLinksAndListPolicy() {
        let map = ExtensionMapComponent(
            key: "commute",
            title: "Morning Commute",
            alt: "Route to Office",
            markers: Self.sampleMarkers,
            routes: Self.sampleRoutes,
            links: [
                ExtensionMapLink(title: "Open in Amap", url: "https://uri.amap.com/navigation?to=116.3340,39.9929"),
                ExtensionMapLink(title: "Blocked", url: "javascript:alert(1)"),
                ExtensionMapLink(title: "Insecure", url: "http://example.com"),
            ]
        )
        #expect(ExtensionMapGeometry.defaultSelectedRouteID(in: map) == "fast")
        let links = ExtensionMapGeometry.sanitizedLinks(map.links)
        #expect(links.count == 1 && links[0].title == "Open in Amap")
        #expect(ExtensionMapGeometry.shouldShowList(for: map, compact: false))
        #expect(!ExtensionMapGeometry.shouldShowList(for: map, compact: true))

        var forcedCompactList = map
        forcedCompactList.showList = true
        #expect(ExtensionMapGeometry.shouldShowList(for: forcedCompactList, compact: true))

        var suppressedPageList = map
        suppressedPageList.showList = false
        #expect(!ExtensionMapGeometry.shouldShowList(for: suppressedPageList, compact: false))
    }

    @Test func formattingAndActionCollectionCoverModesAndMarkerActions() {
        #expect(ExtensionMapFormatting.modeIcon(ExtensionMapTravelMode.driving) == Theme.Icon.pluginCar)
        #expect(ExtensionMapFormatting.modeIcon(ExtensionMapTravelMode.walking) == Theme.Icon.travelWalk)
        #expect(ExtensionMapFormatting.modeIcon(ExtensionMapTravelMode.transit) == Theme.Icon.travelTransit)
        #expect(ExtensionMapFormatting.modeIcon(ExtensionMapTravelMode.cycling) == Theme.Icon.travelCycle)

        let meta = ExtensionMapFormatting.routeMetadata(Self.sampleRoutes[0])
        #expect(meta != nil && !(meta ?? "").isEmpty)

        var actionMarker = Self.sampleMarkers[1]
        actionMarker.action = ExtensionAction(id: "inspect-stop", reply: "Inspect Gate 2")
        let view = ExtensionView(
            title: "Route",
            sections: [ExtensionSection(items: [
                .map(ExtensionMapComponent(alt: "Route", markers: [Self.sampleMarkers[0], actionMarker], routes: Self.sampleRoutes)),
            ])],
            fallback: "Route overview"
        )
        let actions = AssistantViewModel.actions(view)
        #expect(actions.map(\.id) == ["inspect-stop"])
    }

    @Test(arguments: [false, true]) func shareCardDrawsMapSynchronously(dark: Bool) throws {
        let mapJSON = #"""
        ```persona-view
        {
          "title": "Evening Route",
          "fallback": "Evening drive to Home: 23 min, 12.4 km.",
          "sections": [{
            "items": [{
              "type": "map",
              "alt": "Evening drive route",
              "markers": [
                {"id": "o", "lat": 39.9929, "lon": 116.3340, "kind": "origin", "label": "Office"},
                {"id": "d", "lat": 39.9087, "lon": 116.3975, "kind": "destination", "label": "Home"}
              ],
              "routes": [{
                "id": "r1",
                "mode": "driving",
                "distance_meters": 12400,
                "duration_seconds": 1380,
                "summary": "Fastest",
                "selected": true,
                "points": [
                  {"lat": 39.9929, "lon": 116.3340},
                  {"lat": 39.9087, "lon": 116.3975}
                ]
              }]
            }]
          }]
        }
        ```
        """#
        let parts = ShareCardParts.of("Here is tonight's route.\n\n" + mapJSON)
        #expect(parts.contains { if case .card = $0 { true } else { false } })

        let message = ShareMessage(text: "Here is tonight's route.\n\n" + mapJSON, isUser: false, source: "", date: nil, agent: "Persona")
        let rendered = try #require(MessageShareCard.render(message, dark: dark))
        #expect(rendered.size.width >= ShareCardWidth.base && rendered.size.height > 0)
    }
}
