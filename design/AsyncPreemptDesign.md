# Async Preemption Design (Legacy Document)

**NOTE**: This document has been superseded by **SchedulingAndPreemption.md** which provides comprehensive documentation of the entire scheduling and preemption system.

Please refer to [SchedulingAndPreemption.md](SchedulingAndPreemption.md) for:
- Complete scheduling architecture
- All three scheduling paths (initial start, timer preemption, syscall context switch)
- Data structures and their critical fields
- Async preemption injection mechanism
- InCloneSetup protection (originally documented here)
- Timer IRQ handling and deadline-based preemption
- Critical invariants and assumptions
- Debugging tips and testing checklist

---

## Quick Reference: InCloneSetup Protection

The InCloneSetup flag protects clone child threads from stack corruption during async preemption. This is documented in detail in SchedulingAndPreemption.md, but the key points are:

1. Clone children have fn/gp/mp stored on stack at SP-8, SP-16, SP-24
2. Async preemption writes LR/R29 at SP-8, SP-16 - this would corrupt the clone data
3. Solution: Set `thread.InCloneSetup = 1` in CloneThread, clear on first syscall
4. Timer handler skips async preemption when InCloneSetup is set

See SchedulingAndPreemption.md for full details.
