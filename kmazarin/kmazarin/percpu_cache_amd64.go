package main

// MAZ-136: x86 CPU-ID cache.
//
// "Which CPU am I?" on x86 was answered by executing CPUID (leaf 1, APIC ID) on
// every per-CPU access — and CPUID is an unconditional VM-exit. Under nested KVM
// on the GCP host (Google's L0 emulates our VM's VMX), the scheduler/IRQ paths
// stormed ~10k CPUID exits/sec computing a constant (x86 runs single-CPU: no AP
// mailbox, boot APIC ID 0). We now probe the APIC ID once and cache it; the
// hot-path accessors (percpu_amd64.s) and the timer IRQ handler
// (kirq/preempt_amd64.s) read this instead. ARM64 needs no cache — getCPUIDAsm
// there reads MPIDR_EL1, a register that never traps.
//
// Revisit when x86 SMP lands (an AP mailbox from diplomat): each AP would
// publish its own id, e.g. via GS-based per-CPU.
var cpuIDCache uint64

// probeAPICIDAsm runs CPUID leaf 1 once (percpu_amd64.s) — the only remaining
// CPUID on x86.
func probeAPICIDAsm() uint64

// initCPUIDCacheArch probes and caches the boot CPU's APIC ID. Called once from
// InitPerCPU. (arm64 has a no-op counterpart — see percpu_cache_arm64.go.)
//
//go:nosplit
func initCPUIDCacheArch() {
	cpuIDCache = probeAPICIDAsm()
}
