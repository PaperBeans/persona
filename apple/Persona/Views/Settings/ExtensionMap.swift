#if os(iOS)
import CoreGraphics
import MapKit
import PersonaWire
import SwiftUI
import UIKit

/// Coordinate validation, viewport framing, route fitting, and normalized vector projection for UI version 12 `map`.
nonisolated enum ExtensionMapGeometry {
    static func isValidCoordinate(_ lat: Double, _ lon: Double) -> Bool {
        lat.isFinite && lon.isFinite && (-90...90).contains(lat) && (-180...180).contains(lon)
    }

    static func validCoordinate(_ marker: ExtensionMapMarker) -> CLLocationCoordinate2D? {
        guard isValidCoordinate(marker.lat, marker.lon) else { return nil }
        return CLLocationCoordinate2D(latitude: marker.lat, longitude: marker.lon)
    }

    static func validPoints(_ route: ExtensionMapRoute) -> [CLLocationCoordinate2D] {
        let points = route.points.compactMap { point -> CLLocationCoordinate2D? in
            guard isValidCoordinate(point.lat, point.lon) else { return nil }
            return CLLocationCoordinate2D(latitude: point.lat, longitude: point.lon)
        }
        return points.count == route.points.count ? points : []
    }

    static func defaultSelectedRouteID(in map: ExtensionMapComponent) -> String? {
        let routes = map.routes ?? []
        return routes.first(where: { $0.selected == true })?.id ?? routes.first?.id
    }

    static func hasRenderableGeometry(_ map: ExtensionMapComponent) -> Bool {
        if let vp = map.viewport, isValidCoordinate(vp.center.lat, vp.center.lon) { return true }
        if (map.markers ?? []).contains(where: { validCoordinate($0) != nil }) { return true }
        return (map.routes ?? []).contains(where: { validPoints($0).count >= 2 })
    }

    /// Fitting region in WGS-84. Explicit valid `viewport` wins when no route override is requested; otherwise
    /// fits valid points of the selected route (or all routes when `selectedRouteID` is nil) plus all valid markers.
    static func region(for map: ExtensionMapComponent, selectedRouteID: String? = nil) -> MKCoordinateRegion {
        if selectedRouteID == nil,
           let vp = map.viewport,
           isValidCoordinate(vp.center.lat, vp.center.lon) {
            let latDelta = min(160, max(vp.spanLat.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? Theme.Map.minSpanDegrees,
                                        Theme.Map.minSpanDegrees))
            let lonDelta = min(350, max(vp.spanLon.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? Theme.Map.minSpanDegrees,
                                        Theme.Map.minSpanDegrees))
            return MKCoordinateRegion(
                center: CLLocationCoordinate2D(latitude: vp.center.lat, longitude: vp.center.lon),
                span: MKCoordinateSpan(latitudeDelta: latDelta, longitudeDelta: lonDelta)
            )
        }

        let routes = map.routes ?? []
        let chosenRoutes: [ExtensionMapRoute]
        if let selectedRouteID, let matched = routes.first(where: { $0.id == selectedRouteID }),
           !validPoints(matched).isEmpty {
            chosenRoutes = [matched]
        } else {
            chosenRoutes = routes
        }

        var coords = (map.markers ?? []).compactMap(validCoordinate)
        coords += chosenRoutes.flatMap(validPoints)

        if coords.isEmpty, let vp = map.viewport, isValidCoordinate(vp.center.lat, vp.center.lon) {
            coords.append(CLLocationCoordinate2D(latitude: vp.center.lat, longitude: vp.center.lon))
        }

        guard !coords.isEmpty else {
            return MKCoordinateRegion(
                center: CLLocationCoordinate2D(latitude: 0, longitude: 0),
                span: MKCoordinateSpan(latitudeDelta: 1, longitudeDelta: 1)
            )
        }

        let minLat = coords.map(\.latitude).min()!
        let maxLat = coords.map(\.latitude).max()!
        let minLon = coords.map(\.longitude).min()!
        let maxLon = coords.map(\.longitude).max()!
        let center = CLLocationCoordinate2D(latitude: (minLat + maxLat) / 2, longitude: (minLon + maxLon) / 2)

        let metersPerDegree = 111_000.0
        let minDegFromMeters = Theme.Map.minSpanMeters / metersPerDegree
        let minLatDelta = max(Theme.Map.minSpanDegrees, minDegFromMeters)
        let cosLat = max(abs(cos(center.latitude * .pi / 180)), 0.15)
        let minLonDelta = min(350, max(Theme.Map.minSpanDegrees, minDegFromMeters / cosLat))

        let latDelta = min(160, max((maxLat - minLat) * Theme.Map.fitPadding, minLatDelta))
        let lonDelta = min(350, max((maxLon - minLon) * Theme.Map.fitPadding, minLonDelta))
        return MKCoordinateRegion(
            center: center,
            span: MKCoordinateSpan(latitudeDelta: latDelta, longitudeDelta: lonDelta)
        )
    }

    /// Projects a WGS-84 coordinate into `[0, 1]^2` (top-left origin) within `region` for fallback vector drawing.
    static func normalizedPoint(_ coord: CLLocationCoordinate2D, in region: MKCoordinateRegion) -> CGPoint {
        let halfLat = max(region.span.latitudeDelta / 2, Theme.Map.minSpanDegrees / 2)
        let halfLon = max(region.span.longitudeDelta / 2, Theme.Map.minSpanDegrees / 2)
        let x = 0.5 + (coord.longitude - region.center.longitude) / (halfLon * 2)
        let y = 0.5 - (coord.latitude - region.center.latitude) / (halfLat * 2)
        return CGPoint(x: x, y: y)
    }

    static func regionsApproximatelyEqual(_ a: MKCoordinateRegion, _ b: MKCoordinateRegion) -> Bool {
        let latEps = max(0.0001, b.span.latitudeDelta * 0.02)
        let lonEps = max(0.0001, b.span.longitudeDelta * 0.02)
        return abs(a.center.latitude - b.center.latitude) <= latEps
            && abs(a.center.longitude - b.center.longitude) <= lonEps
            && abs(a.span.latitudeDelta - b.span.latitudeDelta) <= latEps
            && abs(a.span.longitudeDelta - b.span.longitudeDelta) <= lonEps
    }

    static func aspectRatio(for aspect: String?) -> CGFloat? {
        switch aspect {
        case "square": 1
        case "landscape": 4 / 3
        case "wide": 16 / 9
        case "portrait": 3 / 4
        default: nil
        }
    }

    /// HTTPS-only external links allowed alongside or inside a map.
    static func sanitizedLinks(_ links: [ExtensionMapLink]?) -> [(title: String, url: URL)] {
        (links ?? []).compactMap { entry in
            guard let url = URL(string: entry.url),
                  url.scheme?.lowercased() == "https",
                  url.host()?.isEmpty == false else { return nil }
            let label = entry.title.trimmingCharacters(in: .whitespacesAndNewlines)
            return (label.isEmpty ? (url.host() ?? entry.url) : label, url)
        }
    }

    static func shouldShowList(for map: ExtensionMapComponent, compact: Bool) -> Bool {
        if let explicit = map.showList { return explicit }
        if compact { return false }
        return (map.routes?.count ?? 0) > 1 || (map.markers?.count ?? 0) > 1
    }
}

