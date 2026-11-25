package linktray

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

// Glyphs are the panel's and pairing window's icons: 24×24 line drawings, stroked in the current color with round
// ends. The tray icon itself is the Persona helper (RenderIcon). Drawn for this
// tray, one per meaning; add one here before using a new name.
var Glyphs = map[string]string{
	"hotspot":      "M12 10.5a1.5 1.5 0 1 0 0 3a1.5 1.5 0 1 0 0-3z M8.5 8.5a5 5 0 0 0 0 7 M15.5 8.5a5 5 0 0 1 0 7 M5.6 5.6a9 9 0 0 0 0 12.8 M18.4 5.6a9 9 0 0 1 0 12.8",
	"offline":      "M12 10.5a1.5 1.5 0 1 0 0 3a1.5 1.5 0 1 0 0-3z M5.6 5.6a9 9 0 0 0 0 12.8 M18.4 5.6a9 9 0 0 1 0 12.8 M4 4l16 16",
	"antenna":      "M12 8.5a1.5 1.5 0 1 0 0 3a1.5 1.5 0 1 0 0-3z M12 11.5V21 M8.5 6.5a5 5 0 0 0 0 7 M15.5 6.5a5 5 0 0 1 0 7 M5.6 3.6a9 9 0 0 0 0 12.8 M18.4 3.6a9 9 0 0 1 0 12.8",
	"hourglass":    "M5.5 3h13 M5.5 21h13 M7 3v2.5c0 2.5 5 4.5 5 6.5s-5 4-5 6.5V21 M17 3v2.5c0 2.5-5 4.5-5 6.5s5 4 5 6.5V21",
	"sync":         "M20 12a8 8 0 0 1-13.7 5.6 M4 12a8 8 0 0 1 13.7-5.6 M17.7 3v3.6h-3.6 M6.3 21v-3.6h3.6",
	"pencil":       "M14.5 5.5l4 4 M4 20l1-4.5L15.8 4.7a1.4 1.4 0 0 1 2 0l1.5 1.5a1.4 1.4 0 0 1 0 2L8.5 19z",
	"link":         "M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1 M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1",
	"link_plus":    "M9 13a3.6 3.6 0 0 0 5.1 0l2.7-2.7a3.6 3.6 0 0 0-5.1-5.1l-.9.9 M12.6 9.4a3.6 3.6 0 0 0-5.1 0l-2.7 2.7a3.6 3.6 0 0 0 5.1 5.1l.9-.9 M18.5 15v6 M15.5 18h6",
	"check":        "M5 12.5l4.5 4.5L19 7.5",
	"check_filled": "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18z M8 12.5l3 3l5-6",
	"x":            "M7 7l10 10 M17 7L7 17",
	"close":        "M6 6l12 12 M18 6L6 18",
	"info":         "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18z M12 11v5.5 M12 7.75v.01",
	"warning":      "M12 3.5L2.5 20h19z M12 10v4.5 M12 17.25v.01",
	"octagon":      "M8.3 3h7.4L21 8.3v7.4L15.7 21H8.3L3 15.7V8.3z M12 7.5v6 M12 16.75v.01",
	"clock":        "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18z M12 7v5l3.5 2",
	"network":      "M12 3a2 2 0 1 0 0 4a2 2 0 1 0 0-4z M5 17a2 2 0 1 0 0 4a2 2 0 1 0 0-4z M19 17a2 2 0 1 0 0 4a2 2 0 1 0 0-4z M11 6.8L6 17.2 M13 6.8l5 10.4 M7 19h10",
	"shield":       "M12 3l7.5 3v5.5c0 4.5-3.2 8.2-7.5 9.5c-4.3-1.3-7.5-5-7.5-9.5V6z M9 12l2 2l4-4",
	"server":       "M4 4h16v6H4z M4 14h16v6H4z M7.5 7v.01 M7.5 17v.01",
	"laptop":       "M5 5h14v10H5z M2.5 19h19",
	"phone":        "M8 2.5h8a1.5 1.5 0 0 1 1.5 1.5v16a1.5 1.5 0 0 1-1.5 1.5H8a1.5 1.5 0 0 1-1.5-1.5V4A1.5 1.5 0 0 1 8 2.5z M11 18.5h2",
	"ellipsis":     "M6 12v.01 M12 12v.01 M18 12v.01",
	"lock_off":     "M6 11h12v10H6z M8.5 11V8a3.5 3.5 0 0 1 6.6-1.6",
	"gear":         "M4 7h10 M18 7h2 M4 17h2 M10 17h10 M16 5a2 2 0 1 0 0 4a2 2 0 1 0 0-4z M8 15a2 2 0 1 0 0 4a2 2 0 1 0 0-4z",
	"arrow_out":    "M7 17L17 7 M9 7h8v8",
	"arrow_in":     "M17 7L7 17 M15 17H7V9",
	"refresh":      "M20 12a8 8 0 1 1-2.3-5.6 M20 4v4.5h-4.5",
	"restart":      "M4 12a8 8 0 1 0 2.3-5.6 M4 4v4.5h4.5",
	"download":     "M12 4v11 M7.5 10.5L12 15l4.5-4.5 M5 20h14",
	"chevron":      "M9.5 6l6 6-6 6",
	"sign_in":      "M14 4h4.5A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5H14 M4 12h11 M11 8l4 4-4 4",
	"copy":         "M9 8h10.5v12.5H9z M5 16V4.5A1.5 1.5 0 0 1 6.5 3H15",
	"folder":       "M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2.5h8.5A1.5 1.5 0 0 1 21 9v9.5a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18.5z",
	"power":        "M12 3v8 M7 6a7.5 7.5 0 1 0 10 0",
}

