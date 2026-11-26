package hub

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/lookmedia"
	"github.com/nwverse/persona/server/internal/proto"
)

func solidRect(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func makeTestGIFB64(t *testing.T, w, h int, colors []color.RGBA, delaysCS []int) string {
	t.Helper()
	pal := color.Palette{color.RGBA{0, 0, 0, 0}}
	for _, c := range colors {
		pal = append(pal, c)
	}
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: w, Height: h}, Delay: delaysCS}
	for i := range colors {
		p := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := range h {
			for x := range w {
				p.SetColorIndex(x, y, uint8(i+1))
			}
		}
		g.Image = append(g.Image, p)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func makeTestAPNGB64(t *testing.T, w, h int, colors []color.RGBA, delayMS int) string {
	t.Helper()
	frames := make([]lookmedia.Frame, len(colors))
	for i, c := range colors {
		frames[i] = lookmedia.NewFrame(solidRect(w, h, c), delayMS)
	}
	raw, err := lookmedia.EncodeAPNG(frames)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func makeTestWebPB64(t *testing.T, w, h int, colors []color.NRGBA, delayMS int) string {
	t.Helper()
	putLE24 := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	chunk := func(tag string, payload []byte) []byte {
		var buf bytes.Buffer
		buf.WriteString(tag)
		var sz [4]byte
		binary.LittleEndian.PutUint32(sz[:], uint32(len(payload)))
		buf.Write(sz[:])
		buf.Write(payload)
		if len(payload)&1 == 1 {
			buf.WriteByte(0)
		}
		return buf.Bytes()
	}
	vp8l := func(c color.NRGBA) []byte {
		var bits []byte
		var cur byte
		nb := 0
		put := func(val uint32, n int) {
			for range n {
				if (val & 1) != 0 {
					cur |= 1 << nb
				}
				val >>= 1
				nb++
				if nb == 8 {
					bits = append(bits, cur)
					cur, nb = 0, 0
				}
			}
		}
		s1 := func(sym byte) { put(1, 1); put(0, 1); put(1, 1); put(uint32(sym), 8) }
		put(0, 1)
		put(0, 1)
		put(0, 1)
		s1(c.G)
		s1(c.R)
		s1(c.B)
		s1(c.A)
		s1(0)
		if nb > 0 {
			bits = append(bits, cur)
		}
		out := make([]byte, 5+len(bits))
		out[0] = 0x2F
		binary.LittleEndian.PutUint32(out[1:5], uint32(w-1)|(uint32(h-1)<<14)|(1<<28))
		copy(out[5:], bits)
		return out
	}
	var body bytes.Buffer
	var vp8x [10]byte
	vp8x[0] = 0x12
	putLE24(vp8x[4:7], w-1)
	putLE24(vp8x[7:10], h-1)
	body.Write(chunk("VP8X", vp8x[:]))
	var anim [6]byte
	body.Write(chunk("ANIM", anim[:]))
	for _, c := range colors {
		var hdr [16]byte
		putLE24(hdr[6:9], w-1)
		putLE24(hdr[9:12], h-1)
		putLE24(hdr[12:15], delayMS)
		hdr[15] = 0x02 // noBlend
		var anmf bytes.Buffer
		anmf.Write(hdr[:])
		anmf.Write(chunk("VP8L", vp8l(c)))
		body.Write(chunk("ANMF", anmf.Bytes()))
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(4+body.Len()))
	out.Write(sz[:])
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func checkColorsMeetFloors(t *testing.T, colors *lookmedia.LookColors) {
	t.Helper()
	if colors == nil {
		t.Fatal("manifest has no colors")
	}
	white := color.NRGBA{255, 255, 255, 255}
	black := color.NRGBA{0, 0, 0, 255}
	for _, p := range []struct {
		name string
		s    lookmedia.ColorScheme
		page color.NRGBA
	}{{"light", colors.Light(), white}, {"dark", colors.Dark(), black}} {
		acc, _, err := lookmedia.ParseHexColor(p.s.Accent())
		if err != nil {
			t.Fatalf("%s accent %q: %v", p.name, p.s.Accent(), err)
		}
		bub, _, err := lookmedia.ParseHexColor(p.s.Bubble())
		if err != nil {
			t.Fatalf("%s bubble %q: %v", p.name, p.s.Bubble(), err)
		}
		btx, _, err := lookmedia.ParseHexColor(p.s.BubbleText())
		if err != nil {
			t.Fatalf("%s bubble_text %q: %v", p.name, p.s.BubbleText(), err)
		}
		if c := lookmedia.Contrast(white, acc); c < lookmedia.FloorOnAccent {
			t.Errorf("%s white on accent = %.2f < %.1f", p.name, c, lookmedia.FloorOnAccent)
		}
		if c := lookmedia.Contrast(acc, p.page); c < lookmedia.FloorLink {
			t.Errorf("%s accent on page = %.2f < %.1f", p.name, c, lookmedia.FloorLink)
		}
		if c := lookmedia.Contrast(bub, btx); c < lookmedia.FloorBubble {
			t.Errorf("%s bubble text = %.2f < %.1f", p.name, c, lookmedia.FloorBubble)
		}
	}
}

func readUploadedManifest(t *testing.T, dir string) uploadedLookManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "persona.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m uploadedLookManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLookUploadsStatesPostAnimatedAllFormatsAndCrop(t *testing.T) {
	if !slices.Contains(proto.Features, proto.FeatureLookUploadsStates) {
		t.Fatal("look_uploads_states feature is not announced")
	}

	h := newTestHub(t)
	h.store = &settingsStore{vals: map[string][]byte{}}
	mux := h.routes()
	master := h.cfg.Token

	// 1. Default POST with only an image yields 5 states + auto colors clearing all floors.
	code, out := call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name":  "Simple",
		"image": pngBase64(t, solidRect(128, 128, color.RGBA{58, 123, 213, 255})),
	}))
	if code != 200 {
		t.Fatalf("simple POST: %d %v", code, out)
	}
	simpleID := out["look"].(map[string]any)["id"].(string)
	simpleManifest := readUploadedManifest(t, filepath.Join(h.personasDir(), simpleID))
	if len(simpleManifest.Dot.States) != 5 {
		t.Fatalf("expected 5 states, got %d", len(simpleManifest.Dot.States))
	}
	checkColorsMeetFloors(t, simpleManifest.Colors)
	if simpleManifest.Source.Color == nil || !simpleManifest.Source.Color.Auto || simpleManifest.Source.Color.Seed != "#3A7BD5" {
		t.Fatalf("unexpected auto color source: %+v", simpleManifest.Source.Color)
	}

	// 2. Full POST with animated GIF main, animated APNG working (marked), animated WebP needs_you (unmarked),
	//    still cropped thinking (unmarked), own still idle, own animated offline (unmarked), and chosen color.
	//    Main is 200x100 with red left (0..100) and blue right (100..200), cropped to the blue right square.
	mainF0 := image.NewRGBA(image.Rect(0, 0, 200, 100))
	mainF1 := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			if x < 100 {
				mainF0.SetRGBA(x, y, color.RGBA{220, 20, 20, 255})
				mainF1.SetRGBA(x, y, color.RGBA{220, 20, 20, 255})
			} else {
				mainF0.SetRGBA(x, y, color.RGBA{20, 90, 220, 255})
				mainF1.SetRGBA(x, y, color.RGBA{20, 180, 220, 255})
			}
		}
	}
	mainAPNG, err := lookmedia.EncodeAPNG([]lookmedia.Frame{
		lookmedia.NewFrame(mainF0, 150),
		lookmedia.NewFrame(mainF1, 150),
	})
	if err != nil {
		t.Fatal(err)
	}
	gifState := makeTestGIFB64(t, 96, 96, []color.RGBA{{220, 40, 40, 255}, {220, 180, 40, 255}}, []int{15, 15})
	apngState := makeTestAPNGB64(t, 96, 96, []color.RGBA{{40, 200, 60, 255}, {40, 200, 180, 255}, {40, 100, 200, 255}}, 140)
	webpState := makeTestWebPB64(t, 96, 96, []color.NRGBA{{180, 60, 220, 255}, {220, 60, 140, 255}}, 160)
	stillState := pngBase64(t, bands(200, 100, 100)) // left half red, right half blue

	code, out = call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name":     "AnimatedAll",
		"subtitle": "All formats",
		"image":    base64.StdEncoding.EncodeToString(mainAPNG),
		"crop":     map[string]any{"x": 100, "y": 0, "side": 100},
		"color":    "#e8a054",
		"choose":   true,
		"states": map[string]any{
			"idle":      map[string]any{"image": gifState, "mark": true}, // idle always mark=false
			"thinking":  map[string]any{"image": stillState, "crop": map[string]any{"x": 0, "y": 0, "side": 100}, "mark": false},
			"working":   map[string]any{"image": apngState, "mark": true},
			"needs_you": map[string]any{"image": webpState, "mark": false},
			"offline":   map[string]any{"image": gifState, "mark": false},
		},
	}))
	if code != 200 {
		t.Fatalf("animated POST: %d %v", code, out)
	}
	id := out["look"].(map[string]any)["id"].(string)
	dir := filepath.Join(h.personasDir(), id)
	m := readUploadedManifest(t, dir)

	checkColorsMeetFloors(t, m.Colors)
	if m.Source.Color == nil || m.Source.Color.Auto || m.Source.Color.Seed != "#E8A054" {
		t.Fatalf("color source: %+v", m.Source.Color)
	}
	if m.Source.States["idle"].Mark {
		t.Fatal("idle must have mark=false")
	}
	if !m.Source.States["working"].Own || !m.Source.States["working"].Mark {
		t.Fatalf("working source state: %+v", m.Source.States["working"])
	}
	if !m.Source.States["needs_you"].Own || m.Source.States["needs_you"].Mark {
		t.Fatalf("needs_you source state: %+v", m.Source.States["needs_you"])
	}
	// Avatar 512 must be the cropped blue right half, still (not red left half).
	av := readPNG(t, filepath.Join(dir, "avatar", "512.png"))
	if c := nrgba(av, 256, 256); c.B < 180 || c.R > 60 {
		t.Fatalf("cropped avatar middle = %v, want blue", c)
	}
	// Working APNG has 3 frames + orange arc mark on frame 0; needs_you WebP->APNG has 2 frames + NO red ring mark.
	workRaw, err := os.ReadFile(filepath.Join(dir, "dot", "working.png"))
	if err != nil {
		t.Fatal(err)
	}
	workAnim, err := lookmedia.Decode(workRaw)
	if err != nil || len(workAnim.Frames()) != 3 {
		t.Fatalf("working APNG: err=%v frames=%d", err, len(workAnim.Frames()))
	}
	if c := nrgba(workAnim.Frames()[0].Image(), 140, 72); c.R < 180 || c.B > 120 {
		t.Fatalf("working frame 0 should have orange arc mark, got %v", c)
	}
	needsRaw, err := os.ReadFile(filepath.Join(dir, "dot", "needs_you.png"))
	if err != nil {
		t.Fatal(err)
	}
	needsAnim, err := lookmedia.Decode(needsRaw)
	if err != nil || len(needsAnim.Frames()) != 2 {
		t.Fatalf("needs_you APNG: err=%v frames=%d", err, len(needsAnim.Frames()))
	}
	// With mark=false on purple needs_you, top rim should NOT be pure red (G low, B low).
	if c := nrgba(needsAnim.Frames()[0].Image(), 72, 4); c.B < 100 {
		t.Fatalf("unmarked needs_you got red ring mark: %v", c)
	}
	// .edit/ must exist on disk, must NOT contain any original uploaded bytes anywhere, and must return 404 via HTTP.
	editMain, err := os.ReadFile(filepath.Join(dir, ".edit", "main.png"))
	if err != nil {
		t.Fatalf(".edit/main.png missing: %v", err)
	}
	editAnim, err := lookmedia.Decode(editMain)
	if err != nil || editAnim.Width() != lookUploadDotSide || editAnim.Height() != lookUploadDotSide {
		t.Fatalf(".edit/main.png size = %dx%d, want %d", editAnim.Width(), editAnim.Height(), lookUploadDotSide)
	}
	gifRaw, _ := base64.StdEncoding.DecodeString(gifState)
	apngRaw, _ := base64.StdEncoding.DecodeString(apngState)
	webpRaw, _ := base64.StdEncoding.DecodeString(webpState)
	stillRaw, _ := base64.StdEncoding.DecodeString(stillState)
	originals := [][]byte{mainAPNG, gifRaw, apngRaw, webpRaw, stillRaw}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, orig := range originals {
			if bytes.Equal(data, orig) {
				t.Fatalf("file %s stored original uploaded bytes", p)
			}
		}
		return nil
	})
	if code, _ := call(t, mux, "GET", "/api/v1/personas/"+id+"/.edit/main.png", master, ""); code != 404 {
		t.Fatalf(".edit file served with status %d, want 404", code)
	}

	// Automatic states with an animated main picture: idle/thinking/working/needs_you animate, offline stays still.
	code, out = call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name":  "AutoAnimMain",
		"image": makeTestGIFB64(t, 96, 96, []color.RGBA{{30, 120, 220, 255}, {30, 200, 120, 255}}, []int{15, 15}),
	}))
	if code != 200 {
		t.Fatalf("auto animated main POST: %d %v", code, out)
	}
	autoID := out["look"].(map[string]any)["id"].(string)
	autoDir := filepath.Join(h.personasDir(), autoID)
	for _, st := range []string{"idle", "thinking", "working", "needs_you"} {
		b, err := os.ReadFile(filepath.Join(autoDir, "dot", st+".png"))
		if err != nil {
			t.Fatal(err)
		}
		a, err := lookmedia.Decode(b)
		if err != nil || len(a.Frames()) != 2 {
			t.Fatalf("auto %s expected 2 animated frames, got err=%v len=%d", st, err, len(a.Frames()))
		}
	}
	offBytes, err := os.ReadFile(filepath.Join(autoDir, "dot", "offline.png"))
	if err != nil {
		t.Fatal(err)
	}
	offAnim, err := lookmedia.Decode(offBytes)
	if err != nil || len(offAnim.Frames()) != 1 {
		t.Fatalf("auto offline must stay still (1 frame), got err=%v len=%d", err, len(offAnim.Frames()))
	}
}

