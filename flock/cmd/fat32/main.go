// fat32 is a .maz module that provides FAT32 filesystem services via IPC.
// It is loaded by the disk priest into its address space, inheriting block
// device ownership. It mounts the FAT32 filesystem using SysBlockRead and
// serves file read requests from other priests via IPC.
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
	"mazzy/shared/ipc"
	"syscall"
	"unsafe"
)

// debugPuts writes a string to the serial console via DebugPutChar syscalls.
// Used instead of fmt because .maz programs have os.Stdout = nil
// (runtime.main/os.init never run with thin stubs).
func debugPuts(s string) {
	for i := 0; i < len(s); i++ {
		sys.DebugPutChar(s[i])
	}
}

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// init forces the linker to keep MazarinMain alive. With thin stubs,
// runtime.main never reaches main.main (the compiler optimizes it away),
// so package-level var initializers referencing MazarinMain get DCE'd.
// But init() IS called during runtime initialization, and reading
// MazEntryPoint here prevents the linker from treating it as write-only.
func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
}

// MazarinMain is the entry point called by the disk priest when this .maz
// is loaded. It mounts FAT32 and enters the IPC serve loop. Never returns.
//
//go:noinline
func MazarinMain() {
	debugPuts("[fs] FAT32 filesystem maz starting\n")

	// Mount FAT32 filesystem using SysBlockRead (inherited from disk priest PID)
	blkDev := &userspaceBlockDev{}
	fs, fsErr := fat32.Mount(blkDev)
	if fsErr != nil {
		debugPuts("[fs] FAT32 mount failed\n")
		return
	}
	debugPuts("[fs] FAT32 mounted successfully\n")

	// Enter IPC serve loop (never returns)
	debugPuts("[fs] entering IPC serve loop\n")
	serveLoop(fs)
}

func main() {
	MazarinMain()
}

// userspaceBlockDev implements blockdev.BlockDevice using SysBlockRead.
type userspaceBlockDev struct{}

func (d *userspaceBlockDev) Name() string         { return "virtio-blk-user" }
func (d *userspaceBlockDev) Close() error          { return nil }
func (d *userspaceBlockDev) BlockSize() uint64     { return 512 }
func (d *userspaceBlockDev) NumBlocks() uint64     { return 0 } // Unknown from userspace
func (d *userspaceBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

func (d *userspaceBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return fmt.Errorf("buffer too small: %d < 512", len(buf))
	}
	return sys.BlockRead(lba, 1, buf)
}

// Verify interface compliance
var _ blockdev.BlockDevice = (*userspaceBlockDev)(nil)

// serveLoop handles incoming IPC requests.
func serveLoop(fs *fat32.FileSystem) {
	for {
		senderPID, reqVA, reqPages, err := sys.IPCRecv()
		if err != nil {
			debugPuts("[fs] IPCRecv error\n")
			continue
		}

		debugPuts("[fs] request from P")
		debugPutDec(senderPID)
		debugPuts("\n")

		handleRequest(fs, senderPID, reqVA, reqPages)
	}
}

// handleRequest processes a single IPC filesystem request.
func handleRequest(fs *fat32.FileSystem, senderPID int, reqVA uintptr, reqPages int) {
	reqSize := uintptr(reqPages) * 4096
	reqBuf := unsafe.Slice((*byte)(unsafe.Pointer(reqVA)), reqSize)

	if len(reqBuf) < ipc.HeaderSize {
		sendErrorReply(senderPID, -22) // EINVAL
		return
	}

	hdr := ipc.UnmarshalHeader(reqBuf)

	switch hdr.Opcode {
	case ipc.OpFSRead:
		handleFSRead(fs, senderPID, reqBuf[ipc.HeaderSize:], hdr.PayloadLen)
	default:
		debugPuts("[fs] unknown opcode\n")
		sendErrorReply(senderPID, -38) // ENOSYS
	}
}

// handleFSRead reads a file and sends its contents as the IPC reply.
func handleFSRead(fs *fat32.FileSystem, senderPID int, payload []byte, payloadLen uint64) {
	// Extract null-terminated path from payload
	if payloadLen == 0 || payloadLen > uint64(len(payload)) {
		sendErrorReply(senderPID, -22)
		return
	}
	pathBytes := payload[:payloadLen]
	// Find null terminator
	pathEnd := 0
	for pathEnd < len(pathBytes) && pathBytes[pathEnd] != 0 {
		pathEnd++
	}
	path := string(pathBytes[:pathEnd])

	debugPuts("[fs] FS_READ \"")
	debugPuts(path)
	debugPuts("\"\n")

	// Open and read the file
	file, err := fs.Open(path)
	if err != nil {
		debugPuts("[fs] open failed\n")
		sendErrorReply(senderPID, -2) // ENOENT
		return
	}
	defer file.Close()

	fileSize := uint64(file.Size())

	// Calculate reply size: header + file data
	replyDataSize := ipc.HeaderSize + int(fileSize)
	replyPages := (replyDataSize + 4095) / 4096
	if replyPages == 0 {
		replyPages = 1
	}

	// Allocate reply pages
	replySize := uintptr(replyPages) * 4096
	replyVA, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, replySize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), 0)
	if errno != 0 || int64(replyVA) < 0 {
		debugPuts("[fs] mmap for reply failed\n")
		sendErrorReply(senderPID, -12) // ENOMEM
		return
	}

	replyBuf := unsafe.Slice((*byte)(unsafe.Pointer(replyVA)), replySize)

	// Write reply header
	replyHdr := ipc.IPCHeader{
		Opcode:     ipc.OpFSRead,
		PayloadLen: fileSize,
		ErrorCode:  0,
	}
	ipc.MarshalHeader(replyBuf, &replyHdr)

	// Read file data into reply buffer
	dataBuf := replyBuf[ipc.HeaderSize:]
	n, err := file.Read(dataBuf)
	if err != nil && n == 0 {
		debugPuts("[fs] read failed\n")
		sendErrorReply(senderPID, -5) // EIO
		return
	}

	debugPuts("[fs] sending reply\n")

	// Send reply via IPC
	err = sys.IPCReply(senderPID, replyVA, replyPages)
	if err != nil {
		debugPuts("[fs] IPCReply failed\n")
	}
}

// sendErrorReply sends a minimal error reply (1 page with header only).
func sendErrorReply(senderPID int, errCode int64) {
	replyVA, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, 4096,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), 0)
	if errno != 0 || int64(replyVA) < 0 {
		return
	}

	replyBuf := unsafe.Slice((*byte)(unsafe.Pointer(replyVA)), 4096)
	replyHdr := ipc.IPCHeader{
		ErrorCode: errCode,
	}
	ipc.MarshalHeader(replyBuf, &replyHdr)

	sys.IPCReply(senderPID, replyVA, 1)
}

// debugPutDec writes an integer as decimal to the serial console.
func debugPutDec(n int) {
	if n < 0 {
		sys.DebugPutChar('-')
		n = -n
	}
	if n == 0 {
		sys.DebugPutChar('0')
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	for i < len(buf) {
		sys.DebugPutChar(buf[i])
		i++
	}
}
