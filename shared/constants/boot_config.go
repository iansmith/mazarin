package constants

const (
	MaxBootstrapPriests = 4   // Maximum bootstrap priests (kernel-loaded)
	MaxPriests          = 8   // Maximum application priests (fs-loaded)
	MaxModulesPerPriest = 8   // Maximum .maz/.mzr per priest
	MaxPathLen          = 64  // Maximum path string length
	MaxNameLen          = 32  // Maximum name identifier length
	MaxTimezoneLen      = 48  // Maximum timezone string length
)

// BootModule describes a named module (.maz or .mzr) to load into a priest.
type BootModule struct {
	Name [MaxNameLen]byte
	Path [MaxPathLen]byte
}

// BootPriest describes one priest in the boot sequence.
type BootPriest struct {
	Name     [MaxNameLen]byte
	Path     [MaxPathLen]byte
	Mzr      [MaxModulesPerPriest]BootModule
	Maz      [MaxModulesPerPriest]BootModule
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
	// priest's display. When true, only the stdio priest writes to serial.
	SuppressSerialStdioCopy bool

	// SuppressKernelSerial controls whether kernel console output
	// (KPrintf, KWriteString, etc.) is suppressed. When true, all
	// console package output becomes a no-op. Use together with
	// SuppressSerialStdioCopy to eliminate nearly all UART traffic for
	// performance testing.
	SuppressKernelSerial bool

	// GCPercentage is the GOGC value for priest processes. 0 means use
	// the default (currently 5). Higher values delay GC, reducing CPU
	// overhead at the cost of more memory usage. For example, 90 means
	// the GC triggers when heap is 90% larger than the previous live set.
	GCPercentage int

	// GoMemLimitMB is the GOMEMLIMIT value (in MB) for priest processes.
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

	// TimeUpdateHertz is the frequency (in Hz) at which the kernel publishes
	// time attributes (utc_seconds, utc_nanos). 0 or 1 means once per second
	// (default). Higher values (e.g. 3) reduce visual jitter in clock displays
	// by providing more frequent dirty notifications.
	TimeUpdateHertz int

	Timezone [MaxTimezoneLen]byte

	BootstrapPriests     [MaxBootstrapPriests]BootPriest
	BootstrapPriestCount int

	Priests     [MaxPriests]BootPriest
	PriestCount int
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
