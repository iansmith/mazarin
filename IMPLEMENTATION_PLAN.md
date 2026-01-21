# Implementation Plan for ID Allocation and Static Data Structures

## Overview
This plan covers the implementation of unique thread IDs, static data structures with spinlocks, and comprehensive testing.

---

## Group 1: StaticStack and StaticAllocator

### StaticStack (ds/static_stack.go)
```go
type StaticStack struct {
    Data []int16  // Backed by static array
    top  int      // Index of top element (-1 = empty)
    capacity int  // Max capacity
}

// Push(val int16) - adds at front/top, panics if full
// Pop() int16 - removes from front/top, panics if empty
// Peek() int16 - returns top without removing, panics if empty
// IsEmpty() bool
// IsFull() bool
// Size() int
// Capacity() int
```

### StaticAllocator (ds/static_allocator.go)
```go
type StaticAllocator struct {
    stack StaticStack
    max   int16
}

// NewStaticAllocator(max int) *StaticAllocator
//   - Creates allocator with capacity max
//   - Returns uninitialized allocator
// Init()
//   - Seeds stack with IDs 0..max-1 (INCLUDING 0)
//   - Pushes in order: max-1, max-2, ..., 1, 0
//   - Stack starts nearly full (top ID will be 0)
// Acquire() int16 - pops from stack (gets next available ID)
// Release(id int16) - pushes to stack (returns ID to pool)
```

**Note:** Stack will be nearly full after Init(), with IDs available starting from 0.

---

## Group 2: ID Allocation System with Constants

### Constants (kmazarin/threads.go)
```go
const (
    MaxPriests = 4
    MaxThreads = 8 * MaxPriests  // 32
)
```

### Global Allocators (kmazarin/threads.go)
```go
var (
    // Backing arrays - sized exactly to max
    threadIdStackData [MaxThreads]int16
    priestIdStackData [MaxPriests]int16

    // Allocators
    threadIdAllocator *ds.StaticAllocator  // Max size = 32
    priestIdAllocator *ds.StaticAllocator  // Max size = 4
)

// InitIdAllocators() - called early in InitThreads()
func InitIdAllocators() {
    // Thread ID allocator
    threadIdAllocator = ds.NewStaticAllocator(MaxThreads)
    threadIdAllocator.stack.Data = threadIdStackData[:]
    threadIdAllocator.Init()  // Seeds with IDs 0-31

    // Priest ID allocator
    priestIdAllocator = ds.NewStaticAllocator(MaxPriests)
    priestIdAllocator.stack.Data = priestIdStackData[:]
    priestIdAllocator.Init()  // Seeds with IDs 0-3
}
```

**Key Points:**
- Thread IDs: 0-31 (32 total)
- Priest IDs: 0-3 (4 total)
- IDs start at 0, not 1
- Init() pushes them in reverse order so 0 comes out first

---

## Group 3: Spinlock Implementation

### Spinlock (ds/spinlock.go)
```go
type Spinlock struct {
    locked uint32  // 0 = unlocked, 1 = locked (must be 32-bit for atomic ops)
}

// Lock() - acquires lock
//   Implementation options:
//   Option A: Use sync/atomic.CompareAndSwapUint32 (Go standard)
//   Option B: ARM64 assembly (LDAXR/STXR loop)
//
//   Strategy:
//   1. Try to acquire with CAS in a tight loop (few iterations)
//   2. If still can't acquire after N attempts:
//      - Add self to deadline queue with deadline = now + timer interval
//      - Yield to scheduler
//      - Retry when deadline expires
//
//   Note: For kernel code, we may want to avoid yielding if in nosplit context.
//   Consider having LockSpin() that never yields vs Lock() that can yield.

// Unlock() - releases lock
//   - atomic.StoreUint32(&s.locked, 0) with memory ordering
//   - Or ARM64 STLR instruction for release semantics
//   - MUST NOT be interruptible (atomic operation ensures this)
```

**ARM64 Notes:**
- LDAXR: Load-acquire exclusive register (acquire semantics)
- STXR: Store exclusive register (returns 0 on success)
- STLR: Store-release register (release semantics)
- These provide necessary memory ordering guarantees

**Decision:**
- Start with Go's sync/atomic for correctness
- Can optimize to assembly later if needed
- Spinlock MUST be safe in nosplit context

---

## Group 4: Integrate Spinlocks into Static* Types

