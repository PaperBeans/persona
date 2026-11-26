// Package lookmedia decodes still and animated avatar uploads (PNG/APNG, JPEG, GIF, WebP), normalizes animation bounds,
// encodes looping APNG files within byte budgets, and extracts contrast-safe theme color palettes in pure Go.
//
//persona:layer library
package lookmedia

import (
	"errors"
	"image"
)

// Bounds enforced on uploaded avatar images and generated dot animations.
const (
	MaxImageBytes     = 8 << 20     // 8 MB per picture
	MaxRequestImages  = 24 << 20    // 24 MB across all pictures in one request
	MaxCanvasPixels   = 40_000_000  // 40M pixels per frame canvas
	MinSide           = 64          // minimum image width/height and crop side
	MaxSourceFrames   = 300         // maximum source frames before decoding
	MaxSourcePixels   = 200_000_000 // maximum frames * canvas pixels before decoding
	MaxOutputFPS      = 10          // maximum output frame rate
	MinFrameDelayMS   = 100         // minimum frame delay in ms (1000 / MaxOutputFPS)
	MaxOutputFrames   = 36          // maximum output frames per dot APNG
	MaxLoopSeconds    = 9           // maximum animation loop length in seconds
	MaxLoopMS         = MaxLoopSeconds * 1000
	DefaultFrameDelay = 100     // fallback delay (ms) when a source frame declares 0 ms
	MaxDotFileBytes   = 2 << 20 // 2 MB per dot file keeps 5 animated states + avatars well under 16 MB
	DotSide           = 144     // output dot frame side in pixels
)

// Sentinel errors mapped by the hub to localized HTTP refusals.
var (
	ErrNotImage         = errors.New("not a readable image")
	ErrTooSmall         = errors.New("image smaller than minimum side")
	ErrTooManyPixels    = errors.New("image canvas exceeds pixel limit")
	ErrTooManyFrames    = errors.New("animation exceeds frame or frame-pixel limit")
	ErrAnimationTooLong = errors.New("animation loop exceeds maximum duration")
	ErrBadCrop          = errors.New("invalid crop rectangle")
)

// Crop selects a square region of a source image canvas in pixels.
type Crop struct {
	x, y, side int
}

// NewCrop returns a Crop for the square at (x, y) with the given side length.
func NewCrop(x, y, side int) Crop { return Crop{x: x, y: y, side: side} }

// X returns the crop's left pixel coordinate.
func (c Crop) X() int { return c.x }

// Y returns the crop's top pixel coordinate.
func (c Crop) Y() int { return c.y }

// Side returns the crop's square side length in pixels.
func (c Crop) Side() int { return c.side }

// Frame is one fully composited RGBA frame of an image or animation.
type Frame struct {
	img     *image.RGBA
	delayMS int // 0 for a still image; > 0 for animated frames
}

// NewFrame returns a Frame holding img and delayMS.
func NewFrame(img *image.RGBA, delayMS int) Frame { return Frame{img: img, delayMS: delayMS} }

// Image returns the frame's RGBA pixels.
func (f Frame) Image() *image.RGBA { return f.img }

// DelayMS returns the frame's display duration in milliseconds (0 for still images).
func (f Frame) DelayMS() int { return f.delayMS }

// Animation holds a decoded still image (1 frame) or animated image (>1 frames).
type Animation struct {
	width  int
	height int
	frames []Frame
}

// NewAnimation constructs an Animation with canvas dimensions and frames.
func NewAnimation(width, height int, frames []Frame) *Animation {
	return &Animation{width: width, height: height, frames: frames}
}

// Width returns the animation canvas width in pixels.
func (a *Animation) Width() int { return a.width }

// Height returns the animation canvas height in pixels.
func (a *Animation) Height() int { return a.height }

// Frames returns the decoded frames.
func (a *Animation) Frames() []Frame { return a.frames }
