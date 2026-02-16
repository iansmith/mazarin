package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/shared/fs/fat32"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/ktimer"
	"mazzy/kmazarin/uart"
	_ "os"     // Keep to maintain BSS size
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"unsafe"
)

// SyscallDispatch is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls syscallDispatchInternal. This is the actual implementation.
//
// Called from exception handler - nosplit required because SP may be on a foreign
// stack (e.g., clone child stack) where Go's stack check would fail.
//go:nosplit
//go:noinline
func syscallDispatchInternal(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return ksyscall.DispatchSyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
}

// IRQDispatch is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls irqDispatchInternal. This is the actual implementation.
//
// Called from exception handler - nosplit required because SP may be on a foreign
// stack (e.g., clone child stack) where Go's stack check would fail.
//go:nosplit
//go:noinline
func irqDispatchInternal(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	info := kirq.DispatchIRQ(irqNum, framePtr, elr, spEl0)
	return info.NewELR, info.NewSP, info.NewLR, info.DoPreempt
}

// timerIRQHandlerInternal is called directly from exception handler for timer IRQs
//go:nosplit
//go:noinline
func timerIRQHandlerInternal(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	// RISC-V: Verify process page table integrity on every timer tick
	kmem.VerifyCurrentSATPL3E0()
	info := kirq.TimerIRQHandlerCanPreempt(irqNum, framePtr, elr, spEl0)
	return info.NewELR, info.NewSP, info.NewLR, info.DoPreempt
}

// dumpInstructionPageFaultInternal is the Go wrapper for DumpInstructionPageFaultAsm.
// Called from the instruction page fault handler to walk and dump the PTE chain.
//
//go:nosplit
//go:noinline
func dumpInstructionPageFaultInternal(faultAddr uint64) {
	kmem.DumpInstructionPageFault(uintptr(faultAddr))
}

// HandlePageFaultAsm is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls handlePageFaultInternal. This is the actual implementation.
// Returns 1 if the fault was handled successfully, 0 otherwise.
//
//go:nosplit
func handlePageFaultInternal(faultAddr uint64) uint64 {
	if kmem.HandlePageFault(uintptr(faultAddr)) {
		return 1
	}
	return 0
}

// HandleUserPageFaultAsm is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls handleUserPageFaultInternal. This handles page faults from EL0.
// Returns 1 if the fault was handled successfully, 0 otherwise.
//
//go:nosplit
//go:noinline
func handleUserPageFaultInternal(faultAddr uint64) uint64 {
	if kmem.HandleUserPageFault(uintptr(faultAddr)) {
		return 1
	}
	return 0
}

