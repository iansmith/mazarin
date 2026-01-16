package main

import (
	"fmt"
	"kmazarin/device"
	//  "kmazarin/dtb"
	"kmazarin/kirq"
	"kmazarin/kmem"
	"kmazarin/ksyscall"
	_ "os" // Keep to maintain BSS size
	"runtime"
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


// init runs before main - called after Go runtime is fully initialized
// Set up exception handlers and enable interrupts
func init() {
	// NOTE: Runtime config is already initialized in runtime_config.go:init()
	// which runs at package load time, before the Go runtime initializes.

	// NOTE: Syscall table is initialized at package level, before runtime init
	// NOTE: VBAR_EL1 is already set by Cardinal before jumping to kmazarin
	// This must happen before the Go runtime initializes (which runs before init())
	// because runtime init calls mmap() syscalls that need kmazarin's handlers.

	// CRITICAL: Call GetExceptionVectorBase() to force linker to include exception vector table
	// Without this, the linker removes ExceptionVectorTable as dead code!
	vectorAddr := GetExceptionVectorBase()
	_ = vectorAddr // Suppress unused warning

	// CRITICAL: Reference G0Struct to force linker to include it
	// This buffer is where Cardinal copies the g0 goroutine struct for kmazarin to use.
	// Without this reference, the linker removes G0Struct as dead code!
	g0StructAddr := GetG0StructAddr()
	_ = g0StructAddr // Suppress unused warning

	// Initialize thread table - M0 is thread 0
	// MUST happen before any clone syscalls!
	InitThreads()

	// Initialize critical early devices (UART, GIC, Timer, RNG)
	EarlyInit()

	// Initialize timer, IRQ handlers, and preemption subsystem
	kirq.InitTimer()
	kirq.RegisterHandlers()
	kirq.InitPreemption()

	// Enable interrupts - handlers are ready
	EnableIRQs()
	Print("[Init] Initialization complete")
}

// uartPutc writes a single character to UART
// Before interrupt mode: direct UART write
// After interrupt mode: uses ring buffer with TX interrupt
//go:nosplit
func uartPutc(c byte) {
	// Check if interrupt-driven UART is ready
	if uartInterruptReady {
		// Use interrupt-driven ring buffer
		kirq.UARTPutc(c)
	} else {
		// Direct UART write for early boot
		uartBase := GetUartBase() // Read directly from StartupParams
		*(*byte)(unsafe.Pointer(uartBase)) = c
	}
}

// uartInterruptReady tracks whether interrupt-driven UART is ready
var uartInterruptReady bool

// uartPuts writes a string directly to UART
//go:nosplit
func uartPuts(s string) {
	for i := 0; i < len(s); i++ {
		uartPutc(s[i])
	}
}

// uartPutsDirect writes a string directly to UART (alias for uartPuts)
// Used by ksyscall and kthread packages via linkname
//go:linkname uartPutsDirect kmazarin/ksyscall.uartPutsDirect
//go:nosplit
func uartPutsDirect(s string) {
	uartPuts(s)
}

// uartPutcDirectForKmem writes a byte directly to UART
// Used by kmem package via linkname
//go:linkname uartPutcDirectForKmem kmazarin/kmem.uartPutcDirect
//go:nosplit
func uartPutcDirectForKmem(c byte) {
	uartPutc(c)
}

// getRuntimeConfigForKmem provides runtime config to kmem package via linkname
//go:linkname getRuntimeConfigForKmem kmazarin/kmem.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKmem() interface{} {
	return GetRuntimeConfig()
}

// getUartBaseForKsyscall provides UART base to ksyscall package via linkname
//go:linkname getUartBaseForKsyscall kmazarin/ksyscall.getUartBase
//go:nosplit
func getUartBaseForKsyscall() uintptr {
	return GetUartBase()
}

// getUartBaseForKirq provides UART base to kirq package via linkname
//go:linkname getUartBaseForKirq kmazarin/kirq.getUartBase
//go:nosplit
func getUartBaseForKirq() uintptr {
	return GetUartBase()
}

// getAsyncPreemptAddrForKirq provides asyncPreempt address to kirq package via linkname
//go:linkname getAsyncPreemptAddrForKirq kmazarin/kirq.getAsyncPreemptAddr
//go:nosplit
func getAsyncPreemptAddrForKirq() uintptr {
	return GetAsyncPreemptAddr()
}

// getReadyForAsyncPreemptAddrForKirq provides readyForAsyncPreempt flag address to kirq package via linkname
//go:linkname getReadyForAsyncPreemptAddrForKirq kmazarin/kirq.getReadyForAsyncPreemptAddr
//go:nosplit
func getReadyForAsyncPreemptAddrForKirq() uintptr {
	return GetReadyForAsyncPreemptAddr()
}

// processDeadlinesForKirq provides deadline processing to kirq package via linkname
//go:linkname processDeadlinesForKirq kmazarin/kirq.processDeadlines
//go:nosplit
func processDeadlinesForKirq() {
	ProcessDeadlines()
}

