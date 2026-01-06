//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// Complete runtime type definitions matching Go runtime exactly
// Based on Go runtime's runtime2.go

// Supporting types
type guintptr uintptr // Pointer-sized integer for goroutine pointers

type runtimeStack struct {
	lo uintptr
	hi uintptr
}

type runtimeGobuf struct {
	sp   uintptr
	pc   uintptr
	g    uintptr // guintptr
	ctxt unsafe.Pointer
	lr   uintptr
	bp   uintptr // for framepointer-enabled architectures
	// NOTE: Go 1.25.5's gobuf does NOT have a 'ret' field!
	// Total: 48 bytes (6 fields × 8 bytes)
}

// runtimeG matches Go runtime's g struct EXACTLY
// All fields present, unused fields remain zero
type runtimeG struct {
	// Stack parameters (offsets 0-31)
	stack       runtimeStack
	stackguard0 uintptr
	stackguard1 uintptr

	_panic       unsafe.Pointer // *_panic
	_defer       unsafe.Pointer // *_defer
	m            *runtimeM
	sched        runtimeGobuf
	syscallsp    uintptr
	syscallpc    uintptr
	syscallbp    uintptr
	stktopsp     uintptr
	param        unsafe.Pointer
	atomicstatus uint32
	stackLock    uint32
	goid         uint64
	schedlink    uintptr // guintptr
	waitsince    int64
	waitreason   uint32 // waitReason

	preempt          bool
	preemptStop      bool
	preemptShrink    bool
	asyncSafePoint   bool
	paniconfault     bool
	gcscandone       bool
	throwsplit       bool
	activeStackChans bool
	parkingOnChan    uint32 // atomic.Bool
	inMarkAssist     bool
	coroexit         bool

	raceignore      int8
	nocgocallback   bool
	tracking        bool
	trackingSeq     uint8
	trackingStamp   int64
	runnableTime    int64
	lockedm         uintptr // muintptr
	fipsIndicator   uint8
	fipsOnlyBypass  bool
	syncSafePoint   bool
	runningCleanups uint32 // atomic.Bool
	sig             uint32
	secret          int32
	writebuf        []byte
	sigcode0        uintptr
	sigcode1        uintptr
	sigpc           uintptr
	parentGoid      uint64
	gopc            uintptr
	ancestors       unsafe.Pointer // *[]ancestorInfo
	startpc         uintptr
	racectx         uintptr
	waiting         unsafe.Pointer // *sudog
	cgoCtxt         []uintptr
	labels          unsafe.Pointer
	timer           unsafe.Pointer // *timer
	sleepWhen       int64
	selectDone      uint32 // atomic.Uint32

	goroutineProfiled uint64 // goroutineProfileStateHolder

	coroarg unsafe.Pointer // *coro
	bubble  unsafe.Pointer // *synctestBubble

	xRegs [64]byte  // xRegPerG (simplified)
	trace [128]byte // gTraceState (simplified)

	gcAssistBytes   int64
	valgrindStackID uintptr
}

// runtimeM matches Go runtime's m struct EXACTLY
// All fields present, unused fields remain zero
// CRITICAL: These sizes must match Go 1.25.5 runtime exactly:
//   - morebuf (gobuf): 48 bytes (6 fields, NO ret field)
//   - goSigStack (gsignalStack): 40 bytes (stack:16 + stackguard0:8 + stackguard1:8 + stktopsp:8)
//   - sigmask (sigset for linux arm64): 8 bytes ([2]uint32)
// curg offset should be 0xB8 (184 bytes)
type runtimeM struct {
	g0      *runtimeG
	morebuf runtimeGobuf
	divmod  uint32

	procid       uint64
	gsignal      *runtimeG
	goSigStack   [40]byte // gsignalStack: stack(16) + stackguard0(8) + stackguard1(8) + stktopsp(8) = 40 bytes
	sigmask      [8]byte  // sigset for linux arm64: [2]uint32 = 8 bytes
	tls          [6]uintptr
	mstartfn     uintptr // func() (simplified)
	curg         *runtimeG
	caughtsig    uintptr // guintptr
	signalSecret uint32

	p               uintptr // puintptr
	nextp           uintptr // puintptr
	oldp            uintptr // puintptr
	id              int64
	mallocing       int32
	throwing        uint32  // throwType
	preemptoff      uintptr // string (simplified)
	locks           int32
	dying           int32
	profilehz       int32
	spinning        bool
	blocked         bool
	newSigstack     bool
	printlock       int8
	incgo           bool
	isextra         bool
	isExtraInC      bool
	isExtraInSig    bool
	freeWait        uint32 // atomic.Uint32
	needextram      bool
	g0StackAccurate bool
	traceback       uint8
	allpSnapshot    unsafe.Pointer // []*p (simplified)
	ncgocall        uint64
	ncgo            int32
	cgoCallersUse   uint32         // atomic.Uint32
	cgoCallers      unsafe.Pointer // *cgoCallers
	park            [16]byte       // note (simplified)
	alllink         *runtimeM
	schedlink       uintptr  // muintptr
	idleNode        [32]byte // listNodeManual (simplified)
	lockedg         uintptr  // guintptr
	createstack     [32]uintptr
	lockedExt       uint32
	lockedInt       uint32
	mWaitList       [64]byte       // mWaitList (simplified)
	mLockProfile    [128]byte      // mLockProfile (simplified)
	profStack       unsafe.Pointer // []uintptr (simplified)

	waitunlockf          uintptr // func(*g, unsafe.Pointer) bool (simplified)
	waitlock             unsafe.Pointer
	waitTraceSkip        int
	waitTraceBlockReason uint32 // traceBlockReason

	syscalltick uint32
	freelink    *runtimeM
	trace       [64]byte // mTraceState (simplified)

	libcallpc  uintptr
	libcallsp  uintptr
	libcallg   uintptr  // guintptr
	winsyscall [64]byte // winlibcall (simplified)

	vdsoSP uintptr
	vdsoPC uintptr

	preemptGen    uint32 // atomic.Uint32
	signalPending uint32 // atomic.Uint32

	pcvalueCache [32]byte  // pcvalueCache (simplified)
	dlogPerM     [64]byte  // dlogPerM (simplified)
	mOS          [128]byte // mOS (simplified)

	chacha8   [64]byte // chacha8rand.State (simplified)
	cheaprand uint64

	locksHeldLen int
	locksHeld    [10][16]byte // [10]heldLockInfo (simplified)

	self [8]byte // mWeakPointer (simplified)
}