// init runs before main - called after Go runtime is fully initialized
// Set up exception handlers and enable interrupts
func init() {
	// NOTE: GC will be disabled and flushed in simpleMain() where runtime is fully ready.
	// Disabling here is too early and causes hangs.

	// NOTE: Runtime config is already initialized in runtime_config.go:init()
	// which runs at package load time, before the Go runtime initializes.

	// NOTE: Syscall table is initialized at package level, before runtime init
	// NOTE: We set VBAR_EL1 below to point to kmazarin's exception vectors.
	// This must happen before any interrupts are enabled, but after the runtime
	// initializes (since syscalls during runtime init don't use kmazarin's handlers yet).

	// CRITICAL: Call GetExceptionVectorBase() to force linker to include exception vector table
	// Without this, the linker removes ExceptionVectorTable as dead code!
	vectorAddr := GetExceptionVectorBase()
	_ = vectorAddr // Will be used later in simpleMain()

	// CRITICAL: Reference G0Struct to force linker to include it
	// This buffer is where Cardinal copies the g0 goroutine struct for kmazarin to use.
	// Without this reference, the linker removes G0Struct as dead code!
	g0StructAddr := GetG0StructAddr()
	_ = g0StructAddr // Suppress unused warning

	// Initialize preemption thresholds BEFORE threads, so deadlines use correct values
	kirq.InitPreemptThresholds()

	// Initialize thread table - M0 is thread 0
	// MUST happen before any clone syscalls!
	InitThreads()

	// Initialize soft IRQ subsystem (static allocation, no heap needed)
	InitSoftIRQ()

	// Initialize channel subsystem for kernel-to-priest async messaging
	InitChannels()

	// Initialize critical early devices (UART, GIC, Timer, RNG)
	EarlyInit()

	// Initialize timer, IRQ handlers, and preemption subsystem
	kirq.InitTimer()
	kirq.RegisterHandlers()
	kirq.InitPreemption()

	// Store asyncPreemptWrapper address for IRQ handler to read.
	// This must be done before EnableIRQs() since the timer IRQ handler
	// needs this address to inject async preemption.
	asyncPreemptAddr := getAsyncPreemptWrapperAddr()
	kirq.SetAsyncPreemptWrapperAddr(asyncPreemptAddr)

	// Also set the asyncPreempt address in all existing kernel threads.
	// This allows the exception handler to use per-thread asyncPreempt addresses,
	// enabling a unified approach for kmazarin and priest goroutine preemption.
	SetKmazarinAsyncPreemptAddr(uint64(asyncPreemptAddr))

	// NOTE: OLD UART initialization disabled - now using PL011 device driver
	// kirq.InitUART()

	// Start bottom-half processors for interrupt dispatch
	// The event poller will check IRQ pending flags and call handlers in safe Go context
	StartBottomHalfProcessors()

	// NOTE: IRQs are NOT enabled yet - GIC must be initialized first (in main)
	// Transition from bump allocator to buddy allocator
	// All early boot allocations used the bump allocator; now switch to buddy
	// for proper allocation/deallocation support
	kmem.TransitionToBuddy()

}

// uartPutc writes a single character to UART.
// This function can be called from normal Go code (preemptible goroutines).
// It's NOT marked nosplit because it calls into the console package.
//
// NOTE: Do not call from IRQ handlers or nosplit contexts - use Breadcrumb() instead.
//
func uartPutc(c byte) {
	console.KWriteByte(c)
}

// uartPuts writes a string to UART.
// Safe for normal Go code (preemptible).
func uartPuts(s string) {
	for i := 0; i < len(s); i++ {
		uartPutc(s[i])
	}
}

// uartPutsDirect writes a string directly to UART using breadcrumbs.
// Used by ksyscall and kthread packages via linkname (nosplit contexts).
// Uses Breadcrumb which is NOT safe for async preemption.
//go:linkname uartPutsDirect mazzy/kmazarin/ksyscall.uartPutsDirect
//go:nosplit
func uartPutsDirect(s string) {
	BreadcrumbString(s)
}

// uartPutcDirectForKmem writes a byte directly to UART using breadcrumbs.
// Used by kmem package via linkname (nosplit context).
// Uses Breadcrumb which is NOT safe for async preemption.
//go:linkname uartPutcDirectForKmem mazzy/kmazarin/kmem.uartPutcDirect
//go:nosplit
func uartPutcDirectForKmem(c byte) {
	Breadcrumb(c)
}

// Runtime readiness flag - set to true once we verify runtime is fully initialized
var runtimeReady = false

// safeDTBVirtAddr holds the VA of a safe copy of the FDT.
// On RISC-V, the FDT at PA 0xFFE00000 is within the buddy allocator's range
// and gets overwritten by the framebuffer allocation (PA 0xFF000000, ~16MB).
// Copying the FDT early preserves it for device discovery.
var safeDTBVirtAddr uintptr

