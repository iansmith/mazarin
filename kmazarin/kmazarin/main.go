package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/ktime"
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
// Removed //go:nosplit - not needed (page fault handler proves this)
//go:noinline
func syscallDispatchInternal(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return ksyscall.DispatchSyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
}

// IRQDispatch is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls irqDispatchInternal. This is the actual implementation.
//
// Removed //go:nosplit - not needed (page fault handler proves this)
//go:noinline
func irqDispatchInternal(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	info := kirq.DispatchIRQ(irqNum, framePtr, elr, spEl0)
	return info.NewELR, info.NewSP, info.NewLR, info.DoPreempt
}

// timerIRQHandlerInternal is called directly from exception handler for timer IRQs
//go:noinline
func timerIRQHandlerInternal(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	info := kirq.TimerIRQHandlerCanPreempt(irqNum, framePtr, elr, spEl0)
	return info.NewELR, info.NewSP, info.NewLR, info.DoPreempt
}

// HandlePageFaultAsm is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls handlePageFaultInternal. This is the actual implementation.
// Returns 1 if the fault was handled successfully, 0 otherwise.
//
// Note: We don't use nosplit here because HandlePageFault needs a full call chain.
// The exception handler runs on SP_EL1 (exception stack) which has 16KB available.
//
//go:noinline
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
	Print("[Init] Initialization complete")
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
	thread.Context.X[0] = 0xDEADBEEF
	if thread.State != ThreadReady || thread.TID != 999 || thread.Context.X[0] != 0xDEADBEEF {
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

// DisableTimerIRQ disables BOTH the timer hardware and its IRQ at the GIC.
// This is necessary because disabling just the IRQ masks delivery but doesn't
// stop the timer from generating interrupt requests.
func DisableTimerIRQ() {
	// CRITICAL: Disable the timer hardware FIRST
	// This stops the timer from generating interrupt requests
	DisableTimerHardware()

	// Then disable the IRQ at the GIC to mask any pending requests
	if gic, ok := device.GetInterruptController(); ok {
		gic.DisableIRQ(27)
	}
}

// printHex32 prints a 32-bit hex value directly to UART
//go:nosplit
func printHex32(uartBase uintptr, val uint32) {
	hexChars := "0123456789ABCDEF"
	for i := 28; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		*(*uint32)(unsafe.Pointer(uartBase)) = uint32(hexChars[nibble])
	}
}

