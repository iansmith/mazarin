# Priest Overlay

Priest does NOT override the syscall mechanism. It uses standard Go syscalls
which execute real SVC instructions to trap into kmazarin.

This is intentional:
- kmazarin overlay: direct function call (no SVC, already in kernel)
- priest overlay: none - uses real SVC to reach kernel

Other userspace programs (shepherds, .maz plugins) also make real SVCs
directly to the kernel; the previous "route through priest" model went
away with the userspace runtime overlay (MAZ-46 Phase 1). Whether the
priest overlay itself is still load-bearing is a separate question.
