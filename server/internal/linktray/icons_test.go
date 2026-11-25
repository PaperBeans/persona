package linktray

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestEveryGlyphDraws(t *testing.T) {
	for name := range Glyphs {
		img, err := renderGlyph(name, 24, ToneColors[ToneInfo][0], 2)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ink(img) < 8 {
			t.Errorf("%s draws almost nothing (%d pixels)", name, ink(img))
		}
	}
}

func TestRenderIconEveryLookAndSize(t *testing.T) {
	out := os.Getenv("LINKTRAY_ICON_SHEET") // a directory: write the icons there to look at them
	for _, look := range Looks {
		for _, size := range []int{16, 20, 24, 32, 48} {
			for _, dark := range []bool{false, true} {
				img, err := RenderIcon(look, size, dark)
				if err != nil {
					t.Fatalf("%s %d: %v", look, size, err)
				}
				if img.Bounds().Dx() != size || ink(img) < size {
					t.Errorf("%s %d dark=%v: %d pixels", look, size, dark, ink(img))
				}
				if out != "" && size == 48 {
					name := string(look) + map[bool]string{false: "-light", true: "-dark"}[dark] + ".png"
					f, _ := os.Create(filepath.Join(out, name))
					_ = png.Encode(f, img)
					f.Close()
				}
			}
		}
	}
	// The badge is the tone's color: a look that needs the user is not just a drawing.
	img, _ := RenderIcon(LookDown, 32, true)
	c := img.NRGBAAt(31-6, 31-6)
	if want := ToneColors[ToneDanger][1]; c.R != want.R || c.G != want.G || c.B != want.B {
		t.Fatalf("badge %v, want %v", c, want)
	}
}

func ink(img *image.NRGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 64 {
			n++
		}
	}
	return n
}

// TestIconSheet writes every look at 16, 24 and 32 px on a light and a dark taskbar, magnified, to look at.
func TestIconSheet(t *testing.T) {
	out := os.Getenv("LINKTRAY_ICON_SHEET")
	if out == "" {
		t.Skip("LINKTRAY_ICON_SHEET not set")
	}
	const zoom, cell = 4, 40
	sizes := []int{16, 24, 32}
	sheet := image.NewNRGBA(image.Rect(0, 0, len(Looks)*cell*zoom, 2*len(sizes)*cell*zoom))
	for row, dark := range []bool{false, true} {
		bg := [3]uint8{0xF3, 0xF3, 0xF3}
		if dark {
			bg = [3]uint8{0x20, 0x20, 0x20}
		}
		for si, size := range sizes {
			y0 := (row*len(sizes) + si) * cell
			for x := 0; x < sheet.Bounds().Dx(); x++ {
				for y := y0 * zoom; y < (y0+cell)*zoom; y++ {
					sheet.Pix[sheet.PixOffset(x, y)+0], sheet.Pix[sheet.PixOffset(x, y)+1], sheet.Pix[sheet.PixOffset(x, y)+2], sheet.Pix[sheet.PixOffset(x, y)+3] = bg[0], bg[1], bg[2], 255
				}
			}
			for li, look := range Looks {
				img, _ := RenderIcon(look, size, dark)
				ox, oy := li*cell+(cell-size)/2, y0+(cell-size)/2
				for y := 0; y < size; y++ {
					for x := 0; x < size; x++ {
						c := img.NRGBAAt(x, y)
						a := uint32(c.A)
						for zy := 0; zy < zoom; zy++ {
							for zx := 0; zx < zoom; zx++ {
								i := sheet.PixOffset((ox+x)*zoom+zx, (oy+y)*zoom+zy)
								for k, v := range []uint8{c.R, c.G, c.B} {
									sheet.Pix[i+k] = uint8((uint32(v)*a + uint32(sheet.Pix[i+k])*(255-a)) / 255)
								}
							}
						}
					}
				}
			}
		}
	}
	f, err := os.Create(filepath.Join(out, "sheet.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
}
