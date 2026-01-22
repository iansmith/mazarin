# Lower Half Interrupt Handlers: Futex-Based Design

## Problem Statement

Interrupt handlers (upper half) run in exception context with interrupts disabled. They must complete quickly to avoid missing other interrupts and to maintain system responsiveness. However, some interrupt handling requires complex processing that cannot safely run in exception context - this work is traditionally deferred to the "lower half" or "bottom half."

Currently, there's no clean mechanism for the upper half to signal the lower half that work is available, nor a way to prioritize this deferred work over normal threads.

## Proposed Architecture

### Core Concept: Lower Half as Blocked Thread

Each interrupt source (or category of interrupts) has a dedicated **lower half thread** that:

1. Runs as a normal thread in the scheduler
2. Blocks on a futex-like primitive when no work is pending
3. Gets woken by the upper half when work arrives
4. Processes work items from a ring buffer
5. Returns to blocked state when the ring is empty

This model treats interrupt processing as a producer-consumer problem:
- **Producer**: Upper half (runs in exception context, very fast)
- **Consumer**: Lower half thread (runs in thread context, can do complex work)

### Upper Half Responsibilities (Exception Context)

When an interrupt fires, the upper half handler:

1. Acknowledges the interrupt hardware (e.g., write to GICC_EOIR)
2. Pushes minimal data into a **ring buffer** (e.g., IRQ number, timestamp, device-specific info)
3. Changes the lower half thread's state from `Blocked` to `Ready`
4. Adds the lower half thread to the **front** of the ready queue (priority handling)
5. Returns immediately (total time: ~100 cycles)

The upper half NEVER does:
- Memory allocation
- Complex data structure manipulation
- Acquiring locks that lower half might hold
- Any operation that could block or take unbounded time

### Lower Half Responsibilities (Thread Context)

The lower half thread runs this loop:

```
forever:
    while ring_buffer is not empty:
        item = ring_buffer.pop()
        process_interrupt(item)  // Can do complex work, allocate, etc.

    futex_wait(&wakeup_flag, 0)  // Block until upper half signals
```

Because it runs in thread context, the lower half CAN:
- Allocate memory
- Acquire/release locks
- Call complex Go functions
- Take as long as needed (will be preempted fairly)

### Ring Buffer Design (Virtio-Style)

A simple ring buffer suffices, similar to virtio's virtqueue:

```
struct IRQRingBuffer {
    entries [256]IRQEntry  // Power of 2 for fast modulo
    head    uint32         // Upper half writes here (atomic)
    tail    uint32         // Lower half reads here (atomic)
}

struct IRQEntry {
    irq_num   uint32
    timestamp uint64
    data      uint64       // Device-specific (e.g., UART character, disk block)
}
```

**Upper half push** (lock-free):
```
idx = atomic_fetch_add(&head, 1) % 256
entries[idx] = {irq_num, timestamp, data}
memory_barrier()
```

**Lower half pop** (lock-free):
```
if head == tail: return empty
idx = tail % 256
item = entries[idx]
atomic_store(&tail, tail + 1)
return item
```

### Priority: Front of Ready Queue

Normal preemption adds threads to the **back** of the ready queue (FIFO fairness). Lower half threads go to the **front** because:

1. Interrupt latency matters - hardware may be waiting
2. The work is usually small (process one event, then block again)
3. Starving lower halves can cause hardware buffer overflows

This requires a small extension to the ready queue:
```go
func (q *ReadyQueue) PushFront(tid ThreadId)  // For lower half
func (q *ReadyQueue) PushBack(tid ThreadId)   // Normal preemption
```

### Example: UART Lower Half

**Upper half** (in timer IRQ or dedicated UART IRQ):
```
if uart_has_data():
    char = read_uart_data_register()
    ring_buffer.push({IRQ_UART_RX, now(), char})
    wake_lower_half(uart_lower_half_thread)
```

**Lower half thread**:
```
forever:
    while item = ring_buffer.pop():
        match item.irq_num:
            IRQ_UART_RX:
                // Can do complex processing here
                append_to_line_buffer(item.data)
                if item.data == '\n':
                    process_complete_line()

    futex_wait(&wakeup_flag, 0)
```

### Benefits

1. **Testability**: Lower half is pure Go code, easily unit tested
2. **Safety**: Complex logic runs in thread context with full Go runtime
3. **Latency**: Upper half completes in ~100 cycles
4. **Priority**: Lower halves run before normal threads
5. **Simplicity**: Uses existing primitives (futex, ready queue)

### Implementation Notes

- Each lower half thread should be created at boot, not dynamically
- The futex wake in upper half must be safe (no locks, no allocation)
- Ring buffer size should match expected interrupt rate
- Consider per-CPU lower half threads if SMP is added later
