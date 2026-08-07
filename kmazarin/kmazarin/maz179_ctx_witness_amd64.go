//go:build amd64

package main

// maz179YieldResumeCheck is the amd64 no-op counterpart of the arm64
// suspended-handler-chain check (exceptions_arm64.s). It exists because
// serial_console.go's pushStringFull is shared across architectures.
//
// amd64 does not need it: the equivalent hazard there — a context switch made
// from inside a handler abandoning its stack level — is already handled by the
// MAZ-136 global IST/RSP0 rotation cursors, which retire the abandoning
// handler's level on every such switch (see syscall_amd64.go's rotation
// minefield note). ARM64 has no rotation, which is why it needs the check.
//
//go:nosplit
func maz179YieldResumeCheck() {}
