//go:build arm64

package ksyscall

// arm64ToSysID maps ARM64 Linux syscall numbers to platform-independent SysID.
// Uninitialized entries are SysIDInvalid (zero value).
var arm64ToSysID = [512]SysID{
	0:   SysIDIoSetup,
	19:  SysIDEventfd,
	20:  SysIDEpollCreate,
	21:  SysIDEpollCtl,
	22:  SysIDEpollPwait,
	25:  SysIDFcntl,
	56:  SysIDOpenat,
	57:  SysIDClose,
	63:  SysIDRead,
	64:  SysIDWrite,
	93:  SysIDExit,
	94:  SysIDExitGroup,
	96:  SysIDSetTidAddress,
	98:  SysIDFutex,
	101: SysIDNanosleep,
	102: SysIDGetitimer,
	113: SysIDClockGettime,
	123: SysIDSchedGetaffinity,
	124: SysIDSchedYield,
	129: SysIDKill,
	130: SysIDTkill,
	131: SysIDTgkill,
	132: SysIDSigaltstack,
	134: SysIDRtSigaction,
	135: SysIDRtSigprocmask,
	139: SysIDRtSigreturn,
	167: SysIDPrctl,
	172: SysIDGetpid,
	178: SysIDGettid,
	204: SysIDSchedSetaffinity,
	214: SysIDBrk,
	215: SysIDMunmap,
	220: SysIDClone,
	222: SysIDMmap,
	226: SysIDMprotect,
	233: SysIDMadvise,
	261: SysIDPrlimit64,
	278: SysIDGetrandom,

	// File-related syscalls delegated to the linux shepherd.
	62:  SysIDLseek,
	80:  SysIDFstat,
	79:  SysIDFstatat,
	34:  SysIDMkdirat,
	35:  SysIDUnlinkat,
	276: SysIDRenameat, // renameat2 on ARM64
	46:  SysIDFtruncate,
	61:  SysIDGetdents64,
	78:  SysIDReadlinkat,
	48:  SysIDFaccessat,
	53:  SysIDFchmodat,
	88:  SysIDUtimensat,
	17:  SysIDGetcwd,
	49:  SysIDChdir,
	50:  SysIDFchdir,
	29:  SysIDIoctl,
	66:  SysIDWritev,
	65:  SysIDReadv,
	43:  SysIDStatfs,
	44:  SysIDFstatfs,
	82:  SysIDFsync,
	83:  SysIDFdatasync,
}

// translateSyscallNum converts an ARM64 Linux syscall number to SysID.
//
//go:nosplit
func translateSyscallNum(num uint64) SysID {
	if num >= 512 {
		return SysIDInvalid
	}
	return arm64ToSysID[num]
}
