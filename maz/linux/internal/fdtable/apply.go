package fdtable

import "mazzy/shared/linuxabi"

// FD_CLOEXEC is the fcntl(F_SETFD) flag bit that marks an fd close-on-exec.
// It is the F_SETFD-flavored value (POSIX FD_CLOEXEC == 1), distinct from the
// open(2) O_CLOEXEC bit (OCLOEXEC == 0x80000): an IntentFSetFD op carries the
// fcntl flags in Arg1, so the cloexec bit is read with this mask.
const FD_CLOEXEC = 1

// EBADF is the Linux "Bad file descriptor" errno. ApplyStartupIntent returns
// -EBADF when an op references an fd that is out of range or not open.
const EBADF = 9

// ApplyStartupIntent applies a buffered clone_exec startup intent to the table
// BEFORE the child's first delegated FD syscall is serviced. STUB: no-op so
// the MAZ-113 tests compile and fail on their assertions, not on a build break.
func (t *Table) ApplyStartupIntent(ops []linuxabi.IntentOp, cwd []byte) int64 {
	_ = ops
	_ = cwd
	return 0
}
