package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/draw"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lookmedia"
)

// Uploaded looks (docs/apple/persona.md "Uploading an image", #2404, #15449): the user makes Persona's avatar from one
// or more still or animated pictures of their own in the apps, optionally with per-state pictures and a theme color,
// and edits or deletes it later. The hub turns the pictures into a format-1 persona package in personas/u-<hex>/, so
// every app that shows packages shows it. Only packages made this way can be edited or deleted here: one placed by
// hand, a built-in one and an extension's are left alone.
//
// The avatar sizes (64–512) are always still (frame 0). Automatic states follow the main picture (animating with it
// when animated, with each state's mark drawn on every frame; automatic offline stays still, frame 0, gray, asleep).
// States with their own picture get the state's mark on every frame unless mark is false (idle has no mark). Unmarked
// generated frames are kept in .edit/ (ignored by persona.go) so PATCH can toggle marks or revert states to automatic
// without storing original uploaded bytes.

const (
	lookUploadKind           = "upload" // persona.json's source.kind
	lookUploadIDPrefix       = "u-"
	lookUploadMaxBody        = 34 << 20                   // JSON body: 24 MB of images in base64 (32 MB) plus JSON overhead
	lookUploadMaxImage       = lookmedia.MaxImageBytes    // 8 MB per picture
	lookUploadMaxTotalImages = lookmedia.MaxRequestImages // 24 MB across all pictures in one request
	lookUploadMaxPixels      = lookmedia.MaxCanvasPixels  // 40M pixels per frame canvas
	lookUploadMinSide        = lookmedia.MinSide          // 64 px minimum side
	lookUploadMaxLooks       = 50
	lookUploadMaxName        = 40
	lookUploadMaxSub         = 80
	lookUploadDotSide        = lookmedia.DotSide // 144 px (48 pt @3x)
	lookUploadSuper          = 3                 // dot marks drawn at 3x and scaled down for smooth edges
	lookUploadEditDir        = ".edit"
)

// lookUploadSizes are the avatar's sides; the apps pick the smallest one big enough for what they draw.
var lookUploadSizes = []int{64, 128, 256, 512}

// lookUploadsMu keeps concurrent uploads, edits and deletes from counting or writing the directory at once.
var lookUploadsMu sync.Mutex

// lookSwapMu coordinates directory swaps and removals against concurrent package walks (os.DirFS opens directory
// handles across multiple syscalls, so removing a swapped-out old directory while a walk holds an open dirfd to it
// would otherwise produce an empty directory read). Only the short rename/swap+remove step takes the write lock.
var lookSwapMu sync.RWMutex

var (
	lookUploadAccent   = color.NRGBA{0xF0, 0x8C, 0x1E, 0xFF} // the working arc and the lit thinking dot: Mochi's orange
	lookUploadNeedsYou = color.NRGBA{0xE5, 0x48, 0x4D, 0xFF} // make_persona.py's NEEDS_YOU
	lookUploadDim      = color.NRGBA{150, 150, 158, 0xFF}    // the thinking dots not lit
	lookUploadZ        = color.NRGBA{72, 72, 84, 0xFF}       // the offline z's
	lookUploadWhite    = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// lookUploadCrop is the JSON crop rectangle on POST and PATCH /api/v1/looks/uploads.
type lookUploadCrop struct {
	X    int `json:"x"`
	Y    int `json:"y"`
	Side int `json:"side"`
}

func (c *lookUploadCrop) toMedia() *lookmedia.Crop {
	if c == nil {
		return nil
	}
	v := lookmedia.NewCrop(c.X, c.Y, c.Side)
	return &v
}

// lookUploadStateInput is one state entry in POST or PATCH /api/v1/looks/uploads.
type lookUploadStateInput struct {
	Image *string         `json:"image,omitempty"`
	Crop  *lookUploadCrop `json:"crop,omitempty"`
	Mark  *bool           `json:"mark,omitempty"`
	Auto  *bool           `json:"auto,omitempty"`
}

// lookUploadRequest is POST /api/v1/looks/uploads's body.
type lookUploadRequest struct {
	Name     string                          `json:"name"`
	Subtitle string                          `json:"subtitle"`
	Image    string                          `json:"image"`
	Crop     *lookUploadCrop                 `json:"crop,omitempty"`
	States   map[string]lookUploadStateInput `json:"states,omitempty"`
	Color    string                          `json:"color,omitempty"`
	Choose   bool                            `json:"choose"`
}

// lookPatchResponse is PATCH /api/v1/looks/uploads/{id}'s response body.
type lookPatchResponse struct {
	Look lookView `json:"look"`
}

// personasChangedEvent notifies connected clients that the personas / looks list changed.
type personasChangedEvent struct {
	Type string `json:"type"`
}

// lookPatchRequest is PATCH /api/v1/looks/uploads/{id}'s body.
type lookPatchRequest struct {
	Name     *string                         `json:"name,omitempty"`
	Subtitle *string                         `json:"subtitle,omitempty"`
	Image    *string                         `json:"image,omitempty"`
	Crop     *lookUploadCrop                 `json:"crop,omitempty"`
	States   map[string]lookUploadStateInput `json:"states,omitempty"`
	Color    *string                         `json:"color,omitempty"`
}

type lookUploadSourceColor struct {
	Seed string `json:"seed"`
	Auto bool   `json:"auto"`
}

type lookUploadSourceState struct {
	Own  bool `json:"own"`
	Mark bool `json:"mark"`
}

type lookUploadSource struct {
	Kind       string                           `json:"kind"`
	UploadedAt string                           `json:"uploaded_at"`
	Color      *lookUploadSourceColor           `json:"color,omitempty"`
	States     map[string]lookUploadSourceState `json:"states,omitempty"`
}

type lookDotStateManifest struct {
	File   string `json:"file"`
	FPS    int    `json:"fps,omitempty"`
	Frames int    `json:"frames,omitempty"`
}

// uploadedLookManifest is the full persona.json the hub reads and writes for uploaded looks.
type uploadedLookManifest struct {
	Format   int    `json:"format"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle,omitempty"`
	Avatar   struct {
		Sizes map[string]string `json:"sizes"`
		Safe  float64           `json:"safe"`
	} `json:"avatar"`
	Dot struct {
		Size   int                             `json:"size"`
		States map[string]lookDotStateManifest `json:"states"`
	} `json:"dot"`
	Colors          *lookmedia.LookColors `json:"colors,omitempty"`
	AppIconFallback string                `json:"app_icon_fallback,omitempty"`
	Source          lookUploadSource      `json:"source"`
}

// lookUploadError is a refusal to tell the user: its status and stable code, with the message in their language.
type lookUploadError struct {
	status int
	code   string
	err    error
}

func (e *lookUploadError) Error() string { return e.err.Error() }

func lookUploadRefusal(status int, code string, args ...i18n.Args) *lookUploadError {
	return &lookUploadError{status: status, code: code, err: i18n.Err("hub-look_uploads."+code, args...)}
}

func (h *Hub) lookUploadRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/v1/looks/uploads", func(w http.ResponseWriter, r *http.Request) {
		var body lookUploadRequest
		if err := decodeJSONBody(w, r, &body, lookUploadMaxBody); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeLookUploadErr(w, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxTotalImages >> 20}))
				return
			}
			writeBodyErr(w, err, err)
			return
		}
		view, cur, err := h.uploadLook(r.Context(), body)
		if err != nil {
			writeLookUploadErr(w, err)
			return
		}
		out := map[string]any{"look": view}
		if cur != nil {
			out["current"] = cur
		}
		writeJSON(w, out)
	})
	api.HandleFunc("PATCH /api/v1/looks/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body lookPatchRequest
		if err := decodeJSONBody(w, r, &body, lookUploadMaxBody); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeLookUploadErr(w, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxTotalImages >> 20}))
				return
			}
			writeBodyErr(w, err, err)
			return
		}
		view, err := h.patchUploadedLook(r.Context(), r.PathValue("id"), body)
		if err != nil {
			writeLookUploadErr(w, err)
			return
		}
		writeJSON(w, lookPatchResponse{Look: view})
	})
	api.HandleFunc("DELETE /api/v1/looks/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		cur, err := h.deleteUploadedLook(r.Context(), r.PathValue("id"))
		if err != nil {
			writeLookUploadErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"current": cur})
	})
}

