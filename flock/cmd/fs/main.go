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
	"mazzy/mazarin/uring"
	"mazzy/shared/blockdev"
	"mazzy/shared/constants"
	"mazzy/shared/fs/ext2"
	"mazzy/shared/hid"
	"mazzy/shared/iouring"
	uringipc "mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
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

	// Timing hooks disabled — enable for block I/O debugging.
	ext2.ReadIntoTimingHook = nil
	ext2.ReadBlocksTimingHook = nil

	// Experiment 3: Enable per-syscall tracing for this SID around large readBlocks.
	// Magic marker 0xDB6 sets kernel DbgTraceSID via DebugPrint syscall.
	var fsSID atomic.Int32
	fsSID.Store(-1)
	ext2.ReadBlocksPreHook = func(blocks int) {
		sid := fsSID.Load()
		if sid < 0 {
			// Resolve our own SID once via ShepherdInfo
			entries, err := sys.ShepherdInfo()
			if err == nil {
				for _, e := range entries {
					fn := string(e.Filename[:e.FilenameLen])
					if fn == "/fs.elf" || fn == "fs" {
						sid = int32(e.SID)
						fsSID.Store(sid)
						break
					}
				}
			}
		}
		if sid >= 0 {
			sys.UartWriteString(fmt.Sprintf("[ext2] TRACE ON sid=%d blocks=%d\n", sid, blocks))
			syscall.RawSyscall6(0x1006, 0xDB6, uintptr(sid), 0, 0, 0, 0)
		}
	}
	ext2.ReadBlocksPostHook = func(blocks int) {
		// Disable tracing: set DbgTraceSID = -1
		sys.UartWriteString("[ext2] TRACE OFF\n")
		syscall.RawSyscall6(0x1006, 0xDB6, ^uintptr(0), 0, 0, 0, 0)
	}

	// 1. Register for block device soft IRQ
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[fs] QueryInputDevices failed: %v\n", err)
		os.Exit(1)
	}

	blockFound := false
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeBlock {
			blockFound = true
			break
		}
	}
	if !blockFound {
		fmt.Println("[fs] ERROR: no block device found")
		os.Exit(1)
	}

	// 2. Allocate io_uring ring page for block I/O completions.
	ringPage, ringErr := mem.AllocPages(1, mem.PageShared)
	if ringErr != nil {
		fmt.Printf("[fs] AllocPages for io_uring ring failed: %v\n", ringErr)
		os.Exit(1)
	}
	ioRing := (*iouring.IORing)(ringPage)
	ringID, setupErr := sys.IOUringSetup(ioRing, 0)
	if setupErr != nil {
		fmt.Printf("[fs] IOUringSetup failed: %v\n", setupErr)
		os.Exit(1)
	}
	fmt.Printf("[fs] io_uring created: ringID=%d\n", ringID)

	// 3. Allocate DMA scratch buffer for the fs shepherd's own block reads
	//    (ext2 metadata, boot config, etc.). Self-targeted BlockSubmit.
	scratch, scratchErr := mem.AllocContiguous(scratchPages * 4096)
	if scratchErr != nil {
		fmt.Printf("[fs] AllocContiguous for scratch failed: %v\n", scratchErr)
		os.Exit(1)
	}
	// 4. Create block device.
	blkDev := newAsyncBlockDev(scratch.Addr, ioRing, ringID)
	ipcSrv := newFsIPCServer()

	fsys, mountErr := ext2.Mount(blkDev)
	if mountErr != nil {
		fmt.Printf("[fs] ext2 mount failed: %v\n", mountErr)
		os.Exit(1)
	}
	fmt.Println("[fs] ext2 root mounted successfully (read-only)")

	// 4b. Create 128MB ext2 ramdisk at /tmp.
	// Backing store is off-heap (kernel-allocated PageRamdisk pages) to avoid
	// GC pressure — 128MB on the Go heap causes multi-second GC pauses at GOGC=5%.
	// 512-byte device blocks to match ext2 reader's sectorBuf.
	ramdiskBytes := 512 * ramdiskSectors
	ramdiskPages := (ramdiskBytes + 4095) / 4096
	ramdiskBacking, ramdiskErr := mem.AllocPagesSlice(ramdiskPages, mem.PageRamdisk)
	if ramdiskErr != nil {
		fmt.Printf("[fs] FATAL: AllocPagesSlice for ramdisk failed: %v\n", ramdiskErr)
		os.Exit(1)
	}
	memDev := blockdev.NewMemBlockDeviceFromBacking("ramdisk", 512, ramdiskBacking)
	if err := ext2.Format(memDev, "ramdisk"); err != nil {
		fmt.Printf("[fs] FATAL: format ramdisk: %v\n", err)
		os.Exit(1)
	}
	tmpFS, tmpErr := ext2.MountRW(memDev)
	if tmpErr != nil {
		fmt.Printf("[fs] FATAL: mount ramdisk: %v\n", tmpErr)
		os.Exit(1)
	}
	mt := &mountTable{root: fsys, tmpFS: tmpFS}

	// 5. Register as delegate handler for LoadFile and ReadFilePages.
	// Requests arrive via uring Dispatcher (ProtoFSDelegateReq).
	// Shepherd file operations arrive via ProtoFSIPCReq.
	err = sys.RegisterSyscallHandlers(sysid.LoadFile, sysid.ReadFilePages)
	if err != nil {
		fmt.Printf("[fs] RegisterSyscallHandlers failed: %v\n", err)
		os.Exit(1)
	}
	fsDelegateCh := make(chan any, 8)
	fsIPCCh := make(chan any, 8)
	fsDisp := uring.NewDispatcher()
	fsDisp.On(uringipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, fsDelegateCh)
	fsDisp.On(uringipc.ProtoFSIPCReq, DecodeReq, fsIPCCh)
	fsDisp.OnDeath(func(deadSID int16) {
		sys.UartWriteString(fmt.Sprintf("[fs] shepherd %d died\n", deadSID))
	})
	fsDisp.Start()
	fmt.Println("[fs] registered as LoadFile + ReadFilePages delegate (uring)")

	// 5. Signal readiness — delegate handler is running, serve loop
	// will start momentarily. Shepherds waiting on fs can proceed.
	sys.SetReady(true)
	fmt.Println("[fs] SetReady(true)")

	// 6. Boot sequence goroutine: launch linux → rachel (with ready
	// waits) → then read startup.toml and launch remaining shepherds.
	// Runs as a goroutine so the serve loop can process LoadFile
	// requests during shepherd boot (e.g., rachel loading fontsvc.maz).
	go bootSequence(fsys)

	// 7. Serve delegate requests + shepherd IPC requests.
	// Both are processed in the main goroutine to avoid concurrent
	// filesystem access (ext2 is not thread-safe).
	fmt.Println("[fs] entering serve loop")
	for {
		select {
		case raw := <-fsDelegateCh:
			req, ok := raw.(sys.SyscallRequest)
			if !ok {
				continue
			}
			switch req.SysID {
			case sysid.LoadFile:
				handleLoadFile(mt, &req)
			case sysid.ReadFilePages:
				handleReadFilePages(&req)
			}
		case raw := <-fsIPCCh:
			req, ok := raw.(fsIPCRequest)
			if !ok {
				continue
			}
			ipcSrv.processRequest(req, mt)
		}
	}
}

