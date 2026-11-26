package devices

// A computer's disk usage map (#14576, docs/server/machine-usage-map.md "Manager routes"): the routes the map reads and
// acts through, relayed to the node's machine.storage.usage, .trash, .untrash and .purge. As with the diagnosis, the
// manager checks what it can before the node is asked (the role, the body's size and shape, the capability) and passes
// the node's answer through with the machine's id; the node checks every path against its protection rules itself.
// Only a device with full credentials gets here: the hub keeps answer-only devices and extensions' tokens out, and
// usageMapRoleOK checks the role it relays again. Logs say counts and bytes, never paths.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// How long the manager waits on the node for each route.
const (
	usageMapUsageTimeout   = 15 * time.Second
	usageMapTrashTimeout   = 60 * time.Second
	usageMapUntrashTimeout = 30 * time.Second
	usageMapPurgeTimeout   = 120 * time.Second
	// usageMapBodyMost bounds a request's body.
	usageMapBodyMost = 64 << 10
	// codeUsageFailed: the node could not answer (502).
	codeUsageFailed = "usage_failed"
)

// usageMapRoutes registers the four routes on route (bus.go's).
func (d *Devices) usageMapRoutes(route func(string, func(http.ResponseWriter, *http.Request, *Machine))) {
	route("GET /api/v1/machines/{mid}/storage/usage", d.getStorageUsage)
	route("POST /api/v1/machines/{mid}/storage/trash", d.postStorageTrash)
	route("POST /api/v1/machines/{mid}/storage/trash/undo", d.postStorageUntrash)
	route("POST /api/v1/machines/{mid}/storage/trash/purge", d.postStoragePurge)
}

// getStorageUsage: GET /api/v1/machines/{mid}/storage/usage?path=&rescan=1&wait_ms= → proto.StorageUsageReport.
func (d *Devices) getStorageUsage(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !usageMapRoleOK(w, r) {
		return
	}
	q := r.URL.Query()
	p := proto.StorageUsageParams{Path: q.Get("path"), Rescan: q.Get("rescan") == "1" || q.Get("rescan") == "true"}
	if len(p.Path) > 4096 || strings.ContainsRune(p.Path, 0) {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.bad_path"))
		return
	}
	if v := q.Get("wait_ms"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.bad_path"))
			return
		}
		p.WaitMS = min(n, proto.StorageUsageWaitMostMS)
	}
	if !usageMapReady(w, m) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usageMapUsageTimeout)
	defer cancel()
	var out proto.StorageUsageReport
	if err := m.call(ctx, proto.MethodMachineStorageUsage, p, &out); err != nil {
		d.writeUsageMapErr(w, m, "usage", err)
		return
	}
	out.MachineID = m.cfg.ID
	writeJSON(w, out)
}

// postStorageTrash: POST /api/v1/machines/{mid}/storage/trash {paths, dry_run?, confirm?} → proto.StorageTrashResult.
func (d *Devices) postStorageTrash(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !usageMapRoleOK(w, r) {
		return
	}
	var in proto.StorageTrashParams
	if !readUsageMapBody(w, r, &in) || !usageMapList(w, in.Paths, "devices-usagemap.need_paths") {
		return
	}
	if !in.DryRun && !in.Confirm {
		writeErrCode(w, http.StatusBadRequest, proto.CodeConfirmationRequired, i18n.Err("devices-usagemap.confirmation_required"))
		return
	}
	if !usageMapReady(w, m) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usageMapTrashTimeout)
	defer cancel()
	var out proto.StorageTrashResult
	if err := m.call(ctx, proto.MethodMachineStorageTrash, in, &out); err != nil {
		d.writeUsageMapErr(w, m, "trash", err)
		return
	}
	if !in.DryRun {
		trashed := 0
		for _, it := range out.Items {
			if it.Status == proto.StorageTrashTrashed {
				trashed++
			}
		}
		d.logf("[%s] usage map trash from the app: %d of %d items, %d bytes waiting in the trash", m.cfg.ID, trashed, len(in.Paths), out.PendingBytes)
	}
	out.MachineID = m.cfg.ID
	writeJSON(w, out)
}

// postStorageUntrash: POST /api/v1/machines/{mid}/storage/trash/undo {trash_ids} → proto.StorageUntrashResult.
func (d *Devices) postStorageUntrash(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !usageMapRoleOK(w, r) {
		return
	}
	var in proto.StorageUntrashParams
	if !readUsageMapBody(w, r, &in) || !usageMapList(w, in.TrashIDs, "devices-usagemap.need_paths") {
		return
	}
	if !usageMapReady(w, m) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usageMapUntrashTimeout)
	defer cancel()
	var out proto.StorageUntrashResult
	if err := m.call(ctx, proto.MethodMachineStorageUntrash, in, &out); err != nil {
		d.writeUsageMapErr(w, m, "untrash", err)
		return
	}
	d.logf("[%s] usage map undo from the app: %d items put back", m.cfg.ID, len(out.Restored))
	out.MachineID = m.cfg.ID
	writeJSON(w, out)
}

