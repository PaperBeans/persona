//go:build windows

package linktray

import (
	"image"
	"unsafe"
)

// hicon turns a drawing into an icon handle: a 32-bit color bitmap whose alpha the shell uses, and an empty mask.
// The caller destroys it (destroyIcon) when the icon is replaced.
func hicon(img *image.NRGBA) uintptr {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bgra := make([]byte, w*h*4)
	for i := 0; i < len(bgra); i += 4 {
		bgra[i], bgra[i+1], bgra[i+2], bgra[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], img.Pix[i+3]
	}
	mask := make([]byte, ((w+15)/16)*2*h) // 1 bit per pixel, rows padded to 16 bits; all zero: the alpha decides
	color, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 32, uintptr(unsafe.Pointer(&bgra[0])))
	monochrome, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	defer procDeleteObject.Call(color)
	defer procDeleteObject.Call(monochrome)
	info := iconInfo{IsIcon: 1, Mask: monochrome, Color: color}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return icon
}

func destroyIcon(icon uintptr) {
	if icon != 0 {
		procDestroyIcon.Call(icon)
	}
}
