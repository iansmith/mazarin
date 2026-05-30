// MAZ-77 producer helper: turn a dying child Shepherd + its raw exit status
// into the NotificationEvent the kernel publishes to the parent's ring.
//
// This is the pure, host-testable core of the child-exit notification path.
// The kmazarin-package call site (TerminateShepherd) is a thin adapter that
// looks up the dying child, calls this helper, and — when ok is true — hands
// the event to KernelPublishProcessNotify for the returned parent SID.
//
// ExitStatus carries the RAW exit code. The kernel does NOT apply the Linux
// (status<<8) wait4 encoding here; that Linux-ABI knowledge stays in the
// linux shepherd (MAZ-80), keeping the kernel layer ABI-agnostic.
//
// Marked //go:nosplit to mirror the other proc notification helpers and keep
// it callable from a nosplit context if ever needed; it is pure and cheap.

package proc

// BuildChildExitEvent constructs the EventChildExit notification for a dying
// child and reports the SID of the parent that should receive it.
//
// Returns ok=false when the child has no parent (ParentPID == 0, i.e. a
// boot-time system service): there is no parent ring to notify, so the caller
// must skip delivery. In that case the returned event and target SID are the
// zero value.
//
// The PIDs are narrowed to int16 at the wire boundary exactly as the existing
// notification code does — the [MinPID, MaxPID] range fits in int16.
//
//go:nosplit
func BuildChildExitEvent(child *Shepherd, status int32) (NotificationEvent, ShepherdId, bool) {
	if child.ParentPID == 0 {
		return NotificationEvent{}, 0, false
	}
	ev := NotificationEvent{
		Type:       EventChildExit,
		Pid:        int16(child.PID),
		ParentPid:  int16(child.ParentPID),
		ExitStatus: status,
	}
	return ev, child.ParentPID, true
}
