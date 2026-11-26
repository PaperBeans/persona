package lookmedia_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/lookmedia"
)

func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func makeAnimatedGIF(t *testing.T, w, h int, colors []color.RGBA, delaysCS []int, disposals []byte) []byte {
	t.Helper()
	pal := color.Palette{color.RGBA{0, 0, 0, 0}}
	for _, c := range colors {
		pal = append(pal, c)
	}
	g := &gif.GIF{
		Config:   image.Config{ColorModel: pal, Width: w, Height: h},
		Delay:    delaysCS,
		Disposal: disposals,
	}
	for i := range colors {
		p := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		if i > 0 && len(disposals) > 0 {
			// Sub-rect on frame > 0 to exercise disposal & compositing.
			p = image.NewPaletted(image.Rect(w/4, h/4, 3*w/4, 3*h/4), pal)
		}
		for y := p.Bounds().Min.Y; y < p.Bounds().Max.Y; y++ {
			for x := p.Bounds().Min.X; x < p.Bounds().Max.X; x++ {
				p.SetColorIndex(x, y, uint8(i+1))
			}
		}
		g.Image = append(g.Image, p)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// encodeSolidVP8L builds a valid RFC 9649 lossless VP8L bitstream for a solid w*h RGBA rectangle.
func encodeSolidVP8L(w, h int, c color.NRGBA) []byte {
	var bits []byte
	var cur byte
	nbits := 0
	put := func(val uint32, n int) {
		for range n {
			if (val & 1) != 0 {
				cur |= 1 << nbits
			}
			val >>= 1
			nbits++
			if nbits == 8 {
				bits = append(bits, cur)
				cur = 0
				nbits = 0
			}
		}
	}
	// 1-symbol simple Huffman code for a channel.
	putSimple1 := func(sym byte) {
		put(1, 1) // simple code
		put(0, 1) // num_symbols - 1 = 0 (1 symbol)
		put(1, 1) // is_first_8bits = 1
		put(uint32(sym), 8)
	}
	put(0, 1) // no transforms
	put(0, 1) // no color cache
	put(0, 1) // no meta Huffman
	// Prefix codes: Green, Red, Blue, Alpha, Distance.
	putSimple1(c.G)
	putSimple1(c.R)
	putSimple1(c.B)
	putSimple1(c.A)
	putSimple1(0)
	if nbits > 0 {
		bits = append(bits, cur)
	}
	hdr := uint32(w-1) | (uint32(h-1) << 14) | (1 << 28) // alpha_is_used=1, version=0
	out := make([]byte, 5+len(bits))
	out[0] = 0x2F
	binary.LittleEndian.PutUint32(out[1:5], hdr)
	copy(out[5:], bits)
	return out
}

func makeRIFFChunk(tag string, payload []byte) []byte {
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

type testWebPFrame struct {
	x, y, w, h int
	delayMS    int
	dispose    bool
	noBlend    bool
	col        color.NRGBA
}

func makeAnimatedWebP(canvasW, canvasH int, bg color.NRGBA, frames []testWebPFrame) []byte {
	putLE24 := func(b []byte, v int) {
		b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
	}
	var body bytes.Buffer
	var vp8x [10]byte
	vp8x[0] = 0x12 // animation + alpha
	putLE24(vp8x[4:7], canvasW-1)
	putLE24(vp8x[7:10], canvasH-1)
	body.Write(makeRIFFChunk("VP8X", vp8x[:]))

	var anim [6]byte
	anim[0], anim[1], anim[2], anim[3] = bg.B, bg.G, bg.R, bg.A // BGRA
	body.Write(makeRIFFChunk("ANIM", anim[:]))

	for _, f := range frames {
		var hdr [16]byte
		putLE24(hdr[0:3], f.x/2)
		putLE24(hdr[3:6], f.y/2)
		putLE24(hdr[6:9], f.w-1)
		putLE24(hdr[9:12], f.h-1)
		putLE24(hdr[12:15], f.delayMS)
		var flags byte
		if f.dispose {
			flags |= 0x01
		}
		if f.noBlend {
			flags |= 0x02
		}
		hdr[15] = flags
		var anmf bytes.Buffer
		anmf.Write(hdr[:])
		anmf.Write(makeRIFFChunk("VP8L", encodeSolidVP8L(f.w, f.h, f.col)))
		body.Write(makeRIFFChunk("ANMF", anmf.Bytes()))
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(4+body.Len()))
	out.Write(riffSize[:])
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return out.Bytes()
}

func TestAPNGEncodeDecodeRoundTripAndFrame0PNG(t *testing.T) {
	f0 := solidRGBA(72, 72, color.RGBA{220, 40, 40, 255})
	f1 := solidRGBA(72, 72, color.RGBA{40, 200, 60, 255})
	f2 := solidRGBA(72, 72, color.RGBA{40, 80, 220, 255})
	frames := []lookmedia.Frame{
		lookmedia.NewFrame(f0, 120),
		lookmedia.NewFrame(f1, 250),
		lookmedia.NewFrame(f2, 180),
	}

	raw, err := lookmedia.EncodeAPNG(frames)
	if err != nil {
		t.Fatal(err)
	}
	// Must decode as a standard PNG returning frame 0.
	img0, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode frame 0: %v", err)
	}
	if c := color.RGBAModel.Convert(img0.At(10, 10)).(color.RGBA); c != (color.RGBA{220, 40, 40, 255}) {
		t.Fatalf("frame 0 pixel = %v, want red", c)
	}

	// Must round-trip through our own APNG decoder with exact delays and pixels.
	anim, err := lookmedia.Decode(raw)
	if err != nil {
		t.Fatalf("Decode APNG: %v", err)
	}
	if len(anim.Frames()) != 3 {
		t.Fatalf("got %d frames, want 3", len(anim.Frames()))
	}
	wantDelays := []int{120, 250, 180}
	wantColors := []color.RGBA{{220, 40, 40, 255}, {40, 200, 60, 255}, {40, 80, 220, 255}}
	for i := range 3 {
		if anim.Frames()[i].DelayMS() != wantDelays[i] {
			t.Errorf("frame %d delay = %d, want %d", i, anim.Frames()[i].DelayMS(), wantDelays[i])
		}
		if got := anim.Frames()[i].Image().RGBAAt(36, 36); got != wantColors[i] {
			t.Errorf("frame %d pixel = %v, want %v", i, got, wantColors[i])
		}
	}

	// Paletted APNG round-trip too (with transparent corners).
	f0.SetRGBA(0, 0, color.RGBA{0, 0, 0, 0})
	f1.SetRGBA(0, 0, color.RGBA{0, 0, 0, 0})
	f2.SetRGBA(0, 0, color.RGBA{0, 0, 0, 0})
	palRaw, err := lookmedia.EncodeAPNGPaletted(frames)
	if err != nil {
		t.Fatal(err)
	}
	palAnim, err := lookmedia.Decode(palRaw)
	if err != nil {
		t.Fatalf("Decode paletted APNG: %v", err)
	}
	if len(palAnim.Frames()) != 3 {
		t.Fatalf("paletted got %d frames, want 3", len(palAnim.Frames()))
	}
	if a := palAnim.Frames()[1].Image().RGBAAt(0, 0).A; a != 0 {
		t.Fatalf("paletted transparent corner alpha = %d, want 0", a)
	}
}

func TestDecodeGIFWithDisposal(t *testing.T) {
	// Frame 0: full blue (DisposalNone); Frame 1: center red (DisposalPrevious); Frame 2: center green.
	// On frame 2, pixels outside center remain blue; if frame 1 was DisposalBackground, center before frame 2 would clear.
	raw := makeAnimatedGIF(t, 80, 80,
		[]color.RGBA{{20, 60, 220, 255}, {220, 30, 30, 255}, {30, 200, 50, 255}},
		[]int{15, 15, 15},
		[]byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
	)
	anim, err := lookmedia.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(anim.Frames()) != 3 {
		t.Fatalf("frames = %d, want 3", len(anim.Frames()))
	}
	if c := anim.Frames()[1].Image().RGBAAt(40, 40); c.R < 200 {
		t.Fatalf("frame 1 center = %v, want red", c)
	}
	if c := anim.Frames()[1].Image().RGBAAt(2, 2); c.B < 200 {
		t.Fatalf("frame 1 corner = %v, want blue retained", c)
	}
	if c := anim.Frames()[2].Image().RGBAAt(40, 40); c.G < 180 {
		t.Fatalf("frame 2 center = %v, want green", c)
	}
}

func TestDecodeAnimatedWebPBlendingAndDisposal(t *testing.T) {
	raw := makeAnimatedWebP(80, 80, color.NRGBA{0, 0, 0, 0}, []testWebPFrame{
		{x: 0, y: 0, w: 80, h: 80, delayMS: 120, dispose: false, noBlend: true, col: color.NRGBA{30, 80, 220, 255}},
		{x: 20, y: 20, w: 40, h: 40, delayMS: 140, dispose: true, noBlend: false, col: color.NRGBA{220, 40, 40, 255}},
		{x: 40, y: 40, w: 40, h: 40, delayMS: 160, dispose: false, noBlend: true, col: color.NRGBA{40, 200, 60, 255}},
	})
	anim, err := lookmedia.Decode(raw)
	if err != nil {
		t.Fatalf("Decode animated WebP: %v", err)
	}
	if len(anim.Frames()) != 3 {
		t.Fatalf("frames = %d, want 3", len(anim.Frames()))
	}
	if c := anim.Frames()[0].Image().RGBAAt(10, 10); c != (color.RGBA{30, 80, 220, 255}) {
		t.Fatalf("frame 0 = %v, want blue", c)
	}
	if c := anim.Frames()[1].Image().RGBAAt(30, 30); c != (color.RGBA{220, 40, 40, 255}) {
		t.Fatalf("frame 1 center = %v, want red", c)
	}
	// Frame 1 disposed (20..60, 20..60) to transparent background; frame 2 only painted (40..80, 40..80),
	// so (25, 25) on frame 2 must be transparent!
	if c := anim.Frames()[2].Image().RGBAAt(25, 25); c.A != 0 {
		t.Fatalf("frame 2 disposed pixel (25,25) = %v, want transparent", c)
	}
	if c := anim.Frames()[2].Image().RGBAAt(60, 60); c != (color.RGBA{40, 200, 60, 255}) {
		t.Fatalf("frame 2 (60,60) = %v, want green", c)
	}
}

func TestBoundsRefusedBeforeDecodingWork(t *testing.T) {
	// 1. Crafted tiny APNG declaring 500 frames (> MaxSourceFrames) with no real IDAT work.
	writeChunk := func(buf *bytes.Buffer, tag string, data []byte) {
		var h [8]byte
		binary.BigEndian.PutUint32(h[0:4], uint32(len(data)))
		copy(h[4:8], tag)
		buf.Write(h[:])
		buf.Write(data)
		crc := crc32.NewIEEE()
		_, _ = crc.Write(h[4:8])
		_, _ = crc.Write(data)
		var tail [4]byte
		binary.BigEndian.PutUint32(tail[:], crc.Sum32())
		buf.Write(tail[:])
	}
	var bomb bytes.Buffer
	bomb.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], 128)
	binary.BigEndian.PutUint32(ihdr[4:8], 128)
	ihdr[8], ihdr[9] = 8, 6
	writeChunk(&bomb, "IHDR", ihdr[:])
	var actl [8]byte
	binary.BigEndian.PutUint32(actl[0:4], lookmedia.MaxSourceFrames+1)
	writeChunk(&bomb, "acTL", actl[:])
	writeChunk(&bomb, "IEND", nil)
	if _, err := lookmedia.Decode(bomb.Bytes()); !errors.Is(err, lookmedia.ErrTooManyFrames) {
		t.Fatalf("frame bomb: got %v, want ErrTooManyFrames", err)
	}

	// 2. Crafted tiny APNG declaring 4000x4000 with 20 frames -> 320M frame-pixels > MaxSourcePixels (200M).
	bomb.Reset()
	bomb.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	binary.BigEndian.PutUint32(ihdr[0:4], 4000)
	binary.BigEndian.PutUint32(ihdr[4:8], 4000)
	writeChunk(&bomb, "IHDR", ihdr[:])
	binary.BigEndian.PutUint32(actl[0:4], 20)
	writeChunk(&bomb, "acTL", actl[:])
	writeChunk(&bomb, "IEND", nil)
	if _, err := lookmedia.Decode(bomb.Bytes()); !errors.Is(err, lookmedia.ErrTooManyFrames) {
		t.Fatalf("frames*pixels bomb: got %v, want ErrTooManyFrames", err)
	}

	// 3. Canvas > 40M px -> ErrTooManyPixels.
	bomb.Reset()
	bomb.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	binary.BigEndian.PutUint32(ihdr[0:4], 7000)
	binary.BigEndian.PutUint32(ihdr[4:8], 7000)
	writeChunk(&bomb, "IHDR", ihdr[:])
	writeChunk(&bomb, "IEND", nil)
	if _, err := lookmedia.Decode(bomb.Bytes()); !errors.Is(err, lookmedia.ErrTooManyPixels) {
		t.Fatalf("canvas bomb: got %v, want ErrTooManyPixels", err)
	}

	// 4. Loop > 9 seconds -> ErrAnimationTooLong.
	tooLong := makeAnimatedGIF(t, 64, 64,
		[]color.RGBA{{200, 0, 0, 255}, {0, 200, 0, 255}},
		[]int{500, 500}, // 10 seconds
		nil,
	)
	if _, err := lookmedia.Decode(tooLong); !errors.Is(err, lookmedia.ErrAnimationTooLong) {
		t.Fatalf("too long GIF: got %v, want ErrAnimationTooLong", err)
	}
}

