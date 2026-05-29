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

// csignalMask isolates the exit-signal selector that occupies the low byte of
// the clone flag word (the CSIGNAL mask in the Linux UAPI).
const csignalMask = 0xff

// NOTE (MAZ-78, Phase 0): the classifier IsProcessClone is INTENTIONALLY not
// defined yet. The Phase-0 RED test in clone_flags_test.go exercises it and
// must fail (compile error: undefined) on current code. Implementing
// IsProcessClone is the work this ticket locks the spec for.
