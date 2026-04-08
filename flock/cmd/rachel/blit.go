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
	"math"
"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazarin/textshape"
	"strconv"
	"time"
)

// windowTitleBar is the TitleBar implementation used for all managed windows.
// Set during rachel's main() after fonts are available. If nil, title bars
// are not drawn (NeuBox decoration only).
var windowTitleBar mancini.TitleBar

// titleGlyphProvider renders glyphs for decoration title text.
// Uses the internal (in-process) provider when fontsvc runs inside rachel,
// or the IPC-based provider for external fontsvc.
var titleGlyphProvider textshape.GlyphProvider

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

// faceScreenRect returns the face area (titlebar + content, excluding shadow
// borders) in screen coordinates. For unfocused windows, only this region is
// blitted — no neumorphic shadows.
func faceScreenRect(ta *trackedApp) image.Rectangle {
	ox, oy := screenOrigin(ta)
	return image.Rect(
		ox+borderLeft,
		oy+shadowTop,
		ox+int(ta.bsWidth)-borderRight,
		oy+int(ta.bsHeight)-borderBottom,
	)
}

// lightShadowPad is the shadow extent for focused window decoration.
// neuRaised uses pad = maxOff + ceil(maxBlur*3) + 2.
// Custom: maxOff=4, maxBlur=3 → pad = 4 + 9 + 2 = 15.
var lightShadowPad = 15

// windowVisibleRect returns the screen rectangle a window actually occupies.
// Focused windows use the face rect expanded by the light shadow padding.
// Unfocused windows use only the face rect (titlebar + content).
func windowVisibleRect(ta *trackedApp, focused bool) image.Rectangle {
	face := faceScreenRect(ta)
	if focused {
		return image.Rect(
			face.Min.X-lightShadowPad,
			face.Min.Y-lightShadowPad,
			face.Max.X+lightShadowPad,
			face.Max.Y+lightShadowPad,
		)
	}
	return face
}

// exposedRegion returns the set of non-overlapping rectangles that
// represent the visible portion of the window identified by sid.
// It subtracts the bounds of every window above sid in z-order.
// Unfocused windows use only their face rect (no shadow borders) for
// both their own bounds and for occluding windows below.
func exposedRegion(sid int) []image.Rectangle {
	ta, ok := trackedApps[sid]
	if !ok {
		return nil
	}
	focused := sid == mouseFocusSID
	winRect := windowVisibleRect(ta, focused)

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
		aboveFocused := aboveSID == mouseFocusSID
		aboveRect := windowVisibleRect(above, aboveFocused)

		// Subtract aboveRect from every rect in the current list.
		var next []image.Rectangle
		for _, r := range rects {
			next = append(next, rectSubtract(r, aboveRect)...)
		}
		rects = next
	}
	return rects
}

// borderZoneRects returns the four border strips (top, bottom, left, right)
// for a tracked window in screen coordinates.
func borderZoneRects(ta *trackedApp) [4]image.Rectangle {
	ox, oy := screenOrigin(ta)
	bsW, bsH := int(ta.bsWidth), int(ta.bsHeight)
	return [4]image.Rectangle{
		image.Rect(ox, oy, ox+bsW, oy+borderTop),                           // top
		image.Rect(ox, oy+bsH-borderBottom, ox+bsW, oy+bsH),               // bottom
		image.Rect(ox, oy+borderTop, ox+borderLeft, oy+bsH-borderBottom),   // left
		image.Rect(ox+bsW-borderRight, oy+borderTop, ox+bsW, oy+bsH-borderBottom), // right
	}
}

