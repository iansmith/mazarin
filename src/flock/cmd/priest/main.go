
// priest is the syscall router for Mazzy userspace.
// It receives syscalls from normal programs and either handles them directly
// (Mazzy syscalls) or routes them to appropriate servers (Linux syscalls).
package main

import (
	"fmt"
	"unsafe"

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

// busyLoop3s prints '3' in a tight loop to test userspace scheduling
// alongside kmazarin's goroutines that print '1' and '2'.
func busyLoop3s() {
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker (same as kmazarin)
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				fmt.Print("\n")
			} else {
				fmt.Print("3")
			}
		}
	}
}

func main() {
	// =====================================================
	// USERSPACE ENTRY POINT
	// =====================================================
	// This is the first code running in EL0 (userspace)!
	// If we see this message, the kernel successfully:
	//   1. Loaded priest.elf from FAT32 disk
	//   2. Mapped it into low memory with user permissions
	//   3. Performed ERET to EL0
	//   4. We made an SVC syscall for fmt.Println and it worked!
	fmt.Println("========================================")
	fmt.Println("[PRIEST] RUNNING IN USERSPACE (EL0)!")
	fmt.Println("========================================")

	// Get framebuffer info via syscall
	if fb, err := sys.GetFramebuffer(); err == nil {
		fmt.Printf("[priest] Framebuffer: addr=0x%x %dx%d pitch=%d\n",
			fb.Addr, fb.Width, fb.Height, fb.Pitch)
	} else {
		fmt.Printf("[priest] No framebuffer: %v\n", err)
	}

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

	// Try to load priest2.elf (goroutine scheduling test: prints 1s and 2s)
	fmt.Println("[priest] Loading /priest2.elf...")

	// Get address of PriestSyscallEntry as uintptr
	entryAddr := uintptr(unsafe.Pointer(&priestSyscallEntryAddr))

	// Allocate ProgramControl on the stack (writable memory)
	var pc sys.ProgramControl

	err = sys.Run("/priest2.elf", entryAddr, &pc)
	if err != nil {
		fmt.Printf("[priest] Failed to load priest2.elf: %v\n", err)
		fmt.Println("[priest] Entering busy loop printing 3s...")
		busyLoop3s()
		return
	}

	fmt.Printf("[priest] Loaded program %d at 0x%x, entry=0x%x\n",
		pc.ProgramID, pc.LoadAddress, pc.EntryPoint)

	// Convert entry point to function pointer and call it
	type entryFunc func()
	entry := *(*entryFunc)(unsafe.Pointer(&pc.EntryPoint))

	fmt.Println("[priest] Calling program entry point...")
	entry() // Calls main.MazarinMain() → main.main()

	fmt.Println("[priest] Program returned, entering busy loop printing 3s...")
	busyLoop3s()
}
