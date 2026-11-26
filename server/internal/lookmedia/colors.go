package lookmedia

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Contrast floors shared with scripts/assets/make_persona.py check.
const (
	FloorOnAccent = 3.5
	FloorLink     = 3.0
	FloorBubble   = 4.5
)

// ColorScheme is one appearance's colors entry in persona.json.
type ColorScheme struct {
	primary    string
	secondary  string
	accent     string
	bubble     string
	bubbleText string
}

// Primary returns the scheme's primary #RRGGBB color.
func (s ColorScheme) Primary() string { return s.primary }

// Secondary returns the scheme's secondary #RRGGBB color.
func (s ColorScheme) Secondary() string { return s.secondary }

// Accent returns the scheme's accent #RRGGBB color.
func (s ColorScheme) Accent() string { return s.accent }

// Bubble returns the scheme's user-bubble #RRGGBB color.
func (s ColorScheme) Bubble() string { return s.bubble }

// BubbleText returns the scheme's user-bubble text #RRGGBB color.
func (s ColorScheme) BubbleText() string { return s.bubbleText }

// MarshalJSON encodes ColorScheme in persona.json's wire shape.
func (s ColorScheme) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Primary    string `json:"primary"`
		Secondary  string `json:"secondary"`
		Accent     string `json:"accent"`
		Bubble     string `json:"bubble"`
		BubbleText string `json:"bubble_text"`
	}{s.primary, s.secondary, s.accent, s.bubble, s.bubbleText})
}

// UnmarshalJSON decodes ColorScheme from persona.json's wire shape.
func (s *ColorScheme) UnmarshalJSON(raw []byte) error {
	var w struct {
		Primary    string `json:"primary"`
		Secondary  string `json:"secondary"`
		Accent     string `json:"accent"`
		Bubble     string `json:"bubble"`
		BubbleText string `json:"bubble_text"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	s.primary, s.secondary, s.accent, s.bubble, s.bubbleText = w.Primary, w.Secondary, w.Accent, w.Bubble, w.BubbleText
	return nil
}

// LookColors is persona.json's colors block.
type LookColors struct {
	light ColorScheme
	dark  ColorScheme
}

// Light returns the light appearance ColorScheme.
func (c LookColors) Light() ColorScheme { return c.light }

// Dark returns the dark appearance ColorScheme.
func (c LookColors) Dark() ColorScheme { return c.dark }

// MarshalJSON encodes LookColors in persona.json's wire shape.
func (c LookColors) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Light ColorScheme `json:"light"`
		Dark  ColorScheme `json:"dark"`
	}{c.light, c.dark})
}

// UnmarshalJSON decodes LookColors from persona.json's wire shape.
func (c *LookColors) UnmarshalJSON(raw []byte) error {
	var w struct {
		Light ColorScheme `json:"light"`
		Dark  ColorScheme `json:"dark"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	c.light, c.dark = w.Light, w.Dark
	return nil
}

// ErrBadColor reports a color string that is neither "auto" nor "#RRGGBB".
var ErrBadColor = errors.New("invalid color")

// ParseHexColor parses a strict "#RRGGBB" color and returns it as NRGBA and normalized uppercase "#RRGGBB".
func ParseHexColor(s string) (color.NRGBA, string, error) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{}, "", ErrBadColor
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.NRGBA{}, "", ErrBadColor
	}
	c := color.NRGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
		A: 255,
	}
	return c, FormatHexColor(c), nil
}

