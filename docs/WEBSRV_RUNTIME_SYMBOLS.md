# Runtime Package Symbol Groups

The `runtime` package contains **1,407 symbols** - the largest package in the binary. These symbols are organized into functional groups below.

## Symbol Categories

### 1. Memory Management & Garbage Collection (219 symbols)

Core memory allocation, garbage collection, heap management, and memory spans.

**Key symbols:**
```
runtime.(*gcCPULimiterState).accumulate
runtime.(*gcCPULimiterState).finishGCTransition
runtime.(*gcCPULimiterState).resetCapacity
runtime.(*gcCPULimiterState).startGCTransition
runtime.(*gcCPULimiterState).unlock
runtime.(*gcCPULimiterState).update
runtime.(*gcCPULimiterState).updateLocked
runtime.(*gcControllerState).addIdleMarkWorker
runtime.(*gcControllerState).commit
runtime.(*gcControllerState).endCycle
runtime.(*gcControllerState).enlistWorker
runtime.(*gcControllerState).findRunnableGCWorker
runtime.(*gcControllerState).heapGoalInternal
runtime.(*gcControllerState).init
runtime.(*gcControllerState).markWorkerStop
runtime.(*gcControllerState).memoryLimitHeapGoal
runtime.(*gcControllerState).removeIdleMarkWorker
runtime.(*gcControllerState).resetLive
runtime.(*gcControllerState).revise
runtime.(*gcControllerState).setMaxIdleMarkWorkers
runtime.(*gcControllerState).startCycle
runtime.(*gcControllerState).trigger
runtime.(*gcControllerState).update
runtime.(*gcWork).balance
runtime.(*gcWork).dispose
runtime.(*gcWork).init
runtime.(*gcWork).put
runtime.(*gcWork).putBatch
runtime.(*gcWork).tryGet
runtime.(*mcache).allocLarge
... (and 189 more)
```

### 2. Goroutine & Scheduling (42 symbols)

Goroutine creation, context switching, scheduling, parking, and readying.

**Key symbols:**
```
runtime.(*scavengerState).park
runtime.casgstatus
runtime.casgstatus.func1
runtime.chanparkcommit
runtime.goexit.abi0
runtime.goexit0
runtime.goexit1
runtime.goexit1.abi0
runtime.gogo.abi0
runtime.gopark
runtime.goready
runtime.goready.func1
runtime.netpollready
runtime.newproc
runtime.newproc.abi0
runtime.newproc.func1
runtime.newproc1
runtime.newprocs
runtime.park_m
runtime.parkunlock_c
runtime.ready
runtime.readyWithTime
runtime.schedule
runtime.selparkcommit
runtime.traceGoUnpark
... (and 12 more)
```

### 3. Channel Operations (21 symbols)

Channel send, receive, select operations.

**Key symbols:**
```
runtime.chanparkcommit
runtime.chanrecv
runtime.chanrecv.func1
runtime.chanrecv1
runtime.chanrecvpc
runtime.chansend
runtime.chansend.func1
runtime.chansend1
runtime.chansendpc
runtime.closechan
runtime.makechan
```

### 4. Synchronization Primitives (78 symbols)

Mutexes, semaphores, atomic operations, read-write locks.

**Key symbols:**
```
runtime.(*gcCPULimiterState).unlock
runtime.(*lockRank).String
runtime.(*rwmutex).rlock
runtime.(*rwmutex).rlock.func1
runtime.(*rwmutex).runlock
runtime.(*semaRoot).dequeue
runtime.(*semaRoot).queue
runtime.(*semaRoot).rotateLeft
runtime.(*semaRoot).rotateRight
runtime.(*spanSetBlockAlloc).alloc
runtime.allglock
runtime.atomicstorep
runtime.atomicwb
runtime.badunlockosthread
runtime.blockevent
runtime.blockprofilerate
runtime.casGFromPreempted
runtime.casGToPreemptScan
runtime.casfrom_Gscanstatus
runtime.casgstatus
runtime.casgstatus.func1
runtime.castogscanstatus
runtime.cgoCheckTypedBlock
runtime.cgoCheckTypedBlock.func1
runtime.deadlock
runtime.debuglock
runtime.entersyscallblock
runtime.entersyscallblock.func1
runtime.entersyscallblock.func2
runtime.entersyscallblock_handoff
... (and 48 more)
```

### 5. Panic & Error Handling (51 symbols)

Panic, recover, fatal errors, throwing exceptions.