// copyFDTToSafeLocation copies the Device Tree Blob to a buddy-allocated
// buffer so it survives large physical memory allocations like the framebuffer.
func copyFDTToSafeLocation() {
	dtbPhysAddr := GetDtbPhysAddr()
	if dtbPhysAddr == 0 {
		return
	}

	dtbVA := dtbVirtAddr(dtbPhysAddr)

	// Read big-endian FDT header: magic (4 bytes) + totalsize (4 bytes)
	hdr := (*[8]byte)(unsafe.Pointer(dtbVA))
	magic := uint32(hdr[0])<<24 | uint32(hdr[1])<<16 | uint32(hdr[2])<<8 | uint32(hdr[3])
	if magic != 0xd00dfeed {
		console.KPrintf("[DTB] Bad magic at VA 0x%X: 0x%X\n", dtbVA, magic)
		return
	}

	totalSize := uint32(hdr[4])<<24 | uint32(hdr[5])<<16 | uint32(hdr[6])<<8 | uint32(hdr[7])
	if totalSize == 0 || totalSize > 1<<20 {
		console.KPrintf("[DTB] Invalid size: %d\n", totalSize)
		return
	}

	buf := kmem.AllocBuffer(uint64(totalSize))
	if buf == nil {
		console.KPrintf("[DTB] Failed to alloc %d bytes\n", totalSize)
		return
	}

	// Copy the FDT data
	src := unsafe.Slice((*byte)(unsafe.Pointer(dtbVA)), int(totalSize))
	copy(buf.Bytes(), src)

	safeDTBVirtAddr = buf.VA
	console.KPrintf("[DTB] Preserved %d bytes (PA 0x%X → VA 0x%X)\n", totalSize, dtbPhysAddr, safeDTBVirtAddr)
}

// DebugTimerCount is a debug counter for tracking timer IRQs.
// Placed in main package to test if the kirq.TimerIRQCount location has mapping issues.
// This variable is written from timer IRQ handler assembly.
var DebugTimerCount uint64

// readyForAsyncPreempt controls whether timer IRQs should trigger async preemption.
// Set to 0 initially, then 1 in main() after Go runtime is fully initialized.
// This prevents asyncPreempt crashes during runtime.doInit1 (package init phase).
// Accessed atomically from interrupt context.
var readyForAsyncPreempt atomic.Uint32

// Print uses direct UART before runtime is ready, fmt.Println after
func Print(s string) {
	if !runtimeReady {
		uartPuts(s)
		uartPuts("\r\n")
	} else {
		fmt.Println(s)
	}
}

// Printf uses direct UART with hex before runtime is ready, fmt.Printf after
func Printf(format string, args ...interface{}) {
	if !runtimeReady {
		// Simple implementation for early boot
		// Just print format string and args with direct UART
		uartPuts(format)
		uartPuts(" ")
		for _, arg := range args {
			// Print hex representation
			switch v := arg.(type) {
			case uint64:
				printHex(v)
			case uintptr:
				printHex(uint64(v))
			case int:
				printHex(uint64(v))
			default:
				uartPuts("[?]")
			}
			uartPuts(" ")
		}
		uartPuts("\r\n")
	} else {
		fmt.Printf(format, args...)
	}
}

// readDAIF reads the DAIF register to check interrupt mask state
//go:nosplit
func readDAIF() uint64 {
	var daif uint64
	// MRS DAIF, X0 = 0xD53B4200
	// We'll use inline assembly via pointer tricks
	// For now, just read via a syscall to mazboot (we pass 9999 as a special debug syscall)
	// Actually, let's just use raw assembly in a .s file
	// For now, print a placeholder and check if timer fires at all
	return daif
}

// printHex prints a hex value directly to UART
//go:nosplit
func printHex(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
}

// PrintHex64 prints a hex value with 0x prefix and newline
func PrintHex64(val uint64) {
	uartPuts("0x")
	printHex(val)
	uartPuts("\r\n")
}

