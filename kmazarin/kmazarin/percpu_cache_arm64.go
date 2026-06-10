package main

// initCPUIDCacheArch is a no-op on arm64: getCPUIDAsm (percpu_arm64.s) reads
// MPIDR_EL1 directly — a system register, never a trap — so no cache is needed.
// It exists only so InitPerCPU has a single cross-arch hook; the CPU-ID cache
// is an x86-specific optimization (MAZ-136, see percpu_cache_amd64.go).
//
//go:nosplit
func initCPUIDCacheArch() {}
