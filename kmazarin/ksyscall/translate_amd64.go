//go:build amd64

package ksyscall

// x86ToSysID maps x86_64 Linux syscall numbers to platform-independent SysID.
// Uninitialized entries are SysIDInvalid (zero value).
var x86ToSysID = [512]SysID{
	0:   SysIDRead,
	1:   SysIDWrite,
	3:   SysIDClose,
	9:   SysIDMmap,
	10:  SysIDMprotect,
	11:  SysIDMunmap,
	12:  SysIDBrk,
	13:  SysIDRtSigaction,
	14:  SysIDRtSigprocmask,
	24:  SysIDSchedYield,
	28:  SysIDMadvise,
	35:  SysIDNanosleep,
	39:  SysIDGetpid,
	56:  SysIDClone,
	60:  SysIDExit,
	62:  SysIDKill,
	72:  SysIDFcntl,
	131: SysIDSigaltstack,
	157: SysIDPrctl,
	158: SysIDArchPrctl,
	186: SysIDGettid,
	200: SysIDTkill,
	202: SysIDFutex,
	203: SysIDSchedSetaffinity,
	204: SysIDSchedGetaffinity,
	206: SysIDIoSetup,
	218: SysIDSetTidAddress,
	228: SysIDClockGettime,
	231: SysIDExitGroup,
	233: SysIDEpollCtl,
	234: SysIDTgkill,
	257: SysIDOpenat,
	281: SysIDEpollPwait,
	290: SysIDEventfd,
	291: SysIDEpollCreate,
	302: SysIDPrlimit64,
	318: SysIDGetrandom,
}

// translateSyscallNum converts an x86_64 Linux syscall number to SysID.
//
//go:nosplit
func translateSyscallNum(num uint64) SysID {
	if num >= 512 {
		return SysIDInvalid
	}
	return x86ToSysID[num]
}
