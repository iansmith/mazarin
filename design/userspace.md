# Mazzy Userspace Architecture

## Overview

Mazzy's userspace implements a microkernel-inspired design where most system call handling occurs in userspace rather than the kernel. The kernel (kmazarin) provides minimal services, while **priest** acts as a syscall router that dispatches requests to specialized userspace servers.

## Components

### 1. Kmazarin (Kernel)
- Runs in high memory (`0xFFFFFFFF...`)
- Handles actual hardware access
- Provides Mazzy-specific syscalls (0x1000+)
- Minimal responsibilities: memory management, scheduling, hardware abstraction

### 2. Priest (Syscall Router)
- Special userspace program, first to run
- Routes syscalls from normal programs to appropriate handlers
- Makes real SVC traps to kmazarin for Mazzy syscalls
- Provides registration API for syscall servers
- Position-independent code

### 3. Normal Programs (helloworld, tty, fs, etc.)
- Standard Go programs compiled with userspace overlay
- Know nothing about priest internals
- Syscalls intercepted and routed to priest via function pointer
- Position-independent code

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                    KERNEL (kmazarin)                            │
│  - High memory (0xFFFFFFFF...)                                  │
│  - Overlay: RawSyscall6 → DispatchFromOverlay (direct call)     │
│  - Handles actual hardware, Mazzy syscalls (0x1000+)            │
│  - Location: src/kmazarin/golang/                               │
└─────────────────────────────────────────────────────────────────┘
                              ▲
                              │ SVC (real trap)
                              │
┌─────────────────────────────────────────────────────────────────┐
│                    PRIEST (syscall router)                      │
│  - Low memory, position-independent                             │
│  - Overlay: priest-overlay (real SVC for Mazzy syscalls)        │
│  - Receives syscalls from normal programs via function call     │
│  - Routes:                                                      │
│    • Mazzy syscalls (0x1000+) → handles directly via SVC        │
│    • Linux syscalls → dispatches to registered servers          │
│  - Provides registration API for syscall servers                │
│  - Location: src/flock/cmd/priest/                              │
└─────────────────────────────────────────────────────────────────┘
                              ▲
                              │ Function call (via patched pointer)
                              │