/// Travel mode, distance/duration formatting, and semantic marker/route styling for `ExtensionMap`.
enum ExtensionMapFormatting {
    static func modeIcon(_ mode: String?) -> String {
        switch mode {
        case ExtensionMapTravelMode.driving: Theme.Icon.pluginCar
        case ExtensionMapTravelMode.walking: Theme.Icon.travelWalk
        case ExtensionMapTravelMode.transit: Theme.Icon.travelTransit
        case ExtensionMapTravelMode.cycling: Theme.Icon.travelCycle
        default: Theme.Icon.directions
        }
    }

    static func modeLabel(_ mode: String?) -> String {
        switch mode {
        case ExtensionMapTravelMode.driving: String(localized: "Driving [Place Map]")
        case ExtensionMapTravelMode.walking: String(localized: "Walking [Place Map]")
        case ExtensionMapTravelMode.transit: String(localized: "Transit [Place Map]")
        case ExtensionMapTravelMode.cycling: String(localized: "Cycling [Extension Map]")
        default: String(localized: "Route [Extension Map]")
        }
    }

    static func distance(_ meters: Int) -> String {
        Measurement(value: Double(meters), unit: UnitLength.meters)
            .formatted(.measurement(width: .abbreviated, usage: .road, numberFormatStyle: .number.precision(.fractionLength(0...1))))
    }

