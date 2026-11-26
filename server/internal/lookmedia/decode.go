package lookmedia

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// Decode decodes a PNG, APNG, JPEG, GIF, or still/animated WebP image in pure Go, checking dimension, frame count,
// frame×pixel, and loop duration bounds before performing full decoding work.
func Decode(raw []byte) (*Animation, error) {
	if len(raw) < 8 {
		return nil, ErrNotImage
	}
	switch {
	case bytes.HasPrefix(raw, pngSignature):
		return decodeAPNGOrPNG(raw)
	case len(raw) >= 6 && (string(raw[:6]) == "GIF87a" || string(raw[:6]) == "GIF89a"):
		return decodeGIF(raw)
	case len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return decodeWebP(raw)
	case raw[0] == 0xFF && raw[1] == 0xD8:
		return decodeJPEG(raw)
	default:
		return nil, ErrNotImage
	}
}

func decodeJPEG(raw []byte) (*Animation, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotImage
	}
	if err := checkCanvasBounds(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotImage
	}
	return &Animation{
		width:  cfg.Width,
		height: cfg.Height,
		frames: []Frame{{img: toRGBA(img), delayMS: 0}},
	}, nil
}

func checkCanvasBounds(w, h int) error {
	if w <= 0 || h <= 0 {
		return ErrNotImage
	}
	if w < MinSide || h < MinSide {
		return ErrTooSmall
	}
	if int64(w)*int64(h) > MaxCanvasPixels {
		return ErrTooManyPixels
	}
	return nil
}

func checkSourceFrameBudget(frames, w, h int) error {
	if frames <= 0 {
		return ErrNotImage
	}
	if frames > MaxSourceFrames {
		return fmt.Errorf("%w: %d frames > %d", ErrTooManyFrames, frames, MaxSourceFrames)
	}
	if int64(frames)*int64(w)*int64(h) > MaxSourcePixels {
		return fmt.Errorf("%w: %d frames of %dx%d exceeds pixel work cap", ErrTooManyFrames, frames, w, h)
	}
	return nil
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && r.Bounds().Min == (image.Point{}) {
		return cloneRGBA(r)
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	copy(dst.Pix, src.Pix)
	return dst
}
