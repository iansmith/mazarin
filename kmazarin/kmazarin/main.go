package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/kmazarin/device/virtio/rng"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/klog"
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

	// Wire on-demand [status] dump trigger so .maz programs can call
	// sys.DumpKernelStatus() when they observe interesting events.
	ksyscall.EpochStatusDumpFn = RequestEpochStatusDump

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
		klog.Fatalf("DTB BAD MAGIC\n", "[DTB] Bad magic at VA 0x%X: 0x%X\n", dtbVA, magic)
		return
	}

	totalSize := uint32(hdr[4])<<24 | uint32(hdr[5])<<16 | uint32(hdr[6])<<8 | uint32(hdr[7])
	if totalSize == 0 || totalSize > 1<<20 {
		klog.Fatalf("DTB BAD SIZE\n", "[DTB] Invalid size: %d\n", totalSize)
		return
	}

	buf := kmem.AllocBuffer(uint64(totalSize))
	if buf == nil {
		klog.Fatalf("DTB ALLOC FAIL\n", "[DTB] Failed to alloc %d bytes\n", totalSize)
		return
	}

	// Copy the FDT data
	src := unsafe.Slice((*byte)(unsafe.Pointer(dtbVA)), int(totalSize))
	copy(buf.Bytes(), src)

	safeDTBVirtAddr = buf.VA
}

// DebugTimerCount is a debug counter for tracking timer IRQs.
// Placed in main package to test if the kirq.TimerIRQCount location has mapping issues.
// This variable is written from timer IRQ handler assembly.
var DebugTimerCount uint64

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
	// NOTE: Drivers are already registered in EarlyInit()

	// Use safe FDT copy if available (protects against framebuffer overwrite),
	// otherwise fall back to the original location.
	var dtbAddr uintptr
	if safeDTBVirtAddr != 0 {
		dtbAddr = safeDTBVirtAddr
	} else {
		dtbPhysAddr := GetDtbPhysAddr()
		if dtbPhysAddr == 0 {
			return
		}
		dtbAddr = dtbVirtAddr(dtbPhysAddr)
	}
	// Register all device drivers BEFORE discovering devices
	device.RegisterAllDrivers()

	// Parse DTB and discover devices
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		klog.Fatalf("DTB PARSE FAIL\n", "[DeviceTest] DTB parse error: %v\n", err)
		return
	}

	// Initialize kernel time subsystem (reads RTC once, caches for tick-based derivation).
	// On platforms without an RTC (e.g. AMD64), falls back to uptime mode (base=0).
	ktime.Init()

	// Wire up interrupts now that interrupt controller is discovered
	if err := device.WireInterrupts(); err != nil {
		klog.Fatalf("WIRE IRQ FAIL\n", "[DeviceTest] ERROR wiring interrupts: %v\n", err)
	} else {
		// Set up UART soft IRQ hook for userspace serial RX, and
		// register the interrupt-driven TX path (QueueByte → txBuf → top-half drain).
		if bs, ok := device.GetByteStream(); ok {
			if pl011, ok := bs.(*uart.PL011); ok {
				SetupUartSoftIRQ(pl011.IRQ())
				serial.SetQueueByteFunc(pl011.WriteByte)
				serial.SetQueueByteTryFunc(pl011.WriteByteTry)
				StoreUartTxDriver(pl011)
			} else if ns16550, ok := bs.(*uart.NS16550); ok {
				SetupUartSoftIRQ(ns16550.IRQ())
				serial.SetQueueByteFunc(ns16550.WriteByte)
				serial.SetQueueByteTryFunc(ns16550.WriteByteTry)
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



// kernelBootTick is the hardware counter value when kmazarin's main() starts.
// Used to compute true kernel uptime (excluding UEFI boot).
var kernelBootTick uint64

func simpleMain() {
	kernelBootTick = kirq.ReadCounterValue()

	// Test runtime readiness FIRST (before unmapping Cardinal)
	if testRuntimeReadiness() {
		// NOTE: GOGC is NOT set here — kernel uses Go default (100%).
		// Userspace programs also get GOGC=100 via their envp in launch.go.
		// GOMEMLIMIT is set via diplomat envp (64MiB).
		debug.SetMemoryLimit(64 * 1024 * 1024)     // 64MB soft heap cap (matches diplomat envp)
		InitDeadlineQueue()
		InitSoftIRQDispatcher()
	} else {
		klog.Fatalf("RT NOT READY\n", "[Main] Runtime not ready\n")
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
		klog.Errf("[Main] VirtIO Block init failed (no device found?)\n")
	} else {
		klog.Logf("[Main] VirtIO Block init OK, IRQ=%d\n", uint64(block.GetIRQNum()))
	}

	// Initialize VirtIO RNG device (entropy source).
	// Uses polling (no IRQ) — RNG requests are infrequent.
	if !rng.Init() {
		klog.Errf("[Main] VirtIO RNG init failed (no device found?)\n")
	}

	// Wire up block device IRQ: register with top-half dispatcher and
	// enable at the interrupt controller so interrupts reach the CPU.
	if irq := block.GetIRQNum(); irq != 0 {
		SetBlockIRQ(irq, block.GetISRBase(), block.GetIOCompletePtr(), block.GetEnginePtr(), block.GetSidecarFreeBitsPtr())
		enableBlockDeviceIRQ(irq)
		// Register async mode callback so doBlockIO (kernel polling path)
		// can temporarily disable async mode during its polling loop.
		block.GetDevice().SetAsyncMode = SetBlockAsyncMode
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
	initKernelWorkers()

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
	klog.Fatalf("IDLE RETURNED\n", "[Main] ERROR: KernelIdleLoop returned!\n")
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
		klog.Errf("[boot] kernel.toml parse error: %v\n", err)
		return cfg
	}
	return cfg
}

// launchEmbeddedFS launches the fs shepherd from the go:embed'd ELF data.
// This eliminates the circular bootstrap dependency — fs no longer needs to
// be loaded from the filesystem it implements.
func launchEmbeddedFS() {
	result := ksyscall.LaunchFromMemory(EmbeddedFSElf, "fs")
	if result == 0 {
		kmem.FinalUserspaceSync()
	} else {
		klog.Fatalf("FS LAUNCH FAIL\n", "[boot] embedded fs launch FAILED (error %d)\n", result)
	}
}
