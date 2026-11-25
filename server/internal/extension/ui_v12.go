package extension

import (
	"cmp"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// UI version 12 (docs/extensions/extensions.md "UI version 12", #14730): a map, showing routes, markers, viewport and
// https links in WGS-84. downgrade12 draws it with version 11's components for an older app or a client listing "map"
// in context.unsupported: an optional header text (title / caption), a row per route, a row per marker, and each
// external https link.
func downgrade12(v *View, native, compact bool, l i18n.Lang) {
	used := make(map[string]bool)
	for i := range v.Sections {
		if k := v.Sections[i].Key; k != "" {
			used[k] = true
		}
		for j := range v.Sections[i].Items {
			e := &v.Sections[i].Items[j]
			if k := e.Key; k != "" {
				used[k] = true
			}
			for k := range e.Tiles {
				if t := e.Tiles[k].Key; t != "" {
					used[t] = true
				}
			}
		}
	}
	room := CompactItems
	mapsLeft := 0
	if compact {
		for i := range v.Sections {
			for j := range v.Sections[i].Items {
				e := &v.Sections[i].Items[j]
				if e.Type == wire.ComponentMap {
					mapsLeft++
				} else {
					room -= max(1, len(e.Tiles))
				}
			}
		}
		room -= max(mapsLeft-1, 0)
	}
	for i := range v.Sections {
		s := &v.Sections[i]
		items := make([]UIElem, 0, len(s.Items))
		for _, e := range s.Items {
			if e.Type != wire.ComponentMap {
				items = append(items, e)
				continue
			}
			var rows []UIElem
			summary := cmp.Or(e.Title, e.Caption)
			if e.Title != "" && e.Caption != "" && e.Caption != e.Title {
				summary = e.Title + " — " + e.Caption
			}
			if summary != "" {
				header := UIElem{Type: wire.ComponentText, Text: clip(summary, StringMost)}
				if native {
					header.Key = mapItemKey(e.Key, "summary", true, used)
				}
				rows = append(rows, header)
			}
			for _, r := range e.Routes {
				modeLabel := mapTravelModeLabel(r.Mode, l)
				title := clip(cmp.Or(r.Label, modeLabel, r.ID), StringMost)
				subtitle := ""
				if r.Label != "" && modeLabel != "" {
					subtitle = clip(modeLabel, StringMost)
				}
				value := clip(cmp.Or(r.Summary, formatMapRouteMetrics(r.DurationSeconds, r.DistanceMeters)), StringMost)
				row := UIElem{
					Type:     wire.ComponentRow,
					Key:      mapItemKey(e.Key, "route/"+r.ID, native, used),
					Icon:     wire.NavIconCar,
					Title:    title,
					Subtitle: subtitle,
					Value:    value,
				}
				if r.Selected {
					b := &wire.Badge{Text: i18n.T(l, "extension-ui-fallback.map_selected")}
					if r.Color != "" {
						b.Color = r.Color
					} else {
						b.Tone = cmp.Or(r.Tone, wire.TonePositive)
					}
					row.Badge = b
				}
				rows = append(rows, row)
			}
			for _, m := range e.Markers {
				row := UIElem{
					Type:      wire.ComponentRow,
					Key:       mapItemKey(e.Key, m.ID, native, used),
					Icon:      cmp.Or(m.Icon, wire.NavIconLocation),
					IconColor: m.Color,
					Title:     clip(m.Label, StringMost),
					Subtitle:  clip(m.Subtitle, StringMost),
					Value:     clip(m.Detail, StringMost),
					Action:    m.Action,
				}
				rows = append(rows, row)
			}
			for idx, lk := range e.Links {
				linkItem := UIElem{Type: wire.ComponentLink, Title: lk.Title, URL: lk.URL}
				if native {
					linkItem.Key = mapItemKey(e.Key, fmt.Sprintf("link-%d", idx), true, used)
				}
				rows = append(rows, linkItem)
			}
			if len(rows) == 0 {
				altItem := UIElem{Type: wire.ComponentText, Text: clip(e.Alt, StringMost)}
				if native {
					altItem.Key = mapItemKey(e.Key, "alt", true, used)
				}
				rows = append(rows, altItem)
			}
			if compact {
				mapsLeft--
				take := min(len(rows), max(room, 1))
				rows = rows[:take]
				room -= take
			}
			items = append(items, rows...)
		}
		s.Items = items
	}
}

func mapItemKey(baseKey, suffix string, native bool, used map[string]bool) string {
	if baseKey != "" {
		if k := baseKey + "/" + suffix; !used[k] && utf8.RuneCountInString(k) <= graphRowKeyMost {
			used[k] = true
			return k
		}
		if native {
			return freeGraphRowKey(baseKey, suffix, used)
		}
		return ""
	}
	if native {
		return freeGraphRowKey("", suffix, used)
	}
	return ""
}

func mapTravelModeLabel(mode string, l i18n.Lang) string {
	switch mode {
	case wire.MapTravelModeDriving:
		return i18n.T(l, "extension-ui-fallback.map_mode_driving")
	case wire.MapTravelModeWalking:
		return i18n.T(l, "extension-ui-fallback.map_mode_walking")
	case wire.MapTravelModeCycling:
		return i18n.T(l, "extension-ui-fallback.map_mode_cycling")
	case wire.MapTravelModeTransit:
		return i18n.T(l, "extension-ui-fallback.map_mode_transit")
	}
	return ""
}

func formatMapRouteMetrics(dur, dist *int) string {
	var parts []string
	if dur != nil {
		d := *dur
		switch {
		case d < 60:
			parts = append(parts, "<1 min")
		case d < 3600:
			parts = append(parts, fmt.Sprintf("%d min", (d+30)/60))
		default:
			mins := ((d % 3600) + 30) / 60
			hours := d / 3600
			if mins == 60 {
				hours++
				mins = 0
			}
			if mins == 0 {
				parts = append(parts, fmt.Sprintf("%d h", hours))
			} else {
				parts = append(parts, fmt.Sprintf("%d h %d min", hours, mins))
			}
		}
	}
	if dist != nil {
		m := *dist
		if m < 1000 {
			parts = append(parts, fmt.Sprintf("%d m", m))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f km", float64(m)/1000.0))
		}
	}
	return strings.Join(parts, " · ")
}
