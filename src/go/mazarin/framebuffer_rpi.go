//go:build !qemu

package main

import (
	"unsafe"
)

// Property tag IDs for framebuffer (Raspberry Pi specific)
const (
	NULL_TAG                   = 0
	FB_ALLOCATE_BUFFER         = 0x00040001
	FB_RELEASE_BUFFER          = 0x00048001
	FB_GET_PHYSICAL_DIMENSIONS = 0x00040003
	FB_SET_PHYSICAL_DIMENSIONS = 0x00048003
	FB_GET_VIRTUAL_DIMENSIONS  = 0x00040004
	FB_SET_VIRTUAL_DIMENSIONS  = 0x00048004
	FB_GET_BITS_PER_PIXEL      = 0x00040005
	FB_SET_BITS_PER_PIXEL      = 0x00048005
	FB_GET_BYTES_PER_ROW       = 0x00040008
)

// Property message constants
const (
	REQUEST        = 0x00000000 // Request code
	RESPONSE       = 0x80000000 // Response code (bit 31 set)
	RESPONSE_ERROR = 0x80000001 // Error response
)

// Property tag value buffer types
type fbAllocateRes struct {
	FbAddr unsafe.Pointer
	FbSize uint32
}

type fbScreenSize struct {
	Width  uint32
	Height uint32
}

type valueBuffer struct {
	fbAllocateAlign uint32
	fbAllocateRes   fbAllocateRes
	fbScreenSize    fbScreenSize
	fbBitsPerPixel  uint32
	fbBytesPerRow   uint32
}

// PropertyMessageTag represents a single property tag
type PropertyMessageTag struct {
	Proptag     uint32
	ValueBuffer valueBuffer
}

// PropertyMessageBuffer represents the entire property message buffer
// This must be 16-byte aligned
type PropertyMessageBuffer struct {
	Size       uint32      // Buffer size in bytes
	ReqResCode uint32      // Request/response code
	Tags       [256]uint32 // Tags array (variable length, but we allocate space)
}

// getValueBufferLen returns the size of the value buffer for a given tag
//
//go:nosplit
func getValueBufferLen(tag *PropertyMessageTag) uint32 {
	switch tag.Proptag {
	case FB_ALLOCATE_BUFFER:
		return 8
	case FB_GET_PHYSICAL_DIMENSIONS:
		return 8
	case FB_SET_PHYSICAL_DIMENSIONS:
		return 8
	case FB_GET_VIRTUAL_DIMENSIONS:
		return 8
	case FB_SET_VIRTUAL_DIMENSIONS:
		return 8
	case FB_GET_BITS_PER_PIXEL:
		return 4
	case FB_SET_BITS_PER_PIXEL:
		return 4
	case FB_GET_BYTES_PER_ROW:
		return 4
	case FB_RELEASE_BUFFER:
		return 0
	case NULL_TAG:
		return 0
	default:
		return 0
	}
}

