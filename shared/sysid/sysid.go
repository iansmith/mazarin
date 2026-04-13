// Package sysid defines platform-independent syscall identifiers.
// Both the kernel (kmazarin) and userspace (mazarin) import this package
// so that shepherds can register to handle specific syscalls portably.
package sysid

// ID is a platform-independent syscall identifier.
// The kernel's dispatch table is indexed by ID, not native Linux syscall numbers.
// Each architecture has a translation table mapping native numbers → ID.
type ID uint16

const (
	Invalid          ID = iota // sentinel — zero value for unmapped entries
	IoSetup                    // io_setup
	Eventfd                    // eventfd2
	EpollCreate                // epoll_create1
	EpollCtl                   // epoll_ctl
	EpollPwait                 // epoll_pwait
	Fcntl                      // fcntl
	Openat                     // openat
	Close                      // close
	Read                       // read
	Write                      // write
	Exit                       // exit
	ExitGroup                  // exit_group
	SetTidAddress              // set_tid_address
	Futex                      // futex
	Nanosleep                  // nanosleep
	ClockGettime               // clock_gettime
	SchedGetaffinity           // sched_getaffinity
	SchedYield                 // sched_yield
	Kill                       // kill
	Tkill                      // tkill
	Tgkill                     // tgkill
	Sigaltstack                // sigaltstack
	RtSigaction                // rt_sigaction
	RtSigprocmask              // rt_sigprocmask
	ArchPrctl                  // arch_prctl (x86_64 only)
	Prctl                      // prctl
	Getpid                     // getpid
	Gettid                     // gettid
	SchedSetaffinity           // sched_setaffinity
	Brk                        // brk
	Munmap                     // munmap
	Clone                      // clone
	Mmap                       // mmap
	Mprotect                   // mprotect
	Madvise                    // madvise
	Prlimit64                  // prlimit64
	Getrandom                  // getrandom
	RtSigreturn                // rt_sigreturn
	Getitimer                  // getitimer
	LoadFile                   // LoadFile (Mazzy delegated file load)
	ReadFilePages              // ReadFilePages (Mazzy delegated file read into DMA pages)

	// File-related syscalls delegated to the linux shepherd.
	Lseek      // lseek
	Fstat      // fstat
	Fstatat    // fstatat / newfstatat
	Mkdirat    // mkdirat
	Unlinkat   // unlinkat
	Renameat   // renameat / renameat2
	Ftruncate  // ftruncate
	Getdents64 // getdents64
	Readlinkat // readlinkat
	Faccessat  // faccessat
	Fchmodat   // fchmodat
	Utimensat  // utimensat
	Getcwd     // getcwd
	Chdir      // chdir
	Fchdir     // fchdir
	Ioctl      // ioctl
	Writev     // writev
	Readv      // readv
	Statfs     // statfs
	Fstatfs    // fstatfs
	Fsync      // fsync
	Fdatasync  // fdatasync
	Flock      // flock
	Pread64    // pread64
	Pwrite64   // pwrite64

	MmapPageFill // internal: kernel → linux shepherd page fill for file-backed mmap

	NumIDs // sentinel — array size
)
