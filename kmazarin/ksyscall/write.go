
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	_ "unsafe" // for go:linkname
)

// SyscallWrite implements the write(2) syscall.
// For now, we only support stdout/stderr (fd 1 and 2).
//
// Routing logic:
//   - If the caller is the stdio priest (UART ring owner), use Breadcrumb
//     (direct MMIO) to avoid deadlock — the stdio priest consumes the ring,
//     so writing into it would block on itself.
//   - If the caller is any other priest, use KWriteByte to push through the
//     ring buffer so the stdio priest can display it.
//   - If no priest owns the UART slot yet (early boot, kernel fmt.Println),
//     use Breadcrumb as fallback.
//
//go:nosplit
func SyscallWrite(fd, bufPtr, count, _, _, _ uint64) int64 {
	// Only support stdout/stderr for now
	if fd != 1 && fd != 2 {
		return -1 // EBADF
	}

	if count == 0 {
		return 0
	}

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}

	// Copy user buffer into kernel memory first, then output.
	// Process in chunks to avoid large stack allocations.
	// Determine output path: ring buffer vs direct MMIO
	// The stdio priest (UART ring owner) must use Breadcrumb to avoid deadlock.
	// All other priests go through KWriteByte → ring → stdio displays it.
	useRing := false
	if fd == 1 {
		ownerPID := getUartSlotPriestID()
		if ownerPID >= 0 {
			callerPID := getCurrentThreadPID()
			if callerPID != ownerPID {
				useRing = true
			}
		}
	}
	// Stderr (fd=2) always goes direct to MMIO for crash safety

	remaining := count
	offset := uint64(0)
	for remaining > 0 {
		var chunk [256]byte
		n := remaining
		if n > 256 {
			n = 256
		}
		if !kmem.CopyFromUser(chunk[:n], uintptr(bufPtr+offset), int(n)) {
			return -14 // EFAULT
		}
		if useRing {
			for i := uint64(0); i < n; i++ {
				c := chunk[i]
				if c == '\n' {
					pushByteToUartRing('\r')
				}
				pushByteToUartRing(c)
			}
		}
		offset += n
		remaining -= n
	}

	if useRing {
		flushUartRingWake()
	}

	return int64(count)
}

// getUartSlotPriestID returns the priest ID that owns the UART serial slot.
// Returns -1 if no priest has registered.
//
//go:nosplit
//go:linkname getUartSlotPriestID main.GetUartSlotPriestID
func getUartSlotPriestID() int16

// pushByteToUartRing pushes a byte directly into the UART ring buffer.
//
//go:linkname pushByteToUartRing main.PushByteToUartRing
func pushByteToUartRing(b byte)

// flushUartRingWake wakes the UART slot consumer after pushing bytes.
//
//go:linkname flushUartRingWake main.FlushUartRingWake
func flushUartRingWake()
