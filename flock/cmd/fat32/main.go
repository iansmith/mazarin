// fat32 is a .maz module that provides FAT32 filesystem services via
// delegated syscalls. It is loaded by the disk shepherd into its address space,
// inheriting block device ownership. It mounts the FAT32 filesystem using
// the BlockDevice injected via MazarinShepherd and serves LoadFile requests
// via the delegate mechanism. It also reads /kmazarin.toml and launches
// [[shepherd]] entries via RunShepherd.
package main

import (
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/constants"
	"mazzy/shared/fs/fat32"
	"mazzy/shared/hid"
	"mazzy/shared/sysid"
	"mazzy/shared/toml"
	"syscall"
	"unsafe"
)

// injectedBlockDev holds the BlockDevice passed in by the disk shepherd
// via MazarinShepherd. Set before MazarinMain is called.
var injectedBlockDev blockdev.BlockDevice

// MazarinShepherd is called by the host shepherd (via mazhost) after loading
// this .maz. It receives the shepherd's interface implementation for use
// by this module. For fs.maz, this is a blockdev.BlockDevice.
//
//go:noinline
func MazarinShepherd(shepherd interface{}) error {
	debugPuts("[fs] MazarinShepherd: entered\n")
	if shepherd == nil {
		debugPuts("[fs] MazarinShepherd: nil shepherd\n")
		return nil
	}
	blk, ok := shepherd.(blockdev.BlockDevice)
	if !ok {
		debugPuts("[fs] MazarinShepherd: type assertion failed\n")
		return nil
	}
	injectedBlockDev = blk
	debugPuts("[fs] MazarinShepherd: received BlockDevice\n")
	return nil
}

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

// MazShepherdEntry holds a reference to MazarinShepherd to prevent DCE.
var MazShepherdEntry func(interface{}) error = MazarinShepherd

// init forces the linker to keep MazarinMain and MazarinShepherd alive.
func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazShepherdEntry == nil {
		panic("unreachable")
	}
}

// MazarinMain is the entry point called by the disk shepherd when this .maz
// is loaded. It mounts FAT32 using the injected BlockDevice, reads boot
// config, launches application shepherds synchronously, then registers as
// the LoadFile delegate handler and serves requests. Never returns.
//
//go:noinline
func MazarinMain() {
	debugPuts("[fs] FAT32 filesystem maz starting\n")

	// Mount FAT32 filesystem using the injected BlockDevice, or fall back to SysBlockRead
	var blkDev blockdev.BlockDevice
	if injectedBlockDev != nil {
		debugPuts("[fs] using injected BlockDevice\n")
		blkDev = injectedBlockDev
	} else {
		debugPuts("[fs] no injected BlockDevice, using SysBlockRead fallback\n")
		blkDev = &userspaceBlockDev{}
	}
	fs, fsErr := fat32.Mount(blkDev)
	if fsErr != nil {
		debugPuts("[fs] FAT32 mount failed\n")
		return
	}
	debugPuts("[fs] FAT32 mounted successfully\n")

	// Read and parse boot config (synchronous, before any concurrency)
	cfg := readBootConfig(fs)

	// Launch application shepherds synchronously before starting the delegate
	// infrastructure. The delegateRecvLoop goroutine blocks in the kernel
	// without releasing the P (entersyscall is a no-op in .maz), so any work
	// queued for a fileWorker goroutine would never be processed.
	if cfg != nil {
		for i := 0; i < cfg.ShepherdCount; i++ {
			p := &cfg.Shepherds[i]
			name := constants.NullTermString(p.Name[:])
			path := constants.NullTermString(p.Path[:])
			handleLaunchShepherd(fs, name, path)
		}
	}

	// Register as LoadFile delegate handler
	delegateCh, err := sys.HandleSyscalls(sysid.LoadFile)
	if err != nil {
		debugPuts("[fs] HandleSyscalls(LoadFile) failed\n")
		return
	}
	debugPuts("[fs] registered as LoadFile delegate\n")

	// Signal readiness — kernel will now forward LoadFile requests to us
	sys.SetReady(true)
	debugPuts("[fs] SetReady(true)\n")

	// Handle LoadFile requests inline on this goroutine's pre-grown stack.
	// DO NOT use "go fileWorker(fs)" — goroutines created inside .maz code
	// get a default 8KB stack, and when they need to grow, .maz's broken
	// runtime.morestack hangs the goroutine forever. By processing requests
	// inline, we use the host's runWithLargeStack frame (256KB), which is
	// kept alive for the duration of MazarinMain.
	// Run async I/O verification test after delegate is ready.
	// Requests arriving during the test block in the kernel and will
	// be served as soon as we enter the delegate loop below.
	verifyAsyncIO(fs)

	debugPuts("[fs] entering delegate receive loop\n")
	for req := range delegateCh {
		handleLoadFile(fs, &req)
	}
}

func main() {
	MazarinMain()
}

