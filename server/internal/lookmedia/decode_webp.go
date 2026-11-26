package lookmedia

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/webp"
)

type webpFrameChunk struct {
	x, y     int
	w, h     int
	delayMS  int
	dispose  bool // true = dispose to background color
	noBlend  bool // true = overwrite (Src); false = alpha blend (Over)
	bitData  []byte
	hasAlpha bool
}

// decodeWebP decodes still or animated (RIFF VP8X / ANIM / ANMF) WebP files using golang.org/x/image/webp per frame.
func decodeWebP(raw []byte) (*Animation, error) {
	if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" {
		return nil, ErrNotImage
	}

	// Scan top-level RIFF chunks to see if this is an animated extended WebP.
	var (
		hasVP8X  bool
		animated bool
		canvasW  int
		canvasH  int
		bgColor  color.RGBA
		frames   []webpFrameChunk
	)

	pos := 12
	for pos+8 <= len(raw) {
		tag := string(raw[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(raw[pos+4 : pos+8]))
		dataStart := pos + 8
		dataEnd := dataStart + size
		if size < 0 || dataEnd > len(raw) {
			return nil, ErrNotImage
		}
		data := raw[dataStart:dataEnd]
		pos = dataEnd + (size & 1) // RIFF chunks pad to even byte offsets

		switch tag {
		case "VP8X":
			if len(data) < 10 {
				return nil, ErrNotImage
			}
			hasVP8X = true
			animated = (data[0] & 0x02) != 0
			canvasW = readLE24(data[4:7]) + 1
			canvasH = readLE24(data[7:10]) + 1
			if err := checkCanvasBounds(canvasW, canvasH); err != nil {
				return nil, err
			}
		case "ANIM":
			if len(data) < 6 {
				return nil, ErrNotImage
			}
			// RFC 9649: ANIM background color is stored in BGRA byte order.
			bgNRGBA := color.NRGBA{R: data[2], G: data[1], B: data[0], A: data[3]}
			bgColor = color.RGBAModel.Convert(bgNRGBA).(color.RGBA)
		case "ANMF":
			if !animated || len(data) < 16 {
				return nil, ErrNotImage
			}
			fx := readLE24(data[0:3]) * 2
			fy := readLE24(data[3:6]) * 2
			fw := readLE24(data[6:9]) + 1
			fh := readLE24(data[9:12]) + 1
			dur := readLE24(data[12:15])
			if dur == 0 {
				dur = DefaultFrameDelay
			}
			flags := data[15]
			dispose := (flags & 0x01) != 0
			noBlend := (flags & 0x02) != 0
			if fw <= 0 || fh <= 0 || fx < 0 || fy < 0 || int64(fx)+int64(fw) > int64(canvasW) || int64(fy)+int64(fh) > int64(canvasH) {
				return nil, ErrNotImage
			}
			sub, hasAlpha, err := extractWebPFrameBitstream(data[16:])
			if err != nil {
				return nil, err
			}
			frames = append(frames, webpFrameChunk{
				x: fx, y: fy, w: fw, h: fh,
				delayMS:  dur,
				dispose:  dispose,
				noBlend:  noBlend,
				bitData:  sub,
				hasAlpha: hasAlpha,
			})
			if err := checkSourceFrameBudget(len(frames), canvasW, canvasH); err != nil {
				return nil, err
			}
		}
	}

	if !hasVP8X || !animated {
		cfg, err := webp.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, ErrNotImage
		}
		if err := checkCanvasBounds(cfg.Width, cfg.Height); err != nil {
			return nil, err
		}
		img, err := webp.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, ErrNotImage
		}
		return &Animation{width: cfg.Width, height: cfg.Height, frames: []Frame{{img: toRGBA(img), delayMS: 0}}}, nil
	}

	if len(frames) == 0 {
		return nil, ErrNotImage
	}
	if len(frames) > 1 {
		totalMS := 0
		for _, fc := range frames {
			totalMS += fc.delayMS
		}
		if totalMS > MaxLoopMS {
			return nil, ErrAnimationTooLong
		}
	}

	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	bgUniform := image.NewUniform(bgColor)
	if bgColor.A > 0 {
		draw.Draw(canvas, canvas.Bounds(), bgUniform, image.Point{}, draw.Src)
	}
	out := make([]Frame, 0, len(frames))

	for _, fc := range frames {
		wrapped := wrapWebPFrame(fc.w, fc.h, fc.hasAlpha, fc.bitData)
		subImg, err := webp.Decode(bytes.NewReader(wrapped))
		if err != nil {
			return nil, ErrNotImage
		}
		dstRect := image.Rect(fc.x, fc.y, fc.x+fc.w, fc.y+fc.h)
		op := draw.Over
		if fc.noBlend {
			op = draw.Src
		}
		draw.Draw(canvas, dstRect, subImg, subImg.Bounds().Min, op)
		delay := fc.delayMS
		if len(frames) == 1 {
			delay = 0
		}
		out = append(out, Frame{
			img:     cloneRGBA(canvas),
			delayMS: delay,
		})
		if fc.dispose {
			draw.Draw(canvas, dstRect, bgUniform, image.Point{}, draw.Src)
		}
	}
	return &Animation{width: canvasW, height: canvasH, frames: out}, nil
}

func extractWebPFrameBitstream(sub []byte) ([]byte, bool, error) {
	var (
		out      bytes.Buffer
		hasAlpha bool
		hasImage bool
	)
	pos := 0
	for pos+8 <= len(sub) {
		tag := string(sub[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(sub[pos+4 : pos+8]))
		chunkEnd := pos + 8 + size + (size & 1)
		if size < 0 || pos+8+size > len(sub) {
			return nil, false, ErrNotImage
		}
		if chunkEnd > len(sub) {
			chunkEnd = len(sub)
		}
		switch tag {
		case "ALPH":
			hasAlpha = true
			out.Write(sub[pos : pos+8+size])
			if size&1 == 1 {
				out.WriteByte(0)
			}
		case "VP8 ", "VP8L":
			hasImage = true
			out.Write(sub[pos : pos+8+size])
			if size&1 == 1 {
				out.WriteByte(0)
			}
		}
		pos = chunkEnd
	}
	if !hasImage {
		return nil, false, ErrNotImage
	}
	return out.Bytes(), hasAlpha, nil
}

func wrapWebPFrame(fw, fh int, hasAlpha bool, bitData []byte) []byte {
	var body bytes.Buffer
	if hasAlpha {
		var vp8x [10]byte
		vp8x[0] = 0x10 // alpha flag
		putLE24(vp8x[4:7], fw-1)
		putLE24(vp8x[7:10], fh-1)
		body.WriteString("VP8X")
		var sz [4]byte
		binary.LittleEndian.PutUint32(sz[:], 10)
		body.Write(sz[:])
		body.Write(vp8x[:])
	}
	body.Write(bitData)
	var out bytes.Buffer
	out.WriteString("RIFF")
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(4+body.Len()))
	out.Write(riffSize[:])
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return out.Bytes()
}

func readLE24(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
}

func putLE24(b []byte, v int) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
}