**Key symbols:**
```
runtime._cgo_panic_internal
runtime.crash
runtime.crashing
runtime.dopanic_m
runtime.fatal
runtime.fatal.func1
runtime.fatalpanic
runtime.fatalpanic.func1
runtime.fatalpanic.func2
runtime.fatalthrow
runtime.fatalthrow.func1
runtime.freedeferpanic
runtime.gopanic
runtime.gorecover
runtime.panicCheck1
runtime.panicCheck2
runtime.panicIndex
runtime.panicIndexU
runtime.panicSlice3Acap
runtime.panicSlice3Alen
runtime.panicSlice3AlenU
runtime.panicSlice3C
runtime.panicSliceAcap
runtime.panicSliceAcapU
runtime.panicSliceAlen
runtime.panicSliceAlenU
runtime.panicSliceB
runtime.panicSliceBU
runtime.panicdivide
runtime.panicdottypeE
... (and 21 more)
```

### 6. Map Operations (54 symbols)

Hash map creation, access, deletion, iteration, growth.

**Key symbols:**
```
runtime.(*hmap).newoverflow
runtime.bulkBarrierBitmap
runtime.makemap
runtime.makemap_small
runtime.mapaccess1
runtime.mapaccess1_fast32
runtime.mapaccess1_fast64
runtime.mapaccess1_faststr
runtime.mapaccess2
runtime.mapaccess2_fast32
runtime.mapaccess2_fast64
runtime.mapaccess2_faststr
runtime.mapaccessK
runtime.mapassign
runtime.mapassign_fast32
runtime.mapassign_fast64
runtime.mapassign_fast64ptr
runtime.mapassign_faststr
runtime.mapdelete
runtime.mapdelete_fast32
runtime.mapdelete_fast64
runtime.mapdelete_faststr
runtime.mapiterinit
runtime.mapiternext
runtime.mmap
runtime.mmap_trampoline.abi0
runtime.munmap.abi0
runtime.munmap_trampoline.abi0
runtime.pinnedTypemaps
runtime.textsectionmap
... (and 24 more)
```

### 7. Profiling & Tracing (54 symbols)

CPU profiling, memory profiling, execution tracing, block profiling.

**Key symbols:**
```
runtime.(*spanSetBlockAlloc).alloc
runtime.(*traceAlloc).alloc
runtime.(*traceStackTable).newStack
runtime.(*traceStackTable).put
runtime.blockprofilerate
runtime.gentraceback
runtime.inittrace
runtime.mutexprofilerate
runtime.profilealloc
runtime.schedtrace
runtime.schedtrace.func1
runtime.setprofilebucket
runtime.spanSetBlockPool
runtime.trace
runtime.traceAcquireBuffer
runtime.traceCPUSample
runtime.traceEvent
runtime.traceEventLocked
runtime.traceFlush
runtime.traceGCSweepDone
runtime.traceGCSweepSpan
runtime.traceGCSweepStart
runtime.traceGoCreate
runtime.traceGoPark
runtime.traceGoSched
runtime.traceGoStart
runtime.traceGoSysBlock
runtime.traceGoSysCall
runtime.traceGoSysExit
runtime.traceGoUnpark
... (and 24 more)
```

### 8. Stack Management (50 symbols)

Stack growth, copying, unwinding, frame inspection, caller information.

**Key symbols:**
```
runtime.(*lfstack).push
runtime.(*stackScanState).addObject
runtime.(*stackScanState).getPtr
runtime.(*stackScanState).putPtr
runtime.Caller
runtime.adjustframe
runtime.badmorestackg0
runtime.badmorestackg0.abi0
runtime.badmorestackg0Msg
runtime.badmorestackgsignal
runtime.badmorestackgsignal.abi0
runtime.badmorestackgsignalMsg
runtime.badsystemstack
runtime.badsystemstack.abi0
runtime.badsystemstackMsg
runtime.callers
runtime.callers.func1
runtime.copystack
runtime.gcallers
runtime.maxstackceiling
runtime.maxstacksize
runtime.morestack.abi0
runtime.morestack_noctxt.abi0
runtime.morestackc
runtime.morestackc.abi0
runtime.newstack
runtime.newstack.abi0
runtime.pthread_attr_getstacksize.abi0
runtime.pthread_attr_getstacksize_trampoline.abi0
runtime.scanframeworker
... (and 20 more)
```

### 9. Debugging & Printing (52 symbols)

Print functions, debug output, dumping structures.

