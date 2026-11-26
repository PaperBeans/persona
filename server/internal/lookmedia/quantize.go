package lookmedia

import (
	"image"
	"image/color"
	"sort"
)

type colorCount struct {
	r, g, b, a uint8
	count      int64
}

// quantizeSharedPalette converts all RGBA frames to a single shared 256-color palette (preserving alpha), using
// median-cut quantization across all frames so colors stay consistent without per-frame flicker.
func quantizeSharedPalette(frames []Frame) []*image.Paletted {
	// Histogram non-transparent pixels quantized slightly (top 6 bits per channel) for fast median-cut.
	hist := make(map[uint32]int64, 4096)
	for _, f := range frames {
		pix := f.img.Pix
		for i := 0; i+3 < len(pix); i += 4 {
			a := pix[i+3]
			if a == 0 {
				continue
			}
			r, g, b := unpremulByte(pix[i], a), unpremulByte(pix[i+1], a), unpremulByte(pix[i+2], a)
			key := uint32(r&0xFC)<<24 | uint32(g&0xFC)<<16 | uint32(b&0xFC)<<8 | uint32(a&0xFC)
			hist[key]++
		}
	}

	entries := make([]colorCount, 0, len(hist))
	for k, cnt := range hist {
		entries = append(entries, colorCount{
			r:     uint8(k >> 24),
			g:     uint8(k >> 16),
			b:     uint8(k >> 8),
			a:     uint8(k),
			count: cnt,
		})
	}

	// Reserve index 0 for fully transparent pixels.
	pal := make(color.Palette, 1, 256)
	pal[0] = color.NRGBA{0, 0, 0, 0}

	if len(entries) > 0 {
		buckets := [][]colorCount{entries}
		for len(buckets) < 255 {
			bestIdx := -1
			bestScore := int64(0)
			bestCh := 0
			for i, b := range buckets {
				if len(b) < 2 {
					continue
				}
				ch, spread := widestChannel(b)
				if spread == 0 {
					continue
				}
				score := int64(spread) * int64(len(b))
				if score > bestScore {
					bestScore = score
					bestIdx = i
					bestCh = ch
				}
			}
			if bestIdx < 0 {
				break
			}
			b := buckets[bestIdx]
			sort.Slice(b, func(i, j int) bool { return channelVal(b[i], bestCh) < channelVal(b[j], bestCh) })
			var total int64
			for _, c := range b {
				total += c.count
			}
			half := total / 2
			var acc int64
			split := 1
			for i, c := range b {
				acc += c.count
				if acc >= half && i > 0 {
					split = i
					break
				}
			}
			if split >= len(b) {
				split = len(b) / 2
			}
			left := b[:split]
			right := b[split:]
			buckets[bestIdx] = left
			buckets = append(buckets, right)
		}
		for _, b := range buckets {
			var sr, sg, sb, sa, tot int64
			for _, c := range b {
				sr += int64(c.r) * c.count
				sg += int64(c.g) * c.count
				sb += int64(c.b) * c.count
				sa += int64(c.a) * c.count
				tot += c.count
			}
			if tot > 0 {
				pal = append(pal, color.NRGBA{
					R: uint8(sr / tot),
					G: uint8(sg / tot),
					B: uint8(sb / tot),
					A: uint8(sa / tot),
				})
			}
		}
	}

	// Precompute NRGBA entries for fast distance lookup.
	pr := make([]int, len(pal))
	pg := make([]int, len(pal))
	pb := make([]int, len(pal))
	pa := make([]int, len(pal))
	for i, c := range pal {
		nc := color.NRGBAModel.Convert(c).(color.NRGBA)
		pr[i], pg[i], pb[i], pa[i] = int(nc.R), int(nc.G), int(nc.B), int(nc.A)
	}

	// 5-bit per channel cache for fast nearest-palette lookup on noisy frames.
	cache := make(map[uint32]uint8, 4096)
	nearest := func(r, g, b, a uint8) uint8 {
		if a == 0 {
			return 0
		}
		key := uint32(r&0xF8)<<17 | uint32(g&0xF8)<<12 | uint32(b&0xF8)<<7 | uint32(a&0xF8)
		if idx, ok := cache[key]; ok {
			return idx
		}
		ri, gi, bi, ai := int(r), int(g), int(b), int(a)
		best := uint8(1)
		bestDist := 1 << 30
		for i := 1; i < len(pal); i++ {
			dr := ri - pr[i]
			dg := gi - pg[i]
			db := bi - pb[i]
			da := ai - pa[i]
			d := dr*dr + dg*dg + db*db + da*da*2
			if d < bestDist {
				bestDist = d
				best = uint8(i)
				if d == 0 {
					break
				}
			}
		}
		if len(cache) < 65536 {
			cache[key] = best
		}
		return best
	}

	out := make([]*image.Paletted, len(frames))
	for fi, f := range frames {
		w, h := f.img.Bounds().Dx(), f.img.Bounds().Dy()
		dst := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		pix := f.img.Pix
		for y := 0; y < h; y++ {
			srcRow := y * f.img.Stride
			dstRow := y * dst.Stride
			for x := 0; x < w; x++ {
				off := srcRow + x*4
				a := pix[off+3]
				if a == 0 {
					dst.Pix[dstRow+x] = 0
					continue
				}
				dst.Pix[dstRow+x] = nearest(
					unpremulByte(pix[off], a),
					unpremulByte(pix[off+1], a),
					unpremulByte(pix[off+2], a),
					a,
				)
			}
		}
		out[fi] = dst
	}
	return out
}

func unpremulByte(c, a uint8) uint8 {
	if a == 255 {
		return c
	}
	if a == 0 {
		return 0
	}
	v := (int(c)*255 + int(a)/2) / int(a)
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func channelVal(c colorCount, ch int) uint8 {
	switch ch {
	case 0:
		return c.r
	case 1:
		return c.g
	case 2:
		return c.b
	default:
		return c.a
	}
}

func widestChannel(b []colorCount) (int, int) {
	mins := [4]uint8{255, 255, 255, 255}
	maxs := [4]uint8{0, 0, 0, 0}
	for _, c := range b {
		vs := [4]uint8{c.r, c.g, c.b, c.a}
		for ch := range 4 {
			if vs[ch] < mins[ch] {
				mins[ch] = vs[ch]
			}
			if vs[ch] > maxs[ch] {
				maxs[ch] = vs[ch]
			}
		}
	}
	bestCh, bestSpread := 0, 0
	for ch := range 4 {
		if sp := int(maxs[ch]) - int(mins[ch]); sp > bestSpread {
			bestSpread = sp
			bestCh = ch
		}
	}
	return bestCh, bestSpread
}
