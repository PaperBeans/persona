package lookmedia

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/gif"
)

// decodeGIF pre-scans the GIF container for dimensions, frame count, and total duration before invoking image/gif.DecodeAll,
// then composites every frame onto a full RGBA canvas honoring GIF disposal methods.
func decodeGIF(raw []byte) (*Animation, error) {
	w, h, nFrames, totalDelayMS, err := prescanGIF(raw)
	if err != nil {
		return nil, err
	}
	if err := checkCanvasBounds(w, h); err != nil {
		return nil, err
	}
	if err := checkSourceFrameBudget(nFrames, w, h); err != nil {
		return nil, err
	}
	if nFrames > 1 && totalDelayMS > MaxLoopMS {
		return nil, ErrAnimationTooLong
	}

	g, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil || len(g.Image) == 0 {
		return nil, ErrNotImage
	}
	if g.Config.Width > 0 && g.Config.Height > 0 {
		w, h = g.Config.Width, g.Config.Height
	} else {
		b := g.Image[0].Bounds()
		w, h = b.Max.X, b.Max.Y
	}
	if err := checkCanvasBounds(w, h); err != nil {
		return nil, err
	}
	if err := checkSourceFrameBudget(len(g.Image), w, h); err != nil {
		return nil, err
	}

	if len(g.Image) == 1 {
		canvas := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(canvas, g.Image[0].Bounds(), g.Image[0], g.Image[0].Bounds().Min, draw.Over)
		return &Animation{width: w, height: h, frames: []Frame{{img: canvas, delayMS: 0}}}, nil
	}

	var sumMS int
	delays := make([]int, len(g.Image))
	for i := range g.Image {
		d := 0
		if i < len(g.Delay) {
			d = g.Delay[i] * 10
		}
		if d <= 0 {
			d = DefaultFrameDelay
		}
		delays[i] = d
		sumMS += d
	}
	if sumMS > MaxLoopMS {
		return nil, ErrAnimationTooLong
	}

	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	prev := image.NewRGBA(image.Rect(0, 0, w, h))
	frames := make([]Frame, 0, len(g.Image))

	for i, src := range g.Image {
		var disp byte
		if i < len(g.Disposal) {
			disp = g.Disposal[i]
		}
		if disp == gif.DisposalPrevious {
			copy(prev.Pix, canvas.Pix)
		}
		b := src.Bounds().Intersect(canvas.Bounds())
		if !b.Empty() {
			draw.Draw(canvas, b, src, b.Min, draw.Over)
		}
		frames = append(frames, Frame{
			img:     cloneRGBA(canvas),
			delayMS: delays[i],
		})
		switch disp {
		case gif.DisposalBackground:
			if !b.Empty() {
				draw.Draw(canvas, b, image.Transparent, image.Point{}, draw.Src)
			}
		case gif.DisposalPrevious:
			copy(canvas.Pix, prev.Pix)
		}
	}
	return &Animation{width: w, height: h, frames: frames}, nil
}

// prescanGIF walks the GIF block structure without decompressing LZW streams so oversized or frame-bomb files are
// rejected immediately.
func prescanGIF(raw []byte) (w, h, nFrames, totalDelayMS int, err error) {
	if len(raw) < 13 {
		return 0, 0, 0, 0, ErrNotImage
	}
	w = int(binary.LittleEndian.Uint16(raw[6:8]))
	h = int(binary.LittleEndian.Uint16(raw[8:10]))
	packed := raw[10]
	pos := 13
	if packed&0x80 != 0 {
		gctSize := 3 * (1 << ((packed & 0x07) + 1))
		if pos+gctSize > len(raw) {
			return 0, 0, 0, 0, ErrNotImage
		}
		pos += gctSize
	}
	pendingDelay := 0
	haveGCE := false
	for pos < len(raw) {
		switch raw[pos] {
		case 0x3B: // Trailer
			return w, h, nFrames, totalDelayMS, nil
		case 0x21: // Extension
			if pos+2 > len(raw) {
				return 0, 0, 0, 0, ErrNotImage
			}
			label := raw[pos+1]
			pos += 2
			if label == 0xF9 && pos+6 <= len(raw) && raw[pos] == 4 {
				pendingDelay = int(binary.LittleEndian.Uint16(raw[pos+2:pos+4])) * 10
				haveGCE = true
			}
			next, ok := skipGIFSubBlocks(raw, pos)
			if !ok {
				return 0, 0, 0, 0, ErrNotImage
			}
			pos = next
		case 0x2C: // Image Descriptor
			if pos+10 > len(raw) {
				return 0, 0, 0, 0, ErrNotImage
			}
			nFrames++
			if nFrames > MaxSourceFrames {
				return w, h, nFrames, totalDelayMS, ErrTooManyFrames
			}
			if int64(nFrames)*int64(w)*int64(h) > MaxSourcePixels {
				return w, h, nFrames, totalDelayMS, ErrTooManyFrames
			}
			d := DefaultFrameDelay
			if haveGCE && pendingDelay > 0 {
				d = pendingDelay
			}
			totalDelayMS += d
			pendingDelay = 0
			haveGCE = false
			imgPacked := raw[pos+9]
			pos += 10
			if imgPacked&0x80 != 0 {
				lctSize := 3 * (1 << ((imgPacked & 0x07) + 1))
				if pos+lctSize > len(raw) {
					return 0, 0, 0, 0, ErrNotImage
				}
				pos += lctSize
			}
			if pos >= len(raw) {
				return 0, 0, 0, 0, ErrNotImage
			}
			pos++ // LZW minimum code size
			next, ok := skipGIFSubBlocks(raw, pos)
			if !ok {
				return 0, 0, 0, 0, ErrNotImage
			}
			pos = next
		case 0x00:
			pos++
		default:
			if nFrames > 0 {
				return w, h, nFrames, totalDelayMS, nil
			}
			return 0, 0, 0, 0, ErrNotImage
		}
	}
	return w, h, nFrames, totalDelayMS, nil
}

func skipGIFSubBlocks(raw []byte, pos int) (int, bool) {
	for {
		if pos >= len(raw) {
			return 0, false
		}
		n := int(raw[pos])
		pos++
		if n == 0 {
			return pos, true
		}
		if pos+n > len(raw) {
			return 0, false
		}
		pos += n
	}
}
