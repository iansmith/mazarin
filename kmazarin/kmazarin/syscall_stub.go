//go:build !amd64

package main

// SetupSyscallMSRs is a no-op on non-x86_64 platforms.
// On x86_64, this configures MSRs for the SYSCALL instruction.
func SetupSyscallMSRs() {}
