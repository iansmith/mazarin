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

	// VirtIO RNG device information
	KeyRngDeviceID = "RNG_DEVICE_ID" // VirtIO RNG device ID (vendor:device)
	KeyRngBarPhys  = "RNG_BAR_PHYS"  // Physical address of RNG BAR

	// Page table information
	KeyTTBR0L0Phys = "TTBR0_L0_PHYS" // Physical address of TTBR0 L0 table
	KeyTTBR1L0Phys = "TTBR1_L0_PHYS" // Physical address of TTBR1 L0 table (once implemented)
)

// ============================================================================
// Auxv Constants
// ============================================================================

const (
	// Custom auxv type for parameter buffer
	// Uses value in unused range (0x1000+) to avoid conflicts with standard types
	AT_CARDINAL_PARAMS     = 0x1000 // Parameter buffer physical address
	AT_CARDINAL_PARAMS_LEN = 0x1001 // Parameter buffer length in bytes
)