func writeLookUploadErr(w http.ResponseWriter, err error) {
	var refusal *lookUploadError
	if errors.As(err, &refusal) {
		writeErrCode(w, refusal.status, refusal.code, refusal.err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// uploadLook makes a package from the uploaded picture(s), and chooses it when asked. It returns the new look as
// GET /api/v1/looks lists it, and the choice when it chose it.
func (h *Hub) uploadLook(ctx context.Context, req lookUploadRequest) (lookView, *lookCurrent, error) {
	name, subtitle, err := lookUploadNames(req.Name, req.Subtitle)
	if err != nil {
		return lookView{}, nil, err
	}
	colorMode, chosenColor, err := parseLookColorChoice(req.Color, true)
	if err != nil {
		return lookView{}, nil, err
	}

	// Validate state keys and field shapes + check total image byte budget before decoding any image.
	var totalBytes int64
	mainRaw, err := decodeLookBase64(req.Image, true)
	if err != nil {
		return lookView{}, nil, err
	}
	totalBytes += int64(len(mainRaw))

	type parsedStateReq struct {
		raw  []byte
		crop *lookUploadCrop
		mark bool
	}
	stateReqs := make(map[string]parsedStateReq, len(req.States))
	for st, in := range req.States {
		if !isPersonaState(st) {
			return lookView{}, nil, lookUploadRefusal(http.StatusBadRequest, "bad_state")
		}
		if in.Auto != nil && *in.Auto {
			return lookView{}, nil, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
		}
		if in.Image == nil {
			if in.Mark != nil {
				return lookView{}, nil, lookUploadRefusal(http.StatusBadRequest, "mark_without_image")
			}
			return lookView{}, nil, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
		}
		raw, err := decodeLookBase64(*in.Image, false)
		if err != nil {
			return lookView{}, nil, err
		}
		totalBytes += int64(len(raw))
		if totalBytes > lookUploadMaxTotalImages {
			return lookView{}, nil, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxTotalImages >> 20})
		}
		mark := true
		if st == "idle" {
			mark = false
		} else if in.Mark != nil {
			mark = *in.Mark
		}
		stateReqs[st] = parsedStateReq{raw: raw, crop: in.Crop, mark: mark}
	}

	mainFrames, err := decodeAndNormalizeLook(mainRaw, req.Crop.toMedia())
	if err != nil {
		return lookView{}, nil, err
	}

	ownStates := make(map[string]ownStateInput, len(stateReqs))
	for st, sr := range stateReqs {
		frames, err := decodeAndNormalizeLook(sr.raw, sr.crop.toMedia())
		if err != nil {
			return lookView{}, nil, err
		}
		ownStates[st] = ownStateInput{frames: frames, mark: sr.mark}
	}

	lookUploadsMu.Lock()
	defer lookUploadsMu.Unlock()
	if n := h.uploadedLookCount(); n >= lookUploadMaxLooks {
		return lookView{}, nil, lookUploadRefusal(http.StatusConflict, "too_many", i18n.Args{"max": lookUploadMaxLooks})
	}
	id, err := h.newUploadID()
	if err != nil {
		return lookView{}, nil, err
	}
	files, err := buildUploadedLookFiles(id, name, subtitle, mainFrames, ownStates, colorMode, chosenColor, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return lookView{}, nil, err
	}
	if err := h.writeUploadedLook(id, files); err != nil {
		return lookView{}, nil, err
	}
	h.logf("look %s uploaded (%s)", id, name)
	h.broadcast(map[string]any{"type": "personas"})

	looks := h.looks()
	var view lookView
	for _, v := range looks {
		if v.ID == id {
			view = v
		}
	}
	if view.ID == "" { // written but not listed: it would never show, so it is not kept either
		_ = os.RemoveAll(filepath.Join(h.personasDir(), id))
		return lookView{}, nil, fmt.Errorf("look %s was written but does not check out", id)
	}
	if !req.Choose || h.store == nil {
		return view, nil, nil
	}
	cur, err := h.chooseLook(ctx, id, looks)
	if err != nil {
		return view, nil, err
	}
	return view, &cur, nil
}

// patchUploadedLook edits an existing uploaded look in place (keeping its id, changing rev, swapping atomically).
func (h *Hub) patchUploadedLook(ctx context.Context, id string, req lookPatchRequest) (lookView, error) {
	if !personaIDPattern.MatchString(id) {
		return lookView{}, lookUploadRefusal(http.StatusNotFound, "not_found")
	}
	if req.Crop != nil && req.Image == nil {
		return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
	}
	if req.Name == nil && req.Subtitle == nil && req.Image == nil && req.Color == nil && len(req.States) == 0 {
		return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
	}

	// Validate inputs that do not depend on disk state and enforce byte budgets before decoding.
	var (
		newName     *string
		newSubtitle *string
		newColor    string // "" unchanged, "auto", or "#RRGGBB"
		chosenColor color.NRGBA
		totalBytes  int64
		newMainRaw  []byte
	)
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" {
			return lookView{}, lookUploadRefusal(http.StatusBadRequest, "no_name")
		}
		if !lookUploadText(n) || utf8.RuneCountInString(n) > lookUploadMaxName {
			return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_name", i18n.Args{"max": lookUploadMaxName})
		}
		newName = &n
	}
	if req.Subtitle != nil {
		s := strings.TrimSpace(*req.Subtitle)
		if !lookUploadText(s) || utf8.RuneCountInString(s) > lookUploadMaxSub {
			return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_subtitle", i18n.Args{"max": lookUploadMaxSub})
		}
		newSubtitle = &s
	}
	if req.Color != nil {
		mode, col, err := parseLookColorChoice(*req.Color, false)
		if err != nil {
			return lookView{}, err
		}
		newColor, chosenColor = mode, col
	}
	if req.Image != nil {
		raw, err := decodeLookBase64(*req.Image, true)
		if err != nil {
			return lookView{}, err
		}
		newMainRaw = raw
		totalBytes += int64(len(raw))
	}

	type patchStateReq struct {
		auto     bool
		raw      []byte
		crop     *lookUploadCrop
		hasImage bool
		hasMark  bool
		mark     bool
	}
	stateUpdates := make(map[string]patchStateReq, len(req.States))
	for st, in := range req.States {
		if !isPersonaState(st) {
			return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_state")
		}
		if in.Auto != nil && *in.Auto {
			if in.Image != nil || in.Crop != nil || in.Mark != nil {
				return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
			}
			stateUpdates[st] = patchStateReq{auto: true}
			continue
		}
		if in.Image != nil {
			raw, err := decodeLookBase64(*in.Image, false)
			if err != nil {
				return lookView{}, err
			}
			totalBytes += int64(len(raw))
			if totalBytes > lookUploadMaxTotalImages {
				return lookView{}, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxTotalImages >> 20})
			}
			mark := true
			if st == "idle" {
				mark = false
			} else if in.Mark != nil {
				mark = *in.Mark
			}
			stateUpdates[st] = patchStateReq{raw: raw, crop: in.Crop, hasImage: true, hasMark: true, mark: mark}
			continue
		}
		if in.Crop != nil {
			return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
		}
		if in.Mark != nil {
			mark := *in.Mark
			if st == "idle" {
				mark = false
			}
			stateUpdates[st] = patchStateReq{hasMark: true, mark: mark}
			continue
		}
		return lookView{}, lookUploadRefusal(http.StatusBadRequest, "bad_fields")
	}

	// Decode uploaded images before taking the directory lock so CPU work doesn't block unrelated operations,
	// wait: check existence/ownership after locking so invalid ids still fail fast on bad inputs OR check lock first?
	// Check look existence first for 404/409 semantics even if a client sends a mark-only update!
	lookUploadsMu.Lock()
	defer lookUploadsMu.Unlock()

	dir := filepath.Join(h.personasDir(), id)
	rawManifest, err := os.ReadFile(filepath.Join(dir, "persona.json"))
	if errors.Is(err, os.ErrNotExist) {
		// Check if it is a built-in or extension look (exists in looks list, but not an uploaded dir).
		if h.hasNonUploadedLook(id) {
			return lookView{}, lookUploadRefusal(http.StatusConflict, "not_uploaded")
		}
		return lookView{}, lookUploadRefusal(http.StatusNotFound, "not_found")
	} else if err != nil {
		return lookView{}, err
	}
	var existing uploadedLookManifest
	if err := json.Unmarshal(rawManifest, &existing); err != nil || existing.Source.Kind != lookUploadKind {
		return lookView{}, lookUploadRefusal(http.StatusConflict, "not_uploaded")
	}

	// Validate mark-only toggles against existing own states before heavy image decoding.
	for st, upd := range stateUpdates {
		if !upd.auto && !upd.hasImage && upd.hasMark {
			if cur, ok := existing.Source.States[st]; !ok || !cur.Own {
				return lookView{}, lookUploadRefusal(http.StatusBadRequest, "mark_without_image")
			}
		}
	}

	// Decode new main / state images now.
	var newMainFrames []lookmedia.Frame
	if len(newMainRaw) > 0 {
		newMainFrames, err = decodeAndNormalizeLook(newMainRaw, req.Crop.toMedia())
		if err != nil {
			return lookView{}, err
		}
	}
	decodedStateFrames := make(map[string][]lookmedia.Frame)
	for st, upd := range stateUpdates {
		if upd.hasImage {
			frames, err := decodeAndNormalizeLook(upd.raw, upd.crop.toMedia())
			if err != nil {
				return lookView{}, err
			}
			decodedStateFrames[st] = frames
		}
	}

	// Prepare the complete next package file set.
	files := map[string][]byte{}
	avatarSizes := map[string]string{}
	if len(newMainFrames) > 0 {
		if err := addAvatarFiles(files, avatarSizes, newMainFrames[0].Image()); err != nil {
			return lookView{}, err
		}
		if len(newMainFrames) > 1 {
			b, err := encodeEditFrames(newMainFrames)
			if err != nil {
				return lookView{}, err
			}
			files[lookUploadEditDir+"/main.png"] = b
		}
	} else {
		for sz, rel := range existing.Avatar.Sizes {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				return lookView{}, err
			}
			files[rel] = b
			avatarSizes[sz] = rel
		}
		if b, err := os.ReadFile(filepath.Join(dir, lookUploadEditDir, "main.png")); err == nil {
			files[lookUploadEditDir+"/main.png"] = b
		}
	}

	var cachedMainFrames []lookmedia.Frame
	if len(newMainFrames) > 0 {
		cachedMainFrames = newMainFrames
	}
	loadMainFrames := func() ([]lookmedia.Frame, error) {
		if len(cachedMainFrames) > 0 {
			return cachedMainFrames, nil
		}
		if b, ok := files[lookUploadEditDir+"/main.png"]; ok {
			anim, err := lookmedia.Decode(b)
			if err != nil {
				return nil, err
			}
			cachedMainFrames = anim.Frames()
			return cachedMainFrames, nil
		}
		// Legacy or still upload: read the largest avatar PNG.
		bestSide := 0
		bestRel := ""
		for sz, rel := range avatarSizes {
			if n, _ := strconv.Atoi(sz); n > bestSide {
				bestSide, bestRel = n, rel
			}
		}
		if bestRel == "" {
			return nil, errors.New("existing uploaded look has no avatar file")
		}
		anim, err := lookmedia.Decode(files[bestRel])
		if err != nil {
			return nil, err
		}
		cachedMainFrames = anim.Frames()
		return cachedMainFrames, nil
	}

	nextSourceStates := make(map[string]lookUploadSourceState)
	for st, v := range existing.Source.States {
		if v.Own {
			nextSourceStates[st] = v
		}
	}
	nextDotStates := make(map[string]lookDotStateManifest, len(personaStates))

	for _, st := range personaStates {
		dotFile := "dot/" + st + ".png"
		editFile := lookUploadEditDir + "/" + st + ".png"
		upd, hasUpd := stateUpdates[st]

		switch {
		case hasUpd && upd.auto:
			delete(nextSourceStates, st)
			mf, err := loadMainFrames()
			if err != nil {
				return lookView{}, err
			}
			entry, data, err := renderAutoDotState(st, mf)
			if err != nil {
				return lookView{}, err
			}
			files[dotFile] = data
			nextDotStates[st] = entry

		case hasUpd && upd.hasImage:
			stFrames := decodedStateFrames[st]
			editBytes, err := encodeEditFrames(stFrames)
			if err != nil {
				return lookView{}, err
			}
			files[editFile] = editBytes
			entry, data, err := renderOwnDotState(st, stFrames, upd.mark)
			if err != nil {
				return lookView{}, err
			}
			files[dotFile] = data
			nextDotStates[st] = entry
			nextSourceStates[st] = lookUploadSourceState{Own: true, Mark: upd.mark}

		case hasUpd && upd.hasMark:
			editBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(editFile)))
			if err != nil {
				return lookView{}, fmt.Errorf("load %s: %w", editFile, err)
			}
			anim, err := lookmedia.Decode(editBytes)
			if err != nil {
				return lookView{}, err
			}
			files[editFile] = editBytes
			entry, data, err := renderOwnDotState(st, anim.Frames(), upd.mark)
			if err != nil {
				return lookView{}, err
			}
			files[dotFile] = data
			nextDotStates[st] = entry
			nextSourceStates[st] = lookUploadSourceState{Own: true, Mark: upd.mark}

		default:
			if cur, own := nextSourceStates[st]; own && cur.Own {
				// Untouched own state: copy existing dot and .edit files.
				dotBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(dotFile)))
				if err != nil {
					return lookView{}, err
				}
				files[dotFile] = dotBytes
				if eb, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(editFile))); err == nil {
					files[editFile] = eb
				}
				if oldSpec, ok := existing.Dot.States[st]; ok && oldSpec.File != "" {
					nextDotStates[st] = oldSpec
				} else {
					nextDotStates[st] = lookDotStateManifest{File: dotFile}
				}
			} else if len(newMainFrames) > 0 {
				// Automatic state regenerated from new main picture.
				entry, data, err := renderAutoDotState(st, newMainFrames)
				if err != nil {
					return lookView{}, err
				}
				files[dotFile] = data
				nextDotStates[st] = entry
			} else {
				// Untouched automatic state: preserve existing dot bytes and manifest spec.
				dotBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(dotFile)))
				if err != nil {
					return lookView{}, err
				}
				files[dotFile] = dotBytes
				if oldSpec, ok := existing.Dot.States[st]; ok && oldSpec.File != "" {
					nextDotStates[st] = oldSpec
				} else {
					nextDotStates[st] = lookDotStateManifest{File: dotFile}
				}
			}
		}
	}

	nextName := existing.Name
	if newName != nil {
		nextName = *newName
	}
	nextSubtitle := existing.Subtitle
	if newSubtitle != nil {
		nextSubtitle = *newSubtitle
	}

	nextColors := existing.Colors
	nextSourceColor := existing.Source.Color
	switch {
	case newColor == "auto":
		mf, err := loadMainFrames()
		if err != nil {
			return lookView{}, err
		}
		p, s := lookmedia.ExtractAutoSeed(mf[0].Image())
		pal := lookmedia.Palette(p, s)
		nextColors = &pal
		nextSourceColor = &lookUploadSourceColor{Seed: lookmedia.FormatHexColor(p), Auto: true}
	case newColor != "":
		pal := lookmedia.Palette(chosenColor, lookmedia.DefaultSecondary(chosenColor))
		nextColors = &pal
		nextSourceColor = &lookUploadSourceColor{Seed: newColor, Auto: false}
	case len(newMainFrames) > 0 && (existing.Source.Color == nil || existing.Source.Color.Auto):
		p, s := lookmedia.ExtractAutoSeed(newMainFrames[0].Image())
		pal := lookmedia.Palette(p, s)
		nextColors = &pal
		nextSourceColor = &lookUploadSourceColor{Seed: lookmedia.FormatHexColor(p), Auto: true}
	}

	uploadedAt := existing.Source.UploadedAt
	if uploadedAt == "" {
		uploadedAt = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	manifestBytes, err := marshalUploadedLookManifest(id, nextName, nextSubtitle, avatarSizes, nextDotStates, nextColors, lookUploadSource{
		Kind:       lookUploadKind,
		UploadedAt: uploadedAt,
		Color:      nextSourceColor,
		States:     nextSourceStates,
	})
	if err != nil {
		return lookView{}, err
	}
	files["persona.json"] = manifestBytes

	if err := h.replaceUploadedLook(id, files); err != nil {
		return lookView{}, err
	}
	h.logf("look %s updated (%s)", id, nextName)
	h.broadcast(personasChangedEvent{Type: "personas"})

	looks := h.looks()
	for _, v := range looks {
		if v.ID == id {
			return v, nil
		}
	}
	return lookView{}, fmt.Errorf("look %s was updated but does not check out", id)
}