// FormatHexColor formats c as uppercase "#RRGGBB".
func FormatHexColor(c color.NRGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

type keptPixel struct {
	r, g, b uint8
	hbin    int
}

// ExtractAutoSeed extracts the dominant primary and secondary colors from img using the exact method specified in
// spec/fixtures/look-colors/README.md (make_persona.py's extract_colors with integer channel math and lower-middle
// median, plus the #787878 gray fallback when fewer than 50 pixels qualify).
func ExtractAutoSeed(img image.Image) (primary, secondary color.NRGBA) {
	b := img.Bounds()
	kept := make([]keptPixel, 0, min(b.Dx()*b.Dy(), 4096))
	var counts [36]int

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			nc := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if nc.A <= 200 {
				continue
			}
			r, g, bl := int(nc.R), int(nc.G), int(nc.B)
			mx := max(r, max(g, bl))
			mn := min(r, min(g, bl))
			d := mx - mn
			// Identical to mx/255.0 > 0.35 and d/mx > 0.3 for all 8-bit integers.
			if mx < 90 || 10*d <= 3*mx {
				continue
			}
			var num int
			switch mx {
			case r:
				num = g - bl
				if num < 0 {
					num += 6 * d
				}
			case g:
				num = (bl - r) + 2*d
			default:
				num = (r - g) + 4*d
			}
			hb := (6 * num / d) % 36
			kept = append(kept, keptPixel{r: nc.R, g: nc.G, b: nc.B, hbin: hb})
			counts[hb]++
		}
	}

	if len(kept) < 50 {
		return color.NRGBA{120, 120, 120, 255}, color.NRGBA{90, 90, 90, 255}
	}

	peak := 0
	for i := 1; i < 36; i++ {
		if counts[i] > counts[peak] {
			peak = i
		}
	}

	medianOfBin := func(bin int) color.NRGBA {
		prevB := (bin + 35) % 36
		nextB := (bin + 1) % 36
		var rs, gs, bs []uint8
		for _, px := range kept {
			if px.hbin == prevB || px.hbin == bin || px.hbin == nextB {
				rs = append(rs, px.r)
				gs = append(gs, px.g)
				bs = append(bs, px.b)
			}
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
		sort.Slice(gs, func(i, j int) bool { return gs[i] < gs[j] })
		sort.Slice(bs, func(i, j int) bool { return bs[i] < bs[j] })
		idx := (len(rs) - 1) / 2
		return color.NRGBA{R: rs[idx], G: gs[idx], B: bs[idx], A: 255}
	}

	primary = medianOfBin(peak)

	// Secondary: the most frequent bin at least 4 bins (40 deg) away with >= 0.4% of kept pixels.
	minFarCount := 0.004 * float64(len(kept))
	farBin := -1
	for i := 0; i < 36; i++ {
		dist := min((i-peak+36)%36, (peak-i+36)%36)
		if dist >= 4 && float64(counts[i]) >= minFarCount {
			if farBin < 0 || counts[i] > counts[farBin] {
				farBin = i
			}
		}
	}
	if farBin >= 0 {
		secondary = medianOfBin(farBin)
	} else {
		secondary = DefaultSecondary(primary)
	}
	return primary, secondary
}

// DefaultSecondary derives a secondary color from primary when none was extracted.
func DefaultSecondary(primary color.NRGBA) color.NRGBA {
	if isNearGray(primary) {
		return color.NRGBA{90, 90, 90, 255}
	}
	return mixNRGBA(primary, color.NRGBA{60, 120, 200, 255}, 0.7)
}

// Palette builds persona.json's light and dark color schemes from primary and secondary, clearing all contrast floors
// (white on accent >= 3.5:1, accent vs page >= 3.0:1, bubble text >= 4.5:1). Near-gray seeds stay neutral gray.
func Palette(primary, secondary color.NRGBA) LookColors {
	white := color.NRGBA{255, 255, 255, 255}
	black := color.NRGBA{0, 0, 0, 255}
	al := Shade(primary, 4.55, FloorLink+0.05, white)
	ad := Shade(primary, FloorOnAccent+0.15, FloorLink+0.05, black)
	bl := Shade(al, FloorBubble+0.25, 0, white)
	bd := Shade(ad, FloorBubble+0.15, 0, black)
	return LookColors{
		light: ColorScheme{
			primary:    FormatHexColor(primary),
			secondary:  FormatHexColor(secondary),
			accent:     FormatHexColor(al),
			bubble:     FormatHexColor(bl),
			bubbleText: "#FFFFFF",
		},
		dark: ColorScheme{
			primary:    FormatHexColor(primary),
			secondary:  FormatHexColor(secondary),
			accent:     FormatHexColor(ad),
			bubble:     FormatHexColor(bd),
			bubbleText: "#FFFFFF",
		},
	}
}