// printTimerDebug prints comprehensive timer and GIC debug info
func printTimerDebug() {
	const uartBaseAddr = uintptr(0xFFFFFFFF09000000)
	const gicDist = 0x08000000

	// Print header
	str := "\r\n=== TIMER DEBUG ===\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Read timer registers
	ctl := ReadCntvCtlEl0()
	tval := ReadCntvTvalEl0()
	cval := ReadCntvctEl0()
	freq := ReadCntfrqEl0()
	daif := ReadDAIF()

	// Print CNTV_CTL_EL0
	str = "CNTV_CTL_EL0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, uint32(ctl))
	str = " (enable="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32(ctl&1)
	str = " mask="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((ctl>>1)&1)
	str = " status="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((ctl>>2)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Print CNTV_TVAL_EL0 (signed!)
	str = "CNTV_TVAL_EL0: "
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	if (tval & 0x80000000) != 0 {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '-'
		printHex32(uartBaseAddr, uint32(-int32(tval)))
	} else {
		printHex32(uartBaseAddr, uint32(tval))
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\n'

	// Print CNTVCT_EL0
	str = "CNTVCT_EL0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex(cval)
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\n'

	// Print CNTFRQ_EL0
	str = "CNTFRQ_EL0: "
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, uint32(freq))
	str = " Hz\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Print DAIF
	str = "DAIF: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, uint32(daif))
	str = " (I="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((daif>>7)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Read GIC registers for IRQ 27
	// GICD_ISENABLER0 (0x08000100) - bit 27 should be 1
	isenabler := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x100)))
	str = "GICD_ISENABLER0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, isenabler)
	str = " (bit27="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((isenabler>>27)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// GICD_ISPENDR0 (0x08000200) - bit 27 shows if interrupt is pending
	ispendr := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x200)))
	str = "GICD_ISPENDR0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, ispendr)
	str = " (bit27="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((ispendr>>27)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// GICD_ISACTIVER0 (0x08000300) - bit 27 shows if interrupt is active
	isactiver := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x300)))
	str = "GICD_ISACTIVER0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, isactiver)
	str = " (bit27="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((isactiver>>27)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Check IRQ 27 priority (GICD_IPRIORITYR6 byte 3)
	// IRQ 27 = priority register 27/4 = 6, byte offset 27%4 = 3
	ipriority6 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x400 + 24))) // Register 6 (0x418)
	str = "GICD_IPRIORITYR6: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, ipriority6)
	str = " (IRQ27 pri="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	priority27 := (ipriority6 >> 24) & 0xFF
	printHex32(uartBaseAddr, priority27)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Check IRQ 27 target (GICD_ITARGETSR6 byte 3)
	// IRQ 27 = target register 27/4 = 6, byte offset 27%4 = 3
	itargets6 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x800 + 24))) // Register 6 (0x818)
	str = "GICD_ITARGETSR6: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, itargets6)
	str = " (IRQ27 cpu="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	target27 := (itargets6 >> 24) & 0xFF
	printHex32(uartBaseAddr, target27)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	// Check GIC CPU interface priority mask
	const gicCpu = 0x08010000
	giccPmr := *(*uint32)(unsafe.Pointer(uintptr(gicCpu + 0x004)))
	str = "GICC_PMR: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, giccPmr)
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\n'

	// Check GICD_CTLR (distributor control)
	gicdCtlr := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x000)))
	str = "GICD_CTLR: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, gicdCtlr)
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\n'

	// Check GICC_CTLR (CPU interface control)
	giccCtlr := *(*uint32)(unsafe.Pointer(uintptr(gicCpu + 0x000)))
	str = "GICC_CTLR: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, giccCtlr)
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '\n'

	// Check GICD_IGROUPR0 (interrupt group register - Group 0 vs Group 1)
	// IRQ 27 is bit 27 of IGROUPR0
	igroupr0 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x080)))
	str = "GICD_IGROUPR0: 0x"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	printHex32(uartBaseAddr, igroupr0)
	str = " (IRQ27 grp="
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
	*(*uint32)(unsafe.Pointer(uartBaseAddr)) = '0' + uint32((igroupr0>>27)&1)
	str = ")\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}

	str = "==================\r\n"
	for i := 0; i < len(str); i++ {
		*(*uint32)(unsafe.Pointer(uartBaseAddr)) = uint32(str[i])
	}
}

// EnableTimerIRQ enables the timer IRQ (27) using the GIC device driver.
func EnableTimerIRQ() {
	if gic, ok := device.GetInterruptController(); ok {
		gic.EnableIRQ(27)
		// Start the timer hardware
		RearmTimerNow()
	}
}

// testDeviceDiscovery tests the DTB-based device discovery system
// This is a temporary test function to verify DTB parsing and device matching
func testDeviceDiscovery() {
	console.KPrintln("")
	console.KPrintln("[DeviceTest] === Testing DTB Device Discovery ===")

	// NOTE: Drivers are already registered in EarlyInit()

	// Get DTB address from runtime config
	cfg := GetRuntimeConfig()
	if cfg == nil {
		console.KPrintln("[DeviceTest] ERROR: RuntimeConfig not available")
		return
	}

	// Use physical address - DTB is in low memory which is still mapped
	dtbAddr := uintptr(cfg.DtbPhysAddr)
	console.KWriteString("[DeviceTest] DTB physical address: ")
	console.KPrintHex64(cfg.DtbPhysAddr)
	console.KPrintln("")

	// Register all device drivers BEFORE discovering devices
	device.RegisterAllDrivers()

	// Parse DTB and discover devices (silent - no printing inside)
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		console.KWriteString("[DeviceTest] ERROR: ")
		console.KPrintln(err.Error())
		return
	}

	// Show what was discovered
	console.KPrintln("")
	console.KPrintln("[DeviceTest] Discovered devices:")

	// Check for byte streams (UART)
	if uart, ok := device.GetByteStream(); ok {
		console.KWriteString("  - ByteStream: ")
		console.KPrintln(uart.Name())
	}

	// Check for interrupt controller (GIC)
	if gic, ok := device.GetInterruptController(); ok {
		console.KWriteString("  - InterruptController: ")
		console.KPrintln(gic.Name())
	}

	// Check for random source (VirtIO RNG)
	if rng, ok := device.GetRandomSource(); ok {
		console.KWriteString("  - RandomSource: ")
		console.KPrintln(rng.Name())
	}

	// Check for clock (RTC)
	if clk, ok := device.GetClock(); ok {
		console.KWriteString("  - Clock: ")
		console.KPrintln(clk.Name())

		// Initialize kernel time subsystem (reads RTC once, caches for tick-based derivation)
		if ktime.Init() {
			console.KPrintln("  - ktime: initialized (RTC cached)")
		} else {
			console.KPrintln("  - ktime: ERROR: initialization failed")
		}
	}

	// Check for block devices
	if blk, ok := device.GetBlockDevice(); ok {
		console.KWriteString("  - BlockDevice: ")
		console.KPrintln(blk.Name())
	}

	// Wire up interrupts now that GIC is discovered
	console.KPrintln("")
	console.KPrintln("[DeviceTest] Wiring interrupts...")
	if err := device.WireInterrupts(); err != nil {
		console.KWriteString("[DeviceTest] ERROR wiring interrupts: ")
		console.KPrintln(err.Error())
	} else {
		console.KPrintln("[DeviceTest] Interrupts wired successfully")

		// NOTE: Timer IRQ remains enabled to allow goroutine preemption.
		// The event poller needs to run to dispatch UART interrupts.
		console.KPrintln("[DeviceTest] Timer IRQ remains enabled for scheduler preemption")

		// Switch to interrupt-driven PL011 console
		if bs, ok := device.GetByteStream(); ok {
			// Type assert to *uart.PL011 to access AsConsole()
			if _, ok := bs.(*uart.PL011); ok {
				console.KPrintln("[DeviceTest] PL011 available but keeping polling console for stability")
				// DISABLED: Switching to interrupt-driven console causes crash
				// console.Set(pl011.AsConsole())
				// console.KPrintln("[DeviceTest] Now using PL011 interrupt-driven console!")
			}
		}
	}

	console.KPrintln("[DeviceTest] === Test Complete ===")

	// Test KPrintHex() with various types
	testKPrintHex()
}

