// clone_exec request + buffered-intent types (MAZ-75).
//
// The linux shepherd buffers in-child setup syscalls (dup3, close, chdir,
// fcntl(F_SETFD)) between a process-flavor clone and its matching execve.
// On execve, the shepherd flushes the buffered intent and issues a single
// kernel-internal clone_exec call. This file declares the wire shape of
// that call (CloneExecRequest) and the buffered ops (CloneExecIntentOp).
//
// Storage discipline:
//
//   - CloneExecRequest is a TRANSIENT struct passed to the kernel worker
//     thread (DoCloneExecWork). The worker has heap access — the request
//     uses slices freely (Argv, Envp, Intent, Cwd, Filename).
//
//   - The buffered intent is COPIED into the new child Shepherd's
//     long-term storage (Shepherd.StartupIntent, Shepherd.StartupCwd,
//     etc.). Those fields use fixed arrays per kernel "no heap" discipline,
//     matching the existing Environ / Children pattern.
//
// Application discipline:
//
//   - The kernel only STORES the intent. The new shepherd's startup stub
//     (in maz/linux, landed via MAZ-78/79) reads Shepherd.StartupIntent
//     and applies the ops (dup3/close/F_SETFD against the shepherd-side
//     FD table at maz/linux/fdtable.go; chdir against ShepherdFilesystemData
//     cwd) BEFORE exec'ing the target ELF entry point.
//
//   - This split exists because FD tables live in linux-shepherd userspace,
//     not kernel memory — the kernel has no dup3/close/F_SETFD primitives
//     to invoke directly. See findings.md for the full architectural note.

package proc

import "mazzy/shared/linuxabi"

// SINGLE SOURCE OF TRUTH (MAZ-118 locked decision).
//
// The clone_exec buffered-intent encoding lives ONCE in shared/linuxabi and is
// imported by BOTH the kernel (here) and the linux shepherd
// (maz/linux/internal/cloneexec). Below we reconcile proc to that definition
// via Go TYPE ALIASES (`=`) and const re-exports, so the already-merged MAZ-75
// kernel call sites (Shepherd.StartupIntent array dimensions, ksyscall
// cap-checks, `{Kind: IntentDup3, ...}` literals) compile unchanged while there
// is no second copy to drift. See shared/linuxabi/cloneexec.go for the spec.

// Sizing caps, re-exported from linuxabi. Used as array dimensions
// (Shepherd.StartupIntent / StartupCwd) and ksyscall cap-checks.
const (
	// MaxStartupIntentOps caps the number of FD-flavored intent ops the
	// kernel stores per shepherd.
	MaxStartupIntentOps = linuxabi.MaxStartupIntentOps

	// MaxStartupCwdBytes caps the chdir target path stored on the child
	// shepherd.
	MaxStartupCwdBytes = linuxabi.MaxStartupCwdBytes
)

// CloneExecIntentKind is an alias of linuxabi.IntentKind — the same type the
// shepherd buffers, so the two sides cannot drift.
type CloneExecIntentKind = linuxabi.IntentKind

// Kind sentinels, re-exported from linuxabi so existing call sites keep using
// the unqualified names.
const (
	IntentNone   = linuxabi.IntentNone   // sentinel / zero
	IntentDup3   = linuxabi.IntentDup3   // Arg0=oldfd, Arg1=newfd, Arg2=flags
	IntentClose  = linuxabi.IntentClose  // Arg0=fd
	IntentFSetFD = linuxabi.IntentFSetFD // Arg0=fd, Arg1=flags (FD_CLOEXEC, etc.)
)

// CloneExecIntentOp is an alias of linuxabi.IntentOp. Size is pinned at 16
// bytes by the shared definition (the per-Shepherd StartupIntent array budget
// assumes 16-byte ops). Chdir is NOT an op kind — it lives directly on
// Shepherd.StartupCwd, not here.
type CloneExecIntentOp = linuxabi.IntentOp

// CloneExecRequest is the transient work-request passed from the shepherd-
// side caller (eventually maz/linux's execve dispatch, MAZ-79) to the kernel
// worker thread's DoCloneExecWork. Slices are OK here because the worker
// thread has heap access (matches the existing RunShepherdWorkRequest +
// DoRunShepherdWork pattern).
type CloneExecRequest struct {
	// --- ELF source ---
	// The shepherd-side caller has pre-loaded the ELF binary into its own
	// VA (typically via fsclient IPC against the fs shepherd). The kernel
	// reads via copyPagesFromUser, matching DoRunShepherdWork.
	ELFStartVA  uintptr
	ELFNumBytes int
	ELFNumPages int
	CallerL0PA  uintptr // caller's L0 page table root for the user-page walk

	// --- Parent identity ---
	// Kernel wires new child's ParentPID = CallerShepherd.PID and calls
	// CallerShepherd.AddChild(newPID).
	CallerShepherd *Shepherd

	// --- Stack setup ---
	// Argv / envp for the new shepherd's setupUserStack call.
	Argv [][]byte
	Envp [][]byte

	// --- Buffered intent (copied into child Shepherd.StartupIntent / Cwd) ---
	Intent []CloneExecIntentOp // FD-flavored ops; cap-checked against MaxStartupIntentOps
	Cwd    []byte              // chdir target; empty = no chdir; cap-checked against MaxStartupCwdBytes

	// --- PID reservation (MAZ-127 vfork) ---
	// ReservedPID is the child PID reserved at clone time. 0 = no reservation
	// (boot path); non-zero = adopt this PID instead of allocating a fresh one.
	// Set by SyscallCloneExec from the clone-time linkage.
	ReservedPID ShepherdId

	// TransientTID is the TID of the transient vfork thread that issued the
	// CloneExec SVC. Used by DoCloneExecWork to wake the parked parent — the
	// worker runs on thread 0, so GetCurrentThreadTID() there would return the
	// wrong TID. 0 = non-vfork path (boot).
	TransientTID int32

	// --- Diagnostics ---
	Filename []byte // shepherd-name for logging + symbol table cache
}
