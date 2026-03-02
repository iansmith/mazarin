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

	// AMD64: Check if the current thread's preemption deadline has expired.
	// On ARM64/RISC-V, TimerIRQHandlerAsm is called directly from the exception
	// handler and sets NeedsThreadPreempt. On AMD64, the assembly handler isn't
	// called from the exception path, so we check the deadline here instead.
	t := GetCurrentThread()
	if t != nil {
		currentTick := kirq.ReadCounterValue()
		if currentTick >= t.ThreadPreemptDeadline {
			atomic.StoreUint32(&kirq.NeedsThreadPreempt, 1)
		}
	}

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

// HandleUserPageFaultAsm is defined in abi_stubs_<arch>.s as an ABI0 entry point
// that tail-calls handleUserPageFaultInternal. This handles page faults from EL0.
// isPermFault is 1 if the exception was a permission fault, 0 for translation/access.
// Returns 1 if the fault was handled successfully, 0 otherwise.
//
//go:nosplit
//go:noinline
func handleUserPageFaultInternal(faultAddr, isPermFault uint64) uint64 {
	if kmem.HandleUserPageFault(uintptr(faultAddr), isPermFault) {
		return 1
	}
	return 0
}

// init runs before main - called after Go runtime is fully initialized
// Set up exception handlers and enable interrupts
func init() {
	// NOTE: GC percentage will be set in simpleMain() where runtime is fully ready.
	// Setting here is too early and causes hangs.

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
	console.KPrintf("[Timer] freq=%d Hz\n", ktimer.Frequency())
	kirq.RegisterHandlers()
	kirq.InitPreemption()

	// Initialize signal delivery infrastructure (sigreturn trampoline address)
	InitSignals()

	// NOTE: OLD UART initialization disabled - now using PL011 device driver
	// kirq.InitUART()

	// Start bottom-half processors for interrupt dispatch
	// The event poller will check IRQ pending flags and call handlers in safe Go context
	StartBottomHalfProcessors()

	// NOTE: IRQs are NOT enabled yet - GIC must be initialized first (in main)
	// NOTE: Buddy allocator is now initialized eagerly in InitUnifiedPool()
	// (Stage 2 of memory overhaul). No TransitionToBuddy() call needed.

}

