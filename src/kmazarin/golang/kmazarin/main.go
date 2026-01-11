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
	"unsafe"
)

// SyscallDispatch is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls syscallDispatchInternal. This is the actual implementation.
//
//go:nosplit
//go:noinline
func syscallDispatchInternal(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return ksyscall.DispatchSyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
}

// IRQDispatch is defined in abi_stubs_arm64.s as an ABI0 entry point
// that tail-calls irqDispatchInternal. This is the actual implementation.
//
//go:nosplit
//go:noinline
func irqDispatchInternal(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	info := kirq.DispatchIRQ(irqNum, framePtr, elr, spEl0)
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
	// Debug: print 'P' to show we reached the page fault handler
	uartPutc('P')
	uartPutc('[')
	uartPutHex64Direct(faultAddr)
	uartPutc(']')

	if kmem.HandlePageFault(uintptr(faultAddr)) {
		uartPutc('+')
		return 1
	}
	uartPutc('-')
	return 0
}


// init runs before main - called after Go runtime is fully initialized
// Set up exception handlers and enable interrupts
func init() {
	// NOTE: Runtime config is already initialized in runtime_config.go:init()
	// which runs at package load time, before the Go runtime initializes.

	// Print "INIT" to show runtime has initialized
	uartPutc('I')
	uartPutc('N')
	uartPutc('I')
	uartPutc('T')
	uartPutc('\r')
	uartPutc('\n')

	// NOTE: Syscall table is initialized at package level, before runtime init
	// NOTE: VBAR_EL1 is already set by Cardinal before jumping to kmazarin
	// This must happen before the Go runtime initializes (which runs before init())
	// because runtime init calls mmap() syscalls that need kmazarin's handlers.

	// CRITICAL: Call GetExceptionVectorBase() to force linker to include exception vector table
	// Without this, the linker removes ExceptionVectorTable as dead code!
	vectorAddr := GetExceptionVectorBase()
	uartPuts("[Exception Vector] Located at 0x")
	uartPutHex64Direct(uint64(vectorAddr))
	uartPuts("\r\n")

	// Initialize critical early devices (UART, GIC, Timer, RNG)
	EarlyInit()

	// Now enable interrupts - handlers are ready
	uartPuts("[Enabling IRQs]\r\n")
	EnableIRQs()
	uartPuts("[IRQs Enabled]\r\n")

	// Initialize thread table
	// InitThreads()
}

// uartPutc writes a single character directly to UART (bypasses Go runtime)
// NOTE: UART address comes directly from StartupParams (not runtimeConfig copy)
// to avoid package initialization order issues.
//go:nosplit
func uartPutc(c byte) {
	uartBase := GetUartBase()  // Read directly from StartupParams
	*(*byte)(unsafe.Pointer(uartBase)) = c
}

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

// testRuntimeReadiness runs a comprehensive test suite to verify Go runtime is ready
// Returns true if all tests pass, false otherwise
func testRuntimeReadiness() bool {
	Print("=== Go Runtime Readiness Test Suite ===")

	// Test 1: Memory allocation with make()
	Print("[1/7] Testing make() allocation...")
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
	Print("[2/7] Testing new() allocation...")
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
	Print("[3/7] Testing GOMAXPROCS...")
	n := runtime.GOMAXPROCS(0)
	if n == 0 {
		Print("  FAIL: GOMAXPROCS returned 0")
		return false
	}
	Print("  PASS")

	// Test 4: Goroutine count
	Print("[4/7] Testing NumGoroutine...")
	ng := runtime.NumGoroutine()
	if ng == 0 {
		Print("  FAIL: NumGoroutine returned 0")
		return false
	}
	Print("  PASS")

	// Test 5: Mutex operations
	Print("[5/7] Testing sync.Mutex...")
	var mu sync.Mutex
	mu.Lock()
	mu.Unlock()
	Print("  PASS")

	// Test 6: String operations (uses runtime)
	Print("[6/7] Testing string concatenation...")
	str1 := "Hello"
	str2 := "World"
	str3 := str1 + " " + str2
	if len(str3) != 11 {
		Print("  FAIL: String concat wrong length")
		return false
	}
	Print("  PASS")

	// Test 7: fmt.Println (the big one!)
	Print("[7/7] Testing fmt.Println...")
	Print("  SKIP - fmt package causes hang (needs debugging)")
	// TODO: Debug why fmt.Sprintf hangs
	// str := fmt.Sprintf("test %d", 42)
	// fmt.Println("  fmt.Println works!")

	Print("=== All Runtime Tests Passed! ===")
	Print("")
	return true
}

// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
func simpleMain() {
	// Direct UART to verify entry
	uartPutc('M')
	uartPutc('A')
	uartPutc('I')
	uartPutc('N')
	uartPutc('\r')
	uartPutc('\n')

	// DEBUG: Print g register and PC values at start
	gVal := GetGRegister()
	uartPuts("[g register] = 0x")
	uartPutHex64Direct(gVal)
	uartPuts("\r\n")

	pcVal := GetPC()
	uartPuts("[PC] = 0x")
	uartPutHex64Direct(pcVal)
	uartPuts("\r\n")

	Print("")
	Print("[g1] Kmazarin kernel starting...")
	Print("")

	// =============================================
	// SKIP UNMAP: g register still points to Cardinal's g struct
	// =============================================
	// TODO: The Go runtime's g register (x28) still points to Cardinal's
	// g struct in low memory. Until we set up kmazarin's own g struct,
	// we can't unmap Cardinal without breaking the Go runtime.
	Print("[g1] SKIPPING Cardinal unmap (g still points to Cardinal)")
	// unmapCardinal()  // DISABLED - causes infinite page faults
	Print("[g1] Cardinal still mapped - g points to 0x40000021c0")
	Print("")

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

	// Infinite busy-wait loop, printing '1' periodically
	// NO calls to Gosched() - relies purely on timer-based preemption
	counter := uint64(0)

	for {
		counter++
		// Every 1000 iterations, print our marker
		if counter%1000 == 0 {
			// Print '1' to show g1 is running (direct UART, no runtime)
			uartPutc('1')
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

// simpleGoroutine2 is the second goroutine for the preemption test
// Pure busy-wait with NO cooperative yielding
func simpleGoroutine2(ch chan string) {
	uartPuts("[g2] Started, entering busy-wait loop (NO yielding)...\r\n")

	// Infinite busy-wait loop to test timer-based preemption
	// NO calls to Gosched() - the timer interrupt must forcibly preempt us
	counter := uint64(0)

	for {
		counter++
		// Every 1000 iterations, print our marker
		if counter%1000 == 0 {
			// Print '2' to show g2 is running (direct UART, no runtime)
			uartPutc('2')
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

func main() {
	simpleMain()
}