// testRuntimeReadiness runs a comprehensive test suite to verify Go runtime is ready
// Returns true if all tests pass, false otherwise
func testRuntimeReadiness() bool {
	// Test 1: Memory allocation with make()
	s := make([]byte, 16)
	if len(s) != 16 {
		return false
	}
	s[0] = 42
	if s[0] != 42 {
		return false
	}

	// Test 2: Memory allocation with new()
	p := new(int)
	if p == nil {
		return false
	}
	*p = 123
	if *p != 123 {
		return false
	}

	// Test 3: GOMAXPROCS (scheduler P structs)
	n := runtime.GOMAXPROCS(0)
	if n == 0 {
		return false
	}

	// Test 4: Goroutine count
	ng := runtime.NumGoroutine()
	if ng == 0 {
		return false
	}

	// Test 5: Mutex operations
	var mu sync.Mutex
	mu.Lock()
	mu.Unlock()

	// Test 6: String operations (uses runtime)
	str1 := "Hello"
	str2 := "World"
	str3 := str1 + " " + str2
	if len(str3) != 11 {
		return false
	}

	// Test 7: Thread structure allocation
	thread := new(Thread)
	if thread == nil {
		return false
	}
	thread.State = ThreadReady
	thread.TID = 999
	thread.Context.SetReturnValue(0xDEADBEEF)
	if thread.State != ThreadReady || thread.TID != 999 || thread.Context.GetReturnValue() != 0xDEADBEEF {
		return false
	}

	// Test 8: Slice of Threads allocation
	threadSlice := make([]Thread, 4)
	if len(threadSlice) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		threadSlice[i].TID = ThreadId(100 + i)
	}
	for i := 0; i < 4; i++ {
		if threadSlice[i].TID != ThreadId(100+i) {
			return false
		}
	}

	return true
}

// DisableTimerIRQ disables BOTH the timer hardware and its IRQ at the interrupt controller.
// This is necessary because disabling just the IRQ masks delivery but doesn't
// stop the timer from generating interrupt requests.
func DisableTimerIRQ() {
	ktimer.Disable()
	if cachedIC != nil {
		cachedIC.DisableIRQ(ktimer.IRQNum())
	}
}

// printTimerDebug is defined in debug_arm64.go (ARM64-specific).

// cachedIC holds a reference to the interrupt controller for timer enable/disable.
// Set once during boot after device discovery.
var cachedIC deviceapi.InterruptController

// initCachedIC must be called once during boot after device discovery.
func initCachedIC() {
	if ic, ok := device.GetInterruptController(); ok {
		cachedIC = ic
	}
}

// EnableTimerIRQ enables the timer IRQ.
// On RISC-V, the timer is controlled directly via SIE CSR (STIE bit),
// so it works without an interrupt controller. On ARM64/x86, the timer
// also needs to be enabled at the interrupt controller (GIC/APIC).
func EnableTimerIRQ() {
	// CRITICAL: Rearm timer BEFORE enabling the IRQ at the controller.
	// On ARM64, IRQ 27 is edge-triggered. If the timer line is already
	// asserted (ISTATUS=1 from a previous expiration) when we enable
	// the controller, there's no rising edge and the interrupt never fires.
	// Rearming first clears the pending state, then after the enable,
	// the next expiration creates a proper edge.
	// On RISC-V, PlatformRearmTimer also sets the STIE bit in SIE.
	ktimer.Rearm(kirq.GetTimerTicksFor10ms())
	if cachedIC != nil {
		cachedIC.EnableIRQ(ktimer.IRQNum())
	}
}

