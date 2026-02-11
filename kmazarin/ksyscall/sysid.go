package ksyscall

// SysID is a platform-independent syscall identifier.
// The dispatch table is indexed by SysID, not native Linux syscall numbers.
// Each architecture has a translation table mapping native numbers → SysID.
type SysID uint16

const (
	SysIDInvalid          SysID = iota // sentinel — zero value for unmapped entries
	SysIDIoSetup                       // io_setup
	SysIDEventfd                       // eventfd2
	SysIDEpollCreate                   // epoll_create1
	SysIDEpollCtl                      // epoll_ctl
	SysIDEpollPwait                    // epoll_pwait
	SysIDFcntl                         // fcntl
	SysIDOpenat                        // openat
	SysIDClose                         // close
	SysIDRead                          // read
	SysIDWrite                         // write
	SysIDExit                          // exit
	SysIDExitGroup                     // exit_group
	SysIDSetTidAddress                 // set_tid_address
	SysIDFutex                         // futex
	SysIDNanosleep                     // nanosleep
	SysIDClockGettime                  // clock_gettime
	SysIDSchedGetaffinity              // sched_getaffinity
	SysIDSchedYield                    // sched_yield
	SysIDKill                          // kill
	SysIDTkill                         // tkill
	SysIDTgkill                        // tgkill
	SysIDSigaltstack                   // sigaltstack
	SysIDRtSigaction                   // rt_sigaction
	SysIDRtSigprocmask                 // rt_sigprocmask
	SysIDArchPrctl                     // arch_prctl (x86_64 only)
	SysIDPrctl                         // prctl
	SysIDGetpid                        // getpid
	SysIDGettid                        // gettid
	SysIDSchedSetaffinity              // sched_setaffinity
	SysIDBrk                           // brk
	SysIDMunmap                        // munmap
	SysIDClone                         // clone
	SysIDMmap                          // mmap
	SysIDMprotect                      // mprotect
	SysIDMadvise                       // madvise
	SysIDPrlimit64                     // prlimit64
	SysIDGetrandom                     // getrandom

	NumSyscallIDs // sentinel — array size
)