// sendMessages sends property messages via the mailbox and receives responses
// tags: Array of property tags (must be NULL_TAG terminated)
// Returns 0 on success, non-zero on error
// Note: This function uses significant stack space, so we can't use //go:nosplit
func sendMessages(tags []PropertyMessageTag) int32 {
	// Calculate buffer size needed
	bufsize := uint32(0)
	tagCount := 0

	// Count tags and calculate size
	for i := 0; i < len(tags) && tags[i].Proptag != NULL_TAG; i++ {
		valLen := getValueBufferLen(&tags[i])
		// Each tag: ID (4) + value buffer length (4) + request/response size (4) + value buffer
		bufsize += valLen + 3*4
		tagCount++
	}

	// Add header: buffer size (4) + request/response code (4), and end tag (4)
	bufsize += 3 * 4

	// Buffer must be 16-byte aligned for allocation
	// We'll calculate the actual message size after writing all tags
	if bufsize%16 != 0 {
		bufsize += 16 - (bufsize % 16)
	}

	// Safety check: bufsize should be reasonable (max 1KB for property messages)
	if bufsize == 0 {
		uartPuts("sendMessages: ERROR - buffer size is 0!\r\n")
		return -5 // -5 = invalid buffer size (zero)
	}

	if bufsize > 1024 {
		uartPuts("sendMessages: Buffer size too large (>1KB)\r\n")
		return -4 // -4 = buffer size too large
	}

	// Allocate buffer (kmalloc returns 16-byte aligned addresses)
	msgPtr := kmalloc(uint32(bufsize))
	if msgPtr == nil {
		uartPuts("sendMessages: kmalloc failed for size: ")
		uartPutUint32(bufsize)
		uartPuts("\r\n")
		return -1 // -1 = memory allocation failed
	}

	// Cast to PropertyMessageBuffer
	msg := (*PropertyMessageBuffer)(msgPtr)

	// Zero out the buffer
	bzero(msgPtr, bufsize)

	// Copy tags into buffer
	bufpos := uint32(0)
	for i := 0; i < tagCount; i++ {
		valLen := getValueBufferLen(&tags[i])

		// Write tag ID
		msg.Tags[bufpos] = tags[i].Proptag
		bufpos++

		// Write value buffer length (in bytes) - maximum of request and response
		msg.Tags[bufpos] = valLen
		bufpos++

		// Write TAG_REQUEST_CODE (0x00000000 for requests)
		msg.Tags[bufpos] = REQUEST
		bufpos++

		// Copy value buffer
		valWords := (valLen + 3) / 4 // Round up to word boundary

		// Read values based on tag type
		if tags[i].Proptag == FB_SET_PHYSICAL_DIMENSIONS || tags[i].Proptag == FB_SET_VIRTUAL_DIMENSIONS ||
			tags[i].Proptag == FB_GET_PHYSICAL_DIMENSIONS || tags[i].Proptag == FB_GET_VIRTUAL_DIMENSIONS {
			// These use fbScreenSize (Width, Height) - 2 words
			msg.Tags[bufpos] = tags[i].ValueBuffer.fbScreenSize.Width
			bufpos++
			if valWords > 1 {
				msg.Tags[bufpos] = tags[i].ValueBuffer.fbScreenSize.Height
				bufpos++
			}
		} else if tags[i].Proptag == FB_SET_BITS_PER_PIXEL || tags[i].Proptag == FB_GET_BITS_PER_PIXEL {
			// These use fbBitsPerPixel - 1 word
			msg.Tags[bufpos] = tags[i].ValueBuffer.fbBitsPerPixel
			bufpos++
		} else if tags[i].Proptag == FB_GET_BYTES_PER_ROW {
			// This uses fbBytesPerRow - 1 word
			msg.Tags[bufpos] = tags[i].ValueBuffer.fbBytesPerRow
			bufpos++
		} else if tags[i].Proptag == FB_ALLOCATE_BUFFER {
			// This uses fbAllocateAlign - 2 words: alignment request + placeholder for returned size
			msg.Tags[bufpos] = tags[i].ValueBuffer.fbAllocateAlign // Alignment request (16)
			bufpos++
			msg.Tags[bufpos] = 0 // Placeholder for returned size (GPU will fill this)
			bufpos++
		} else {
			// Fallback: read from start of struct as raw uint32 array
			valuePtr := (*[8]uint32)(unsafe.Pointer(&tags[i].ValueBuffer))
			for j := uint32(0); j < valWords && j < 8; j++ {
				msg.Tags[bufpos] = valuePtr[j]
				bufpos++
			}
		}
	}

	// Write end tag (0x00000000)
	msg.Tags[bufpos] = NULL_TAG
	bufpos++ // Increment after end tag

	// Now set the buffer size and request code
	calculatedSize := 8 + (bufpos * 4)
	msg.Size = uint32(calculatedSize)
	msg.ReqResCode = REQUEST

	// Convert physical address to mailbox format
	physAddr := uintptr(unsafe.Pointer(msg))

	// Check if address fits in 32 bits (after adding 0x40000000)
	if physAddr > 0x3FFFFFFF {
		uartPuts("sendMessages: ERROR - address too high for mailbox (64-bit not supported)\r\n")
		kfree(msgPtr)
		return -6 // -6 = address too high
	}

	// Convert to uint32 before addition to avoid 64-bit overflow issues
	physAddr32 := uint32(physAddr)
	// Mailbox format: For property channel, we need to add 0x40000000 to get GPU's view of memory
	// Then shift right by 4 (since address must be 16-byte aligned, lower 4 bits are used for channel)
	mailboxAddr := (physAddr32 + 0x40000000) >> 4

	// Ensure all memory writes are visible to the GPU before sending
	dsb()

	mailboxSend(mailboxAddr, PROPERTY_CHANNEL)

	// Read response - keep reading until we get our message back
	var response uint32
	responseCount := 0
	for {
		response = mailboxRead(PROPERTY_CHANNEL)
		if response == 0 {
			// No response yet, keep waiting
			responseCount++
			if responseCount > 1000 {
				uartPuts("sendMessages: Timeout waiting for response\r\n")
				kfree(msgPtr)
				return -2
			}
			continue
		}
		// Check if response matches what we sent
		responseAddr := response & 0xFFFFFFF0
		expectedAddr := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
		expectedAddr = expectedAddr & 0xFFFFFFF0
		if responseAddr == expectedAddr {
			break
		}
		// Address doesn't match - keep reading
		responseCount++
		if responseCount > 1000 {
			uartPuts("sendMessages: Too many mismatched responses, giving up\r\n")
			kfree(msgPtr)
			return -2
		}
	}

	// Check response code - GPU should have modified this from 0x00000000 to 0x80000000
	if msg.ReqResCode == REQUEST {
		// If response address matches, the GPU did receive our message
		// In some cases (especially QEMU), the GPU might process it but not modify the buffer
		// due to cache coherency. Let's check the response address as a workaround
		expectedRespAddr := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
		if response != expectedRespAddr && (response&0xFFFFFFF0) != (expectedRespAddr&0xFFFFFFF0) {
			uartPuts("sendMessages: Still REQUEST and address mismatch (GPU didn't process)\r\n")
			kfree(msgPtr)
			return 1
		}
	}
	if msg.ReqResCode == RESPONSE_ERROR {
		uartPuts("sendMessages: GPU returned error\r\n")
		kfree(msgPtr)
		return 2
	}
	if msg.ReqResCode != RESPONSE {
		// If ReqResCode is still REQUEST but we got our address back,
		// this might be a QEMU cache coherency issue
		expectedRespAddr := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
		if (response & 0xFFFFFFF0) != (expectedRespAddr & 0xFFFFFFF0) {
			uartPuts("sendMessages: Unexpected response code\r\n")
			kfree(msgPtr)
			return -3 // -3 = unexpected response code
		}
	}

	// Copy responses back into tags
	bufpos = 0
	for i := 0; i < tagCount; i++ {
		valLen := getValueBufferLen(&tags[i])

		// Skip tag ID and value buffer length
		bufpos += 2

		// Check TAG_REQUEST_CODE field (should have bit 31 set for response)
		tagRespCode := msg.Tags[bufpos]
		if (tagRespCode & 0x80000000) == 0 {
			// Tag not processed
			uartPuts("sendMessages: Tag not processed when copying back\r\n")
			kfree(msgPtr)
			return 1
		}
		if (tagRespCode & 0x1) != 0 {
			// Tag returned error
			uartPuts("sendMessages: Tag returned error when copying back\r\n")
			kfree(msgPtr)
			return 1
		}
		bufpos++ // Skip TAG_REQUEST_CODE

		// Copy value buffer back
		valuePtr := (*[8]uint32)(unsafe.Pointer(&tags[i].ValueBuffer))
		valWords := (valLen + 3) / 4 // Round up to word boundary
		for j := uint32(0); j < valWords && j < 8; j++ {
			if bufpos < 256 { // msg.Tags is [256]uint32
				valuePtr[j] = msg.Tags[bufpos]
				bufpos++
			}
		}
	}

	kfree(msgPtr)
	return 0
}