// postStoragePurge: POST /api/v1/machines/{mid}/storage/trash/purge {trash_ids, confirm: true} → proto.StoragePurgeResult.
func (d *Devices) postStoragePurge(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !usageMapRoleOK(w, r) {
		return
	}
	var in proto.StoragePurgeParams
	if !readUsageMapBody(w, r, &in) || !usageMapList(w, in.TrashIDs, "devices-usagemap.need_paths") {
		return
	}
	if !in.Confirm {
		writeErrCode(w, http.StatusBadRequest, proto.CodeConfirmationRequired, i18n.Err("devices-usagemap.confirmation_required"))
		return
	}
	if !usageMapReady(w, m) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usageMapPurgeTimeout)
	defer cancel()
	var out proto.StoragePurgeResult
	if err := m.call(ctx, proto.MethodMachineStoragePurge, in, &out); err != nil {
		d.writeUsageMapErr(w, m, "purge", err)
		return
	}
	purged := 0
	for _, it := range out.Items {
		if it.Status == proto.StoragePurgePurged {
			purged++
		}
	}
	d.logf("[%s] usage map purge from the app: %d of %d items deleted for good, %d bytes freed", m.cfg.ID, purged, len(in.TrashIDs), out.FreedBytes)
	out.MachineID = m.cfg.ID
	writeJSON(w, out)
}

// readUsageMapBody decodes a body of at most usageMapBodyMost into v, or answers 400.
func readUsageMapBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, usageMapBodyMost)).Decode(v); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.need_paths"))
		return false
	}
	return true
}

// usageMapList checks a request's paths or trash ids: 1 to proto.StoragePathsMost, none empty.
func usageMapList(w http.ResponseWriter, list []string, empty string) bool {
	switch {
	case len(list) == 0:
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err(empty))
		return false
	case len(list) > proto.StoragePathsMost:
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.too_many", i18n.Args{"most": proto.StoragePathsMost}))
		return false
	}
	for _, s := range list {
		if strings.TrimSpace(s) == "" || len(s) > 4096 || strings.ContainsRune(s, 0) {
			writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.bad_path"))
			return false
		}
	}
	return true
}

// usageMapRoleOK refuses a device that may only read and answer, and an extension's token (the hub already does; this
// is the manager's own check of the role it relays): the map lists what is on the computer, and its trash moves files.
func usageMapRoleOK(w http.ResponseWriter, r *http.Request) bool {
	switch r.Header.Get(assistantwire.RoleHeader) {
	case "", "full":
		return true
	}
	writeErrCode(w, http.StatusForbidden, proto.CodeReadOnly, i18n.Err("devices-usagemap.read_only"))
	return false
}

// usageMapReady answers the request itself, and false, when the node cannot be asked: offline (503), or too old to
// have the methods (409). A node reconnecting (warm) is waited for by the call.
func usageMapReady(w http.ResponseWriter, m *Machine) bool {
	m.mu.Lock()
	reachable := m.online || m.warmLocked(time.Now())
	info := m.info
	m.mu.Unlock()
	if !reachable {
		writeErrCode(w, http.StatusServiceUnavailable, devicewire.CodeMachineOffline, i18n.Err("devices-usagemap.offline", i18n.Args{"name": m.name()}))
		return false
	}
	if !nodeHas(info, nodeproto.MachineStorageUsage) {
		writeErrCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.Err("devices-usagemap.node_outdated"))
		return false
	}
	return true
}

// writeUsageMapErr answers a failed call by what went wrong; the node's own words are logged, not shown.
func (d *Devices) writeUsageMapErr(w http.ResponseWriter, m *Machine, op string, err error) {
	var coded *rpc.Error
	errors.As(err, &coded)
	switch {
	case isOffline(err):
		writeErrCode(w, http.StatusServiceUnavailable, devicewire.CodeMachineOffline, i18n.Err("devices-usagemap.offline", i18n.Args{"name": m.name()}))
	case coded != nil && (coded.Code == proto.CodeNodeOutdated || strings.HasPrefix(coded.Message, "unknown method")):
		writeErrCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.Err("devices-usagemap.node_outdated"))
	case coded != nil && coded.Code == codeBadRequest:
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, i18n.Err("devices-usagemap.bad_path"))
	case coded != nil && coded.Code == proto.CodeConfirmationRequired:
		writeErrCode(w, http.StatusBadRequest, proto.CodeConfirmationRequired, i18n.Err("devices-usagemap.confirmation_required"))
	case errors.Is(err, context.DeadlineExceeded) || coded != nil && coded.Message == context.DeadlineExceeded.Error():
		writeErrCode(w, http.StatusGatewayTimeout, devicewire.CodeDeadline, i18n.Err("devices-usagemap.deadline", i18n.Args{"name": m.name()}))
	default:
		d.logf("[%s] usage map %s failed", m.cfg.ID, op)
		writeErrCode(w, http.StatusBadGateway, codeUsageFailed, i18n.Err("devices-usagemap.failed", i18n.Args{"name": m.name()}))
	}
}