// uartPutc writes a single character to UART.
// This function can be called from normal Go code (preemptible goroutines).
// It's NOT marked nosplit because it calls into the console package.
//
// NOTE: Do not call from IRQ handlers or nosplit contexts - use serial.PollWrite() instead.
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
	ktimer.Rearm(kirq.TimerRearmTicks)
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
	// Register all device drivers BEFORE discovering devices
	device.RegisterAllDrivers()

	// Parse DTB and discover devices
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		console.KWriteString("[DeviceTest] ERROR: ")
		console.KPrintln(err.Error())
		return
	}

	// Initialize kernel time subsystem (reads RTC once, caches for tick-based derivation).
	// On platforms without an RTC (e.g. AMD64), falls back to uptime mode (base=0).
	ktime.Init()

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
		// AMD64: register COM1 as UART if no DTB-based UART was found.
		if uartIRQNum == 0 {
			initCOM1Uart()
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

		// Wire hardware interrupt controller for real IRQs.
		// ARM64 uses MSI-X (GIC SPIs), RISC-V uses PCI INTx (PLIC sources 32-35),
		// AMD64 uses MSI-X (LAPIC vectors). On AMD64, MSI-X bypasses the IOAPIC
		// entirely — the device writes directly to the LAPIC, so we only need
		// to register a no-op handler with kirq (in case DispatchIRQ is called).
		if cachedIC != nil && irqNum != 0 {
			localIRQ := irqNum
			cachedIC.RegisterHandler(localIRQ, func() {
				// No-op: events handled by NonTimerIRQTopHalf via assembly top-half.
				_ = localIRQ
			})
			if runtime.GOARCH != "amd64" {
				// ARM64/RISC-V: configure interrupt controller for this IRQ.
				// AMD64 MSI-X bypasses IOAPIC, no configuration needed.
				cachedIC.SetIRQEdgeTriggered(localIRQ)
				cachedIC.SetIRQPriority(localIRQ, 0xA0)
				cachedIC.SetIRQTarget(localIRQ, 0x01)
				cachedIC.EnableIRQ(localIRQ)
			}
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

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		Print("[Main] Runtime ready")

		// NOTE: GOGC is NOT set here — kernel uses Go default (100%).
		// Userspace programs also get GOGC=100 via their envp in launch.go.
		// GOMEMLIMIT is set via diplomat envp (64MiB).
		debug.SetMemoryLimit(64 * 1024 * 1024)     // 64MB soft heap cap (matches diplomat envp)
		InitDeadlineQueue()
		InitSoftIRQDispatcher()
	} else {
		Print("[Main] Runtime not ready - continuing with direct UART")
	}

	// Log RAM layout and kernel budget (Stage 3: verify AT_RAM_BASE/AT_RAM_SIZE received)
	console.KPrintf("[Main] RAM: base=0x%X size=%dMB kernel_budget=%dMB\n",
		GetRAMBaseAddr(), GetTotalRAMSize()>>20, GetKernelBudgetMB())

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

	// CRITICAL: Set VBAR_EL1 to point to kmazarin's exception vector table
	// Cardinal's VBAR_EL1 points to its own vectors at low memory (0x401xxxxx).
	// We must update VBAR_EL1 to use kmazarin's vectors at high memory.
	// This must happen BEFORE any device initialization that might enable interrupts!
	vectorAddr := GetExceptionVectorBase()
	SetVBAR(vectorAddr)

	// Mark syscall handlers as operational. This enables the runtime overlays
	// (usleep, futex) to issue real SVCs for proper blocking instead of spin+yield.
	SetSyscallReady()

	// Configure SYSCALL MSRs for userspace (x86_64 only; no-op on ARM64)
	SetupSyscallMSRs()

	// Test DTB-based device discovery BEFORE unmapping Cardinal
	// (DTB is at 0x40000000 in Cardinal's memory region)
	testDeviceDiscovery()

	// Cache GIC pointer for nosplit-safe timer IRQ enable/disable
	initCachedIC()

	// Initialize VirtIO Input devices (keyboard, mouse).
	// This also initializes the platform interrupt infrastructure (GICv2m SPI
	// allocator on ARM64) needed for MSI-X configuration of any PCI device.
	initVirtIOInputDevices()

	// Initialize VirtIO block device. For PCI transport, Init() determines
	// the INTx GIC IRQ from the PCI interrupt pin routing (no MSI-X).
	if !block.Init() {
		console.KPrintln("[Main] VirtIO Block init failed (no device found?)")
	}

	// Wire up block device IRQ: register with top-half dispatcher and
	// enable the GIC SPI so INTx interrupts reach the CPU.
	if irq := block.GetIRQNum(); irq != 0 {
		SetBlockIRQ(irq, block.GetISRBase(), block.GetIOCompletePtr())
		if cachedIC != nil {
			cachedIC.EnableIRQ(irq)
		}
	}

	// CRITICAL: Enable IRQs at CPU AFTER GIC is initialized (matches Cardinal's order)
	// This unmasks IRQs at the CPU (clears DAIF.I bit)
	EnableIRQs()
	// Fix clone threads created during Go runtime init — they have IF=0 in
	// their saved RFLAGS because they were cloned before EnableIRQs(). Without
	// this, those threads run without timer interrupts when scheduled, causing
	// the system to freeze if they pick up a non-blocking goroutine.
	FixCloneThreadIFFlags()
	EnableTimerIRQ()

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

	// Suppress Go runtime write1 → UART output now that boot is complete.
	// Panic/traceback paths temporarily unsuppress (see runtime-patches/panic.go).
	// TEMPORARILY DISABLED for debugging ARM64 userspace crash:
	// atomic.StoreUint32(&suppressSerial, 1)

	// Enter the kernel idle loop. Thread 0 (m0/g0) stays alive as a normal
	// scheduled thread. Priest threads are already running. The timer IRQ
	// preempts thread 0 and context-switches to priest threads naturally.
	//
	// This preserves thread 0 for the Go runtime — m0 continues to exist
	// and can run goroutines (sysmon, GC, etc.) when scheduled back.
	KernelIdleLoop()

	// Should never reach here
	Print("[Main] ERROR: KernelIdleLoop returned!\r\n")
	for {
	}
}

func main() {
	simpleMain()
}
