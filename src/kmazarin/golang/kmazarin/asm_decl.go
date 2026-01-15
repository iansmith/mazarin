//go:build qemuvirt && aarch64

package main

// Assembly function declarations
// Implementations are in exceptions_arm64.s and runtime_arm64.s

//go:nosplit
func GetExceptionVectorBase() uintptr

//go:nosplit
func ExceptionVectorTable()

//go:nosplit
func SetVBAR(addr uintptr)

//go:nosplit
func EnableIRQs()

//go:nosplit
func DisableIRQs()

// SaveAndDisableIRQs saves the current DAIF state and disables IRQs.
// Returns the saved DAIF value which should be passed to RestoreIRQs.
// This allows nested disable/restore pairs.
//go:nosplit
func SaveAndDisableIRQs() uint64

// RestoreIRQs restores the DAIF register to a previously saved state.
// Use with SaveAndDisableIRQs for nested critical sections.
//go:nosplit
func RestoreIRQs(savedDAIF uint64)

//go:nosplit
func getAuxval(tag uint64) uint64

//go:nosplit
func EnableGIC()

//go:nosplit
func EnableTimerIRQ()

//go:nosplit
func RearmTimerNow()

//go:nosplit
func GetGRegister() uint64

//go:nosplit
func GetPC() uint64

// asyncPreemptWrapper is the wrapper that saves g around asyncPreempt calls.
// Used by the IRQ handler for safe async preemption.
// Defined in exceptions_arm64.s
//go:nosplit
func asyncPreemptWrapper()

// HandlePageFaultAsm is the ABI0 entry point for the page fault handler.
// Called from data_abort in exceptions_arm64.s
// Takes faultAddr as argument, returns bool (1=handled, 0=not handled)
//go:nosplit
func HandlePageFaultAsm(faultAddr uint64) uint64

// GetSyscallSwitchTarget returns the context switch target set by syscall handlers.
// Returns -1 if no context switch needed, >=0 for target thread index.
// Called from assembly after DispatchSyscall returns.
// Returns int64 to avoid sign extension issues in assembly.
//go:nosplit
func GetSyscallSwitchTarget() int64

// DoContextSwitch saves current context from frame, returns new context to load.
// framePtr = exception frame pointer
// targetIdx = thread index to switch to
// Returns pointer to new thread's ThreadContext structure.
//go:nosplit
func DoContextSwitch(framePtr uint64, targetIdx int32) uint64

// SetSyscallELR stores the ELR_EL1 for the current syscall.
// Called by assembly before DispatchSyscall so clone can get the proper return address.
//go:nosplit
func SetSyscallELR(elr uint64)

// SetSyscallSPSR stores the SPSR_EL1 for the current syscall.
// Called by assembly before DispatchSyscall so clone can get the proper processor state.
//go:nosplit
func SetSyscallSPSR(spsr uint64)
