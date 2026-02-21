
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	_ "unsafe" // for go:linkname
)

// SyscallWrite implements the write(2) syscall.
// For now, we only support stdout/stderr (fd 1 and 2).
//
// Routing logic:
//   - If the caller is the stdio priest (UART ring owner), writes are
//     silently dropped (it cannot push into its own ring without deadlock).
//   - If the caller is any other priest, bytes are pushed through the
//     ring buffer so the stdio priest can display them. The fd number
//     is carried through the ring so stdio can color stderr differently.
//   - If no priest owns the UART slot yet (early boot), writes are dropped.
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

	// Route to ring buffer for display by the stdio priest.
	// The stdio priest itself (UART ring owner) cannot use the ring
	// (it would deadlock consuming its own output).
	useRing := false
	ownerPID := getUartSlotPriestID()
	if ownerPID >= 0 {
		callerPID := getCurrentThreadPID()
		if callerPID != ownerPID {
			useRing = true
		}
	}

	remaining := count
	offset := uint64(0)
	fdByte := byte(fd)
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
					pushByteToUartRing(fdByte, '\r')
				}
				pushByteToUartRing(fdByte, c)
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

// pushByteToUartRing pushes a byte into the UART ring buffer with fd info.
// The fd is carried in the HIDEvent.Code field so the consumer can
// distinguish stdout (1) from stderr (2).
//
//go:linkname pushByteToUartRing main.PushByteToUartRing
func pushByteToUartRing(fd byte, b byte)

// flushUartRingWake wakes the UART slot consumer after pushing bytes.
//
//go:linkname flushUartRingWake main.FlushUartRingWake
func flushUartRingWake()