// restoreBorderBackground prepares the framebuffer under a window's border
// zones for correct alpha compositing. It fills the border regions with the
// desktop background, then re-blits all windows behind sid (back-to-front)
// that overlap the border zones. After this call, the FB contains the correct
// background for the target window's semi-transparent shadows to composite over.
func restoreBorderBackground(sid int, exposed []image.Rectangle, fb []byte, fbStride int) {
	ta, ok := trackedApps[sid]
	if !ok {
		return
	}

	// Intersect border zone rects with exposed regions.
	borders := borderZoneRects(ta)
	var borderExposed []image.Rectangle
	for _, b := range borders {
		for _, e := range exposed {
			isect := b.Intersect(e)
			if !isect.Empty() {
				borderExposed = append(borderExposed, isect)
			}
		}
	}
	if len(borderExposed) == 0 {
		return
	}

	// Fill border-exposed rects with desktop BG.
	for _, r := range borderExposed {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			off := y*fbStride + r.Min.X*4
			w := r.Dx() * 4
			if off >= 0 && off+w <= len(fb) {
				fillDesktopBG(fb[off : off+w])
			}
		}
	}

	// Re-blit windows behind sid that overlap the border zones, back-to-front.
	for i := len(zOrder) - 1; i >= 0; i-- {
		belowSID := zOrder[i]
		if belowSID == sid {
			break
		}
		belowTA, ok := trackedApps[belowSID]
		if !ok || belowTA.backingStore == nil {
			continue
		}
		belowOX, belowOY := screenOrigin(belowTA)
		belowRect := image.Rect(belowOX, belowOY,
			belowOX+int(belowTA.bsWidth), belowOY+int(belowTA.bsHeight))

		var clipped []image.Rectangle
		for _, r := range borderExposed {
			isect := r.Intersect(belowRect)
			if !isect.Empty() {
				clipped = append(clipped, isect)
			}
		}
		if len(clipped) > 0 {
			blitWindow(belowSID, clipped, fb, fbStride, belowSID == mouseFocusSID)
		}
	}
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

	// Clip content compositing to the region the app has actually drawn.
	// During resize, the window may be larger than what the app has rendered;
	// pixels beyond the drawn region are not blitted, so the drag background
	// (or desktop) shows through instead of black.
	drawnRight := bsW - borderRight // default: full app area
	drawnBottom := bsH - borderBottom
	if ta.lastBlitAppW > 0 && int(ta.lastBlitAppW)+borderLeft < drawnRight {
		drawnRight = int(ta.lastBlitAppW) + borderLeft
	}
	if ta.lastBlitAppH > 0 && int(ta.lastBlitAppH)+borderTop < drawnBottom {
		drawnBottom = int(ta.lastBlitAppH) + borderTop
	}

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
				// Entire row is border zone — alpha blend over existing FB.
				// Caller must ensure the FB already has the correct background
				// (desktop BG + lower windows) via restoreBorderBackground.
				blitScanlineAlpha(fb[fbRowOff:fbRowOff+w], bs[bsRowOff:bsRowOff+w])
			} else {
				// Skip content rows beyond what the app has drawn.
				if localY >= drawnBottom {
					continue
				}

				// Content row — split into left-border | content | right-border.
				// Left border segment.
				if localX0 < borderLeft {
					lEnd := borderLeft - localX0
					if lEnd > r.Dx() {
						lEnd = r.Dx()
					}
					blitScanlineAlpha(fb[fbRowOff:fbRowOff+lEnd*4], bs[bsRowOff:bsRowOff+lEnd*4])
				}
				// Content segment (opaque fast copy), clipped to drawn width.
				cStart := borderLeft - localX0
				if cStart < 0 {
					cStart = 0
				}
				cEnd := drawnRight - localX0
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
					blitScanlineAlpha(fb[rFbOff:rFbOff+rLen], bs[rBsOff:rBsOff+rLen])
				}
			}
		}
	}
}

// renderDecorOnce renders decoration into a temporary buffer for the given
// depth/state and returns the pixels. The buffer has the same layout as the
// backing store.
//
// Focused (Raised): light neumorphic shadows with a transparent face. The
// shadow pixels remain as pure alpha layers so they composite correctly over
// the actual background at blit time.
//
// Unfocused (Flush): opaque surface-colored face with no neumorphic shadows.
// Only the face area (titlebar + content) is meaningful; border zones are
// transparent and never blitted for unfocused windows.
// decorPhaseStats holds per-phase timing for a single renderDecorOnce call.
type decorPhaseStats struct {
	bufAllocNs int64
	dcCreateNs int64
	neuBoxNs   int64 // NeuBoxWith total (only for Raised)
	fillNs     int64 // rounded rect fill (only for Flush)
	titleBarNs int64
}

