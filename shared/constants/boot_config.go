package constants

const (
	MaxBootstrapShepherds = 4   // Maximum bootstrap shepherds (kernel-loaded)
	MaxShepherds          = 8   // Maximum application shepherds (fs-loaded)
	MaxModulesPerShepherd = 8   // Maximum .maz/.mzr per shepherd
	MaxPathLen          = 64  // Maximum path string length
	MaxNameLen          = 32  // Maximum name identifier length
	MaxTimezoneLen      = 48  // Maximum timezone string length
)

// BootModule describes a named module (.maz or .mzr) to load into a shepherd.
type BootModule struct {
	Name [MaxNameLen]byte
	Path [MaxPathLen]byte
}

// BootShepherd describes one shepherd in the boot sequence.
type BootShepherd struct {
	Name     [MaxNameLen]byte
	Path     [MaxPathLen]byte
	Mzr      [MaxModulesPerShepherd]BootModule
	Maz      [MaxModulesPerShepherd]BootModule
	MzrCount int
	MazCount int
}

// BootConfig is the parsed /kmazarin.toml configuration.
type BootConfig struct {
	MinCpus        int
	MaxCpus        int
	MinRamMB       int
	MaxRamMB       int
	KernelBudgetMB int

	// SuppressSerialStdioCopy controls whether userspace write() output is echoed
	// to the serial port in addition to being routed through the stdio
	// shepherd's display. When true, only the stdio shepherd writes to serial.
	SuppressSerialStdioCopy bool

	// SuppressKernelSerial controls whether kernel console output
	// (KPrintf, KWriteString, etc.) is suppressed. When true, all
	// console package output becomes a no-op. Use together with
	// SuppressSerialStdioCopy to eliminate nearly all UART traffic for
	// performance testing.
	SuppressKernelSerial bool

	// GCPercentage is the GOGC value for shepherd processes. 0 means use
	// the default (currently 5). Higher values delay GC, reducing CPU
	// overhead at the cost of more memory usage. For example, 90 means
	// the GC triggers when heap is 90% larger than the previous live set.
	GCPercentage int

	// GoMemLimitMB is the GOMEMLIMIT value (in MB) for shepherd processes.
	// 0 means use the default (24MB).
	GoMemLimitMB int

	// KernelMemLimitMB is the GOMEMLIMIT value (in MB) for the kernel.
	// 0 means use the default (24MB). Diplomat reads this from the TOML
	// config and passes it as the kernel's GOMEMLIMIT environment variable.
	KernelMemLimitMB int

	// GCPercentKernel is the GOGC value for the kernel process.
	// 0 means use Go default (100). Set very high (e.g. 10000) to let
	// GOMEMLIMIT be the GC governor instead of GOGC.
	GCPercentKernel int

	// KernelTickRate is the kernel timer tick frequency in Hz.
	// Default 250 (4ms ticks). Controls deadline processing, signal delivery,
	// and the base scheduling granularity.
	KernelTickRate int

	// PreemptAfterTicks is the number of kernel ticks before forcing a
	// thread preemption. Default 25 (25 × 4ms = 100ms at 250Hz).
	// Also determines the time attribute update frequency.
	PreemptAfterTicks int

	Timezone [MaxTimezoneLen]byte

	BootstrapShepherds     [MaxBootstrapShepherds]BootShepherd
	BootstrapShepherdCount int

	Shepherds     [MaxShepherds]BootShepherd
	ShepherdCount int
}

// NullTermString returns a Go string from a null-terminated byte array.
func NullTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