func TestLookUploadsPatchEveryKindAndLegacy(t *testing.T) {
	h := newTestHub(t)
	h.store = &settingsStore{vals: map[string][]byte{}}
	mux := h.routes()
	master := h.cfg.Token
	app := appClient(h)

	// Create an animated-main look and choose it.
	animMain := makeTestAPNGB64(t, 96, 96, []color.RGBA{{40, 100, 220, 255}, {40, 180, 220, 255}}, 150)
	code, out := call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name": "Original", "subtitle": "First", "image": animMain, "choose": true,
	}))
	if code != 200 {
		t.Fatalf("POST: %d %v", code, out)
	}
	id := out["look"].(map[string]any)["id"].(string)
	rev := out["look"].(map[string]any)["rev"].(string)
	_ = broadcastTypes(app)

	patchOK := func(body map[string]any) map[string]any {
		t.Helper()
		c, o := call(t, mux, "PATCH", "/api/v1/looks/uploads/"+id, master, uploadBody(t, body))
		if c != 200 {
			t.Fatalf("PATCH %v -> %d %v", body, c, o)
		}
		look := o["look"].(map[string]any)
		if look["id"] != id {
			t.Fatalf("id changed: %v != %s", look["id"], id)
		}
		if look["rev"] == rev {
			t.Fatalf("rev did not change after PATCH %v", body)
		}
		rev = look["rev"].(string)
		if got := broadcastTypes(app); got["personas"] == 0 {
			t.Fatalf("no personas broadcast after PATCH %v", body)
		}
		return look
	}

	// 1. Name and subtitle (clearing subtitle).
	look := patchOK(map[string]any{"name": "Renamed", "subtitle": ""})
	if look["name"] != "Renamed" || look["subtitle"] != nil {
		t.Fatalf("rename/subtitle: %v", look)
	}

	// 2. Give working its own still picture with mark=true.
	greenStill := pngBase64(t, solidRect(96, 96, color.RGBA{40, 200, 80, 255}))
	patchOK(map[string]any{"states": map[string]any{"working": map[string]any{"image": greenStill, "mark": true}}})
	dir := filepath.Join(h.personasDir(), id)
	workMarked := readPNG(t, filepath.Join(dir, "dot", "working.png"))
	if c := nrgba(workMarked, 140, 72); c.R < 180 || c.B > 120 {
		t.Fatalf("working mark=true missing orange arc: %v", c)
	}

	// 3. Toggle working mark off without re-uploading the picture (uses .edit/working.png).
	patchOK(map[string]any{"states": map[string]any{"working": map[string]any{"mark": false}}})
	workUnmarked := readPNG(t, filepath.Join(dir, "dot", "working.png"))
	if c := nrgba(workUnmarked, 140, 72); c.G < 160 || c.R > 100 {
		t.Fatalf("working mark=false still has orange arc: %v", c)
	}
	if m := readUploadedManifest(t, dir); m.Source.States["working"].Mark {
		t.Fatalf("manifest still says mark=true")
	}

	// 4. Reset working back to automatic (restores 2-frame animation from .edit/main.png with mark!).
	patchOK(map[string]any{"states": map[string]any{"working": map[string]any{"auto": true}}})
	if _, err := os.Stat(filepath.Join(dir, ".edit", "working.png")); !os.IsNotExist(err) {
		t.Fatalf(".edit/working.png should be cleaned up on auto reset")
	}
	workAutoBytes, _ := os.ReadFile(filepath.Join(dir, "dot", "working.png"))
	workAutoAnim, err := lookmedia.Decode(workAutoBytes)
	if err != nil || len(workAutoAnim.Frames()) != 2 {
		t.Fatalf("working back to auto should animate with main (2 frames), got err=%v len=%d", err, len(workAutoAnim.Frames()))
	}

	// 5. Change color to chosen #E68C28, then back to auto.
	patchOK(map[string]any{"color": "#E68C28"})
	if m := readUploadedManifest(t, dir); m.Source.Color == nil || m.Source.Color.Auto || m.Source.Color.Seed != "#E68C28" {
		t.Fatalf("chosen color: %+v", m.Source.Color)
	}
	patchOK(map[string]any{"color": "auto"})
	if m := readUploadedManifest(t, dir); m.Source.Color == nil || !m.Source.Color.Auto {
		t.Fatalf("auto color: %+v", m.Source.Color)
	}

	// 6. Replace main picture with a cropped still red image (automatic states & auto color regenerate; own state untouched).
	patchOK(map[string]any{"states": map[string]any{"thinking": map[string]any{"image": greenStill, "mark": false}}})
	wideRedBlue := pngBase64(t, bands(200, 100, 100)) // left 100x100 red (#DC2828), right 100x100 blue
	patchOK(map[string]any{"image": wideRedBlue, "crop": map[string]any{"x": 0, "y": 0, "side": 100}})
	m := readUploadedManifest(t, dir)
	if m.Source.Color == nil || !m.Source.Color.Auto {
		t.Fatalf("auto color did not update on new main image: %+v", m.Source.Color)
	}
	// Own thinking state stayed green; automatic idle became still red.
	if c := nrgba(readPNG(t, filepath.Join(dir, "dot", "thinking.png")), 72, 72); c.G < 160 {
		t.Fatalf("own thinking state was overwritten: %v", c)
	}
	if c := nrgba(readPNG(t, filepath.Join(dir, "dot", "idle.png")), 72, 72); c.R < 180 {
		t.Fatalf("auto idle state did not update to red: %v", c)
	}
	if _, err := os.Stat(filepath.Join(dir, ".edit", "main.png")); !os.IsNotExist(err) {
		t.Fatalf(".edit/main.png should be removed when main picture becomes still")
	}
	// Chosen look stays chosen after edits.
	_, listOut := call(t, mux, "GET", "/api/v1/looks", master, "")
	if cur := listOut["current"].(map[string]any); cur["id"] != id || cur["effective"] != id {
		t.Fatalf("chosen look changed during edit: %v", cur)
	}

	// 7. Old-format upload (no colors, no source.states, no .edit/) is still listed and editable.
	oldID := "u-old00001"
	oldDir := filepath.Join(h.personasDir(), oldID)
	if err := os.MkdirAll(filepath.Join(oldDir, "avatar"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(oldDir, "dot"), 0o700); err != nil {
		t.Fatal(err)
	}
	stillBytes, _ := encodePNG(solidRect(128, 128, color.RGBA{58, 123, 213, 255}))
	dotBytes, _ := encodePNG(solidRect(144, 144, color.RGBA{58, 123, 213, 255}))
	for _, s := range []string{"64", "128", "256", "512"} {
		_ = os.WriteFile(filepath.Join(oldDir, "avatar", s+".png"), stillBytes, 0o600)
	}
	for _, st := range personaStates {
		_ = os.WriteFile(filepath.Join(oldDir, "dot", st+".png"), dotBytes, 0o600)
	}
	oldManifest := `{"format":1,"id":"u-old00001","name":"Legacy","avatar":{"sizes":{"64":"avatar/64.png","128":"avatar/128.png","256":"avatar/256.png","512":"avatar/512.png"},"safe":1},"dot":{"size":144,"states":{"idle":{"file":"dot/idle.png"},"thinking":{"file":"dot/thinking.png"},"working":{"file":"dot/working.png"},"needs_you":{"file":"dot/needs_you.png"},"offline":{"file":"dot/offline.png"}}},"app_icon_fallback":"persona","source":{"kind":"upload","uploaded_at":"2026-10-01T00:00:00Z"}}`
	_ = os.WriteFile(filepath.Join(oldDir, "persona.json"), []byte(oldManifest), 0o600)
	_, listOut = call(t, mux, "GET", "/api/v1/looks", master, "")
	if lookByID(t, listOut, oldID) == nil {
		t.Fatal("legacy upload not listed")
	}
	// PATCH color on legacy upload adds colors!
	code, out = call(t, mux, "PATCH", "/api/v1/looks/uploads/"+oldID, master, uploadBody(t, map[string]any{"color": "auto"}))
	if code != 200 {
		t.Fatalf("PATCH legacy color: %d %v", code, out)
	}
	legacyM := readUploadedManifest(t, oldDir)
	checkColorsMeetFloors(t, legacyM.Colors)
	if legacyM.Source.Color == nil || !legacyM.Source.Color.Auto || legacyM.Source.Color.Seed != "#3A7BD5" {
		t.Fatalf("legacy auto color: %+v", legacyM.Source.Color)
	}
}