    static func duration(_ seconds: Int) -> String {
        Duration.seconds(max(60, seconds)).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated,
                                                             fractionalPart: .hide(rounded: .toNearestOrAwayFromZero)))
    }

    static func routeTitle(_ route: ExtensionMapRoute, index: Int) -> String {
        if let label = route.label, !label.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return label
        }
        if let mode = route.mode, !mode.isEmpty {
            return modeLabel(mode)
        }
        return String(localized: "Route \(index + 1) [Extension Map]")
    }

    static func routeMetadata(_ route: ExtensionMapRoute) -> String? {
        var parts: [String] = []
        if let mode = route.mode, !mode.isEmpty {
            parts.append(modeLabel(mode))
        }
        if let seconds = route.durationSeconds, seconds > 0 {
            parts.append(duration(seconds))
        }
        if let meters = route.distanceMeters, meters >= 0 {
            parts.append(distance(meters))
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    static func resolveColor(tone: String?, color: String?, fallback: Color = Theme.accent) -> Color {
        if let tone, tone == "positive" || tone == "caution" || tone == "danger" {
            return ExtensionUIStyle.tone(tone)
        }
        if let named = ExtensionUIStyle.color(color) {
            return named
        }
        return fallback
    }

    static func markerColor(_ marker: ExtensionMapMarker) -> Color {
        let roleDefault: Color = switch marker.kind {
        case ExtensionMapMarkerKind.origin: Theme.positive
        case ExtensionMapMarkerKind.user: Theme.Hue.blue
        default: Theme.accent
        }
        return resolveColor(tone: marker.tone, color: marker.color, fallback: roleDefault)
    }

    static func markerSymbol(_ marker: ExtensionMapMarker) -> String {
        if let icon = marker.icon, !icon.isEmpty {
            return ExtensionUIStyle.icon(icon)
        }
        switch marker.kind {
        case ExtensionMapMarkerKind.user: return Theme.Icon.currentLocation
        case ExtensionMapMarkerKind.origin: return Theme.Icon.run
        case ExtensionMapMarkerKind.destination, ExtensionMapMarkerKind.pin, ExtensionMapMarkerKind.waypoint, nil:
            return Theme.Icon.place
        default: return Theme.Icon.place
        }
    }
}

/// Cached `MKMapSnapshotter` bitmap and projected screen-space coordinates for compact/static previews.
final class ExtensionMapSnapshot: @unchecked Sendable {
    let image: UIImage
    let routes: [String: [CGPoint]]
    let markers: [String: CGPoint]

    init(image: UIImage, routes: [String: [CGPoint]], markers: [String: CGPoint]) {
        self.image = image
        self.routes = routes
        self.markers = markers
    }

    nonisolated(unsafe) private static let cache: NSCache<NSString, ExtensionMapSnapshot> = {
        let c = NSCache<NSString, ExtensionMapSnapshot>()
        c.totalCostLimit = 24 << 20
        return c
    }()

    struct Request: Hashable {
        var map: ExtensionMapComponent
        var selectedRouteID: String?
        var dark: Bool
        var width: Int
        var height: Int

        var cacheKey: NSString {
            "\(map.hashValue)|\(selectedRouteID ?? "")|\(width)x\(height)|\(dark ? "d" : "l")" as NSString
        }
    }

    static func cached(_ key: NSString) -> ExtensionMapSnapshot? { cache.object(forKey: key) }

    static func make(_ map: ExtensionMapComponent, region: MKCoordinateRegion, size: CGSize,
                     dark: Bool, scale: CGFloat, key: NSString) async -> ExtensionMapSnapshot? {
        if let hit = cache.object(forKey: key) { return hit }
        let options = MKMapSnapshotter.Options()
        options.region = region
        options.size = size
        options.traitCollection = UITraitCollection { traits in
            traits.userInterfaceStyle = dark ? .dark : .light
            traits.displayScale = scale
        }
        options.pointOfInterestFilter = .excludingAll
        guard let shot = try? await MKMapSnapshotter(options: options).start() else { return nil }
        func spot(_ coord: CLLocationCoordinate2D) -> CGPoint {
            let p = shot.point(for: coord)
            return CGPoint(x: p.x / max(size.width, 1), y: p.y / max(size.height, 1))
        }
        var projectedRoutes: [String: [CGPoint]] = [:]
        for route in map.routes ?? [] {
            let pts = ExtensionMapGeometry.validPoints(route)
            if pts.count >= 2 {
                projectedRoutes[route.id] = pts.map(spot)
            }
        }
        var projectedMarkers: [String: CGPoint] = [:]
        for marker in map.markers ?? [] {
            if let coord = ExtensionMapGeometry.validCoordinate(marker) {
                projectedMarkers[marker.id] = spot(coord)
            }
        }
        let built = ExtensionMapSnapshot(image: shot.image, routes: projectedRoutes, markers: projectedMarkers)
        let w = Int(round(shot.image.size.width * shot.image.scale))
        let h = Int(round(shot.image.size.height * shot.image.scale))
        cache.setObject(built, forKey: key, cost: max(1, w * h * 4))
        return built
    }
}

/// UI version 12's declarative native `map` component (`type: "map"`).
/// Renders WGS-84 routes, markers, travel metadata, HTTPS links, and optional companion stop/route lists.
/// Compact cards (`overview_card`, assistant inline `tool_views`, `persona-view` cards) draw an immediate vector +
/// MapKit snapshot preview that expands into a full-screen interactive map unless `static == true`.
struct ExtensionMap: View {
    var map: ExtensionMapComponent
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?

    @Environment(\.pluginUICompact) private var compact
    @Environment(\.extensionPicture) private var picture
    @Environment(\.extensionCardInset) private var cardInset
    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var selectedRouteID: String?
    @State private var selectedMarkerID: String?
    @State private var camera: MapCameraPosition
    @State private var adjustedCamera = false
    @State private var fullScreen = false
    @State private var deferredLink: LinkTarget?
    @State private var deferredAction: (() -> Void)?
    @FocusState private var coverFocused: Bool

    init(map: ExtensionMapComponent, controller: (any ExtensionViewControlling)?, link: Binding<LinkTarget?>) {
        self.map = map
        self.controller = controller
        _link = link
        let initialRoute = ExtensionMapGeometry.defaultSelectedRouteID(in: map)
        _selectedRouteID = State(initialValue: initialRoute)
        _camera = State(initialValue: .region(ExtensionMapGeometry.region(for: map)))
    }

    private var isStatic: Bool { map.static == true }
    private var routes: [ExtensionMapRoute] { map.routes ?? [] }
    private var markers: [ExtensionMapMarker] { map.markers ?? [] }
    private var safeLinks: [ExtensionMapSafeLink] {
        ExtensionMapGeometry.sanitizedLinks(map.links).enumerated().map {
            ExtensionMapSafeLink(index: $0.offset, title: $0.element.title, url: $0.element.url)
        }
    }
    private var horizontalMargin: CGFloat { (!compact && cardInset == nil) ? Theme.Space.page : Theme.Space.flush }

    private var activeRoute: ExtensionMapRoute? {
        if let selectedRouteID, let matched = routes.first(where: { $0.id == selectedRouteID }) {
            return matched
        }
        return routes.first(where: { $0.selected == true }) ?? routes.first
    }

    private var activeMarker: ExtensionMapMarker? {
        guard let selectedMarkerID else { return nil }
        return markers.first { $0.id == selectedMarkerID }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let title = map.title, !title.isEmpty {
                Text(title)
                    .font(compact ? Theme.Font.secondaryStrong : Theme.Font.heading)
                    .foregroundStyle(Color.primary)
                    .lineLimit(2)
                    .padding(.horizontal, horizontalMargin)
                    .accessibilityAddTraits(.isHeader)
            }

            if ExtensionMapGeometry.hasRenderableGeometry(map) {
                mapSurface
                if !picture {
                    routeControls
                    selectedMarkerBanner
                    if ExtensionMapGeometry.shouldShowList(for: map, compact: compact) {
                        companionList
                    }
                    linksRow
                } else {
                    routeSummaryStrip
                }
            } else {
                fallbackView
            }

            if let caption = map.caption, !caption.isEmpty {
                Text(caption)
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, horizontalMargin)
            }
        }
        .dynamicTypeSize(...Theme.interfaceLargestTypeSize)
        .onChange(of: map) { _, updated in
            let fallbackRoute = ExtensionMapGeometry.defaultSelectedRouteID(in: updated)
            if let current = selectedRouteID,
               (updated.routes ?? []).contains(where: { $0.id == current }) {
                // Keep person's selected alternative route across live redraws.
            } else {
                selectedRouteID = fallbackRoute
            }
            if !adjustedCamera {
                camera = .region(ExtensionMapGeometry.region(for: updated))
            }
        }
        .fullScreenCover(isPresented: $fullScreen, onDismiss: handleCoverDismiss) {
            NavigationStack {
                ExtensionMapCover(
                    map: map,
                    selectedRouteID: $selectedRouteID,
                    selectedMarkerID: $selectedMarkerID,
                    camera: $camera,
                    adjustedCamera: $adjustedCamera,
                    controller: controller,
                    link: coverLinkBinding,
                    onReset: resetCamera,
                    onSelectRoute: selectRoute,
                    onDismissCover: { fullScreen = false },
                    onDeferAction: { action in
                        deferredAction = action
                        fullScreen = false
                    }
                )
                .dynamicTypeSize(...Theme.interfaceLargestTypeSize)
                .accessibilityIdentifier("extension-map-cover")
                .background(Theme.pageBackground)
                .navigationTitle(coverTitle)
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { SheetClose(id: "extension-map-close") { fullScreen = false } }
                .focusable()
                .focused($coverFocused)
                .onKeyPress(.escape) {
                    fullScreen = false
                    return .handled
                }
                .accessibilityAction(.escape) { fullScreen = false }
                .onAppear { coverFocused = true }
                .onChange(of: (controller as? ExtensionViewModel)?.pushed) { _, pushed in
                    if pushed != nil { fullScreen = false }
                }
                .onChange(of: (controller as? ExtensionViewModel)?.popped) { _, popped in
                    if popped == true { fullScreen = false }
                }
                .onChange(of: (controller as? ExtensionViewModel)?.confirmation != nil) { _, asking in
                    if asking { fullScreen = false }
                }
                .onChange(of: (controller as? AssistantViewModel)?.page) { _, _ in
                    fullScreen = false
                }
                .onChange(of: (controller as? AssistantViewModel)?.pending != nil) { _, pending in
                    if pending { fullScreen = false }
                }
            }
        }
    }

    @ViewBuilder private var mapSurface: some View {
        if compact || isStatic || picture {
            snapshotSurface
        } else {
            ExtensionMapInteractiveViewport(
                map: map,
                selectedRouteID: activeRoute?.id,
                selectedMarkerID: $selectedMarkerID,
                camera: $camera,
                adjustedCamera: $adjustedCamera,
                inCover: false,
                showsFullScreen: true,
                controller: controller,
                link: $link,
                onReset: resetCamera,
                onFullScreen: { fullScreen = true },
                onMarkerPush: { fullScreen = false },
                onDeferAction: { $0() }
            )
            .background(Theme.Map.ground)
            .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                    .stroke(Theme.cardStroke, lineWidth: Theme.Size.hairline)
            )
            .padding(.horizontal, horizontalMargin)
            .containerRelativeFrame(.vertical) { visible, _ in
                let scale = Theme.sizeScale(typeSize)
                let height = visible * Theme.Size.mapPageShare
                return min(max(height, Theme.Size.mapPageMinHeight * scale), Theme.Size.mapPageMaxHeight * scale)
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("extension-map")
        }
    }

    @ViewBuilder private var snapshotSurface: some View {
        let previewHeight = compact ? Theme.Size.mapPreviewHeight : Theme.Size.mapToolHeight
        let aspect = ExtensionMapGeometry.aspectRatio(for: map.aspect)
        let preview = ExtensionMapSnapshotView(
            map: map,
            selectedRouteID: activeRoute?.id,
            synchronous: picture
        )

        Group {
            if picture || isStatic {
                preview
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(map.alt.isEmpty ? String(localized: "Map [Extension Map]") : map.alt)
                    .accessibilityIdentifier(compact ? "extension-map-preview" : "extension-map")
            } else {
                ZStack {
                    preview
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                    Button {
                        fullScreen = true
                    } label: {
                        Color.clear.contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(String(localized: "Open Map Full Screen"))
                    .accessibilityValue(map.alt)
                    .accessibilityIdentifier("extension-map-preview")
                }
            }
        }
        .modifier(ExtensionMapFrameModifier(aspect: compact ? nil : aspect, height: previewHeight))
        .background(Theme.Map.ground)
        .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                .stroke(Theme.cardStroke, lineWidth: Theme.Size.hairline)
        )
        .padding(.horizontal, horizontalMargin)
    }

    @ViewBuilder private var routeControls: some View {
        if routes.count > 1 {
            ScrollView(.horizontal) {
                HStack(spacing: Theme.Space.s) {
                    ForEach(Array(routes.enumerated()), id: \.element.id) { index, route in
                        let selected = activeRoute?.id == route.id
                        Button {
                            selectRoute(route.id)
                        } label: {
                            HStack(spacing: Theme.Space.xs) {
                                ThemedIcon(ExtensionMapFormatting.modeIcon(route.mode))
                                    .font(Theme.Font.labelStrong)
                                Text(ExtensionMapFormatting.routeTitle(route, index: index))
                                    .font(Theme.Font.labelStrong)
                                    .lineLimit(1)
                                if let meta = ExtensionMapFormatting.routeMetadata(route) {
                                    Text(meta)
                                        .font(Theme.Font.label)
                                        .foregroundStyle(selected ? Theme.accent : .secondary)
                                        .lineLimit(1)
                                }
                            }
                            .padding(.horizontal, Theme.Space.m)
                            .padding(.vertical, Theme.Space.xs)
                            .background(selected ? Theme.accentTint : Theme.Fill.chip, in: .capsule)
                            .overlay(
                                Capsule().stroke(selected ? Theme.accent : Theme.cardStroke,
                                                 lineWidth: Theme.Size.hairline)
                            )
                            .foregroundStyle(selected ? Theme.accent : Color.primary)
                            .tapTargetHeight()
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("extension-map-route-\(route.id)")
                    }
                }
                .padding(.horizontal, horizontalMargin)
            }
            .scrollIndicators(.hidden)
        } else {
            routeSummaryStrip
        }
    }

    @ViewBuilder private var routeSummaryStrip: some View {
        if let route = activeRoute {
            let meta = ExtensionMapFormatting.routeMetadata(route)
            let summary = route.summary?.trimmingCharacters(in: .whitespacesAndNewlines)
            if meta != nil || (summary?.isEmpty == false) {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(ExtensionMapFormatting.modeIcon(route.mode))
                        .font(Theme.Font.labelStrong)
                        .foregroundStyle(ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color))
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        if let meta {
                            Text(meta)
                                .font(Theme.Font.captionMedium)
                                .foregroundStyle(Color.primary)
                        }
                        if let summary, !summary.isEmpty {
                            Text(summary)
                                .font(Theme.Font.label)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                    }
                    Spacer(minLength: Theme.Space.flush)
                }
                .padding(.horizontal, horizontalMargin)
                .accessibilityElement(children: .combine)
            }
        }
    }

    @ViewBuilder private var selectedMarkerBanner: some View {
        if let marker = activeMarker, !compact {
            ExtensionMapMarkerDetailCard(marker: marker, controller: controller, link: $link) {
                selectedMarkerID = nil
            }
            .padding(.horizontal, horizontalMargin)
        }
    }

    @ViewBuilder private var companionList: some View {
        if !markers.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                ForEach(markers, id: \.id) { marker in
                    ExtensionMapStopRow(
                        marker: marker,
                        selected: selectedMarkerID == marker.id,
                        controller: controller,
                        link: $link,
                        onSelect: {
                            withAnimation(reduceMotion ? nil : Theme.Motion.state) {
                                selectedMarkerID = marker.id
                            }
                            if let coord = ExtensionMapGeometry.validCoordinate(marker), !isStatic {
                                let base = ExtensionMapGeometry.region(for: map, selectedRouteID: activeRoute?.id)
                                camera = .region(MKCoordinateRegion(
                                    center: coord,
                                    span: MKCoordinateSpan(
                                        latitudeDelta: min(base.span.latitudeDelta, Theme.Map.minSpanDegrees * 2),
                                        longitudeDelta: min(base.span.longitudeDelta, Theme.Map.minSpanDegrees * 2)
                                    )
                                ))
                                adjustedCamera = true
                            }
                        }
                    )
                }
            }
            .padding(.horizontal, horizontalMargin)
        }
    }

    @ViewBuilder private var linksRow: some View {
        ExtensionMapLinksStrip(
            links: safeLinks,
            horizontalPadding: horizontalMargin,
            identifierPrefix: "extension-map-link"
        ) { url in
            link = LinkTarget(url: url)
        }
    }

    private var fallbackView: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.map)
                    .font(Theme.Font.secondaryStrong)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text(map.alt.isEmpty ? String(localized: "Map [Extension Map]") : map.alt)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(Theme.Space.m)
        .background(Theme.Map.ground, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .padding(.horizontal, horizontalMargin)
    }

    private func selectRoute(_ id: String) {
        withAnimation(reduceMotion ? nil : Theme.Motion.state) {
            selectedRouteID = id
            adjustedCamera = false
            camera = .region(ExtensionMapGeometry.region(for: map, selectedRouteID: id))
        }
    }

    private func resetCamera() {
        withAnimation(reduceMotion ? nil : Theme.Motion.state) {
            adjustedCamera = false
            selectedMarkerID = nil
            camera = .region(ExtensionMapGeometry.region(for: map))
        }
    }

    private var coverLinkBinding: Binding<LinkTarget?> {
        Binding(
            get: { deferredLink ?? link },
            set: { target in
                if let target {
                    if fullScreen {
                        deferredLink = target
                        fullScreen = false
                    } else {
                        link = target
                    }
                } else {
                    deferredLink = nil
                    link = nil
                }
            }
        )
    }

    private func handleCoverDismiss() {
        if let target = deferredLink {
            deferredLink = nil
            link = target
        }
        if let action = deferredAction {
            deferredAction = nil
            action()
        }
    }

    private var coverTitle: String {
        if let title = map.title, !title.isEmpty { return title }
        if let title = (controller as? ExtensionViewModel)?.view?.title, !title.isEmpty { return title }
        if let title = (controller as? AssistantViewModel)?.view.title, !title.isEmpty { return title }
        return String(localized: "Map [Extension Map]")
    }
}