// handleLoadFile processes a delegated LoadFile request:
// reads the file from FAT32 into mmap'd pages, transfers them to the
// caller via TransferAndUnmap, and replies with the result.
func handleLoadFile(fs *fat32.FileSystem, req *sys.SyscallRequest) {
	path := req.PathString()
	debugPuts("[fs] LoadFile \"")
	debugPuts(path)
	debugPuts("\"\n")

	va, numPages, bytesRead, err := readFileIntoPages(fs, path)
	if err != nil {
		debugPuts("[fs] LoadFile: error=")
		debugPuts(err.Error())
		debugPuts("\n")
		req.Reply(-2) // ENOENT
		return
	}

	targetVA, terr := sys.TransferAndUnmap(int(req.CallerPID), va, numPages)
	if terr != nil {
		debugPuts("[fs] LoadFile: TransferAndUnmap failed\n")
		req.Reply(-5) // EIO
		return
	}

	debugPuts("[fs] LoadFile: transferred ")
	debugPutDec(numPages)
	debugPuts(" pages\n")

	req.LoadFileReply(0, uint64(targetVA), uint64(numPages), uint64(bytesRead))
}

// handleLaunchShepherd reads an ELF from FAT32 and launches it as a new shepherd
// via the RunShepherd syscall.
func handleLaunchShepherd(fs *fat32.FileSystem, name, path string) {
	debugPuts("[fs] launching shepherd ")
	debugPuts(name)
	debugPuts(" from ")
	debugPuts(path)
	debugPuts("\n")

	va, numPages, bytesRead, err := readFileIntoPages(fs, path)
	if err != nil {
		debugPuts("[fs] failed to read ")
		debugPuts(path)
		debugPuts("\n")
		return
	}

	rpErr := sys.RunShepherd(name, va, numPages, bytesRead)
	if rpErr != nil {
		debugPuts("[fs] RunShepherd failed for ")
		debugPuts(name)
		debugPuts(": ")
		debugPuts(rpErr.Error())
		debugPuts("\n")
		return
	}

	debugPuts("[fs] shepherd ")
	debugPuts(name)
	debugPuts(" launched\n")
}

// readFileIntoPages opens a file from FAT32, allocates pages via mmap,
// reads the file data into them, and returns the VA, page count, and bytes read.
func readFileIntoPages(fs *fat32.FileSystem, path string) (va uintptr, numPages int, bytesRead int, err error) {
	debugPuts("[fs] readFile: opening ")
	debugPuts(path)
	debugPuts("\n")
	file, ferr := fs.Open(path)
	if ferr != nil {
		debugPuts("[fs] readFile: open failed\n")
		return 0, 0, 0, ferr
	}
	defer file.Close()

	fileSize := int(file.Size())
	debugPuts("[fs] readFile: size=")
	debugPutDec(fileSize)
	debugPuts("\n")
	numPages = (fileSize + 4095) / 4096
	if numPages == 0 {
		numPages = 1
	}

	totalSize := uintptr(numPages) * 4096
	debugPuts("[fs] readFile: mmap ")
	debugPutDec(numPages)
	debugPuts(" pages\n")
	va, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, totalSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), 0)
	if errno != 0 || int64(va) < 0 {
		debugPuts("[fs] readFile: mmap failed\n")
		return 0, 0, 0, errno
	}

	debugPuts("[fs] readFile: reading...\n")
	buf := unsafe.Slice((*byte)(unsafe.Pointer(va)), totalSize)
	n, _ := file.Read(buf[:fileSize])

	debugPuts("[fs] readFile: read ")
	debugPutDec(n)
	debugPuts(" bytes\n")
	return va, numPages, n, nil
}

