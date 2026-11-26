package larksync

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Drive and wiki walking bounds.
const (
	driveDepthMost  = 4
	driveFilesMost  = 2000 // files looked at per run; beyond, the cursor stays and the rest is read next run
	drivePagesMost  = 50   // pages of one folder
	wikiDepthMost   = 6
	wikiNodesMost   = 3000 // nodes looked at per space per run
	wikiPagesMost   = 50   // pages of one node's children
	editOverlap     = time.Hour
	wikiSpacesPages = 10
)

// apiFile is one entry of GET /open-apis/drive/v1/files.
type apiFile struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	ModifiedTime string `json:"modified_time"` // seconds
	ShortcutInfo struct {
		TargetType  string `json:"target_type"`
		TargetToken string `json:"target_token"`
	} `json:"shortcut_info"`
}

// driveWalk is one Drive walk's progress.
type driveWalk struct {
	floor  int64 // older edits than this end a folder's listing (newest first)
	newest int64
	files  int
	capped bool
	over   bool // the budget ran out
}

// readDrive walks the owner's Drive from the root, newest edits first, down to driveDepthMost folders, and reads
// every document edited since the cursor (less an hour: edits saved late). Cursor "drive" is the newest edit seen.
func (r *run) readDrive(ctx context.Context) error {
	const resource = larksyncwire.ResDrive
	old := parseUnix(r.cursors[resource])
	w := &driveWalk{floor: r.backfillFrom().Unix()}
	if old > 0 {
		w.floor = old - int64(editOverlap/time.Second)
	}
	if err := r.walkFolder(ctx, w, "", 1); err != nil {
		return r.settle(ctx, resource, err)
	}
	if w.over {
		r.partial = true // what was read stays good; the cursor waits for a complete walk
		return nil
	}
	if !w.capped {
		r.moveCursor(resource, unixCursor(max(w.newest, old, w.floor)))
	}
	return nil
}

func (r *run) walkFolder(ctx context.Context, w *driveWalk, folder string, depth int) error {
	tok := ""
	for range drivePagesMost {
		if r.overBudget() {
			w.over = true
			return nil
		}
		var data struct {
			Files         []apiFile `json:"files"`
			HasMore       bool      `json:"has_more"`
			NextPageToken string    `json:"next_page_token"`
		}
		q := map[string]string{"page_size": "200", "order_by": "EditedTime", "direction": "DESC", "folder_token": folder}
		if tok != "" {
			q["page_token"] = tok
		}
		if err := r.sess.getJSON(ctx, request{family: famDrive, op: "list files", path: "/open-apis/drive/v1/files", query: q}, &data); err != nil {
			return err
		}
		for _, f := range data.Files {
			mod := parseUnix(f.ModifiedTime)
			if mod > 0 && mod < w.floor {
				return nil // newest first: the rest of this folder is older than the cursor
			}
			if f.Type == "folder" {
				if depth < driveDepthMost {
					if err := r.walkFolder(ctx, w, f.Token, depth+1); err != nil || w.over || w.capped {
						return err
					}
				}
				continue
			}
			if w.files >= driveFilesMost {
				w.capped = true
				return nil
			}
			w.files++
			w.newest = max(w.newest, mod)
			if err := r.driveFile(ctx, f, mod); err != nil {
				return err
			}
			if r.overBudget() {
				w.over = true
				return nil
			}
		}
		if !data.HasMore || data.NextPageToken == "" {
			return nil
		}
		tok = data.NextPageToken
	}
	return nil
}

// driveFile reads one Drive entry: a document's content, or a minutes token for later.
func (r *run) driveFile(ctx context.Context, f apiFile, mod int64) error {
	typ, token := f.Type, f.Token
	if typ == "shortcut" {
		typ, token = f.ShortcutInfo.TargetType, f.ShortcutInfo.TargetToken
	}
	r.learnWebHost(f.URL)
	if kind, t := TokenFromURL(f.URL); kind == "minutes" || typ == "minutes" {
		if t == "" {
			t = token
		}
		r.leads = append(r.leads, minuteLead{token: t, url: f.URL, resource: larksyncwire.ResDrive, entry: larksyncwire.EntryDrive})
		return nil
	}
	if token == "" || !docType(typ) {
		return nil
	}
	return r.consider(ctx, larksyncwire.ResDrive, larksyncwire.EntryDrive, docCandidate{
		objToken: token,
		objType:  typ,
		title:    f.Name,
		url:      f.URL,
		edited:   mod,
	})
}

// apiNode is one node of GET /open-apis/wiki/v2/spaces/{space_id}/nodes.
type apiNode struct {
	NodeToken   string `json:"node_token"`
	ObjToken    string `json:"obj_token"`
	ObjType     string `json:"obj_type"`
	Title       string `json:"title"`
	HasChild    bool   `json:"has_child"`
	ObjEditTime string `json:"obj_edit_time"` // seconds
}

// wikiWalk is one space's walk.
type wikiWalk struct {
	space  string
	floor  int64
	newest int64
	nodes  int
	capped bool
	over   bool
}

