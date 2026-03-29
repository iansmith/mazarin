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
	root   *ext2.FileSystem // / — ext2 on VirtIO block device (read-only)
	tmpFS  *ext2.FileSystem // /tmp — ext2 on MemBlockDevice ramdisk (read-write)
	blkDev *asyncBlockDev   // DMA block device (for batched reads on root)
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

	mt := &mountTable{root: fsys, tmpFS: tmpFS, blkDev: blkDev}

	// 4. Register as delegate handler for LoadFile and ReadFilePages.
	delegateCh, err := sys.HandleSyscalls(sysid.LoadFile, sysid.ReadFilePages)
	if err != nil {
		fmt.Printf("[fs] HandleSyscalls failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[fs] registered as LoadFile + ReadFilePages delegate")

	// 4b. Start IPC goroutine.
	ipc := newFsIPC()
	go ipc.mailboxLoop()

	// 5. Signal readiness — delegate handler is running, serve loop
	// will start momentarily. Shepherds waiting on fs can proceed.
	sys.SetReady(true)
	fmt.Println("[fs] SetReady(true)")

	// 6. Boot sequence goroutine: launch linux → rachel (with ready
	// waits) → then read startup.toml and launch remaining shepherds.
	// Runs as a goroutine so the serve loop can process LoadFile
	// requests during shepherd boot (e.g., rachel loading fontsvc.maz).
	go bootSequence(fsys, blkDev)

	// 7. Serve delegate requests + IPC requests.
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

	// Use batched DMA for root mount, fall back to sequential for ramdisk.
	var blk *asyncBlockDev
	if kind == mountRoot {
		blk = mt.blkDev
	}
	sys.UartWriteString(fmt.Sprintf("[fs] LoadFile %q: reading (transferable)...\n", path))
	va, numPages, bytesRead, err := readFileIntoPages(fsys, blk, relPath, true)
	if err != nil {
		fmt.Printf("[fs] LoadFile %q: error=%v\n", path, err)
		req.Reply(-2) // ENOENT
		return
	}
	sys.UartWriteString(fmt.Sprintf("[fs] LoadFile %q: read done (%d pages, %d bytes), transferring...\n", path, numPages, bytesRead))

	targetVA, terr := sys.TransferAndUnmap(int(req.CallerPID), va, numPages)
	if terr != nil {
		fmt.Printf("[fs] LoadFile %q: TransferAndUnmap failed (%d pages, va=0x%x, targetPID=%d): %v\n",
			path, numPages, va, req.CallerPID, terr)
		req.Reply(-5) // EIO
		return
	}

	fmt.Printf("[fs] LoadFile %q: transferred %d pages\n", path, numPages)
	req.LoadFileReply(0, uint64(targetVA), uint64(numPages), uint64(bytesRead))
}

