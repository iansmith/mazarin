// fat32 is a .maz module that provides FAT32 filesystem services via
// delegated syscalls. It is loaded by the disk priest into its address space,
// inheriting block device ownership. It mounts the FAT32 filesystem using
// the BlockDevice injected via MazarinPriest and serves LoadFile requests
// via the delegate mechanism. It also reads /kmazarin.toml and launches
// [[priest]] entries via RunPriest.
package main

import (
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/constants"
	"mazzy/shared/fs/fat32"
	"mazzy/shared/sysid"
	"mazzy/shared/toml"
	"syscall"
	"unsafe"
)

// injectedBlockDev holds the BlockDevice passed in by the disk priest
// via MazarinPriest. Set before MazarinMain is called.
var injectedBlockDev blockdev.BlockDevice

// MazarinPriest is called by the host priest (via mazhost) after loading
// this .maz. It receives the priest's interface implementation for use
// by this module. For fs.maz, this is a blockdev.BlockDevice.
//
//go:noinline
func MazarinPriest(priest interface{}) error {
	debugPuts("[fs] MazarinPriest: entered\n")
	if priest == nil {
		debugPuts("[fs] MazarinPriest: nil priest\n")
		return nil
	}
	blk, ok := priest.(blockdev.BlockDevice)
	if !ok {
		debugPuts("[fs] MazarinPriest: type assertion failed\n")
		return nil
	}
	injectedBlockDev = blk
	debugPuts("[fs] MazarinPriest: received BlockDevice\n")
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

// MazPriestEntry holds a reference to MazarinPriest to prevent DCE.
var MazPriestEntry func(interface{}) error = MazarinPriest

// init forces the linker to keep MazarinMain and MazarinPriest alive.
func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazPriestEntry == nil {
		panic("unreachable")
	}
}

// workItem represents a unit of work for the file worker goroutine.
// Either loadReq is set (LoadFile delegate request) or launchName/launchPath
// are set (launch a priest from TOML config).
type workItem struct {
	loadReq    *sys.SyscallRequest
	launchName string
	launchPath string
}

// workCh serializes all disk I/O through a single goroutine.
var workCh chan workItem

// MazarinMain is the entry point called by the disk priest when this .maz
// is loaded. It mounts FAT32 using the injected BlockDevice, reads boot
// config, launches application priests synchronously, then registers as
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

	// Launch application priests synchronously before starting the delegate
	// infrastructure. The delegateRecvLoop goroutine blocks in the kernel
	// without releasing the P (entersyscall is a no-op in .maz), so any work
	// queued for a fileWorker goroutine would never be processed.
	if cfg != nil {
		for i := 0; i < cfg.PriestCount; i++ {
			p := &cfg.Priests[i]
			name := constants.NullTermString(p.Name[:])
			path := constants.NullTermString(p.Path[:])
			handleLaunchPriest(fs, name, path)
		}
	}

	// Start the serialized file worker goroutine for LoadFile requests
	workCh = make(chan workItem, 64)
	go fileWorker(fs)

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

	// Forward delegate requests to the worker (never returns)
	debugPuts("[fs] entering delegate receive loop\n")
	for req := range delegateCh {
		r := req // copy for channel send
		workCh <- workItem{loadReq: &r}
	}
}

func main() {
	MazarinMain()
}

// fileWorker is the single goroutine that performs all disk I/O.
// It processes work items from workCh one at a time, ensuring serialized
// access to the FAT32 filesystem.
func fileWorker(fs *fat32.FileSystem) {
	for item := range workCh {
		if item.loadReq != nil {
			handleLoadFile(fs, item.loadReq)
		} else {
			handleLaunchPriest(fs, item.launchName, item.launchPath)
		}
	}
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
		debugPuts("[fs] LoadFile: file not found\n")
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

// handleLaunchPriest reads an ELF from FAT32 and launches it as a new priest
// via the RunPriest syscall.
func handleLaunchPriest(fs *fat32.FileSystem, name, path string) {
	debugPuts("[fs] launching priest ")
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

	rpErr := sys.RunPriest(name, va, numPages, bytesRead)
	if rpErr != nil {
		debugPuts("[fs] RunPriest failed for ")
		debugPuts(name)
		debugPuts(": ")
		debugPuts(rpErr.Error())
		debugPuts("\n")
		return
	}

	debugPuts("[fs] priest ")
	debugPuts(name)
	debugPuts(" launched\n")
}

// readFileIntoPages opens a file from FAT32, allocates pages via mmap,
// reads the file data into them, and returns the VA, page count, and bytes read.
func readFileIntoPages(fs *fat32.FileSystem, path string) (va uintptr, numPages int, bytesRead int, err error) {
	file, ferr := fs.Open(path)
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
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), 0)
	if errno != 0 || int64(va) < 0 {
		return 0, 0, 0, errno
	}

	buf := unsafe.Slice((*byte)(unsafe.Pointer(va)), totalSize)
	n, _ := file.Read(buf[:fileSize])

	return va, numPages, n, nil
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
	debugPutDec(cfg.PriestCount)
	debugPuts(" priests\n")
	return cfg
}

// userspaceBlockDev implements blockdev.BlockDevice using SysBlockRead.
// Used as fallback when MazarinPriest was not called.
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
