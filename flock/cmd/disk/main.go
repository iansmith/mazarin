// disk is a userspace priest that serves filesystem requests via IPC.
// It registers for block device ownership, mounts the FAT32 filesystem
// using SysBlockRead for sector I/O, and serves file read requests
// from other priests.
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
	"mazzy/shared/hid"
	"mazzy/shared/ipc"
	"os"
	"syscall"
	"unsafe"
)

func main() {
	fmt.Println("[disk] starting disk priest")

	// 1. Query devices to find the block device virtual IRQ
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[disk] QueryInputDevices failed: %v\n", err)
		os.Exit(1)
	}

	blockSlot := -1
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeBlock {
			// Register for block device ownership
			err := sys.RegisterSoftIRQ(dev.IRQNum, 0) // slot 0
			if err != nil {
				fmt.Printf("[disk] RegisterSoftIRQ for block device failed: %v\n", err)
				os.Exit(1)
			}
			blockSlot = 0
			fmt.Printf("[disk] registered block device IRQ %d on slot %d\n", dev.IRQNum, blockSlot)
			break
		}
	}

	if blockSlot < 0 {
		fmt.Println("[disk] ERROR: no block device found")
		os.Exit(1)
	}

	// 2. Mount FAT32 filesystem using SysBlockRead
	blkDev := &userspaceBlockDev{}
	fs, fsErr := fat32.Mount(blkDev)
	if fsErr != nil {
		fmt.Printf("[disk] FAT32 mount failed: %v\n", fsErr)
		os.Exit(1)
	}
	fmt.Println("[disk] FAT32 mounted successfully")

	// 3. Serve IPC requests
	fmt.Println("[disk] entering IPC serve loop")
	serveLoop(fs)
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
			fmt.Printf("[disk] IPCRecv error: %v\n", err)
			continue
		}

		fmt.Printf("[disk] request from P%d (%d pages at 0x%X)\n", senderPID, reqPages, reqVA)

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
		fmt.Printf("[disk] unknown opcode %d\n", hdr.Opcode)
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

	fmt.Printf("[disk] FS_READ \"%s\"\n", path)

	// Open and read the file
	file, err := fs.Open(path)
	if err != nil {
		fmt.Printf("[disk] open failed: %v\n", err)
		sendErrorReply(senderPID, -2) // ENOENT
		return
	}
	defer file.Close()

	fileSize := uint64(file.Size())
	fmt.Printf("[disk] file size: %d bytes\n", fileSize)

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
		fmt.Printf("[disk] mmap for reply failed\n")
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
		fmt.Printf("[disk] read failed: %v\n", err)
		sendErrorReply(senderPID, -5) // EIO
		return
	}

	fmt.Printf("[disk] read %d bytes, sending reply (%d pages)\n", n, replyPages)

	// Send reply via IPC
	err = sys.IPCReply(senderPID, replyVA, replyPages)
	if err != nil {
		fmt.Printf("[disk] IPCReply failed: %v\n", err)
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