// Shade adjusts c's lightness along its hue (preserving neutral gray when c is near-gray) to find the closest color
// meeting contrast(white, out) >= minOnWhite and (when minOnPage > 0) contrast(out, page) >= minOnPage.
func Shade(c color.NRGBA, minOnWhite, minOnPage float64, page color.NRGBA) color.NRGBA {
	white := color.NRGBA{255, 255, 255, 255}
	h, l, s := rgbToHLS(c)
	if isNearGray(c) || s < 0.08 {
		s = 0
	} else if s < 0.65 {
		s = 0.65
	}
	ok := func(cand color.NRGBA) bool {
		if Contrast(white, cand) < minOnWhite {
			return false
		}
		if minOnPage > 0 && Contrast(cand, page) < minOnPage {
			return false
		}
		return true
	}
	// First search downward from l toward 0 (lightest qualifying color <= seed lightness, matching make_persona.py).
	for i := 0; i <= 200; i++ {
		L := l * (1.0 - float64(i)/200.0)
		cand := hlsToRGB(h, L, s)
		if ok(cand) {
			return cand
		}
	}
	// If the seed is too dark against a dark page, search upward from l toward 1.
	for i := 1; i <= 200; i++ {
		L := l + (1.0-l)*(float64(i)/200.0)
		cand := hlsToRGB(h, L, s)
		if ok(cand) {
			return cand
		}
	}
	// Safe neutral fallback clearing all floors on both white and black pages.
	return color.NRGBA{110, 110, 110, 255}
}

func isNearGray(c color.NRGBA) bool {
	mx := max(int(c.R), max(int(c.G), int(c.B)))
	mn := min(int(c.R), min(int(c.G), int(c.B)))
	if mx-mn < 8 {
		return true
	}
	_, _, s := rgbToHLS(c)
	return s < 0.08
}

// Luminance computes WCAG 2.x relative luminance of c.
func Luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255.0
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// Contrast computes WCAG 2.x contrast ratio between a and b.
func Contrast(a, b color.NRGBA) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func mixNRGBA(a, b color.NRGBA, t float64) color.NRGBA {
	ch := func(x, y uint8) uint8 {
		v := math.Round(float64(x)*(1-t) + float64(y)*t)
		return uint8(max(0, min(255, int(v))))
	}
	return color.NRGBA{R: ch(a.R, b.R), G: ch(a.G, b.G), B: ch(a.B, b.B), A: 255}
}

// rgbToHLS matches Python's colorsys.rgb_to_hls.
func rgbToHLS(c color.NRGBA) (h, l, s float64) {
	r, g, b := float64(c.R)/255.0, float64(c.G)/255.0, float64(c.B)/255.0
	maxc := max(r, max(g, b))
	minc := min(r, min(g, b))
	sumc := maxc + minc
	rangec := maxc - minc
	l = sumc / 2.0
	if minc == maxc {
		return 0, l, 0
	}
	if l <= 0.5 {
		s = rangec / sumc
	} else {
		s = rangec / (2.0 - sumc)
	}
	rc := (maxc - r) / rangec
	gc := (maxc - g) / rangec
	bc := (maxc - b) / rangec
	switch {
	case r == maxc:
		h = bc - gc
	case g == maxc:
		h = 2.0 + rc - bc
	default:
		h = 4.0 + gc - rc
	}
	h = math.Mod(h/6.0, 1.0)
	if h < 0 {
		h += 1.0
	}
	return h, l, s
}

// hlsToRGB matches Python's colorsys.hls_to_rgb, rounded to 8-bit NRGBA.
func hlsToRGB(h, l, s float64) color.NRGBA {
	toByte := func(v float64) uint8 {
		n := int(math.Round(v * 255.0))
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return uint8(n)
	}
	if s == 0 {
		v := toByte(l)
		return color.NRGBA{R: v, G: v, B: v, A: 255}
	}
	var m2 float64
	if l <= 0.5 {
		m2 = l * (1.0 + s)
	} else {
		m2 = l + s - l*s
	}
	m1 := 2.0*l - m2
	v := func(hue float64) float64 {
		hue = math.Mod(hue, 1.0)
		if hue < 0 {
			hue += 1.0
		}
		switch {
		case hue < 1.0/6.0:
			return m1 + (m2-m1)*hue*6.0
		case hue < 0.5:
			return m2
		case hue < 2.0/3.0:
			return m1 + (m2-m1)*(2.0/3.0-hue)*6.0
		default:
			return m1
		}
	}
	return color.NRGBA{
		R: toByte(v(h + 1.0/3.0)),
		G: toByte(v(h)),
		B: toByte(v(h - 1.0/3.0)),
		A: 255,
	}
}
