# Constraint VM: Kernel Execution Concerns

Analysis of major concerns for running the constraint VM inside kmazarin,
analogous to how BPF runs inside the Linux kernel.

## 1. Allocation and GC Pressure

The report's "no heap allocation" guarantee applies to the *source language*,
not the *interpreter*. The interpreter itself allocates freely: every `Value`
with a string or collection field (`str string`, `coll []Value`) is a Go heap
object. Builtins like `str_concat`, `coll_take`, `coll_sort` all create new
slices and strings. In the kernel with GOMEMLIMIT=64MiB, a layout pass
touching dozens of constraint attributes could generate meaningful garbage. At
GOGC=100% that's fine in bursts, but a 60fps compositor re-evaluating dirty
constraints every frame would create sustained GC pressure.

## 2. Compilation vs. Execution Boundary

BPF compiles in userspace, loads bytecode into the kernel. The constraint VM
currently uses `go/parser` and `go/types` -- these are enormous standard
library packages that pull in `go/ast`, `go/token`, `go/constant`, etc. You
almost certainly don't want these in the kernel. The natural split: a userspace
tool or priest compiles restricted Go to bytecode, then a syscall loads the
verified `Program` struct into the kernel. But that means
serializing/deserializing the program (including the string table), and the
kernel needs its own verifier pass on the loaded bytecode -- you can't trust
userspace's claim that a program is safe.

## 3. The String Problem

Go strings are immutable heap-allocated byte slices. In user-space UI code
that's fine. In the kernel, every string concatenation, substring, or
comparison allocates. For layout constraints, strings are mostly used for
things like text measurement keys or label content -- you might be able to get
away with a string-interning table or replacing strings with integer IDs at the
kernel boundary. But the current `Value` representation bakes `string` into
every value.

## 4. Stack Safety and Nosplit Chains

The VM interpreter is a `for`/`switch` loop that could execute up to 100,000
instructions. That's fine for normal Go code, but if constraint evaluation is
ever triggered from a context near a nosplit chain (timer IRQ -> display
compositor -> dirty attribute -> VM eval), you'd hit the nosplit stack limit.
The interpreter must never be reachable from interrupt top-halves. You need to
ensure evaluation only happens in a proper goroutine context with a growable
stack.

## 5. Preemption Interaction

The kernel requires async preemption enabled. A long-running VM `Run()` call
(even if bounded by fuel) is a cooperative scheduling black hole until Go
injects a preemption point. For most constraint programs (10-100 instructions)
this is a non-issue, but a pathological program iterating over a large
collection could hold the CPU long enough to delay signal delivery or timer
processing. BPF solves this by being *fast* (JIT-compiled, register machine);
the stack-machine interpreter with tagged values and type-checking on every
operation is comparatively slow.

## 6. The Closure Problem in the Attribute Graph

The attribute integration layer uses Go closures everywhere: `VMDep.extract` is
a `func() vm.Value`, and each constraint attribute's compute function is a
closure capturing the program and dependency list. Every time you build or
rebuild the attribute graph, you allocate closure objects. In the kernel, if the
graph is static after setup this is a one-time cost. But if widgets are
created/destroyed dynamically (scrolling list, tab switching), you're
continuously allocating and collecting closures.

## 7. Where Does the Attribute Graph Live?

This is the architectural question underneath all the others. In BPF, the
kernel runs individual programs in response to events -- there's no persistent
graph. The attribute system with dirty propagation, generation counters, and
lazy evaluation is a much richer runtime structure. If the whole graph lives in
the kernel, you're committing a significant chunk of your 64MiB budget to UI
layout state. If it lives in a priest (like stdio), then the VM is just a
userspace library and the "run in kernel like BPF" analogy doesn't quite apply
-- the kernel would only need to run individual constraint programs on behalf of
a priest, not manage the graph.

## Priority Assessment

**#2 (compilation boundary)** and **#7 (where the graph lives)** are the
architectural decisions that need to be made first, because they determine how
much of the other problems actually need to be solved. If the answer is
"priests own the attribute graph, kernel just runs individual verified programs
via syscall," then #1 and #6 largely disappear from the kernel side, and you're
left with #3 (strings) and #4 (stack safety) as the hard implementation
problems.
