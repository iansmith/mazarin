//go:build qemuvirt && aarch64

package main

// Assembly function declarations
// Implementations are in exceptions_arm64.s and runtime_arm64.s

func GetExceptionVectorBase() uintptr
func ExceptionVectorTable()
func SetVBAR(addr uintptr)
func EnableIRQs()
func getAuxval(tag uint64) uint64
func EnableGIC()
func EnableTimerIRQ()
func RearmTimerNow()
func GetGRegister() uint64
func GetPC() uint64

// HandlePageFaultAsm is the ABI0 entry point for the page fault handler.
// Called from data_abort in exceptions_arm64.s
// Takes faultAddr as argument, returns bool (1=handled, 0=not handled)
func HandlePageFaultAsm(faultAddr uint64) uint64

// GetSyscallSwitchTarget returns the context switch target set by syscall handlers.
// Returns -1 if no context switch needed, >=0 for target thread index.
// Called from assembly after DispatchSyscall returns.
// Returns int64 to avoid sign extension issues in assembly.
func GetSyscallSwitchTarget() int64

// DoContextSwitch saves current context from frame, returns new context to load.
// framePtr = exception frame pointer
// targetIdx = thread index to switch to
// Returns pointer to new thread's ThreadContext structure.
func DoContextSwitch(framePtr uint64, targetIdx int32) uint64

// SetSyscallELR stores the ELR_EL1 for the current syscall.
// Called by assembly before DispatchSyscall so clone can get the proper return address.
func SetSyscallELR(elr uint64)
