package extmgr

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/extension"
)

func TestExtensionChannelTargetOverride(t *testing.T) {
	newManager := func(t *testing.T) *Manager {
		t.Helper()
		h := &Manager{cfg: &Config{}}
		h.bots.loaded = true
		return h
	}
	newManagerWithPlugin := func(t *testing.T, id string, channels []channel.Item, schemaJSON, savedConfigJSON string) *Manager {
		t.Helper()
		h := newManager(t)
		reg := extension.NewRegistry(t.TempDir(), nil, nil, 0)
		h.extensions = newExtensions(t.TempDir(), nil, func(string, ...any) {})
		h.extensions.reg = reg
		h.extensions.markets.loaded = true
		h.extensions.markets.byID = map[string]*marketState{}
		rawChannels, err := json.Marshal(channels)
		if err != nil {
			t.Fatal(err)
		}
		m := &extension.Manifest{
			ManifestVersion: extension.ManifestVersion,
			ID:              id,
			Name:            "Example " + id,
			Version:         "1.0.0",
			Contributes:     map[string]json.RawMessage{"channel": rawChannels},
		}
		if schemaJSON != "" {
			m.Configuration = json.RawMessage(schemaJSON)
		}
		if err := reg.AddBuiltin(m, "", true); err != nil {
			t.Fatal(err)
		}
		if err := reg.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		if savedConfigJSON != "" {
			if err := reg.SetConfig(context.Background(), id, json.RawMessage(savedConfigJSON), "test"); err != nil {
				t.Fatal(err)
			}
		}
		return h
	}

	t.Run("active when extension enables agent_group_topics boolean", func(t *testing.T) {
		schema := `{"type":"object","properties":{"agent_group_topics":{"type":"boolean","default":false}}}`
		ch := channel.Item{ID: "topicchat", Name: "Topic Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}}
		h := newManagerWithPlugin(t, "topic-ext", []channel.Item{ch}, schema, `{"agent_group_topics":true}`)

		_, overrides := h.channelListWithOverrides(context.Background())
		ov := overrides["topicchat"]
		if ov == nil || ov.Mode != channel.TargetOverrideTopics {
			t.Fatalf("overrides[topicchat] = %+v, want mode=%q", ov, channel.TargetOverrideTopics)
		}
	})

	t.Run("inactive when extension explicitly disables agent_group_topics despite threads capability", func(t *testing.T) {
		schema := `{"type":"object","properties":{"agent_group_topics":{"type":"boolean","default":false}}}`
		ch := channel.Item{ID: "topicchat", Name: "Topic Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}}
		h := newManagerWithPlugin(t, "topic-ext", []channel.Item{ch}, schema, `{"agent_group_topics":false}`)

		_, overrides := h.channelListWithOverrides(context.Background())
		if ov := overrides["topicchat"]; ov != nil {
			t.Fatalf("overrides[topicchat] = %+v, want nil when agent_group_topics is false", ov)
		}
	})

	t.Run("inactive on legacy empty config when schema defaults agent_group_topics to false", func(t *testing.T) {
		schema := `{"type":"object","properties":{"agent_group_topics":{"type":"boolean","default":false}}}`
		ch := channel.Item{ID: "topicchat", Name: "Topic Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}}
		h := newManagerWithPlugin(t, "topic-ext", []channel.Item{ch}, schema, ``)

		_, overrides := h.channelListWithOverrides(context.Background())
		if ov := overrides["topicchat"]; ov != nil {
			t.Fatalf("overrides[topicchat] = %+v, want nil on untouched default-off config", ov)
		}
	})

	t.Run("active when agent_group_topics is a non-empty list and inactive when empty", func(t *testing.T) {
		schema := `{"type":"object","properties":{"agent_group_topics":{"type":"array","items":{"type":"string"}}}}`
		ch := channel.Item{ID: "listchat", Name: "List Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}}
		hOn := newManagerWithPlugin(t, "list-ext", []channel.Item{ch}, schema, `{"agent_group_topics":["chat_1"]}`)
		_, overridesOn := hOn.channelListWithOverrides(context.Background())
		if ov := overridesOn["listchat"]; ov == nil || ov.Mode != channel.TargetOverrideTopics {
			t.Fatalf("non-empty list: overrides[listchat] = %+v, want topics", ov)
		}

		hOff := newManagerWithPlugin(t, "list-ext", []channel.Item{ch}, schema, `{"agent_group_topics":[]}`)
		_, overridesOff := hOff.channelListWithOverrides(context.Background())
		if ov := overridesOff["listchat"]; ov != nil {
			t.Fatalf("empty list: overrides[listchat] = %+v, want nil", ov)
		}
	})

	t.Run("active with threads mode when capabilities.threads is true and no routing config exists", func(t *testing.T) {
		ch := channel.Item{ID: "threadchat", Name: "Thread Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}}
		h := newManagerWithPlugin(t, "thread-ext", []channel.Item{ch}, "", "")

		_, overrides := h.channelListWithOverrides(context.Background())
		if ov := overrides["threadchat"]; ov == nil || ov.Mode != channel.TargetOverrideThreads {
			t.Fatalf("overrides[threadchat] = %+v, want mode=%q", ov, channel.TargetOverrideThreads)
		}
	})

	t.Run("inactive for ordinary channel without threads capability or routing config", func(t *testing.T) {
		ch := channel.Item{ID: "plainchat", Name: "Plain Chat", Kind: channel.KindIM, Capabilities: channel.Capabilities{Format: channel.FormatPlain}}
		h := newManagerWithPlugin(t, "plain-ext", []channel.Item{ch}, "", "")

		_, overrides := h.channelListWithOverrides(context.Background())
		if ov := overrides["plainchat"]; ov != nil {
			t.Fatalf("overrides[plainchat] = %+v, want nil", ov)
		}
	})

	t.Run("multi-channel extension only flags threaded sibling when topic routing is active", func(t *testing.T) {
		schema := `{"type":"object","properties":{"agent_group_topics":{"type":"boolean","default":false}}}`
		channels := []channel.Item{
			{ID: "threaded", Name: "Threaded", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: true}},
			{ID: "plain", Name: "Plain", Kind: channel.KindIM, Capabilities: channel.Capabilities{Threads: false}},
		}
		h := newManagerWithPlugin(t, "multi-ext", channels, schema, `{"agent_group_topics":true}`)

		_, overrides := h.channelListWithOverrides(context.Background())
		if ov := overrides["threaded"]; ov == nil || ov.Mode != channel.TargetOverrideTopics {
			t.Fatalf("overrides[threaded] = %+v, want topics", ov)
		}
		if ov := overrides["plain"]; ov != nil {
			t.Fatalf("overrides[plain] = %+v, want nil", ov)
		}
	})

	t.Run("feishu bot sets topics override only when topic groups are opted in", func(t *testing.T) {
		h := newManager(t)
		_, offOverrides := h.channelListWithOverrides(context.Background())
		if ov := offOverrides[channel.Feishu]; ov != nil {
			t.Fatalf("feishu without topic chats: got %+v, want nil", ov)
		}

		h.bots.mu.Lock()
		h.bots.loaded = true
		h.bots.set.TopicChats = []BotChat{{ChatID: "oc_topic_1", Name: "Team topics"}}
		h.bots.mu.Unlock()

		_, onOverrides := h.channelListWithOverrides(context.Background())
		if ov := onOverrides[channel.Feishu]; ov == nil || ov.Mode != channel.TargetOverrideTopics {
			t.Fatalf("feishu with topic chats: got %+v, want topics", ov)
		}
	})

	t.Run("channels.list bus response always includes target_overrides map", func(t *testing.T) {
		h := newManager(t)
		res, err := h.channelsCall(context.Background(), assistantwire.ChannelsList, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if _, ok := decoded["target_overrides"]; !ok {
			t.Fatalf("channels.list missing target_overrides key: %s", raw)
		}
	})
}
