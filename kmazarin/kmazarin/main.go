package main

import (
	"fmt"
	arm64gic "mazzy/kmazarin/arch/arm64/gic"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/shared/fs/fat32"
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
// DisableTimerIRQ disables the timer IRQ using the cached GIC pointer.
//
//go:nosplit
func DisableTimerIRQ() {
	DisableTimerHardware()
	if cachedGIC != nil {
		cachedGIC.DisableIRQ(27)
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
//
// cachedGIC holds a direct reference to the GIC, avoiding interface dispatch
// (which can trigger morestack) in hot paths like timer enable/disable.
var cachedGIC *arm64gic.GICv2

// initCachedGIC must be called once during boot after device discovery.
func initCachedGIC() {
	if gic, ok := device.GetInterruptController(); ok {
		if g, ok := gic.(*arm64gic.GICv2); ok {
			cachedGIC = g
		}
	}
}

// EnableTimerIRQ enables the timer IRQ (27) using the cached GIC pointer.
//
//go:nosplit
func EnableTimerIRQ() {
	if cachedGIC != nil {
		// CRITICAL: Rearm timer BEFORE enabling GIC IRQ.
		// IRQ 27 is edge-triggered. If the timer line is already asserted
		// (ISTATUS=1 from a previous expiration) when we enable the GIC,
		// there's no rising edge and the GIC never generates the interrupt.
		// Rearming first clears ISTATUS (line goes low), then after the GIC
		// enable, the next expiration creates a proper rising edge.
		RearmTimerNow()
		cachedGIC.EnableIRQ(27)
	}
}

// testDeviceDiscovery tests the DTB-based device discovery system
// This is a temporary test function to verify DTB parsing and device matching
func testDeviceDiscovery() {
	console.KPrintln("")
	// NOTE: Drivers are already registered in EarlyInit()

	// Get DTB address from startup params (correct offset)
	dtbPhysAddr := GetDtbPhysAddr()

	// Use physical address - DTB is in low memory which is still mapped
	dtbAddr := uintptr(dtbPhysAddr)

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

	// Wire up interrupts now that GIC is discovered
	if err := device.WireInterrupts(); err != nil {
		console.KWriteString("[DeviceTest] ERROR wiring interrupts: ")
		console.KPrintln(err.Error())
	} else {
		// Set up UART soft IRQ hook for userspace serial RX
		if bs, ok := device.GetByteStream(); ok {
			if pl011, ok := bs.(*uart.PL011); ok {
				SetupUartSoftIRQ(pl011.IRQ())
			}
		}
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
		if cachedGIC != nil {
			// Capture irqNum for closure (Go 1.22+ loop var semantics make this safe,
			// but be explicit for clarity)
			localIRQ := irqNum
			// Register handler: when this IRQ fires, dispatch to input driver
			cachedGIC.RegisterHandler(localIRQ, func() {
				input.DispatchIRQ(uint64(localIRQ))
			})
			// MSI-X interrupts are edge-triggered (message write = edge event)
			cachedGIC.SetIRQEdgeTriggered(localIRQ)
			// Set priority to 0xA0 (lower than timer at 0x00, higher than nothing)
			cachedGIC.SetIRQPriority(localIRQ, 0xA0)
			// Route to CPU 0 (required — bulk SPI init may not cover MSI-X IRQs)
			cachedGIC.SetIRQTarget(localIRQ, 0x01)
			cachedGIC.EnableIRQ(localIRQ)
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
			notifyAddr, evtBufPA, vq.QueueSize, initAvailIdx, isMouse)
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
func simpleMain() {
	Print("[Main] Kmazarin kernel starting...")

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		Print("[Main] Runtime ready")

		debug.SetGCPercent(-1)
		InitDeadlineQueue()
		InitSoftIRQDispatcher()
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

	// Cache GIC pointer for nosplit-safe timer IRQ enable/disable
	initCachedGIC()

	// Initialize VirtIO GPU for display output
	initVirtIOGPU()

	// Initialize VirtIO Input devices (keyboard, mouse)
	initVirtIOInputDevices()

	// CRITICAL: Enable IRQs at CPU AFTER GIC is initialized (matches Cardinal's order)
	// This unmasks IRQs at the CPU (clears DAIF.I bit)
	EnableIRQs()
	EnableTimerIRQ()

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

	// Launch dapope (input event handler priest)
	dapopeName := "/dapope.elf\x00"
	dapopePtr := uintptr(unsafe.Pointer(&([]byte(dapopeName))[0]))
	result := ksyscall.SyscallLaunch(uint64(dapopePtr), 0, 0, 0, 0, 0)
	if result == 0 {
		kmem.FinalUserspaceSync()
		Print("[main] dapope launched")
	} else {
		Print("[main] dapope launch failed")
	}

	// Launch stdio (console priest — serial port + display)
	stdioName := "/stdio.elf\x00"
	stdioPtr := uintptr(unsafe.Pointer(&([]byte(stdioName))[0]))
	result = ksyscall.SyscallLaunch(uint64(stdioPtr), 0, 0, 0, 0, 0)
	if result == 0 {
		kmem.FinalUserspaceSync()
		Print("[main] stdio launched")
	} else {
		Print("[main] stdio launch failed")
	}

	// Re-enable IRQs and timer for ongoing scheduling
	EnableIRQs()
	EnableTimerIRQ()

	// Timer and IRQs verified working at this point

	// Start kernel time accounting for performance measurements
	kirq.StartKernelTimeAccounting()

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