// iconOf is how the tray icon shows a look on the Persona helper (Mac: LinkLook's menu bar image): faded while the
// link is not up (connecting, down, not paired), and a colored badge on the states that need the user, so they stand
// out in a row of icons.
func iconOf(l Look) (faded, badge bool) {
	switch l {
	case LookConnected, LookListening:
		return false, false
	case LookAwaitingConfirm, LookNodeMissing:
		return false, true
	case LookConnecting:
		return true, false
	case LookDown:
		return true, true
	}
	return true, false
}

// helperArt are the Persona helper at the notification area's sizes, made by scripts/assets/make_icon.py link from
// the app icon (the same art as the desktop app's tray).
//
//go:embed art/helper-*.png
var helperArt embed.FS

var helperSizes = []int{16, 20, 24, 32, 40, 48}

// helper is the helper at size×size: the drawing made for that size, else the next larger one scaled down.
func helper(size int) (*image.NRGBA, error) {
	from := helperSizes[len(helperSizes)-1]
	for _, s := range helperSizes {
		if s >= size {
			from = s
			break
		}
	}
	f, err := helperArt.Open(fmt.Sprintf("art/helper-%d.png", from))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(out, out.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return out, nil
}

// ToneColors are the badge's colors, the system's own status colors for a light and a dark taskbar.
var ToneColors = map[string][2]color.NRGBA{
	TonePositive: {{0x0F, 0x7B, 0x0F, 0xFF}, {0x6C, 0xCB, 0x5F, 0xFF}},
	ToneCaution:  {{0x9D, 0x5D, 0x00, 0xFF}, {0xFC, 0xE1, 0x00, 0xFF}},
	ToneInfo:     {{0x00, 0x5F, 0xB8, 0xFF}, {0x60, 0xCD, 0xFF, 0xFF}},
	ToneDanger:   {{0xC4, 0x2B, 0x1C, 0xFF}, {0xFF, 0x99, 0xA4, 0xFF}},
}

// RenderIcon draws the tray icon for look at size×size pixels. The helper is in color on either taskbar; the badge
// takes the light or dark taskbar's shade of its tone.
func RenderIcon(look Look, size int, darkTaskbar bool) (*image.NRGBA, error) {
	faded, badge := iconOf(look)
	img, err := helper(size)
	if err != nil {
		return nil, err
	}
	if faded {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = uint8(uint32(img.Pix[i]) * fadedAlpha / 255)
		}
	}
	if badge {
		theme := 0
		if darkTaskbar {
			theme = 1
		}
		drawBadge(img, ToneColors[look.Tone()][theme])
	}
	return img, nil
}

// fadedAlpha is how much of the helper shows while the link is not up (of 255).
const fadedAlpha = 110

// iconStroke is the line width in the 24-unit drawing: one pixel at 16 px (100 %), as the system's own tray icons
// draw, growing with the scale; never under one pixel.
func iconStroke(size int) float64 {
	return math.Max(1.5, 24/float64(size))
}

// renderGlyph strokes the named glyph in ink.
func renderGlyph(name string, size int, ink color.NRGBA, stroke float64) (*image.NRGBA, error) {
	d, ok := Glyphs[name]
	if !ok {
		return nil, fmt.Errorf("no glyph %q", name)
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
		`<path d="%s" fill="none" stroke="#%02x%02x%02x" stroke-width="%.2f" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
		d, ink.R, ink.G, ink.B, stroke)
	icon, err := oksvg.ReadIconStream(strings.NewReader(svg))
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	rgba := image.NewRGBA(image.Rect(0, 0, size, size))
	icon.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, rgba, rgba.Bounds())), 1)
	out := image.NewNRGBA(rgba.Bounds())
	for i := 0; i < len(rgba.Pix); i += 4 {
		a := rgba.Pix[i+3]
		if a == 0 {
			continue
		}
		// rasterx draws premultiplied; icons want straight alpha.
		out.Pix[i] = uint8(uint32(rgba.Pix[i]) * 255 / uint32(a))
		out.Pix[i+1] = uint8(uint32(rgba.Pix[i+1]) * 255 / uint32(a))
		out.Pix[i+2] = uint8(uint32(rgba.Pix[i+2]) * 255 / uint32(a))
		out.Pix[i+3] = a
	}
	return out, nil
}

// drawBadge puts a dot in the bottom-right corner, with a ring cut out of the drawing around it so it reads on any
// taskbar.
func drawBadge(img *image.NRGBA, c color.NRGBA) {
	size := float64(img.Bounds().Dx())
	r := size * 0.18
	cx, cy := size-r-0.5, size-r-0.5
	gap := math.Max(1, size*0.07)
	const ss = 4 // supersampling per axis
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			var inDot, inRing int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px, py := float64(x)+(float64(sx)+0.5)/ss, float64(y)+(float64(sy)+0.5)/ss
					d := math.Hypot(px-cx, py-cy)
					if d <= r {
						inDot++
					}
					if d <= r+gap {
						inRing++
					}
				}
			}
			if inRing == 0 {
				continue
			}
			i := img.PixOffset(x, y)
			// Clear the ring, then lay the dot over it.
			keep := 1 - float64(inRing)/(ss*ss)
			a := float64(img.Pix[i+3]) * keep
			dot := float64(inDot) / (ss * ss)
			outA := dot*255 + a*(1-dot)
			if outA == 0 {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 0, 0
				continue
			}
			mix := func(dotC, under uint8) uint8 {
				return uint8((float64(dotC)*dot*255 + float64(under)*a*(1-dot)) / outA)
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = mix(c.R, img.Pix[i]), mix(c.G, img.Pix[i+1]), mix(c.B, img.Pix[i+2])
			img.Pix[i+3] = uint8(math.Round(outA))
		}
	}
}
