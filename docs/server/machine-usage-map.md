# Disk usage map (#14576)

**Audience:** everyone who owns a computer that fills up. The map is a picture, not a tool for engineers: boxes sized by
what they hold, a tap to look inside, and plain actions (Move to Trash, Offload, Clean cache). Paths and scan details
appear only in details and menus. Follows principles 1 (defaults first), 2 (progressive disclosure), 3 (user control:
every change is confirmed and names what it touches) and 7 (fast, local-first, stateful) of `docs/design/PRINCIPLES.md`.

It builds on what `docs/server/machines.md` already describes and does not repeat it: the per-mount report, the
hotspot categories and the allowlisted cleanup (#12392), and the disk alert sheet with its volumes, largest folders and
cache cleanup (#8037). Those lists, their scans, their cache catalog and their confirmation dialogs stay as they are.
This adds four things:

1. an interactive, drill-down **treemap** of one computer's disk, on the web and natively in the Apple apps;
2. a guarded **Move to Trash** for items picked on the map;
3. a generic extension point through which an extension **offloads items to the cloud**, lists what is already
   offloaded (drawn beside the local map on one scale) and brings items back;
4. **low-disk guidance**: every disk alert leads straight to the map with the biggest reclaimable items marked.

Analysis and trash are the node's own Go code on macOS, Linux and Windows; nothing is executed. Offloading is never
core's: core does not know any offload product, only the extension point.

| Part | Where |
| --- | --- |
| Node scan, cache, protection rules, trash | `server/internal/node/diskmap/` (walk, cache, rules, overview, apps), `server/internal/node/machine_storage_usage.go` (the five methods; the trash itself is the existing `toTrash` in `server/internal/node/fileedit.go`) |
| Wire types and method names | `server/internal/proto/machine_storage_usage.go` |
| HTTP routes (role, capability, limits, pass-through) | `server/internal/devices/usagemap.go`, relayed by the hub from the route list in `server/internal/devicewire/wire.go` |
| Extension point `disk_offload` | `server/internal/extension/disk_offload.go` and `server/internal/extension/points.go`, `server/internal/extmgr/extension_offload.go`, `schema/extension/offload.schema.json` |
| Web | `web/src/views/machines/` (new `UsageMap*` components), mounted in `web/src/views/machines/StoragePanel.tsx` |
| Apple | `apple/Persona/Views/Machines/` (new `UsageMap*` files), entered from `apple/Persona/Views/Machines/MachineDetailView.swift` |

## Node

### `machine.storage.usage`

`{path?, rescan?, wait_ms?}` → `StorageUsageReport`. Capability `machine_storage_usage` (announced in
`server/internal/nodeproto/nodeproto.go`), so an older node answers 409 `node_outdated` before it is asked.

- **`path` empty: the overview.** The volume that holds the home folder, split into fixed groups whose bytes add up
  to the volume: `apps` (macOS only), `home` (the folders and files directly in the home folder that people fill),
  `appdata` (the children of `~/Library` on macOS minus what apps claimed, the children of `AppData` on Windows, and
  the dot folders directly in home on every system), `system` (what is measured outside home on the home's volume:
  macOS `/Applications` entries that are not apps, `/Library`, `/opt`, `/usr/local`, `/private/var`; Linux `/opt`,
  `/usr/local`, `/var`; Windows `%ProgramFiles%`, `%ProgramFiles(x86)%`, `%ProgramData%`; each where it exists and is
  readable, a root reached through a link resolved first; and one `unmeasured` box for used − measured, never below 0)
  and `free`. If the boxes exceed the volume (clones, snapshots) the root grows to fit.
- **`path` set: one folder**, its direct children sized (depth 1), each child carrying its own largest children when
  the walk already knows them (depth 2, see "Prefetch"). Any absolute folder is accepted (`~` and `~/…` too) except the
  pseudo filesystems (`/proc`, `/sys`, `/dev`, `/run`); a child on another filesystem is a `mount` node with its
  `statfs` numbers (its `bytes` are that filesystem's used bytes and are not part of the parent's `bytes`), not walked
  from its parent, and can be opened as a path of its own. The listing shows names and
  sizes only, which a full device already sees in the file browser.
- **App aggregation (macOS).** Each `.app` in `/Applications` and `~/Applications` becomes an `app` node with up to
  three children keyed `bundle`, `cache` (`~/Library/Caches/<bundle id>`) and `data`
  (`~/Library/Application Support/<bundle id or name>`, `~/Library/Containers/<bundle id>`), read from the bundle's
  `Info.plist` in Go (XML or binary; cached by the plist's modification time). `bundle` is a group (id
  `group:bundle:<app path>`) holding the `.app` itself; `cache` is that folder's own node; `data` is the one folder's
  node, or a group (id `data:<app path>`) when there are two. What an app claims is left out of `appdata`, so nothing is
  counted twice. Linux and Windows have no `apps` group in version 1.
- **Sizes** are allocated blocks (what frees when the item goes), one filesystem per walk, symbolic links and Windows
  reparse points never followed, a hard-linked file counted once per scan. On Windows sizes are logical bytes and
  hard links are not deduplicated; cloud placeholders (recall-on-access) count as 0.
- **`used_at`** is the newest modification time seen in the subtree: free to collect while walking, and what the
  "last used" tint and the cold-item suggestions read.

**It answers at once and fills in.** The call starts (or joins) the scan of `path` and waits at most `wait_ms`
(default 1500, limit 10 000). A fast folder answers `state: "ready"` in one round trip. A slow one answers
`state: "scanning"` with the children measured so far; the scan goes on in the node and the next call returns more.
Clients poll once a second while `scanning`, so boxes appear and grow instead of a spinner.

**Bounds.** A folder scan ends after 20 s, the overview after 120 s; a scan reads at most 3 000 000 entries; what is
cut short is `partial: true` on the node it happened in. Four walkers per scan (eight from 8 cores up), two scans at a
time, later ones queue; calls for a path being scanned join it. A node lists its 64 largest children and sums the rest
into one `other` node (`items` says how many); a prefetched child lists 16.

**Prefetch.** While a child folder is being sized, the walker also totals that child's own direct children. Opening
the child is then instant (`cached: true`), and the treemap can draw two levels inside a box without a second request.
The overview walks home, `~/Library` and its `Caches`, `Application Support` and `Containers`, `~/Applications`,
`AppData` and `/Applications` with every child kept (they are what the groups are made of); the listing still shows 64.
Walking breadth first through those folders and depth first below them, a scan that hits its budget still has every
group's first two levels.

**Cache and invalidation.** Results stay in the node's memory: 512 folders, 15 minutes, least recently used first out.
`rescan: true` drops `path` and everything under it before scanning. A successful trash removes the item from its
parent's cached entry and subtracts its bytes (the cached size, the one the map showed) up the cached ancestors, and an
undo adds it back, so the map is right without a rescan; the overview's `free` is read again with `statfs` on every
answer. The scan goes on when the asking call returns (its context is not the call's); a later call joins it. Nothing is written to disk and nothing runs in the
background unless a client asked.

```json
{"machine_id": "mac", "at": 1767000000, "os": "darwin", "path": "", "state": "scanning", "partial": false, "cached": false,
 "scanned_at": 1767000000, "duration_ms": 1480, "home": "/Users/me",
 "volume": {"mount": "/", "total_bytes": 245107195904, "used_bytes": 221000000000, "free_bytes": 24107195904},
 "node": {"id": "group:root", "name": "", "kind": "root", "category": "other", "bytes": 245107195904, "has_children": true,
  "children": [
   {"id": "group:apps", "name": "", "key": "apps", "kind": "group", "category": "apps", "bytes": 41000000000, "has_children": true,
    "children": [{"id": "/Applications/Example.app", "name": "Example", "path": "/Applications/Example.app", "display": "/Applications/Example.app",
      "kind": "app", "category": "apps", "bytes": 9100000000, "used_at": 1741000000, "has_children": true,
      "can_trash": false, "can_offload": true, "refusal": "app_bundle", "app": {"bundle_id": "com.example.app", "version": "3.2"}}]},
   {"id": "group:appdata", "name": "", "key": "appdata", "kind": "group", "category": "appdata", "bytes": 30000000000, "has_children": true,
    "partial": false, "can_trash": false, "can_offload": false,
    "children": [{"id": "/Users/me/Library/Caches/go-build", "name": "go-build", "path": "/Users/me/Library/Caches/go-build",
      "display": "~/Library/Caches/go-build", "kind": "dir", "category": "cache", "bytes": 6200000000, "has_children": true,
      "partial": false, "can_trash": true, "can_offload": false, "cache_id": "go-build", "cache_in_use": false}]},
   {"id": "group:free", "name": "", "key": "free", "kind": "free", "category": "free", "bytes": 24107195904, "has_children": false,
    "partial": false, "can_trash": false, "can_offload": false}]}}
```

| Field | Values |
| --- | --- |
| `kind` | `root`, `group`, `dir`, `file`, `app`, `mount`, `other`, `free`, `unmeasured` |
| `id` | a path node's absolute path; a fixed box `group:<key>`; the summed rest `group:other:<parent id>`; an app's bundle `group:bundle:<app path>` and two data folders `data:<app path>` |
| `has_children`, `partial`, `can_trash`, `can_offload` | always present, `false` included (the example leaves some out on the first boxes for brevity) |
| `key` | on fixed boxes the clients name themselves: `apps`, `home`, `appdata`, `system`, `free`, `other`, `unmeasured`, `bundle`, `cache`, `data` |
| `category` | what the box is colored by: `apps`, `personal`, `appdata`, `cache`, `system`, `free`, `other` |
| `can_trash`, `can_offload`, `refusal` | the protection rules below, evaluated by the node; `refusal` is the code of the first rule that refuses trash |
| `cache_id`, `cache_in_use` | the folder is exactly one cache of the existing catalog (`proto.CacheIDs`): clients offer the existing **Clean** flow for it instead of Trash |

### Protection rules

Ported from a sibling product's trash policy and widened to three systems. The node decides, every time, on the real
path (`lstat`, never following the last link; the links in its parents resolved first); the flags on a usage node are
the same function's answer. Names compare case-insensitively on macOS and Windows.

The rules are evaluated in this order and the first that refuses names the code: `not_found`, `not_regular`,
`symlink`; `credentials`, `agent_data`, `persona_data` for a path inside one of those folders; `cloud_synced`;
`system_cache`; `inside_app` / `app_bundle` (a `.app` under `~/Library` is a cache or data folder, not an app);
`mount_point`; `managed_library`; `outside_home`, `home_root`, `standard_folder`; then, for trash only, the exact
catalog caches are allowed; `app_managed`; `dot_folder`; and last `credentials`, `agent_data`, `persona_data` for a
folder that holds one of them (so `~/Library` reads `app_managed`, not `credentials`, though it holds the keychains).

| Code | Refused |
| --- | --- |
| `not_found`, `not_regular` | gone, or not a regular file or folder |
| `symlink` | a symbolic link or reparse point (act on what it points to) |
| `outside_home` | anything not under the home folder |
| `home_root` | the home folder itself |
| `standard_folder` | `Desktop`, `Documents`, `Downloads`, `Movies`, `Music`, `Pictures`, `Public`, `Videos`, `Templates` themselves (their contents are fine) |
| `app_managed` | inside `~/Library`, `~/Applications`, the trash (`~/.Trash`, `~/.local/share/Trash`), or `AppData`, except for trash: a direct child of `~/Library/Caches`, `~/Library/Developer/Xcode/DerivedData` or `AppData\Local\Temp`, and the cache catalog's folders |
| `system_cache` | `~/Library/Caches/com.apple.*` and `CloudKit`, `FamilyCircle`, `GeoServices`, `PassKit` |
| `dot_folder` | a dot folder directly in home (`~/.config`, `~/.local`), except a direct child of `~/.cache` and the catalog's folders |
| `credentials` | anything in or holding `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, keychains |
| `agent_data` | anything in or holding the Agents' own folders (`~/.claude`, `~/.codex` and the other runtimes' config folders the node already knows) |
| `persona_data` | anything in or holding `~/.persona`, the node's data folder or a hub's state |
| `inside_app`, `app_bundle` | inside a `.app`; a `.app` itself (trash refuses; offload is the extension's to accept) |
| `managed_library` | photo, music and video libraries, virtual machine and container disks, sparse images, partial downloads (by extension and by the fixed file names) |
| `cloud_synced` | under `~/Library/Mobile Documents`, `~/Library/CloudStorage`, `~/Dropbox` or (Windows) `~/OneDrive…` and `~/iCloudDrive`: removing it removes it on every device |
| `mount_point` | a folder that is another filesystem's root |

`can_offload` uses the same rules without the cache exceptions (a cache is cleaned, not uploaded) and without
`app_bundle` for an app directly in `/Applications` or `~/Applications` (which also passes `outside_home`); whether a
provider takes the item is its own answer (`offload.run` with `dry_run`).

### `machine.storage.trash`, `machine.storage.untrash`, `machine.storage.purge`, `machine.storage.check`

- **`machine.storage.trash`** `{paths (1–50, absolute), dry_run?, confirm?}` → `StorageTrashResult`. Neither flag is
  `confirmation_required`. Each path is checked again by the rules above and moved with the node's existing `toTrash`:
  `~/.Trash` on macOS, `$XDG_DATA_HOME/Trash` (with its `.trashinfo`) on Linux, the Recycle Bin through the shell on
  Windows. Items are independent: one refusal does not undo the others. Each item answers `status` (`preview`,
  `trashed`, `refused`, `missing`, `failed`), `bytes`, `refusal` or `error`, and a `trash_id`.
  Folders the map has not measured are sized within one 20 s budget for the whole call; past it an item's `bytes`
  (and so `pending_bytes`) is a lower bound. The result carries `available_before` / `available_after` (`statfs`) and `pending_bytes`: **moving to the trash
  frees nothing until the trash is emptied**, and the clients say so instead of claiming the space.
- **`machine.storage.untrash`** `{trash_ids}` puts items back (Undo), refusing a place that is taken.
- **`machine.storage.purge`** `{trash_ids, confirm: true}` deletes for good only items this flow put in the trash:
  an id must decode to a plain name inside the node's own trash folder and be one this node issued since it started
  (it keeps the last 2000 in memory; an id from before a restart, or made up, answers `missing` and the user empties
  the trash themselves). It is the second, separately confirmed step that actually returns the space; `freed_bytes` is
  measured. Windows answers `unsupported` in version 1.
- **`machine.storage.check`** `{paths (1–50), purpose: "trash" | "offload"}` → `{items: [{path, ok, refusal?, kind,
  bytes?}]}`, no side effect; `kind` is `file`, `dir`, `app` or empty when the path is not there, `bytes` the cached
  size when the map knows it. The manager calls it before handing paths to an extension.

None of these is on the read-only facade (`node.MachineOps`), the `ops` command line or the opt-in MCP server:
the usage scan may join them later; the mutating three never do.

## Manager routes

In the devices manager, like the diagnosis routes: full device credentials only (an answer-only device and an
extension's token get 403 `read_only` at the hub and again at the manager), the capability checked before the node is
asked, the node's answer passed through with `machine_id` set.

| Route | Node method | Manager timeout |
| --- | --- | --- |
| `GET /api/v1/machines/{mid}/storage/usage?path=&rescan=1&wait_ms=` | `machine.storage.usage` | 15 s |
| `POST /api/v1/machines/{mid}/storage/trash` `{paths, dry_run?, confirm?}` | `machine.storage.trash` | 60 s |
| `POST /api/v1/machines/{mid}/storage/trash/undo` `{trash_ids}` | `machine.storage.untrash` | 30 s |
| `POST /api/v1/machines/{mid}/storage/trash/purge` `{trash_ids, confirm}` | `machine.storage.purge` | 120 s |

Bodies are capped at 64 KiB, `paths` and `trash_ids` at 50. Errors are `{error, code}` in the reader's language
(`devices-usagemap`): 400 `bad_request`, 400 `confirmation_required`, 403 `read_only`, 404 `not_found`,
409 `node_outdated`, 503 `machine_offline`, 504 `deadline`, 502 `usage_failed`. A hub from before these routes answers
404 without a code, which clients read as "update the hub". A node's `bad_request` (a relative path, a pseudo
filesystem) is 400 `bad_request`; the node's own words are logged, never shown. The manager logs counts and bytes of a
trash or purge, never the paths.

## Extension point `disk_offload`

An extension that can move items from a computer to storage elsewhere contributes `disk_offload` (version 1,
namespace `offload`). Core draws the offered action and the already-offloaded side of the map and asks for
confirmation; the extension does the work its own way (its hub program, a program on the computer, a command-line tool
it installs). Several providers can coexist; clients list them by name.

```json
"contributes": {"disk_offload": [{"id": "cloud", "name": "Example Cloud", "kinds": ["file", "dir", "app"], "restore": true}]}
```

`id` `a-z0-9_-`; `name` 1–40 characters; `kinds` any of `file`, `dir`, `app` (default `file`, `dir`); `restore` says
items can be brought back; the program speaks the protocol. The install page says the extension can move items off
the user's computers (`disk_offload_title`). Every call carries `contribution`, `locale` and
`machine: {id, name, os, online}` (the same object a `machine_section` gets). The schema is
`schema/extension/offload.schema.json` (generated as `wire.Offload*` in the Go SDK, `ext.OffloadNamespace`; in the
web, Swift and Kotlin wire as `Offload*` / `ExtensionOffload*`), which also types the routes' answers below
(`OffloadProviders`, `OffloadRunRequest`, `OffloadStarted`, `OffloadError`).

| Method | Params | Result |
| --- | --- | --- |
| `offload.inventory` | `{refresh?}` | `{state, detail?, tool?, at, total_bytes, stored_bytes?, partial?, items, more?: {count, bytes}}`. `state`: `ready`, `needs_install`, `needs_setup`, `unavailable`. At most 500 `items`, largest first (`items` may be left out when there are none) |
| `offload.run` | `{action: "offload" \| "restore", paths?, items?, dry_run?, install?, request_id}` | `{status: "started", job}` · `{status: "needs_install", tool}` · `{status: "refused", items: [{path? \| id?, reason}]}` · with `dry_run`: `{status: "ok", items: [{path, ok, reason?, bytes?}], tool?}` |
| `offload.job` | `{job}` | `{state: "running" \| "done" \| "failed" \| "canceled", phase?, done_bytes, total_bytes, freed_bytes?, error?, items?: [{path? \| id?, status, error?, bytes?}]}`. `phase`: `install`, `check`, `upload`, `verify`, `cleanup`, `download` |
| `offload.cancel` | `{job}` | `{}` (optional: `unknown_method` means it cannot be canceled) |

An inventory item: `{id, name, kind, bytes, stored_bytes?, origin_path?, stub_path?, local_bytes?, offloaded_at?,
used_at?, group?, restorable}`. `kind` is `file`, `dir`, `app`, `app_data`, `bundle`, `session` or `other`; `bytes` is
the size the item had on disk (what the map draws, so both sides share a scale); `origin_path` is where it was and
where a restore puts it; `stub_path` is the placeholder left behind, if any; `local_bytes` is what of it is cached on
the computer now. `tool` is `{name, title?, version?, installed, source?, detail?}`: `source` is one sentence, in the
caller's language, saying where the tool comes from and where it is installed.

The manager serves the point to the apps (full devices only; extension tokens are refused):

| Route | Does |
| --- | --- |
| `GET /api/v1/extensions/-/offload?machine={mid}` | `{providers: [{extension, id, name, kinds, restore}]}` of the enabled contributions; no program is called |
| `GET /api/v1/extensions/{id}/-/offload/{cid}/inventory?machine={mid}&refresh=1` | `offload.inventory`, 20 s |
| `POST /api/v1/extensions/{id}/-/offload/{cid}/run` `{machine, action, paths?, items?, dry_run?, confirm?, install?}` | `offload.run`, 30 s. 200 for a dry run, 202 `{job}` when started |
| `GET /api/v1/extensions/{id}/-/offload/{cid}/jobs/{job}?machine={mid}` | `offload.job`, 10 s |
| `DELETE /api/v1/extensions/{id}/-/offload/{cid}/jobs/{job}?machine={mid}` | `offload.cancel`, 10 s → `{canceled}`: `false` when the program has no `offload.cancel` (`unknown_method`) |

`machine` is a computer's id or name. The inventory and job answers always have `items` (an empty list included).
Before an offload (a dry run too) reaches the program the manager checks every path with `machine.storage.check`
(`purpose: "offload"`) and answers 422 `offload_refused` with the refused items, each `{path, refusal, reason}`
(`refusal` the rule's code, `reason` in the reader's language), when any fails, and otherwise hands the program the
paths as the computer resolved and judged them (absolute, cleaned, `~` expanded, the folders they are in followed
through links; two spellings of one path are 400 `bad_request`); that check needs the computer online
(else 503 `machine_offline`) and a node with `machine_storage_usage` (else 409 `node_outdated`), and its own failures
are 504 `deadline` and 502 `usage_failed`. A restore names inventory item ids and is not checked on the computer (the
provider puts them back where they were); a provider without `restore` refuses it with 400 `bad_request`. 1–50 paths
(offload) or item ids (restore) a run, each once, and none of the other kind; the body is at most 64 KiB and unknown
fields are refused. Without `dry_run` or `confirm` the answer is 400 `confirmation_required`; `install` is never sent
with a dry run. The program's `refused` is 422 `offload_refused` with its own items; `started` without a job, or `ok`
for a run that is not a dry run, is an answer the manager refuses. Every answer is held to `offload.schema.json`, and
one that breaks it is 502 `plugin_failed` (logged with the rule it broke, never the answer). Errors are
`OffloadError` `{error, code, tool?, items?, detail?}` in the reader's language (`hub-extension_offload`): 400
`bad_request`, 400 `confirmation_required`, 403 `read_only`, 404 `not_found` (no such extension, enabled contribution
or machine), 409 `node_outdated`, 409 `tool_install_required` (with `tool`), 422 `offload_refused` (with `items`), 502
`plugin_failed` (a program that is not running, has no such method, failed — `detail` is its own words on one line,
clipped — or answered out of the schema), 502 `usage_failed`, 503 `machine_offline`, 504 `deadline`. The manager logs
that a run started with counts of paths and items, never the paths.

**Install confirmation.** When the provider's tool is not on that computer, `offload.run` without `install` answers
`needs_install`, which the manager turns into 409 `{code: "tool_install_required", error, tool}`. Nothing has been
installed or moved. The client then shows a second confirmation, naming the tool, the computer and `tool.source`, and
on approval posts the same run with `install: true`; the job starts with phase `install` and goes on to the offload.
Declining leaves everything as it was. A job belongs to the extension (the hub's memory may be wiped by a redeploy), so
a client that lost track asks the inventory again.

## The map

One picture, two shores. **Left: this computer's disk** (everything on the volume, free space as a strip along the
edge). **Right: what is already offloaded**, drawn in the same pixels-per-byte so a 40 GB box is the same size on
either side. Between them a narrow channel carries ribbons. A phone in portrait stacks the two, cloud below.

- **Scale.** Both sides share one scale: with the map's height fixed, the cloud side's width is its share of
  `volume.total_bytes + cloud total`. A cloud side narrower than its minimum width keeps the minimum width and gets
  shorter instead, so its area still tells the truth. A cloud side that would take more than 45 % of the width is
  compressed, and says so with a scale chip ("1 : 3"); exact numbers are always in the detail line.
- **Nested squarified treemap** (Bruls, Huizing, van Wijk), ported from the sibling product's dashboard: children
  sorted by size, rows placed to keep boxes near square, edges snapped to whole pixels so neighbors share one line. A
  box with room draws its children inside under a title strip, up to three levels (two on narrow screens). A name or a
  size is drawn only where it fits whole. Apps show their icon when the provider or the platform has one.
- **Zoom.** Click or tap a box with children to make it the whole map; the breadcrumb above names the trail and each
  step is a button; Escape (and the back swipe) goes up one level. Zooming is a 160 ms fade and grow, a cross-fade
  under Reduce Motion. A folder not yet measured starts its scan on zoom and fills in while it runs.
- **Details.** Hover, keyboard focus or a touch shows one detail line under the map: name, size, share of the focused
  box, last changed, and why an action is not offered. Paths appear here and in the menu, never as box labels.
- **Context menu** (right-click, long press, the menu key): Zoom In, Move to Trash…, Offload to *provider*…, Clean
  Cache… (a catalog cache), Show in Files (the existing file browser), Copy Path; on the cloud side Restore…. An
  action the rules refuse is disabled and says why.
- **Links.** A local box whose subtree holds offloaded items carries a small cloud badge with their count. Hover or
  focus the badge, or a cloud box, and its partner on the other side lights up with one ribbon across the channel (the
  deepest visible ancestor of `origin_path` on the local side). Activating the badge zooms the cloud side to those
  items. Ribbons are drawn only for the hovered, focused or selected pairs.
- **Selection and "what if".** Space, a modifier click or a long press then tap selects boxes. With a selection the
  map previews the result before anything happens: selected local boxes turn into hatched ghosts, the free strip grows
  by their sum, their dashed twins appear on the cloud side, and a tray under the map reads "Free after: 61 GB
  (+38 GB) · 91 % → 76 %" with **Offload N…**, **Move to Trash…** and **Clear**. The tray lists the selection as rows
  with sizes and a remove button each: it is also the map's accessible, non-visual equivalent.
- **Drag.** Drag a local box onto the cloud side to offload it, a cloud box onto the local side to restore it. A drop
  only opens the same confirmation the menu does; the preview shows while dragging. Every drag has the menu and the
  tray as keyboard and VoiceOver equivalents.
- **Free-up goal.** A slider in the tray ("Free up 40 GB") selects for the user: unused caches first, then the items
  with the highest size × age score among what is measured and allowed, until the goal is met. It only selects;
  the user still reviews the tray and confirms.
- **Tint.** A two-way control colors boxes by **Kind** (default, the legend's categories) or **Last used** (four steps
  from recent to over a year, coldest strongest). Age is never color alone: the detail line and the accessibility
  label say it, and a box of 44 pt or more over a year old carries a glyph.
- **Confirmations.** Trash: the existing dialog pattern, listing every item, its path and the total, with the note
  that space returns when the trash is emptied; afterwards a notice offers **Undo** and **Delete Permanently…** (its
  own danger confirmation). Offload: one dialog naming the provider, the items and the total, then the install
  confirmation when needed, then progress in the tray from `offload.job`. Clean Cache reuses the existing cleanup
  call and wording.
- **After a change** only the affected folder is asked again (`rescan=1` on the parent), the focus and zoom stay, and
  the inventory is reloaded after an offload or restore.
- **No provider.** The cloud side is a quiet empty state ("Nothing offloaded. An extension can add cloud offload.")
  with a link to **Settings › Extensions**; Offload is absent from menus. Core names no product.
- **Accessibility.** The map is a group; each drawn box is a button whose label is "name, size, share, last changed,
  selected"; arrow keys move to the nearest box in that direction, Enter zooms, Space selects, Escape goes up, Delete
  asks to trash, the menu key opens the menu. Focus returns to the first box after a zoom. Colors meet contrast on
  both themes; category is also in the label.

### Low-disk guidance

The thresholds stay where they are (disk ≥ 80 % caution, ≥ 90 % severe). What changes is where they lead:

- **Web.** The disk alert sheet keeps its content and gains **See What's Using Space**, which opens the Machines page's
  Storage view on that computer with `focus=reclaim`. A machine's disk figure in its details links to the same place.
- **Apple.** The **Disk almost full** chip on a machine card, the Disk tile in machine details and a one-line banner
  in details while the level is raised all open the native map in the same mode (the Apple apps have no disk sheet to
  reuse).
- **`focus=reclaim`** starts the overview scan at once and, as results arrive, marks up to five suggestions with a
  ring and lists them as chips above the map, largest first: unused catalog caches (Clean), trashable cache folders,
  and large items not changed for 180 days that a provider can take (Offload). Activating a chip zooms to the item
  and selects it, which shows the "what if" preview. The headline is one sentence: "24 GB free of 245 GB. Freeing
  the 3 marked items returns about 31 GB."

## Clients

**Web** (`web/src/views/machines/`, loaded lazily so the first screen's budget is untouched). `UsageMap` owns the
data (overview, per-folder cache, polling, providers, inventory, selection); `UsageTreemap` draws absolutely positioned
buttons from a pure `squarify` layout in a `usage-map` library module (unit-tested without a DOM), one for each side;
`UsageMapTray`, `UsageMapMenu` and the dialogs reuse `Dialog`, `Button` and `Segmented` from the web's atoms. Colors,
radii and type come from the existing tokens; the new semantic tokens for the seven categories and four age steps
(`--umap-*`, light and dark, the Apple values of `docs/design/DESIGN_SYSTEM.md` "Usage treemap") live in the map's
own lazily loaded `web/src/views/machines/usage-map.css`, not the token sheet, so the first screen's budget is
untouched. It is mounted at the top of the existing Storage view; the lists below are
unchanged. Strings: namespace `machines-usagemap`, in `en`, `zh-Hans` and `ja`.

**Apple** (`apple/Persona/Views/Machines/`). Native SwiftUI, no web view. `UsageMapModel` (`@Observable`,
dependencies from `Deps`, tested with `FakeHub`) holds the same state as the web's. `TreemapLayout` is a pure,
`nonisolated` squarify computed off the main actor and cached by node, size and depth (performance rules P2, P3, P8).
`UsageTreemapView` draws all boxes in one `Canvas` and lays a custom `Layout` of clear hit targets over the boxes of
at least 24 pt (the 200 largest): those carry the tap, `contextMenu`, drag and drop, hover, focus and the
accessibility element, so every interaction is the platform's own. Zoom animates one `Animatable` progress, never a
running timeline. Escape goes up on the Mac and with a hardware keyboard. Everything reads `Theme` tokens
(`Theme.Font`, `Theme.Space`, `Theme.Radius`, `Theme.Motion`, role colors); the category and age colors are new
`Theme` tokens. Strings go in `apple/Shared/Localization/Localizable.xcstrings` in the three languages.

Android and HarmonyOS keep their current machine pages; the routes are ready for them.

## Tests

- **Node** (`go test -p 2 ./internal/node/diskmap/ ./internal/node/ -run 'Storage|Usage|Trash'`): the walker on
  temporary trees (links, hard links, deadline, entry budget, `other` merging, prefetch depth; another filesystem is
  not covered, since a test cannot mount one in a temporary folder); the overview's groups adding up to the volume;
  app aggregation from fixture bundles; the cache (join, expiry, `rescan`, in-place update after a trash); every
  protection code on macOS, Linux and Windows path shapes (table-driven, with a fake home); trash, untrash and purge
  in a temporary home, including a made-up `trash_id`; dispatch of the five methods.
- **Manager** (`./internal/devices/...`, `./internal/extmgr/...`, `./internal/extension/...`): pass-through, roles,
  capability, offline, limits, confirmation; the `disk_offload` item rules; the offload routes with a fake program
  (`extmgr/extension_offload_test.go`: inventory, dry run, refused paths, `tool_install_required` then
  `install: true`, job, cancel, roles); the offload calls held to the Go types (`extension/conformance`); the hub's
  refusal of answer-only devices and extension tokens (`hub/usagemap_routes_test.go`, `hub/testdata/route_scopes.txt`); `scripts/test/test_doc_wire.py` and the schema examples.
- **Web** (`vitest --maxWorkers=2`): `squarify` (areas proportional, nothing overlaps, stable order), the shared scale
  and its clamps, polling until ready, zoom and breadcrumb, keyboard map, menu rules per flag, selection and the
  "what if" numbers, the goal picker, trash → undo → purge, offload with and without the install step, `focus=reclaim`.
- **Apple** (`-only-testing`, `-jobs 2`, `-parallel-testing-enabled NO`): `TreemapLayoutTests`, `UsageMapModelTests`
  with `FakeHub` (the same flows), a UI test on the mock hub that opens the map from a disk chip, zooms, goes back and
  reaches the trash confirmation; the token and type-scale ratchets unchanged.
- **Extension**: in its own repository, the conformance check plus its provider's tests.
