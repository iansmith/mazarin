// disk is a userspace priest that owns the block device and loads a
// filesystem .maz module to serve IPC requests.
package main

import (
	"fmt"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/shared/blockdev"
	"mazzy/shared/hid"
	"os"
	"time"
	"unsafe"
)

// forceBlockDevItab ensures the linker includes the blockdev.BlockDevice
// interface type descriptor, itab, and method wrappers for (*diskBlockDev, blockdev.BlockDevice).
// Without this, fs.maz's type assertion priest.(blockdev.BlockDevice) fails
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

	// 2. Load filesystem .maz module
	fmt.Println("[disk] loading filesystem maz...")
	blkDev := &diskBlockDev{}
	// Force linker to include blockdev.BlockDevice itab for cross-module type assertions
	forceBlockDevItab(blkDev)
	mazMain, priestInitAddr, mazErr := mazhost.LoadMazBootstrap("/fs.maz", blkDev)
	if mazErr != nil {
		fmt.Printf("[disk] LoadMazBootstrap failed: %v\n", mazErr)
		os.Exit(1)
	}

	// 3. Experiment: try calling MazarinPriest in a goroutine
	if priestInitAddr != 0 {
		fmt.Printf("[disk] MazarinPriest at 0x%X — testing goroutine call\n", priestInitAddr)

		type funcval struct{ fn uintptr }
		fv := &funcval{fn: priestInitAddr}
		priestInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))

		done := make(chan error, 1)
		go func() {
			fmt.Println("[disk] goroutine: calling priestInit...")
			initErr := priestInit(blkDev)
			fmt.Printf("[disk] goroutine: priestInit returned: %v\n", initErr)
			done <- initErr
		}()

		// Wait with timeout
		select {
		case initErr := <-done:
			fmt.Printf("[disk] MazarinPriest result: %v\n", initErr)
		case <-time.After(3 * time.Second):
			fmt.Println("[disk] MazarinPriest TIMED OUT (3s)")
		}
	}

	// 4. Run MazarinMain as goroutine.
	// Pre-grow the goroutine stack BEFORE entering .maz code, because
	// .maz modules have their own copy of runtime.morestack/newstack
	// that cannot function correctly (they reference uninitialized runtime
	// globals from the .maz binary). By growing the stack here using the
	// host's working morestack, we ensure .maz code never needs to grow.
	fmt.Println("[disk] starting fs.maz goroutine")
	go func() {
		preGrowStack()
		mazMain()
	}()

	select {}
}

// preGrowStack forces the goroutine stack to grow to at least 64KB+
// by allocating a large local buffer. This must be a separate function
// (not inlined) so the stack growth check fires in the host's runtime
// code, not in the .maz module's broken copy of morestack.
//
//go:noinline
func preGrowStack() {
	var buf [65536]byte
	buf[0] = 1
	buf[len(buf)-1] = 1
	// Prevent the compiler from optimizing away the stack allocation.
	// Use a volatile-style read to ensure the buffer is actually allocated.
	if buf[32768] != 0 {
		panic("unreachable")
	}
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
