package lookmedia

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"math"
)

// EncodeAPNG encodes frames as a plain PNG (1 frame) or a looping APNG (num_plays=0, per-frame fcTL delays in ms,
// IHDR + acTL + fcTL/IDAT for frame 0 + fcTL/fdAT for subsequent frames + IEND).
func EncodeAPNG(frames []Frame) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to encode")
	}
	if len(frames) == 1 {
		return encodeBestPNG(frames[0].img)
	}
	imgs := make([]image.Image, len(frames))
	delays := make([]int, len(frames))
	for i, f := range frames {
		imgs[i] = f.img
		d := f.delayMS
		if d <= 0 {
			d = DefaultFrameDelay
		}
		delays[i] = d
	}
	return assembleAPNG(imgs, delays)
}

// EncodeAPNGPaletted encodes frames using a single shared 256-color palette (with alpha) as a looping APNG.
func EncodeAPNGPaletted(frames []Frame) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to encode")
	}
	palFrames := quantizeSharedPalette(frames)
	if len(palFrames) == 1 {
		return encodeBestPNG(palFrames[0])
	}
	imgs := make([]image.Image, len(palFrames))
	delays := make([]int, len(palFrames))
	for i, pf := range palFrames {
		imgs[i] = pf
		d := frames[i].delayMS
		if d <= 0 {
			d = DefaultFrameDelay
		}
		delays[i] = d
	}
	return assembleAPNG(imgs, delays)
}

// EncodeDotAPNG encodes a dot state's frames within maxBytes: first merges identical consecutive frames, tries full
// RGBA APNG, then reduces to a shared 256-color palette, and if still over budget reduces frames evenly until within
// maxBytes. Returns the encoded bytes, final frame count, and average rounded fps (0 for a 1-frame still PNG).
func EncodeDotAPNG(frames []Frame, maxBytes int) ([]byte, int, int, error) {
	if len(frames) == 0 {
		return nil, 0, 0, errors.New("no frames to encode")
	}
	if maxBytes <= 0 {
		maxBytes = MaxDotFileBytes
	}
	cur := MergeIdenticalFrames(frames)
	if len(cur) == 1 {
		b, err := encodeBestPNG(cur[0].img)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(b) > maxBytes {
			b, err = encodeBestPNG(quantizeSharedPalette(cur)[0])
			if err != nil {
				return nil, 0, 0, err
			}
		}
		return b, 1, 0, nil
	}

	// 1. Full RGBA APNG.
	if b, err := EncodeAPNG(cur); err == nil && len(b) <= maxBytes {
		return b, len(cur), averageFPS(cur), nil
	}

	// 2. Shared 256-color palette first.
	if b, err := EncodeAPNGPaletted(cur); err == nil && len(b) <= maxBytes {
		return b, len(cur), averageFPS(cur), nil
	}

	// 3. Reduce frame count progressively, staying paletted, until within budget.
	target := len(cur)
	for target > 2 {
		next := target * 3 / 4
		if next >= target {
			next = target - 1
		}
		if next < 2 {
			next = 2
		}
		target = next
		cand := MergeIdenticalFrames(ResampleFrames(cur, target))
		if len(cand) == 1 {
			b, err := encodeBestPNG(quantizeSharedPalette(cand)[0])
			return b, 1, 0, err
		}
		b, err := EncodeAPNGPaletted(cand)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(b) <= maxBytes {
			return b, len(cand), averageFPS(cand), nil
		}
	}

	// Final fallback: still frame 0 (paletted) to guarantee the byte budget.
	still := quantizeSharedPalette(cur[:1])[0]
	b, err := encodeBestPNG(still)
	return b, 1, 0, err
}

