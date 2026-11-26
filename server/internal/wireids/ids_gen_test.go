package wireids

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Every sample of spec/fixtures/ids reads into its generated type and writes back as the same JSON; an ID is a bare
// string, an AgentRef two required fields and no other.
func TestIDSamplesRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "ids", "ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name     string `json:"name"`
			Expected struct {
				Definition string          `json:"definition"`
				Value      json.RawMessage `json:"value"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range file.Cases {
		var v any
		switch c.Expected.Definition {
		case "AgentRef":
			v = new(AgentRef)
		case "MachineID":
			v = new(MachineID)
		case "SessionID":
			v = new(SessionID)
		case "InteractionID":
			v = new(InteractionID)
		case "TodoID":
			v = new(TodoID)
		case "ConversationID":
			v = new(ConversationID)
		case "HubID":
			v = new(HubID)
		case "DeviceID":
			v = new(DeviceID)
		case "ExtensionID":
			v = new(ExtensionID)
		case "ClientMsgID":
			v = new(ClientMsgID)
		case "InboxItemID":
			v = new(InboxItemID)
		case "ConnectorID":
			v = new(ConnectorID)
		case "SubscriptionID":
			v = new(SubscriptionID)
		default:
			t.Fatalf("%s: unregistered definition %s", c.Name, c.Expected.Definition)
		}
		d := json.NewDecoder(bytes.NewReader(c.Expected.Value))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		_ = json.Unmarshal(c.Expected.Value, &want)
		_ = json.Unmarshal(out, &got)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: written back differently: want %s got %s", c.Name, c.Expected.Value, out)
		}
	}
}

func TestAgentRefRefusesAnUnknownField(t *testing.T) {
	var ref AgentRef
	d := json.NewDecoder(bytes.NewReader([]byte(`{"machine":"m","session":"s","extra":1}`)))
	d.DisallowUnknownFields()
	if err := d.Decode(&ref); err == nil {
		t.Fatal("an unknown field decoded")
	}
	if err := json.Unmarshal([]byte(`{"machine":1,"session":"s"}`), &ref); err == nil {
		t.Fatal("a mistyped machine decoded")
	}
}

func TestIDsAreBareStrings(t *testing.T) {
	out, err := json.Marshal(AgentRef{Machine: "m", Session: "s"})
	if err != nil || string(out) != `{"machine":"m","session":"s"}` {
		t.Fatalf("%s %v", out, err)
	}
	if TodoID("todo_a1").String() != "todo_a1" {
		t.Fatal("String")
	}
}