private struct ExtensionMapFrameModifier: ViewModifier {
    var aspect: CGFloat?
    var height: CGFloat

    @ViewBuilder func body(content: Content) -> some View {
        if let aspect {
            content.aspectRatio(aspect, contentMode: .fit).frame(maxWidth: .infinity)
        } else {
            content.scaledFrame(height: height).frame(maxWidth: .infinity)
        }
    }
}

/// Interactive MapKit viewport shared between inline page mode and full-screen cover.
private struct ExtensionMapInteractiveViewport: View {
    var map: ExtensionMapComponent
    var selectedRouteID: String?
    @Binding var selectedMarkerID: String?
    @Binding var camera: MapCameraPosition
    @Binding var adjustedCamera: Bool
    var inCover: Bool
    var showsFullScreen: Bool
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    var onReset: () -> Void
    var onFullScreen: () -> Void
    var onMarkerPush: () -> Void
    var onDeferAction: (@escaping () -> Void) -> Void

    @State private var fittedRegion: MKCoordinateRegion?

    private var orderedRoutes: [ExtensionMapRoute] {
        let all = map.routes ?? []
        let unselected = all.filter { $0.id != selectedRouteID }
        let selected = all.filter { $0.id == selectedRouteID }
        return unselected + selected
    }

    var body: some View {
        Map(position: $camera) {
            UserAnnotation()
            ForEach(orderedRoutes, id: \.id) { route in
                let coords = ExtensionMapGeometry.validPoints(route)
                if coords.count >= 2 {
                    let isSelected = (selectedRouteID == nil) || (route.id == selectedRouteID)
                    let baseColor = ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color)
                    MapPolyline(coordinates: coords)
                        .stroke(
                            baseColor.opacity(isSelected ? 1 : Theme.Map.unselectedOpacity),
                            style: StrokeStyle(
                                lineWidth: isSelected ? Theme.Size.mapRouteStroke : Theme.Size.mapRouteDimStroke,
                                lineCap: .round,
                                lineJoin: .round
                            )
                        )
                }
            }

            ForEach(map.markers ?? [], id: \.id) { marker in
                if let coord = ExtensionMapGeometry.validCoordinate(marker) {
                    Annotation("", coordinate: coord, anchor: .center) {
                        ExtensionMapAnnotationButton(
                            marker: marker,
                            selected: selectedMarkerID == marker.id,
                            inCover: inCover,
                            controller: controller,
                            link: $link,
                            onSelect: { selectedMarkerID = marker.id },
                            onMarkerPush: onMarkerPush,
                            onDeferAction: onDeferAction
                        )
                    }
                }
            }
        }
        .onMapCameraChange(frequency: .onEnd) { context in
            if camera.positionedByUser {
                if let fittedRegion, ExtensionMapGeometry.regionsApproximatelyEqual(context.region, fittedRegion) {
                    adjustedCamera = false
                } else {
                    adjustedCamera = true
                }
            } else if !adjustedCamera {
                // Capture the viewport's aspect-fitted region rather than treating MapKit's initial fit as a user pan.
                fittedRegion = context.region
            }
        }
        .overlay(alignment: .topTrailing) {
            if showsFullScreen || adjustedCamera {
                HStack(spacing: Theme.Space.flush) {
                    Button(action: onReset) {
                        ViewerIcon(symbol: Theme.Icon.resetDefaults, enabled: adjustedCamera)
                    }
                    .buttonStyle(.plain)
                    .disabled(!adjustedCamera)
                    .accessibilityLabel(String(localized: "Reset View"))
                    .accessibilityShowsLargeContentViewer { Text(String(localized: "Reset View")) }
                    .accessibilityIdentifier("extension-map-reset")

                    if showsFullScreen {
                        Button(action: onFullScreen) {
                            ViewerIcon(symbol: Theme.Icon.enlarge, enabled: true)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "Full Screen"))
                        .accessibilityShowsLargeContentViewer { Text(String(localized: "Full Screen")) }
                        .accessibilityIdentifier("extension-map-full-screen")
                    }
                }
                .padding(Theme.Space.xs)
                .chromeSurface(in: Capsule())
                .padding(Theme.Space.page)
            }
        }
        .accessibilityElement(children: .contain)
    }
}