// testDeviceDiscovery tests the DTB-based device discovery system
// This is a temporary test function to verify DTB parsing and device matching
func testDeviceDiscovery() {
	console.KPrintln("")
	// NOTE: Drivers are already registered in EarlyInit()

	// Use safe FDT copy if available (protects against framebuffer overwrite),
	// otherwise fall back to the original location.
	var dtbAddr uintptr
	if safeDTBVirtAddr != 0 {
		dtbAddr = safeDTBVirtAddr
	} else {
		dtbPhysAddr := GetDtbPhysAddr()
		if dtbPhysAddr == 0 {
			console.KPrintln("[DeviceTest] No DTB available (UEFI ACPI mode?) - skipping device discovery")
			return
		}
		dtbAddr = dtbVirtAddr(dtbPhysAddr)
	}
	console.KPrintf("[DeviceTest] DTB at VA 0x%X\n", dtbAddr)

	// Register all device drivers BEFORE discovering devices
	device.RegisterAllDrivers()

	// Parse DTB and discover devices
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		console.KWriteString("[DeviceTest] ERROR: ")
		console.KPrintln(err.Error())
		return
	}

	// Initialize kernel time subsystem (reads RTC once, caches for tick-based derivation)
	if clk, ok := device.GetClock(); ok {
		_ = clk
		ktime.Init()
	}

	// Wire up interrupts now that interrupt controller is discovered
	if err := device.WireInterrupts(); err != nil {
		console.KWriteString("[DeviceTest] ERROR wiring interrupts: ")
		console.KPrintln(err.Error())
	} else {
		// Set up UART soft IRQ hook for userspace serial RX
		if bs, ok := device.GetByteStream(); ok {
			if pl011, ok := bs.(*uart.PL011); ok {
				SetupUartSoftIRQ(pl011.IRQ())
			} else if ns16550, ok := bs.(*uart.NS16550); ok {
				SetupUartSoftIRQ(ns16550.IRQ())
			}
		}
		// Enable external interrupt delivery (PLIC on RISC-V, no-op elsewhere)
		setupExternalInterrupts()
	}
}

// initVirtIOGPU initializes the VirtIO GPU device for display output
func initVirtIOGPU() {
	if !gpu.Init() {
		return
	}

	// Boot image disabled temporarily to evaluate neumorphic shadows
	if false {
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KPrintln("[VirtIO GPU] No block device, skipping boot image")
	} else {
		fs, err := fat32.Mount(blk)
		if err != nil {
			console.KPrintf("[VirtIO GPU] FAT32 mount failed: %v\n", err)
		} else {
			file, err := fs.Open("/boot-image.bin")
			if err != nil {
				console.KPrintf("[VirtIO GPU] boot-image.bin not found: %v\n", err)
			} else {
				fileSize := file.Size()

				// Use buddy allocator for contiguous physical memory (avoids demand paging issues)
				buf := kmem.AllocBuffer(uint64(fileSize))
				if buf == nil {
					console.KPrintln("[VirtIO GPU] Failed to allocate buffer for boot image")
				} else {
					data := buf.Bytes()
					n, err := file.Read(data)
					file.Close()
					if err != nil && n == 0 {
						console.KPrintf("[VirtIO GPU] Failed to read boot-image.bin: %v\n", err)
						kmem.FreeBuffer(buf)
					} else {
						console.KPrintf("[VirtIO GPU] Boot image loaded from disk, %d bytes\n", n)
						if gpu.RenderBootImage(uintptr(unsafe.Pointer(&data[0])), uint64(n)) {
							gpu.UpdateDisplay(0, 0, gpu.GetWidth(), gpu.GetHeight())
							console.KPrintln("[VirtIO GPU] Boot image displayed")
						}
						kmem.FreeBuffer(buf)
					}
				}
			}
		}
	}
	} // end if false
}