// readFileIntoPages reads a file from an ext2 filesystem into pages.
// When transferable is true, uses kernel-tracked pages (AllocPages) that
// can be passed to TransferAndUnmap. When false, uses anonymous mmap
// for temporary use (caller must munmap).
// When blkDev is non-nil, uses batched DMA (submits up to scratchPages
// concurrent BlockSubmits). When nil (ramdisk), falls back to sequential
// file.Read.
func readFileIntoPages(fsys *ext2.FileSystem, blkDev *asyncBlockDev, path string, transferable bool) (va uintptr, numPages int, bytesRead int, err error) {
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
	if transferable {
		// Kernel-tracked pages so TransferAndUnmap can validate ownership.
		ptr, allocErr := mem.AllocPages(numPages, mem.PageShared)
		if allocErr != nil {
			return 0, 0, 0, allocErr
		}
		va = uintptr(ptr)
	} else {
		// Anonymous mmap for temporary use (caller munmaps after).
		var errno syscall.Errno
		va, _, errno = syscall.RawSyscall6(
			syscall.SYS_MMAP, 0, totalSize,
			syscall.PROT_READ|syscall.PROT_WRITE,
			syscall.MAP_PRIVATE|0x20, // MAP_ANONYMOUS
			^uintptr(0), 0)
		if errno != 0 || int64(va) < 0 {
			return 0, 0, 0, errno
		}
	}

	dst := unsafe.Slice((*byte)(unsafe.Pointer(va)), totalSize)

	// Ramdisk fallback: sequential reads (no DMA).
	if blkDev == nil {
		n, _ := file.Read(dst[:fileSize])
		return va, numPages, n, nil
	}

	// Batched DMA path: submit up to scratchPages reads concurrently.
	inode := file.InodeRaw()
	totalBlocks := uint32((fileSize + int(fsys.BlockSizeBytes()) - 1) / int(fsys.BlockSizeBytes()))

	// Resolve all block numbers upfront. ResolveBlockList caches
	// indirect pointer tables internally, so this reads each metadata
	// block at most once regardless of file size.
	sys.UartWriteString(fmt.Sprintf("[fs] resolving %d blocks for %s\n", totalBlocks, path))
	allBlocks, rerr := fsys.ResolveBlockList(inode, 0, totalBlocks)
	if rerr != nil {
		return 0, 0, 0, rerr
	}
	sys.UartWriteString(fmt.Sprintf("[fs] resolved %d blocks, starting batched DMA for %s\n", len(allBlocks), path))

	for blockIdx := uint32(0); blockIdx < totalBlocks; {
		batch := totalBlocks - blockIdx
		if batch > scratchPages {
			batch = scratchPages
		}

		batchBlocks := allBlocks[blockIdx : blockIdx+batch]

		// Submit all reads in this batch concurrently.
		if blockIdx%64 == 0 || blockIdx+batch >= totalBlocks {
			sys.UartWriteString(fmt.Sprintf("[fs] batch %d/%d (%d blocks)\n", blockIdx, totalBlocks, batch))
		}
		submitted := 0
		for i, bn := range batchBlocks {
			if bn == 0 {
				// Sparse block — zero the scratch slot.
				off := uintptr(i) * asyncBlockSize
				scratch := unsafe.Slice((*byte)(unsafe.Pointer(blkDev.scratchVA+off)), asyncBlockSize)
				for k := range scratch {
					scratch[k] = 0
				}
				continue
			}
			if serr := blkDev.submitRead(bn, i); serr != nil {
				return 0, 0, 0, serr
			}
			submitted++
		}

		// Wait for all submitted reads to complete.
		if submitted > 0 {
			if werr := blkDev.waitReads(submitted); werr != nil {
				return 0, 0, 0, werr
			}
		}

		// Copy from scratch buffer to destination pages.
		for i := range batchBlocks {
			srcOff := uintptr(i) * asyncBlockSize
			dstOff := int(blockIdx+uint32(i)) * asyncBlockSize
			remaining := fileSize - dstOff
			copyLen := asyncBlockSize
			if remaining < copyLen {
				copyLen = remaining
			}
			if copyLen > 0 {
				src := unsafe.Slice((*byte)(unsafe.Pointer(blkDev.scratchVA+srcOff)), copyLen)
				copy(dst[dstOff:dstOff+copyLen], src)
			}
		}

		blockIdx += batch
	}

	return va, numPages, fileSize, nil
}

// handleReadFilePages returns ENOSYS — ext2 does not support direct DMA
// sector-run reads. Callers should use LoadFile instead.
func handleReadFilePages(req *sys.SyscallRequest) {
	path := req.PathString()
	fmt.Printf("[fs] ReadFilePages %q: not supported on ext2, use LoadFile\n", path)
	req.Reply(-38) // ENOSYS
}