/// Full-screen interactive map sheet presented from compact cards or page expand button.
private struct ExtensionMapCover: View {
    var map: ExtensionMapComponent
    @Binding var selectedRouteID: String?
    @Binding var selectedMarkerID: String?
    @Binding var camera: MapCameraPosition
    @Binding var adjustedCamera: Bool
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    var onReset: () -> Void
    var onSelectRoute: (String) -> Void
    var onDismissCover: () -> Void
    var onDeferAction: (@escaping () -> Void) -> Void

    private var routes: [ExtensionMapRoute] { map.routes ?? [] }
    private var markers: [ExtensionMapMarker] { map.markers ?? [] }
    private var safeLinks: [ExtensionMapSafeLink] {
        ExtensionMapGeometry.sanitizedLinks(map.links).enumerated().map {
            ExtensionMapSafeLink(index: $0.offset, title: $0.element.title, url: $0.element.url)
        }
    }

    private var activeRoute: ExtensionMapRoute? {
        if let selectedRouteID, let matched = routes.first(where: { $0.id == selectedRouteID }) {
            return matched
        }
        return routes.first(where: { $0.selected == true }) ?? routes.first
    }

    var body: some View {
        VStack(spacing: Theme.Space.flush) {
            ExtensionMapInteractiveViewport(
                map: map,
                selectedRouteID: activeRoute?.id,
                selectedMarkerID: $selectedMarkerID,
                camera: $camera,
                adjustedCamera: $adjustedCamera,
                inCover: true,
                showsFullScreen: false,
                controller: controller,
                link: $link,
                onReset: onReset,
                onFullScreen: {},
                onMarkerPush: onDismissCover,
                onDeferAction: onDeferAction
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)

            if !routes.isEmpty || !markers.isEmpty || !safeLinks.isEmpty {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    if routes.count > 1 {
                        ScrollView(.horizontal) {
                            HStack(spacing: Theme.Space.s) {
                                ForEach(Array(routes.enumerated()), id: \.element.id) { index, route in
                                    let selected = activeRoute?.id == route.id
                                    Button { onSelectRoute(route.id) } label: {
                                        HStack(spacing: Theme.Space.xs) {
                                            ThemedIcon(ExtensionMapFormatting.modeIcon(route.mode))
                                                .font(Theme.Font.labelStrong)
                                            Text(ExtensionMapFormatting.routeTitle(route, index: index))
                                                .font(Theme.Font.labelStrong)
                                                .lineLimit(1)
                                            if let meta = ExtensionMapFormatting.routeMetadata(route) {
                                                Text(meta).font(Theme.Font.label).lineLimit(1)
                                            }
                                        }
                                        .padding(.horizontal, Theme.Space.m)
                                        .padding(.vertical, Theme.Space.xs)
                                        .background(selected ? Theme.accentTint : Theme.Fill.chip, in: .capsule)
                                        .foregroundStyle(selected ? Theme.accent : Color.primary)
                                        .tapTargetHeight()
                                        .contentShape(.rect)
                                    }
                                    .buttonStyle(.plain)
                                    .accessibilityIdentifier("extension-map-cover-route-\(route.id)")
                                }
                            }
                            .padding(.horizontal, Theme.Space.page)
                        }
                        .scrollIndicators(.hidden)
                    } else if let route = activeRoute {
                        let meta = ExtensionMapFormatting.routeMetadata(route)
                        let summary = route.summary?.trimmingCharacters(in: .whitespacesAndNewlines)
                        if meta != nil || (summary?.isEmpty == false) {
                            HStack(spacing: Theme.Space.s) {
                                ThemedIcon(ExtensionMapFormatting.modeIcon(route.mode))
                                    .font(Theme.Font.labelStrong)
                                    .foregroundStyle(ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color))
                                    .accessibilityHidden(true)
                                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                    if let meta {
                                        Text(meta)
                                            .font(Theme.Font.captionMedium)
                                            .foregroundStyle(Color.primary)
                                    }
                                    if let summary, !summary.isEmpty {
                                        Text(summary)
                                            .font(Theme.Font.label)
                                            .foregroundStyle(.secondary)
                                            .lineLimit(2)
                                    }
                                }
                                Spacer(minLength: Theme.Space.flush)
                            }
                            .padding(.horizontal, Theme.Space.page)
                            .accessibilityElement(children: .combine)
                        }
                    }

                    if !markers.isEmpty || !safeLinks.isEmpty {
                        ScrollView {
                            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                                ForEach(markers, id: \.id) { marker in
                                    ExtensionMapStopRow(
                                        marker: marker,
                                        selected: selectedMarkerID == marker.id,
                                        controller: controller,
                                        link: $link,
                                        inCover: true,
                                        onSelect: { selectedMarkerID = marker.id },
                                        onPush: onDismissCover,
                                        onDeferAction: onDeferAction
                                    )
                                }
                                ExtensionMapLinksStrip(
                                    links: safeLinks,
                                    horizontalPadding: Theme.Space.flush,
                                    identifierPrefix: "extension-map-cover-link"
                                ) { url in
                                    link = LinkTarget(url: url)
                                }
                                .padding(.top, safeLinks.isEmpty ? Theme.Space.flush : Theme.Space.xs)
                            }
                            .padding(.horizontal, Theme.Space.page)
                        }
                        .scaledFrame(maxHeight: Theme.Size.mapListMaxHeight)
                    }
                }
                .padding(.vertical, Theme.Space.m)
                .background(Theme.pageBackground)
            }
        }
    }
}

