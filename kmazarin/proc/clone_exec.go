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

// Sizing constants. Tunable; chosen to cover the os/exec.Cmd pattern (3-5
// FD ops typical, occasional Cmd.Dir for chdir).
const (
	// MaxStartupIntentOps caps the number of FD-flavored intent ops the
	// kernel stores per shepherd. os/exec typically uses 3-5 (dup3 stdout +
	// dup3 stderr + close pipe ends + F_SETFD on inherited FDs). 16 covers
	// comfortably; bump if a real workload hits the cap.
	MaxStartupIntentOps = 16

	// MaxStartupCwdBytes caps the chdir target path stored on the child
	// shepherd. Linux PATH_MAX is 4096 but most paths are < 256.
	MaxStartupCwdBytes = 256
)

// CloneExecIntentKind discriminates the buffered FD-flavored ops.
// IntentNone is the sentinel zero value so unused trailing slots in
// Shepherd.StartupIntent don't get misinterpreted as real ops.
type CloneExecIntentKind uint8

const (
	IntentNone   CloneExecIntentKind = iota // sentinel / zero
	IntentDup3                              // Arg0=oldfd, Arg1=newfd, Arg2=flags
	IntentClose                             // Arg0=fd
	IntentFSetFD                            // Arg0=fd, Arg1=flags (FD_CLOEXEC, etc.)
)

// CloneExecIntentOp is one buffered FD-flavored op. Size is pinned at 16
// bytes (see TestCloneExecIntentOpSize); enlarging this struct inflates
// the per-Shepherd StartupIntent array, so any size change is deliberate.
//
// Chdir is NOT represented here — at most one chdir per clone_exec is
// meaningful, so it lives directly on Shepherd.StartupCwd instead of
// burning a 256-byte path field on every IntentOp.
type CloneExecIntentOp struct {
	Kind CloneExecIntentKind
	_pad [3]byte // alignment to 4 so Arg0 is 4-byte-aligned
	Arg0 int32
	Arg1 int32
	Arg2 int32
}

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

	// --- Diagnostics ---
	Filename []byte // shepherd-name for logging + symbol table cache
}