func TestNormalizeFPSFramesAndIdenticalMerging(t *testing.T) {
	// 60 distinct frames at 50 ms each = 3000 ms total.
	// Capping at 10 fps (min 100 ms) should merge pairs -> 30 frames (>= 100 ms each), total still 3000 ms.
	frames := make([]lookmedia.Frame, 60)
	for i := range 60 {
		frames[i] = lookmedia.NewFrame(solidRGBA(64, 64, color.RGBA{uint8(i * 4), 100, 150, 255}), 50)
	}
	norm, err := lookmedia.Normalize(lookmedia.NewAnimation(64, 64, frames), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(norm) > lookmedia.MaxOutputFrames {
		t.Fatalf("got %d frames, max %d", len(norm), lookmedia.MaxOutputFrames)
	}
	total := 0
	for i, f := range norm {
		if f.DelayMS() < lookmedia.MinFrameDelayMS {
			t.Errorf("frame %d delay %d < %d", i, f.DelayMS(), lookmedia.MinFrameDelayMS)
		}
		total += f.DelayMS()
	}
	if total != 3000 {
		t.Errorf("total delay = %d, want 3000", total)
	}

	// 80 distinct frames at 100 ms each = 8000 ms total -> must resample to <= 36 frames preserving 8000 ms.
	frames80 := make([]lookmedia.Frame, 80)
	for i := range 80 {
		frames80[i] = lookmedia.NewFrame(solidRGBA(64, 64, color.RGBA{uint8(i * 3), uint8(255 - i*3), 80, 255}), 100)
	}
	norm80, err := lookmedia.Normalize(lookmedia.NewAnimation(64, 64, frames80), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(norm80) != lookmedia.MaxOutputFrames {
		t.Fatalf("got %d frames, want %d", len(norm80), lookmedia.MaxOutputFrames)
	}
	total80 := 0
	for _, f := range norm80 {
		total80 += f.DelayMS()
	}
	if total80 != 8000 {
		t.Errorf("total80 = %d, want 8000", total80)
	}

	// Identical consecutive frames merge into 1 still frame.
	same := []lookmedia.Frame{
		lookmedia.NewFrame(solidRGBA(64, 64, color.RGBA{10, 20, 30, 255}), 150),
		lookmedia.NewFrame(solidRGBA(64, 64, color.RGBA{10, 20, 30, 255}), 150),
	}
	merged, err := lookmedia.Normalize(lookmedia.NewAnimation(64, 64, same), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].DelayMS() != 0 {
		t.Fatalf("identical frames did not collapse to still: len=%d delay=%d", len(merged), merged[0].DelayMS())
	}
}

func TestEncodeDotAPNGByteBudgetReducesPaletteThenFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	frames := make([]lookmedia.Frame, 36)
	for i := range 36 {
		img := image.NewRGBA(image.Rect(0, 0, lookmedia.DotSide, lookmedia.DotSide))
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p] = uint8(rng.Intn(256))
			img.Pix[p+1] = uint8(rng.Intn(256))
			img.Pix[p+2] = uint8(rng.Intn(256))
			img.Pix[p+3] = 255
		}
		frames[i] = lookmedia.NewFrame(img, 120)
	}

	// Full RGBA of 36 noisy 144x144 frames exceeds 2 MB, so EncodeDotAPNG reduces to 256 colors and stays under 2 MB keeping all 36 frames.
	b, count, fps, err := lookmedia.EncodeDotAPNG(frames, lookmedia.MaxDotFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > lookmedia.MaxDotFileBytes {
		t.Fatalf("size %d > budget %d", len(b), lookmedia.MaxDotFileBytes)
	}
	if count != 36 || fps <= 0 {
		t.Fatalf("expected 36 paletted frames under 2 MB, got count=%d fps=%d size=%d", count, fps, len(b))
	}

	// With a tight budget (120 KB), palette alone is still too big, so frame count is reduced too.
	tight, tightCount, _, err := lookmedia.EncodeDotAPNG(frames, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(tight) > 120_000 {
		t.Fatalf("tight size %d > 120000", len(tight))
	}
	if tightCount >= 36 {
		t.Fatalf("expected frame reduction under tight budget, got %d frames", tightCount)
	}
}

func TestPaletteContrastFloorsSweep(t *testing.T) {
	white := color.NRGBA{255, 255, 255, 255}
	black := color.NRGBA{0, 0, 0, 255}

	checkSeed := func(seed color.NRGBA) {
		t.Helper()
		pal := lookmedia.Palette(seed, lookmedia.DefaultSecondary(seed))
		for _, pair := range []struct {
			name   string
			scheme lookmedia.ColorScheme
			page   color.NRGBA
		}{
			{"light", pal.Light(), white},
			{"dark", pal.Dark(), black},
		} {
			acc, _, err := lookmedia.ParseHexColor(pair.scheme.Accent())
			if err != nil {
				t.Fatalf("seed %v %s bad accent %q: %v", seed, pair.name, pair.scheme.Accent(), err)
			}
			bub, _, err := lookmedia.ParseHexColor(pair.scheme.Bubble())
			if err != nil {
				t.Fatalf("seed %v %s bad bubble %q: %v", seed, pair.name, pair.scheme.Bubble(), err)
			}
			btx, _, err := lookmedia.ParseHexColor(pair.scheme.BubbleText())
			if err != nil {
				t.Fatalf("seed %v %s bad bubble_text %q: %v", seed, pair.name, pair.scheme.BubbleText(), err)
			}
			if c := lookmedia.Contrast(white, acc); c < lookmedia.FloorOnAccent {
				t.Errorf("seed %v %s white on accent %s = %.2f < %.1f", seed, pair.name, pair.scheme.Accent(), c, lookmedia.FloorOnAccent)
			}
			if c := lookmedia.Contrast(acc, pair.page); c < lookmedia.FloorLink {
				t.Errorf("seed %v %s accent %s on page = %.2f < %.1f", seed, pair.name, pair.scheme.Accent(), c, lookmedia.FloorLink)
			}
			if c := lookmedia.Contrast(bub, btx); c < lookmedia.FloorBubble {
				t.Errorf("seed %v %s bubble %s text = %.2f < %.1f", seed, pair.name, pair.scheme.Bubble(), c, lookmedia.FloorBubble)
			}
		}
	}

	// Explicit edge seeds: grays, pure/near white, pure/near black, yellows, blues.
	edgeSeeds := []color.NRGBA{
		{0, 0, 0, 255}, {5, 5, 5, 255}, {255, 255, 255, 255}, {252, 252, 250, 255},
		{120, 120, 120, 255}, {130, 128, 126, 255},
		{255, 255, 0, 255}, {242, 194, 48, 255}, {0, 0, 255, 255}, {0, 0, 60, 255},
		{0, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255}, {255, 0, 0, 255},
	}
	for _, s := range edgeSeeds {
		checkSeed(s)
	}

	// Verify near-gray seeds stay neutral gray (R == G == B) instead of turning red.
	for _, gray := range []color.NRGBA{{120, 120, 120, 255}, {128, 126, 125, 255}, {0, 0, 0, 255}, {255, 255, 255, 255}} {
		pal := lookmedia.Palette(gray, lookmedia.DefaultSecondary(gray))
		for _, hex := range []string{pal.Light().Accent(), pal.Light().Bubble(), pal.Dark().Accent(), pal.Dark().Bubble()} {
			c, _, _ := lookmedia.ParseHexColor(hex)
			if c.R != c.G || c.G != c.B {
				t.Errorf("gray seed %v turned chromatic: %s (%v)", gray, hex, c)
			}
		}
	}

	// Sweep hues (0..345 step 15) x lightness (0.05..0.95 step 0.1) x saturation (0.0..1.0 step 0.25).
	for hDeg := 0; hDeg < 360; hDeg += 15 {
		for li := 1; li <= 19; li += 2 {
			l := float64(li) / 20.0
			for si := 0; si <= 4; si++ {
				s := float64(si) / 4.0
				checkSeed(hslToNRGBA(float64(hDeg)/360.0, l, s))
			}
		}
	}
}

func hslToNRGBA(h, l, s float64) color.NRGBA {
	if s == 0 {
		v := uint8(math.Round(l * 255))
		return color.NRGBA{v, v, v, 255}
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	ch := func(t float64) uint8 {
		for t < 0 {
			t++
		}
		for t > 1 {
			t--
		}
		var v float64
		switch {
		case t < 1.0/6.0:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3.0:
			v = p + (q-p)*(2.0/3.0-t)*6
		default:
			v = p
		}
		return uint8(math.Round(v * 255))
	}
	return color.NRGBA{ch(h + 1.0/3.0), ch(h), ch(h - 1.0/3.0), 255}
}

func TestExtractAutoSeedInline(t *testing.T) {
	// Mirrors the five synthetic cases in spec/fixtures/look-colors/cases.json so auto-seed extraction is always
	// verified even on branches checked out before the contract PR merges.
	makeImg := func(fn func(x, y int) color.NRGBA) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		for y := range 16 {
			for x := range 16 {
				img.SetNRGBA(x, y, fn(x, y))
			}
		}
		return img
	}

	cases := []struct {
		name string
		img  image.Image
		want string
	}{
		{
			name: "solid-blue",
			img:  makeImg(func(_, _ int) color.NRGBA { return color.NRGBA{58, 123, 213, 255} }),
			want: "#3A7BD5",
		},
		{
			name: "two-hues-lower-middle-median",
			img: makeImg(func(_, y int) color.NRGBA {
				switch {
				case y < 5:
					return color.NRGBA{230, 140, 40, 255}
				case y < 10:
					return color.NRGBA{250, 160, 50, 255}
				default:
					return color.NRGBA{50, 180, 100, 255}
				}
			}),
			want: "#E68C28",
		},
		{
			name: "transparent-and-dark-filtered",
			img: makeImg(func(_, y int) color.NRGBA {
				switch {
				case y < 6:
					return color.NRGBA{255, 0, 0, 120}
				case y < 10:
					return color.NRGBA{40, 10, 10, 255}
				case y < 12:
					return color.NRGBA{200, 190, 185, 255}
				default:
					return color.NRGBA{60, 180, 90, 255}
				}
			}),
			want: "#3CB45A",
		},
		{
			name: "gray-fallback",
			img: makeImg(func(x, y int) color.NRGBA {
				if y*16+x < 30 {
					return color.NRGBA{220, 60, 60, 255}
				}
				return color.NRGBA{160, 160, 160, 255}
			}),
			want: "#787878",
		},
		{
			name: "hue-wrap",
			img: makeImg(func(_, y int) color.NRGBA {
				if y < 6 {
					return color.NRGBA{220, 30, 50, 255}
				}
				return color.NRGBA{220, 50, 30, 255}
			}),
			want: "#DC321E",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := lookmedia.ExtractAutoSeed(tc.img)
			if got := lookmedia.FormatHexColor(p); got != tc.want {
				t.Fatalf("ExtractAutoSeed = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestExtractAutoSeedMatchesSpecFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "spec", "fixtures", "look-colors")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if os.Getenv("CI") != "" {
			t.Fatalf("spec fixture dir %s missing in CI", dir)
		}
		t.Skipf("spec/fixtures/look-colors not present on this branch yet (lands in contract PR)")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err != nil {
		t.Fatalf("read look-colors cases.json: %v", err)
	}
	var spec struct {
		Version int `json:"version"`
		Cases   []struct {
			Name         string `json:"name"`
			File         string `json:"file"`
			ExpectedSeed string `json:"expected_seed"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Cases) < 3 {
		t.Fatalf("expected >= 3 cases, got %d", len(spec.Cases))
	}
	for _, tc := range spec.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, tc.File))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			img, err := png.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := lookmedia.ExtractAutoSeed(img)
			if got := lookmedia.FormatHexColor(p); got != tc.ExpectedSeed {
				t.Fatalf("ExtractAutoSeed(%s) = %s, want %s", tc.File, got, tc.ExpectedSeed)
			}
		})
	}
}
