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

// writeRawString writes a string to stderr using RawSyscall (no entersyscall/exitsyscall).
// This avoids P-reacquisition hangs that can occur with fmt.Printf → Syscall.
func writeRawString(s string) {
	for i := 0; i < len(s); i++ {
		sys.RawWrite(2, s[i])
	}
}

func main() {
	fmt.Println("[fs] starting filesystem shepherd")

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

	// 2. Allocate shared completion ring page for block I/O
	ringPage, ringErr := mem.AllocPages(1, mem.PageShared)
	if ringErr != nil {
		fmt.Printf("[fs] AllocPages for completion ring failed: %v\n", ringErr)
		os.Exit(1)
	}
	completionRing := (*hid.CompletionRing)(ringPage)
	if err := sys.RegisterCompletionRing(uintptr(unsafe.Pointer(completionRing)), 0); err != nil {
		fmt.Printf("[fs] RegisterCompletionRing failed: %v\n", err)
		os.Exit(1)
	}

	// 3. Allocate DMA scratch buffer for the fs shepherd's own block reads
	//    (ext2 metadata, boot config, etc.). Self-targeted BlockSubmit.
	scratch, scratchErr := mem.AllocContiguous(scratchPages * 4096)
	if scratchErr != nil {
		fmt.Printf("[fs] AllocContiguous for scratch failed: %v\n", scratchErr)
		os.Exit(1)
	}
	// 4. Mount ext2 on VirtIO block device (read-only)
	blkDev := newAsyncBlockDev(scratch.Addr, completionRing)
	fsys, mountErr := ext2.Mount(blkDev)
	if mountErr != nil {
		fmt.Printf("[fs] ext2 mount failed: %v\n", mountErr)
		os.Exit(1)
	}
	fmt.Println("[fs] ext2 root mounted successfully (read-only)")

	// 3b. Create 128MB ext2 ramdisk at /tmp.
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
	go bootSequence(fsys)

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
	tOpen := time.Since(t0)

	fileSize := int(file.Size())
	numPages = (fileSize + 4095) / 4096
	if numPages == 0 {
		numPages = 1
	}

	totalSize := uintptr(numPages) * 4096

	tAllocStart := time.Now()
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
	tAlloc := time.Since(tAllocStart)

	dst := unsafe.Slice((*byte)(unsafe.Pointer(va)), totalSize)

	tReadStart := time.Now()
	n, rerr := file.ReadInto(dst[:fileSize])
	tRead := time.Since(tReadStart)

	writeRawString(fmt.Sprintf("[fs] PERF %s: size=%d open=%dms alloc=%dms read=%dms total=%dms\n",
		path, fileSize, tOpen.Milliseconds(), tAlloc.Milliseconds(),
		tRead.Milliseconds(), time.Since(t0).Milliseconds()))

	if rerr != nil {
		return 0, 0, 0, rerr
	}

	return va, numPages, n, nil
}

// handleReadFilePages returns ENOSYS — ext2 does not support direct DMA
// sector-run reads. Callers should use LoadFile instead.
func handleReadFilePages(req *sys.SyscallRequest) {
	req.Reply(-38) // ENOSYS
}

