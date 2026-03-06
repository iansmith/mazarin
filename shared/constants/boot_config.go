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