┌─────────────────────────────┴───────────────────────────────────┐
│              NORMAL PROGRAMS (helloworld, tty, fs, etc.)        │
│  - Low memory, position-independent                             │
│  - Overlay: userspace-overlay (routes to priest)                │
│  - Know nothing about priest internals                          │
│  - Standard Go programs using channels for IPC                  │
│  - Location: src/flock/cmd/*/                                   │
└─────────────────────────────────────────────────────────────────┘
```

## Overlay Structure

Three distinct overlays live in `src/mazarin/`:

| Overlay | Used By | Behavior |
|---------|---------|----------|
| `kmazarin-overlay.json` | kmazarin | `RawSyscall6` → `DispatchFromOverlay` (direct function call within kernel) |
| `priest-overlay.json` | priest | `RawSyscall6` → real SVC instruction (traps to kmazarin) |
| `userspace-overlay.json` | normal programs | `RawSyscall6` → `priestSyscallHandler` (function pointer, patched at load time) |

### Userspace Overlay Implementation

```go
// src/mazarin/overlay/userspace/syscall_linux.go
package syscall

// PriestSyscallHandler is patched by priest at program load time
// to point to priest's syscall dispatch function.
var PriestSyscallHandler func(num, a1, a2, a3, a4, a5, a6 uintptr) int64

func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
    if PriestSyscallHandler == nil {
        // Fallback or panic - priest not initialized
        return ^uintptr(0), 0, ENOSYS
    }

    result := PriestSyscallHandler(trap, a1, a2, a3, a4, a5, a6)

    if int64(result) < 0 {
        return ^uintptr(0), 0, Errno(-int64(result))
    }
    return uintptr(result), 0, 0
}
```

## Syscall Flow Examples

### Example 1: `sys.GetTime()` (Mazzy syscall)

```go
// helloworld.go
t, _ := sys.GetTime()
```

```
helloworld                    priest                      kmazarin
    │                            │                            │
    │ sys.GetTime()              │                            │
    │ → RawSyscall6(0x1000,...)  │                            │
    │                            │                            │
    │ ──function call──────────► │                            │
    │   (via patched pointer)    │                            │
    │                            │ Mazzy syscall detected     │
    │                            │ → real SVC(0x1000,...)     │
    │                            │                            │
    │                            │ ─────SVC trap────────────► │
    │                            │                            │ GetTime handler
    │                            │                            │ reads RTC/ticks
    │                            │ ◄────return───────────────│
    │                            │                            │
    │ ◄─────return──────────────│                            │
    │                            │                            │
    │ t = TimeSpec{...}          │                            │
```

### Example 2: `fmt.Printf()` (Linux syscall → tty server)

```go
// helloworld.go
fmt.Printf("hello world")
```

```
helloworld                    priest                      tty server
    │                            │                            │
    │ fmt.Printf(...)            │                            │
    │ → eventually write(1,...)  │                            │
    │ → RawSyscall6(64,...)      │                            │
    │                            │                            │
    │ ──function call──────────► │                            │
    │                            │                            │
    │                            │ Linux syscall detected     │
    │                            │ fd=1 → tty handler         │
    │                            │                            │
    │                            │ ────send on channel──────► │
    │                            │   SyscallRequest{          │
    │                            │     Num: 64,               │
    │                            │     Args: [...],           │
    │                            │     ResultCh: <-chan,      │
    │                            │     ErrorCh: <-chan,       │
    │                            │   }                        │
    │                            │                            │
    │                            │                            │ tty processes
    │                            │                            │ write request
    │                            │                            │ (may call Mazzy
    │                            │                            │  UART syscall)
    │                            │                            │
    │                            │ ◄───result on channel─────│
    │                            │                            │
    │ ◄─────return──────────────│                            │
```

## Priest Registration API

Userspace servers register with priest to handle specific syscalls or resources:

```go
// src/flock/cmd/priest/api.go
package priest

// SyscallRequest is sent to registered handlers
type SyscallRequest struct {
    Num      uint64      // Syscall number
    Args     [6]uint64   // Syscall arguments
    ResultCh chan<- int64    // Send success result here
    ErrorCh  chan<- int64    // Send error (negative errno) here
}

// RegisterTTY registers a handler for TTY-related syscalls (read/write on fd 0,1,2)
// Returns a channel on which syscall requests will be sent.
// Returns error if a TTY handler is already registered.
func RegisterTTY() (<-chan SyscallRequest, error)

// RegisterFS registers a handler for filesystem syscalls
func RegisterFS() (<-chan SyscallRequest, error)

// RegisterNet registers a handler for network syscalls
func RegisterNet() (<-chan SyscallRequest, error)
```

### Example: TTY Server

```go
// src/flock/cmd/tty/main.go
package main

import (
    "flock/cmd/priest"
    "mazarin/sys"
)

func main() {
    // Register as TTY handler
    requests, err := priest.RegisterTTY()
    if err != nil {
        panic(err)
    }

    // Handle syscall requests
    for req := range requests {
        switch req.Num {
        case 63: // read
            n, err := handleRead(req.Args)
            if err != nil {
                req.ErrorCh <- int64(err)
            } else {
                req.ResultCh <- int64(n)
            }
        case 64: // write
            n, err := handleWrite(req.Args)
            if err != nil {
                req.ErrorCh <- int64(err)
            } else {
                req.ResultCh <- int64(n)
            }
        default:
            req.ErrorCh <- -38 // ENOSYS
        }
    }
}

func handleWrite(args [6]uint64) (int, error) {
    fd := args[0]
    buf := args[1]  // pointer
    count := args[2]

    // For stdout/stderr, write to UART via Mazzy syscall
    if fd == 1 || fd == 2 {
        // Eventually: sys.WriteUART(buf, count)
        // For now, this is where actual output happens
    }

    return int(count), nil
}
```

## Build Requirements

### Position-Independent Code
All userspace programs must be compiled as position-independent executables (PIE):
- Allows loading at arbitrary addresses
- Required for function pointer patching to work
- Go compiler flag: `-buildmode=pie` (or equivalent for bare metal)

### Runtime Patching
When priest loads a program:
1. Parse ELF to find `PriestSyscallHandler` symbol
2. Patch the symbol's value to point to priest's syscall dispatch function
3. Set up program's stack and jump to entry point

### Overlay Generation
Overlays are generated at build time based on GOROOT:
```makefile
# Makefile targets
priest-overlay: ...      # For priest (real SVC)
userspace-overlay: ...   # For normal programs (function pointer)
```

## File Locations

```
src/
├── mazarin/
│   ├── sys/                    # Mazzy syscall client library
│   │   ├── syscall.go          # Syscall numbers
│   │   └── time.go             # GetTime(), etc.
│   └── overlay/
│       ├── priest/             # Priest overlay (real SVC)
│       │   └── syscall_linux.go
│       └── userspace/          # Normal program overlay (function ptr)
│           └── syscall_linux.go
├── flock/
│   └── cmd/
│       ├── priest/             # Syscall router
│       │   ├── main.go
│       │   ├── api.go          # Registration API
│       │   └── dispatch.go     # Syscall dispatch logic
│       ├── tty/                # TTY server
│       │   └── main.go
│       ├── fs/                 # Filesystem server (future)
│       │   └── main.go
│       └── helloworld/         # Example program
│           └── main.go
└── kmazarin/
    └── golang/
        └── ksyscall/
            ├── dispatch.go     # Kernel syscall dispatch
            ├── mazzy.go        # Mazzy syscall table
            └── gettime.go      # GetTime handler
```

## Minimal Hello World

```go
// src/flock/cmd/helloworld/main.go
package main

import (
    "fmt"
    "mazarin/sys"
)

func main() {
    t, err := sys.GetTime()
    if err != nil {
        fmt.Printf("GetTime error: %v\n", err)
        return
    }
    fmt.Printf("hello world %d.%09d\n", t.Seconds, t.Nanoseconds)
}
```

This program:
1. Calls `sys.GetTime()` → intercepted by overlay → priest → SVC to kmazarin → returns time
2. Calls `fmt.Printf()` → intercepted by overlay → priest → dispatched to tty server → output

## Future Considerations

- **Shared memory**: For high-bandwidth IPC between programs
- **Capability system**: Security model for syscall routing
- **Multiple instances**: Load balancing syscall servers
- **Namespaces**: Process isolation via separate server instances