// drainDelegateRequests processes any pending delegate requests between
// shepherd launches. If a request is found, the caller did not wait for
// fs readiness — log a warning but serve the request anyway.
func drainDelegateRequests(delegateCh <-chan sys.SyscallRequest, mt *mountTable) {
	for {
		select {
		case req := <-delegateCh:
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
	return &cfg
}

// launchShepherd reads an ELF from ext2 and launches it as a new shepherd.
func launchShepherd(fsys *ext2.FileSystem, name, path string) {
	writeRawString("[fs] reading " + path + "...\n")
	va, numPages, bytesRead, err := readFileIntoPages(fsys, path, false)
	if err != nil {
		writeRawString("[fs] failed to read " + path + "\n")
		return
	}
	writeRawString("[fs] read " + path + ", calling RunShepherd\n")
	rpErr := sys.RunShepherd(name, va, numPages, bytesRead)
	// Free temporary pages (RunShepherd copies them to the new shepherd).
	syscall.RawSyscall6(syscall.SYS_MUNMAP, va, uintptr(numPages)*4096, 0, 0, 0, 0)
	if rpErr != nil {
		writeRawString("[fs] RunShepherd FAILED for " + name + "\n")
		return
	}
	writeRawString("[fs] " + name + " launched\n")
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
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		writeRawString("[fs] FATAL: rachel not ready\n")
		return
	}
	// 2. Launch linux and wait — provides syscall delegation (Openat, Read, etc.).
	// Linux depends on both fs (IPC) and rachel (fonts/WM).
	launchShepherd(fsys, "linux", "/linux.elf")
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		writeRawString("[fs] FATAL: linux not ready\n")
		return
	}
	// 3. Read startup config and launch remaining application shepherds.
	writeRawString("[fs] reading startup.toml...\n")
	cfg := readStartupConfig(fsys)
	if cfg != nil {
		for _, s := range cfg.Shepherds {
			writeRawString("[fs] launching " + s.Name + " from " + s.Path + "\n")
			launchShepherd(fsys, s.Name, s.Path)
		}
	} else {
		writeRawString("[fs] no startup.toml\n")
	}
	writeRawString("[fs] boot sequence complete\n")
}

// asyncBlockDev implements blockdev.BlockDevice backed by a DMA worker
// goroutine. All DMA operations (BlockSubmit + completion ring poll) run on a
// single dedicated goroutine, which is the sole owner of the completion ring
// and scratch buffer. Other goroutines send requests via channel and block on
// a per-request response channel. This makes the single-waiter-per-slot
// invariant structural — no mutex needed, no stolen completion events.
type asyncBlockDev struct {
	reqCh     chan dmaRequest
	scratchVA uintptr              // base of MAZARIN_CONTIGUOUS scratch pages
	ring      *hid.CompletionRing  // shared completion ring for block I/O
}

// dmaRequest is sent to the DMA worker goroutine.
type dmaRequest struct {
	kind    dmaRequestKind
	replyCh chan dmaReply

	// For single-block reads (dmaReadBlock):
	lba uint64
	buf []byte

	// For batched reads (dmaReadBatch):
	blocks []uint32 // ext2 block numbers
	dst    []byte   // destination buffer (len >= len(blocks)*asyncBlockSize)
}

type dmaRequestKind int

const (
	dmaReadBlock dmaRequestKind = iota // single 4KB block read
	dmaReadBatch                       // batched multi-block read
)

type dmaReply struct {
	err error
}

const (
	asyncBlockSize  = 4096
	sectorsPerBlock = asyncBlockSize / 512
)

// newAsyncBlockDev creates the block device and starts the DMA worker.
func newAsyncBlockDev(scratchVA uintptr, ring *hid.CompletionRing) *asyncBlockDev {
	d := &asyncBlockDev{
		reqCh:     make(chan dmaRequest, 4),
		scratchVA: scratchVA,
		ring:      ring,
	}
	go d.dmaWorker()
	return d
}

// dmaWorker is the sole goroutine that touches the SoftIRQ slot and scratch
// buffer. It processes requests sequentially from reqCh.
func (d *asyncBlockDev) dmaWorker() {
	for req := range d.reqCh {
		var err error
		switch req.kind {
		case dmaReadBlock:
			err = d.doReadBlock(req.lba, req.buf)
		case dmaReadBatch:
			err = d.doReadBatch(req.blocks, req.dst)
		}
		req.replyCh <- dmaReply{err: err}
	}
}

var singleReadCount int
var singleReadTotal time.Duration