// handleLoadFile processes a delegated LoadFile request: reads the file
// into mmap'd pages, transfers them to the caller, and replies.
// Routes through the mount table to select the correct filesystem.
func handleLoadFile(mt *mountTable, req *sys.SyscallRequest) {
	path := req.PathString()
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)

	va, numPages, bytesRead, err := readFileIntoPages(fsys, relPath, true)
	if err != nil {
		fmt.Printf("[fs] LoadFile %q: error=%v\n", path, err)
		req.Reply(-2) // ENOENT
		return
	}
	targetVA, terr := sys.TransferAndUnmap(int(req.CallerPID), va, numPages)
	if terr != nil {
		fmt.Printf("[fs] LoadFile %q: TransferAndUnmap failed (%d pages, va=0x%x, targetPID=%d): %v\n",
			path, numPages, va, req.CallerPID, terr)
		// TransferAndUnmap failed — pages are still mapped in our address space.
		syscall.RawSyscall6(syscall.SYS_MUNMAP, va, uintptr(numPages)*4096, 0, 0, 0, 0)
		req.Reply(-5) // EIO
		return
	}

	req.LoadFileReply(0, uint64(targetVA), uint64(numPages), uint64(bytesRead))
}

// readFileIntoPages reads a file from an ext2 filesystem into pages.
// When transferable is true, uses kernel-tracked pages (AllocPages) that
// can be passed to TransferAndUnmap. When false, uses anonymous mmap
// for temporary use (caller must munmap).
// The ext2 ReadInto method handles batched I/O internally — if the
// underlying BlockDevice implements BatchBlockDevice, all data blocks
// are read in a single batch operation.
func readFileIntoPages(fsys *ext2.FileSystem, path string, transferable bool) (va uintptr, numPages int, bytesRead int, err error) {
	t0 := time.Now()
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

	n, rerr := file.ReadInto(dst[:fileSize])

	sys.UartWriteString(fmt.Sprintf("[fs] %s: %d bytes, %d pages, %dms\n",
		path, fileSize, numPages, time.Since(t0).Milliseconds()))

	if rerr != nil {
		// Free allocated pages on read failure to avoid leaking memory.
		syscall.RawSyscall6(syscall.SYS_MUNMAP, va, totalSize, 0, 0, 0, 0)
		return 0, 0, 0, rerr
	}
	return va, numPages, n, nil
}

