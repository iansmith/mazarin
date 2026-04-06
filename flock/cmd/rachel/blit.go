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
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"strconv"
	"time"
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

	// Clip to screen bounds — windows may extend off-screen during drag.
	screenRect := image.Rect(0, 0, int(displayWidth), int(displayHeight))
	winRect = winRect.Intersect(screenRect)
	if winRect.Empty() {
		return nil
	}

	// Start with the visible portion of the window rect.
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

// blitScanlineAlpha alpha-blends BGRA pixels from src over dst.
// Both slices must have the same length (a multiple of 4).
// Source pixels are premultiplied alpha (from Go's image.RGBA).
// Assumes dst is always opaque (alpha=255), which is true because
// the framebuffer is cleared to the opaque desktop BG before compositing.
// Porter-Duff "over" with premultiplied src: out = src + dst * (1 - srcA/255)
func blitScanlineAlpha(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		sa := uint32(src[i+3])
		if sa == 0 {
			continue // fully transparent — leave dst as-is
		}
		if sa == 255 {
			dst[i] = src[i]
			dst[i+1] = src[i+1]
			dst[i+2] = src[i+2]
			dst[i+3] = 255
			continue
		}
		// Premultiplied source over opaque dest:
		// out = src_premul + dst * (255 - srcA) / 255
		inv := 255 - sa
		dst[i] = uint8(uint32(src[i]) + uint32(dst[i])*inv/255)
		dst[i+1] = uint8(uint32(src[i+1]) + uint32(dst[i+1])*inv/255)
		dst[i+2] = uint8(uint32(src[i+2]) + uint32(dst[i+2])*inv/255)
		dst[i+3] = 255
	}
}

