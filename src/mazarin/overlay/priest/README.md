# Priest Overlay

Priest does NOT override the syscall mechanism. It uses standard Go syscalls
which execute real SVC instructions to trap into kmazarin.

This is intentional:
- kmazarin overlay: direct function call (no SVC, already in kernel)
- priest overlay: none - uses real SVC to reach kernel
- userspace overlay: function pointer to priest (no SVC, direct call)

Priest is the only userspace program that makes real syscalls to the kernel.
All other userspace programs route their syscalls through priest.
