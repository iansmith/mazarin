package sys

import (
	"errors"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

const (
	// mapAnonymous is MAP_ANONYMOUS (0x20) — defined here because
	// syscall.MAP_ANONYMOUS only exists on Linux (macOS uses MAP_ANON).
	mapAnonymous = 0x20
)

// IPCRecvResult is the struct filled by SysIPCRecv.
// Layout must match kernel-side ksyscall.IPCRecvResult.
type IPCRecvResult struct {
	SenderPID    int64
	RequestVA    uint64
	RequestPages uint64
}

// IPCCall sends an IPC request to a target shepherd and blocks until a reply arrives.
// requestVA must be page-aligned. requestPages is the number of 4KB pages.
// Returns the reply VA and reply page count.
//
// Uses Syscall6 so entersyscall/exitsyscall release the P while blocked.
func IPCCall(targetPID int, requestVA uintptr, requestPages int) (replyVA uintptr, replyPages int, err error) {
	r1, _, errno := syscall.Syscall6(mazzy.SysUringConnect,
		uintptr(targetPID),
		requestVA,
		uintptr(requestPages),
		0, 0, 0)

	if errno != 0 {
		return 0, 0, errno
	}
	if int64(r1) < 0 {
		return 0, 0, syscall.Errno(-int64(r1))
	}

	// r1 encodes replyVA | replyPages (bits 11:0 = page count, rest = VA)
	packed := uint64(r1)
	va := uintptr(packed &^ 0xFFF)
	pages := int(packed & 0xFFF)
	return va, pages, nil
}

// IPCRecv blocks until an IPC request arrives from a client shepherd.
// Returns the sender's PID, the request VA (in our address space), and the page count.
//
// Uses Syscall6 so the P is released while blocked.
func IPCRecv() (senderPID int, requestVA uintptr, requestPages int, err error) {
	var result IPCRecvResult

	r1, _, errno := syscall.Syscall6(mazzy.SysUringRecv,
		uintptr(unsafe.Pointer(&result)),
		0, 0, 0, 0, 0)

	if errno != 0 {
		return 0, 0, 0, errno
	}
	if int64(r1) < 0 {
		return 0, 0, 0, syscall.Errno(-int64(r1))
	}

	return int(result.SenderPID), uintptr(result.RequestVA), int(result.RequestPages), nil
}

// IPCReply sends a reply to a client shepherd blocked in IPCCall.
// replyVA must be page-aligned.
func IPCReply(clientPID int, replyVA uintptr, replyPages int) error {
	r1, _, errno := RawSyscall(mazzy.SysUringRelease,
		uintptr(clientPID),
		replyVA,
		uintptr(replyPages),
		0, 0, 0)

	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// ReadFile is a convenience wrapper that reads a file via IPC to the disk shepherd.
// Sends a FS_READ request to diskPID and returns the file contents.
func ReadFile(diskPID int, path string) ([]byte, error) {
	// Allocate 1 page for the request
	reqSize := uintptr(4096)
	reqVA, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, reqSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|mapAnonymous,
		^uintptr(0), 0)
	if errno != 0 || int64(reqVA) < 0 {
		return nil, errors.New("mmap failed for IPC request")
	}

	// Write IPC header + path
	// Header: Opcode(4) + Flags(4) + PayloadLen(8) + ErrorCode(8) = 24 bytes
	// nolint: gosec — reqVA is an mmap-returned VA (validated above); casting to
	// array pointer is the standard way to write structured data into mmap'd memory.
	hdr := (*[24]byte)(unsafe.Pointer(reqVA))
	// Opcode = 1 (FS_READ)
	hdr[0] = 1
	hdr[1] = 0
	hdr[2] = 0
	hdr[3] = 0
	// Flags = 0
	hdr[4] = 0
	hdr[5] = 0
	hdr[6] = 0
	hdr[7] = 0
	// PayloadLen = len(path) + 1 (null terminator)
	pathLen := uint64(len(path) + 1)
	for i := 0; i < 8; i++ {
		hdr[8+i] = byte(pathLen >> (i * 8))
	}
	// ErrorCode = 0
	for i := 16; i < 24; i++ {
		hdr[i] = 0
	}

	// Write path after header
	// nolint: gosec — reqVA+24 is pointer arithmetic within the mmap'd page (4096
	// bytes); offset 24 is well within bounds. Required to write past the header.
	pathDst := unsafe.Slice((*byte)(unsafe.Pointer(reqVA+24)), len(path)+1)
	copy(pathDst, path)
	pathDst[len(path)] = 0 // null terminator

	// Send IPC call
	replyVA, replyPages, err := IPCCall(diskPID, reqVA, 1)
	if err != nil {
		return nil, err
	}

	if replyPages == 0 || replyVA == 0 {
		return nil, errors.New("empty IPC reply")
	}

	// Parse reply header
	// nolint: gosec — replyVA is a kernel IPC-returned VA (validated by replyPages > 0
	// check above); casting to array pointer to read the 24-byte header.
	replyHdr := (*[24]byte)(unsafe.Pointer(replyVA))
	errCode := int64(0)
	for i := 0; i < 8; i++ {
		errCode |= int64(replyHdr[16+i]) << (i * 8)
	}
	if errCode != 0 {
		return nil, errors.New("disk shepherd returned error")
	}

	// Read payload length
	payloadLen := uint64(0)
	for i := 0; i < 8; i++ {
		payloadLen |= uint64(replyHdr[8+i]) << (i * 8)
	}

	// Copy file data
	totalReplySize := uint64(replyPages) * 4096
	if payloadLen > totalReplySize-24 {
		payloadLen = totalReplySize - 24
	}

	data := make([]byte, payloadLen)
	// nolint: gosec — replyVA+24 is pointer arithmetic within the IPC reply pages;
	// payloadLen is bounds-checked against totalReplySize-24 above.
	src := unsafe.Slice((*byte)(unsafe.Pointer(replyVA+24)), payloadLen)
	copy(data, src)

	return data, nil
}