// readWiki walks every wiki space the owner can read, one resource per space ("wiki:<space_id>"), and reads every
// node's document edited since the space's cursor (less an hour). A node's document is the same content as the
// document it wraps: it is keyed by its obj_token and known by its node token too (an alias).
func (r *run) readWiki(ctx context.Context) error {
	var spaces []string
	tok := ""
	for range wikiSpacesPages {
		var data struct {
			page
			Items []struct {
				SpaceID string `json:"space_id"`
			} `json:"items"`
		}
		q := map[string]string{"page_size": "50"}
		if tok != "" {
			q["page_token"] = tok
		}
		if err := r.sess.getJSON(ctx, request{family: famWiki, op: "list wiki spaces", path: "/open-apis/wiki/v2/spaces", query: q}, &data); err != nil {
			return r.settle(ctx, larksyncwire.ResWiki, err)
		}
		for _, sp := range data.Items {
			if sp.SpaceID != "" {
				spaces = append(spaces, sp.SpaceID)
			}
		}
		if !data.HasMore || data.PageToken == "" {
			break
		}
		tok = data.PageToken
	}
	for _, space := range spaces {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		resource := larksyncwire.ResWiki + ":" + space
		old := parseUnix(r.cursors[resource])
		w := &wikiWalk{space: space, floor: r.backfillFrom().Unix()}
		if old > 0 {
			w.floor = old - int64(editOverlap/time.Second)
		}
		if err := r.walkNodes(ctx, w, resource, "", 1); err != nil {
			if err := r.settle(ctx, resource, err); err != nil {
				return err
			}
			continue
		}
		if w.over {
			r.partial = true
			return nil
		}
		if !w.capped {
			r.moveCursor(resource, unixCursor(max(w.newest, old, w.floor)))
		}
	}
	return nil
}

// walkNodes lists parent's children (the space's top when ""), reading the documents edited since the floor and going
// down into every node that has children: a wiki has no "newest first" order, so the whole tree is listed.
func (r *run) walkNodes(ctx context.Context, w *wikiWalk, resource, parent string, depth int) error {
	tok := ""
	for range wikiPagesMost {
		if r.overBudget() {
			w.over = true
			return nil
		}
		var data struct {
			page
			Items []apiNode `json:"items"`
		}
		q := map[string]string{"page_size": "50"}
		if parent != "" {
			q["parent_node_token"] = parent
		}
		if tok != "" {
			q["page_token"] = tok
		}
		if err := r.sess.getJSON(ctx, request{family: famWiki, op: "list wiki nodes", path: "/open-apis/wiki/v2/spaces/" + pathEsc(w.space) + "/nodes", query: q}, &data); err != nil {
			return err
		}
		for _, n := range data.Items {
			if w.nodes >= wikiNodesMost {
				w.capped = true
				return nil
			}
			w.nodes++
			edited := parseUnix(n.ObjEditTime)
			w.newest = max(w.newest, edited)
			if n.NodeToken != "" && n.ObjToken != "" {
				r.wikiNodes[n.NodeToken] = wikiNodeInfo{objToken: n.ObjToken, objType: strings.ToLower(n.ObjType), title: n.Title, edited: edited}
			}
			if edited >= w.floor && n.ObjToken != "" {
				var err error
				if n.ObjType == "minutes" {
					r.leads = append(r.leads, minuteLead{token: n.ObjToken, resource: resource, entry: larksyncwire.EntryWiki})
				} else if docType(n.ObjType) {
					err = r.consider(ctx, resource, larksyncwire.EntryWiki, docCandidate{
						objToken: n.ObjToken,
						objType:  n.ObjType,
						title:    n.Title,
						url:      r.wikiURL(n.NodeToken),
						alias:    n.NodeToken,
						edited:   edited,
					})
				}
				if err != nil {
					return err
				}
			}
			if r.overBudget() {
				w.over = true
				return nil
			}
			if n.HasChild && depth < wikiDepthMost {
				if err := r.walkNodes(ctx, w, resource, n.NodeToken, depth+1); err != nil || w.over || w.capped {
					return err
				}
			}
		}
		if !data.HasMore || data.PageToken == "" {
			return nil
		}
		tok = data.PageToken
	}
	return nil
}

// learnWebHost keeps the tenant's document site from a document URL the platform gave ("https://<tenant>.<cloud>"),
// so the wiki links this run builds open there directly.
func (r *run) learnWebHost(raw string) {
	if r.webHost != "" || raw == "" {
		return
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		r.webHost = u.Scheme + "://" + u.Host
	}
}

// wikiURL is a wiki node's page: on the tenant's site when known, else on the cloud's main site, which sends the
// signed-in owner on to their tenant's.
func (r *run) wikiURL(node string) string {
	if node == "" {
		return ""
	}
	host := r.webHost
	if host == "" {
		host = webHostFor(r.sess.base)
	}
	return host + "/wiki/" + node
}

// webHostFor is the document site of an open-platform base URL: the cloud's main site, or a private deployment's own
// host.
func webHostFor(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	host := u.Hostname()
	for _, cloud := range []string{"feishu.cn", "larksuite.com"} {
		if host == cloud || strings.HasSuffix(host, "."+cloud) {
			return "https://www." + cloud
		}
	}
	if p := u.Port(); p != "" {
		return u.Scheme + "://" + host + ":" + p
	}
	return u.Scheme + "://" + host
}

// docType: a Drive or wiki object the sync keeps as a document.
func docType(t string) bool {
	switch t {
	case "docx", "sheet", "bitable", "file", "doc", "mindnote", "slides":
		return true
	}
	return false
}
