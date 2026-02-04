# The Big Fish: Layout-Sensitive Latent Bug

## Summary

The system has a latent bug that causes functional failures when the kmazarin
binary layout changes. Any source change that produces different compiler output
can trigger it. The bug is not in the relocator -- it's somewhere in cardinal's
exception handling, page fault paths, or the syscall bridge.

## Evidence

### 1. gopclntab false-positive relocation (FIXED, commit e033de0)

The relocator was blindly scanning `.gopclntab` for 8-byte values in the
kmazarin address range and adding `0xFFFFFFFF00000000` to them. In Go 1.18+,
`.gopclntab` uses offsets (not absolute pointers) for everything except
`pcHeader.textStart` at offset 24. The blind scan corrupted runtime metadata,
causing crashes during stack scanning.

**Fix:** Remove `.gopclntab` from the blind pointer scan; explicitly relocate
only `pcHeader.textStart`.

This was a real bug but not the big fish.

### 2. threadArraySize experiment

`threads.go` has `const threadArraySize = 1024` as a fixed backing array size
for thread-related arrays, even though `MaxThreads = 16`. The original comment
said this was a "workaround for ADRP-related crash during Go runtime stack
scanning."

We tested changing `threadArraySize` from 1024 to `MaxThreads` (16):

- **MaxThreads=16, threadArraySize=16:** No crash, but priests hang. Serial
  output stops at `[main] stdio launched` -- the Serial Console window never
  renders. QEMU sampling shows the CPU is actively running (context switching,
  reading timers, CAS operations), but no priest produces output.

- **MaxThreads=12, threadArraySize=12:** Hard crash after priests launch:
  `FAIL EL=1 FAR=FFFFFFFF43E0C000 ELR=FFFFFFFF4385BB54` (FAR == SP, suggesting
  stack overflow or guard page fault).

- **MaxThreads=16, threadArraySize=1024:** Everything works. Full boot, priests
  render the Serial Console window with framebuffer output.

### 3. The relocator is not the cause

We compared verbose relocation output between the working (1024) and broken (16)
builds:

| Metric | Good (1024) | Bad (16) |
|--------|-------------|----------|
| Detected range | 0x43800000 - 0x43C00000 | 0x43800000 - 0x43C00000 |
| ADRP instructions | 9965 | 9965 |
| .data relocations | 1008 | 1008 |
| .rodata relocations | 7641 | 7641 |
| .noptrdata relocations | 81 | 81 |
| .itablink relocations | 36 | 36 |
| .text literal pool | 0 | 0 |
| Total | 18736 | 18736 |

The relocations differ only in *which values* are at each location (pointers
shifted because BSS moved), not in count or which locations are relocated.
9 `.rodata` locations swap between builds (different offsets), but each build
has the same count -- these are layout shifts, not false positives.

### 4. Section layout is identical except .noptrbss size

```
Section         Good (1024)              Bad (16)
.text           0x43800000  size 0xB4934  same
.rodata         0x438C0000               same
.data           0x439C5080               same
.bss            0x439CBAE0  size 0x2BD68  same
.noptrbss       0x439F7860  size 0x187E00 size 0x118260
```

The .noptrbss shrinks by 457,632 bytes (the smaller arrays), but all other
section start addresses are identical.

### 5. The Go compiler produces globally different code

Despite identical section addresses, the pre-relocation binaries differ by
**2875 bytes in .text** spread across hundreds of functions:

```
internal/cpu.doinit
runtime.mapaccess1_fast64
runtime.chansend
runtime.persistentalloc1
runtime.gcStart.func4
runtime.gcDrain
runtime.(*mheap).allocSpan
runtime.releaseSudog
runtime.newm
sync.poolCleanup
os.init
fmt.(*pp).printValue
mazzy/kmazarin/kmem.TrackPage
main.initVirtIOInputDevices
main.PushTimerEventAndWake
... (many more)
```

A single constant change (1024 -> 16) causes the Go compiler to make different
register allocation and inlining decisions across the entire binary. The
.noptrbss size change shifts BSS-resident variable addresses, which changes the
immediates in instructions that reference them, which cascades into different
code generation globally.

### 6. QEMU PC sampling during the hang

With the broken build (threadArraySize=16), sampling the PC 5 times over 10
seconds showed:

```
PC=ffffffff438b18c0  main.YieldToReadyThread.abi0      (eret instruction)
PC=ffffffff43896b60  mazzy/kmazarin/kirq.asm_readCntvctEl0.abi0
PC=ffffffff438b1844  main.YieldToReadyThread.abi0
PC=ffffffff4389affc  mazzy/kmazarin/ds.CompareAndSwapUint32.abi0
PC=ffffffff4389affc  mazzy/kmazarin/ds.CompareAndSwapUint32.abi0
```

The system is alive -- threads are being scheduled, timers are being read,
locks are being contended. But no priest makes progress. This suggests a
scheduling or syscall issue, not a crash.

## Conclusion

There is a latent bug that is **not** in the relocator and **not** in the
data layout. It manifests as either a hang or crash depending on the exact
.text layout. The bug is somewhere that is sensitive to code addresses or
instruction sequences -- likely:

1. **Cardinal's exception vector / syscall dispatch** -- if it uses hardcoded
   offsets or makes assumptions about instruction alignment
2. **Page fault handling** -- if demand paging or permission faults interact
   badly with certain PC values
3. **Context switch save/restore** -- if register save areas are corrupted or
   misaligned for certain thread states
4. **The Go runtime's stack scanning** -- if it misinterprets stack contents
   when function layouts change (though the gopclntab fix should have addressed
   the metadata corruption path)

The `threadArraySize = 1024` workaround masks the bug by keeping the binary
layout stable. The real fix requires finding why certain binary layouts cause
priests to deadlock.

## How to Reproduce

```bash
# Working build
# threads.go: const threadArraySize = 1024
$GO tool task run TIMEOUT=15
# -> Serial Console window appears, full output

# Broken build
# threads.go: const threadArraySize = MaxThreads
$GO tool task run TIMEOUT=15
# -> Stops at "[main] stdio launched", no Serial Console window
# -> QEMU PC sampling shows active scheduling but no progress

# Pre-built comparison ELFs saved at:
#   /tmp/kmazarin-good.elf      (threadArraySize=1024, pre-reloc)
#   /tmp/kmazarin-bad.elf       (threadArraySize=16, pre-reloc)
#   /tmp/kmazarin-good-reloc.elf (post-reloc)
#   /tmp/kmazarin-bad-reloc.elf  (post-reloc)
```
