
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

import (
	_ "unsafe" // For go:linkname
)

// Mazzy syscall numbers (numerically ordered).
// These must match the kernel-side definitions in ksyscall/mazzy.go.
const (
	sysGetTime              = 0x1000 // Get current time
	sysLaunch               = 0x1001 // Launch a shepherd from ELF file
	sysBootstrapRunElf      = 0x1002 // Bootstrap: load ELF from disk (disk manager only)
	sysAllocPages           = 0x1003 // Allocate pages for userspace
	sysExit                 = 0x1004 // Exit program (Mazzy-specific)
	sysReap                 = 0x1005 // Reap terminated program
	sysDebugPrint           = 0x1006 // Debug print arguments
	sysGetFramebuffer       = 0x1007 // Get framebuffer info
	sysWaitKernelAsync      = 0x1008 // Wait for kernel async message
	sysFlushFramebuffer      = 0x100D // Flush framebuffer region to display
	sysSetTimerDeadline      = 0x100E // Set timer deadline on soft IRQ slot
	sysSetScanoutOffset      = 0x100F // Set scanout Y offset for hardware scrolling
	sysLoadMaz               = 0x1012 // Load .maz PIE ELF into shepherd's address space
	sysRegisterSyscallHandler = 0x1017 // Register shepherd as handler for a SysID
	sysDelegatedRecv          = 0x1018 // Receive a delegated syscall request
	sysDelegatedReply         = 0x1019 // Reply to a delegated syscall
	sysUartWrite              = 0x101A // Write to UART (non-blocking)
	sysUartWriteBlocking      = 0x101B // Write to UART via PollWrite (synchronous, guaranteed delivery)
	sysShepherdInfo             = 0x101C // Get info about running shepherds
	sysSetReady               = 0x101D // Signal shepherd is ready for delegated work
	sysLoadFile               = 0x101E // Load file via fs.maz delegate
	sysRunMaz                 = 0x101F // Load .maz ELF from caller's pages
	sysRunShepherd              = 0x1020 // Create new shepherd from caller's pages
	// slots 0x1035-0x1036 freed: were RegisterDMAPool/UnregisterDMAPool
	sysBlockSubmit = 0x1037 // Async block I/O submit (returns IOTag)
)

// DebugPutChar writes a single character to the kernel debug output.
// Uses a direct syscall with no Go runtime locks/synchronization.
// Safe to call from busy loops without blocking.
//
//go:nosplit
func DebugPutChar(c byte) {
	RawSyscall(sysDebugPrint, uintptr(c), 0, 0, 0, 0, 0)
}