func (h *Hub) hasNonUploadedLook(id string) bool {
	for _, p := range h.personaPackages() {
		if p.id == id {
			return !isUploadedManifest(p.raw)
		}
	}
	return false
}

// deleteUploadedLook deletes a package the hub made from an upload. When it was the chosen look, the default is chosen.
func (h *Hub) deleteUploadedLook(ctx context.Context, id string) (lookCurrent, error) {
	if !personaIDPattern.MatchString(id) {
		return lookCurrent{}, lookUploadRefusal(http.StatusNotFound, "not_found")
	}
	lookUploadsMu.Lock()
	defer lookUploadsMu.Unlock()
	dir := filepath.Join(h.personasDir(), id)
	raw, err := os.ReadFile(filepath.Join(dir, "persona.json"))
	if errors.Is(err, os.ErrNotExist) {
		return lookCurrent{}, lookUploadRefusal(http.StatusNotFound, "not_found")
	} else if err != nil {
		return lookCurrent{}, err
	}
	if !isUploadedManifest(raw) {
		return lookCurrent{}, lookUploadRefusal(http.StatusConflict, "not_uploaded")
	}
	// Out of the list in one step (the hub skips dot directories), then off the disk.
	gone := filepath.Join(h.personasDir(), "."+id+".gone-"+randomHex(4))
	lookSwapMu.Lock()
	err = os.Rename(dir, gone)
	var removeErr error
	if err == nil {
		removeErr = os.RemoveAll(gone)
	}
	lookSwapMu.Unlock()
	if err != nil {
		return lookCurrent{}, err
	}
	if removeErr != nil {
		h.logf("look %s: remove %s: %v", id, gone, removeErr)
	}
	h.logf("look %s deleted", id)
	h.broadcast(map[string]any{"type": "personas"})

	looks := h.looks()
	chosen, err := h.lookChoice(ctx)
	if err != nil {
		return lookCurrent{}, err
	}
	if chosen != nil && chosen.ID == id {
		return h.chooseLook(ctx, extension.DefaultLook, looks)
	}
	return lookCurrentOf(chosen, looks), nil
}

