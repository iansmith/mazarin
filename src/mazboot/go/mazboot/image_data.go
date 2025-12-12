package main

import "unsafe"

// Boot Mazarin image data - embedded from assets/boot-mazarin.bin
// Image format: [4 bytes width][4 bytes height][width*height*4 bytes ARGB8888 data]

// External assembly functions that load linker symbols
//
//go:linkname imageDataStart imageDataStart
func imageDataStart() uintptr

//go:linkname imageDataEnd imageDataEnd
func imageDataEnd() uintptr

// GetBootMazarinImageData returns a pointer to the boot Mazarin image data
//
//go:nosplit
func GetBootMazarinImageData() unsafe.Pointer {
	ptr := imageDataStart()
	return unsafe.Pointer(ptr)
}

// GetBootMazarinImageSize returns the size of the boot Mazarin image data
//
//go:nosplit
func GetBootMazarinImageSize() uintptr {
	startAddr := imageDataStart()
	endAddr := imageDataEnd()
	return endAddr - startAddr
}

// RenderImageData renders image data from a buffer in the format:
// [4 bytes width][4 bytes height][width*height*4 bytes ARGB8888 pixels]
// Uses alpha blending if useAlpha is true
// xOffset, yOffset can be negative to position image partially off-screen
//
//go:nosplit
func RenderImageData(data unsafe.Pointer, xOffset, yOffset int32, useAlpha bool) {
	if data == nil {
		return
	}

	// Read width and height
	header := (*[2]uint32)(data)
	imgWidth := header[0]
	imgHeight := header[1]

	if imgWidth == 0 || imgHeight == 0 {
		return
	}

	// Point to pixel data (after 8 bytes of header)
	pixelData := unsafe.Pointer(uintptr(data) + 8)
	pixels := (*[1 << 30]uint32)(pixelData)

	// Render pixels
	pixelIndex := 0
	for y := uint32(0); y < imgHeight; y++ {
		// Optimization: If no alpha and row is fully on screen, copy entire row
		// This uses MemmoveBytes which is much faster than pixel-by-pixel writes
		screenY := yOffset + int32(y)
		if !useAlpha && screenY >= 0 && screenY < int32(fbinfo.Height) {
			// Check X bounds for the whole row
			rowStartScreenX := xOffset
			rowEndScreenX := xOffset + int32(imgWidth)

			if rowStartScreenX >= 0 && rowEndScreenX <= int32(fbinfo.Width) {
				// Copy entire row using MemmoveBytes
				// Calculate source address: pixelData + (pixelIndex * 4)
				srcRowAddr := uintptr(pixelData) + uintptr(pixelIndex)*4
				// Calculate dest address: fbinfo.Buf + (screenY * Pitch) + (screenX * 4)
				destRowAddr := uintptr(fbinfo.Buf) + uintptr(screenY)*uintptr(fbinfo.Pitch) + uintptr(rowStartScreenX)*4
				rowSize := imgWidth * 4

				MemmoveBytes(destRowAddr, srcRowAddr, uintptr(rowSize))

				// Advance pixelIndex by width
				pixelIndex += int(imgWidth)
				continue
			}
		}

		for x := uint32(0); x < imgWidth; x++ {
			screenX := int32(xOffset) + int32(x)
			screenY := yOffset + int32(y)

			// Bounds check - skip if off-screen
			if screenX < 0 || screenX >= int32(fbinfo.Width) || screenY < 0 || screenY >= int32(fbinfo.Height) {
				pixelIndex++
				continue
			}

			color := pixels[pixelIndex]

			if useAlpha {
				WritePixelAlpha(uint32(screenX), uint32(screenY), color)
			} else {
				WritePixel(uint32(screenX), uint32(screenY), color)
			}

			pixelIndex++
		}
	}
}