// fillDesktopBG fills a framebuffer span with the desktop background color.
// This must be called before alpha-blending border zones to prevent
// darkening on repeated blits (alpha accumulation over stale blend results).
func fillDesktopBG(dst []byte) {
	for i := 0; i+3 < len(dst); i += 4 {
		dst[i] = desktopBG.B
		dst[i+1] = desktopBG.G
		dst[i+2] = desktopBG.R
		dst[i+3] = desktopBG.A
	}
}

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

	bsW := int(ta.bsWidth)
	bsH := int(ta.bsHeight)

	for _, r := range regions {
		localX0 := r.Min.X - winX
		localX1 := r.Max.X - winX

		for y := r.Min.Y; y < r.Max.Y; y++ {
			localY := y - winY
			fbRowOff := y*fbStride + r.Min.X*4
			bsRowOff := localY*bsStride + localX0*4
			w := r.Dx() * 4

			if fbRowOff < 0 || fbRowOff+w > len(fb) || bsRowOff < 0 || bsRowOff+w > len(bs) {
				continue
			}

			// Determine if this scanline row is in the border zone (top/bottom).
			inBorderRow := localY < borderTop || localY >= bsH-borderBottom

			if inBorderRow {
				// Entire row is border zone — restore desktop BG then alpha blend.
				fillDesktopBG(fb[fbRowOff : fbRowOff+w])
				blitScanlineAlpha(fb[fbRowOff:fbRowOff+w], bs[bsRowOff:bsRowOff+w])
			} else {
				// Content row — split into left-border | content | right-border.
				// Left border segment.
				if localX0 < borderLeft {
					lEnd := borderLeft - localX0
					if lEnd > r.Dx() {
						lEnd = r.Dx()
					}
					fillDesktopBG(fb[fbRowOff : fbRowOff+lEnd*4])
					blitScanlineAlpha(fb[fbRowOff:fbRowOff+lEnd*4], bs[bsRowOff:bsRowOff+lEnd*4])
				}
				// Content segment (opaque fast copy).
				cStart := borderLeft - localX0
				if cStart < 0 {
					cStart = 0
				}
				cEnd := (bsW - borderRight) - localX0
				if cEnd > r.Dx() {
					cEnd = r.Dx()
				}
				if cStart < cEnd {
					cFbOff := fbRowOff + cStart*4
					cBsOff := bsRowOff + cStart*4
					cLen := (cEnd - cStart) * 4
					copy(fb[cFbOff:cFbOff+cLen], bs[cBsOff:cBsOff+cLen])
				}
				// Right border segment.
				rStart := (bsW - borderRight) - localX0
				if rStart < 0 {
					rStart = 0
				}
				if rStart < r.Dx() && localX1 > bsW-borderRight {
					rEnd := r.Dx()
					rFbOff := fbRowOff + rStart*4
					rBsOff := bsRowOff + rStart*4
					rLen := (rEnd - rStart) * 4
					fillDesktopBG(fb[rFbOff : rFbOff+rLen])
					blitScanlineAlpha(fb[rFbOff:rFbOff+rLen], bs[rBsOff:rBsOff+rLen])
				}
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
	// Buffer starts zeroed (alpha=0 = fully transparent).
	// NeuBox shadows will composite with proper alpha via draw.Over,
	// and the face fill writes opaque pixels (alpha=255) on top.

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

// --- Drag compositing helpers ---

// windowScreenRect returns the full window rect (including borders) in screen
// coordinates.
func windowScreenRect(ta *trackedApp) image.Rectangle {
	ox, oy := screenOrigin(ta)
	return image.Rect(ox, oy, ox+int(ta.bsWidth), oy+int(ta.bsHeight))
}

// exposedRegionExcluding is like exposedRegion but ignores excludeSID when
// computing occlusion. Used to render the drag background as if the dragged
// window doesn't exist.
func exposedRegionExcluding(sid, excludeSID int) []image.Rectangle {
	ta, ok := trackedApps[sid]
	if !ok {
		return nil
	}
	ox, oy := screenOrigin(ta)
	winRect := image.Rect(ox, oy, ox+int(ta.bsWidth), oy+int(ta.bsHeight))
	screenRect := image.Rect(0, 0, int(displayWidth), int(displayHeight))
	winRect = winRect.Intersect(screenRect)
	if winRect.Empty() {
		return nil
	}
	rects := []image.Rectangle{winRect}

	for _, aboveSID := range zOrder {
		if aboveSID == sid {
			break
		}
		if aboveSID == excludeSID {
			continue // skip the dragged window
		}
		above, ok := trackedApps[aboveSID]
		if !ok {
			continue
		}
		aox, aoy := screenOrigin(above)
		aboveRect := image.Rect(aox, aoy, aox+int(above.bsWidth), aoy+int(above.bsHeight))
		var next []image.Rectangle
		for _, r := range rects {
			next = append(next, rectSubtract(r, aboveRect)...)
		}
		rects = next
	}
	return rects
}

// copyRectFromBuffer copies a rectangle of pixels from src buffer to dst buffer.
// Both buffers have the same stride (screen-width * 4). Only copies within bounds.
func copyRectFromBuffer(dst []byte, dstStride int, src []byte, srcStride int, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dstOff := y*dstStride + r.Min.X*4
		srcOff := y*srcStride + r.Min.X*4
		w := r.Dx() * 4
		if dstOff >= 0 && dstOff+w <= len(dst) && srcOff >= 0 && srcOff+w <= len(src) {
			copy(dst[dstOff:dstOff+w], src[srcOff:srcOff+w])
		}
	}
}

// startDragComposite pre-renders all windows except dragSID into dragBG.
// Called when a titlebar drag begins.
func startDragComposite(sid int) {
	dw := int(displayWidth)
	dh := int(displayHeight)
	stride := dw * 4
	bufSize := stride * dh

	// Allocate drag background buffer lazily.
	if dragBG == nil || len(dragBG) < bufSize {
		pages := (bufSize + 4095) / 4096
		var err error
		dragBG, err = mem.AllocPagesSlice(pages, 0) // regular pages
		if err != nil {
			sys.UartWriteString("[rachel:drag] dragBG alloc failed, falling back\n")
			dragActive = false
			return
		}
		sys.UartWriteString(fmt.Sprintf("[rachel:drag] dragBG allocated: %d bytes (%d pages)\n", bufSize, pages))
	}
	dragBGStride = stride

	// Fill with desktop background color.
	for i := 0; i+3 < bufSize; i += 4 {
		dragBG[i] = desktopBG.B
		dragBG[i+1] = desktopBG.G
		dragBG[i+2] = desktopBG.R
		dragBG[i+3] = desktopBG.A
	}

	// Render all windows except the dragged one, back-to-front.
	for i := len(zOrder) - 1; i >= 0; i-- {
		otherSID := zOrder[i]
		if otherSID == sid {
			continue
		}
		ta, ok := trackedApps[otherSID]
		if !ok || ta.backingStore == nil {
			continue
		}
		regions := exposedRegionExcluding(otherSID, sid)
		blitWindowToBuffer(otherSID, regions, dragBG, dragBGStride)
	}

	ta := trackedApps[sid]
	dragActive = true
	dragSID = sid
	dragPrevRect = windowScreenRect(ta)
	sys.UartWriteString(fmt.Sprintf("[rachel:drag] startDragComposite SID=%d prev=%v\n", sid, dragPrevRect))
}

// endDragComposite cleans up after a titlebar drag ends.
func endDragComposite() {
	dragActive = false
	timedBlitAllWindows()
}

// blitWindowToBuffer is like blitWindow but renders to an arbitrary buffer
// instead of fbPix. Used for rendering the drag background.
func blitWindowToBuffer(sid int, regions []image.Rectangle, buf []byte, bufStride int) {
	ta, ok := trackedApps[sid]
	if !ok || ta.backingStore == nil {
		return
	}
	bs := ta.backingStore
	bsStride := int(ta.bsStride)
	bsW := int(ta.bsWidth)
	bsH := int(ta.bsHeight)
	winX, winY := screenOrigin(ta)

	for _, r := range regions {
		localX0 := r.Min.X - winX
		localX1 := r.Max.X - winX

		for y := r.Min.Y; y < r.Max.Y; y++ {
			localY := y - winY
			bufRowOff := y*bufStride + r.Min.X*4
			bsRowOff := localY*bsStride + localX0*4
			w := r.Dx() * 4

			if bufRowOff < 0 || bufRowOff+w > len(buf) || bsRowOff < 0 || bsRowOff+w > len(bs) {
				continue
			}

			inBorderRow := localY < borderTop || localY >= bsH-borderBottom
			if inBorderRow {
				blitScanlineAlpha(buf[bufRowOff:bufRowOff+w], bs[bsRowOff:bsRowOff+w])
			} else {
				cStart := borderLeft - localX0
				if cStart < 0 {
					cStart = 0
				}
				cEnd := (bsW - borderRight) - localX0
				if cEnd > r.Dx() {
					cEnd = r.Dx()
				}
				if localX0 < borderLeft {
					lEnd := borderLeft - localX0
					if lEnd > r.Dx() {
						lEnd = r.Dx()
					}
					blitScanlineAlpha(buf[bufRowOff:bufRowOff+lEnd*4], bs[bsRowOff:bsRowOff+lEnd*4])
				}
				if cStart < cEnd {
					cBufOff := bufRowOff + cStart*4
					cBsOff := bsRowOff + cStart*4
					cLen := (cEnd - cStart) * 4
					copy(buf[cBufOff:cBufOff+cLen], bs[cBsOff:cBsOff+cLen])
				}
				if localX1 > bsW-borderRight {
					rStart := (bsW - borderRight) - localX0
					if rStart < 0 {
						rStart = 0
					}
					if rStart < r.Dx() {
						rEnd := r.Dx()
						rBufOff := bufRowOff + rStart*4
						rBsOff := bsRowOff + rStart*4
						rLen := (rEnd - rStart) * 4
						blitScanlineAlpha(buf[rBufOff:rBufOff+rLen], bs[rBsOff:rBsOff+rLen])
					}
				}
			}
		}
	}
}

// --- Blit timing instrumentation ---
//
// Measures the three phases of each single-window blit:
//   occlusion  = exposedRegion() computation
//   copy       = blitWindow() pixel copy to framebuffer
//   flush      = flushRect() → VirtIO GPU transfer+flush syscall
//
// Also measures blitAllWindows() as a whole.

// tickBudgetUs is the tick interval in microseconds, set from kernel config.
// Updated by setTickBudgetHz at startup.
var tickBudgetUs int64 = 3030 // ~330 Hz (1_000_000 / 330)

func setTickBudgetHz(hz int) {
	if hz > 0 {
		tickBudgetUs = 1_000_000 / int64(hz)
	}
}

// Per-phase accumulators (microseconds).
var (
	btSamples   int64
	btOcclusUs  int64
	btCopyUs    int64
	btFlushUs   int64
	btTotalUs   int64 // occlusion+copy+flush
	btMaxUs     int64
	btMaxOccUs  int64
	btMaxCopyUs int64
	btMaxFlshUs int64

	// blitAllWindows timing
	btAllCount   int64
	btAllTotalUs int64
	btAllMaxUs   int64
)

// blitTimingRecord records one blit's phase durations.
func blitTimingRecord(occUs, copyUs, flushUs int64) {
	btSamples++
	btOcclusUs += occUs
	btCopyUs += copyUs
	btFlushUs += flushUs
	total := occUs + copyUs + flushUs
	btTotalUs += total
	if total > btMaxUs {
		btMaxUs = total
	}
	if occUs > btMaxOccUs {
		btMaxOccUs = occUs
	}
	if copyUs > btMaxCopyUs {
		btMaxCopyUs = copyUs
	}
	if flushUs > btMaxFlshUs {
		btMaxFlshUs = flushUs
	}
}

// blitAllTimingRecord records one blitAllWindows duration.
func blitAllTimingRecord(totalUs int64) {
	btAllCount++
	btAllTotalUs += totalUs
	if totalUs > btAllMaxUs {
		btAllMaxUs = totalUs
	}
}

// blitTimingReport prints accumulated stats and resets counters.
func blitTimingReport() {
	if btSamples == 0 {
		return
	}
	avgTotal := btTotalUs / btSamples
	avgOcc := btOcclusUs / btSamples
	avgCopy := btCopyUs / btSamples
	avgFlush := btFlushUs / btSamples

	pctAvg := avgTotal * 100 / tickBudgetUs
	pctMax := btMaxUs * 100 / tickBudgetUs

	sys.UartWriteString("[blit:timing] n=" + strconv.FormatInt(btSamples, 10) +
		" avg=" + strconv.FormatInt(avgTotal, 10) + "us" +
		" (occ=" + strconv.FormatInt(avgOcc, 10) +
		" copy=" + strconv.FormatInt(avgCopy, 10) +
		" flush=" + strconv.FormatInt(avgFlush, 10) + ")" +
		" max=" + strconv.FormatInt(btMaxUs, 10) + "us" +
		" (occ=" + strconv.FormatInt(btMaxOccUs, 10) +
		" copy=" + strconv.FormatInt(btMaxCopyUs, 10) +
		" flush=" + strconv.FormatInt(btMaxFlshUs, 10) + ")" +
		" tick=" + strconv.FormatInt(tickBudgetUs, 10) + "us" +
		" avg%=" + strconv.FormatInt(pctAvg, 10) +
		" max%=" + strconv.FormatInt(pctMax, 10) + "\n")

	if btAllCount > 0 {
		avgAll := btAllTotalUs / btAllCount
		pctAll := btAllMaxUs * 100 / tickBudgetUs
		sys.UartWriteString("[blit:timing] blitAll n=" + strconv.FormatInt(btAllCount, 10) +
			" avg=" + strconv.FormatInt(avgAll, 10) + "us" +
			" max=" + strconv.FormatInt(btAllMaxUs, 10) + "us" +
			" max%=" + strconv.FormatInt(pctAll, 10) + "\n")
	}

	// Reset.
	btSamples = 0
	btOcclusUs = 0
	btCopyUs = 0
	btFlushUs = 0
	btTotalUs = 0
	btMaxUs = 0
	btMaxOccUs = 0
	btMaxCopyUs = 0
	btMaxFlshUs = 0
	btAllCount = 0
	btAllTotalUs = 0
	btAllMaxUs = 0
}

// timedExposedRegion wraps exposedRegion with timing.
func timedExposedRegion(sid int) ([]image.Rectangle, time.Duration) {
	t0 := time.Now()
	r := exposedRegion(sid)
	return r, time.Since(t0)
}

// timedBlitWindow wraps blitWindow with timing.
func timedBlitWindow(sid int, regions []image.Rectangle, fb []byte, fbStride int, focused bool) time.Duration {
	t0 := time.Now()
	blitWindow(sid, regions, fb, fbStride, focused)
	return time.Since(t0)
}

// timedFlushRect wraps flushRect with timing.
func timedFlushRect(x, y, w, h int) time.Duration {
	t0 := time.Now()
	flushRect(x, y, w, h)
	return time.Since(t0)
}

// timedBlitAllWindows wraps blitAllWindows with timing.
func timedBlitAllWindows() {
	t0 := time.Now()
	blitAllWindows()
	blitAllTimingRecord(time.Since(t0).Microseconds())
}
