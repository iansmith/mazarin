// fs is a standalone shepherd that owns the block device and provides
// filesystem services via delegated syscalls. It mounts ext2 using async
// DMA block I/O, reads the boot config to launch application shepherds,
// then enters a delegate serve loop handling LoadFile and ReadFilePages.
//
// Both the root filesystem (VirtIO block device, read-only) and the /tmp
// ramdisk (MemBlockDevice, read-write) are ext2.
package main

import (
	"fmt"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/constants"
	"mazzy/shared/fs/ext2"
	"mazzy/shared/hid"
	"mazzy/shared/sysid"
	"os"
	"strings"
	"syscall"
	"unsafe"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	// scratchPages is the number of DMA pages for ext2 metadata reads (own buffer).
	scratchPages = 8
	// ramdiskSectors is the number of 512-byte sectors for the /tmp ramdisk (128MB).
	ramdiskSectors = 262144
)

// mountKind identifies which mount point a path resolves to.
type mountKind int

const (
	mountRoot mountKind = iota // / — ext2 on VirtIO block device (read-only)
	mountTmp                   // /tmp — ext2 on MemBlockDevice ramdisk (read-write)
)

// mountTable tracks mounted filesystems and resolves paths to the correct one.
type mountTable struct {
	root  *ext2.FileSystem // / — ext2 on VirtIO block device (read-only)
	tmpFS *ext2.FileSystem // /tmp — ext2 on MemBlockDevice ramdisk (read-write)
}

// resolve finds the best mount for a path, returning the kind and the
// path relative to that mount's root.
func (mt *mountTable) resolve(path string) (mountKind, string) {
	if mt.tmpFS != nil && (strings.HasPrefix(path, "/tmp/") || path == "/tmp") {
		rel := path[4:] // strip "/tmp"
		if rel == "" {
			rel = "/"
		}
		return mountTmp, rel
	}
	return mountRoot, path
}

// getFS returns the ext2 filesystem for the given mount kind.
func (mt *mountTable) getFS(kind mountKind) *ext2.FileSystem {
	if kind == mountTmp {
		return mt.tmpFS
	}
	return mt.root
}

func main() {
	fmt.Println("[fs] starting filesystem shepherd")

	// 1. Register for block device soft IRQ
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[fs] QueryInputDevices failed: %v\n", err)
		os.Exit(1)
	}

	blockSlot := -1
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeBlock {
			err := sys.RegisterSoftIRQ(dev.IRQNum, 0)
			if err != nil {
				fmt.Printf("[fs] RegisterSoftIRQ failed: %v\n", err)
				os.Exit(1)
			}
			blockSlot = 0
			fmt.Printf("[fs] registered block device IRQ %d on slot %d\n", dev.IRQNum, blockSlot)
			break
		}
	}
	if blockSlot < 0 {
		fmt.Println("[fs] ERROR: no block device found")
		os.Exit(1)
	}

	// 2. Allocate DMA scratch buffer for the fs shepherd's own block reads
	//    (ext2 metadata, boot config, etc.). Self-targeted BlockSubmit.
	scratch, scratchErr := mem.AllocContiguous(scratchPages * 4096)
	if scratchErr != nil {
		fmt.Printf("[fs] AllocContiguous for scratch failed: %v\n", scratchErr)
		os.Exit(1)
	}
	fmt.Printf("[fs] scratch DMA buffer at 0x%x (%d pages)\n", scratch.Addr, scratchPages)

	// 3. Mount ext2 on VirtIO block device (read-only)
	blkDev := &asyncBlockDev{scratchVA: scratch.Addr}
	fsys, mountErr := ext2.Mount(blkDev)
	if mountErr != nil {
		fmt.Printf("[fs] ext2 mount failed: %v\n", mountErr)
		os.Exit(1)
	}
	fmt.Println("[fs] ext2 root mounted successfully (read-only)")

	// 3b. Create 128MB ext2 ramdisk at /tmp.
	// 512-byte device blocks to match ext2 reader's sectorBuf.
	memDev := blockdev.NewMemBlockDevice("ramdisk", 512, ramdiskSectors)
	if err := ext2.Format(memDev, "ramdisk"); err != nil {
		fmt.Printf("[fs] FATAL: format ramdisk: %v\n", err)
		os.Exit(1)
	}
	tmpFS, tmpErr := ext2.MountRW(memDev)
	if tmpErr != nil {
		fmt.Printf("[fs] FATAL: mount ramdisk: %v\n", tmpErr)
		os.Exit(1)
	}
	fmt.Printf("[fs] /tmp ramdisk mounted (128MB ext2, %d free blocks)\n",
		tmpFS.Superblock().FreeBlocksCount)

	mt := &mountTable{root: fsys, tmpFS: tmpFS}

	// 4. Read and parse startup config
	cfg := readStartupConfig(fsys)

	// 5. Launch application shepherds from startup config
	if cfg != nil {
		for _, s := range cfg.Shepherds {
			launchShepherd(fsys, s.Name, s.Path)
		}
	}

	// 6. Register as delegate handler for LoadFile and ReadFilePages
	delegateCh, delegateErr := sys.HandleSyscalls(sysid.LoadFile, sysid.ReadFilePages)
	if delegateErr != nil {
		fmt.Printf("[fs] HandleSyscalls failed: %v\n", delegateErr)
		os.Exit(1)
	}
	fmt.Println("[fs] registered as LoadFile + ReadFilePages delegate")

	// 6b. Start IPC mailbox recv goroutine (before SetReady so linux's
	// handshake notification can be received immediately).
	ipc := newFsIPC()
	go ipc.mailboxLoop()

	// 7. Signal readiness
	sys.SetReady(true)
	fmt.Println("[fs] SetReady(true)")

	// 8. Serve delegate requests + IPC requests.
	// Both are processed in the main goroutine to avoid concurrent
	// filesystem access (ext2 is not thread-safe).
	fmt.Println("[fs] entering serve loop")
	for {
		select {
		case req := <-delegateCh:
			switch req.SysID {
			case sysid.LoadFile:
				handleLoadFile(mt, &req)
			case sysid.ReadFilePages:
				handleReadFilePages(&req)
			}
		case ipcReq := <-ipc.requestCh:
			ipc.processRequest(&ipcReq, mt)
		}
	}
}

