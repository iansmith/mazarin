// overlay.go — Overlay allocation, compositing, and lifecycle for rachel.
//
// Overlays allow shepherds to draw outside their window bounds (e.g. popup
// menus, tooltips). Rachel allocates the shared pixel buffer, composites it
// on top of all windows during blit, and auto-dismisses the overlay when the
// app loses focus. Input routing uses normal mouse events (MouseMove,
// MouseRelease) via the DragAgent — overlays do not intercept input.
package main

import (
	"fmt"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	"unsafe"
)

// nextOverlayID is a monotonically increasing counter for overlay IDs.
var nextOverlayID int32

// overlayBlitCount tracks how many overlay blits have occurred (for log throttling).
var overlayBlitCount int

// handleOverlayAllocate processes an OverlayAllocate request from a shepherd.
// It allocates shared pages for the overlay pixel buffer, records the overlay
// state in the trackedApp, and sends OverlayReady back to the shepherd.
func handleOverlayAllocate(senderSID int, msg wm.OverlayAllocate) {
	ta, ok := trackedApps[senderSID]
	if !ok {
		sys.UartWriteString(fmt.Sprintf("[rachel:overlay] OverlayAllocate from unknown SID %d\n", senderSID))
		return
	}
	if ta.overlayActive {
		sys.UartWriteString(fmt.Sprintf("[rachel:overlay] SID %d already has overlay %d, ignoring\n",
			senderSID, ta.overlayID))
		return
	}

	// Clamp to display bounds.
	x1, y1 := msg.ScreenX1, msg.ScreenY1
	x2, y2 := msg.ScreenX2, msg.ScreenY2
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > displayWidth {
		x2 = displayWidth
	}
	if y2 > displayHeight {
		y2 = displayHeight
	}
	w := x2 - x1
	h := y2 - y1
	if w <= 0 || h <= 0 {
		sys.UartWriteString(fmt.Sprintf("[rachel:overlay] degenerate rect (%d,%d)-(%d,%d)\n",
			x1, y1, x2, y2))
		return
	}

	stride := w * 4
	totalBytes := int(stride) * int(h)
	nPages := (totalBytes + 4095) / 4096

	ovSlice, err := mem.AllocPagesSlice(nPages, mem.PageShared)
	if err != nil {
		sys.UartWriteString("[rachel:overlay] alloc failed\n")
		return
	}

	// Clear to transparent.
	for i := range ovSlice {
		ovSlice[i] = 0
	}

	nextOverlayID++
	ovID := nextOverlayID

	ta.overlayActive = true
	ta.overlayID = ovID
	ta.overlayStore = ovSlice
	ta.overlayWidth = w
	ta.overlayHeight = h
	ta.overlayStride = stride
	ta.overlayScreenX = x1
	ta.overlayScreenY = y1

	// Share pages with the requesting shepherd.
	ovVA := uintptr(unsafe.Pointer(&ovSlice[0]))
	clientVA, shareErr := sys.SharePagesWithTarget(senderSID, ovVA, nPages)
	if shareErr != nil {
		sys.UartWriteString("[rachel:overlay] share pages failed\n")
		ta.overlayActive = false
		return
	}

	// Ensure the overlay owner has focus and is raised to front.
	// This pairs with revokeFocus which tears down the overlay on focus loss.
	if !hasFocus(senderSID) {
		grantFocus(senderSID)
	}

	// Send OverlayReady to shepherd.
	ready := wm.EncodeOverlayReady(&wm.OverlayReady{
		OverlayID:   ovID,
		OverlayAddr: int64(clientVA),
		Width:       w,
		Height:      h,
		Stride:      stride,
		ScreenX:     x1,
		ScreenY:     y1,
	})
	if err := uring.Send(senderSID, &ready); err != nil {
		sys.UartWriteString("[rachel:overlay] uring.Send OverlayReady failed\n")
	}
	fmt.Printf("[rachel:overlay] SID %d: overlay %d at (%d,%d) %dx%d\n",
		senderSID, ovID, x1, y1, w, h)
}

