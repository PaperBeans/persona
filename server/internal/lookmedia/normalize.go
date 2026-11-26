package lookmedia

import (
	"bytes"
	"image"
	"image/draw"
)

// Normalize crops every frame of anim (centered square when crop is nil) and applies the animation bounds:
// loop duration <= MaxLoopMS, identical consecutive frames merged, frames shorter than MinFrameDelayMS (100 ms / 10 fps)
// merged, and frame count resampled evenly to at most MaxOutputFrames (36).
func Normalize(anim *Animation, crop *Crop) ([]Frame, error) {
	if anim == nil || len(anim.frames) == 0 {
		return nil, ErrNotImage
	}
	rect, err := cropRect(anim.width, anim.height, crop)
	if err != nil {
		return nil, err
	}
	if len(anim.frames) > 1 {
		totalMS := 0
		for _, f := range anim.frames {
			d := f.delayMS
			if d <= 0 {
				d = DefaultFrameDelay
			}
			totalMS += d
		}
		if totalMS > MaxLoopMS {
			return nil, ErrAnimationTooLong
		}
	}

	cropped := make([]Frame, len(anim.frames))
	for i, f := range anim.frames {
		sq := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(sq, sq.Bounds(), f.img, rect.Min, draw.Src)
		d := f.delayMS
		if len(anim.frames) == 1 {
			d = 0
		} else if d <= 0 {
			d = DefaultFrameDelay
		}
		cropped[i] = Frame{img: sq, delayMS: d}
	}
	if len(cropped) == 1 {
		return cropped, nil
	}

	out := MergeIdenticalFrames(cropped)
	if len(out) <= 1 {
		return stillOnly(out), nil
	}
	out = capFrameRate(out, MinFrameDelayMS)
	if len(out) <= 1 {
		return stillOnly(out), nil
	}
	if len(out) > MaxOutputFrames {
		out = ResampleFrames(out, MaxOutputFrames)
	}
	out = MergeIdenticalFrames(out)
	if len(out) <= 1 {
		return stillOnly(out), nil
	}
	return out, nil
}

func cropRect(w, h int, crop *Crop) (image.Rectangle, error) {
	if crop == nil {
		side := min(w, h)
		if side < MinSide {
			return image.Rectangle{}, ErrTooSmall
		}
		x0 := (w - side) / 2
		y0 := (h - side) / 2
		return image.Rect(x0, y0, x0+side, y0+side), nil
	}
	if crop.x < 0 || crop.y < 0 || crop.side < MinSide || crop.x+crop.side > w || crop.y+crop.side > h {
		return image.Rectangle{}, ErrBadCrop
	}
	return image.Rect(crop.x, crop.y, crop.x+crop.side, crop.y+crop.side), nil
}

func stillOnly(frames []Frame) []Frame {
	if len(frames) == 0 {
		return nil
	}
	return []Frame{{img: frames[0].img, delayMS: 0}}
}

// MergeIdenticalFrames merges consecutive frames with identical RGBA bytes, summing their delays.
func MergeIdenticalFrames(frames []Frame) []Frame {
	if len(frames) <= 1 {
		return frames
	}
	out := make([]Frame, 0, len(frames))
	out = append(out, frames[0])
	for i := 1; i < len(frames); i++ {
		last := &out[len(out)-1]
		if framesEqual(last.img, frames[i].img) {
			last.delayMS += frames[i].delayMS
		} else {
			out = append(out, frames[i])
		}
	}
	if len(out) == 1 {
		out[0].delayMS = 0
	}
	return out
}

func framesEqual(a, b *image.RGBA) bool {
	return a.Bounds().Dx() == b.Bounds().Dx() &&
		a.Bounds().Dy() == b.Bounds().Dy() &&
		bytes.Equal(a.Pix, b.Pix)
}

// capFrameRate merges frames shorter than minDelayMS into the kept frame so total loop duration is preserved and no
// output frame is shorter than minDelayMS.
func capFrameRate(frames []Frame, minDelayMS int) []Frame {
	if len(frames) <= 1 {
		return frames
	}
	out := make([]Frame, 0, len(frames))
	i := 0
	for i < len(frames) {
		kept := frames[i]
		acc := kept.delayMS
		j := i + 1
		for acc < minDelayMS && j < len(frames) {
			acc += frames[j].delayMS
			j++
		}
		if acc < minDelayMS && len(out) > 0 {
			out[len(out)-1].delayMS += acc
			break
		}
		kept.delayMS = acc
		out = append(out, kept)
		i = j
	}
	if len(out) == 1 {
		out[0].delayMS = 0
	}
	return out
}

// ResampleFrames resamples frames evenly over the total loop duration down to target frames (target >= 1), keeping
// frame 0 intact and preserving total loop duration.
func ResampleFrames(frames []Frame, target int) []Frame {
	if len(frames) <= target || target <= 0 {
		return frames
	}
	if target == 1 {
		return stillOnly(frames)
	}
	totalMS := 0
	ends := make([]int, len(frames))
	for i, f := range frames {
		d := f.delayMS
		if d <= 0 {
			d = DefaultFrameDelay
		}
		totalMS += d
		ends[i] = totalMS
	}
	out := make([]Frame, 0, target)
	srcIdx := 0
	for k := 0; k < target; k++ {
		t0 := k * totalMS / target
		t1 := (k + 1) * totalMS / target
		for srcIdx+1 < len(frames) && ends[srcIdx] <= t0 {
			srcIdx++
		}
		dur := t1 - t0
		if dur < 1 {
			dur = 1
		}
		out = append(out, Frame{
			img:     frames[srcIdx].img,
			delayMS: dur,
		})
	}
	return out
}
