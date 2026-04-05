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
	"image/color"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
)

// windowTitleBar is the TitleBar implementation used for all managed windows.
// Set during rachel's main() after fonts are available. If nil, title bars
// are not drawn (NeuBox decoration only).
var windowTitleBar mancini.TitleBar

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

func blitWindow(sid int, regions []image.Rectangle, fb []byte, fbStride int, focused bool) {
	ta, ok := trackedApps[sid]
	if !ok || ta.backingStore == nil {
		return
	}
	bs := ta.backingStore
	bsStride := int(ta.bsStride)
	winX, winY := screenOrigin(ta) // top-left of full buffer on screen

	blitDbgCount++
	// Log first 3 blits per SID for debugging.
	if blitDbgCount <= 3 {
		nonZero := 0
		for i := 0; i < len(bs) && i < bsStride*4; i += 4 {
			if bs[i] != 0 || bs[i+1] != 0 || bs[i+2] != 0 || bs[i+3] != 0 {
				nonZero++
			}
		}
		sys.UartWriteString(fmt.Sprintf("[rachel:blit] SID=%d win=(%d,%d) bs=%dx%d stride=%d regions=%d bsLen=%d bsNonZero=%d/4rows focused=%v\n",
			sid, winX, winY, ta.bsWidth, ta.bsHeight, bsStride, len(regions), len(bs), nonZero, focused))
		for i, r := range regions {
			if i < 4 {
				sys.UartWriteString(fmt.Sprintf("[rachel:blit]   region[%d]: (%d,%d)-(%d,%d)\n", i, r.Min.X, r.Min.Y, r.Max.X, r.Max.Y))
			}
		}
	}

	// For unfocused (Flush) windows, clip blit to the NeuBox face bounds —
	// the outer shadow margin is negligible and should show desktop through.
	// Face in BS coords: (borderLeft, shadowTop) to (bsWidth-borderRight, bsHeight-borderBottom).
	var faceClip image.Rectangle
	if !focused {
		faceClip = image.Rect(
			winX+borderLeft, winY+shadowTop,
			winX+int(ta.bsWidth)-borderRight, winY+int(ta.bsHeight)-borderBottom)
	}

	for _, r := range regions {
		if !focused {
			r = r.Intersect(faceClip)
			if r.Empty() {
				continue
			}
		}
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

// renderDecorOnce renders a NeuBox + title bar into a temporary buffer
// for the given depth/state and returns the border pixels only.
// The returned slice has the same layout as the backing store but only
// the border zone pixels are meaningful.
func renderDecorOnce(ta *trackedApp, depth mancini.NeuDepth, state mancini.WindowState, desktopBG color.NRGBA) []byte {
	tw := int(ta.bsWidth)
	th := int(ta.bsHeight)
	stride := int(ta.bsStride)
	buf := make([]byte, len(ta.backingStore))

	// Fill entire buffer with desktop background (swapped R↔B for BGRA FB).
	bg := desktopBG
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i] = bg.B
		buf[i+1] = bg.G
		buf[i+2] = bg.R
		buf[i+3] = bg.A
	}

	tmpImg := &image.RGBA{
		Pix:    buf,
		Stride: stride,
		Rect:   image.Rect(0, 0, tw, th),
	}
	dc := mancini.NewDrawContextForImage(tmpImg, nil)
	pal := mctheme.NewDefaultPaletteSwapRB()
	neuP := mctheme.NewDefaultNeumorphicParams().Heavy()
	// Reduce light shadow intensity for window decorations — full white
	// (α=255) creates a visible white margin in the border zone.
	neuP.Raised.LightAlpha = 160

	x1 := float64(borderLeft)
	y1 := float64(shadowTop)
	x2 := float64(tw - borderRight)
	y2 := float64(th - borderBottom)
	r := 6.0

	// Use the desktop BG as the NeuBox face color so the shadows float
	// on the desktop surface — no visible lighter band from pal.Surface().
	// The buffer was pre-filled in BGRA byte order, but the image.RGBA
	// drawing expects RGBA — swap R↔B so the rendered face matches.
	faceColor := color.NRGBA{R: desktopBG.B, G: desktopBG.G, B: desktopBG.R, A: desktopBG.A}
	std.NeuBoxWith(pal, dc, depth, x1, y1, x2, y2, r, faceColor, neuP)

	// Title bar drawn AFTER NeuBox so stripes are visible on top of the face.
	if windowTitleBar != nil {
		tbX := float64(borderLeft) + 4
		tbY := float64(shadowTop) + 2
		tbW := float64(tw-borderRight) - tbX - 4
		tbH := float64(titleBarHeight)
		windowTitleBar.DrawTitleBar(dc, ta.title, state, mancini.AppWindowType, tbX, tbY, tbW, tbH)
	}

	return buf
}

// preRenderDecorations pre-renders both focused (Raised) and unfocused
// (Flush) decorations into cached buffers and applies the focused state
// to the backing store. Called once at allocation time.
func preRenderDecorations(ta *trackedApp, desktopBG color.NRGBA) {
	ta.decorFocused = renderDecorOnce(ta, mancini.Raised, mancini.Active, desktopBG)
	ta.decorUnfocused = renderDecorOnce(ta, mancini.Flush, mancini.Inactive, desktopBG)
	applyDecorations(ta, true)
	sys.UartWriteString(fmt.Sprintf("[rachel:decor] pre-rendered %dx%d title=%q\n",
		ta.bsWidth, ta.bsHeight, ta.title))
}

// applyDecorations copies pre-rendered border pixels into the backing store.
// Only the border zone is overwritten — app content is untouched.
func applyDecorations(ta *trackedApp, focused bool) {
	src := ta.decorUnfocused
	if focused {
		src = ta.decorFocused
	}
	if src == nil {
		return
	}

	tw := int(ta.bsWidth)
	th := int(ta.bsHeight)
	stride := int(ta.bsStride)
	bs := ta.backingStore

	// Top border (including title bar area).
	topBytes := borderTop * stride
	if topBytes <= len(bs) && topBytes <= len(src) {
		copy(bs[:topBytes], src[:topBytes])
	}
	// Bottom border.
	botStart := (th - borderBottom) * stride
	if botStart >= 0 && botStart < len(bs) {
		copy(bs[botStart:], src[botStart:])
	}
	// Left and right strips (between top and bottom).
	for y := borderTop; y < th-borderBottom; y++ {
		rowOff := y * stride
		// Left strip.
		leftEnd := rowOff + borderLeft*4
		if leftEnd <= len(bs) {
			copy(bs[rowOff:leftEnd], src[rowOff:leftEnd])
		}
		// Right strip.
		rightStart := rowOff + (tw-borderRight)*4
		rightEnd := rowOff + tw*4
		if rightEnd <= len(bs) {
			copy(bs[rightStart:rightEnd], src[rightStart:rightEnd])
		}
	}
}