func averageFPS(frames []Frame) int {
	if len(frames) <= 1 {
		return 0
	}
	totalMS := 0
	for _, f := range frames {
		totalMS += f.delayMS
	}
	if totalMS <= 0 {
		return MaxOutputFPS
	}
	fps := int(math.Round(float64(len(frames)) * 1000.0 / float64(totalMS)))
	if fps < 1 {
		return 1
	}
	if fps > MaxOutputFPS {
		return MaxOutputFPS
	}
	return fps
}

func encodeBestPNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pngParts struct {
	ihdr []byte
	plte []byte
	trns []byte
	idat []byte
}

func splitPNGParts(raw []byte) (pngParts, error) {
	if !bytes.HasPrefix(raw, pngSignature) {
		return pngParts{}, errors.New("not a PNG")
	}
	var out pngParts
	var idat bytes.Buffer
	pos := len(pngSignature)
	for pos+12 <= len(raw) {
		n := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
		tag := string(raw[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + n
		if n < 0 || dataEnd+4 > len(raw) {
			return pngParts{}, errors.New("truncated PNG chunk")
		}
		data := raw[dataStart:dataEnd]
		pos = dataEnd + 4
		switch tag {
		case "IHDR":
			out.ihdr = append([]byte(nil), data...)
		case "PLTE":
			out.plte = append([]byte(nil), data...)
		case "tRNS":
			out.trns = append([]byte(nil), data...)
		case "IDAT":
			idat.Write(data)
		case "IEND":
			pos = len(raw)
		}
	}
	out.idat = idat.Bytes()
	if len(out.ihdr) != 13 || len(out.idat) == 0 {
		return pngParts{}, errors.New("incomplete PNG")
	}
	return out, nil
}

func assembleAPNG(imgs []image.Image, delays []int) ([]byte, error) {
	parts := make([]pngParts, len(imgs))
	for i, img := range imgs {
		b, err := encodeBestPNG(img)
		if err != nil {
			return nil, err
		}
		p, err := splitPNGParts(b)
		if err != nil {
			return nil, err
		}
		parts[i] = p
	}

	w := binary.BigEndian.Uint32(parts[0].ihdr[0:4])
	h := binary.BigEndian.Uint32(parts[0].ihdr[4:8])

	var out bytes.Buffer
	out.Write(pngSignature)
	writePNGChunk(&out, "IHDR", parts[0].ihdr)
	if len(parts[0].plte) > 0 {
		writePNGChunk(&out, "PLTE", parts[0].plte)
	}
	if len(parts[0].trns) > 0 {
		writePNGChunk(&out, "tRNS", parts[0].trns)
	}

	var actl [8]byte
	binary.BigEndian.PutUint32(actl[0:4], uint32(len(imgs)))
	binary.BigEndian.PutUint32(actl[4:8], 0) // num_plays = 0 (loop forever)
	writePNGChunk(&out, "acTL", actl[:])

	var seq uint32
	for i, p := range parts {
		d := delays[i]
		if d < 1 {
			d = 1
		}
		if d > 65535 {
			d = 65535
		}
		var fctl [26]byte
		binary.BigEndian.PutUint32(fctl[0:4], seq)
		seq++
		binary.BigEndian.PutUint32(fctl[4:8], w)
		binary.BigEndian.PutUint32(fctl[8:12], h)
		binary.BigEndian.PutUint32(fctl[12:16], 0) // x_offset
		binary.BigEndian.PutUint32(fctl[16:20], 0) // y_offset
		binary.BigEndian.PutUint16(fctl[20:22], uint16(d))
		binary.BigEndian.PutUint16(fctl[22:24], 1000)
		fctl[24] = apngDisposeNone
		fctl[25] = apngBlendSource
		writePNGChunk(&out, "fcTL", fctl[:])

		if i == 0 {
			writePNGChunk(&out, "IDAT", p.idat)
		} else {
			fdat := make([]byte, 4+len(p.idat))
			binary.BigEndian.PutUint32(fdat[0:4], seq)
			seq++
			copy(fdat[4:], p.idat)
			writePNGChunk(&out, "fdAT", fdat)
		}
	}
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}
