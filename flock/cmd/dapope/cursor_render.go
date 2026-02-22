package main

import (
	"mazzy/mazarin/gui/core"
	"mazzy/mazarin/sys"
	"sync"
	"unsafe"
)

const (
	cursorSize = 32
	hotspotX   = 31
	hotspotY   = 16
)

type cursorRenderer struct {
	mu       sync.Mutex
	fb       *sys.FramebufferInfo
	pixels   []uint32 // framebuffer as uint32 slice (BGRA pixels)
	under    [cursorSize * cursorSize]uint32
	lastX    int
	lastY    int
	rendered bool
	width    int
	height   int
	stride   int // pixels per row (pitch / 4)
}

func newCursorRenderer(fb *sys.FramebufferInfo) *cursorRenderer {
	w := int(fb.Width)
	h := int(fb.Height)
	stride := int(fb.Pitch) / 4
	totalPixels := stride * h
	pixels := unsafe.Slice((*uint32)(unsafe.Pointer(fb.Addr)), totalPixels)
	return &cursorRenderer{
		fb:     fb,
		pixels: pixels,
		width:  w,
		height: h,
		stride: stride,
	}
}

func (r *cursorRenderer) Draw(stack core.CursorStack, images core.CursorImageMap) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cur := stack.Current()
	if !cur.IsValid() {
		return
	}
	img := images.CursorImageGet(cur)
	if img == nil {
		return
	}

	// Remember old position for flushing the erased region.
	oldX, oldY := r.lastX, r.lastY
	hadOld := r.rendered

	// Erase old cursor at its OLD position (no flush yet).
	if r.rendered {
		r.eraseLockedNoFlush()
	}

	// Get new cursor position.
	cx, cy := stack.Position()
	r.lastX = cx
	r.lastY = cy

	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()

	for iy := 0; iy < imgH; iy++ {
		sx0 := cx - hotspotX
		sy := cy + hotspotY - iy
		fbRow := (r.height - 1) - sy

		for ix := 0; ix < imgW; ix++ {
			sx := sx0 + ix

			if sx < 0 || sx >= r.width || fbRow < 0 || fbRow >= r.height {
				r.under[iy*cursorSize+ix] = 0
				continue
			}

			idx := fbRow*r.stride + sx
			r.under[iy*cursorSize+ix] = r.pixels[idx]

			pidx := (iy-bounds.Min.Y)*img.Stride + (ix-bounds.Min.X)*4
			aa := img.Pix[pidx+3]
			if aa == 0 {
				continue
			}

			ra := img.Pix[pidx+0]
			ga := img.Pix[pidx+1]
			ba := img.Pix[pidx+2]
			// BGRA framebuffer — swap R↔B from image.RGBA source
			r.pixels[idx] = uint32(ba) | uint32(ga)<<8 | uint32(ra)<<16 | uint32(aa)<<24
		}
	}
	r.rendered = true

	// Flush old region (where background was restored).
	if hadOld {
		r.flushCursorRegion(oldX, oldY, cursorSize, cursorSize)
	}

	// Flush new region (where cursor was drawn).
	r.flushCursorRegion(cx, cy, imgW, imgH)
}

// flushCursorRegion flushes a cursor-sized region around screen position (cx, cy).
func (r *cursorRenderer) flushCursorRegion(cx, cy, w, h int) {
	flushX := cx - hotspotX
	if flushX < 0 {
		flushX = 0
	}
	flushY := (r.height - 1) - (cy + hotspotY)
	if flushY < 0 {
		flushY = 0
	}
	flushW := w
	flushH := h
	if flushX+flushW > r.width {
		flushW = r.width - flushX
	}
	if flushY+flushH > r.height {
		flushH = r.height - flushY
	}
	if flushW > 0 && flushH > 0 {
		sys.FlushFramebuffer(uint32(flushX), uint32(flushY), uint32(flushW), uint32(flushH))
	}
}

func (r *cursorRenderer) Erase() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eraseLocked()
}

// eraseLockedNoFlush restores the saved background at the OLD cursor position
// (r.lastX, r.lastY). Does NOT flush — caller is responsible for flushing
// both the old and new regions.
func (r *cursorRenderer) eraseLockedNoFlush() {
	if !r.rendered {
		return
	}

	cx := r.lastX
	cy := r.lastY

	for iy := 0; iy < cursorSize; iy++ {
		sx0 := cx - hotspotX
		sy := cy + hotspotY - iy
		fbRow := (r.height - 1) - sy

		for ix := 0; ix < cursorSize; ix++ {
			sx := sx0 + ix
			if sx < 0 || sx >= r.width || fbRow < 0 || fbRow >= r.height {
				continue
			}
			r.pixels[fbRow*r.stride+sx] = r.under[iy*cursorSize+ix]
		}
	}
	r.rendered = false
}

func (r *cursorRenderer) eraseLocked() {
	if !r.rendered {
		return
	}
	cx, cy := r.lastX, r.lastY
	r.eraseLockedNoFlush()
	r.flushCursorRegion(cx, cy, cursorSize, cursorSize)
}
