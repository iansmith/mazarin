// disk is a userspace shepherd that owns the block device and loads a
// filesystem .maz module to serve IPC requests.
package main

import (
	"fmt"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/hid"
	"os"
	"runtime"
	"time"
	"unsafe"
)

// forceBlockDevItab ensures the linker includes the blockdev.BlockDevice
// interface type descriptor, itab, and method wrappers for (*diskBlockDev, blockdev.BlockDevice).
// Without this, fs.maz's type assertion shepherd.(blockdev.BlockDevice) fails
// because the host binary doesn't include the interface type in its typelinks.
// The methods must actually be called to prevent the linker from marking Ifn as -1 (unreachable).
//
//go:noinline
func forceBlockDevItab(v interface{}) {
	bd, ok := v.(blockdev.BlockDevice)
	if !ok {
		return
	}
	// Call each method to force the linker to keep the interface method wrappers
	_ = bd.Name()
	_ = bd.Close()
	_ = bd.BlockSize()
	_ = bd.NumBlocks()
	buf := make([]byte, 512)
	_ = bd.ReadBlock(0, buf)
	_ = bd.WriteBlock(0, buf)
}

func main() {
	fmt.Println("[disk] starting disk shepherd")

	// 1. Query devices to find the block device virtual IRQ
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[disk] QueryInputDevices failed: %v\n", err)
		os.Exit(1)
	}

	blockSlot := -1
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeBlock {
			err := sys.RegisterSoftIRQ(dev.IRQNum, 0)
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

	// 2. Load filesystem module (.maz on ARM64/AMD64, .mzr on RISC-V)
	fsPath := sys.LoadMazByName("/fs")
	fmt.Printf("[disk] loading filesystem module %s...\n", fsPath)
	blkDev := &diskBlockDev{}
	// Force linker to include blockdev.BlockDevice itab for cross-module type assertions
	forceBlockDevItab(blkDev)
	mazMain, shepherdInitAddr, mazErr := mazhost.LoadMazBootstrap(fsPath, blkDev)
	if mazErr != nil {
		fmt.Printf("[disk] LoadMazBootstrap failed: %v\n", mazErr)
		os.Exit(1)
	}

	// 3. Experiment: try calling MazarinShepherd in a goroutine
	if shepherdInitAddr != 0 {
		fmt.Printf("[disk] MazarinShepherd at 0x%X — testing goroutine call\n", shepherdInitAddr)

		type funcval struct{ fn uintptr }
		fv := &funcval{fn: shepherdInitAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))

		done := make(chan error, 1)
		go func() {
			fmt.Println("[disk] goroutine: calling shepherdInit...")
			initErr := shepherdInit(blkDev)
			fmt.Printf("[disk] goroutine: shepherdInit returned: %v\n", initErr)
			done <- initErr
		}()

		// Wait with timeout
		select {
		case initErr := <-done:
			fmt.Printf("[disk] MazarinShepherd result: %v\n", initErr)
		case <-time.After(3 * time.Second):
			fmt.Println("[disk] MazarinShepherd TIMED OUT (3s)")
		}
	}

	// 4. Run MazarinMain as goroutine.
	// Use runWithLargeStack to keep a 256KB frame alive for the ENTIRE
	// duration of .maz execution, preventing GC from shrinking the
	// goroutine stack. .maz modules have their own broken copy of
	// runtime.morestack/newstack (uninitialized globals) so we must
	// ensure the stack never needs to grow while .maz code runs.
	fmt.Println("[disk] starting fs.maz goroutine")
	go func() {
		runWithLargeStack(mazMain)
	}()

	select {}
}

// runWithLargeStack allocates a 256KB stack frame, calls fn, then
// touches the buffer after fn returns to prevent GC from shrinking
// the goroutine stack while fn is running. This is critical because
// GC's shrinkstack halves any goroutine stack where <1/4 is used.
// If preGrowStack were a separate call before fn, GC could shrink
// the stack between the two calls (or during fn's execution), causing
// .maz code to hit its broken morestack and hang.
//
//go:noinline
func runWithLargeStack(fn func()) {
	var buf [262144]byte
	buf[0] = 1
	buf[len(buf)-1] = 1
	if buf[131072] != 0 {
		panic("unreachable")
	}
	fn()
	// Keep buf alive across the fn() call so GC's shrinkstack sees the
	// goroutine as using >1/4 of its 256KB stack and doesn't shrink it.
	runtime.KeepAlive(&buf)
}

// diskBlockDev implements blockdev.BlockDevice using SysBlockRead.
type diskBlockDev struct{}

func (d *diskBlockDev) Name() string         { return "virtio-blk-disk" }
func (d *diskBlockDev) Close() error         { return nil }
func (d *diskBlockDev) BlockSize() uint64    { return 512 }
func (d *diskBlockDev) NumBlocks() uint64    { return 0 }
func (d *diskBlockDev) WriteBlock(lba uint64, buf []byte) error {
	return fmt.Errorf("write not supported")
}

func (d *diskBlockDev) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return fmt.Errorf("buffer too small: %d < 512", len(buf))
	}
	return sys.BlockRead(lba, 1, buf)
}
