package main

// Assembly function declarations — architecture-neutral.
// Each function has a per-architecture assembly implementation.
// ARM64-specific declarations are in asm_decl_arm64.go.

//go:nosplit
func GetExceptionVectorBase() uintptr

// ExceptionVectorTable has NO Go declaration — it's a pure assembly symbol
// used only by diplomat's ELF symbol lookup. Without a Go declaration,
// the linker emits "main.ExceptionVectorTable" (no .abi0 suffix).

//go:nosplit
func SetVBAR(addr uintptr)

//go:nosplit
func ReadVBAR() uintptr

// EnableIRQs is deliberately unpaired — see the note at its asm definition.
// To mask, use ksync.SaveAndDisableIRQs via the irq.go wrappers.
//
//go:nosplit
func EnableIRQs()

// SaveAndDisableIRQs / RestoreIRQs are Go wrappers over the single canonical
// ksync implementation — see irq.go (MAZ-167).

// readELR_EL1 returns ELR_EL1 (the interrupted PC). Valid only when called early
// in an exception/IRQ handler. Used by the [SCHEDLK-VIOLATION] detector to print
// the PC of whoever held schedulerLock with IRQs enabled.
//
//go:nosplit
func readELR_EL1() uint64

//go:nosplit
func GetGRegister() uint64

//go:nosplit
func GetPC() uint64

// DumpInstructionPageFaultAsm is declared in asm_decl_riscv64.go (RISC-V only).
// It walks Sv48 page tables and prints PTE chain for instruction page fault diagnostics.

// HandlePageFaultAsm is the ABI0 entry point for the page fault handler.
// Called from exception handler assembly.
// Takes faultAddr as argument, returns bool (1=handled, 0=not handled).
//
//go:nosplit
func HandlePageFaultAsm(faultAddr uint64) uint64

// HandleUserPageFaultAsm is the ABI0 entry point for userspace page fault handler.
// Called from exception handler for page faults from userspace.
// Handles demand paging for mmap'd regions.
// Takes faultAddr and isPermFault, returns bool (1=handled, 0=not handled).
//
//go:nosplit
func HandleUserPageFaultAsm(faultAddr, isPermFault uint64) uint64

// GetSyscallSwitchTarget returns the context switch target set by syscall handlers.
// Returns -1 if no context switch needed, >=0 for target thread index.
// Called from assembly after DispatchSyscall returns.
// Returns int64 to avoid sign extension issues in assembly.
//
//go:nosplit
func GetSyscallSwitchTarget() int64

// DoContextSwitch saves current context from frame, returns new context to load.
// framePtr = exception frame pointer
// targetIdx = thread index to switch to
// Returns pointer to new thread's ThreadContext structure.
//
//go:nosplit
func DoContextSwitch(framePtr uint64, targetIdx int32) uint64

// SetSyscallELR stores the return address for the current syscall.
// Called by assembly before DispatchSyscall so clone can get the proper return address.
//
//go:nosplit
func SetSyscallELR(elr uint64)

// SetSyscallSPSR stores the processor state for the current syscall.
// Called by assembly before DispatchSyscall so clone can get the proper processor state.
//
//go:nosplit
func SetSyscallSPSR(spsr uint64)

// SetSyscallCloneRegs saves R12/R13/R9 from the exception frame for clone.
// On AMD64, the standard Go runtime's clone keeps mp(R13)/gp(R9)/fn(R12) in
// callee-saved registers instead of storing them on the child stack.
//
//go:nosplit
func SetSyscallCloneRegs(r12, r13, r9 uint64)

// CheckThreadPreemption checks if the current thread should be preempted.
// If preemption is needed, saves current thread context, switches to next ready thread.
// framePtr = exception frame pointer containing saved registers
// Returns pointer to new ThreadContext if switch happened, 0 otherwise.
// Called from timer IRQ handler after NeedsThreadPreempt is set.
//
//go:nosplit
func CheckThreadPreemption(framePtr uint64) uint64

// CheckKernelGoroutinePreempt checks if the Go runtime has requested async
// preemption of the current kernel goroutine (via m.signalPending, set by
// preemptM). If the interrupted code is at an async-safe point, injects
// asyncPreempt into the exception frame so the goroutine will be preempted
// when ERET/IRET/SRET restores the modified frame.
//
// framePtr = exception frame pointer containing saved registers
// Returns 1 if asyncPreempt was injected, 0 otherwise.
// Called from timer IRQ handler when interrupted code is kernel mode with svcDepth==0.
//
// NOT nosplit: calls runtime.isAsyncSafePoint which walks pclntab.
// Safe because exception stack address > g0.stackguard0, so stack check passes.
func CheckKernelGoroutinePreempt(framePtr uint64) uint64

// RunFirstThread starts the first thread from the ready queue.
// Waits for a thread to become ready, then switches to it via ERET/IRET/SRET.
// This function never returns - it transitions to userspace.
// Called from kernel main after launching threads.
//
//go:nosplit
func RunFirstThread()

// ThreadExitAsm is the ABI0 entry point for killing the current thread.
// Called from exception handler when an unrecoverable user-mode fault occurs.
// Returns pointer to next ThreadContext (or 0 if no threads remain).
//
//go:nosplit
func ThreadExitAsm() uint64

// TerminateShepherdAsm is the ABI0 entry point for killing all threads of a shepherd.
// Called from exception handler or syscall handler.
// Args: pid (uint64), status (int64)
// Returns pointer to next ThreadContext (or 0 if no threads remain).
//
//go:nosplit
func TerminateShepherdAsm(pid uint64, status int64) uint64

// HandleUnhandledExceptionAsm is the ABI0 entry point for handling unhandled
// userspace exceptions (data abort, instruction abort, illegal instruction, etc.).
// Maps hardware exception to signal, delivers to handler or kills shepherd.
// Args: excInfo (ESR/vector/scause), faultAddr (FAR/CR2/stval), faultPC (ELR/RIP/SEPC)
// Returns: 0 if signal was queued (return via normal path), or pointer to next
// ThreadContext if shepherd was killed.
// NOT nosplit: exception stack is above g0.stackguard0, stack check passes.
func HandleUnhandledExceptionAsm(excInfo, faultAddr, faultPC uint64) uint64

// YieldToReadyThread saves thread 0's full register state into its ThreadContext,
// puts thread 0 on the ready queue, and returns to the next ready thread.
// When thread 0 is scheduled back via timer preemption, execution resumes
// at the instruction after the call to YieldToReadyThread.
// If no other thread is available, returns without yielding.
//
//go:nosplit
func YieldToReadyThread()

// sigreturnTrampoline issues the rt_sigreturn syscall.
// Called when sigtramp returns (via LR/return address on stack).
// This function must never return.
//
//go:nosplit
func sigreturnTrampoline()

// getSigreturnTrampolineAddr returns the address of sigreturnTrampoline.
//
//go:nosplit
func getSigreturnTrampolineAddr() uintptr