// handleLoadFile processes a delegated LoadFile request: reads the file
// into mmap'd pages, transfers them to the caller, and replies.
// Routes through the mount table to select the correct filesystem.
func handleLoadFile(mt *mountTable, req *sys.SyscallRequest) {
	path := req.PathString()
	fmt.Printf("[fs] LoadFile %q\n", path)

	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)

	va, numPages, bytesRead, err := readFileIntoPages(fsys, relPath)
	if err != nil {
		fmt.Printf("[fs] LoadFile %q: error=%v\n", path, err)
		req.Reply(-2) // ENOENT
		return
	}

	targetVA, terr := sys.TransferAndUnmap(int(req.CallerPID), va, numPages)
	if terr != nil {
		fmt.Printf("[fs] LoadFile %q: TransferAndUnmap failed\n", path)
		req.Reply(-5) // EIO
		return
	}

	fmt.Printf("[fs] LoadFile %q: transferred %d pages\n", path, numPages)
	req.LoadFileReply(0, uint64(targetVA), uint64(numPages), uint64(bytesRead))
}

// readFileIntoPages reads a file from an ext2 filesystem into mmap'd pages
// for TransferAndUnmap.
func readFileIntoPages(fsys *ext2.FileSystem, path string) (va uintptr, numPages int, bytesRead int, err error) {
	file, ferr := fsys.Open(path)
	if ferr != nil {
		return 0, 0, 0, ferr
	}
	defer file.Close()

	fileSize := int(file.Size())
	numPages = (fileSize + 4095) / 4096
	if numPages == 0 {
		numPages = 1
	}

	totalSize := uintptr(numPages) * 4096
	va, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, totalSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|0x20, // MAP_ANONYMOUS
		^uintptr(0), 0)
	if errno != 0 || int64(va) < 0 {
		return 0, 0, 0, errno
	}

	buf := unsafe.Slice((*byte)(unsafe.Pointer(va)), totalSize)
	n, _ := file.Read(buf[:fileSize])
	return va, numPages, n, nil
}

// handleReadFilePages returns ENOSYS — ext2 does not support direct DMA
// sector-run reads. Callers should use LoadFile instead.
func handleReadFilePages(req *sys.SyscallRequest) {
	path := req.PathString()
	fmt.Printf("[fs] ReadFilePages %q: not supported on ext2, use LoadFile\n", path)
	req.Reply(-38) // ENOSYS
}

// readStartupConfig reads and parses /startup.toml from the ext2 filesystem.
func readStartupConfig(fsys *ext2.FileSystem) *constants.StartupConfig {
	file, err := fsys.Open("/startup.toml")
	if err != nil {
		fmt.Printf("[fs] Open(/startup.toml) error: %v\n", err)
		return nil
	}
	defer file.Close()

	data, err := file.ReadAll()
	if err != nil {
		fmt.Println("[fs] failed to read startup.toml")
		return nil
	}

	var cfg constants.StartupConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("[fs] startup.toml parse error: %v\n", err)
		return nil
	}
	fmt.Printf("[fs] startup config: %d shepherds\n", len(cfg.Shepherds))
	return &cfg
}

// launchShepherd reads an ELF from ext2 and launches it as a new shepherd.
func launchShepherd(fsys *ext2.FileSystem, name, path string) {
	fmt.Printf("[fs] launching shepherd %s from %s\n", name, path)

	va, numPages, bytesRead, err := readFileIntoPages(fsys, path)
	if err != nil {
		fmt.Printf("[fs] failed to read %s: %v\n", path, err)
		return
	}

	rpErr := sys.RunShepherd(name, va, numPages, bytesRead)
	if rpErr != nil {
		fmt.Printf("[fs] RunShepherd failed for %s: %v\n", name, rpErr)
		return
	}
	fmt.Printf("[fs] shepherd %s launched\n", name)
}

// asyncBlockDev implements blockdev.BlockDevice using the fs shepherd's own
// DMA scratch buffer + WaitSoftIRQ for completion.
type asyncBlockDev struct {
	scratchVA uintptr // base of MAZARIN_CONTIGUOUS scratch pages
}

func (d *asyncBlockDev) Name() string      { return "virtio-blk-async" }
func (d *asyncBlockDev) Close() error      { return nil }
func (d *asyncBlockDev) BlockSize() uint64 { return 512 }
func (d *asyncBlockDev) NumBlocks() uint64 { return 0 }
func (d *asyncBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

func (d *asyncBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return fmt.Errorf("buffer too small")
	}

	// Submit async read targeting the first scratch page (self-targeted, SID=0)
	dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA)), 512)
	_, serr := mem.BlockSubmit(0, lba, 1, dmaBuf, 0)
	if serr != nil {
		return serr
	}

	// Block until completion via WaitSoftIRQ
	var softBuf hid.SoftIRQReturn
	_, werr := sys.WaitSoftIRQ(0, &softBuf)
	if werr != nil {
		return werr
	}

	// Check status
	if softBuf.Length > 0 && softBuf.Events[0].Code != 0 {
		return syscall.EIO
	}

	copy(buf[:512], dmaBuf)
	return nil
}