// lookUploadNames are the name and subtitle as the package keeps them: trimmed, the name required, both bounded and
// free of control and bidi formatting characters.
func lookUploadNames(name, subtitle string) (string, string, error) {
	name, subtitle = strings.TrimSpace(name), strings.TrimSpace(subtitle)
	if name == "" {
		return "", "", lookUploadRefusal(http.StatusBadRequest, "no_name")
	}
	if !lookUploadText(name) || utf8.RuneCountInString(name) > lookUploadMaxName {
		return "", "", lookUploadRefusal(http.StatusBadRequest, "bad_name", i18n.Args{"max": lookUploadMaxName})
	}
	if !lookUploadText(subtitle) || utf8.RuneCountInString(subtitle) > lookUploadMaxSub {
		return "", "", lookUploadRefusal(http.StatusBadRequest, "bad_subtitle", i18n.Args{"max": lookUploadMaxSub})
	}
	return name, subtitle, nil
}

func lookUploadText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return false
		}
	}
	return true
}

func parseLookColorChoice(raw string, allowEmptyAuto bool) (mode string, parsed color.NRGBA, err error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if allowEmptyAuto {
			return "auto", color.NRGBA{}, nil
		}
		return "", color.NRGBA{}, lookUploadRefusal(http.StatusBadRequest, "bad_color")
	}
	if v == "auto" {
		return "auto", color.NRGBA{}, nil
	}
	c, hex, err := lookmedia.ParseHexColor(v)
	if err != nil {
		return "", color.NRGBA{}, lookUploadRefusal(http.StatusBadRequest, "bad_color")
	}
	return hex, c, nil
}

