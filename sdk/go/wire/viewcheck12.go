package wire

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// UI version 12's checks (docs/extensions/extensions.md "UI version 12"): an interactive map's viewport, markers,
// routes and https links. What the schema cannot say: at least one of viewport, markers or routes, finite
// coordinates in bounds, unique marker and route ids, a marker's or route's tone or color (not both), at least 2
// points per route and at most MapPointsMost points across all routes together, and https links without credentials.

const (
	// MapMarkersMost is how many markers a single map component may hold.
	MapMarkersMost = 100
	// MapRoutesMost is how many routes a single map component may hold.
	MapRoutesMost = 8
	// MapPointsMost is how many route points a map component may hold across all of its routes together.
	MapPointsMost = 2000
	// MapLinksMost is how many external links a map component may hold.
	MapLinksMost = 6
)

// checkV12 holds a map component to UI version 12's rules.
func (p *pageCheck) checkV12(e *Component) error {
	if e.Type != ComponentMap {
		return nil
	}
	return CheckMap(e)
}

// CheckMap holds a map component to UI version 12's semantic rules, so exttest and conformance tests can check a view
// outside CheckView as well.
func CheckMap(e *Component) error {
	if e.Alt == "" {
		return errors.New("alt is required")
	}
	if e.Viewport == nil && len(e.Markers) == 0 && len(e.Routes) == 0 {
		return errors.New("a map has at least one of viewport, markers or routes")
	}
	if vp := e.Viewport; vp != nil {
		if err := checkMapCoord(vp.Center, "viewport.center"); err != nil {
			return err
		}
		if !finitePtr(vp.SpanLat) || (vp.SpanLat != nil && (*vp.SpanLat < 0.0001 || *vp.SpanLat > 180)) {
			return errors.New("viewport.span_lat: must be between 0.0001 and 180")
		}
		if !finitePtr(vp.SpanLon) || (vp.SpanLon != nil && (*vp.SpanLon < 0.0001 || *vp.SpanLon > 360)) {
			return errors.New("viewport.span_lon: must be between 0.0001 and 360")
		}
	}
	if len(e.Markers) > MapMarkersMost {
		return fmt.Errorf("a map has at most %d markers", MapMarkersMost)
	}
	markerIDs := make(map[string]bool, len(e.Markers))
	for k := range e.Markers {
		m := &e.Markers[k]
		if m.ID == "" {
			return fmt.Errorf("marker %d: id is required", k)
		}
		if markerIDs[m.ID] {
			return fmt.Errorf("marker %d: id %q twice", k, m.ID)
		}
		markerIDs[m.ID] = true
		if err := checkMapCoord(MapCoordinate{Lat: m.Lat, Lon: m.Lon}, fmt.Sprintf("marker %q", m.ID)); err != nil {
			return err
		}
		if m.Label == "" {
			return fmt.Errorf("marker %q: label is required", m.ID)
		}
		if m.Kind != "" && !slices.Contains(MapMarkerKinds, m.Kind) {
			return fmt.Errorf("marker %q: unknown kind %q", m.ID, m.Kind)
		}
		if !slices.Contains(tones, m.Tone) {
			return fmt.Errorf("marker %q: unknown tone %q", m.ID, m.Tone)
		}
		if m.Color != "" && !slices.Contains(Colors, m.Color) {
			return fmt.Errorf("marker %q: unknown color %q", m.ID, m.Color)
		}
		if m.Tone != "" && m.Color != "" {
			return fmt.Errorf("marker %q: a tone or a color, not both", m.ID)
		}
	}
	if len(e.Routes) > MapRoutesMost {
		return fmt.Errorf("a map has at most %d routes", MapRoutesMost)
	}
	routeIDs := make(map[string]bool, len(e.Routes))
	totalPoints := 0
	for k := range e.Routes {
		r := &e.Routes[k]
		if r.ID == "" {
			return fmt.Errorf("route %d: id is required", k)
		}
		if routeIDs[r.ID] {
			return fmt.Errorf("route %d: id %q twice", k, r.ID)
		}
		routeIDs[r.ID] = true
		if r.Mode != "" && !slices.Contains(MapTravelModes, r.Mode) {
			return fmt.Errorf("route %q: unknown mode %q", r.ID, r.Mode)
		}
		if !slices.Contains(tones, r.Tone) {
			return fmt.Errorf("route %q: unknown tone %q", r.ID, r.Tone)
		}
		if r.Color != "" && !slices.Contains(Colors, r.Color) {
			return fmt.Errorf("route %q: unknown color %q", r.ID, r.Color)
		}
		if r.Tone != "" && r.Color != "" {
			return fmt.Errorf("route %q: a tone or a color, not both", r.ID)
		}
		if r.DistanceMeters != nil && (*r.DistanceMeters < 0 || *r.DistanceMeters > 100000000) {
			return fmt.Errorf("route %q: distance_meters out of range", r.ID)
		}
		if r.DurationSeconds != nil && (*r.DurationSeconds < 0 || *r.DurationSeconds > 100000000) {
			return fmt.Errorf("route %q: duration_seconds out of range", r.ID)
		}
		if len(r.Points) < 2 {
			return fmt.Errorf("route %q: at least 2 points", r.ID)
		}
		if totalPoints += len(r.Points); totalPoints > MapPointsMost {
			return fmt.Errorf("a map's routes have at most %d points together", MapPointsMost)
		}
		for p := range r.Points {
			if err := checkMapCoord(r.Points[p], fmt.Sprintf("route %q point %d", r.ID, p)); err != nil {
				return err
			}
		}
	}
	if len(e.Links) > MapLinksMost {
		return fmt.Errorf("a map has at most %d links", MapLinksMost)
	}
	for k := range e.Links {
		lk := &e.Links[k]
		if n := utf8.RuneCountInString(lk.Title); n < 1 || n > 80 {
			return fmt.Errorf("link %d: a title of 1 to 80 characters", k)
		}
		if n := utf8.RuneCountInString(lk.URL); n < 1 || n > 2000 {
			return fmt.Errorf("link %d: a url of 1 to 2000 characters", k)
		}
		if !strings.HasPrefix(lk.URL, "https://") || strings.TrimSpace(lk.URL) != lk.URL {
			return fmt.Errorf("link %d: link needs an https url", k)
		}
		if err := CheckHTTPS(lk.URL, fmt.Sprintf("link %d", k)); err != nil {
			return err
		}
	}
	return nil
}

func checkMapCoord(c MapCoordinate, where string) error {
	if c.Lat == nil || c.Lon == nil || !finite(*c.Lat) || !finite(*c.Lon) {
		return fmt.Errorf("%s: finite lat and lon are required", where)
	}
	if *c.Lat < -90 || *c.Lat > 90 {
		return fmt.Errorf("%s: lat %g out of [-90, 90]", where, *c.Lat)
	}
	if *c.Lon < -180 || *c.Lon > 180 {
		return fmt.Errorf("%s: lon %g out of [-180, 180]", where, *c.Lon)
	}
	return nil
}