**Key symbols:**
```
runtime.debug
runtime.debugCallCheck
runtime.debugCallCheck.abi0
runtime.debugCallCheck.func1
runtime.debugCallPanicked.abi0
runtime.debugCallV2
runtime.debugCallWrap
runtime.debugCallWrap.abi0
runtime.debugCallWrap.func1
runtime.debugCallWrap.func2
runtime.debugCallWrap1
runtime.debugCallWrap1.func1
runtime.debugCallWrap2
runtime.debugCallWrap2.func1
runtime.debuglock
runtime.dumpregs
runtime.hexdumpWords
runtime.parsedebugvars
runtime.preprintpanics
runtime.preprintpanics.func1
runtime.printAncestorTraceback
runtime.printAncestorTracebackFuncInfo
runtime.printArgs
runtime.printArgs.func1
runtime.printArgs.func2
runtime.printBacklog
runtime.printBacklogIndex
runtime.printCgoTraceback
runtime.printOneCgoTraceback
runtime.printScavTrace
... (and 22 more)
```

### 10. Type System & Interfaces (38 symbols)

Type assertions, interface conversions, method calls, type algorithms.

**Key symbols:**
```
runtime.(*_type).pkgpath
runtime.(*_type).string
runtime.(*_type).textOff
runtime.(*_type).uncommon
runtime.(*_type).uncommon.jump5
runtime.alginit
runtime.assertE2I
runtime.assertE2I2
runtime.assertI2I
runtime.assertI2I2
runtime.efaceeq
runtime.etypes
runtime.ifaceeq
runtime.malg
runtime.malg.func1
runtime.methodValueCallFrameObjs
runtime.panicdottypeE
runtime.panicdottypeI
runtime.printanycustomtype
runtime.printanycustomtype.jump4
runtime.typeBitsBulkBarrier
runtime.typedmemclr
runtime.typedmemmove
runtime.typedslicecopy
runtime.typehash
runtime.typehash.jump14
runtime.typelink
runtime.typelinksinit
runtime.types
runtime.typesEqual
... (and 8 more)
```

### 11. CGO & C Interface (38 symbols)

CGO calls, C memory management, callback handling.

**Key symbols:**
```
runtime._cgo_panic_internal
runtime._cgo_setenv
runtime._cgo_unsetenv
runtime.asmcgocall
runtime.asmcgocall.abi0
runtime.asmcgocall_no_g.abi0
runtime.cgoAlwaysFalse
runtime.cgoCheckArg
runtime.cgoCheckArg.jump8
runtime.cgoCheckBits
runtime.cgoCheckMemmove
runtime.cgoCheckPointer
runtime.cgoCheckSliceCopy
runtime.cgoCheckTypedBlock
runtime.cgoCheckTypedBlock.func1
runtime.cgoCheckUnknownPointer
runtime.cgoCheckUsingType
runtime.cgoCheckWriteBarrier
runtime.cgoCheckWriteBarrier.func1
runtime.cgoContextPCs
runtime.cgoHasExtraM
runtime.cgoIsGoPointer
runtime.cgoSigtramp.abi0
runtime.cgoSymbolizer
runtime.cgoTraceback
runtime.cgoUse
runtime.cgo_yield
runtime.cgocall
runtime.cgocallback.abi0
runtime.cgocallbackg
runtime.cgocallbackg.abi0
runtime.cgocallbackg1
runtime.cgocallbackg1.func1
runtime.cgocallbackg1.func2
runtime.cgocallbackg1.func3
runtime.earlycgocallback
runtime.iscgo
runtime.ncgocall
```

### 12. String Operations (37 symbols)

String concatenation, conversions, comparisons, slicing.

**Key symbols:**
```
runtime.(*_type).string
runtime.cmpstring
runtime.concatstring2
runtime.concatstring3
runtime.concatstring4
runtime.concatstring5
runtime.concatstrings
runtime.convTstring
runtime.gostring
runtime.intstring
runtime.printstring
runtime.rawstring
runtime.rawstringtmp
runtime.slicebytetostring
runtime.slicerunetostring
runtime.stringEface
runtime.stringType
runtime.stringtoslicebyte
... (and 7 more)
```

### 13. Memory Operations (29 symbols)

Memory copying, moving, clearing, equality checks.

**Key symbols:**
```
runtime.memclrHasPointers
runtime.memclrNoHeapPointers
runtime.memclrNoHeapPointersChunked
runtime.memequal
runtime.memequal0
runtime.memequal128
runtime.memequal16
runtime.memequal32
runtime.memequal64
runtime.memequal8
runtime.memequal_varlen
runtime.memmove
runtime.typedmemclr
runtime.typedmemmove
```

### 14. Signal Handling (20 symbols)

Signal handling, preemption, signal trampolines.

