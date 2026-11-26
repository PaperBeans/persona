package hub

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// sessionNavigation handles targeted views on the same authorized path as ordinary transcript pages.
func (h *Hub) sessionNavigation(w http.ResponseWriter, r *http.Request, m *Machine, q url.Values, queryErr error) bool {
	if queryErr != nil && hasNavigationQuery(r.URL.RawQuery) {
		writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
		return true
	}
	preview, around := q.Has("preview"), q.Has("around_seq")
	if q.Has("meta_only") && (len(q["meta_only"]) != 1 || q.Get("meta_only") != "1" ||
		len(q["user_inputs"]) != 1 || q.Get("user_inputs") != "1") ||
		q.Has("radius") && !around || preview && around {
		writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
		return true
	}
	if !preview && !around {
		return false
	}
	for _, key := range []string{"user_inputs", "meta_only", "before_seq", "after_seq", "from_seq", "through_seq", "limit", "turns", "strict", "fold_work"} {
		if q.Has(key) {
			writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
			return true
		}
	}
	history := q.Get("history")
	if len(q["history"]) > 1 || (q.Has("history") && history == "") || len(history) > proto.MaxNavigationHistory {
		writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	session := proto.NavigationSessionID(r.PathValue("sid"))
	if preview {
		p := proto.InputPreviewRequest{SessionID: session, History: history}
		if len(q["preview"]) > proto.MaxInputPreviews {
			writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
			return true
		}
		for _, value := range q["preview"] {
			seq, id, _ := strings.Cut(value, ":")
			p.Positions = append(p.Positions, proto.UserInputRef{Seq: navigationNumber(seq), ID: proto.UserInputItemID(id)})
		}
		if !p.Valid() {
			writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
			return true
		}
		h.sessionInputPreviews(ctx, w, r, m, p)
		return true
	}
	seq, radius := navigationNumber(q.Get("around_seq")), proto.DefaultWindowRadius
	if q.Has("radius") {
		radius = navigationNumber(q.Get("radius"))
	}
	if len(q["around_seq"]) != 1 || seq < 1 || seq > proto.MaxNavigationSeq ||
		len(q["radius"]) > 1 || radius < 0 || radius > proto.MaxWindowRadius {
		writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
		return true
	}
	p := proto.ItemsWindowRequest{SessionID: session, History: history,
		FromSeq: max(1, seq-radius), ThroughSeq: seq + min(radius, proto.MaxNavigationSeq-seq)}
	h.sessionItemsWindow(ctx, w, r, m, p, seq)
	return true
}

func hasNavigationQuery(raw string) bool {
	for part := range strings.SplitSeq(raw, "&") {
		key, _, _ := strings.Cut(part, "=")
		key, _ = url.QueryUnescape(key)
		switch key {
		case "meta_only", "preview", "around_seq", "radius":
			return true
		}
	}
	return false
}

func navigationNumber(s string) int {
	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil {
		return -1
	}
	return int(n)
}

func (h *Hub) sessionInputPreviews(ctx context.Context, w http.ResponseWriter, r *http.Request, m *Machine, p proto.InputPreviewRequest) {
	var out proto.InputPreviews
	if err := m.call(ctx, proto.MethodSessionInputPreviews, p, &out); err != nil {
		h.sessionNavigationError(w, r, err)
		return
	}
	if !out.Matches(p) {
		writeErr(w, http.StatusBadGateway, i18n.Err("chat-navigation.unavailable"))
		return
	}
	if p.History != "" && out.History != p.History {
		writeErrCode(w, http.StatusConflict, proto.CodeNavigationChanged, i18n.Err("chat-navigation.history_changed"))
		return
	}
	writeJSON(w, out)
}

func (h *Hub) sessionItemsWindow(ctx context.Context, w http.ResponseWriter, r *http.Request, m *Machine, p proto.ItemsWindowRequest, target int) {
	var out proto.ItemsWindow
	if err := m.call(ctx, proto.MethodSessionItemsWindow, p, &out); err != nil {
		h.sessionNavigationError(w, r, err)
		return
	}
	if !out.Matches(p) {
		writeErr(w, http.StatusBadGateway, i18n.Err("chat-navigation.unavailable"))
		return
	}
	if p.History != "" && out.History != p.History {
		writeErrCode(w, http.StatusConflict, proto.CodeNavigationChanged, i18n.Err("chat-navigation.history_changed"))
		return
	}
	if target <= out.FromSeq || target > out.ToSeq {
		writeErr(w, http.StatusNotFound, i18n.Err("chat-navigation.target_missing"))
		return
	}
	lang := h.requestLang(r)
	out.Items = clientItemsCompat(lang, h.requestWants(r, featureCardRing), h.requestWants(r, featureMarkdownCloseout), h.requestWants(r, proto.FeatureToolViews), localizedItems(lang, h.withInjected(out.Items)))
	h.ttft.annotate(m.cfg.ID, string(p.SessionID), out.Items, false)
	out.Items = proto.WithoutImageData(out.Items)
	writeItemsJSON(w, out)
}

func (h *Hub) sessionNavigationError(w http.ResponseWriter, r *http.Request, err error) {
	if h.nodeOutdated(w, r, err, "chat-navigation.node_outdated", nil) {
		return
	}
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case "bad_request":
			writeErr(w, http.StatusBadRequest, i18n.Err("chat-navigation.invalid"))
			return
		case proto.CodeNavigationChanged:
			writeErrCode(w, http.StatusConflict, re.Code, i18n.Err("chat-navigation.history_changed"))
			return
		}
	}
	callErr(w, err)
}
