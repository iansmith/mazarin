package main

import (
	"unsafe"
)

// Framebuffer constants
const (
	COLORDEPTH     = 24  // 24 bits per pixel (RGB)
	BYTES_PER_PIXEL = 3  // 3 bytes per pixel (RGB)
	CHAR_WIDTH     = 8   // Character width in pixels
	CHAR_HEIGHT    = 8   // Character height in pixels
)

// Property tag IDs for framebuffer
const (
	NULL_TAG                    = 0
	FB_ALLOCATE_BUFFER          = 0x00040001
	FB_RELEASE_BUFFER           = 0x00048001
	FB_GET_PHYSICAL_DIMENSIONS  = 0x00040003
	FB_SET_PHYSICAL_DIMENSIONS  = 0x00048003
	FB_GET_VIRTUAL_DIMENSIONS   = 0x00040004
	FB_SET_VIRTUAL_DIMENSIONS   = 0x00048004
	FB_GET_BITS_PER_PIXEL      = 0x00040005
	FB_SET_BITS_PER_PIXEL      = 0x00048005
	FB_GET_BYTES_PER_ROW        = 0x00040008
)

// Property message constants
const (
	REQUEST  = 0x00000000 // Request code
	RESPONSE = 0x80000000 // Response code (bit 31 set)
	RESPONSE_ERROR = 0x80000001 // Error response
)

// FramebufferInfo holds information about the framebuffer
type FramebufferInfo struct {
	Width       uint32 // Width in pixels
	Height      uint32 // Height in pixels
	Pitch       uint32 // Bytes per row
	Buf         unsafe.Pointer // Pointer to framebuffer memory
	BufSize     uint32 // Size of framebuffer in bytes
	CharsWidth  uint32 // Width in characters
	CharsHeight uint32 // Height in characters
	CharsX      uint32 // Current X cursor position (in characters)
	CharsY      uint32 // Current Y cursor position (in characters)
}