### Add to all Static* types:
- `StaticList` → add `Lock Spinlock` field
- `StaticQueue` → add `Lock Spinlock` field
- `StaticStack` → add `Lock Spinlock` field
- `StaticOrderedList` → add `Lock Spinlock` field
- `StaticAllocator` → add `Lock Spinlock` field

### Locking Strategy:
```go
// Example for StaticList.Allocate()
func (l *StaticList[T]) Allocate() (int, *T) {
    l.Lock.Lock()
    defer l.Lock.Unlock()

    // ... allocation logic ...
}
```

### Critical Sections:
- **Keep SHORT** - hold lock only during structure modification
- **No syscalls** while holding lock
- **No yielding** while holding lock if possible
- All mutating methods: Allocate, Release, Push, Pop, Pluck, Sort, etc.
- Read-only methods may not need locking if reads are atomic

**Challenge:** Many thread functions are `//go:nosplit`. If Spinlock.Lock() can yield, this breaks nosplit. Solutions:
1. Use spin-only lock (LockSpin) for nosplit contexts
2. Ensure critical sections are fast enough to never need yielding
3. Carefully audit which functions truly need nosplit

---

## Group 5: Refactor TID to Use Unique IDs

### Current Problem:
- TID is conflated with slot index
- When thread exits and slot is reused, TID is reused
- Queues might have stale TID references

### Phase 1: Add Slot Index Field
```go
type Thread struct {
    TID          int16   // Unique ID from threadIdAllocator (0-31)
    slotIndex    int16   // Position in threadListData array (may be different)
    State        int32
    FutexAddr    uint64
    MPtr         uint64
    GPtr         uint64
    EntryFunc    uint64
    PageTableL0PA uintptr
    Context      ThreadContext
    LastSeenG    uint64
    StartTick    uint64
    PreemptElapsed uint64
}
```

### Phase 2: Update Thread Allocation
```go
func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int16 {
    // Allocate slot from list
    slotIdx, t := threadList.Allocate()

    // Acquire unique ID from allocator
    tid := threadIdAllocator.Acquire()

    // Set both
    t.TID = tid
    t.slotIndex = int16(slotIdx)
    t.State = ThreadReady
    // ... rest of initialization ...

    return tid  // Return unique TID (NOT slot index)
}
```

### Phase 3: Update Thread Exit
```go
func ThreadExit() uintptr {
    t := threadList.Nth(int(currentThreadIdx))
    if t != nil {
        // Return unique ID to allocator
        threadIdAllocator.Release(t.TID)

        // Release slot in list
        threadList.Release(int(t.slotIndex))
    }
    // ... find next thread ...
}
```

### Phase 4: Update currentThreadIdx
- Rename to `currentThreadSlot` to be clear
- This tracks the slot index, NOT the TID
- Or keep name but ensure it's always set from `thread.slotIndex`

### Phase 5: Fix Queue Usage
**Queues store TID (unique ID), not slot index:**
```go
// Correct: queues use TID
readyQueue.Push(t.TID)
tid := readyQueue.Pop()

// Find thread by TID
t := threadList.FindById(int32(tid))
```

**FindById implementation needs to scan all slots:**
```go
func (l *StaticList[T]) FindById(id int32) *T {
    for i := 0; i < len(l.InUse); i++ {
        if l.InUse[i] && l.Data[i].Id() == int32(l.Data[i].TID) {
            return &l.Data[i]
        }
    }
    return nil
}
```

### Phase 6: Update Thread.Id() Implementation
```go
func (t Thread) Id() int32 {
    return int32(t.TID)  // Return unique ID, not slot index
}
```

### Phase 7: Audit All TID Usage
**Scan for these patterns and fix:**
- `t.TID` where slot index is needed → use `t.slotIndex`
- `int16(slotIdx)` being assigned to TID → use `threadIdAllocator.Acquire()`
- Assumptions that TID == slot index → fix logic

---

## Group 6: Unit Tests

### Test Files to Create:

#### ds/static_stack_test.go
```go
func TestStackPushPop(t *testing.T)
func TestStackCapacity(t *testing.T)
func TestStackPanicOnEmpty(t *testing.T)
func TestStackPanicOnFull(t *testing.T)
func TestStackPeek(t *testing.T)
```

#### ds/static_allocator_test.go
```go
func TestAllocatorInit(t *testing.T)           // Verify 0..max-1 available
func TestAllocatorAcquireRelease(t *testing.T)  // Basic cycle
func TestAllocatorExhaustion(t *testing.T)      // Panic when empty
func TestAllocatorDoubleRelease(t *testing.T)   // Should handle gracefully
func TestAllocatorIdUniqueness(t *testing.T)    // No duplicate IDs
func TestAllocatorStartsAtZero(t *testing.T)    // First ID is 0
```

