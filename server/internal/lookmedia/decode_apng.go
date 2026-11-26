package lookmedia

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"math"
)

const (
	apngDisposeNone       = 0
	apngDisposeBackground = 1
	apngDisposePrevious   = 2

	apngBlendSource = 0
	apngBlendOver   = 1
)

type apngFrameChunk struct {
	w, h      int
	x, y      int
	delayMS   int
	disposeOp byte
	blendOp   byte
	idatParts [][]byte
}

// decodeAPNGOrPNG decodes a plain PNG or an APNG (acTL / fcTL / fdAT) in pure Go with full APNG dispose and blend ops.
func decodeAPNGOrPNG(raw []byte) (*Animation, error) {
	if !bytes.HasPrefix(raw, pngSignature) {
		return nil, ErrNotImage
	}
	var (
		ihdr       []byte
		plte       []byte
		trns       []byte
		w, h       int
		hasACTL    bool
		numFrames  uint32
		seenIDAT   bool
		expectedSq uint32
		frames     []apngFrameChunk
		cur        *apngFrameChunk
	)

	pos := len(pngSignature)
	for pos < len(raw) {
		if pos+12 > len(raw) {
			return nil, ErrNotImage
		}
		length := binary.BigEndian.Uint32(raw[pos : pos+4])
		tag := string(raw[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + int(length)
		if int(length) < 0 || dataEnd+4 > len(raw) {
			return nil, ErrNotImage
		}
		data := raw[dataStart:dataEnd]
		pos = dataEnd + 4

		switch tag {
		case "IHDR":
			if len(data) != 13 || ihdr != nil {
				return nil, ErrNotImage
			}
			ihdr = append([]byte(nil), data...)
			w = int(binary.BigEndian.Uint32(data[0:4]))
			h = int(binary.BigEndian.Uint32(data[4:8]))
			if err := checkCanvasBounds(w, h); err != nil {
				return nil, err
			}
		case "PLTE":
			plte = append([]byte(nil), data...)
		case "tRNS":
			trns = append([]byte(nil), data...)
		case "acTL":
			if len(data) != 8 || ihdr == nil || seenIDAT {
				return nil, ErrNotImage
			}
			hasACTL = true
			numFrames = binary.BigEndian.Uint32(data[0:4])
			if numFrames == 0 {
				return nil, ErrNotImage
			}
			if numFrames > 1 {
				if err := checkSourceFrameBudget(int(numFrames), w, h); err != nil {
					return nil, err
				}
			}
		case "fcTL":
			if len(data) != 26 || ihdr == nil {
				return nil, ErrNotImage
			}
			seq := binary.BigEndian.Uint32(data[0:4])
			if seq != expectedSq {
				return nil, ErrNotImage
			}
			expectedSq++
			fw := int(binary.BigEndian.Uint32(data[4:8]))
			fh := int(binary.BigEndian.Uint32(data[8:12]))
			fx := int(binary.BigEndian.Uint32(data[12:16]))
			fy := int(binary.BigEndian.Uint32(data[16:20]))
			delayNum := binary.BigEndian.Uint16(data[20:22])
			delayDen := binary.BigEndian.Uint16(data[22:24])
			disposeOp := data[24]
			blendOp := data[25]
			if fw <= 0 || fh <= 0 || fx < 0 || fy < 0 || int64(fx)+int64(fw) > int64(w) || int64(fy)+int64(fh) > int64(h) {
				return nil, ErrNotImage
			}
			if disposeOp > apngDisposePrevious || blendOp > apngBlendOver {
				return nil, ErrNotImage
			}
			frames = append(frames, apngFrameChunk{
				w: fw, h: fh, x: fx, y: fy,
				delayMS:   apngDelayMS(delayNum, delayDen),
				disposeOp: disposeOp,
				blendOp:   blendOp,
			})
			if err := checkSourceFrameBudget(len(frames), w, h); err != nil {
				return nil, err
			}
			cur = &frames[len(frames)-1]
		case "IDAT":
			seenIDAT = true
			if hasACTL && cur != nil && len(frames) == 1 {
				cur.idatParts = append(cur.idatParts, data)
			}
		case "fdAT":
			if !hasACTL || cur == nil || len(data) < 4 {
				return nil, ErrNotImage
			}
			seq := binary.BigEndian.Uint32(data[0:4])
			if seq != expectedSq {
				return nil, ErrNotImage
			}
			expectedSq++
			cur.idatParts = append(cur.idatParts, data[4:])
		case "IEND":
			pos = len(raw)
		}
	}

	if ihdr == nil || !seenIDAT {
		return nil, ErrNotImage
	}

	// Plain PNG or single-frame APNG: decode directly through image/png.
	if !hasACTL || numFrames <= 1 || len(frames) <= 1 {
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, ErrNotImage
		}
		return &Animation{width: w, height: h, frames: []Frame{{img: toRGBA(img), delayMS: 0}}}, nil
	}

	if err := checkSourceFrameBudget(len(frames), w, h); err != nil {
		return nil, err
	}
	totalMS := 0
	for i := range frames {
		if len(frames[i].idatParts) == 0 {
			return nil, ErrNotImage
		}
		totalMS += frames[i].delayMS
	}
	if totalMS > MaxLoopMS {
		return nil, ErrAnimationTooLong
	}

	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	prev := image.NewRGBA(image.Rect(0, 0, w, h))
	out := make([]Frame, 0, len(frames))

	for i, fc := range frames {
		framePNG := buildSubPNG(ihdr, plte, trns, fc.w, fc.h, fc.idatParts)
		subImg, err := png.Decode(bytes.NewReader(framePNG))
		if err != nil {
			return nil, ErrNotImage
		}

		disp := fc.disposeOp
		if i == 0 && disp == apngDisposePrevious {
			// Per APNG spec: APNG_DISPOSE_OP_PREVIOUS on frame 0 is treated as APNG_DISPOSE_OP_BACKGROUND.
			disp = apngDisposeBackground
		}
		if disp == apngDisposePrevious {
			copy(prev.Pix, canvas.Pix)
		}

		dstRect := image.Rect(fc.x, fc.y, fc.x+fc.w, fc.y+fc.h)
		op := draw.Src
		if fc.blendOp == apngBlendOver {
			op = draw.Over
		}
		draw.Draw(canvas, dstRect, subImg, subImg.Bounds().Min, op)

		out = append(out, Frame{
			img:     cloneRGBA(canvas),
			delayMS: fc.delayMS,
		})

		switch disp {
		case apngDisposeBackground:
			draw.Draw(canvas, dstRect, image.Transparent, image.Point{}, draw.Src)
		case apngDisposePrevious:
			copy(canvas.Pix, prev.Pix)
		}
	}

	return &Animation{width: w, height: h, frames: out}, nil
}

func apngDelayMS(num, den uint16) int {
	if num == 0 {
		return DefaultFrameDelay
	}
	if den == 0 {
		den = 100
	}
	ms := int(math.Round(float64(num) * 1000.0 / float64(den)))
	if ms < 1 {
		return 1
	}
	return ms
}

func buildSubPNG(baseIHDR, plte, trns []byte, fw, fh int, idatParts [][]byte) []byte {
	var buf bytes.Buffer
	buf.Write(pngSignature)
	subIHDR := append([]byte(nil), baseIHDR...)
	binary.BigEndian.PutUint32(subIHDR[0:4], uint32(fw))
	binary.BigEndian.PutUint32(subIHDR[4:8], uint32(fh))
	writePNGChunk(&buf, "IHDR", subIHDR)
	if len(plte) > 0 {
		writePNGChunk(&buf, "PLTE", plte)
	}
	if len(trns) > 0 {
		writePNGChunk(&buf, "tRNS", trns)
	}
	for _, p := range idatParts {
		writePNGChunk(&buf, "IDAT", p)
	}
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, tag string, data []byte) {
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(data)))
	copy(hdr[4:8], tag)
	buf.Write(hdr[:])
	buf.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write(hdr[4:8])
	_, _ = crc.Write(data)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc.Sum32())
	buf.Write(tail[:])
}