// initVirtIOInputDevices discovers VirtIO input devices and wires their
// MSI-X interrupts into the GIC so keypresses generate IRQs.
func initVirtIOInputDevices() {
	input.InitVirtIOInput()

	// Wire each input device's MSI-X IRQ into the GIC
	devices := input.AllDevices()
	if len(devices) == 0 {
		return
	}

	for _, dev := range devices {
		if dev == nil {
			continue
		}
		irqNum := dev.IRQNum
		if irqNum == 0 {
			continue
		}

		// Register the input dispatch handler in both GIC and kirq tables
		input.RegisterIRQDevice(irqNum, dev)

		// Wire hardware interrupt controller for real IRQs.
		// ARM64 uses MSI-X (GIC SPIs), RISC-V uses PCI INTx (PLIC sources 32-35).
		if cachedIC != nil && irqNum != 0 {
			// Capture irqNum for closure (Go 1.22+ loop var semantics make this safe,
			// but be explicit for clarity)
			localIRQ := irqNum
			// Register handler: when this IRQ fires, dispatch to input driver
			cachedIC.RegisterHandler(localIRQ, func() {
				input.DispatchIRQ(uint64(localIRQ))
			})
			// MSI-X interrupts are edge-triggered (message write = edge event)
			cachedIC.SetIRQEdgeTriggered(localIRQ)
			// Set priority to 0xA0 (lower than timer at 0x00, higher than nothing)
			cachedIC.SetIRQPriority(localIRQ, 0xA0)
			// Route to CPU 0 (required — bulk SPI init may not cover MSI-X IRQs)
			cachedIC.SetIRQTarget(localIRQ, 0x01)
			cachedIC.EnableIRQ(localIRQ)
		}
	}

	// Wire soft IRQ slot fire callback for future userspace event delivery
	input.SetSoftIRQFireFunc(SoftIRQSlotFire)

	// Register devices with the nosplit top-half handler so it can read
	// events directly from the DMA-mapped used ring without Go allocation.
	for _, dev := range devices {
		if dev == nil {
			continue
		}
		isMouse := dev.DevType == 2 // hid.DeviceTypeMouse
		vq := &dev.EventQueue
		usedVA := uintptr(unsafe.Pointer(vq.Used))
		evtBufVA := uintptr(unsafe.Pointer(&dev.EventBuffers[0]))
		availVA := uintptr(unsafe.Pointer(vq.Available))
		descVA := uintptr(vq.DescTable)
		notifyAddr := dev.NotifyBase +
			uintptr(dev.EventQueueNotifyOff)*uintptr(dev.NotifyConfig.NotifyOffMultiplier)
		evtBufPA := dev.EventBuffersPA
		initAvailIdx := vq.Available.Idx
		SetTopHalfDev(dev.IRQNum, usedVA, evtBufVA, availVA, descVA,
			notifyAddr, evtBufPA, dev.ISRBase, vq.QueueSize, initAvailIdx, isMouse, &vq.LastUsedIdx)
	}

}


// busyLoop4s prints '4' in a tight loop to test kernel goroutine scheduling.
// This runs as a separate goroutine in kmazarin alongside the main goroutine.
func busyLoop4s() {
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				fmt.Println("")
			} else {
				// Use fmt.Print instead of console.KWriteString to go through
				// Go's runtime mutexes - this makes scheduling fair with priests
				// who also use fmt.Print and block on the same mutexes.
				fmt.Print("4")
			}
		}
	}
}


// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
// verifyCodeIntegrityKmazarin scans near the end of the text segment for zero corruption.
// Checks BOTH the code VA (through Sv48 mapping) and the physical memory (through linear map)
// to distinguish page table bugs from actual physical corruption.
// RISC-V only — addresses are hardcoded for RISC-V's Sv48 page table layout.
//
//go:nosplit
func verifyCodeIntegrityKmazarin(label string) {
	if runtime.GOARCH != "riscv64" {
		return
	}
	// Use uartPuts directly for reliability
	uartPuts("[VERIFY_K] ")
	uartPuts(label)
	uartPuts(": ")

	// Check via code VA mapping (0x438b5000-0x438b6000)
	codeVAStart := uintptr(0x438b5000)
	count := uintptr(0x1000 / 4)
	codeZeros := 0
	for i := uintptr(0); i < count; i++ {
		val := *(*uint32)(unsafe.Pointer(codeVAStart + i*4))
		if val == 0 {
			codeZeros++
		}
	}

	// Check via linear map (PA = VA - 0x43800000 + 0x90000000 + KernelVAOffset)
	// PA of 0x438b5000 = 0x90000000 + (0x438b5000 - 0x43800000) = 0x900b5000
	// Linear map VA = PA + 0xFFFFFFFF00000000 = 0xFFFFFFFF900b5000
	linVAStart := uintptr(0xFFFFFFFF900b5000)
	linZeros := 0
	for i := uintptr(0); i < count; i++ {
		val := *(*uint32)(unsafe.Pointer(linVAStart + i*4))
		if val == 0 {
			linZeros++
		}
	}

	uartPuts("code_zeros=")
	printHex(uint64(codeZeros))
	uartPuts(" lin_zeros=")
	printHex(uint64(linZeros))
	if codeZeros > 64 && linZeros < 64 {
		uartPuts(" PAGE_TABLE_BUG!")
	} else if codeZeros > 64 && linZeros > 64 {
		uartPuts(" PHYS_CORRUPTION!")
	} else {
		uartPuts(" OK")
	}
	uartPuts("\r\n")
}