**Key symbols:**
```
runtime.badginsignalMsg
runtime.badmorestackgsignal
runtime.badmorestackgsignal.abi0
runtime.badmorestackgsignalMsg
runtime.badsignal
runtime.gopreempt_m
runtime.preemptM
runtime.preemptPark
runtime.preemptall
runtime.preemptone
runtime.pthread_cond_signal.abi0
runtime.pthread_cond_signal_trampoline.abi0
runtime.raisebadsignal
runtime.signalDuringFork
runtime.signalsOK
runtime.signalstack
runtime.sigpanic
runtime.sigtramp.abi0
runtime.sigtrampgo
runtime.sigtrampgo.abi0
```

### 15. Initialization (79 symbols)

Runtime initialization, main entry point, argument setup, OS initialization.

**Key symbols:**
```
runtime.(*workbuf).checkempty
runtime.(*workbuf).checknonempty
runtime.args
runtime.args.abi0
runtime.argslice
runtime.check
runtime.check.abi0
runtime.checkASM.abi0
runtime.checkIdleGCNoP
runtime.checkRunqsNoP
runtime.checkTimers
runtime.checkTimersNoP
runtime.checkdead
runtime.checkdead.func1
runtime.checkmcount
runtime.goargs
runtime.init
runtime.main
runtime.main.func1
runtime.main.func2
runtime.mainPC
runtime.mainStarted
runtime.main_init_done
runtime.mstart0
runtime.mstart0.abi0
runtime.osinit
runtime.osinit.abi0
runtime.rt0_go.abi0
runtime.schedinit
runtime.schedinit.abi0
... (and 49 more)
```

### 16. OS Thread Management (14 symbols)

M (machine/OS thread) creation, starting, stopping, parking.

**Key symbols:**
```
runtime.gcstopm
runtime.handoffp
runtime.mpreinit
runtime.mstart.abi0
runtime.mstart0
runtime.mstart0.abi0
runtime.mstart1
runtime.mstart_stub.abi0
runtime.mstartm0
runtime.newm
runtime.newm1
runtime.newmHandoff
runtime.startm
runtime.stopm
```

### 17. Finalizers (7 symbols)

Object finalization, SetFinalizer support.

**Key symbols:**
```
runtime.SetFinalizer
runtime.SetFinalizer.func1
runtime.SetFinalizer.func2
runtime.addfinalizer
runtime.finalizer1
runtime.queuefinalizer
runtime.removefinalizer
```

### 18. Processor Management (6 symbols)

P (logical processor) allocation, idle management, resizing.

**Key symbols:**
```
runtime.acquirep
runtime.pidleget
runtime.pidleput
runtime.procresize
runtime.releasep
runtime.wirep
```

### 19. Timers (35 symbols)

Timer operations, sleep, nanosecond timing, wall clock.

**Key symbols:**
```
runtime.(*scavengerState).sleep
runtime.addAdjustedTimers
runtime.addtimer
runtime.adjusttimers
runtime.adjusttimers.jump17
runtime.badTimer
runtime.checkTimers
runtime.checkTimersNoP
runtime.cleantimers
runtime.clearDeletedTimers
runtime.clearDeletedTimers.jump12
runtime.deltimer
runtime.deltimer.jump7
runtime.doaddtimer
runtime.dodeltimer
runtime.dodeltimer0
runtime.faketime
runtime.modtimer
runtime.modtimer.jump12
runtime.moveTimers
runtime.moveTimers.jump12
runtime.nanotime
runtime.nanotime1.abi0
runtime.nanotime_trampoline.abi0
runtime.notesleep
runtime.notetsleep
runtime.notetsleep_internal
runtime.notetsleepg
runtime.pthread_cond_timedwait_relative_np.abi0
runtime.pthread_cond_timedwait_relative_np_trampoline.abi0
runtime.readyWithTime
runtime.runOneTimer
runtime.runtimeInitTime
runtime.runtimer
runtime.runtimer.jump12
```

### 20. Write Barriers (1 symbol)

Write barriers for garbage collection.

**Key symbols:**
```
runtime.writeBarrier
```

## Summary

The runtime package implements the complete Go runtime system:

- **Memory Management**: Full GC with concurrent mark/sweep
- **Concurrency**: Goroutine scheduler, channels, sync primitives
- **Type System**: Interface dispatch, type assertions, reflection support
- **Safety**: Panic/recover, bounds checking, nil checks
- **Observability**: Profiling, tracing, debugging
- **OS Integration**: Thread management, signals, CGO support

Note: Some symbols may appear in multiple categories due to overlapping functionality.

---

*Complete list of all 1,407 runtime symbols available in WEBSRV_STDLIB_SYMBOLS.md*