// decorPhaseAccum accumulates per-phase timing across a resize operation.
var decorPhaseAccum struct {
	active           bool
	focusedCalls     int
	unfocusedCalls   int
	// Focused (Raised) phase totals.
	fBufAlloc   time.Duration
	fDCCreate   time.Duration
	fNeuBox     time.Duration
	fTitleBar   time.Duration
	// Unfocused (Flush) phase totals.
	uBufAlloc   time.Duration
	uDCCreate   time.Duration
	uFill       time.Duration
	uTitleBar   time.Duration
	// drawPerf snapshots for isolating NeuBox internals.
	fAllocNs   int64
	fGGDrawNs  int64
	fConvertNs int64
	fBlurNs    int64
	fComposeNs int64
	fFaceNs    int64
	fBlurCount int64
}

func decorPhaseReset() {
	decorPhaseAccum = struct {
		active           bool
		focusedCalls     int
		unfocusedCalls   int
		fBufAlloc   time.Duration
		fDCCreate   time.Duration
		fNeuBox     time.Duration
		fTitleBar   time.Duration
		uBufAlloc   time.Duration
		uDCCreate   time.Duration
		uFill       time.Duration
		uTitleBar   time.Duration
		fAllocNs   int64
		fGGDrawNs  int64
		fConvertNs int64
		fBlurNs    int64
		fComposeNs int64
		fFaceNs    int64
		fBlurCount int64
	}{active: true}
}

func decorPhaseReport() {
	a := &decorPhaseAccum
	if !a.active || (a.focusedCalls == 0 && a.unfocusedCalls == 0) {
		return
	}
	a.active = false

	fmt.Printf("\n=== DECORATION PHASE BREAKDOWN ===\n")
	if a.focusedCalls > 0 {
		fmt.Printf("Focused (Raised) — %d calls:\n", a.focusedCalls)
		fmt.Printf("  buf alloc:   %s\n", a.fBufAlloc)
		fmt.Printf("  DC create:   %s\n", a.fDCCreate)
		fmt.Printf("  NeuBoxWith:  %s\n", a.fNeuBox)
		// NeuBox internal breakdown from drawPerf deltas.
		fmt.Printf("    shadow alloc:  %d us\n", a.fAllocNs/1000)
		fmt.Printf("    gg rasterize:  %d us\n", a.fGGDrawNs/1000)
		fmt.Printf("    RGBA→NRGBA:    %d us\n", a.fConvertNs/1000)
		fmt.Printf("    gaussian blur: %d us (%d blurs)\n", a.fBlurNs/1000, a.fBlurCount)
		fmt.Printf("    compositing:   %d us\n", a.fComposeNs/1000)
		fmt.Printf("    face fill:     %d us\n", a.fFaceNs/1000)
		fmt.Printf("  title bar:   %s\n", a.fTitleBar)
		total := a.fBufAlloc + a.fDCCreate + a.fNeuBox + a.fTitleBar
		if a.focusedCalls > 0 {
			fmt.Printf("  avg/call:    %s\n", total/time.Duration(a.focusedCalls))
		}
	}
	if a.unfocusedCalls > 0 {
		fmt.Printf("Unfocused (Flush) — %d calls:\n", a.unfocusedCalls)
		fmt.Printf("  buf alloc:   %s\n", a.uBufAlloc)
		fmt.Printf("  DC create:   %s\n", a.uDCCreate)
		fmt.Printf("  rect fill:   %s\n", a.uFill)
		fmt.Printf("  title bar:   %s\n", a.uTitleBar)
	}
	fmt.Printf("==================================\n\n")
}

