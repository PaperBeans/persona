package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func navItemsRequest(t *testing.T, m *Machine, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/sessions/m/s1/items?"+query, nil)
	req.SetPathValue("sid", "s1")
	w := httptest.NewRecorder()
	(&Hub{}).sessionItems(w, req, m)
	return w
}

func TestSessionNavigationCompactIgnoresLegacyExcerpts(t *testing.T) {
	m := wireNode(t, func(params json.RawMessage) any {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil || string(p["user_inputs"]) != "true" || string(p["meta_only"]) != "true" || p["before_seq"] != nil {
			t.Errorf("compact index params: %s", params)
		}
		return json.RawMessage(`{"total":2,"inputs":[{"id":"input#0","seq":7,"preview":"Question","answer":"Reply"},{"id":"photo#0","seq":91,"attachments":3}],"history":"h"}`)
	})
	w := navItemsRequest(t, m, "user_inputs=1&meta_only=1&before_seq=5")
	want := `{"total":2,"inputs":[{"id":"input#0","seq":7},{"id":"photo#0","seq":91}],"history":"h"}`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("got %d %s, want %s", w.Code, w.Body, want)
	}
}

func TestSessionNavigationPreviewsAndTargetWindowWire(t *testing.T) {
	const id = "segment:second:question#0"
	m := wireNodeRPC(t, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case proto.MethodSessionInputPreviews:
			var p proto.InputPreviewRequest
			want := proto.InputPreviewRequest{SessionID: "s1", History: "h", Positions: []proto.UserInputRef{{ID: id, Seq: 40}, {ID: "gone#0", Seq: 73}}}
			if json.Unmarshal(params, &p) != nil || !reflect.DeepEqual(p, want) {
				t.Errorf("preview params: %s", params)
			}
			return json.RawMessage(`{"history":"h","inputs":[{"id":"segment:second:question#0","seq":40,"preview":"Question","answer":"Reply","attachments":2,"text":"unused"}],"items":[{"text":"unused"}]}`), nil
		case proto.MethodSessionItemsWindow:
			var p proto.ItemsWindowRequest
			if json.Unmarshal(params, &p) != nil || p != (proto.ItemsWindowRequest{SessionID: "s1", FromSeq: 38, ThroughSeq: 42, History: "h"}) {
				t.Errorf("window params: %s", params)
			}
			return proto.ItemsWindow{History: "h", FromSeq: 37, ToSeq: 42, HasMore: true, MoreAfter: true,
				Items: []proto.Item{{ID: id, Seq: 40, Role: "user", Kind: proto.KindImage, Image: &proto.Image{Mime: "image/png", Data: "bytes"}}}}, nil
		default:
			t.Errorf("unexpected RPC %s", method)
			return nil, nil
		}
	})
	w := navItemsRequest(t, m, "preview="+url.QueryEscape("40:"+id)+"&preview=73:gone%230&history=h")
	want := `{"inputs":[{"id":"segment:second:question#0","seq":40,"preview":"Question","answer":"Reply","attachments":2}],"history":"h"}`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("got %d %s, want %s", w.Code, w.Body, want)
	}
	w = navItemsRequest(t, m, "around_seq=40&radius=2&history=h")
	var out proto.ItemsWindow
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.History != "h" || out.FromSeq != 37 ||
		out.ToSeq != 42 || !out.HasMore || !out.MoreAfter || len(out.Items) != 1 || out.Items[0].Seq != 40 ||
		out.Items[0].Image == nil || !out.Items[0].Image.Lazy || out.Items[0].Image.Data != "" ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid window: %d %s", w.Code, w.Body)
	}
}

