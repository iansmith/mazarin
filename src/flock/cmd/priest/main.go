//go:build qemuvirt && aarch64

// priest is the syscall router for Mazzy userspace.
// It receives syscalls from normal programs and either handles them directly
// (Mazzy syscalls) or routes them to appropriate servers (Linux syscalls).
package main

import (
	"fmt"
	"mazarin/sys"
)

// PriestSyscallEntry is the entry point for syscalls from other programs.
// This function's address is patched into userspace programs at load time.
// The symbol name in the ELF will be "main.PriestSyscallEntry".
//
//go:noinline
func PriestSyscallEntry(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// For now, just print the syscall info
	fmt.Printf("[priest] syscall %d (0x%x) args: %x %x %x %x %x %x\n",
		num, num, a1, a2, a3, a4, a5, a6)

	// Check if it's a Mazzy syscall (0x1000+)
	if num >= 0x1000 {
		return handleMazzySyscall(num, a1, a2, a3, a4, a5, a6)
	}

	// Linux syscall - for now, just print and return error
	fmt.Printf("[priest] Linux syscall %d not implemented\n", num)
	return -38 // ENOSYS
}

// handleMazzySyscall handles Mazzy-specific syscalls by making real SVC traps
func handleMazzySyscall(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// For Mazzy syscalls, we make the real syscall to kmazarin
	// This uses the standard Go syscall which does SVC
	r1, _, errno := sys.RawSyscall(num, a1, a2, a3, a4, a5, a6)
	if errno != 0 {
		return -int64(errno)
	}
	return int64(r1)
}

// priestSyscallEntryAddr holds the address of PriestSyscallEntry.
// This is used to prevent the linker from stripping the function
// and to provide a known symbol for the patchpriest tool to find.
var priestSyscallEntryAddr = PriestSyscallEntry

func main() {
	fmt.Println("[priest] Starting syscall router")

	// Print the address of PriestSyscallEntry for debugging
	// This also ensures the function is not stripped
	fmt.Printf("[priest] PriestSyscallEntry at %p\n", priestSyscallEntryAddr)

	// For now, just call GetTime to verify we can make Mazzy syscalls
	ts, err := sys.GetTime()
	if err != nil {
		fmt.Printf("[priest] GetTime error: %v\n", err)
	} else {
		fmt.Printf("[priest] Current time: %d.%09d\n", ts.Seconds, ts.Nanoseconds)
	}

	fmt.Println("[priest] Ready to handle syscalls from userspace programs")

	// In the future, this will be an event loop handling syscall requests
	// For now, just spin
	select {}
}