func renderDecorOnce(ta *trackedApp, depth mancini.NeuDepth, state mancini.WindowState) []byte {
	tw := int(ta.bsWidth)
	th := int(ta.bsHeight)
	stride := int(ta.bsStride)

	t0 := time.Now()
	buf := make([]byte, len(ta.backingStore))
	bufAllocDur := time.Since(t0)

	tmpImg := &image.RGBA{
		Pix:    buf,
		Stride: stride,
		Rect:   image.Rect(0, 0, tw, th),
	}

	t1 := time.Now()
	dc := mancini.NewDrawContextForImage(tmpImg, titleGlyphProvider)
	dcCreateDur := time.Since(t1)

	x1 := float64(borderLeft)
	y1 := float64(shadowTop)
	x2 := float64(tw - borderRight)
	y2 := float64(th - borderBottom)
	r := wmTheme.CornerRadius()

	var neuBoxDur, fillDur time.Duration

	if depth == mancini.Raised {
		// Snapshot drawPerf before NeuBoxWith.
		dp := std.GetDrawPerf()
		preAlloc := dp.AllocNs.Load()
		preGG := dp.GGDrawNs.Load()
		preConv := dp.ConvertNs.Load()
		preBlur := dp.BlurNs.Load()
		preBlurC := dp.BlurCount.Load()
		preComp := dp.ComposeNs.Load()
		preFace := dp.FaceNs.Load()

		faceColor := pal.Surface()
		neuP := &mancini.NeuParams{
			Raised: mancini.RaisedParams{
				DarkOff:    4,
				DarkBlur:   3,
				DarkAlpha:  200,
				LightOff:   2,
				LightBlur:  2.5,
				LightAlpha: 220,
			},
		}
		t2 := time.Now()
		std.NeuBoxWith(pal, dc, depth, x1, y1, x2, y2, r, faceColor, neuP)
		neuBoxDur = time.Since(t2)

		// Accumulate drawPerf deltas for focused renders.
		if decorPhaseAccum.active {
			decorPhaseAccum.fAllocNs += dp.AllocNs.Load() - preAlloc
			decorPhaseAccum.fGGDrawNs += dp.GGDrawNs.Load() - preGG
			decorPhaseAccum.fConvertNs += dp.ConvertNs.Load() - preConv
			decorPhaseAccum.fBlurNs += dp.BlurNs.Load() - preBlur
			decorPhaseAccum.fBlurCount += dp.BlurCount.Load() - preBlurC
			decorPhaseAccum.fComposeNs += dp.ComposeNs.Load() - preComp
			decorPhaseAccum.fFaceNs += dp.FaceNs.Load() - preFace
		}
	} else {
		faceColor := pal.Surface()
		t2 := time.Now()
		dc.SetColor(faceColor)
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Fill()
		fillDur = time.Since(t2)
	}

	// Title bar drawn AFTER face/shadows.
	var titleBarDur time.Duration
	if windowTitleBar != nil {
		tbX := float64(borderLeft) + 4
		tbY := float64(shadowTop) + 2
		tbW := float64(tw-borderRight) - tbX - 4
		tbH := float64(titleBarHeight)
		t3 := time.Now()
		windowTitleBar.DrawTitleBar(dc, ta.title, state, mancini.AppWindowType, tbX, tbY, tbW, tbH)
		titleBarDur = time.Since(t3)
	}

	// Resize handle semi-circles on left, right, bottom edges.
	drawResizeHandles(dc, tw, th)

	// Accumulate into phase stats if tracking.
	if decorPhaseAccum.active {
		if depth == mancini.Raised {
			decorPhaseAccum.focusedCalls++
			decorPhaseAccum.fBufAlloc += bufAllocDur
			decorPhaseAccum.fDCCreate += dcCreateDur
			decorPhaseAccum.fNeuBox += neuBoxDur
			decorPhaseAccum.fTitleBar += titleBarDur
		} else {
			decorPhaseAccum.unfocusedCalls++
			decorPhaseAccum.uBufAlloc += bufAllocDur
			decorPhaseAccum.uDCCreate += dcCreateDur
			decorPhaseAccum.uFill += fillDur
			decorPhaseAccum.uTitleBar += titleBarDur
		}
	}

	return buf
}

// resizeHandleRadius is the radius of the semi-circle resize handles
// drawn on the left, right, and bottom edges of each window decoration.
const resizeHandleRadius = 12