func TestLookUploadsNewRefusalsLocalized(t *testing.T) {
	h := newTestHub(t)
	h.store = &settingsStore{vals: map[string][]byte{}}
	mux := h.routes()
	master := h.cfg.Token

	img := pngBase64(t, solidRect(100, 100, color.RGBA{40, 120, 200, 255}))
	// Create one uploaded look + one hand-placed look for PATCH refusal tests.
	code, out := call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{"name": "Base", "image": img}))
	if code != 200 {
		t.Fatalf("seed POST: %d %v", code, out)
	}
	id := out["look"].(map[string]any)["id"].(string)
	handDir := filepath.Join(h.personasDir(), "handmade")
	if err := os.CopyFS(handDir, os.DirFS(filepath.Join(h.personasDir(), id))); err != nil {
		t.Fatal(err)
	}
	hm, _ := os.ReadFile(filepath.Join(handDir, "persona.json"))
	hm = bytes.Replace(hm, []byte(`"`+id+`"`), []byte(`"handmade"`), 1)
	hm = bytes.Replace(hm, []byte(`"kind": "upload"`), []byte(`"kind": "manual"`), 1)
	_ = os.WriteFile(filepath.Join(handDir, "persona.json"), hm, 0o600)

	tooLongGIF := makeTestGIFB64(t, 64, 64, []color.RGBA{{200, 0, 0, 255}, {0, 200, 0, 255}}, []int{500, 500})
	// Crafted frame-count bomb (>300 frames).
	var bomb bytes.Buffer
	bomb.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writeChunk := func(tag string, data []byte) {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[0:4], uint32(len(data)))
		copy(hdr[4:8], tag)
		bomb.Write(hdr[:])
		bomb.Write(data)
		bomb.Write([]byte{0, 0, 0, 0})
	}
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], 64)
	binary.BigEndian.PutUint32(ihdr[4:8], 64)
	ihdr[8], ihdr[9] = 8, 6
	writeChunk("IHDR", ihdr[:])
	var actl [8]byte
	binary.BigEndian.PutUint32(actl[0:4], 400)
	writeChunk("acTL", actl[:])
	writeChunk("IEND", nil)
	tooManyFramesB64 := base64.StdEncoding.EncodeToString(bomb.Bytes())

	// Total > 24 MB across 4 images (7 MB each = 28 MB).
	sevenMB := base64.StdEncoding.EncodeToString(make([]byte, 7<<20))

	cases := []struct {
		name      string
		method    string
		path      string
		body      map[string]any
		wantHTTP  int
		wantCode  string
		wantSubEN string
	}{
		{"too long", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": tooLongGIF}, 400, "animation_too_long", "9"},
		{"too many frames", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": tooManyFramesB64}, 413, "too_many_frames", ""},
		{"total images too large", "POST", "/api/v1/looks/uploads", map[string]any{
			"name": "a", "image": sevenMB,
			"states": map[string]any{
				"thinking":  map[string]any{"image": sevenMB},
				"working":   map[string]any{"image": sevenMB},
				"needs_you": map[string]any{"image": sevenMB},
			},
		}, 413, "too_large", "24"},
		{"bad crop out of bounds", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": img, "crop": map[string]any{"x": 50, "y": 0, "side": 64}}, 400, "bad_crop", "64"},
		{"bad crop too small", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": img, "crop": map[string]any{"x": 0, "y": 0, "side": 32}}, 400, "bad_crop", "64"},
		{"bad color", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": img, "color": "blue"}, 400, "bad_color", ""},
		{"bad state", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": img, "states": map[string]any{"dancing": map[string]any{"image": img}}}, 400, "bad_state", ""},
		{"mark without own picture on POST", "POST", "/api/v1/looks/uploads", map[string]any{"name": "a", "image": img, "states": map[string]any{"working": map[string]any{"mark": false}}}, 400, "mark_without_image", ""},
		{"mark without own picture on PATCH", "PATCH", "/api/v1/looks/uploads/" + id, map[string]any{"states": map[string]any{"working": map[string]any{"mark": false}}}, 400, "mark_without_image", ""},
		{"empty PATCH", "PATCH", "/api/v1/looks/uploads/" + id, map[string]any{}, 400, "bad_fields", ""},
		{"crop without image on PATCH", "PATCH", "/api/v1/looks/uploads/" + id, map[string]any{"name": "b", "crop": map[string]any{"x": 0, "y": 0, "side": 64}}, 400, "bad_fields", ""},
		{"auto combined with mark on PATCH", "PATCH", "/api/v1/looks/uploads/" + id, map[string]any{"states": map[string]any{"working": map[string]any{"auto": true, "mark": false}}}, 400, "bad_fields", ""},
		{"PATCH not_found", "PATCH", "/api/v1/looks/uploads/u-missing", map[string]any{"name": "b"}, 404, "not_found", ""},
		{"PATCH not_uploaded", "PATCH", "/api/v1/looks/uploads/handmade", map[string]any{"name": "b"}, 409, "not_uploaded", ""},
	}

	for _, tc := range cases {
		for _, lang := range []string{"en", "zh-Hans", "ja"} {
			t.Run(tc.name+"/"+lang, func(t *testing.T) {
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(uploadBody(t, tc.body)))
				req.RemoteAddr = "127.0.0.1:1234"
				req.Header.Set("Authorization", "Bearer "+master)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept-Language", lang)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				var resp map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &resp)
				if rec.Code != tc.wantHTTP || resp["code"] != tc.wantCode {
					t.Fatalf("got %d %v, want %d %s", rec.Code, resp, tc.wantHTTP, tc.wantCode)
				}
				msg, _ := resp["error"].(string)
				if msg == "" || strings.Contains(msg, "hub-look_uploads.") {
					t.Fatalf("missing localized message for %s (%s): %q", tc.wantCode, lang, msg)
				}
				if lang == "en" && tc.wantSubEN != "" && !strings.Contains(msg, tc.wantSubEN) {
					t.Errorf("en message %q does not contain %q", msg, tc.wantSubEN)
				}
			})
		}
	}
}

func TestWorstCaseFiveAnimatedStatesStaysUnder16MB(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	noisyFrames := func(n int) []lookmedia.Frame {
		out := make([]lookmedia.Frame, n)
		for i := range n {
			img := image.NewRGBA(image.Rect(0, 0, 160, 160))
			for p := 0; p < len(img.Pix); p += 4 {
				img.Pix[p] = uint8(rng.Intn(256))
				img.Pix[p+1] = uint8(rng.Intn(256))
				img.Pix[p+2] = uint8(rng.Intn(256))
				img.Pix[p+3] = 255
			}
			out[i] = lookmedia.NewFrame(img, 120)
		}
		return out
	}

	mainFrames := noisyFrames(1)
	own := make(map[string]ownStateInput, len(personaStates))
	for _, st := range personaStates {
		own[st] = ownStateInput{frames: noisyFrames(lookmedia.MaxOutputFrames), mark: st != "idle"}
	}
	files, err := buildUploadedLookFiles("u-worst001", "WorstCase", "", mainFrames, own, "auto", color.NRGBA{}, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHub(t)
	if err := h.writeUploadedLook("u-worst001", files); err != nil {
		t.Fatalf("worst-case package rejected: %v", err)
	}
	pkg, err := openPersona(os.DirFS(filepath.Join(h.personasDir(), "u-worst001")), "u-worst001")
	if err != nil {
		t.Fatalf("openPersona worst case: %v", err)
	}
	if pkg.total > personaMaxTotal {
		t.Fatalf("worst-case package total %d > %d (16 MB)", pkg.total, personaMaxTotal)
	}
	t.Logf("worst-case 5x36 animated noisy package total: %d bytes (%.2f MB)", pkg.total, float64(pkg.total)/(1<<20))
}

func TestWriteSamplePackageForMakePersonaCheck(t *testing.T) {
	outDir := os.Getenv("PERSONA_WRITE_LOOK_SAMPLE")
	if outDir == "" {
		t.Skip("set PERSONA_WRITE_LOOK_SAMPLE to export a hub-built package for scripts/assets/make_persona.py check")
	}
	h := newTestHub(t)
	h.store = &settingsStore{vals: map[string][]byte{}}
	mux := h.routes()
	master := h.cfg.Token

	mainImg := pngBase64(t, solidRect(256, 256, color.RGBA{58, 123, 213, 255}))
	workAnim := makeTestAPNGB64(t, 144, 144, []color.RGBA{
		{40, 140, 230, 255}, {70, 170, 240, 255}, {100, 190, 245, 255}, {70, 170, 240, 255},
	}, 150)
	code, out := call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name":     "AnimatedSample",
		"subtitle": "Hub sample with animated state and chosen color",
		"image":    mainImg,
		"color":    "#E8A054",
		"states": map[string]any{
			"working": map[string]any{"image": workAnim, "mark": true},
		},
	}))
	if code != 200 {
		t.Fatalf("POST sample: %d %v", code, out)
	}
	id := out["look"].(map[string]any)["id"].(string)
	dst := filepath.Join(outDir, id)
	_ = os.RemoveAll(dst)
	if err := os.CopyFS(dst, os.DirFS(filepath.Join(h.personasDir(), id))); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote sample package to %s", dst)
}

func TestAtomicEditNeverDropsLookAndFailedEditChangesNothing(t *testing.T) {
	h := newTestHub(t)
	h.store = &settingsStore{vals: map[string][]byte{}}
	mux := h.routes()
	master := h.cfg.Token

	img := pngBase64(t, solidRect(96, 96, color.RGBA{40, 120, 220, 255}))
	code, out := call(t, mux, "POST", "/api/v1/looks/uploads", master, uploadBody(t, map[string]any{
		"name": "Atomic", "image": img, "choose": true,
	}))
	if code != 200 {
		t.Fatalf("POST: %d %v", code, out)
	}
	id := out["look"].(map[string]any)["id"].(string)
	origRev := out["look"].(map[string]any)["rev"].(string)

	// A failed PATCH leaves the look completely untouched (same rev, same name, no leftover dot dirs).
	if code, _ := call(t, mux, "PATCH", "/api/v1/looks/uploads/"+id, master, uploadBody(t, map[string]any{
		"name": "ShouldNotLand", "color": "not-a-color",
	})); code != 400 {
		t.Fatalf("expected 400, got %d", code)
	}
	_, listOut := call(t, mux, "GET", "/api/v1/looks", master, "")
	listed := lookByID(t, listOut, id)
	if listed == nil || listed["rev"] != origRev || listed["name"] != "Atomic" {
		t.Fatalf("failed edit mutated look: %v", listed)
	}

	// Hammer GET /api/v1/looks from multiple reader goroutines while performing 30 rapid PATCHes.
	var stop atomic.Bool
	var reads atomic.Int64
	var missing atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				req := httptest.NewRequest("GET", "/api/v1/looks", nil)
				req.RemoteAddr = "127.0.0.1:1234"
				req.Header.Set("Authorization", "Bearer "+master)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != 200 {
					missing.Add(1)
					continue
				}
				var body map[string]any
				if json.Unmarshal(rec.Body.Bytes(), &body) != nil {
					missing.Add(1)
					continue
				}
				found := false
				for _, x := range body["looks"].([]any) {
					if x.(map[string]any)["id"] == id {
						found = true
						break
					}
				}
				if !found {
					missing.Add(1)
				}
				reads.Add(1)
			}
		}()
	}

	palette := []string{"#3A7BD5", "#E8A054", "#32B464", "#D94A64"}
	for i := range 30 {
		c, o := call(t, mux, "PATCH", "/api/v1/looks/uploads/"+id, master, uploadBody(t, map[string]any{
			"name":  "Atomic-" + string(rune('A'+(i%26))),
			"color": palette[i%len(palette)],
		}))
		if c != 200 {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("PATCH %d failed: %d %v", i, c, o)
		}
	}
	stop.Store(true)
	wg.Wait()

	if missing.Load() > 0 {
		t.Fatalf("look went missing %d times across %d concurrent reads", missing.Load(), reads.Load())
	}
	if reads.Load() == 0 {
		t.Fatal("reader goroutines did not execute")
	}
	entries, _ := os.ReadDir(h.personasDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover dot dir after edits: %s", e.Name())
		}
	}
}