func simpleMain() {
	Print("[Main] Kmazarin kernel starting...")

	// DEBUG: Check code integrity at kmazarin entry
	verifyCodeIntegrityKmazarin("at kmazarin entry")

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		Print("[Main] Runtime ready")

		debug.SetGCPercent(-1)
		InitDeadlineQueue()
		InitSoftIRQDispatcher()
	} else {
		Print("[Main] Runtime not ready - continuing with direct UART")
	}

	// Copy FDT to a safe location BEFORE GPU init. On RISC-V, the FDT at
	// PA 0xFFE00000 is within the buddy allocator's range and gets overwritten
	// when the framebuffer is allocated at PA 0xFF000000 (~16MB).
	copyFDTToSafeLocation()

	// Initialize VirtIO GPU BEFORE switching exception vectors.
	// On x86_64, kmazarin's HandlePageFault uses ARM64 PTE format and can't
	// handle demand paging. By doing GPU init here, heap allocations in
	// VirtqueueInit trigger diplomat's page fault handler instead.
	// On ARM64, this is also safe — GPU init uses polling (no IRQs needed).
	initVirtIOGPU()
	console.KPrintln("[Main] VirtIO GPU init done")

	// Initialize VirtIO block device (also safe to do before VBAR switch)
	console.KPrintln("[Main] About to init VirtIO Block...")
	if block.Init() {
		console.KPrintln("[Main] VirtIO Block init done")
		// Quick verification: check PA translation and block reads
		blk, blkOk := device.GetBlockDevice()
		if blkOk {
			testBuf := make([]byte, 512)
			if err := blk.ReadBlock(0, testBuf); err != nil {
				console.KPrintln("[BlockTest] Sector 0 read FAILED")
			} else {
				console.KPrintf("[BlockTest] Sector 0: sig=0x%02X%02X OEM=%c%c%c%c%c\n",
					testBuf[511], testBuf[510], testBuf[3], testBuf[4], testBuf[5], testBuf[6], testBuf[7])
			}
		}
	} else {
		console.KPrintln("[Main] VirtIO Block init failed (no device found?)")
	}

	// CRITICAL: Set VBAR_EL1 to point to kmazarin's exception vector table
	// Cardinal's VBAR_EL1 points to its own vectors at low memory (0x401xxxxx).
	// We must update VBAR_EL1 to use kmazarin's vectors at high memory.
	// This must happen BEFORE any device initialization that might enable interrupts!
	vectorAddr := GetExceptionVectorBase()
	SetVBAR(vectorAddr)

	// Test DTB-based device discovery BEFORE unmapping Cardinal
	// (DTB is at 0x40000000 in Cardinal's memory region)
	testDeviceDiscovery()

	console.KPrintln("[Main] About to initCachedIC")
	// Cache GIC pointer for nosplit-safe timer IRQ enable/disable
	initCachedIC()
	console.KPrintln("[Main] initCachedIC done")

	console.KPrintln("[Main] About to initVirtIOInputDevices")
	// Initialize VirtIO Input devices (keyboard, mouse)
	initVirtIOInputDevices()
	console.KPrintln("[Main] initVirtIOInputDevices done")

	console.KPrintln("[Main] About to EnableIRQs")
	// CRITICAL: Enable IRQs at CPU AFTER GIC is initialized (matches Cardinal's order)
	// This unmasks IRQs at the CPU (clears DAIF.I bit)
	EnableIRQs()
	console.KPrintln("[Main] EnableIRQs done")
	EnableTimerIRQ()
	console.KPrintln("[Main] EnableTimerIRQ done")

	asyncPreemptAddr := GetAsyncPreemptAddr()
	kirq.SetAsyncPreemptAddr(asyncPreemptAddr)
	readyForAsyncPreempt.Store(1)
	kirq.SetReadyForAsyncPreempt()

	unmapCardinal()
	DisableTimerIRQ()

	// =========================================================================
	// USERSPACE TEST: Launch 6 sieve workers as separate threads
	// =========================================================================
	// This tests kernel thread scheduling fairness:
	//   1. Load 6 separate sieve programs (sieve3-9) from FAT32 disk
	//   2. Each runs as a separate OS thread (not goroutines)
	//   3. Kernel scheduler distributes CPU time among them
	//   4. Each prints primes as "ID:prime" (e.g., "3:20011")
	//
	// To switch back to goroutine test, comment out below and launch priestsieve.elf

	// DEBUG: ReadMemStats disabled - hangs in bare-metal (triggers STW GC)

	// DEBUG: Check code integrity before launching userspace
	verifyCodeIntegrityKmazarin("before userspace launch")

	// Launch dapope (input event handler priest)
	dapopeName := "/dapope.elf\x00"
	dapopePtr := uintptr(unsafe.Pointer(&([]byte(dapopeName))[0]))
	result := ksyscall.SyscallLaunch(uint64(dapopePtr), 0, 0, 0, 0, 0)
	if result == 0 {
		kmem.FinalUserspaceSync()
		Print("[main] dapope launched")
	} else {
		console.KPrintf("[main] dapope launch failed (error %d)\n", result)
	}

	// Launch stdio (console priest — serial port + display)
	stdioName := "/stdio.elf\x00"
	stdioPtr := uintptr(unsafe.Pointer(&([]byte(stdioName))[0]))
	result = ksyscall.SyscallLaunch(uint64(stdioPtr), 0, 0, 0, 0, 0)
	if result == 0 {
		kmem.FinalUserspaceSync()
		Print("[main] stdio launched")
	} else {
		console.KPrintf("[main] stdio launch failed (error %d)\n", result)
	}

	// Re-enable IRQs and timer for ongoing scheduling
	EnableIRQs()
	EnableTimerIRQ()
	console.KPrintln("[Main] Second EnableIRQs done")

	// Timer and IRQs verified working at this point

	// Start kernel time accounting for performance measurements
	kirq.StartKernelTimeAccounting()

	// SMP: Wake secondary CPUs
	// For now, disabled until SMP scheduling is fully tested
	// Uncomment the following line to enable SMP boot:
	// StartSecondaryCPUs()
	_ = StartSecondaryCPUs // Silence unused warning

	// Start program clock for timed shutdown (15 seconds in raw counter ticks).
	// Disable IRQs briefly so no timer preemption during reset.
	savedDAIF := SaveAndDisableIRQs()
	startingTicksProgram = kirq.ReadCounterValue()
	shutdownTicksThreshold = kirq.SystemTimerFrequency * 300
	ResetTickAccounting(startingTicksProgram)
	RestoreIRQs(savedDAIF)

	// Enter the kernel idle loop. Thread 0 (m0/g0) stays alive as a normal
	// scheduled thread. Priest threads are already running. The timer IRQ
	// preempts thread 0 and context-switches to priest threads naturally.
	//
	// This preserves thread 0 for the Go runtime — m0 continues to exist
	// and can run goroutines (sysmon, GC, etc.) when scheduled back.
	// (idle loop entry breadcrumb removed for performance)
	KernelIdleLoop()

	// Should never reach here
	Print("[Main] ERROR: KernelIdleLoop returned!\r\n")
	for {
	}
}

func main() {
	simpleMain()
}
