package main

import "mazboot/asm"

// Mailbox channel constants
const (
	// Property mailbox channel (channel 8) - used for framebuffer and other properties
	PROPERTY_CHANNEL = 8
)

// Mailbox status flags
const (
	MAILBOX_FULL  = 1 << 31 // Mailbox full flag
	MAILBOX_EMPTY = 1 << 30 // Mailbox empty flag
)

// mailboxRead reads a message from the specified mailbox channel
// Returns the message data (28 bits) and channel (4 bits)
// Note: For property channel (8), responses may come back on channel 0
//
//go:nosplit
func mailboxRead(channel uint32) uint32 {
	var data uint32

	// Wait until mailbox is not empty
	for {
		status := asm.MmioRead(MAILBOX_STATUS)
		if status&MAILBOX_EMPTY == 0 {
			break
		}
	}

	// Read the message
	data = asm.MmioRead(MAILBOX_READ)

	// Check if message is for our channel
	// Special case: Property channel (8) responses may come back on channel 0
	responseChannel := data & 0xF
	if responseChannel != channel {
		// For property channel, also accept channel 0 responses
		if channel == PROPERTY_CHANNEL && responseChannel == 0 {
			// Property channel response on channel 0 - accept it
		} else {
			// Wrong channel, return 0 to indicate error
			return 0
		}
	}

	// Return the message data (upper 28 bits)
	return data & 0xFFFFFFF0
}

// mailboxSend sends a message to the specified mailbox channel
// message: The message data (must have channel in lower 4 bits, address in upper 28 bits)
// channel: The mailbox channel to send to
//
//go:nosplit
func mailboxSend(message uint32, channel uint32) {
	// Wait until mailbox is not full
	for {
		status := asm.MmioRead(MAILBOX_STATUS)
		if status&MAILBOX_FULL == 0 {
			break
		}
	}

	// Combine message data with channel number (lower 4 bits)
	// The message address must be 16-byte aligned, so we only use upper 28 bits
	msg := (message & 0xFFFFFFF0) | (channel & 0xF)

	// Write the message
	asm.MmioWrite(MAILBOX_WRITE, msg)
}