// drawSemiCircleArc draws a semi-circular arc path from startAngle to
// endAngle (radians, 0=right, Pi/2=down in screen coords). The path is
// closed with a straight line across the flat edge. If fill is true,
// the shape is filled; otherwise it is stroked.
func drawSemiCircleArc(dc textshape.DrawContext, cx, cy, r, startAngle, endAngle float64, fill bool) {
	const n = 8
	for i := 0; i < n; i++ {
		t1 := float64(i) / float64(n)
		t2 := float64(i+1) / float64(n)
		aa1 := startAngle + (endAngle-startAngle)*t1
		aa2 := startAngle + (endAngle-startAngle)*t2
		x0 := cx + r*math.Cos(aa1)
		y0 := cy + r*math.Sin(aa1)
		xMid := cx + r*math.Cos((aa1+aa2)/2)
		yMid := cy + r*math.Sin((aa1+aa2)/2)
		x2 := cx + r*math.Cos(aa2)
		y2 := cy + r*math.Sin(aa2)
		qcx := 2*xMid - x0/2 - x2/2
		qcy := 2*yMid - y0/2 - y2/2
		if i == 0 {
			dc.MoveTo(x0, y0)
		}
		dc.QuadraticTo(qcx, qcy, x2, y2)
	}
	dc.ClosePath()
	if fill {
		dc.Fill()
	} else {
		dc.Stroke()
	}
}

// drawResizeHandles draws semi-circle resize handles on the left, right,
// and bottom edges of the decoration buffer. Each handle is centered at
// the midpoint of its edge, with the flat side flush against the app area
// boundary and the curved part bulging into the border zone.
// Filled with SurfaceTint and outlined with DarkShadow.
func drawResizeHandles(dc textshape.DrawContext, tw, th int) {
	r := float64(resizeHandleRadius)

	// App area in buffer coordinates.
	appMidX := float64(borderLeft+tw-borderRight) / 2
	appMidY := float64(borderTop+th-borderBottom) / 2

	// Bottom handle: flat edge at top, bulges down.
	drawResizeHandle(dc, appMidX, float64(th-borderBottom), r, 0, math.Pi)

	// Left handle: flat edge at right, bulges left.
	drawResizeHandle(dc, float64(borderLeft), appMidY, r, math.Pi/2, 3*math.Pi/2)

	// Right handle: flat edge at left, bulges right.
	drawResizeHandle(dc, float64(tw-borderRight), appMidY, r, -math.Pi/2, math.Pi/2)
}

// drawResizeHandle draws a single semi-circle: SurfaceTint fill then
// DarkShadow outline on top.
func drawResizeHandle(dc textshape.DrawContext, cx, cy, r, startAngle, endAngle float64) {
	// Fill first.
	dc.SetColor(pal.SurfaceTint())
	drawSemiCircleArc(dc, cx, cy, r, startAngle, endAngle, true)

	// Outline on top of the fill.
	dc.SetColor(pal.DarkShadow())
	dc.SetLineWidth(1.5)
	drawSemiCircleArc(dc, cx, cy, r, startAngle, endAngle, false)
}

// preRenderDecorations pre-renders both focused (Raised) and unfocused
// (Flush) decorations into cached buffers and applies the focused state
// to the backing store. Called once at allocation time.
// decorTimingStats tracks decoration rendering times during a resize operation.
var decorTiming struct {
	active      bool
	calls       int
	renderTotal time.Duration // total time in renderDecorOnce (both focused + unfocused)
	applyTotal  time.Duration // total time in applyDecorations
	renderMin   time.Duration
	renderMax   time.Duration
}

func decorTimingReset() {
	decorTiming = struct {
		active      bool
		calls       int
		renderTotal time.Duration
		applyTotal  time.Duration
		renderMin   time.Duration
		renderMax   time.Duration
	}{active: true, renderMin: time.Hour}
}