// decodeLookBase64 decodes one base64 (or data: URL) image payload, bounded by lookUploadMaxImage before decoding.
func decodeLookBase64(b64 string, emptyIsNoImage bool) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	if i := strings.Index(b64, ";base64,"); strings.HasPrefix(b64, "data:") && i >= 0 { // a data: URL is fine too
		b64 = b64[i+len(";base64,"):]
	}
	if b64 == "" {
		if emptyIsNoImage {
			return nil, lookUploadRefusal(http.StatusBadRequest, "no_image")
		}
		return nil, lookUploadRefusal(http.StatusBadRequest, "not_image")
	}
	if base64.StdEncoding.DecodedLen(len(b64)) > lookUploadMaxImage+3 {
		return nil, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxImage >> 20})
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
	}
	if err != nil {
		return nil, lookUploadRefusal(http.StatusBadRequest, "not_image")
	}
	if len(raw) > lookUploadMaxImage {
		return nil, lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_large", i18n.Args{"mb": lookUploadMaxImage >> 20})
	}
	return raw, nil
}

func decodeAndNormalizeLook(raw []byte, crop *lookmedia.Crop) ([]lookmedia.Frame, error) {
	anim, err := lookmedia.Decode(raw)
	if err != nil {
		return nil, mapLookMediaErr(err)
	}
	frames, err := lookmedia.Normalize(anim, crop)
	if err != nil {
		return nil, mapLookMediaErr(err)
	}
	return frames, nil
}

