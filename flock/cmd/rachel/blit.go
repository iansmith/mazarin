// blit.go — backing store blit and exposed region computation for rachel.
//
// When a shepherd sends MsgBlit, rachel computes the exposed region
// (the parts of the window not covered by windows above it in z-order)
// and copies only those pixels from the shepherd's backing store to
// the real GPU framebuffer.
package main

import (
	"fmt"
	"image"
)

// rectSubtract returns the parts of a that are not covered by b.
// Returns 0-4 rectangles (the top, bottom, left, and right strips
// of a that remain after removing the intersection with b).
func rectSubtract(a, b image.Rectangle) []image.Rectangle {
	isect := a.Intersect(b)
	if isect.Empty() {
		return []image.Rectangle{a}
	}
	if isect == a {
		return nil // fully covered
	}
	var result []image.Rectangle
	// Top strip: above the intersection.
	if a.Min.Y < isect.Min.Y {
		result = append(result, image.Rect(a.Min.X, a.Min.Y, a.Max.X, isect.Min.Y))
	}
	// Bottom strip: below the intersection.
	if isect.Max.Y < a.Max.Y {
		result = append(result, image.Rect(a.Min.X, isect.Max.Y, a.Max.X, a.Max.Y))
	}
	// Left strip: between top and bottom, left of intersection.
	if a.Min.X < isect.Min.X {
		result = append(result, image.Rect(a.Min.X, isect.Min.Y, isect.Min.X, isect.Max.Y))
	}
	// Right strip: between top and bottom, right of intersection.
	if isect.Max.X < a.Max.X {
		result = append(result, image.Rect(isect.Max.X, isect.Min.Y, a.Max.X, isect.Max.Y))
	}
	return result
}

// screenOrigin returns the top-left corner of the full buffer (including borders)
// on the framebuffer. The stored ta.x/ta.y is the app-area position.
func screenOrigin(ta *trackedApp) (int, int) {
	return int(ta.x) - borderLeft, int(ta.y) - borderTop
}

// exposedRegion returns the set of non-overlapping rectangles that
// represent the visible portion of the window identified by sid.
// It subtracts the bounds of every window above sid in z-order.
func exposedRegion(sid int) []image.Rectangle {
	ta, ok := trackedApps[sid]
	if !ok {
		return nil
	}
	ox, oy := screenOrigin(ta)
	winRect := image.Rect(ox, oy, ox+int(ta.bsWidth), oy+int(ta.bsHeight))

	// Start with the full window rect.
	rects := []image.Rectangle{winRect}

	// Walk z-order front-to-back. Every window above sid occludes it.
	for _, aboveSID := range zOrder {
		if aboveSID == sid {
			break // reached our window; everything below is behind us
		}
		above, ok := trackedApps[aboveSID]
		if !ok {
			continue
		}
		aox, aoy := screenOrigin(above)
		aboveRect := image.Rect(aox, aoy, aox+int(above.bsWidth), aoy+int(above.bsHeight))

		// Subtract aboveRect from every rect in the current list.
		var next []image.Rectangle
		for _, r := range rects {
			next = append(next, rectSubtract(r, aboveRect)...)
		}
		rects = next
	}
	return rects
}

// blitWindow copies the exposed region of sid's backing store to the
// framebuffer. Each exposed rect is copied scanline by scanline.
// fb is the framebuffer pixel slice, fbStride is bytes per framebuffer row.
var blitDbgCount int

func blitWindow(sid int, fb []byte, fbStride int) {
	ta, ok := trackedApps[sid]
	if !ok || ta.backingStore == nil {
		return
	}
	bs := ta.backingStore
	bsStride := int(ta.bsStride)
	winX, winY := screenOrigin(ta) // top-left of full buffer on screen

	regions := exposedRegion(sid)

	// Log first 3 blits per SID for debugging.
	blitDbgCount++
	if blitDbgCount <= 3 {
		nonZero := 0
		for i := 0; i < len(bs) && i < bsStride*4; i += 4 {
			if bs[i] != 0 || bs[i+1] != 0 || bs[i+2] != 0 || bs[i+3] != 0 {
				nonZero++
			}
		}
		fmt.Printf("[rachel:blit] SID=%d win=(%d,%d) bs=%dx%d stride=%d regions=%d bsLen=%d bsNonZero=%d/4rows\n",
			sid, winX, winY, ta.bsWidth, ta.bsHeight, bsStride, len(regions), len(bs), nonZero)
		for i, r := range regions {
			if i < 4 {
				fmt.Printf("[rachel:blit]   region[%d]: (%d,%d)-(%d,%d)\n", i, r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
			}
		}
	}

	for _, r := range regions {
		localX0 := r.Min.X - winX
		w := r.Dx() * 4

		for y := r.Min.Y; y < r.Max.Y; y++ {
			localY := y - winY
			fbOff := y*fbStride + r.Min.X*4
			bsOff := localY*bsStride + localX0*4
			if fbOff >= 0 && fbOff+w <= len(fb) && bsOff >= 0 && bsOff+w <= len(bs) {
				copy(fb[fbOff:fbOff+w], bs[bsOff:bsOff+w])
			}
		}
	}
}

// drawBorders fills the border regions of ta's backing store with debug blue.
func drawBorders(ta *trackedApp) {
	bs := ta.backingStore
	if bs == nil {
		return
	}
	tw := int(ta.bsWidth)
	th := int(ta.bsHeight)
	stride := int(ta.bsStride)

	// Blue (BGRA byte order — framebuffer swaps R/B): B=200, G=80, R=40, A=255
	setPixel := func(x, y int) {
		off := y*stride + x*4
		if off+3 < len(bs) {
			bs[off] = 200  // B
			bs[off+1] = 80 // G
			bs[off+2] = 40 // R
			bs[off+3] = 255 // A
		}
	}

	// Top strip
	for y := 0; y < borderTop && y < th; y++ {
		for x := 0; x < tw; x++ {
			setPixel(x, y)
		}
	}
	// Bottom strip
	for y := th - borderBottom; y < th; y++ {
		if y >= 0 {
			for x := 0; x < tw; x++ {
				setPixel(x, y)
			}
		}
	}
	// Left strip (between top and bottom)
	for y := borderTop; y < th-borderBottom; y++ {
		for x := 0; x < borderLeft && x < tw; x++ {
			setPixel(x, y)
		}
	}
	// Right strip (between top and bottom)
	for y := borderTop; y < th-borderBottom; y++ {
		for x := tw - borderRight; x < tw; x++ {
			if x >= 0 {
				setPixel(x, y)
			}
		}
	}
}