#### ds/static_list_test.go
```go
func TestListAllocateRelease(t *testing.T)
func TestListFindById(t *testing.T)
func TestListExhaustion(t *testing.T)
func TestListConcurrent(t *testing.T)  // If using spinlocks
```

#### ds/static_queue_test.go
```go
func TestQueueFIFO(t *testing.T)
func TestQueueCapacity(t *testing.T)
func TestQueuePushPop(t *testing.T)
func TestQueuePluck(t *testing.T)
```

#### ds/static_ordered_list_test.go
```go
func TestOrderedListAscending(t *testing.T)
func TestOrderedListDescending(t *testing.T)
func TestOrderedListPopFirst(t *testing.T)
func TestOrderedListSort(t *testing.T)
```

#### ds/spinlock_test.go
```go
func TestSpinlockBasic(t *testing.T)
func TestSpinlockConcurrent(t *testing.T)
func TestSpinlockNoDeadlock(t *testing.T)
```

#### kmazarin/threads_test.go (or threads_simulation_test.go)
```go
func TestThreadAllocationUniqueTID(t *testing.T)
func TestThreadExitReleasesTID(t *testing.T)
func TestThreadTIDReuse(t *testing.T)
func TestThreadSlotVsTID(t *testing.T)
func TestThreadQueueing(t *testing.T)
func TestSimulateScheduling(t *testing.T)
func TestSimulateBlocking(t *testing.T)
func TestSimulateSleeping(t *testing.T)
```

---

## Implementation Order

1. **Group 1** - StaticStack + StaticAllocator
   - Implement StaticStack with tests
   - Implement StaticAllocator with Init() method
   - Verify Init() seeds 0..max-1

2. **Group 2** - Constants + Global Allocators
   - Add MaxPriests=4, MaxThreads=32
   - Create global allocator instances
   - Add InitIdAllocators() and call from InitThreads()

3. **Group 3** - Spinlock
   - Implement using sync/atomic
   - Add tests for correctness
   - Document nosplit constraints

4. **Group 4** - Add Spinlocks to Static* Types
   - Add Lock field to each type
   - Add Lock/Unlock to all mutating methods
   - Keep critical sections minimal

5. **Group 5** - Refactor TID System
   - Add slotIndex field to Thread
   - Update CloneThread to use threadIdAllocator
   - Update ThreadExit to release ID
   - Audit and fix all TID vs slot index usage
   - Update currentThreadIdx handling

6. **Group 6** - Comprehensive Testing
   - Write unit tests for each component
   - Write integration tests for thread system
   - Write simulation tests for scheduling scenarios

---

## Key Design Decisions

### StaticAllocator Initialization
- **Seeds with 0..max-1** (not 1..max)
- First Acquire() returns 0
- Must call Init() after setting up backing array

### Thread ID Space
- **Thread IDs: 0-31** (MaxThreads=32)
- **Priest IDs: 0-3** (MaxPriests=4)
- IDs are unique and never reused simultaneously
- IDs are returned to pool on thread exit

### Spinlock Strategy
- Use atomic operations for portability
- Keep critical sections SHORT
- Consider nosplit constraints carefully
- May need separate spin-only lock for nosplit contexts

### Queue Storage
- Queues store unique TID values
- Lookup requires scan via FindById()
- This is acceptable - FindById() already exists and is used

---

## Testing Strategy

### Unit Tests
- Test each component in isolation
- Focus on edge cases (empty, full, boundary conditions)
- Test error conditions (panics where expected)

### Integration Tests
- Test thread allocation → queuing → scheduling → exit cycle
- Verify ID uniqueness across cycles
- Verify no ID leaks

### Simulation Tests
- Simulate realistic scheduling scenarios
- Create threads, block some, wake others
- Verify deadline queue ordering
- Verify no lost threads or IDs

---

## Success Criteria

1. ✓ StaticStack works correctly with tests passing
2. ✓ StaticAllocator Init() seeds 0..max-1
3. ✓ Thread IDs are unique and never reused simultaneously
4. ✓ Slot index and TID are properly separated
5. ✓ All Static* types are thread-safe with spinlocks
6. ✓ All tests pass
7. ✓ System boots and runs both priests correctly
8. ✓ No panics or crashes under normal operation