func mapLookMediaErr(err error) error {
	switch {
	case errors.Is(err, lookmedia.ErrTooSmall):
		return lookUploadRefusal(http.StatusBadRequest, "too_small", i18n.Args{"px": lookUploadMinSide})
	case errors.Is(err, lookmedia.ErrTooManyPixels):
		return lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_many_pixels")
	case errors.Is(err, lookmedia.ErrTooManyFrames):
		return lookUploadRefusal(http.StatusRequestEntityTooLarge, "too_many_frames")
	case errors.Is(err, lookmedia.ErrAnimationTooLong):
		return lookUploadRefusal(http.StatusBadRequest, "animation_too_long", i18n.Args{"seconds": lookmedia.MaxLoopSeconds})
	case errors.Is(err, lookmedia.ErrBadCrop):
		return lookUploadRefusal(http.StatusBadRequest, "bad_crop", i18n.Args{"px": lookUploadMinSide})
	default:
		return lookUploadRefusal(http.StatusBadRequest, "not_image")
	}
}

// uploadedLookCount counts the packages in personas/ the hub made from uploads.
func (h *Hub) uploadedLookCount() int {
	entries, err := os.ReadDir(h.personasDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), lookUploadIDPrefix) {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(h.personasDir(), e.Name(), "persona.json")); err == nil && isUploadedManifest(raw) {
			n++
		}
	}
	return n
}

// isUploadedManifest says whether a persona.json is one the hub wrote for an upload.
func isUploadedManifest(raw []byte) bool {
	var m struct {
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	return json.Unmarshal(raw, &m) == nil && m.Source.Kind == lookUploadKind
}

func (h *Hub) newUploadID() (string, error) {
	for range 8 {
		id := lookUploadIDPrefix + randomHex(4)
		if _, err := os.Lstat(filepath.Join(h.personasDir(), id)); errors.Is(err, os.ErrNotExist) && !extension.ReservedLookID(id) {
			return id, nil
		}
	}
	return "", errors.New("no free look id")
}

// stageUploadedLook writes files into a dot directory beside the packages and validates it with openPersona.
func (h *Hub) stageUploadedLook(id string, files map[string][]byte) (string, error) {
	root := h.personasDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, "."+id+".new-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()
	for name, b := range files {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return "", err
		}
	}
	if _, err := openPersona(os.DirFS(tmp), id); err != nil {
		return "", fmt.Errorf("the package made from the upload does not check out: %w", err)
	}
	ok = true
	return tmp, nil
}