// initVirtIOGPU initializes the VirtIO GPU device for display output
func initVirtIOGPU() {
	console.KPrintln("")
	console.KPrintln("[VirtIO GPU] === Initializing Display ===")

	if !gpu.Init() {
		console.KPrintln("[VirtIO GPU] ERROR: Initialization failed")
		console.KPrintln("[VirtIO GPU] Continuing without display")
		return
	}

	console.KPrintln("[VirtIO GPU] Display ready")

	// Render boot image if available
	config := GetRuntimeConfig()
	if config.BootImagePhysAddr != 0 && config.BootImageSize > 0 {
		console.KPrintf("[VirtIO GPU] Boot image at 0x%x, size %d\n", config.BootImagePhysAddr, config.BootImageSize)
		if gpu.RenderBootImage(uintptr(config.BootImagePhysAddr), config.BootImageSize) {
			// Transfer and flush the rendered image to display
			gpu.UpdateDisplay(0, 0, gpu.GetWidth(), gpu.GetHeight())
			console.KPrintln("[VirtIO GPU] Boot image displayed")
		}
	} else {
		console.KPrintln("[VirtIO GPU] No boot image available")
	}
}

// testKPrintHex tests the KPrintHex() method with various value types
func testKPrintHex() {
	console.KPrintln("")
	console.KPrintln("[HexTest] === Testing KPrintHex() ===")

	// Test uint8
	var u8 uint8 = 0xAB
	console.KWriteString("[HexTest] uint8(0xAB): ")
	console.KPrintHex(u8)
	console.KPrintln("")

	// Test uint16
	var u16 uint16 = 0x1234
	console.KWriteString("[HexTest] uint16(0x1234): ")
	console.KPrintHex(u16)
	console.KPrintln("")

	// Test uint32
	var u32 uint32 = 0x12345678
	console.KWriteString("[HexTest] uint32(0x12345678): ")
	console.KPrintHex(u32)
	console.KPrintln("")

	// Test uint64
	var u64 uint64 = 0x123456789ABCDEF0
	console.KWriteString("[HexTest] uint64(0x123456789ABCDEF0): ")
	console.KPrintHex(u64)
	console.KPrintln("")

	// Test uintptr
	var uptr uintptr = 0xFFFFFFFF41800000
	console.KWriteString("[HexTest] uintptr(0xFFFFFFFF41800000): ")
	console.KPrintHex(uptr)
	console.KPrintln("")

	// Test pointer
	testVar := uint32(42)
	testPtr := &testVar
	console.KWriteString("[HexTest] pointer(&testVar): ")
	console.KPrintHex(testPtr)
	console.KPrintln("")

	// Test string (should print "not hex value")
	testStr := "hello"
	console.KWriteString("[HexTest] string(\"hello\"): ")
	console.KPrintHex(testStr)
	console.KPrintln("")

	console.KPrintln("[HexTest] === Test Complete ===")
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
func simpleMain() {
	Print("[Main] Kmazarin kernel starting...")

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		Print("[Main] Runtime ready")

		// Run scheduler tests
		ManualSchedulerTest()

		// CRITICAL: Disable automatic GC.
		// We cannot call runtime.GC() because it triggers taggedPointerPack errors -
		// the Go runtime's tagged pointer code assumes 49-bit addresses but our
		// kernel uses full 64-bit TTBR1 addresses (0xFFFF...).
		// The "bad sweepgen in refill" error occurs when GC state becomes inconsistent.
		// For now, we disable GC entirely to avoid both issues.
		// TODO: Patch tagptr_64bit.go to handle TTBR1 addresses, then we can enable GC.
		debug.SetGCPercent(-1)
		Print("[Main] GC disabled")

		InitDeadlineQueue()
		// Note: readyQueue uses static allocation with zero value - no init needed

		// Initialize soft IRQ dispatcher
		// TEMPORARILY DISABLED: The dispatcher blocks the kernel thread when it
		// calls ThreadBlockSoftIRQ, which stops everything. It should be started
		// as a separate kernel thread, not a goroutine on M0.
		InitSoftIRQDispatcher()
		// go SoftIRQDispatcher()
		Print("[Main] Soft IRQ dispatcher initialized (goroutine disabled)")
	} else {
		Print("[Main] Runtime not ready - continuing with direct UART")
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

	// Initialize VirtIO GPU for display output
	initVirtIOGPU()

	// CRITICAL: Enable IRQs at CPU AFTER GIC is initialized (matches Cardinal's order)
	// This unmasks IRQs at the CPU (clears DAIF.I bit)
	EnableIRQs()
	Print("[Main] IRQs enabled at CPU")

	// NOW enable timer IRQ for async preemption (after GIC is initialized)
	EnableTimerIRQ()

	asyncPreemptAddr := GetAsyncPreemptAddr()
	kirq.SetAsyncPreemptAddr(asyncPreemptAddr)
	readyForAsyncPreempt.Store(1)
	kirq.SetReadyForAsyncPreempt()
	Print("[Main] Async preemption enabled")

	// Unmap Cardinal at L1 level - zeros L1[1-2] (1-3GB) while preserving L1[0] for MMIO and L1[256+] for heap
	unmapCardinal()

	// Temporarily disable timer IRQ during priest launch to avoid interrupt interference
	DisableTimerIRQ()
	Print("[Main] Timer IRQ disabled for priest launch")

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

	// DEBUG: Print GC stats before launching sievetest
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	console.KPrintf("[Main] GC debug: NumGC=%d HeapAlloc=%d NextGC=%d GCPercent=%d\n",
		memStats.NumGC, memStats.HeapAlloc, memStats.NextGC, debug.SetGCPercent(-1))

	// Launch priestsieve - single priest with multiple goroutines (GOMAXPROCS=1)
	// This tests goroutine-level preemption within a single priest.
	Print("[Main] Launching priestsieve.elf (single priest, 6 goroutines)...\r\n")
	filename := "/priestsieve.elf\x00"
	filenamePtr := uintptr(unsafe.Pointer(&([]byte(filename))[0]))
	result := ksyscall.SyscallLaunch(uint64(filenamePtr), 0, 0, 0, 0, 0)
	if result != 0 {
		console.KPrintf("[Main] ERROR: priestsieve launch failed with code %d\n", result)
	} else {
		console.KPrintf("[Main] Launched priestsieve successfully\n")
	}

	Print("[Main] priestsieve launched - testing goroutine scheduling")

	// Re-enable IRQs and timer for priest scheduling
	EnableIRQs()
	EnableTimerIRQ()

	// Debug: Print DAIF to verify IRQs are enabled
	daif := ReadDAIF()
	console.KPrintf("[Main] DAIF before idle: 0x%x (I=0x80 clear means IRQs enabled)\n", daif)

	Print("[Main] Starting first thread from ready queue...\r\n")

	// Start kernel time accounting for performance measurements
	kirq.StartKernelTimeAccounting()

	// Start the first thread - this function never returns!
	// It waits for a ready thread, then switches to it via ERET.
	// The timer IRQ will preempt threads and allow others to run.
	RunFirstThread()

	// Should never reach here
	Print("[Main] ERROR: RunFirstThread returned!\r\n")
	for {
	}
}

func main() {
	simpleMain()
}
