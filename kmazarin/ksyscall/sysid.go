package ksyscall

import "mazzy/shared/sysid"

// SysID is a platform-independent syscall identifier.
// Alias of sysid.ID so existing kernel code doesn't need to change.
type SysID = sysid.ID

// Re-export all constants so existing kernel code compiles unchanged.
const (
	SysIDInvalid          = sysid.Invalid
	SysIDIoSetup          = sysid.IoSetup
	SysIDEventfd          = sysid.Eventfd
	SysIDEpollCreate      = sysid.EpollCreate
	SysIDEpollCtl         = sysid.EpollCtl
	SysIDEpollPwait       = sysid.EpollPwait
	SysIDFcntl            = sysid.Fcntl
	SysIDOpenat           = sysid.Openat
	SysIDClose            = sysid.Close
	SysIDRead             = sysid.Read
	SysIDWrite            = sysid.Write
	SysIDExit             = sysid.Exit
	SysIDExitGroup        = sysid.ExitGroup
	SysIDSetTidAddress    = sysid.SetTidAddress
	SysIDFutex            = sysid.Futex
	SysIDNanosleep        = sysid.Nanosleep
	SysIDClockGettime     = sysid.ClockGettime
	SysIDSchedGetaffinity = sysid.SchedGetaffinity
	SysIDSchedYield       = sysid.SchedYield
	SysIDKill             = sysid.Kill
	SysIDTkill            = sysid.Tkill
	SysIDTgkill           = sysid.Tgkill
	SysIDSigaltstack      = sysid.Sigaltstack
	SysIDRtSigaction      = sysid.RtSigaction
	SysIDRtSigprocmask    = sysid.RtSigprocmask
	SysIDArchPrctl        = sysid.ArchPrctl
	SysIDPrctl            = sysid.Prctl
	SysIDGetpid           = sysid.Getpid
	SysIDGettid           = sysid.Gettid
	SysIDSchedSetaffinity = sysid.SchedSetaffinity
	SysIDBrk              = sysid.Brk
	SysIDMunmap           = sysid.Munmap
	SysIDClone            = sysid.Clone
	SysIDMmap             = sysid.Mmap
	SysIDMprotect         = sysid.Mprotect
	SysIDMadvise          = sysid.Madvise
	SysIDPrlimit64        = sysid.Prlimit64
	SysIDGetrandom        = sysid.Getrandom
	SysIDRtSigreturn      = sysid.RtSigreturn
	SysIDGetitimer        = sysid.Getitimer

	// File-related syscalls delegated to the linux shepherd.
	SysIDLseek      = sysid.Lseek
	SysIDFstat      = sysid.Fstat
	SysIDFstatat    = sysid.Fstatat
	SysIDMkdirat    = sysid.Mkdirat
	SysIDUnlinkat   = sysid.Unlinkat
	SysIDRenameat   = sysid.Renameat
	SysIDFtruncate  = sysid.Ftruncate
	SysIDGetdents64 = sysid.Getdents64
	SysIDReadlinkat = sysid.Readlinkat
	SysIDFaccessat  = sysid.Faccessat
	SysIDFchmodat   = sysid.Fchmodat
	SysIDUtimensat  = sysid.Utimensat
	SysIDGetcwd     = sysid.Getcwd
	SysIDChdir      = sysid.Chdir
	SysIDFchdir     = sysid.Fchdir
	SysIDIoctl      = sysid.Ioctl
	SysIDWritev     = sysid.Writev
	SysIDReadv      = sysid.Readv
	SysIDStatfs     = sysid.Statfs
	SysIDFstatfs    = sysid.Fstatfs
	SysIDFsync      = sysid.Fsync
	SysIDFdatasync  = sysid.Fdatasync
	SysIDFlock      = sysid.Flock
	SysIDPread64      = sysid.Pread64
	SysIDPwrite64     = sysid.Pwrite64
	SysIDRiscvHWProbe = sysid.RiscvHWProbe

	NumSyscallIDs = sysid.NumIDs
)
