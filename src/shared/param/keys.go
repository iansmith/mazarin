// keys.go - Well-known parameter keys for Cardinal-to-Kmazarin communication
//
// This file defines the parameter keys used in the parameter buffer that
// Cardinal populates and passes to Kmazarin via auxv.

package param

// ============================================================================
// Parameter Keys
// ============================================================================
// These keys are used in the text-based parameter buffer format:
// KEY value\n

const (
	// Device Tree Blob information
	KeyDTBPhysAddr = "DTB_PHYS_ADDR" // Physical address of DTB
	KeyDTBSize     = "DTB_SIZE"      // Size of DTB in bytes

	// Kmazarin binary information (computed from ELF at load time)
	KeyKmazarinPhysBase   = "KMAZARIN_PHYS_BASE"   // Physical load address
	KeyKmazarinTextStart  = "KMAZARIN_TEXT_START"  // Physical .text start
	KeyKmazarinTextEnd    = "KMAZARIN_TEXT_END"    // Physical .text end
	KeyKmazarinRodataStart = "KMAZARIN_RODATA_START" // Physical .rodata start
	KeyKmazarinRodataEnd   = "KMAZARIN_RODATA_END"   // Physical .rodata end
	KeyKmazarinDataStart  = "KMAZARIN_DATA_START"  // Physical .data start
	KeyKmazarinBssEnd     = "KMAZARIN_BSS_END"     // Physical end of BSS
	KeyKmazarinBinarySize = "KMAZARIN_BINARY_SIZE" // Total static size (text+rodata+data+bss)
	KeyKmazarinEntryPoint = "KMAZARIN_ENTRY"       // Entry point VA

	// Memory pool information
	KeyFramePoolStart = "FRAME_POOL_START" // Start of physical frame pool
	KeyFramePoolEnd   = "FRAME_POOL_END"   // End of physical frame pool

	// Stack information
	KeyKernelG0StackPhys  = "KERNEL_G0_STACK_PHYS"  // Physical address of g0 stack
	KeyKernelExcStackPhys = "KERNEL_EXC_STACK_PHYS" // Physical address of exception stack
	KeyKernelHeapStartVA  = "KERNEL_HEAP_START_VA"  // VA where heap can start
	KeyKernelHeapMaxSize  = "KERNEL_HEAP_MAX_SIZE"  // Maximum heap size (512MB - binary size)

	// Device initialization status
	KeyUartInitialized = "UART_INITIALIZED" // 1 if UART is initialized, 0 otherwise
	KeyUartPhysAddr    = "UART_PHYS_ADDR"   // Physical address of UART
	KeyUartRingBufPhys = "UART_RINGBUF_PHYS" // Physical address of UART ring buffer

	// VirtIO RNG device information
	KeyRngInitialized = "RNG_INITIALIZED" // 1 if RNG is initialized, 0 otherwise
	KeyRngDeviceID    = "RNG_DEVICE_ID"   // VirtIO RNG device ID (e.g., "1005" or "1044")
	KeyRngBarPhys     = "RNG_BAR_PHYS"    // Physical address of RNG BAR
	KeyRngIRQ         = "RNG_IRQ"         // IRQ number for RNG device

	// Page table information
	KeyTTBR0L0Phys = "TTBR0_L0_PHYS" // Physical address of TTBR0 L0 table
	KeyTTBR1L0Phys = "TTBR1_L0_PHYS" // Physical address of TTBR1 L0 table (once implemented)
)

// ============================================================================
// Auxv Constants
// ============================================================================

const (
	// Custom auxv types for device handoff
	// Uses values in unused range (0x1000+) to avoid conflicts with standard types

	// UART/Serial device
	AT_UART_INITIALIZED = 0x1000 // 1 if UART initialized by Cardinal, 0 otherwise
	AT_UART_PHYS_ADDR   = 0x1001 // Physical address of UART (e.g., 0x09000000)
	AT_UART_RINGBUF_PHYS = 0x1002 // Physical address of UART ring buffer
	AT_UART_IRQ         = 0x1003 // IRQ number for UART (e.g., 33)

	// VirtIO RNG device
	AT_RNG_INITIALIZED  = 0x1010 // 1 if RNG initialized by Cardinal, 0 otherwise
	AT_RNG_DEVICE_ID    = 0x1011 // VirtIO device ID (1005=legacy, 1044=modern)
	AT_RNG_BAR_PHYS     = 0x1012 // Physical address of RNG BAR (MMIO base)
	AT_RNG_IRQ          = 0x1013 // IRQ number for RNG device

	// Parameter buffer (deprecated - use AT_ values for device info)
	AT_CARDINAL_PARAMS     = 0x1FFE // Parameter buffer physical address (if used)
	AT_CARDINAL_PARAMS_LEN = 0x1FFF // Parameter buffer length in bytes (if used)
)