// handleOverlayBlit composites the overlay buffer onto the framebuffer.
// The overlay is drawn on top of all windows, using simple alpha compositing.
func handleOverlayBlit(senderSID int) {
	ta, ok := trackedApps[senderSID]
	if !ok || !ta.overlayActive || ta.overlayStore == nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:overlay] blit: no overlay for SID %d (ok=%v active=%v store=%v)\n",
			senderSID, ok, ok && ta.overlayActive, ok && ta.overlayStore != nil))
		return
	}

	ox := int(ta.overlayScreenX)
	oy := int(ta.overlayScreenY)
	ow := int(ta.overlayWidth)
	oh := int(ta.overlayHeight)
	oStride := int(ta.overlayStride)

	// Log first blit only (avoid 10Hz spam from re-compositing).
	if overlayBlitCount == 0 {
		nonZero := 0
		for i := 3; i < len(ta.overlayStore); i += 4 {
			if ta.overlayStore[i] != 0 {
				nonZero++
			}
		}
		fmt.Printf("[rachel:overlay] blit SID=%d at (%d,%d) %dx%d stride=%d nonZeroPx=%d fbStride=%d\n",
			senderSID, ox, oy, ow, oh, oStride, nonZero, fbStride)
	}
	overlayBlitCount++

	// Alpha-composite overlay onto framebuffer.
	for row := 0; row < oh; row++ {
		fbY := oy + row
		if fbY < 0 || fbY >= int(displayHeight) {
			continue
		}
		srcOff := row * oStride
		dstOff := fbY*fbStride + ox*4
		for col := 0; col < ow; col++ {
			fbX := ox + col
			if fbX < 0 || fbX >= int(displayWidth) {
				srcOff += 4
				continue
			}
			// Overlay buffer is RGBA (image.RGBA), framebuffer is BGRA.
			sR := uint32(ta.overlayStore[srcOff+0])
			sG := uint32(ta.overlayStore[srcOff+1])
			sB := uint32(ta.overlayStore[srcOff+2])
			sA := uint32(ta.overlayStore[srcOff+3])

			if sA == 0 {
				srcOff += 4
				dstOff += 4
				continue
			}
			if sA == 255 {
				fbPix[dstOff+0] = uint8(sB)
				fbPix[dstOff+1] = uint8(sG)
				fbPix[dstOff+2] = uint8(sR)
				fbPix[dstOff+3] = 255
			} else {
				dB := uint32(fbPix[dstOff+0])
				dG := uint32(fbPix[dstOff+1])
				dR := uint32(fbPix[dstOff+2])
				fbPix[dstOff+0] = uint8((sB*sA + dB*(255-sA)) / 255)
				fbPix[dstOff+1] = uint8((sG*sA + dG*(255-sA)) / 255)
				fbPix[dstOff+2] = uint8((sR*sA + dR*(255-sA)) / 255)
				fbPix[dstOff+3] = 255
			}
			srcOff += 4
			dstOff += 4
		}
	}

	// Flush the overlay region to GPU.
	flushRect(ox, oy, ow, oh)
}

// teardownOverlay tears down the active overlay for a shepherd.
// Called from both app-initiated release and rachel-initiated focus-loss dismissal.
// Repaints the covered area, clears overlay state, and sends OverlayReleased
// to the shepherd.
func teardownOverlay(sid int, ta *trackedApp) {
	fmt.Printf("[rachel:overlay] SID %d: tearing down overlay %d\n",
		sid, ta.overlayID)

	// Repaint the area that was under the overlay — blit all windows
	// to restore correct content.
	timedBlitAllWindows()

	// Clear overlay state.
	ovID := ta.overlayID
	ta.overlayActive = false
	ta.overlayID = 0
	ta.overlayStore = nil
	ta.overlayWidth = 0
	ta.overlayHeight = 0
	ta.overlayStride = 0
	ta.overlayScreenX = 0
	ta.overlayScreenY = 0

	// Send confirmation to shepherd.
	released := wm.EncodeOverlayReleased(&wm.OverlayReleased{OverlayID: ovID})
	if err := uring.Send(sid, &released); err != nil {
		sys.UartWriteString("[rachel:overlay] uring.Send OverlayReleased failed\n")
	}
}

// handleOverlayRelease processes an app-initiated overlay release request.
func handleOverlayRelease(senderSID int, msg wm.OverlayRelease) {
	ta, ok := trackedApps[senderSID]
	if !ok || !ta.overlayActive {
		return
	}
	if ta.overlayID != msg.OverlayID {
		sys.UartWriteString(fmt.Sprintf("[rachel:overlay] SID %d release ID mismatch: %d != %d\n",
			senderSID, msg.OverlayID, ta.overlayID))
		return
	}
	teardownOverlay(senderSID, ta)
}