// doReadBlock reads a single 4KB block via DMA. Only called from dmaWorker.
func (d *asyncBlockDev) doReadBlock(lba uint64, buf []byte) error {
	t0 := time.Now()
	defer func() {
		singleReadCount++
		singleReadTotal += time.Since(t0)
		if singleReadCount%100 == 0 {
			writeRawString(fmt.Sprintf("[dma] single: %d reads, avg=%dms total=%dms\n",
				singleReadCount, singleReadTotal.Milliseconds()/int64(singleReadCount),
				singleReadTotal.Milliseconds()))
		}
	}()
	sectorLBA := lba * sectorsPerBlock
	dmaBuf := unsafe.Slice((*byte)(unsafe.Pointer(d.scratchVA)), asyncBlockSize)
	_, serr := mem.BlockSubmit(0, sectorLBA, sectorsPerBlock, dmaBuf, 0)
	if serr != nil {
		return serr
	}
	// Spin-poll shared completion ring. The IRQ top-half writes completions
	// directly to this page, so data arrives within microseconds.
	// No mailbox/SVC needed — the IRQ fires while we're in userspace.
	var events [1]hid.HIDEvent
	for {
		n := sys.PollCompletionRing(d.ring, events[:], 1)
		if n > 0 {
			if events[0].Code != 0 {
				return syscall.EIO
			}
			break
		}
	}
	copy(buf[:asyncBlockSize], dmaBuf)
	return nil
}

// doReadBatch reads multiple 4KB blocks in batches of scratchPages.
// Only called from dmaWorker.
func (d *asyncBlockDev) doReadBatch(blocks []uint32, dst []byte) error {
	total := uint32(len(blocks))
	batchCount := 0
	var totalSubmit, totalWait, totalCopy time.Duration
	t0 := time.Now()

	for blockIdx := uint32(0); blockIdx < total; {
		batch := total - blockIdx
		if batch > scratchPages {
			batch = scratchPages
		}
		batchBlocks := blocks[blockIdx : blockIdx+batch]

		tSubmit := time.Now()
		submitted := 0
		for i, bn := range batchBlocks {
			if bn == 0 {
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
			_, serr := mem.BlockSubmit(0, sectorLBA, sectorsPerBlock, dmaBuf, 0)
			if serr != nil {
				return serr
			}
			submitted++
		}
		totalSubmit += time.Since(tSubmit)

		tWait := time.Now()
		if submitted > 0 {
			remaining := submitted
			var events [scratchPages]hid.HIDEvent
			for remaining > 0 {
				n := sys.PollCompletionRing(d.ring, events[:], remaining)
				for i := 0; i < n; i++ {
					if events[i].Code != 0 {
						return syscall.EIO
					}
					remaining--
				}
			}
		}
		totalWait += time.Since(tWait)

		tCopy := time.Now()
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
		totalCopy += time.Since(tCopy)

		blockIdx += batch
		batchCount++
	}

	writeRawString(fmt.Sprintf("[dma] batch: %d blocks, %d batches, submit=%dms wait=%dms copy=%dms total=%dms\n",
		total, batchCount, totalSubmit.Milliseconds(), totalWait.Milliseconds(),
		totalCopy.Milliseconds(), time.Since(t0).Milliseconds()))
	return nil
}

func (d *asyncBlockDev) Name() string      { return "virtio-blk-async" }
func (d *asyncBlockDev) Close() error      { return nil }
func (d *asyncBlockDev) BlockSize() uint64 { return asyncBlockSize }
func (d *asyncBlockDev) NumBlocks() uint64 { return 0 }
func (d *asyncBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

// ReadBlock sends a single-block read request to the DMA worker and waits
// for the result. Safe to call from any goroutine.
func (d *asyncBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < asyncBlockSize {
		return fmt.Errorf("buffer too small")
	}
	replyCh := make(chan dmaReply, 1)
	d.reqCh <- dmaRequest{
		kind:    dmaReadBlock,
		replyCh: replyCh,
		lba:     lba,
		buf:     buf,
	}
	reply := <-replyCh
	return reply.err
}

// ReadBlocks implements blockdev.BatchBlockDevice. Sends a batched multi-block
// read request to the DMA worker. lbas are in device block units (4KB each).
// Safe to call from any goroutine.
func (d *asyncBlockDev) ReadBlocks(lbas []uint64, dst []byte) error {
	// Convert uint64 LBAs to uint32 block numbers for doReadBatch.
	blocks := make([]uint32, len(lbas))
	for i, lba := range lbas {
		blocks[i] = uint32(lba)
	}
	replyCh := make(chan dmaReply, 1)
	d.reqCh <- dmaRequest{
		kind:    dmaReadBatch,
		replyCh: replyCh,
		blocks:  blocks,
		dst:     dst,
	}
	reply := <-replyCh
	return reply.err
}
