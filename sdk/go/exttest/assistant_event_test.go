package exttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// assistant.event from the SDK: the typed call reaches the hub's answer with its params held to the schema, the fake
// hub refuses it without assistant.events as the real one does, and the codes the real hub answers read as errors.
func TestAssistantEventFromTheSDK(t *testing.T) {
	granted := []byte(strings.Replace(string(connectorManifest), `"hub": ["kb.read"]`, `"hub": ["assistant.events"]`, 1))
	c := &exampleChat{}
	h := exttest.Start(t, granted, exttest.Serve(t, c.program()))
	ctx := context.Background()
	var got wire.AssistantEventParams
	h.Answer("assistant.event", func(raw json.RawMessage) (any, error) {
		if err := json.Unmarshal(raw, &got); err != nil {
			return nil, err
		}
		return wire.AssistantEventResult{Delivery: wire.AssistantEventDeliveryQueued}, nil
	})
	res, err := c.hub.AssistantEvent(ctx, wire.AssistantEventParams{Key: "disk-full", Severity: wire.AssistantEventSeverityWarning,
		Title: "Disk almost full", Body: "92% used", ActionHint: "Clear the cache", State: "firing", Replace: wire.AssistantEventReplaceFalse})
	if err != nil || res.Delivery != wire.AssistantEventDeliveryQueued {
		t.Fatalf("assistant.event: %+v %v", res, err)
	}
	if got.Key != "disk-full" || got.Severity != "warning" || got.Replace != wire.AssistantEventReplaceFalse || got.State != "firing" || got.ActionHint != "Clear the cache" {
		t.Fatalf("the hub got %+v", got)
	}
	// The codes the real hub answers.
	h.Answer("assistant.event", func(json.RawMessage) (any, error) {
		return nil, &ext.Error{Code: wire.AssistantErrorCodeNoMainConversation, Message: "there is no main conversation"}
	})
	var e *ext.Error
	if _, err := c.hub.AssistantEvent(ctx, wire.AssistantEventParams{Key: "k", Title: "t"}); !errors.As(err, &e) || e.Code != wire.AssistantErrorCodeNoMainConversation {
		t.Fatalf("no main conversation: %v", err)
	}
}

func TestAssistantEventNeedsItsPermission(t *testing.T) {
	c := &exampleChat{}
	exttest.Start(t, connectorManifest, exttest.Serve(t, c.program())) // permissions.hub is kb.read only
	var e *ext.Error
	_, err := c.hub.AssistantEvent(context.Background(), wire.AssistantEventParams{Key: "k", Title: "t"})
	if !errors.As(err, &e) || e.Code != wire.ErrorCodePermissionDenied || !strings.Contains(string(e.Data), "assistant.events") {
		t.Fatalf("assistant.event without assistant.events: %v", err)
	}
}