// getRuntimeConfigForKsyscall provides runtime config to ksyscall package via linkname
//go:linkname getRuntimeConfigForKsyscall kmazarin/ksyscall.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKsyscall() interface{} {
	return GetRuntimeConfig()
}

// uartPutHex64Direct writes a 64-bit hex value to UART
// Used by ksyscall, kthread, and kmem packages via linkname
//go:linkname uartPutHex64Direct kmazarin/ksyscall.uartPutHex64Direct
//go:linkname uartPutHex64DirectForKmem kmazarin/kmem.uartPutHex64Direct
//go:nosplit
func uartPutHex64Direct(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
}

// Alias for kmem package linkname
//go:nosplit
func uartPutHex64DirectForKmem(val uint64) {
	uartPutHex64Direct(val)
}

// uartPutHex32Direct writes a 32-bit hex value to UART
// Used by ksyscall and kthread packages via linkname
//go:linkname uartPutHex32Direct kmazarin/ksyscall.uartPutHex32Direct
//go:nosplit
func uartPutHex32Direct(val uint32) {
	hexChars := "0123456789ABCDEF"
	for i := 28; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
}

// Runtime readiness flag - set to true once we verify runtime is fully initialized
var runtimeReady = false

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
		threadSlice[i].TID = int32(100 + i)
	}
	for i := 0; i < 4; i++ {
		if threadSlice[i].TID != int32(100+i) {
			return false
		}
	}

	return true
}

// testDeviceDiscovery tests the DTB-based device discovery system
// This is a temporary test function to verify DTB parsing and device matching
func testDeviceDiscovery() {
	fmt.Println("\n[DeviceTest] === Testing DTB Device Discovery ===")

	// Register all device drivers
	device.RegisterAllDrivers()

	// Get DTB address from runtime config
	cfg := GetRuntimeConfig()
	if cfg == nil {
		fmt.Println("[DeviceTest] ERROR: RuntimeConfig not available")
		return
	}

	// Use physical address - DTB is in low memory which is still mapped
	dtbAddr := uintptr(cfg.DtbPhysAddr)
	fmt.Printf("[DeviceTest] DTB physical address: 0x%X\n", dtbAddr)

	// Parse DTB and discover devices
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		fmt.Printf("[DeviceTest] ERROR: %v\n", err)
		return
	}

	// Show what was discovered
	fmt.Println("\n[DeviceTest] Discovered devices:")

	// Check for byte streams (UART)
	if uart, ok := device.GetByteStream(); ok {
		fmt.Printf("  - ByteStream: %s\n", uart.Name())
	}

	// Check for interrupt controller (GIC)
	if gic, ok := device.GetInterruptController(); ok {
		fmt.Printf("  - InterruptController: %s\n", gic.Name())
	}

	// Check for random source (VirtIO RNG)
	if rng, ok := device.GetRandomSource(); ok {
		fmt.Printf("  - RandomSource: %s\n", rng.Name())
	}

	// Check for block devices
	if blk, ok := device.GetBlockDevice(); ok {
		fmt.Printf("  - BlockDevice: %s\n", blk.Name())
	}

	fmt.Println("[DeviceTest] === Test Complete ===\n")
}

// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
func simpleMain() {
	Print("[Main] Kmazarin kernel starting...")

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		Print("[Main] Runtime ready")
		InitDeadlineQueue()
		InitReadyQueue()
	} else {
		Print("[Main] Runtime not ready - continuing with direct UART")
	}

	// Test DTB-based device discovery BEFORE unmapping Cardinal
	// (DTB is at 0x40000000 in Cardinal's memory region)
	testDeviceDiscovery()

	// Unmap Cardinal at L1 level - zeros L1[1-2] (1-3GB) while preserving L1[0] for MMIO and L1[256+] for heap
	unmapCardinal()

	// Launch second goroutine for preemption test
	Print("[Main] Starting preemption test...")
	go simpleGoroutine2(nil)

	// Enable async preemption
	EnableTimerIRQ()
	asyncPreemptAddr := GetAsyncPreemptAddr()
	kirq.SetAsyncPreemptAddr(asyncPreemptAddr)
	readyForAsyncPreempt.Store(1)
	kirq.SetReadyForAsyncPreempt()
	Print("[Main] Async preemption enabled")

	// Infinite busy-wait loop, printing '1' periodically
	// NO calls to Gosched() - relies purely on timer-based preemption
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				// Emit newline every 72 prints
				uartPutc('\n')
			} else {
				// Print '1' to show g1 is running (direct UART, no runtime)
				uartPutc('1')
			}
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

// simpleGoroutine2 is the second goroutine for the preemption test
// Pure busy-wait with NO cooperative yielding
func simpleGoroutine2(ch chan string) {
	// Infinite busy-wait loop to test timer-based preemption
	// NO calls to Gosched() - the timer interrupt must forcibly preempt us
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				// Emit newline every 72 prints
				uartPutc('\n')
			} else {
				// Print '2' to show g2 is running (direct UART, no runtime)
				uartPutc('2')
			}
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

func main() {
	simpleMain()
}
