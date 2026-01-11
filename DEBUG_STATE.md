# Debug State Summary - mcall on g0 Stack Crash

## Problem
After context switch to M1, the Go runtime crashes with:
```
fatal error: runtime: mcall called on m->g0 stack
```

## Key Findings

### Debug Output Before Crash
```
{SAVE t=0 x28=FFFFFFFF4197BF20}  // M0's X28 = runtime.g0 (BSS address)
{REST t=1 x28=FFFF000148002540}  // M1's X28 = heap-allocated g0
~c=FFFFFFFF419A7EB0~Z[g=FFFF000148002540][PC=FFFFFFFF41872880]
(40)Wfatal error: runtime: mcall called on m->g0 stack
```

### Timeline Analysis
1. Context switch correctly saves M0's state (thread slot 0)
2. Context switch correctly restores M1's state (thread slot 1)
3. M1 starts executing at PC=0xFFFFFFFF41872880 (mstart)
4. **NO syscalls from M1 before crash** - first syscall (40=write=64) is error printing
5. This means M1 crashes BEFORE `minit()` which would call `gettid()` (syscall 178=0xB2)

### Crash Location
- Crash happens somewhere in `mstart0` -> `mstart1` path before `minit()` even runs
- `mstart1` at proc.go:1911 calls:
  1. `getg()` - check gp != gp.m.g0
  2. Set up g0.sched
  3. `asminit()` - no-op on arm64
  4. `minit()` - never reaches this (would call gettid)

### mcall Behavior (asm_arm64.s:215)
```asm
TEXT runtime·mcall<ABIInternal>(SB), NOSPLIT|NOFRAME, $0-8
    MOVD    R0, R26                 // context
    // Save caller state in g->sched
    MOVD    RSP, R0
    MOVD    R0, (g_sched+gobuf_sp)(g)
    MOVD    g, R3                   // Save current g
    MOVD    g_m(g), R8
    MOVD    m_g0(R8), g             // Switch to m->g0
    BL      runtime·save_g(SB)
    CMP     g, R3                   // Check if already on g0
    BNE     2(PC)
    B       runtime·badmcall(SB)   // <-- CRASH HERE if g == R3
```

The crash happens because:
- Something calls `mcall` while X28 (g register) is already pointing to g0
- The CMP at line 230 finds g == R3 (current g is same as m->g0)

### Hypothesis
Could be stack guard triggering morestack -> newstack -> mcall.
Was about to investigate morestack at asm_arm64.s:352

## Files Modified in This Session
1. `src/kmazarin/golang/kmazarin/threads.go` - Added debug output to SaveContextFromFrame and doContextSwitchImpl
2. `src/kmazarin/golang/ksyscall/dispatch.go` - Added syscall number printing in format `(XX)`

## Next Steps to Investigate
1. Check if morestack is being triggered (stack guard issue)
2. Look at morestack implementation at asm_arm64.s:352
3. Verify M1's g0.stackguard0 is properly initialized
4. Add debug output to morestack to confirm if it's being hit
5. Check if g0.stack.lo/hi values are correct for M1

## Relevant Code Locations
- `mstart0()`: proc.go:1869
- `mstart1()`: proc.go:1911
- `mcall`: asm_arm64.s:215
- `morestack`: asm_arm64.s:352
- `badmcall()`: proc.go:574
- Thread context switch: threads.go

## Thread Slot Status (Fixed Earlier)
- Thread 0: M0 (reserved, correctly initialized)
- Thread 1: M1 (first clone)
- Thread 2: M2 (second clone)