// handleReadFilePages returns ENOSYS — ext2 does not support direct DMA
// sector-run reads. Callers should use LoadFile instead.
func handleReadFilePages(req *sys.SyscallRequest) {
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
	return &cfg
}

// launchShepherd reads an ELF from ext2 and launches it as a new shepherd.
func launchShepherd(fsys *ext2.FileSystem, name, path string) {
	sys.UartWriteString("[fs] reading " + path + "...\n")
	va, numPages, bytesRead, err := readFileIntoPages(fsys, path, false)
	if err != nil {
		sys.UartWriteString("[fs] failed to read " + path + "\n")
		return
	}
	rpErr := sys.RunShepherd(name, va, numPages, bytesRead)
	// Free temporary pages (RunShepherd copies them to the new shepherd).
	syscall.RawSyscall6(syscall.SYS_MUNMAP, va, uintptr(numPages)*4096, 0, 0, 0, 0)
	if rpErr != nil {
		sys.UartWriteString("[fs] RunShepherd FAILED for " + name + "\n")
		return
	}
}

// bootSequence launches the core shepherds in dependency order, then reads
// startup.toml and launches any remaining application shepherds. Runs as a
// goroutine so the main goroutine's serve loop can process LoadFile requests
// during shepherd boot (e.g., rachel loading fontsvc.maz).
//
// Order: rachel first (only needs fs for LoadFile), then linux (needs both
// fs for IPC and rachel for fonts/WM). TOML shepherds launch last since
// they may need linux's syscall delegation active.
func bootSequence(fsys *ext2.FileSystem) {
	// 1. Launch rachel and wait — provides window manager + font service.
	// Rachel only depends on fs (already ready) for loading fontsvc.maz.
	launchShepherd(fsys, "rachel", "/rachel.elf")
	if err := sys.WaitForShepherdReady("rachel", 30); err != nil {
		sys.UartWriteString("[fs] FATAL: rachel not ready\n")
		return
	}
	// 2. Launch linux and wait — provides syscall delegation (Openat, Read, etc.).
	// Linux depends on both fs (IPC) and rachel (fonts/WM).
	launchShepherd(fsys, "linux", "/linux.elf")
	sys.UartWriteString("[fs] waiting for linux ready...\n")
	if err := sys.WaitForShepherdReady("linux", 30); err != nil {
		sys.UartWriteString("[fs] FATAL: linux not ready: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString("[fs] linux is ready!\n")
	// 3. Read startup config and launch remaining application shepherds.
	sys.UartWriteString("[fs] reading startup.toml...\n")
	cfg := readStartupConfig(fsys)
	if cfg != nil {
		for _, s := range cfg.Shepherds {
			sys.UartWriteString("[fs] launching " + s.Name + " from " + s.Path + "\n")
			launchShepherd(fsys, s.Name, s.Path)
		}
	} else {
		sys.UartWriteString("[fs] no startup.toml\n")
	}
	sys.UartWriteString("[fs] boot sequence complete\n")
}

// asyncBlockDev implements blockdev.BlockDevice backed by io_uring.
// All ring access is serialized by a mutex. Uses IOUringEnter (P-holding,
// RawSyscall) so the Go scheduler cannot steal the P during short I/O waits.
// This avoids P contention with the boot sequence goroutine's polling loop.
type asyncBlockDev struct {
	mu        sync.Mutex
	scratchVA uintptr         // base of MAZARIN_CONTIGUOUS scratch pages
	ioRing    *iouring.IORing // shared io_uring ring page
	ringID    int             // io_uring instance ID from IOUringSetup
}

const (
	asyncBlockSize  = 4096
	sectorsPerBlock = asyncBlockSize / 512
)

// newAsyncBlockDev creates the block device.
func newAsyncBlockDev(scratchVA uintptr, ring *iouring.IORing, ringID int) *asyncBlockDev {
	return &asyncBlockDev{
		scratchVA: scratchVA,
		ioRing:    ring,
		ringID:    ringID,
	}
}

var singleReadCount int
var singleReadTotal time.Duration

// doReadBlock reads a single 4KB block via io_uring. Only called from dmaWorker.
func (d *asyncBlockDev) doReadBlock(lba uint64, buf []byte) error {
	t0 := time.Now()
	defer func() {
		singleReadCount++
		singleReadTotal += time.Since(t0)
	}()

	sectorLBA := lba * sectorsPerBlock
	dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA)), asyncBlockSize)

	// Write 1 SQE to the submission ring.
	sqTail := atomic.LoadUint32(&d.ioRing.SQTail)
	idx := sqTail & iouring.SQMask
	d.ioRing.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpRead,
		FD:       0, // block device
		Off:      sectorLBA,
		Addr:     uint64(uintptr(unsafe.Pointer(&dmaBuf[0]))),
		Len:      sectorsPerBlock,
		UserData: 0,
	}
	atomic.StoreUint32(&d.ioRing.SQTail, sqTail+1)

	// Submit 1 SQE and wait for 1 CQE (P held throughout).
	_, err := sys.IOUringEnter(d.ringID, 1, 1, 0)
	if err != nil {
		return err
	}

	// Drain 1 CQE.
	cqHead := atomic.LoadUint32(&d.ioRing.CQHead)
	cqe := &d.ioRing.CQEntries[cqHead&iouring.CQMask]
	if cqe.Res < 0 {
		return syscall.EIO
	}
	atomic.StoreUint32(&d.ioRing.CQHead, cqHead+1)

	copy(buf[:asyncBlockSize], dmaBuf)
	return nil
}

