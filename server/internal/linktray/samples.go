package linktray

import (
	"time"

	"github.com/nwverse/persona/server/internal/link"
)

// Sample is a made-up state the tests and the page screenshots draw: one for each look, and the
// ones the panel words differently.
type Sample struct {
	Name     string
	Snapshot Snapshot
}

// Samples are the states the snapshots cover, at now.
func Samples(now time.Time) []Sample {
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	cfg := &link.Config{Token: "t", Machine: "studio-pc", FP: "fp", HubName: "home",
		Endpoints: []link.Endpoint{{URL: "https://192.0.2.20:18661", Kind: "lan"}, {URL: "https://hub.example.com:18661", Kind: "name", Manual: true}}}
	passiveCfg := &link.Config{Mode: link.ModePassive, Passive: &link.Passive{TokenSHA256: "h", HubName: "home", Machine: "studio-pc", Listen: ":18663"}}
	task := Task{Installed: true, State: "Running"}
	live := func(c *link.Config, st link.Status) Snapshot {
		return Snapshot{Now: now, Running: true, Status: &link.SavedStatus{Status: st}, Config: c, Task: task, Autostart: true}
	}
	return []Sample{
		{"connected", live(cfg, link.Status{State: "connected", Mode: link.ModeActive, Machine: "studio-pc", HubName: "home", MeshHubs: 2,
			Address: "https://192.0.2.20:18661", Kind: "lan", Since: at(-3*time.Hour - 12*time.Minute), Node: "online"})},
		{"awaiting_confirm", live(cfg, link.Status{State: "connected", Mode: link.ModeActive, Machine: "studio-pc", HubName: "home",
			Address: "https://hub.example.com:18661", Kind: "name", Pending: true, Node: "online"})},
		{"node_missing", live(cfg, link.Status{State: "connected", Mode: link.ModeActive, Machine: "studio-pc", HubName: "home",
			Address: "https://192.0.2.20:18661", Kind: "lan", Since: at(-5 * time.Minute), Node: "offline",
			NodeError: "node-attach.sock: connection refused"})},
		{"connecting", live(cfg, link.Status{State: "connecting", Mode: link.ModeActive, Machine: "studio-pc", HubName: "home"})},
		{"listening", live(passiveCfg, link.Status{State: "listening", Mode: link.ModePassive, Machine: "studio-pc", HubName: "home",
			Listen: ":18663", LastSeen: at(-20 * time.Minute), From: "192.0.2.20"})},
		{"down", live(cfg, link.Status{State: "waiting", Mode: link.ModeActive, Machine: "studio-pc", HubName: "home", Retry: at(14 * time.Second),
			Error: "192.0.2.20:18661: i/o timeout；hub.example.com:18661: connection refused"})}, // i18n-ignore: persona-link's own separator
		{"unpaired", Snapshot{Now: now, Task: task, Autostart: true}},
		{"service_missing", Snapshot{Now: now, Config: cfg}},
		{"not_running", Snapshot{Now: now, Config: cfg, Task: Task{Installed: true, State: "Ready"}, Autostart: true}},
	}
}
