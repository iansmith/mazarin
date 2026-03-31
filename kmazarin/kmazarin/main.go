package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/kmazarin/device/virtio/rng"
	"mazzy/kmazarin/dtb"
	"mazzy/shared/constants"
	"mazzy/shared/hid"
	toml "github.com/pelletier/go-toml/v2"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/ktimer"
	"mazzy/kmazarin/serial"
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

	// Initialize thread table - M0 is thread 0
	// MUST happen before any clone syscalls!
	InitThreads()

	// Initialize soft IRQ subsystem (static allocation, no heap needed)
	InitSoftIRQ()

	// Initialize channel subsystem for kernel-to-shepherd async messaging
	InitChannels()

	// Initialize critical early devices (UART, GIC, Timer, RNG)
	EarlyInit()

	// Initialize timer hardware and IRQ handlers.
	// Timer frequency and tick thresholds are set later during DTB processing
	// in initTimerFrequency(), which is called before EnableTimerIRQ().
	kirq.InitTimer()
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
	disableTimerAtController()
}

// printTimerDebug is defined in debug_arm64.go (ARM64-specific).

// EnableTimerIRQ enables the timer IRQ.
// On RISC-V, the timer is controlled directly via SIE CSR (STIE bit),
// so it works without an interrupt controller. On ARM64, the timer
// also needs to be enabled at the GIC. On x86_64, the LAPIC timer
// is local and doesn't need the IOAPIC.
func EnableTimerIRQ() {
	// CRITICAL: Rearm timer BEFORE enabling the IRQ at the controller.
	// On ARM64, IRQ 27 is edge-triggered. If the timer line is already
	// asserted (ISTATUS=1 from a previous expiration) when we enable
	// the controller, there's no rising edge and the interrupt never fires.
	// Rearming first clears the pending state, then after the enable,
	// the next expiration creates a proper edge.
	// On RISC-V, PlatformRearmTimer also sets the STIE bit in SIE.
	ktimer.Rearm(kirq.TimerRearmTicks)
	enableTimerAtController()
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

	// Calibrate: compare ARM64 generic timer counter against PL031 RTC.
	// The PL031 returns wall-clock seconds from the host. By measuring how
	// many counter ticks elapse per RTC second, we discover the actual
	// counter frequency regardless of what CNTFRQ_EL0 claims.
	if clock, ok := device.GetClock(); ok {
		calibrateCounterVsRTC(clock)
	}

	// Wire up interrupts now that interrupt controller is discovered
	if err := device.WireInterrupts(); err != nil {
		console.KWriteString("[DeviceTest] ERROR wiring interrupts: ")
		console.KPrintln(err.Error())
	} else {
		// Set up UART soft IRQ hook for userspace serial RX, and
		// register the interrupt-driven TX path (QueueByte → txBuf → top-half drain).
		if bs, ok := device.GetByteStream(); ok {
			if pl011, ok := bs.(*uart.PL011); ok {
				SetupUartSoftIRQ(pl011.IRQ())
				serial.SetQueueByteFunc(pl011.WriteByte)
				StoreUartTxDriver(pl011)
			} else if ns16550, ok := bs.(*uart.NS16550); ok {
				SetupUartSoftIRQ(ns16550.IRQ())
				serial.SetQueueByteFunc(ns16550.WriteByte)
				StoreUartTxDriver(ns16550)
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

// discoveredDTBTimerFreq is set by dtbTimerFreqCallback during DTB walk.
var discoveredDTBTimerFreq uint32

// initTimerFrequency discovers the platform timer frequency and sets
// kirq.SystemTimerFrequency, then computes the kernel tick count
// (kirq.TimerRearmTicks from TickIntervalMs) and thread preemption
// quantum (kirq.ThreadPreemptTicks from ThreadPreemptMs).
//
// On RISC-V, the frequency comes from DTB /cpus/timebase-frequency.
// On ARM64 and x86_64, it comes from hardware registers calibrated
// by ktimer.Init() during early boot (CNTFRQ_EL0 and PIT respectively).
//
// Called from simpleMain() after DTB processing, before EnableTimerIRQ().
func initTimerFrequency() {
	// Determine DTB address (same logic as testDeviceDiscovery)
	var dtbAddr uintptr
	if safeDTBVirtAddr != 0 {
		dtbAddr = safeDTBVirtAddr
	} else {
		dtbPhysAddr := GetDtbPhysAddr()
		if dtbPhysAddr != 0 {
			dtbAddr = dtbVirtAddr(dtbPhysAddr)
		}
	}

	// Walk DTB for /cpus/timebase-frequency (present in RISC-V DTBs)
	discoveredDTBTimerFreq = 0
	if dtbAddr != 0 {
		dtb.Walk(dtbAddr, dtbTimerFreqCallback)
	}

	if discoveredDTBTimerFreq > 0 {
		kirq.SystemTimerFrequency = uint64(discoveredDTBTimerFreq)
	} else {
		// ARM64: CNTFRQ_EL0 (hardware register, cached by ktimer.Init())
		// x86_64: PIT-calibrated TSC frequency (cached by ktimer.Init())
		kirq.SystemTimerFrequency = uint64(ktimer.Frequency())
	}

	if kirq.SystemTimerFrequency == 0 {
		kirq.SystemTimerFrequency = 62500000 // Fallback for QEMU virt
	}

	// Update the runtime's nanotime1 conversion factor from the actual frequency.
	// nsPerTickX256 = 256_000_000_000 / freq gives a fixed-point ×256 multiplier
	// that assembly uses: ns = (ticks × nsPerTickX256) >> 8.
	nsPerTickX256 = 256_000_000_000 / kirq.SystemTimerFrequency

	// Compute tick counts from frequency and default policy values.
	// These may be reconfigured later by TOML boot config (InitPreemptConfig).
	kirq.InitPreemptConfig(0, 0)

	console.KPrintf("[Timer] freq=%d Hz, nsPerTickX256=%d, tick=%d ticks (%dms), quantum=%d ticks (%dms)\n",
		kirq.SystemTimerFrequency, nsPerTickX256, kirq.TimerRearmTicks, kirq.TickIntervalMs,
		kirq.ThreadPreemptTicks, kirq.PreemptIntervalMs)
}

// calibrateCounterVsRTC measures the actual counter tick rate by comparing
// the ARM64 generic timer (CNTVCT_EL0) against the PL031 RTC wall-clock.
// PL031 has 1-second resolution, so we wait for two second boundaries
// and count how many counter ticks elapsed in between.
//
// This tells us whether CNTFRQ_EL0 matches reality. On Apple Silicon HVF,
// the counter should be 24MHz. Under TCG, virtual time may differ.
func calibrateCounterVsRTC(clock device.Clock) {
	console.KPrintf("[Calibrate] comparing counter vs RTC (waiting for second boundary)...\n")

	// Read initial RTC second.
	startSec, _ := clock.Now()

	// Busy-wait for the next second boundary.
	for {
		sec, _ := clock.Now()
		if sec != startSec {
			startSec = sec
			break
		}
	}
	// Snapshot counter at the first second boundary.
	counterAtStart := ktimer.ReadCounter()

	// Now sample both clocks 1000 times while waiting for the next second.
	// This shows us how the counter progresses within a single RTC second.
	type sample struct {
		counter uint64
		rtcSec  uint64
	}
	var samples [10]sample // record 10 evenly-spaced snapshots
	sampleIdx := 0
	nextSampleAt := 100 // sample at iteration 100, 200, ..., 1000
	for i := 1; i <= 1000; i++ {
		c := ktimer.ReadCounter()
		s, _ := clock.Now()
		if i == nextSampleAt && sampleIdx < len(samples) {
			samples[sampleIdx] = sample{counter: c, rtcSec: s}
			sampleIdx++
			nextSampleAt += 100
		}
		// If RTC already ticked to next second, record and break
		if s != startSec {
			break
		}
	}

	// Wait for the RTC to tick to the next second (if the loop above
	// finished all 1000 iterations before the second boundary).
	for {
		sec, _ := clock.Now()
		if sec != startSec {
			break
		}
	}
	counterAtEnd := ktimer.ReadCounter()

	// Compute actual ticks per second.
	ticksPerSec := counterAtEnd - counterAtStart
	reportedFreq := uint64(ktimer.Frequency())

	console.KPrintf("[Calibrate] RTC elapsed: 1 second\n")
	console.KPrintf("[Calibrate] counter ticks: %d (reported CNTFRQ: %d)\n", ticksPerSec, reportedFreq)

	if reportedFreq > 0 {
		// Show ratio: actual/reported. 1.0 means perfect match.
		// Multiply by 1000 to show 3 decimal places as integer math.
		ratioX1000 := (ticksPerSec * 1000) / reportedFreq
		console.KPrintf("[Calibrate] ratio (actual/reported × 1000): %d  (1000 = match)\n", ratioX1000)
	}

	// Print the 10 intermediate samples (counter delta from start, RTC second).
	console.KPrintf("[Calibrate] samples (iter, counter_delta, rtc_sec):\n")
	for i := 0; i < sampleIdx; i++ {
		delta := samples[i].counter - counterAtStart
		console.KPrintf("  [%d] +%d ticks, rtc=%d\n", (i+1)*100, delta, samples[i].rtcSec)
	}
}

// dtbTimerFreqCallback is the DTB walk callback for timer frequency discovery.
// Looks for the /cpus node and reads its timebase-frequency property.
func dtbTimerFreqCallback(node *dtb.DTBNode) {
	var nameBuf [64]byte
	nameLen := node.GetNameBuf(nameBuf[:])
	name := string(nameBuf[:nameLen])
	if name == "cpus" {
		if freq, ok := node.GetPropU32("timebase-frequency"); ok {
			discoveredDTBTimerFreq = freq
		}
	}
}

// initVirtIOGPU initializes the VirtIO GPU device for display output
func initVirtIOGPU() {
	if !gpu.Init() {
		return
	}

}

// initVirtIOInputDevices discovers VirtIO input devices and wires their
// PCI INTx interrupts into the interrupt controller so keypresses generate IRQs.
func initVirtIOInputDevices() {
	input.InitVirtIOInput()

	// Wire each input device's INTx IRQ into the interrupt controller
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
		// ARM64 uses PCI INTx (GIC SPIs 35-38), RISC-V uses PCI INTx (PLIC sources 32-35),
		// AMD64 uses MSI-X (LAPIC vectors). On AMD64, MSI-X bypasses the IOAPIC
		// entirely — the device writes directly to the LAPIC, so we only need
		// to register a no-op handler with kirq (in case DispatchIRQ is called).
		enableDeviceIRQ(irqNum, dev.ISRBase)
	}

	// Wire soft IRQ slot fire callback for future userspace event delivery
	input.SetSoftIRQFireFunc(SoftIRQSlotFire)

	// Register devices with the nosplit top-half handler so it can read
	// events directly from the DMA-mapped used ring without Go allocation.
	for _, dev := range devices {
		if dev == nil {
			continue
		}
		// devType: 0=keyboard, 1=mouse, 2=tablet
		devType := 0
		if dev.DevType == hid.DeviceTypeMouse {
			devType = 1
		} else if dev.DevType == hid.DeviceTypeTablet {
			devType = 2
		}
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
			notifyAddr, evtBufPA, dev.ISRBase, vq.QueueSize, initAvailIdx, devType, &vq.LastUsedIdx)
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
				// Go's runtime mutexes - this makes scheduling fair with shepherds
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

	// Discover platform timer frequency and compute tick thresholds.
	// On RISC-V, reads /cpus/timebase-frequency from DTB.
	// On ARM64/x86_64, uses hardware-calibrated frequency from ktimer.Init().
	// Must be called before EnableTimerIRQ().
	initTimerFrequency()

	// Cache interrupt controller pointer for per-arch IRQ enable/disable
	initInterruptController()

	// Initialize VirtIO Input devices (keyboard, mouse).
	// This also initializes the platform interrupt infrastructure (GICv2m SPI
	// allocator on ARM64) needed for MSI-X configuration of any PCI device.
	initVirtIOInputDevices()

	// Initialize VirtIO block device. For PCI transport, Init() determines
	// the INTx GIC IRQ from the PCI interrupt pin routing (no MSI-X).
	if !block.Init() {
		console.KPrintln("[Main] VirtIO Block init failed (no device found?)")
	}

	// Initialize VirtIO RNG device (entropy source).
	// Uses polling (no IRQ) — RNG requests are infrequent.
	console.KPrintln("[RNG] discovering...")
	if !rng.Init() {
		console.KPrintln("[Main] VirtIO RNG init failed (no device found?)")
	} else {
		// Quick sanity check: read 32 bytes and verify they're not all zero
		console.KPrintln("[RNG] init OK, getting 32 bytes...")
		var rngBuf [32]byte
		n := rng.Get(rngBuf[:])
		allZero := true
		for i := 0; i < n; i++ {
			if rngBuf[i] != 0 {
				allZero = false
				break
			}
		}
		console.KPrintf("[Main] RNG test: got %d bytes, allZero=%v\n", n, allZero)
		console.KPrintf("[Main] RNG data: %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x\n",
			rngBuf[0], rngBuf[1], rngBuf[2], rngBuf[3],
			rngBuf[4], rngBuf[5], rngBuf[6], rngBuf[7],
			rngBuf[8], rngBuf[9], rngBuf[10], rngBuf[11],
			rngBuf[12], rngBuf[13], rngBuf[14], rngBuf[15])
		// Read a second time to verify values differ
		console.KPrintln("[RNG] getting 32 more bytes...")
		var rngBuf2 [32]byte
		n2 := rng.Get(rngBuf2[:])
		same := n == n2
		if same {
			for i := 0; i < n; i++ {
				if rngBuf[i] != rngBuf2[i] {
					same = false
					break
				}
			}
		}
		console.KPrintf("[Main] RNG test2: got %d bytes, sameAsPrev=%v\n", n2, same)
		console.KPrintf("[Main] RNG data2: %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x\n",
			rngBuf2[0], rngBuf2[1], rngBuf2[2], rngBuf2[3],
			rngBuf2[4], rngBuf2[5], rngBuf2[6], rngBuf2[7],
			rngBuf2[8], rngBuf2[9], rngBuf2[10], rngBuf2[11],
			rngBuf2[12], rngBuf2[13], rngBuf2[14], rngBuf2[15])
	}

	// Wire up block device IRQ: register with top-half dispatcher and
	// enable at the interrupt controller so interrupts reach the CPU.
	console.KPrintln("[Main] wiring block IRQ...")
	if irq := block.GetIRQNum(); irq != 0 {
		SetBlockIRQ(irq, block.GetISRBase(), block.GetIOCompletePtr(), block.GetEnginePtr(), block.GetSidecarFreeBitsPtr())
		enableBlockDeviceIRQ(irq)
		// Register async mode callback so doBlockIO (kernel polling path)
		// can temporarily disable async mode during its polling loop.
		block.GetDevice().SetAsyncMode = SetBlockAsyncMode
	}
	console.KPrintln("[Main] block IRQ wired")

	// CRITICAL: Enable IRQs at CPU AFTER GIC is initialized (matches Cardinal's order)
	// This unmasks IRQs at the CPU (clears DAIF.I bit)
	console.KPrintln("[Main] EnableIRQs...")
	EnableIRQs()
	console.KPrintln("[Main] IRQs enabled")
	// Fix clone threads created during Go runtime init — they have IF=0 in
	// their saved RFLAGS because they were cloned before EnableIRQs(). Without
	// this, those threads run without timer interrupts when scheduled, causing
	// the system to freeze if they pick up a non-blocking goroutine.
	FixCloneThreadIFFlags()
	console.KPrintln("[Main] EnableTimerIRQ...")
	EnableTimerIRQ()
	console.KPrintln("[Main] timer enabled, unmapping cardinal...")

	unmapCardinal()
	console.KPrintln("[Main] DisableTimerIRQ...")
	DisableTimerIRQ()
	console.KPrintln("[Main] timer disabled, reading boot config...")

	// =========================================================================
	// USERSPACE TEST: Launch 6 sieve workers as separate threads
	// =========================================================================
	// This tests kernel thread scheduling fairness:
	//   1. Load 6 separate sieve programs (sieve3-9) from FAT32 disk
	//   2. Each runs as a separate OS thread (not goroutines)
	//   3. Kernel scheduler distributes CPU time among them
	//   4. Each prints primes as "ID:prime" (e.g., "3:20011")
	//
	// To switch back to goroutine test, comment out below and launch shepherdsieve.elf

	// DEBUG: ReadMemStats disabled - hangs in bare-metal (triggers STW GC)

	// Parse embedded kernel config (no disk I/O — compiled into the binary).
	kernelCfg := parseKernelConfig()

	// Initialize constraint system and publish kernel attributes before
	// launching shepherds, so they can discover kernel attrs at startup.
	if kmem.InitConstraintPages() && ksyscall.InitKernelAttrManager() {
		ksyscall.PublishKernelAttributes()
		ksyscall.PublishSystemAttributes(GetTotalRAMSize()>>20, GetCPUCount(), GetKernelBudgetMB())
		ksyscall.PublishBootConfigAttributes(kernelCfg.Timezone, kernelCfg.GoMemLimitMB, kernelCfg.GCPercentage)
	}

	if kernelCfg.Timezone != "" {
		ksyscall.SetBootTimezone(kernelCfg.Timezone)
	}
	ksyscall.SetSuppressSerialStdioCopy(kernelCfg.SuppressSerialStdioCopy)
	if kernelCfg.GCPercentage > 0 {
		ksyscall.SetShepherdGCPercentage(kernelCfg.GCPercentage)
	}
	if kernelCfg.GoMemLimitMB > 0 {
		ksyscall.SetShepherdMemLimitMB(kernelCfg.GoMemLimitMB)
	}

	// Launch the embedded fs shepherd from memory — no disk I/O needed.
	// fs reads /startup.toml from disk and launches all [[shepherd]] entries.
	launchEmbeddedFS()

	// Reconfigure timer policy from kernel config.
	// Must happen before EnableTimerIRQ so the first tick uses correct values.
	if kernelCfg.KernelTickRate > 0 || kernelCfg.PreemptAfterTicks > 0 {
		kirq.InitPreemptConfig(kernelCfg.KernelTickRate, kernelCfg.PreemptAfterTicks)
		console.KPrintf("[Timer] reconfigured: tickRate=%dHz (%dms), preempt=%d ticks (%dms)\n",
			kirq.KernelTickRate, kirq.TickIntervalMs, kirq.PreemptAfterTicks, kirq.PreemptIntervalMs)
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

	// Apply kernel memory budget from config (overrides diplomat auxv value).
	if kernelCfg.KernelBudgetMB > 0 {
		kmem.SetKernelBudgetMB(kernelCfg.KernelBudgetMB)
	}

	// Suppress serial echo of userspace stdout/stderr if configured.
	if kernelCfg.SuppressSerialStdioCopy {
		atomic.StoreUint32(&suppressSerial, 1)
	}

	// Suppress kernel console output (KPrintf, etc.) if configured.
	if kernelCfg.SuppressKernelSerial {
		console.SetSuppressed(true)
	}

	// Initialize kernel worker goroutines. Must be done
	// before KernelIdleLoop since the workers need normal goroutine stacks.
	initLoadMazWorker()
	initRunMazWorker()
	initRunShepherdWorker()
	initEpollWorker()

	// Start kernel attribute updaters (time update goroutine).
	// Time updates are driven by kirq.TimerIRQCount + kirq.PreemptAfterTicks.
	ksyscall.StartKernelAttrUpdaters()
	SetupTopHalfTimeUpdate()

	// Enter the kernel idle loop. Thread 0 (m0/g0) stays alive as a normal
	// scheduled thread. Shepherd threads are already running. The timer IRQ
	// preempts thread 0 and context-switches to shepherd threads naturally.
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

// parseKernelConfig parses the embedded kernel.toml (compiled into the binary).
// Always succeeds — returns a zero-value config if parsing fails.
func parseKernelConfig() constants.KernelConfig {
	var cfg constants.KernelConfig
	err := toml.Unmarshal(EmbeddedKernelConfig, &cfg)
	if err != nil {
		console.KPrintf("[boot] kernel.toml parse error: %v\n", err)
		return cfg
	}
	console.KPrintf("[boot] kernel config: tz=%s budget=%dMB tick=%dHz preempt=%d\n",
		cfg.Timezone, cfg.KernelBudgetMB, cfg.KernelTickRate, cfg.PreemptAfterTicks)
	return cfg
}

// launchEmbeddedFS launches the fs shepherd from the go:embed'd ELF data.
// This eliminates the circular bootstrap dependency — fs no longer needs to
// be loaded from the filesystem it implements.
func launchEmbeddedFS() {
	console.KPrintf("[boot] launching embedded fs (%d bytes)\n", len(EmbeddedFSElf))
	result := ksyscall.LaunchFromMemory(EmbeddedFSElf, "fs")
	if result == 0 {
		kmem.FinalUserspaceSync()
		console.KPrintln("[boot] embedded fs launched")
	} else {
		console.KPrintf("[boot] embedded fs launch FAILED (error %d)\n", result)
	}
}