// writeUploadedLook writes the package beside the others under a dot name (the hub skips those) and moves it into place
// in one step, so no list ever has half of it.
func (h *Hub) writeUploadedLook(id string, files map[string][]byte) error {
	tmp, err := h.stageUploadedLook(id, files)
	if err != nil {
		return err
	}
	lookSwapMu.Lock()
	err = os.Rename(tmp, filepath.Join(h.personasDir(), id))
	lookSwapMu.Unlock()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// replaceUploadedLook writes the edited package beside the old one under a dot name, validates it, atomically swaps
// the directories so concurrent readers always see either the old or new package, and deletes the replaced directory.
func (h *Hub) replaceUploadedLook(id string, files map[string][]byte) error {
	tmp, err := h.stageUploadedLook(id, files)
	if err != nil {
		return err
	}
	lookSwapMu.Lock()
	err = swapLookDirs(tmp, filepath.Join(h.personasDir(), id))
	_ = os.RemoveAll(tmp)
	lookSwapMu.Unlock()
	return err
}

type ownStateInput struct {
	frames []lookmedia.Frame
	mark   bool
}

func buildUploadedLookFiles(id, name, subtitle string, mainFrames []lookmedia.Frame, ownStates map[string]ownStateInput, colorMode string, chosenColor color.NRGBA, at time.Time) (map[string][]byte, error) {
	files := map[string][]byte{}
	sizes := map[string]string{}
	if err := addAvatarFiles(files, sizes, mainFrames[0].Image()); err != nil {
		return nil, err
	}
	if len(mainFrames) > 1 {
		b, err := encodeEditFrames(mainFrames)
		if err != nil {
			return nil, err
		}
		files[lookUploadEditDir+"/main.png"] = b
	}

	dotStates := make(map[string]lookDotStateManifest, len(personaStates))
	sourceStates := make(map[string]lookUploadSourceState)
	for _, st := range personaStates {
		dotFile := "dot/" + st + ".png"
		if own, ok := ownStates[st]; ok {
			editBytes, err := encodeEditFrames(own.frames)
			if err != nil {
				return nil, err
			}
			files[lookUploadEditDir+"/"+st+".png"] = editBytes
			entry, data, err := renderOwnDotState(st, own.frames, own.mark)
			if err != nil {
				return nil, err
			}
			files[dotFile] = data
			dotStates[st] = entry
			sourceStates[st] = lookUploadSourceState{Own: true, Mark: own.mark}
		} else {
			entry, data, err := renderAutoDotState(st, mainFrames)
			if err != nil {
				return nil, err
			}
			files[dotFile] = data
			dotStates[st] = entry
		}
	}

	var (
		pal      lookmedia.LookColors
		srcColor lookUploadSourceColor
	)
	if colorMode == "auto" {
		p, s := lookmedia.ExtractAutoSeed(mainFrames[0].Image())
		pal = lookmedia.Palette(p, s)
		srcColor = lookUploadSourceColor{Seed: lookmedia.FormatHexColor(p), Auto: true}
	} else {
		pal = lookmedia.Palette(chosenColor, lookmedia.DefaultSecondary(chosenColor))
		srcColor = lookUploadSourceColor{Seed: colorMode, Auto: false}
	}

	manifestBytes, err := marshalUploadedLookManifest(id, name, subtitle, sizes, dotStates, &pal, lookUploadSource{
		Kind:       lookUploadKind,
		UploadedAt: at.Format(time.RFC3339),
		Color:      &srcColor,
		States:     sourceStates,
	})
	if err != nil {
		return nil, err
	}
	files["persona.json"] = manifestBytes
	return files, nil
}

func addAvatarFiles(files map[string][]byte, sizes map[string]string, frame0 *image.RGBA) error {
	for _, side := range lookUploadSizes {
		f := fmt.Sprintf("avatar/%d.png", side)
		b, err := encodePNG(scaled(frame0, side))
		if err != nil {
			return err
		}
		files[f] = b
		sizes[fmt.Sprint(side)] = f
	}
	return nil
}

// encodeEditFrames scales unmarked square frames to dot size and encodes them as PNG/APNG for .edit/ (never original bytes).
func encodeEditFrames(frames []lookmedia.Frame) ([]byte, error) {
	small := make([]lookmedia.Frame, len(frames))
	for i, f := range frames {
		img := f.Image()
		if img.Bounds().Dx() != lookUploadDotSide || img.Bounds().Dy() != lookUploadDotSide {
			img = scaled(img, lookUploadDotSide)
		}
		small[i] = lookmedia.NewFrame(img, f.DelayMS())
	}
	return lookmedia.EncodeAPNG(small)
}

func renderAutoDotState(state string, mainFrames []lookmedia.Frame) (lookDotStateManifest, []byte, error) {
	src := mainFrames
	// Automatic offline always stays still (frame 0, gray, asleep).
	if state == "offline" && len(src) > 1 {
		src = []lookmedia.Frame{lookmedia.NewFrame(mainFrames[0].Image(), 0)}
	}
	return renderDotFrames(state, src, state != "idle")
}

func renderOwnDotState(state string, frames []lookmedia.Frame, mark bool) (lookDotStateManifest, []byte, error) {
	return renderDotFrames(state, frames, mark && state != "idle")
}

func renderDotFrames(state string, frames []lookmedia.Frame, marked bool) (lookDotStateManifest, []byte, error) {
	rendered := make([]lookmedia.Frame, len(frames))
	for i, f := range frames {
		rendered[i] = lookmedia.NewFrame(
			lookUploadDot(f.Image(), state, marked),
			f.DelayMS(),
		)
	}
	data, count, fps, err := lookmedia.EncodeDotAPNG(rendered, lookmedia.MaxDotFileBytes)
	if err != nil {
		return lookDotStateManifest{}, nil, err
	}
	entry := lookDotStateManifest{File: "dot/" + state + ".png"}
	if count > 1 {
		entry.FPS = fps
		entry.Frames = count
	}
	return entry, data, nil
}

func marshalUploadedLookManifest(id, name, subtitle string, sizes map[string]string, states map[string]lookDotStateManifest, colors *lookmedia.LookColors, src lookUploadSource) ([]byte, error) {
	var m uploadedLookManifest
	m.Format = personaFormat
	m.ID = id
	m.Name = name
	m.Subtitle = subtitle
	m.Avatar.Sizes = sizes
	m.Avatar.Safe = 1
	m.Dot.Size = lookUploadDotSide
	m.Dot.States = states
	m.Colors = colors
	m.AppIconFallback = extension.DefaultLook
	if len(src.States) == 0 {
		src.States = nil
	}
	m.Source = src
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func scaled(src image.Image, side int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.CatmullRom.Scale(out, out.Bounds(), src, src.Bounds(), draw.Src, nil)
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// lookUploadDot renders one dot frame: the image in a circle with the subtle white rim, plus the state's mark when
// marked is true (drawn at lookUploadSuper times the size and scaled down). Mark colors stay unchanged.
func lookUploadDot(square *image.RGBA, state string, marked bool) *image.RGBA {
	W := lookUploadDotSide * lookUploadSuper
	c := &canvas{RGBA: image.NewRGBA(image.Rect(0, 0, W, W)), w: float64(W)}
	photo := scaled(square, W)
	if marked && state == "offline" {
		grayOut(photo)
	}
	c.circle(photo)
	c.ring(0.012, color.NRGBA{0xFF, 0xFF, 0xFF, 110}) // the white rim every package has
	if marked {
		switch state {
		case "thinking":
			c.thinking()
		case "working":
			c.ring(0.075, withAlpha(lookUploadAccent, 60))
			c.arc(0.075, -90, 20, lookUploadAccent) // frame 0 of the generated packages: from the top, a third of the way round
		case "needs_you":
			c.ring(0.07, lookUploadNeedsYou)
		case "offline":
			c.sleeping()
		}
	}
	return scaled(c.RGBA, lookUploadDotSide)
}

// canvas draws the dot's marks on a W×W image: shapes are given in fractions of the side, colors blended over what is
// there (supersampling smooths their edges).
type canvas struct {
	*image.RGBA
	w float64
}

// circle puts photo inside the circle the frame is, leaving the corners clear.
func (c *canvas) circle(photo *image.RGBA) {
	r := c.w / 2
	for y := 0; y < c.Rect.Dy(); y++ {
		for x := 0; x < c.Rect.Dx(); x++ {
			if math.Hypot(float64(x)+0.5-r, float64(y)+0.5-r) <= r {
				c.SetRGBA(x, y, photo.RGBAAt(x, y))
			}
		}
	}
}

// ring is a ring of width wf (a fraction of the side) along the inside of the edge.
func (c *canvas) ring(wf float64, col color.NRGBA) {
	c.arc(wf, 0, 360, col)
}

// arc is the ring between from and to degrees (0 at three o'clock, clockwise, as PIL's arc), with round ends.
func (c *canvas) arc(wf, from, to float64, col color.NRGBA) {
	w := c.w * wf
	r := c.w/2 - w/2 // the middle of the stroke
	full := to-from >= 360
	cx := c.w / 2
	end := func(deg float64) (float64, float64) {
		a := deg * math.Pi / 180
		return cx + r*math.Cos(a), cx + r*math.Sin(a)
	}
	ax, ay := end(from)
	bx, by := end(to)
	c.each(func(x, y float64) bool {
		d := math.Abs(math.Hypot(x-cx, y-cx) - r)
		if d > w/2 {
			return false
		}
		if full {
			return true
		}
		deg := math.Mod(math.Atan2(y-cx, x-cx)*180/math.Pi-from+720, 360)
		return deg <= to-from || math.Hypot(x-ax, y-ay) <= w/2 || math.Hypot(x-bx, y-by) <= w/2
	}, col)
}

// thinking is the thought bubble at the top right with three dots, the first one lit (frame 0 of the generated packs).
func (c *canvas) thinking() {
	W := c.w
	cx, cy, r := W*0.76, W*0.19, W*0.15
	halfW, halfH := r, r*0.72
	bubble := func(x, y float64) bool { return inRoundRect(x, y, cx-halfW, cy-halfH, cx+halfW, cy+halfH, halfH) }
	tail := func(x, y float64) bool { return math.Hypot(x-(cx-r*0.7), y-(cy+r*0.77)) <= r*0.15 }
	// A soft outline first, so the white bubble stays apart from a light picture.
	outline := W * 0.012
	c.each(func(x, y float64) bool {
		return inRoundRect(x, y, cx-halfW-outline, cy-halfH-outline, cx+halfW+outline, cy+halfH+outline, halfH+outline) ||
			math.Hypot(x-(cx-r*0.7), y-(cy+r*0.77)) <= r*0.15+outline
	}, color.NRGBA{0, 0, 0, 40})
	c.each(func(x, y float64) bool { return bubble(x, y) || tail(x, y) }, color.NRGBA{0xFF, 0xFF, 0xFF, 250})
	for k := range 3 {
		x0 := cx + float64(k-1)*r*0.55
		rr, col := r*0.14, lookUploadDim
		if k == 0 {
			rr, col = r*0.17, lookUploadAccent
		}
		c.each(func(x, y float64) bool { return math.Hypot(x-x0, y-cy) <= rr }, col)
	}
}

// sleeping is three z's rising out of the top right, on a light halo so they read on a dark picture too.
func (c *canvas) sleeping() {
	W := c.w
	stroke := math.Max(2, W*0.026)
	// A still frame: the z's apart, growing and fading as they rise (the generated packages move them up instead).
	for k, z := range [3]struct{ x, y, size float64 }{{0.60, 0.40, 0.07}, {0.70, 0.27, 0.09}, {0.81, 0.11, 0.11}} {
		size := W * z.size
		x, y := W*z.x, W*z.y
		a := 255 - 40*k
		pts := [][2]float64{{x, y}, {x + size, y}, {x, y + size}, {x + size, y + size}}
		near := func(width float64) func(px, py float64) bool {
			return func(px, py float64) bool {
				for i := range 3 {
					if segmentDist(px, py, pts[i], pts[i+1]) <= width/2 {
						return true
					}
				}
				return false
			}
		}
		c.each(near(stroke*2), withAlpha(lookUploadWhite, uint8(a*3/5)))
		c.each(near(stroke), withAlpha(lookUploadZ, uint8(a)))
	}
}

// each blends col over every pixel whose center in is true for.
func (c *canvas) each(in func(x, y float64) bool, col color.NRGBA) {
	for y := 0; y < c.Rect.Dy(); y++ {
		for x := 0; x < c.Rect.Dx(); x++ {
			if in(float64(x)+0.5, float64(y)+0.5) {
				c.blend(x, y, col)
			}
		}
	}
}

// blend puts col (straight alpha) over the pixel (premultiplied): out = col·a + dst·(1 − a).
func (c *canvas) blend(x, y int, col color.NRGBA) {
	a := uint32(col.A)
	if a == 0 {
		return
	}
	d := c.RGBAAt(x, y)
	mix := func(s, d uint8) uint8 { return uint8((uint32(s)*a + uint32(d)*(255-a)) / 255) }
	c.SetRGBA(x, y, color.RGBA{R: mix(col.R, d.R), G: mix(col.G, d.G), B: mix(col.B, d.B), A: uint8(a + uint32(d.A)*(255-a)/255)})
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// grayOut fades the picture to the offline state's gray: color down to a fifth, a little darker.
func grayOut(img *image.RGBA) {
	for i := 0; i+3 < len(img.Pix); i += 4 {
		r, g, b := float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])
		l := 0.299*r + 0.587*g + 0.114*b
		f := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, (l+(v-l)*0.2)*0.92))) }
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = f(r), f(g), f(b)
	}
}

func inRoundRect(x, y, x0, y0, x1, y1, rad float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+rad, math.Min(x, x1-rad))
	cy := math.Max(y0+rad, math.Min(y, y1-rad))
	return math.Hypot(x-cx, y-cy) <= rad
}

func segmentDist(px, py float64, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((px-a[0])*dx+(py-a[1])*dy)/l))
	}
	return math.Hypot(px-(a[0]+t*dx), py-(a[1]+t*dy))
}