/// Immediate synchronous vector layer with async `MKMapSnapshotter` overlay when online/non-picture.
private struct ExtensionMapSnapshotView: View {
    var map: ExtensionMapComponent
    var selectedRouteID: String?
    var synchronous: Bool

    @Environment(\.colorScheme) private var scheme
    @Environment(\.displayScale) private var scale
    @State private var size: CGSize = .zero
    @State private var snapshot: ExtensionMapSnapshot?

    private var fittingRegion: MKCoordinateRegion {
        ExtensionMapGeometry.region(for: map, selectedRouteID: selectedRouteID)
    }

    private var request: ExtensionMapSnapshot.Request {
        ExtensionMapSnapshot.Request(
            map: map,
            selectedRouteID: selectedRouteID,
            dark: scheme == .dark,
            width: Int(round(size.width)),
            height: Int(round(size.height))
        )
    }

    var body: some View {
        ZStack {
            if !synchronous, let snapshot {
                Image(uiImage: snapshot.image)
                    .resizable()
                    .scaledToFill()
                    .overlay {
                        ExtensionMapSnapshotOverlay(
                            map: map,
                            selectedRouteID: selectedRouteID,
                            routes: snapshot.routes,
                            markers: snapshot.markers
                        )
                    }
                    .transition(.opacity)
            } else {
                ExtensionMapVectorCanvas(
                    map: map,
                    region: fittingRegion,
                    selectedRouteID: selectedRouteID
                )
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onGeometryChange(for: CGSize.self) { $0.size } action: { size = $0 }
        .clipped()
        .task(id: request) {
            guard !synchronous, size.width > 1, size.height > 1 else { return }
            let key = request.cacheKey
            if let hit = ExtensionMapSnapshot.cached(key) {
                snapshot = hit
                return
            }
            snapshot = await ExtensionMapSnapshot.make(
                map, region: fittingRegion, size: size, dark: scheme == .dark, scale: max(scale, 1), key: key
            )
        }
    }
}

/// Vector fallback canvas (used synchronously in Share-as-Image cards and immediately before `MKMapSnapshotter` loads).
struct ExtensionMapVectorCanvas: View {
    var map: ExtensionMapComponent
    var region: MKCoordinateRegion
    var selectedRouteID: String?

    var body: some View {
        let gridColor = Color.primary.opacity(Theme.Map.gridLineOpacity)
        let step = Theme.Map.gridStepFraction
        let hairline = Theme.Size.hairline
        let allRoutes = map.routes ?? []
        let unselected = allRoutes.filter { selectedRouteID != nil && $0.id != selectedRouteID }
        let selected = allRoutes.filter { selectedRouteID == nil || $0.id == selectedRouteID }
        let drawnRoutes: [(points: [CGPoint], color: Color, lineWidth: CGFloat)] =
            (unselected.map { ($0, false) } + selected.map { ($0, true) }).compactMap { route, isSelected in
                let coords = ExtensionMapGeometry.validPoints(route)
                guard coords.count >= 2 else { return nil }
                let norm = coords.map { ExtensionMapGeometry.normalizedPoint($0, in: region) }
                let color = ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color)
                    .opacity(isSelected ? Theme.Map.vectorRouteOpacity : Theme.Map.unselectedOpacity)
                let width = isSelected ? Theme.Size.mapRouteStroke : Theme.Size.mapRouteDimStroke
                return (norm, color, width)
            }

        Canvas { context, size in
            guard size.width > 0, size.height > 0 else { return }

            var frac = step
            while frac < 1 {
                let x = size.width * frac
                let y = size.height * frac
                var vLine = Path()
                vLine.move(to: CGPoint(x: x, y: 0))
                vLine.addLine(to: CGPoint(x: x, y: size.height))
                context.stroke(vLine, with: .color(gridColor), lineWidth: hairline)

                var hLine = Path()
                hLine.move(to: CGPoint(x: 0, y: y))
                hLine.addLine(to: CGPoint(x: size.width, y: y))
                context.stroke(hLine, with: .color(gridColor), lineWidth: hairline)
                frac += step
            }

            for entry in drawnRoutes {
                let pts = entry.points.map { CGPoint(x: $0.x * size.width, y: $0.y * size.height) }
                var path = Path()
                path.addLines(pts)
                context.stroke(
                    path,
                    with: .color(entry.color),
                    style: StrokeStyle(
                        lineWidth: entry.lineWidth,
                        lineCap: .round,
                        lineJoin: .round
                    )
                )
            }
        }
        .background(Theme.Map.ground)
        .overlay {
            GeometryReader { box in
                ForEach(map.markers ?? [], id: \.id) { marker in
                    if let coord = ExtensionMapGeometry.validCoordinate(marker) {
                        let norm = ExtensionMapGeometry.normalizedPoint(coord, in: region)
                        ExtensionMapPinBadge(marker: marker, selected: false)
                            .position(x: norm.x * box.size.width, y: norm.y * box.size.height)
                    }
                }
            }
            .accessibilityHidden(true)
        }
    }
}

private struct ExtensionMapSnapshotOverlay: View {
    var map: ExtensionMapComponent
    var selectedRouteID: String?
    var routes: [String: [CGPoint]]
    var markers: [String: CGPoint]