// verifyAsyncIO performs a self-test of the async block I/O path.
// Registers a DMA pool, reads the root directory to find files, submits
// concurrent reads via BlockSubmit, waits for completions via soft IRQ,
// and verifies data against expected magic bytes.
//
//go:noinline
func verifyAsyncIO(fs *fat32.FileSystem) {
	debugPuts("[fs:verify] async I/O verification starting\n")

	// Allocate 8 pages for DMA pool (32KB)
	const numPoolPages = 8
	poolSize := uintptr(numPoolPages) * 4096
	poolVA, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP, 0, poolSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), 0)
	if errno != 0 || int64(poolVA) < 0 {
		debugPuts("[fs:verify] SKIP: mmap failed\n")
		return
	}

	// Register DMA pool
	if err := sys.RegisterDMAPool(poolVA, numPoolPages); err != nil {
		debugPuts("[fs:verify] SKIP: RegisterDMAPool failed\n")
		return
	}
	defer sys.UnregisterDMAPool()

	// Read root directory to find ELF files for verification
	entries, err := fs.ReadDir(fs.RootCluster())
	if err != nil {
		debugPuts("[fs:verify] SKIP: ReadDir failed\n")
		return
	}

	// Collect test targets: sector 0 (BPB) + up to 7 file start sectors
	type testTarget struct {
		lba       uint64
		expectELF bool // expect 0x7F ELF magic
	}
	var targets [8]testTarget
	targets[0] = testTarget{lba: 0, expectELF: false} // BPB sector
	numTargets := 1

	for _, e := range entries {
		if numTargets >= 8 {
			break
		}
		if e.IsDir || e.Size == 0 || e.Cluster < 2 {
			continue
		}
		lba := fs.ClusterToSector(e.Cluster)
		targets[numTargets] = testTarget{lba: lba, expectELF: true}
		numTargets++
	}

	debugPuts("[fs:verify] submitting ")
	debugPutDec(numTargets)
	debugPuts(" async reads\n")

	// Submit all reads
	var tags [8]uint16
	for i := 0; i < numTargets; i++ {
		offset := uintptr(i) * 4096
		buf := unsafe.Slice((*byte)(unsafe.Pointer(poolVA+offset)), 512)
		tag, serr := sys.BlockSubmit(0, targets[i].lba, 1, buf)
		if serr != nil {
			debugPuts("[fs:verify] BlockSubmit failed at index ")
			debugPutDec(i)
			debugPuts("\n")
			return
		}
		tags[i] = tag
	}

	// Poll for completions via TrySoftIRQ (non-blocking, avoids entersyscall issue in .maz)
	var softBuf hid.SoftIRQReturn
	completed := 0
	for attempts := 0; attempts < 2000000 && completed < numTargets; attempts++ {
		n, _ := sys.TrySoftIRQ(0, &softBuf)
		if n > 0 {
			completed += int(softBuf.Length)
		}
	}

	if completed < numTargets {
		debugPuts("[fs:verify] FAIL: timeout waiting for completions (got ")
		debugPutDec(completed)
		debugPuts("/")
		debugPutDec(numTargets)
		debugPuts(")\n")
		return
	}

	// Verify data
	pass := true

	// Check BPB magic at offset 510-511
	bpbBuf := unsafe.Slice((*byte)(unsafe.Pointer(poolVA)), 512)
	if bpbBuf[510] != 0x55 || bpbBuf[511] != 0xAA {
		debugPuts("[fs:verify] FAIL: BPB magic mismatch\n")
		pass = false
	}

	// Check ELF magic for file reads
	for i := 1; i < numTargets; i++ {
		if !targets[i].expectELF {
			continue
		}
		offset := uintptr(i) * 4096
		fileBuf := unsafe.Slice((*byte)(unsafe.Pointer(poolVA+offset)), 512)
		if fileBuf[0] != 0x7F || fileBuf[1] != 'E' || fileBuf[2] != 'L' || fileBuf[3] != 'F' {
			debugPuts("[fs:verify] FAIL: file at index ")
			debugPutDec(i)
			debugPuts(" LBA ")
			debugPutDec(int(targets[i].lba))
			debugPuts(" not ELF (got 0x")
			debugPutHex(fileBuf[0])
			debugPutHex(fileBuf[1])
			debugPutHex(fileBuf[2])
			debugPutHex(fileBuf[3])
			debugPuts(")\n")
			pass = false
		}
	}

	_ = tags // tags used for debugging if needed

	if pass {
		debugPuts("[fs:verify] PASSED: ")
		debugPutDec(numTargets)
		debugPuts(" async reads verified\n")
	} else {
		debugPuts("[fs:verify] FAILED\n")
	}
}

// debugPutHex writes a single byte as two hex digits.
func debugPutHex(b byte) {
	const hexDigits = "0123456789abcdef"
	sys.DebugPutChar(hexDigits[b>>4])
	sys.DebugPutChar(hexDigits[b&0x0F])
}

// readBootConfig reads and parses /kmazarin.toml from the FAT32 filesystem.
// Returns nil if the file doesn't exist or can't be parsed.
func readBootConfig(fs *fat32.FileSystem) *constants.BootConfig {
	file, err := fs.Open("/kmazarin.toml")
	if err != nil {
		debugPuts("[fs] Open(/kmazarin.toml) error: ")
		debugPuts(err.Error())
		debugPuts("\n")
		return nil
	}
	defer file.Close()

	data, err := file.ReadAll()
	if err != nil {
		debugPuts("[fs] failed to read kmazarin.toml\n")
		return nil
	}

	cfg := toml.Parse(data)
	debugPuts("[fs] boot config: ")
	debugPutDec(cfg.ShepherdCount)
	debugPuts(" shepherds\n")
	return cfg
}

// userspaceBlockDev implements blockdev.BlockDevice using SysBlockRead.
// Used as fallback when MazarinShepherd was not called.
type userspaceBlockDev struct{}

func (d *userspaceBlockDev) Name() string      { return "virtio-blk-user" }
func (d *userspaceBlockDev) Close() error      { return nil }
func (d *userspaceBlockDev) BlockSize() uint64 { return 512 }
func (d *userspaceBlockDev) NumBlocks() uint64 { return 0 }
func (d *userspaceBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return nil // write not supported
}

func (d *userspaceBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return nil // buffer too small
	}
	return sys.BlockRead(lba, 1, buf)
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
