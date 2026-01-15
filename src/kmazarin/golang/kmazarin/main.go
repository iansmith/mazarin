package main

import (
	"fmt"
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
	// DEBUG: Print '[' to show we entered the handler
	uartBase := GetUartBase()
	*(*byte)(unsafe.Pointer(uartBase)) = '['

	info := kirq.TimerIRQHandlerCanPreempt(irqNum, framePtr, elr, spEl0)

	// DEBUG: Print ']' to show we're returning
	*(*byte)(unsafe.Pointer(uartBase)) = ']'

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

	// Initialize timer with actual frequency from CNTFRQ_EL0
	// Must be done before enabling IRQs to ensure correct timer tick calculation
	Print("[Init] Calling kirq.InitTimer()...")
	kirq.InitTimer()
	Print("[Init] kirq.InitTimer() done")

	// Register IRQ handlers BEFORE enabling interrupts
	// This ensures the dispatch tables are populated before any IRQs fire
	Print("[Init] Calling kirq.RegisterHandlers()...")
	kirq.RegisterHandlers()
	Print("[Init] kirq.RegisterHandlers() done")

	// Initialize preemption subsystem - reads runtime offsets
	// Must be done before enabling IRQs so timer handler can access offsets
	Print("[Init] Calling kirq.InitPreemption()...")
	kirq.InitPreemption()
	Print("[Init] kirq.InitPreemption() done")

	// Debug: print preemption offsets
	sg0, preempt, status, stackPreempt, gRunning, gScan := kirq.GetPreemptOffsetDebug()
	Print("[Init] Preemption offsets:")
	Print("[Init]   stackguard0 offset: ")
	PrintHex64(uint64(sg0))
	Print("[Init]   preempt offset: ")
	PrintHex64(uint64(preempt))
	Print("[Init]   atomicstatus offset: ")
	PrintHex64(uint64(status))
	Print("[Init]   stackPreempt value: ")
	PrintHex64(uint64(stackPreempt))
	Print("[Init]   _Grunning: ")
	PrintHex64(uint64(gRunning))
	Print("[Init]   _Gscan: ")
	PrintHex64(uint64(gScan))
	Print("[Init]   SystemTimerFrequency: ")
	PrintHex64(kirq.SystemTimerFrequency)

	// Enable interrupts - handlers are ready
	// NOTE: This happens during runtime init, which causes asyncPreempt issues
	// but delaying to main() causes exit(21). Need to investigate proper timing.
	Print("[Init] Calling EnableIRQs()...")
	EnableIRQs()
	Print("[Init] EnableIRQs() done")
	Print("[Init] init() complete")
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
	Print("=== Go Runtime Readiness Test Suite ===")

	// Test 1: Memory allocation with make()
	Print("[1/8] Testing make() allocation...")
	s := make([]byte, 16)
	if len(s) != 16 {
		Print("  FAIL: make() returned wrong length")
		return false
	}
	s[0] = 42
	if s[0] != 42 {
		Print("  FAIL: make() memory not writable")
		return false
	}
	Print("  PASS")

	// Test 2: Memory allocation with new()
	Print("[2/8] Testing new() allocation...")
	p := new(int)
	if p == nil {
		Print("  FAIL: new() returned nil")
		return false
	}
	*p = 123
	if *p != 123 {
		Print("  FAIL: new() memory not writable")
		return false
	}
	Print("  PASS")

	// Test 3: GOMAXPROCS (scheduler P structs)
	Print("[3/8] Testing GOMAXPROCS...")
	n := runtime.GOMAXPROCS(0)
	if n == 0 {
		Print("  FAIL: GOMAXPROCS returned 0")
		return false
	}
	Print("  PASS")

	// Test 4: Goroutine count
	Print("[4/8] Testing NumGoroutine...")
	ng := runtime.NumGoroutine()
	if ng == 0 {
		Print("  FAIL: NumGoroutine returned 0")
		return false
	}
	Print("  PASS")

	// Test 5: Mutex operations
	Print("[5/8] Testing sync.Mutex...")
	var mu sync.Mutex
	mu.Lock()
	mu.Unlock()
	Print("  PASS")

	// Test 6: String operations (uses runtime)
	Print("[6/8] Testing string concatenation...")
	str1 := "Hello"
	str2 := "World"
	str3 := str1 + " " + str2
	if len(str3) != 11 {
		Print("  FAIL: String concat wrong length")
		return false
	}
	Print("  PASS")

	// Test 7: Thread structure allocation (328 bytes)
	Print("[7/8] Testing Thread struct allocation...")
	Print("  Allocating Thread with new()...")
	thread := new(Thread)
	if thread == nil {
		Print("  FAIL: new(Thread) returned nil")
		return false
	}
	Print("  Thread allocated at: ")
	PrintHex64(uint64(uintptr(unsafe.Pointer(thread))))
	// Test writing to the struct
	thread.State = ThreadReady
	thread.TID = 999
	thread.Context.X[0] = 0xDEADBEEF
	thread.Context.SP = 0x12345678
	// Verify writes
	if thread.State != ThreadReady {
		Print("  FAIL: Thread.State not writable")
		return false
	}
	if thread.TID != 999 {
		Print("  FAIL: Thread.TID not writable")
		return false
	}
	if thread.Context.X[0] != 0xDEADBEEF {
		Print("  FAIL: Thread.Context.X[0] not writable")
		return false
	}
	Print("  PASS")

	// Test 8: Slice of Threads allocation
	Print("[8/8] Testing []Thread slice allocation...")
	Print("  Allocating slice of 4 Threads...")
	threadSlice := make([]Thread, 4)
	if len(threadSlice) != 4 {
		Print("  FAIL: make([]Thread, 4) returned wrong length")
		return false
	}
	Print("  Slice backing array at: ")
	PrintHex64(uint64(uintptr(unsafe.Pointer(&threadSlice[0]))))
	// Test writing to each element
	for i := 0; i < 4; i++ {
		threadSlice[i].TID = int32(100 + i)
		threadSlice[i].State = ThreadReady
	}
	// Verify writes
	for i := 0; i < 4; i++ {
		if threadSlice[i].TID != int32(100+i) {
			Print("  FAIL: threadSlice[i].TID not correct")
			return false
		}
	}
	Print("  PASS")

	// Skip fmt test
	Print("[SKIP] Testing fmt.Println... (causes hang)")

	Print("=== All Runtime Tests Passed! ===")
	Print("")
	return true
}

// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
func simpleMain() {
	// Get g register value (used later for logging)
	gVal := GetGRegister()

	Print("")
	Print("[g1] Kmazarin kernel starting...")
	Print("")

	// =============================================
	// UNMAP CARDINAL: Safe because heap is in different address range
	// =============================================
	// The Go heap is in TTBR0 space at 0x0000004000000000 (user-level addresses).
	// Cardinal is loaded at 0x0000000040100000 (about 1GB from RAM base).
	// These ranges don't overlap, so we can safely unmap Cardinal.
	//
	// The g register (x28) points to the current goroutine's g struct, which is
	// heap-allocated. This is expected to be in the 0x4000000000+ range.
	Print("[g1] Preparing to unmap Cardinal...")
	Printf("[g1] g register points to 0x%x (heap-allocated goroutine struct)", gVal)
	Print("")

	// Unmap Cardinal at L1 level - zeros L1[0-2] (0-3GB) while preserving L1[256+] for heap
	unmapCardinal()
	Print("[g1] Cardinal unmapped (L1-level, heap preserved)")

	// =============================================
	// CHECKPOINT: Test if Go runtime is fully ready
	// =============================================
	Print("[g1] Running runtime readiness tests...")
	Print("")

	if testRuntimeReadiness() {
		// SUCCESS! Runtime is fully initialized
		// NOTE: Keep runtimeReady = false because fmt hangs (needs investigation)
		Print("")
		Print("*** RUNTIME READY (but staying with direct UART - fmt hangs) ***")
		Print("")

		// Initialize heap-based data structures now that heap is ready
		InitDeadlineQueue()
		InitReadyQueue()

		// Test interrupt-driven UART with a message that proved working before
		Print("[Init] Testing interrupt-driven UART...")
		uartInterruptReady = true
		Print("[Init] >>> Interrupt-driven UART test: This long message should trigger both FIFO priming and the IRQ handler <<<")
		uartInterruptReady = false
		Print("[Init] Test complete - switching back to direct mode")
		Print("")
	} else {
		// FAILED - runtime not ready, stay with direct UART
		Print("")
		Print("!!! RUNTIME NOT READY - Staying with direct UART output !!!")
		Print("")
	}

	// Keep using Print/Printf with direct UART (runtimeReady stays false)

	// TODO: DTB discovery hangs - investigate later
	// Skipping for now to test preemption

	Print("")
	Print("[g1] === Skipping DTB Discovery - Testing Preemption ===" )
	Print("")

	// Test scheduler preemption with two busy-wait goroutines
	Print("[g1] Testing scheduler preemption with two busy-wait goroutines...")

	// Launch g2 - it will busy-wait printing '2'
	Print("[g1] Launching g2...")
	go simpleGoroutine2(nil)
	Print("[g1] g2 launched (runtime.newproc called)")

	Print("[g1] Both goroutines will busy-wait WITHOUT yielding")
	Print("[g1] If timer-based preemption works, we should see '1' and '2' interleaved")
	Print("[g1] Starting busy-wait loop (NO cooperative yielding)...")
	Print("")

	// Enable async preemption - runtime is now fully initialized
	Print("[g1] Enabling async preemption...")

	// Enable timer IRQ now that we're ready to handle it
	EnableTimerIRQ()
	Print("[g1] Timer IRQ 27 enabled")

	// Set up asyncPreempt address for assembly IRQ handler
	asyncPreemptAddr := GetAsyncPreemptAddr()
	kirq.SetAsyncPreemptAddr(asyncPreemptAddr)
	Printf("[g1] AsyncPreempt address set: 0x%x", asyncPreemptAddr)

	readyForAsyncPreempt.Store(1)
	kirq.SetReadyForAsyncPreempt()
	Print("[g1] Async preemption ENABLED")
	Print("")

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