    var body: some View {
        GeometryReader { box in
            let allRoutes = map.routes ?? []
            let unselected = allRoutes.filter { selectedRouteID != nil && $0.id != selectedRouteID }
            let selected = allRoutes.filter { selectedRouteID == nil || $0.id == selectedRouteID }

            ForEach(unselected, id: \.id) { route in
                if let norm = routes[route.id], norm.count >= 2 {
                    Path { p in
                        p.addLines(norm.map { CGPoint(x: $0.x * box.size.width, y: $0.y * box.size.height) })
                    }
                    .stroke(
                        ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color)
                            .opacity(Theme.Map.unselectedOpacity),
                        style: StrokeStyle(lineWidth: Theme.Size.mapRouteDimStroke, lineCap: .round, lineJoin: .round)
                    )
                }
            }

            ForEach(selected, id: \.id) { route in
                if let norm = routes[route.id], norm.count >= 2 {
                    Path { p in
                        p.addLines(norm.map { CGPoint(x: $0.x * box.size.width, y: $0.y * box.size.height) })
                    }
                    .stroke(
                        ExtensionMapFormatting.resolveColor(tone: route.tone, color: route.color),
                        style: StrokeStyle(lineWidth: Theme.Size.mapRouteStroke, lineCap: .round, lineJoin: .round)
                    )
                }
            }

            ForEach(map.markers ?? [], id: \.id) { marker in
                if let pos = markers[marker.id] {
                    ExtensionMapPinBadge(marker: marker, selected: false)
                        .position(x: pos.x * box.size.width, y: pos.y * box.size.height)
                }
            }
        }
        .accessibilityHidden(true)
    }
}

private struct ExtensionMapPinBadge: View {
    var marker: ExtensionMapMarker
    var selected: Bool

    private var color: Color { ExtensionMapFormatting.markerColor(marker) }

    var body: some View {
        Group {
            if marker.kind == ExtensionMapMarkerKind.origin {
                Circle()
                    .fill(color)
                    .scaledFrame(width: Theme.Size.mapOriginDot, height: Theme.Size.mapOriginDot)
                    .overlay(Circle().stroke(Theme.onSolid, lineWidth: Theme.Size.lineMedium))
            } else {
                ThemedIcon(ExtensionMapFormatting.markerSymbol(marker))
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(Theme.onSolid)
                    .scaledFrame(width: Theme.Size.mapPinSize, height: Theme.Size.mapPinSize)
                    .background(color, in: .circle)
                    .overlay(Circle().stroke(selected ? Theme.primaryText : Theme.onSolid, lineWidth: Theme.Size.lineMedium))
            }
        }
    }
}

