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
	"mazzy/mazarin/sys"
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

// sampleBorderZone checks 5 scanlines in the right border zone for non-zero
// pixels and reports what it finds. phase is "pre-blit", "post-blit", or
// "post-border". Returns the count of non-zero pixels found.
func sampleBorderZone(phase string, sid int, ta *trackedApp, fb []byte, fbStride int) int {
	ox, oy := screenOrigin(ta)
	bsW := int(ta.bsWidth)
	bsH := int(ta.bsHeight)
	bsStride := int(ta.bsStride)
	bs := ta.backingStore

	// Sample 5 evenly-spaced scanlines in the content area (skip top/bottom border).
	contentH := bsH - borderTop - borderBottom
	if contentH <= 0 {
		return 0
	}
	nonZero := 0
	for i := 0; i < 5; i++ {
		localY := borderTop + (contentH * (2*i + 1) / 10)
		if localY < 0 || localY >= bsH {
			continue
		}
		screenY := oy + localY

		// Check right border zone: last borderRight pixels.
		for dx := 0; dx < borderRight; dx++ {
			localX := bsW - borderRight + dx
			screenX := ox + localX

			// Backing store pixel.
			bsOff := localY*bsStride + localX*4
			var bsR, bsG, bsB, bsA byte
			if bsOff >= 0 && bsOff+3 < len(bs) {
				bsB, bsG, bsR, bsA = bs[bsOff], bs[bsOff+1], bs[bsOff+2], bs[bsOff+3]
			}

			// Framebuffer pixel.
			fbOff := screenY*fbStride + screenX*4
			var fbB, fbG, fbR, fbA byte
			if fbOff >= 0 && fbOff+3 < len(fb) {
				fbB, fbG, fbR, fbA = fb[fbOff], fb[fbOff+1], fb[fbOff+2], fb[fbOff+3]
			}

			// Report if either is non-zero and not the expected blue border color (BGRA 200,80,40,255).
			isBorderColor := fbB == 200 && fbG == 80 && fbR == 40 && fbA == 255
			bsNonZero := bsB != 0 || bsG != 0 || bsR != 0 || bsA != 0
			if bsNonZero || (phase != "pre-blit" && !isBorderColor && (fbR != 0 || fbG != 0 || fbB != 0)) {
				nonZero++
				if nonZero <= 3 {
					sys.UartWriteString(fmt.Sprintf("[blit:diag:%s] SID=%d bs(%d,%d)=BGRA(%d,%d,%d,%d) fb(%d,%d)=BGRA(%d,%d,%d,%d)\n",
						phase, sid, localX, localY, bsB, bsG, bsR, bsA,
						screenX, screenY, fbB, fbG, fbR, fbA))
				}
			}
		}
	}
	return nonZero
}

var borderDiagCount int

// blitWindow copies the exposed region of sid's backing store to the
// framebuffer. Each exposed rect is copied scanline by scanline.
// fb is the framebuffer pixel slice, fbStride is bytes per framebuffer row.
var blitDbgCount int

func blitWindow(sid int, regions []image.Rectangle, fb []byte, fbStride int) {
	ta, ok := trackedApps[sid]
	if !ok || ta.backingStore == nil {
		return
	}
	bs := ta.backingStore
	bsStride := int(ta.bsStride)
	winX, winY := screenOrigin(ta) // top-left of full buffer on screen

	// Scan border zone of backing store for non-zero pixels (every 200 blits).
	blitDbgCount++
	if blitDbgCount%200 == 100 {
		contentRight := borderLeft + int(ta.appWidth)
		nonZero := 0
		for y := borderTop; y < int(ta.bsHeight)-borderBottom; y++ {
			for x := contentRight; x < int(ta.bsWidth); x++ {
				off := y*bsStride + x*4
				if off+3 < len(bs) && (bs[off] != 0 || bs[off+1] != 0 || bs[off+2] != 0 || bs[off+3] != 0) {
					nonZero++
					if nonZero <= 5 {
						sys.UartWriteString(fmt.Sprintf("[blit:leak] SID=%d bs(%d,%d) BGRA=%d,%d,%d,%d\n",
							sid, x, y, bs[off], bs[off+1], bs[off+2], bs[off+3]))
					}
				}
			}
		}
		if nonZero > 0 {
			sys.UartWriteString(fmt.Sprintf("[blit:leak] SID=%d total=%d nonzero pixels in right border zone\n", sid, nonZero))
		}
	}

	// Log first 3 blits per SID for debugging.
	if blitDbgCount <= 3 {
		nonZero := 0
		for i := 0; i < len(bs) && i < bsStride*4; i += 4 {
			if bs[i] != 0 || bs[i+1] != 0 || bs[i+2] != 0 || bs[i+3] != 0 {
				nonZero++
			}
		}
		sys.UartWriteString(fmt.Sprintf("[rachel:blit] SID=%d win=(%d,%d) bs=%dx%d stride=%d regions=%d bsLen=%d bsNonZero=%d/4rows\n",
			sid, winX, winY, ta.bsWidth, ta.bsHeight, bsStride, len(regions), len(bs), nonZero))
		for i, r := range regions {
			if i < 4 {
				sys.UartWriteString(fmt.Sprintf("[rachel:blit]   region[%d]: (%d,%d)-(%d,%d)\n", i, r.Min.X, r.Min.Y, r.Max.X, r.Max.Y))
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

// drawBordersToFB draws the border regions of ta directly onto the framebuffer,
// clipped to the given exposed rectangles so that borders of a background window
// never overwrite content of a foreground window.
var borderDbgPerSID = map[int]int{}

func drawBordersToFB(ta *trackedApp, regions []image.Rectangle, fb []byte, fbStride int) {
	tw := int(ta.bsWidth)
	th := int(ta.bsHeight)
	ox, oy := screenOrigin(ta) // screen position of backing store top-left

	fbW := fbStride / 4        // framebuffer width in pixels

	// setPixel writes one border pixel if it falls within any exposed region.
	setPixel := func(sx, sy int) {
		if sx < 0 || sx >= fbW || sy < 0 {
			return
		}
		// Check that (sx,sy) is inside at least one exposed rect.
		pt := image.Pt(sx, sy)
		visible := false
		for _, r := range regions {
			if pt.In(r) {
				visible = true
				break
			}
		}
		if !visible {
			return
		}
		off := sy*fbStride + sx*4
		if off+3 < len(fb) {
			fb[off] = 200   // B
			fb[off+1] = 80  // G
			fb[off+2] = 40  // R
			fb[off+3] = 255 // A
		}
	}

	// Top strip
	for y := 0; y < borderTop && y < th; y++ {
		for x := 0; x < tw; x++ {
			setPixel(ox+x, oy+y)
		}
	}
	// Bottom strip
	for y := th - borderBottom; y < th; y++ {
		if y >= 0 {
			for x := 0; x < tw; x++ {
				setPixel(ox+x, oy+y)
			}
		}
	}
	// Left strip (between top and bottom)
	for y := borderTop; y < th-borderBottom; y++ {
		for x := 0; x < borderLeft && x < tw; x++ {
			setPixel(ox+x, oy+y)
		}
	}
	// Right strip (between top and bottom)
	for y := borderTop; y < th-borderBottom; y++ {
		for x := tw - borderRight; x < tw; x++ {
			if x >= 0 {
				setPixel(ox+x, oy+y)
			}
		}
	}
}