func TestSessionNavigationRejectsMalformedOrUnboundedQueries(t *testing.T) {
	for _, query := range []string{
		"meta_only=1", "user_inputs=1&meta_only=", "user_inputs=1&meta_only=1&meta_only=1",
		"preview=", "preview=%zz", "preview=40", "preview=0:item", "preview=-1:item", "preview=2147483647:item",
		"preview=1:item&preview=1:item", "preview=1:" + strings.Repeat("a", proto.MaxNavigationID+1),
		strings.Repeat("preview=1:item&", proto.MaxInputPreviews) + "preview=2:item",
		"preview=1:item&user_inputs=1", "preview=1:item&around_seq=1", "preview=1:item&history=h&history=i",
		"preview=1:item&history=" + strings.Repeat("h", proto.MaxNavigationHistory+1),
		"around_seq=", "around_seq=0", "around_seq=2147483647", "around_seq=1&around_seq=2",
		"around_seq=1&radius=-1", "around_seq=1&radius=", "around_seq=1&radius=101",
		"around_seq=1&radius=0&radius=0", "around_seq=1&before_seq=1", "around_seq=2&after_seq=0",
		"around_seq=1&from_seq=1&through_seq=2", "around_seq=1&turns=2", "radius=0",
	} {
		t.Run(query, func(t *testing.T) {
			// A bad query is rejected before it can reach a node or fall through to a body read.
			w := navItemsRequest(t, nil, query)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bad_request") {
				t.Fatalf("invalid query yielded %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestSessionNavigationHandlesOldNodesAndStalePositions(t *testing.T) {
	for _, c := range []struct {
		name   string
		query  string
		reply  any
		err    error
		status int
		code   string
	}{
		{"old preview method", "preview=40:item", nil, &rpc.Error{Message: "unknown method " + proto.MethodSessionInputPreviews}, 409, proto.CodeNodeOutdated},
		{"old window method", "around_seq=40", nil, &rpc.Error{Message: "unknown method " + proto.MethodSessionItemsWindow, Code: "unknown_method"}, 409, proto.CodeNodeOutdated},
		{"old hub ignoring selector cannot be used", "preview=40:item", json.RawMessage(`{"items":[{"id":"whole","seq":1}],"history":"h"}`), nil, 502, "internal"},
		{"wrong preview position", "preview=40:item", proto.InputPreviews{Inputs: []proto.UserInputPosition{{ID: "other", Seq: 40}}, History: "h"}, nil, 502, "internal"},
		{"stale generation", "preview=40:item&history=old", proto.InputPreviews{Inputs: []proto.UserInputPosition{}, History: "new"}, nil, 409, proto.CodeNavigationChanged},
		{"target moved past end", "around_seq=40&radius=1", proto.ItemsWindow{Items: []proto.Item{}, FromSeq: 20, ToSeq: 20, History: "new"}, nil, 404, "not_found"},
		{"invalid coverage", "around_seq=40&radius=1", proto.ItemsWindow{Items: []proto.Item{{ID: "out", Seq: 42}}, FromSeq: 38, ToSeq: 41, History: "h"}, nil, 502, "internal"},
		{"empty valid preview", "preview=40:item", proto.InputPreviews{Inputs: []proto.UserInputPosition{}, History: "h"}, nil, 200, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := wireNodeRPC(t, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				if method == "session.items" {
					t.Error("targeted read fell back to a transcript page")
				}
				return c.reply, c.err
			})
			w := navItemsRequest(t, m, c.query)
			var body struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != c.status || body.Code != c.code {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body, c.status, c.code)
			}
		})
	}
}

func TestSessionNavigationWindowClampsSequenceLimit(t *testing.T) {
	m := wireNodeRPC(t, func(_ context.Context, method string, raw json.RawMessage) (any, error) {
		var p proto.ItemsWindowRequest
		if method != proto.MethodSessionItemsWindow || json.Unmarshal(raw, &p) != nil || !p.Valid() ||
			p.ThroughSeq != proto.MaxNavigationSeq || p.FromSeq != proto.MaxNavigationSeq-proto.DefaultWindowRadius {
			t.Errorf("invalid clamped window %s %s", method, raw)
		}
		return proto.ItemsWindow{Items: []proto.Item{}, FromSeq: p.FromSeq - 1, ToSeq: p.ThroughSeq, HasMore: true}, nil
	})
	w := navItemsRequest(t, m, "around_seq="+strconv.Itoa(proto.MaxNavigationSeq))
	if w.Code != http.StatusOK {
		t.Fatalf("near-limit window %d %s", w.Code, w.Body)
	}
}