// drainDelegateRequests processes any pending delegate requests between
// shepherd launches. If a request is found, the caller did not wait for
// fs readiness — log a warning but serve the request anyway.
func drainDelegateRequests(delegateCh <-chan sys.SyscallRequest, mt *mountTable) {
	for {
		select {
		case req := <-delegateCh:
			fmt.Printf("[fs] ERROR: loading file %q but the caller should have waited for fs to become ready\n", req.PathString())
			switch req.SysID {
			case sysid.LoadFile:
				handleLoadFile(mt, &req)
			case sysid.ReadFilePages:
				handleReadFilePages(&req)
			}
		default:
			return
		}
	}
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
func launchShepherd(fsys *ext2.FileSystem, blkDev *asyncBlockDev, name, path string) {
	fmt.Printf("[fs] launching shepherd %s from %s\n", name, path)

	va, numPages, bytesRead, err := readFileIntoPages(fsys, blkDev, path, false)
	if err != nil {
		fmt.Printf("[fs] failed to read %s: %v\n", path, err)
		return
	}
	fmt.Printf("[fs] read %s: %d pages, %d bytes\n", name, numPages, bytesRead)

	rpErr := sys.RunShepherd(name, va, numPages, bytesRead)
	// Free temporary pages (RunShepherd copies them to the new shepherd).
	syscall.RawSyscall6(syscall.SYS_MUNMAP, va, uintptr(numPages)*4096, 0, 0, 0, 0)
	if rpErr != nil {
		fmt.Printf("[fs] RunShepherd failed for %s: %v\n", name, rpErr)
		return
	}
	fmt.Printf("[fs] shepherd %s launched\n", name)
}

// bootSequence launches the core shepherds in dependency order, then reads
// startup.toml and launches any remaining application shepherds. Runs as a
// goroutine so the main goroutine's serve loop can process LoadFile requests
// during shepherd boot (e.g., rachel loading fontsvc.maz).
//
// Order: rachel first (only needs fs for LoadFile), then linux (needs both
// fs for IPC and rachel for fonts/WM). TOML shepherds launch last since
// they may need linux's syscall delegation active.
func bootSequence(fsys *ext2.FileSystem, blkDev *asyncBlockDev) {
	// 1. Launch rachel and wait — provides window manager + font service.
	// Rachel only depends on fs (already ready) for loading fontsvc.maz.
	launchShepherd(fsys, blkDev, "rachel", "/rachel.elf")
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		fmt.Printf("[fs] FATAL: rachel shepherd not ready: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[fs] rachel shepherd ready")

	// 2. Launch linux and wait — provides syscall delegation (Openat, Read, etc.).
	// Linux depends on both fs (IPC) and rachel (fonts/WM).
	launchShepherd(fsys, blkDev, "linux", "/linux.elf")
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		fmt.Printf("[fs] FATAL: linux shepherd not ready: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[fs] linux shepherd ready")

	// 3. Read startup config and launch remaining application shepherds.
	cfg := readStartupConfig(fsys)
	if cfg != nil {
		for _, s := range cfg.Shepherds {
			launchShepherd(fsys, blkDev, s.Name, s.Path)
		}
	}
	fmt.Println("[fs] boot sequence complete")
}

// asyncBlockDev implements blockdev.BlockDevice using the fs shepherd's own
// DMA scratch buffer + WaitSoftIRQ for completion. Reports a 4096-byte block
// size so ext2 (which also uses 4096-byte blocks) reads a full page per call,
// submitting 8 sectors per DMA roundtrip instead of 1.
type asyncBlockDev struct {
	scratchVA uintptr // base of MAZARIN_CONTIGUOUS scratch pages
}

const (
	asyncBlockSize    = 4096
	sectorsPerBlock   = asyncBlockSize / 512
)

func (d *asyncBlockDev) Name() string      { return "virtio-blk-async" }
func (d *asyncBlockDev) Close() error      { return nil }
func (d *asyncBlockDev) BlockSize() uint64 { return asyncBlockSize }
func (d *asyncBlockDev) NumBlocks() uint64 { return 0 }
func (d *asyncBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

// submitRead issues a BlockSubmit for one 4096-byte page without waiting.
// scratchSlot selects which 4096-byte region of the scratch buffer to use
// (0..scratchPages-1). The block number is an ext2 block number.
func (d *asyncBlockDev) submitRead(blockNum uint32, scratchSlot int) error {
	sectorLBA := uint64(blockNum) * sectorsPerBlock
	off := uintptr(scratchSlot) * asyncBlockSize
	dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA+off)), asyncBlockSize)
	_, serr := mem.BlockSubmit(0, sectorLBA, sectorsPerBlock, dmaBuf, 0)
	return serr
}

// waitReads waits for exactly `expected` block I/O completions via WaitSoftIRQ.
// Returns an error if any completion reports a non-zero status.
func (d *asyncBlockDev) waitReads(expected int) error {
	remaining := expected
	for remaining > 0 {
		var softBuf hid.SoftIRQReturn
		n, err := sys.WaitSoftIRQ(0, &softBuf)
		if err != nil {
			return err
		}
		for i := 0; i < n && remaining > 0; i++ {
			if softBuf.Events[i].Code != 0 {
				return syscall.EIO
			}
			remaining--
		}
	}
	return nil
}

func (d *asyncBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < asyncBlockSize {
		return fmt.Errorf("buffer too small")
	}

	// Convert 4096-byte block LBA to 512-byte sector LBA
	sectorLBA := lba * sectorsPerBlock

	sys.UartWriteString("r1")
	// Submit async read of 8 sectors (one full page) into DMA scratch buffer
	dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA)), asyncBlockSize)
	_, serr := mem.BlockSubmit(0, sectorLBA, sectorsPerBlock, dmaBuf, 0)
	if serr != nil {
		sys.UartWriteString("r!")
		return serr
	}

	sys.UartWriteString("r2")
	// Block until completion arrives via soft IRQ
	var softBuf hid.SoftIRQReturn
	_, err := sys.WaitSoftIRQ(0, &softBuf)
	if err != nil {
		sys.UartWriteString("r!")
		return err
	}

	sys.UartWriteString("r3")
	// Check status
	if softBuf.Length > 0 && softBuf.Events[0].Code != 0 {
		return syscall.EIO
	}

	copy(buf[:asyncBlockSize], dmaBuf)
	return nil
}
