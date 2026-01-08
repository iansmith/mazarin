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
