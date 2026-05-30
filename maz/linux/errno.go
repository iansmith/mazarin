package main

// Linux errno values (negated, as returned by syscalls).
const (
	EOK          int64 = 0
	EPERM        int64 = -1
	ENOENT       int64 = -2
	EIO          int64 = -5
	EBADF        int64 = -9
	ECHILD       int64 = -10
	ENOMEM       int64 = -12
	EACCES       int64 = -13
	EFAULT       int64 = -14
	EEXIST       int64 = -17
	ENOTDIR      int64 = -20
	EISDIR       int64 = -21
	EINVAL       int64 = -22
	EMFILE       int64 = -24
	ENOTTY       int64 = -25
	ENOSPC       int64 = -28
	ESPIPE       int64 = -29
	EROFS        int64 = -30
	ENAMETOOLONG int64 = -36
	ENOSYS       int64 = -38
	ENOTEMPTY    int64 = -39
	EWOULDBLOCK  int64 = -11 // EAGAIN
)