// doReadBatch reads multiple 4KB blocks in batches of scratchPages.
// Only called from dmaWorker.
func (d *asyncBlockDev) doReadBatch(blocks []uint32, dst []byte) error {
	total := uint32(len(blocks))

	for blockIdx := uint32(0); blockIdx < total; {
		batch := total - blockIdx
		if batch > scratchPages {
			batch = scratchPages
		}
		batchBlocks := blocks[blockIdx : blockIdx+batch]

		submitted := uint32(0)
		sqTail := atomic.LoadUint32(&d.ioRing.SQTail)
		for i, bn := range batchBlocks {
			if bn == 0 {
				// Sparse block — zero-fill.
				off := uintptr(i) * asyncBlockSize
				scratch := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA+off)), asyncBlockSize)
				for k := range scratch {
					scratch[k] = 0
				}
				continue
			}
			sectorLBA := uint64(bn) * sectorsPerBlock
			off := uintptr(i) * asyncBlockSize
			dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA+off)), asyncBlockSize)

			idx := sqTail & iouring.SQMask
			d.ioRing.SQEntries[idx] = iouring.SQEntry{
				Opcode:   iouring.IOUringOpRead,
				FD:       0,
				Off:      sectorLBA,
				Addr:     uint64(uintptr(unsafe.Pointer(&dmaBuf[0]))),
				Len:      sectorsPerBlock,
				UserData: uint64(i),
			}
			sqTail++
			submitted++
		}
		atomic.StoreUint32(&d.ioRing.SQTail, sqTail)

		if submitted > 0 {
			// Submit SQEs and wait for all completions (P held throughout).
			nret, serr := sys.IOUringEnter(d.ringID, submitted, submitted, 0)
			if serr != nil {
				return serr
			}

			// Verify completions actually arrived.
			cqHead := atomic.LoadUint32(&d.ioRing.CQHead)
			cqTail := atomic.LoadUint32(&d.ioRing.CQTail)
			actual := cqTail - cqHead
			if actual < submitted {
				sys.UartWriteString(fmt.Sprintf("[dma:UNDERFLOW] ret=%d submitted=%d cqH=%d cqT=%d actual=%d\n",
					nret, submitted, cqHead, cqTail, actual))
				// Don't drain non-existent CQEs — return EIO.
				return syscall.EIO
			}

			// Drain all CQEs.
			for i := uint32(0); i < submitted; i++ {
				cqe := &d.ioRing.CQEntries[(cqHead+i)&iouring.CQMask]
				if cqe.Res < 0 {
					atomic.StoreUint32(&d.ioRing.CQHead, cqHead+submitted)
					return syscall.EIO
				}
			}
			atomic.StoreUint32(&d.ioRing.CQHead, cqHead+submitted)
		}

		for i := range batchBlocks {
			srcOff := uintptr(i) * asyncBlockSize
			dstOff := int(blockIdx+uint32(i)) * asyncBlockSize
			dstEnd := dstOff + asyncBlockSize
			if dstEnd > len(dst) {
				dstEnd = len(dst)
			}
			if dstOff < len(dst) {
				src := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA+srcOff)), dstEnd-dstOff)
				copy(dst[dstOff:dstEnd], src)
			}
		}
		blockIdx += batch
	}

	return nil
}

func (d *asyncBlockDev) Name() string      { return "virtio-blk-iouring" }
func (d *asyncBlockDev) Close() error      { return nil }
func (d *asyncBlockDev) BlockSize() uint64 { return asyncBlockSize }
func (d *asyncBlockDev) NumBlocks() uint64 { return 0 }
func (d *asyncBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

// ReadBlock reads a single 4KB block. Thread-safe via mutex.
func (d *asyncBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < asyncBlockSize {
		return fmt.Errorf("buffer too small")
	}
	d.mu.Lock()
	err := d.doReadBlock(lba, buf)
	d.mu.Unlock()
	return err
}

// ReadBlocks reads multiple 4KB blocks in batches. Thread-safe via mutex.
func (d *asyncBlockDev) ReadBlocks(lbas []uint64, dst []byte) error {
	blocks := make([]uint32, len(lbas))
	for i, lba := range lbas {
		blocks[i] = uint32(lba)
	}
	d.mu.Lock()
	err := d.doReadBatch(blocks, dst)
	d.mu.Unlock()
	return err
}