func decorTimingReport() {
	if !decorTiming.active || decorTiming.calls == 0 {
		return
	}
	decorTiming.active = false
	avg := decorTiming.renderTotal / time.Duration(decorTiming.calls)
	fmt.Printf("\n=== RACHEL DECORATION TIMING ===\n")
	fmt.Printf("preRenderDecorations calls: %d\n", decorTiming.calls)
	fmt.Printf("  Render total:  %s (avg %s, min %s, max %s)\n",
		decorTiming.renderTotal, avg, decorTiming.renderMin, decorTiming.renderMax)
	fmt.Printf("  Apply total:   %s\n", decorTiming.applyTotal)
	fmt.Printf("================================\n\n")
}

func preRenderDecorations(ta *trackedApp) {
	t0 := time.Now()
	ta.decorFocused = renderDecorOnce(ta, mancini.Raised, mancini.Active)
	ta.decorUnfocused = renderDecorOnce(ta, mancini.Flush, mancini.Inactive)
	renderDur := time.Since(t0)

	t1 := time.Now()
	applyDecorations(ta, true)
	applyDur := time.Since(t1)

	if decorTiming.active {
		decorTiming.calls++
		decorTiming.renderTotal += renderDur
		decorTiming.applyTotal += applyDur
		if renderDur < decorTiming.renderMin {
			decorTiming.renderMin = renderDur
		}
		if renderDur > decorTiming.renderMax {
			decorTiming.renderMax = renderDur
		}
	}

	sys.UartWriteString(fmt.Sprintf("[rachel:decor] pre-rendered %dx%d title=%q render=%s apply=%s\n",
		ta.bsWidth, ta.bsHeight, ta.title, renderDur, applyDur))
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
	focused := sid == mouseFocusSID
	winRect := windowVisibleRect(ta, focused)
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
		aboveFocused := aboveSID == mouseFocusSID
		aboveRect := windowVisibleRect(above, aboveFocused)
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
		dragBG, err = mem.AllocPagesSlice(pages, mem.PageShared)
		if err != nil {
			sys.UartWriteString(fmt.Sprintf("[rachel:drag] dragBG alloc failed: %v (wanted %d pages for %d bytes)\n", err, pages, bufSize))
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
	dragPrevRect = windowVisibleRect(ta, true) // focused = face + light shadow pad
	sys.UartWriteString(fmt.Sprintf("[rachel:drag] startDragComposite SID=%d prev=%v\n", sid, dragPrevRect))
}

// endDragComposite cleans up after a titlebar drag ends.
func endDragComposite() {
	dragActive = false
	timedBlitAllWindows()
}

// compositeDragWindow re-composites the dragged window over the fixed background.
// Used during resize drag when the window size changes but the dragBG stays fixed.
func compositeDragWindow() {
	if !dragActive {
		return
	}
	ta, ok := trackedApps[dragSID]
	if !ok || ta.backingStore == nil {
		return
	}

	oldRect := dragPrevRect
	newRect := windowVisibleRect(ta, true)

	screen := image.Rect(0, 0, int(displayWidth), int(displayHeight))
	oldClip := oldRect.Intersect(screen)
	newClip := newRect.Intersect(screen)

	// 1. Restore newly-exposed area from drag background.
	if !oldClip.Empty() {
		exposed := rectSubtract(oldClip, newClip)
		for _, r := range exposed {
			copyRectFromBuffer(fbPix, fbStride, dragBG, dragBGStride, r)
		}
	}

	// 2. Restore shadow zone from dragBG.
	if !newClip.Empty() {
		face := faceScreenRect(ta).Intersect(screen)
		shadowStrips := rectSubtract(newClip, face)
		for _, s := range shadowStrips {
			copyRectFromBuffer(fbPix, fbStride, dragBG, dragBGStride, s)
		}

		// 3. Blit the resized window.
		blitWindow(dragSID, []image.Rectangle{newClip}, fbPix, fbStride, true)
	}

	// 4. Flush union of old and new rects.
	union := oldClip.Union(newClip)
	if !union.Empty() {
		flushRect(union.Min.X, union.Min.Y, union.Dx(), union.Dy())
	}

	dragPrevRect = newRect
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
