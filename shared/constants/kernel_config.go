package constants

// KernelConfig holds kernel-level settings parsed from the embedded kernel.toml.
// Parsed at boot with a real TOML library (pelletier/go-toml/v2).
type KernelConfig struct {
	Timezone string `toml:"timezone"`

	// Memory
	KernelBudgetMB int `toml:"kernel_budget_mb"`
	GoMemLimitMB   int `toml:"go_mem_limit"`
	KernelMemLimitMB int `toml:"kernel_mem_limit"`

	// FramebufferMaxMB caps the GPU framebuffer allocation (megabytes). This is
	// the single source of truth for the framebuffer size bound: the actual
	// framebuffer is sized to the detected display resolution, and the kernel
	// panics at boot if that would exceed this cap (rather than over-allocating).
	// 0 means "use the built-in default" (DefaultFramebufferMaxMB).
	FramebufferMaxMB int `toml:"framebuffer_max_mb"`

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

	// CloneExecRaceTest runs the MAZ-112 clone_exec intent-visibility race
	// self-test at boot (before launching shepherds, while the system is
	// quiescent). It creates a real clone_exec child and, at the moment the
	// child becomes schedulable, reads back its race-sensitive startup state
	// (ParentPID + buffered intent + chdir target), then reaps the child. It
	// logs [clone-exec-race] PASS when the fields are already populated
	// (window closed) or FAIL when they are observed unset. OFF by default;
	// enable only to verify the race fix.
	CloneExecRaceTest bool `toml:"clone_exec_race_test"`

	// CtxMarshalTest runs the MAZ-135 context-marshal self-test at boot (amd64
	// only; no-op on arm64). It drives the REAL SaveContextFromFrame /
	// SaveCurrentThreadContext with synthetic sentinel inputs on a scratch thread
	// and asserts every ThreadContext field was populated (the value-flow
	// complement to the build-time cmd/frameaudit presence gate). Swaps
	// CurrentThread/savedExcFSBase/xmmSaveArea IRQ-masked and restores them, so it
	// is kept OFF the default boot path; enable in test/CI configs.
	CtxMarshalTest bool `toml:"ctx_marshal_test"`
}

// DefaultFramebufferMaxMB is the framebuffer cap (megabytes) used when the kernel
// TOML does not set framebuffer_max_mb. Single source of truth for the default;
// the GPU sizes the actual framebuffer to the detected display resolution and
// panics if it would exceed the cap.
const DefaultFramebufferMaxMB = 64