private struct ExtensionMapAnnotationButton: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var marker: ExtensionMapMarker
    var selected: Bool
    var inCover: Bool
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    var onSelect: () -> Void
    var onMarkerPush: () -> Void
    var onDeferAction: (@escaping () -> Void) -> Void

    var body: some View {
        Group {
            if let action = marker.action, let controller {
                if inCover && action.confirm?.isEmpty == false && controller.managesConfirmation {
                    let rev = displayed
                    let currentApp = app
                    let linkBinding = $link
                    Button {
                        onSelect()
                        let execute = controller.prepared(action, with: [:], displayed: rev)
                        onDeferAction {
                            Task { @MainActor in
                                ExtensionOutcome.show(await execute(), app: currentApp, link: linkBinding)
                            }
                        }
                    } label: {
                        face
                    }
                    .buttonStyle(.plain)
                    .disabled(!controller.isEnabled(action))
                    .accessibilityIdentifier("extension-map-marker-\(marker.id)")
                } else {
                    ExtensionActionButton(
                        action: action,
                        title: marker.label,
                        model: controller,
                        values: [:],
                        link: $link,
                        identifier: "extension-map-marker-\(marker.id)",
                        onPush: onMarkerPush
                    ) {
                        face
                    }
                    .buttonStyle(.plain)
                }
            } else {
                Button(action: onSelect) {
                    face
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("extension-map-marker-\(marker.id)")
            }
        }
        .accessibilityLabel(marker.label)
        .accessibilityValue(marker.subtitle ?? marker.detail ?? "")
    }

    private var face: some View {
        VStack(spacing: Theme.Space.xxs) {
            ExtensionMapPinBadge(marker: marker, selected: selected)
            if !marker.label.isEmpty {
                Text(marker.label)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(Color.primary)
                    .lineLimit(1)
                    .padding(.horizontal, Theme.Space.xs)
                    .padding(.vertical, Theme.Space.xxs)
                    .background(Theme.Map.ground, in: .capsule)
                    .overlay(Capsule().stroke(Theme.cardStroke, lineWidth: Theme.Size.hairline))
            }
        }
        .fixedSize()
        .scaledFrame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
        .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
        .contentShape(.rect)
    }
}

private struct ExtensionMapMarkerDetailCard: View {
    var marker: ExtensionMapMarker
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    var onDismiss: () -> Void

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            ExtensionMapPinBadge(marker: marker, selected: true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(marker.label).font(Theme.Font.secondaryStrong).lineLimit(1)
                if let sub = marker.subtitle, !sub.isEmpty {
                    Text(sub).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                }
                if let detail = marker.detail, !detail.isEmpty {
                    Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                }
            }
            Spacer(minLength: Theme.Space.flush)
            if let action = marker.action, let controller {
                ExtensionActionButton(action: action, title: marker.label, model: controller, link: $link) {
                    ThemedIcon(Theme.Icon.open).font(Theme.Font.labelStrong)
                        .tapTargetOutset(.all)
                }
            }
            Button(action: onDismiss) {
                ThemedIcon(Theme.Icon.close).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                    .tapTargetOutset(.all)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "Close"))
        }
        .padding(Theme.Space.m)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
    }
}

private struct ExtensionMapStopRow: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var marker: ExtensionMapMarker
    var selected: Bool
    var controller: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    var inCover = false
    var onSelect: () -> Void
    var onPush: () -> Void = {}
    var onDeferAction: (@escaping () -> Void) -> Void = { $0() }

    var body: some View {
        Group {
            if let action = marker.action, let controller {
                if inCover && action.confirm?.isEmpty == false && controller.managesConfirmation {
                    let rev = displayed
                    let currentApp = app
                    let linkBinding = $link
                    Button {
                        onSelect()
                        let execute = controller.prepared(action, with: [:], displayed: rev)
                        onDeferAction {
                            Task { @MainActor in
                                ExtensionOutcome.show(await execute(), app: currentApp, link: linkBinding)
                            }
                        }
                    } label: {
                        rowContent
                    }
                    .buttonStyle(.plain)
                } else {
                    ExtensionActionButton(
                        action: action,
                        title: marker.label,
                        model: controller,
                        link: $link,
                        identifier: "extension-map-stop-\(marker.id)",
                        onPush: onPush
                    ) {
                        rowContent
                    }
                    .buttonStyle(.plain)
                }
            } else {
                Button(action: onSelect) {
                    rowContent
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("extension-map-stop-\(marker.id)")
            }
        }
    }

    private var rowContent: some View {
        HStack(spacing: Theme.Space.m) {
            ExtensionMapPinBadge(marker: marker, selected: selected)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(marker.label)
                    .font(Theme.Font.secondaryStrong)
                    .foregroundStyle(Color.primary)
                    .lineLimit(1)
                if let sub = marker.subtitle, !sub.isEmpty {
                    Text(sub).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                if let detail = marker.detail, !detail.isEmpty {
                    Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: Theme.Space.flush)
            if marker.action != nil {
                ThemedIcon(Theme.Icon.disclosure)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .background(selected ? Theme.accentTint : Theme.Fill.well,
                    in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
        .tapTargetHeight()
        .contentShape(.rect)
    }
}

private struct ExtensionMapSafeLink: Identifiable {
    var index: Int
    var title: String
    var url: URL
    var id: String { "\(index)|\(url.absoluteString)" }
}

private struct ExtensionMapLinksStrip: View {
    var links: [ExtensionMapSafeLink]
    var horizontalPadding: CGFloat
    var identifierPrefix: String
    var onOpen: (URL) -> Void

    var body: some View {
        if !links.isEmpty {
            ScrollView(.horizontal) {
                HStack(spacing: Theme.Space.s) {
                    ForEach(links) { item in
                        Button {
                            onOpen(item.url)
                        } label: {
                            Label(item.title, systemImage: Theme.Icon.open)
                                .font(Theme.Font.labelStrong)
                                .lineLimit(1)
                                .padding(.horizontal, Theme.Space.m)
                                .padding(.vertical, Theme.Space.xs)
                                .background(Theme.Fill.chip, in: .capsule)
                        }
                        .buttonStyle(.plain)
                        .tapTargetHeight()
                        .contentShape(.rect)
                        .accessibilityIdentifier("\(identifierPrefix)-\(item.index)")
                    }
                }
                .padding(.horizontal, horizontalPadding)
            }
            .scrollIndicators(.hidden)
        }
    }
}
#endif