// framebufferInit initializes the framebuffer via the property mailbox (Raspberry Pi)
// Returns 0 on success, non-zero on error
// Note: This function calls sendMessages which uses significant stack space
func framebufferInit() int32 {
	// Create tags array (max 5 tags + NULL terminator)
	var tags [6]PropertyMessageTag

	// Set physical dimensions
	tags[0].Proptag = FB_SET_PHYSICAL_DIMENSIONS
	tags[0].ValueBuffer.fbScreenSize.Width = 640
	tags[0].ValueBuffer.fbScreenSize.Height = 480

	// Set virtual dimensions
	tags[1].Proptag = FB_SET_VIRTUAL_DIMENSIONS
	tags[1].ValueBuffer.fbScreenSize.Width = 640
	tags[1].ValueBuffer.fbScreenSize.Height = 480

	// Set bits per pixel
	tags[2].Proptag = FB_SET_BITS_PER_PIXEL
	tags[2].ValueBuffer.fbBitsPerPixel = COLORDEPTH

	// NULL tag to terminate
	tags[3].Proptag = NULL_TAG

	// Send initialization request (set dimensions and color depth)
	uartPuts("framebufferInit (RPI): Calling sendMessages (set dims)\r\n")
	result := sendMessages(tags[:])
	if result != 0 {
		uartPuts("framebufferInit (RPI): sendMessages (set dims) failed\r\n")
		return result
	}
	uartPuts("framebufferInit (RPI): sendMessages (set dims) succeeded\r\n")

	// Store dimensions
	fbinfo.Width = tags[0].ValueBuffer.fbScreenSize.Width
	fbinfo.Height = tags[0].ValueBuffer.fbScreenSize.Height
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	fbinfo.CharsX = 0
	fbinfo.CharsY = 0

	// Query bytes per row (pitch)
	// Note: This is optional - if it fails, we'll use calculated pitch
	tags[0].Proptag = FB_GET_BYTES_PER_ROW
	tags[1].Proptag = NULL_TAG

	uartPuts("framebufferInit (RPI): Calling sendMessages (get pitch)\r\n")
	// Don't fail if this query fails - just use calculated pitch
	if sendMessages(tags[:]) == 0 {
		uartPuts("framebufferInit (RPI): sendMessages (get pitch) succeeded\r\n")
		fbinfo.Pitch = tags[0].ValueBuffer.fbBytesPerRow
	} else {
		uartPuts("framebufferInit (RPI): sendMessages (get pitch) failed, using calculated pitch\r\n")
		// Fallback: calculate pitch as width * bytes_per_pixel
		fbinfo.Pitch = fbinfo.Width * BYTES_PER_PIXEL
	}

	// Request framebuffer allocation
	tags[0].Proptag = FB_ALLOCATE_BUFFER
	tags[0].ValueBuffer.fbAllocateAlign = 16
	tags[1].Proptag = NULL_TAG

	uartPuts("framebufferInit (RPI): Calling sendMessages (alloc buffer)\r\n")
	result = sendMessages(tags[:])
	if result != 0 {
		uartPuts("framebufferInit (RPI): sendMessages (alloc buffer) failed\r\n")
		return result
	}
	uartPuts("framebufferInit (RPI): sendMessages (alloc buffer) succeeded\r\n")

	// Store framebuffer address and size
	// The mailbox returns the address as a 64-bit value in the response
	// The fbAllocateRes struct has FbAddr as unsafe.Pointer (8 bytes) and FbSize as uint32 (4 bytes)
	// The mailbox response format is: [addr_low32, addr_high32, size32]
	// We need to read the raw uint32s and reconstruct the 64-bit pointer
	var addrParts [3]uint32
	addrParts[0] = *(*uint32)(unsafe.Pointer(&tags[0].ValueBuffer.fbAllocateAlign))
	addrParts[1] = *(*uint32)(unsafe.Pointer(uintptr(unsafe.Pointer(&tags[0].ValueBuffer.fbAllocateAlign)) + 4))
	addrParts[2] = *(*uint32)(unsafe.Pointer(uintptr(unsafe.Pointer(&tags[0].ValueBuffer.fbAllocateAlign)) + 8))

	// Reconstruct 64-bit address from two 32-bit values
	fbAddr := uintptr(addrParts[0]) | (uintptr(addrParts[1]) << 32)
	fbinfo.Buf = unsafe.Pointer(fbAddr)
	fbinfo.BufSize = addrParts[2]

	return 0
}




