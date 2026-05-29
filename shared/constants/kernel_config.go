package constants

// KernelConfig holds kernel-level settings parsed from the embedded kernel.toml.
// Parsed at boot with a real TOML library (pelletier/go-toml/v2).
type KernelConfig struct {
	Timezone string `toml:"timezone"`

	// Memory
	KernelBudgetMB int `toml:"kernel_budget_mb"`
	GoMemLimitMB   int `toml:"go_mem_limit"`
	KernelMemLimitMB int `toml:"kernel_mem_limit"`

	// GC tuning
	GCPercentage    int `toml:"gc_percentage"`
	GCPercentKernel int `toml:"gc_percent_kernel"`

	// Scheduling
	KernelTickRate    int `toml:"kernel_tick_rate"`
	PreemptAfterTicks int `toml:"preempt_after_ticks"`

	// Serial output
	SuppressSerialStdioCopy bool `toml:"suppress_serial_stdio_copy"`
	SuppressKernelSerial    bool `toml:"suppress_kernel_serial"`

	// Diagnostics
	// KmemLeakTest runs the MAZ-108 kmem teardown leak-soak self-test at boot
	// (before launching shepherds, while the system is quiescent) and logs
	// before/after free-frame counts. OFF by default; enable only for
	// teardown-primitive verification.
	KmemLeakTest bool `toml:"kmem_leak_test"`
}