// Global framebuffer info
var fbinfo FramebufferInfo

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
	Size        uint32 // Buffer size in bytes
	ReqResCode  uint32 // Request/response code
	Tags        [256]uint32 // Tags array (variable length, but we allocate space)
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
	
	// Debug: Print buffer size being requested (in hex to avoid stack issues)
	uartPuts("sendMessages: buf calc done\r\n")
	uartPuts("sendMessages: size=0x")
	// Print hex digits manually - simple approach
	// Print as 8 hex digits (32 bits = 8 hex digits)
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (bufsize >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Allocate buffer (kmalloc returns 16-byte aligned addresses)
	msgPtr := kmalloc(uint32(bufsize))
	if msgPtr == nil {
		uartPuts("sendMessages: kmalloc failed for size: ")
		uartPutUint32(bufsize)
		uartPuts("\r\n")
		// Allocation failed - this could be because:
		// 1. Heap is out of memory (unlikely with 1MB heap)
		// 2. Heap is corrupted
		// 3. The requested size is invalid
		return -1 // -1 = memory allocation failed
	}
	
	uartPuts("sendMessages: kmalloc succeeded\r\n")
	
	// Cast to PropertyMessageBuffer
	msg := (*PropertyMessageBuffer)(msgPtr)
	
	// Zero out the buffer
	bzero(msgPtr, bufsize)
	
	// Don't set Size and ReqResCode yet - we'll set them after writing all tags
	// (like the C example does)
	
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
		// For responses, GPU will update this with response size (bit 31 set = response)
		// Bit 31 indicates response (0x80000000), bit 0 indicates error (0x80000001)
		msg.Tags[bufpos] = REQUEST // TAG_REQUEST_CODE = 0x00000000
		bufpos++
		
		// Copy value buffer
		// The valueBuffer is a union-like struct, so we need to read from the correct field
		// based on the tag type. Read directly from struct fields to avoid alignment issues.
		valWords := (valLen + 3) / 4 // Round up to word boundary
		
		// Read values based on tag type - avoid switch to reduce stack usage
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
	
	// Now set the buffer size and request code (like C example does)
	// Calculate actual size: header (8 bytes) + tags (bufpos * 4 bytes)
	calculatedSize := 8 + (bufpos * 4)
	msg.Size = uint32(calculatedSize)
	msg.ReqResCode = REQUEST
	
	// Debug: Print message header (after setting it)
	uartPuts("sendMessages: msg.Size=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.Size >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", ReqResCode=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.ReqResCode >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Debug: Print first few words of message buffer to verify format
	uartPuts("sendMessages: Message buffer dump (first 16 words):\r\n")
	for i := 0; i < 16 && i < int(bufpos)+3; i++ {
		uartPuts("  [")
		// Print index
		if i < 10 {
			uartPutc(byte('0' + i))
		} else {
			uartPutc(byte('A' + i - 10))
		}
		uartPuts("]=0x")
		var val uint32
		if i == 0 {
			val = msg.Size
		} else if i == 1 {
			val = msg.ReqResCode
		} else {
			val = msg.Tags[i-2]
		}
		for shift := 28; shift >= 0; shift -= 4 {
			digit := (val >> shift) & 0xF
			if digit < 10 {
				uartPutc(byte('0' + digit))
			} else {
				uartPutc(byte('A' + digit - 10))
			}
		}
		uartPuts("\r\n")
	}
	
	// Debug: Print message structure
	uartPuts("sendMessages: tagCount=")
	// Print tagCount in hex
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (uint32(tagCount) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", First tag: ID=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.Tags[0] >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", Len=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.Tags[1] >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", bufpos after tags=")
	// Print bufpos in hex
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (bufpos >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" words, total bytes=")
	// Print total bytes (header + tags)
	totalBytes := 8 + (bufpos * 4) // 8 bytes header + bufpos words * 4
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (totalBytes >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Convert physical address to mailbox format
	// For Raspberry Pi 4, we need to add 0x40000000 and shift right by 4
	// Since we're using identity mapping (virtual == physical), we can use the address directly
	// The mailbox requires addresses in a specific format: (addr + 0x40000000) >> 4
	// This tells the GPU the address is in the ARM's memory space
	// IMPORTANT: The address must be 16-byte aligned, and we can only use lower 32 bits
	// For 64-bit addresses, we need to ensure the address fits in 32 bits or use a different approach
	physAddr := uintptr(unsafe.Pointer(msg))
	
	// Debug: Print the physical address
	uartPuts("sendMessages: physAddr=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(physAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Check if address fits in 32 bits (after adding 0x40000000)
	// Mailbox format: (addr + 0x40000000) must fit in 28 bits (shifted right by 4)
	// So addr must be < (1 << 28) - 0x40000000 = 0x0C0000000 (but that's 33 bits, so max is 0x3FFFFFFF)
	// Actually, the mailbox uses 28 bits for address, so max address is (1 << 32) - 0x40000000 - 1
	if physAddr > 0x3FFFFFFF {
		uartPuts("sendMessages: ERROR - address too high for mailbox (64-bit not supported)\r\n")
		kfree(msgPtr)
		return -6 // -6 = address too high
	}
	
	// Convert to uint32 before addition to avoid 64-bit overflow issues
	physAddr32 := uint32(physAddr)
	// Mailbox format: For property channel, we need to add 0x40000000 to get GPU's view of memory
	// Then shift right by 4 (since address must be 16-byte aligned, lower 4 bits are used for channel)
	// The GPU sees RAM starting at 0x40000000, so we add that offset
	mailboxAddr := (physAddr32 + 0x40000000) >> 4
	
	uartPuts("sendMessages: mailboxAddr=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (uint32(mailboxAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Send message
	// Calculate what we're sending (for comparison with response, like C code does)
	expectedMail := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
	uartPuts("sendMessages: Sending mail=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (expectedMail >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" to mailbox\r\n")
	
	// Ensure all memory writes are visible to the GPU before sending
	dsb()
	
	mailboxSend(mailboxAddr, PROPERTY_CHANNEL)
	
	// Read response - keep reading until we get our message back (like C code: while(1) until response == mail)
	uartPuts("sendMessages: Reading from mailbox (waiting for matching response)\r\n")
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
		// Check if response matches what we sent (like C code: if (response == mail))
		// Compare address part (upper 28 bits), channel may differ
		responseAddr := response & 0xFFFFFFF0
		expectedAddr := expectedMail & 0xFFFFFFF0
		if responseAddr == expectedAddr {
			uartPuts("sendMessages: Got matching response (address matches)\r\n")
			break
		}
		// Address doesn't match - this might be a response to a different message
		// Keep reading (C code does: while(1) until response == mail)
		responseCount++
		if responseCount > 1000 {
			uartPuts("sendMessages: Too many mismatched responses, giving up\r\n")
			kfree(msgPtr)
			return -2
		}
	}
	
	// After getting matching response, check the message buffer for GPU's modifications
	// The GPU should have updated ReqResCode and tag response codes
	uartPuts("sendMessages: Checking message buffer after mailbox read\r\n")
	uartPuts("sendMessages: ReqResCode=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.ReqResCode >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", Size=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.Size >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	uartPuts("sendMessages: Got response=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (response >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	uartPuts("sendMessages: Expected response=0x")
	expectedResp := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (expectedResp >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(", got=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (response >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	uartPuts("sendMessages: After mailbox read, ReqResCode=0x")
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (msg.ReqResCode >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	
	// Check if response address matches what we sent (indicates GPU received our message)
	expectedRespAddr := (mailboxAddr & 0xFFFFFFF0) | (PROPERTY_CHANNEL & 0xF)
	if response != expectedRespAddr {
		uartPuts("sendMessages: Response address mismatch - GPU may not have processed\r\n")
		// But continue anyway - sometimes the response address is slightly different
	}
	
	// Check response code - GPU should have modified this from 0x00000000 to 0x80000000
	// However, in QEMU or without cache flushing, we might not see the change immediately
	if msg.ReqResCode == REQUEST {
		uartPuts("sendMessages: ReqResCode still REQUEST - checking if response address matches\r\n")
		// If response address matches, the GPU did receive our message
		// In some cases (especially QEMU), the GPU might process it but not modify the buffer
		// due to cache coherency. Let's check the response address as a workaround
		if response == expectedRespAddr || (response & 0xFFFFFFF0) == (expectedRespAddr & 0xFFFFFFF0) {
			uartPuts("sendMessages: Response address matches - checking tag responses (QEMU workaround)\r\n")
			// For QEMU, if we get our address back, check individual tag response codes
			// The GPU might have processed tags even if ReqResCode wasn't updated
			// Check if any tag has a response code set (bit 31 = response, bit 0 = error)
			tagBufpos := uint32(0)
			allTagsProcessed := true
			for i := 0; i < tagCount; i++ {
				valLen := getValueBufferLen(&tags[i])
				// Skip tag ID and value buffer length
				tagBufpos += 2
				// Check request/response size field (bit 31 = response, bit 0 = error)
				// For requests, this equals valLen. For responses, GPU sets bit 31.
				tagRespSize := msg.Tags[tagBufpos]
				tagBufpos++ // Skip request/response size
				// Skip value buffer
				valWords := (valLen + 3) / 4
				tagBufpos += valWords
				
				if (tagRespSize & 0x80000000) == 0 {
					// Tag not processed (bit 31 not set, still equals valLen)
					allTagsProcessed = false
					uartPuts("sendMessages: Tag ")
					// Print tag index
					for shift := 28; shift >= 0; shift -= 4 {
						digit := (uint32(i) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts(" not processed (respSize=0x")
					for shift := 28; shift >= 0; shift -= 4 {
						digit := (tagRespSize >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts(")\r\n")
				} else {
					// Tag was processed (bit 31 set)
					if (tagRespSize & 0x1) != 0 {
						uartPuts("sendMessages: Tag ")
						for shift := 28; shift >= 0; shift -= 4 {
							digit := (uint32(i) >> shift) & 0xF
							if digit < 10 {
								uartPutc(byte('0' + digit))
							} else {
								uartPutc(byte('A' + digit - 10))
							}
						}
						uartPuts(" returned error\r\n")
					} else {
						uartPuts("sendMessages: Tag ")
						for shift := 28; shift >= 0; shift -= 4 {
							digit := (uint32(i) >> shift) & 0xF
							if digit < 10 {
								uartPutc(byte('0' + digit))
							} else {
								uartPutc(byte('A' + digit - 10))
							}
						}
						uartPuts(" processed successfully\r\n")
					}
				}
			}
			
			if !allTagsProcessed && msg.ReqResCode == REQUEST {
				// Tags weren't processed and message-level code wasn't updated
				uartPuts("sendMessages: Tags not processed, GPU may not have seen message\r\n")
				kfree(msgPtr)
				return 1
			}
			// If we get here, either tags were processed or we're in QEMU with cache issues
			// Continue and check response codes below
		} else {
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
		if (response & 0xFFFFFFF0) == (expectedRespAddr & 0xFFFFFFF0) {
			uartPuts("sendMessages: ReqResCode not updated but address matches - QEMU cache issue?\r\n")
			// Continue anyway - we'll check tag responses when copying back
		} else {
			uartPuts("sendMessages: Unexpected response code: 0x")
			// Print response code in hex
			for shift := 28; shift >= 0; shift -= 4 {
				digit := (msg.ReqResCode >> shift) & 0xF
				if digit < 10 {
					uartPutc(byte('0' + digit))
				} else {
					uartPutc(byte('A' + digit - 10))
				}
			}
			uartPuts("\r\n")
			kfree(msgPtr)
			return -3 // -3 = unexpected response code
		}
	}
	uartPuts("sendMessages: Response OK\r\n")
	
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

// framebufferInit initializes the framebuffer via the property mailbox
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
	// This is the FIRST sendMessages call - if it fails here, it's likely a heap issue
	uartPuts("framebufferInit: Calling sendMessages (set dims)\r\n")
	result := sendMessages(tags[:])
	if result != 0 {
		uartPuts("framebufferInit: sendMessages (set dims) failed with code: 0x")
	// Print error code in hex
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (uint32(result) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	return result
	}
	uartPuts("framebufferInit: sendMessages (set dims) succeeded\r\n")
	
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
	
	uartPuts("framebufferInit: Calling sendMessages (get pitch)\r\n")
	// Don't fail if this query fails - just use calculated pitch
	if sendMessages(tags[:]) == 0 {
		uartPuts("framebufferInit: sendMessages (get pitch) succeeded\r\n")
		fbinfo.Pitch = tags[0].ValueBuffer.fbBytesPerRow
	} else {
		uartPuts("framebufferInit: sendMessages (get pitch) failed, using calculated pitch\r\n")
		// Fallback: calculate pitch as width * bytes_per_pixel
		fbinfo.Pitch = fbinfo.Width * BYTES_PER_PIXEL
	}
	
	// Request framebuffer allocation
	tags[0].Proptag = FB_ALLOCATE_BUFFER
	tags[0].ValueBuffer.fbAllocateAlign = 16
	tags[1].Proptag = NULL_TAG
	
	uartPuts("framebufferInit: Calling sendMessages (alloc buffer)\r\n")
	result = sendMessages(tags[:])
	if result != 0 {
		uartPuts("framebufferInit: sendMessages (alloc buffer) failed: ")
		uartPuts("error code\r\n")
		// Error - framebuffer allocation failed at buffer allocation step
		// This is the second sendMessages call, so if it fails here, the heap might be corrupted
		// or the buffer size calculation is wrong
		return result
	}
	uartPuts("framebufferInit: sendMessages (alloc buffer) succeeded\r\n")
	
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

