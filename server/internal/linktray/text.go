package linktray

import (
	"math"
	"net/url"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// The small phrases the lines are built from (Mac: LinkText), one place for each so every line says it the same way.

// KindText names a kind of hub address (link.KindOf): LAN, public IPv6, domain, public IPv4, SSH.
func KindText(l i18n.Lang, kind string) string {
	switch kind {
	case "lan":
		return i18n.T(l, "link-tray.kind_lan")
	case "ipv6":
		return i18n.T(l, "link-tray.kind_ipv6")
	case "name":
		return i18n.T(l, "link-tray.kind_name")
	case "ipv4":
		return i18n.T(l, "link-tray.kind_ipv4")
	case link.KindSSH:
		return i18n.T(l, "link-tray.kind_ssh")
	}
	return i18n.T(l, "link-tray.kind_other")
}

// HostText is a hub URL as a person reads it: host and port, and the path a reverse proxy puts the hub under.
func HostText(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	path := u.EscapedPath()
	if path == "/" {
		path = ""
	}
	return u.Host + path
}

// HubText is the hub line: its name, and how many hubs its mesh has when that is known (0: not known).
func HubText(l i18n.Lang, name string, mesh int) string {
	switch {
	case mesh > 1:
		return i18n.T(l, "link-tray.hub_mesh", i18n.Args{"name": name, "count": mesh})
	case mesh == 1:
		return i18n.T(l, "link-tray.hub_alone", i18n.Args{"name": name})
	}
	return i18n.T(l, "link-tray.hub_named", i18n.Args{"name": name})
}

// Duration is how long something has lasted, in a few words.
func Duration(l i18n.Lang, d time.Duration) string {
	s := max(0, int64(d/time.Second))
	switch {
	case s < 60:
		return i18n.T(l, "link-tray.dur_under_minute")
	case s < 3600:
		return i18n.T(l, "link-tray.dur_minutes", i18n.Args{"m": s / 60})
	case s < 86400:
		return i18n.T(l, "link-tray.dur_hours", i18n.Args{"h": s / 3600, "m": s % 3600 / 60})
	}
	return i18n.T(l, "link-tray.dur_days", i18n.Args{"d": s / 86400, "h": s % 86400 / 3600})
}

// Ago is how long ago, in a few words.
func Ago(l i18n.Lang, d time.Duration) string {
	s := max(0, int64(d/time.Second))
	switch {
	case s < 60:
		return i18n.T(l, "link-tray.ago_just_now")
	case s < 3600:
		return i18n.T(l, "link-tray.ago_minutes", i18n.Args{"m": s / 60})
	case s < 86400:
		return i18n.T(l, "link-tray.ago_hours", i18n.Args{"h": s / 3600})
	}
	return i18n.T(l, "link-tray.ago_days", i18n.Args{"d": s / 86400})
}

// Until is when the next attempt is, d from now.
func Until(l i18n.Lang, d time.Duration) string {
	s := int64(math.Ceil(d.Seconds()))
	switch {
	case s <= 0:
		return i18n.T(l, "link-tray.retry_now")
	case s < 60:
		return i18n.T(l, "link-tray.retry_seconds", i18n.Args{"s": s})
	}
	return i18n.T(l, "link-tray.retry_minutes", i18n.Args{"m": s / 60})
}

// Left is how long a pairing link is still good.
func Left(l i18n.Lang, d time.Duration) string {
	s := int64(math.Ceil(d.Seconds()))
	if s < 60 {
		return i18n.T(l, "link-tray.dur_under_minute")
	}
	return i18n.T(l, "link-tray.dur_minutes", i18n.Args{"m": s / 60})
}
