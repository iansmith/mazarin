// Package linuxabi holds Linux ABI constants and helpers shared between the
// kernel (kmazarin) and the linux shepherd (maz/linux), so both sides agree on
// the exact bit values and classification rules — the same role shared/sysid
// plays for portable syscall identifiers.
//
// The clone(2) flag mask is the discriminator the fork/exec work (MAZ-62
// container; MAZ-78 routing) keys on: a clone whose CLONE_THREAD bit is set is
// thread creation (the existing in-kernel path); a clone with CLONE_THREAD
// clear is process creation (forwarded to the shepherd). See
// design/linux-fork-exec-survey.md for the runtime analysis that makes this
// discriminator unambiguous.
package linuxabi

// CLONE_* flag bit values, matching the Linux UAPI (and the Go runtime's
// runtime/os_linux.go / syscall/exec_linux.go definitions). Architecture
// independent.
const (
	CLONE_VM      = 0x00000100 // child shares the calling process's memory space
	CLONE_FS      = 0x00000200 // child shares filesystem info (cwd, root, umask)
	CLONE_FILES   = 0x00000400 // child shares the open file descriptor table
	CLONE_SIGHAND = 0x00000800 // child shares signal handlers
	CLONE_VFORK   = 0x00004000 // parent suspended until child execs or exits
	CLONE_THREAD  = 0x00010000 // child placed in the same thread group (== thread)
	CLONE_SYSVSEM = 0x00040000 // child shares System V SEM_UNDO semantics
	CLONE_SETTLS  = 0x00080000 // child gets its own TLS from the newtls argument
)

// SIGCHLD is the signal a child delivers to its parent on termination. A bare
// fork()/vfork() encodes it in the low byte of the clone flag word as the
// exit-signal selector; the Go syscall package's forkExec sets it for the
// process-creation case (syscall/exec_linux.go).
const SIGCHLD = 0x11 // 17

// IsProcessClone reports whether a clone(2) flag mask requests PROCESS creation
// (forwarded to the linux shepherd) rather than THREAD creation (the existing
// in-kernel path).
//
// The sole discriminator is the CLONE_THREAD bit:
//
//   - The Go runtime ALWAYS sets CLONE_THREAD|CLONE_VM when spawning an OS
//     thread (runtime/os_linux.go cloneFlags); there is no runtime path that
//     clones a thread without CLONE_THREAD (design/linux-fork-exec-survey.md).
//   - os/exec via syscall.forkExec NEVER sets CLONE_THREAD: it uses
//     CLONE_VFORK|CLONE_VM (plus the SIGCHLD exit-signal in the low byte) or a
//     bare SIGCHLD fork (syscall/exec_linux.go).
//
// CLONE_VFORK and the low-byte SIGCHLD selector corroborate the process case
// but are NOT load-bearing: CLONE_THREAD alone is sufficient and is never
// omitted for threads, so it dominates even when a SIGCHLD-like value appears
// in the low byte. This keeps the existing thread path from ever being
// misrouted to the shepherd.
func IsProcessClone(flags uint64) bool {
	return flags&CLONE_THREAD == 0
}
