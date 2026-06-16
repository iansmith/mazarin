//go:build arm64

package main

// MAZ-143 — gspUnsafeKernelResume is a deliberate no-op on ARM64. The hazard it
// guards on amd64 cannot produce a `morestack on g0` here, but the reason is
// subtle and worth stating precisely: the dangerous (g,SP) WINDOW *does* exist on
// ARM64 — single g-home does NOT prevent it.
//
// The window (asm_arm64.s ~501-504): runtime.morestack sets g (X28) = m.g0 and
// only THEN switches RSP to the g0 stack — a multi-instruction gap (it even spans
// a `BL save_g`) in which g==g0 while SP is still the goroutine heap stack. A
// timer / priority-wake IRQ landing there is raw-switched by the SAME shared
// checkThreadPreemptionImpl (exceptions_arm64.s timer:1508 / pwake:1623), and
// SaveContextFromFrame captures (X[28]=g0, SP=SP_EL0=goroutine-stack). Window,
// raw-switch path, and (g,SP) capture are all present on ARM64.
//
// What makes amd64 crash and ARM64 not is the DUAL-HOME g — the documented root of
// the whole MAZ-135 / MAZ-136 / MAZ-143 family:
//
//   - amd64 keeps g in TWO homes that must agree: R14 (the compiler stack-check
//     prologue, `CMPQ SP,16(R14)`) and TLS [FS_BASE-8] (runtime.morestack's
//     badmorestackg0 check). kmazarin saves and restores them independently, so
//     restoring a context captured in the morestack window reconstructs a DESYNCED
//     state in which both homes resolve to g0 while SP is a goroutine stack; the
//     next prologue then enters morestack with g==m.g0 → `morestack on g0`. A
//     faithful save (MAZ-139) does not help — the transient itself is unsafe to
//     checkpoint. That is why amd64 needs fix C: skip the raw switch in this window.
//
//   - ARM64 keeps g in ONE home (X28). The captured (X28=g0, SP=goroutine) is
//     restored VERBATIM and ERET resumes AT the interrupted morestack instruction,
//     which COMPLETES the SP switch (asm_arm64.s:503-504, SP ← g0.sched.sp). There
//     is no second home to fall out of sync, and no function prologue ever runs at
//     (g==g0, SP=goroutine) before the switch completes — the window-state is
//     self-consistent and resumes cleanly. badmorestackg0 is structurally
//     unreachable. Empirically, `morestack on g0` has never been observed on ARM64
//     across the HVF stability sweeps; the ARM64 bug family (MAZ-5 / MAZ-15
//     page-reuse-without-zeroing & GC-UAF, MAZ-8 IPC-stall) is memory-content /
//     liveness corruption — categorically orthogonal to this register-state hazard
//     (a buggy (g,SP) restore corrupts g/SP → a morestack THROW, never a stale heap
//     pointer or a syscall stall).
//
// This immunity is a PER-THREAD property of single-home g, so it holds under SMP
// too: each CPU has its own g0 (perCPUData[i].G0) but every kernel goroutine homes
// g solely in X28, so every CPU's captured window-state is equally benign.
//
// Documented divergence (feedback_amd64_arch_divergence.md), mirroring the
// pwake_trace_arm64.go no-op. The shared checkThreadPreemptionImpl calls this; on
// ARM64 it never defers a switch.
//
//go:nosplit
func gspUnsafeKernelResume(framePtr uintptr) bool {
	_ = framePtr
	return false
}
