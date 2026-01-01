# Analysis: Linux Kernel Stack Layout - How Programs Start

## Summary

This test reveals the **exact stack layout** that the Linux kernel provides when executing a program. This is critical for mazboot to properly load and start kmazarin, as the Go runtime expects the stack to be set up exactly as Linux does.

## Entry Point Sequence

### 1. ELF Entry Point

The Linux kernel jumps to the entry point specified in the ELF header:

```
Entry point address: 0x82d90 (_rt0_arm64_linux)
```

### 2. _rt0_arm64_linux (The True Entry Point)

```assembly
0000000000082d90 <_rt0_arm64_linux>:
   82d90:  ldr   x0, [sp]           # Load argc from [sp]
   82d94:  add   x1, sp, #0x8       # argv pointer = sp + 8
   82d98:  bl    82da0 <main>       # Call main
   82d9c:  udf   #0                 # Should never reach here
```

**Critical observations**:
- **X0 (argc)** is loaded from `[SP]` - the FIRST 8 bytes on the stack
- **X1 (argv)** points to `SP + 8` - the SECOND 8 bytes on the stack
- These are passed as function parameters to `main` (which then calls `runtime.rt0_go`)

### 3. runtime.args() - Stores argc/argv

Located at `0x5e450`:

```go
func args(c int32, v **byte) {
    argc = c       // Store global argc
    argv = v       // Store global argv pointer
    sysargs(c, v)  // Parse envp and auxv
}
```

The function:
1. Stores argc in a global variable (at offset 3416 in some struct)
2. Stores argv pointer in another global variable
3. Calls `sysargs()` to process environment variables and auxiliary vector

### 4. runtime.sysargs() - Parse envp and auxv

Located at `0x46f80`:

```go
func sysargs(argc int32, argv **byte) {
    n := argc + 1

    // Skip over argv to find envp
    // argv[0], argv[1], ..., argv[argc-1], NULL
    for argv_index(argv, n) != nil {
        n++
    }

    // Skip NULL separator
    n++

    // Now argv+n points to auxv
    auxvp := (*[1 << 28]uintptr)(add(unsafe.Pointer(argv), uintptr(n)*8))

    // Parse auxv pairs
    if pairs := sysauxv(auxvp[:]); pairs != 0 {
        auxv = auxvp[: pairs*2 : pairs*2]
        return
    }

    // Fallback: read /proc/self/auxv
    fd := open(&procAuxv[0], 0, 0)
    // ... read auxv from file ...
}
```

**Key logic**:
1. Start at index `argc + 1` (first element after argv array)
2. Scan forward through envp until finding NULL terminator
3. Advance by 1 to skip the NULL
4. Now pointing to **auxv** (auxiliary vector)
5. Parse auxv as array of (tag, value) pairs
6. If auxv not found on stack, fall back to reading `/proc/self/auxv`

## Complete Stack Layout

When the Linux kernel executes a program, it sets up the initial stack like this:

```
High Memory
┌────────────────────────────────────┐
│  Environment strings               │  "PATH=/usr/bin", "HOME=/root", etc.
│  (null-terminated C strings)       │
├────────────────────────────────────┤
│  Argument strings                  │  "./program", "arg1", "arg2", etc.
│  (null-terminated C strings)       │
├────────────────────────────────────┤
│  Random bytes (16 bytes)           │  For AT_RANDOM (security)
├────────────────────────────────────┤
│  Platform string "aarch64"         │  For AT_PLATFORM
├────────────────────────────────────┤
│  Auxiliary Vector                  │
│  ┌──────────────────────────────┐  │
│  │ AT_NULL (0)                  │  │  Terminator
│  │ 0                            │  │
│  ├──────────────────────────────┤  │
│  │ AT_SECURE (23)               │  │  Secure mode?
│  │ 0                            │  │  0 = not secure
│  ├──────────────────────────────┤  │
│  │ AT_RANDOM (25)               │  │  Pointer to random bytes
│  │ address → random bytes       │  │
│  ├──────────────────────────────┤  │
│  │ AT_PAGESZ (6)                │  │  Physical page size
│  │ 4096                         │  │
│  ├──────────────────────────────┤  │
│  │ ...more auxv entries...      │  │
│  └──────────────────────────────┘  │
├────────────────────────────────────┤ ← SP + (argc+1+envc+1)*8
│  NULL (0)                          │  End of envp
├────────────────────────────────────┤
│  envp[envc-1]  → env string        │
│  ...                               │
│  envp[1]       → env string        │
│  envp[0]       → env string        │
├────────────────────────────────────┤ ← SP + (argc+1)*8
│  NULL (0)                          │  End of argv
├────────────────────────────────────┤
│  argv[argc-1]  → arg string        │
│  argv[argc-2]  → arg string        │
│  ...                               │
│  argv[1]       → arg string        │
│  argv[0]       → arg string        │
├────────────────────────────────────┤ ← SP + 8
│  argc                              │  Number of arguments
└────────────────────────────────────┘ ← SP (stack pointer at entry)
Low Memory
```

## Data Structure Details

### Stack at Entry Point

```
[SP + 0]  = argc (int64, 8 bytes)
[SP + 8]  = argv[0] (pointer to first argument string)
[SP + 16] = argv[1] (pointer to second argument string)
...
[SP + 8*(argc)]   = argv[argc-1] (pointer to last argument)
[SP + 8*(argc+1)] = NULL (0x0, end of argv)
[SP + 8*(argc+2)] = envp[0] (pointer to first environment string)
[SP + 8*(argc+3)] = envp[1] (pointer to second environment string)
...
[SP + 8*(argc+1+envc)]   = envp[envc-1] (last environment variable)
[SP + 8*(argc+1+envc+1)] = NULL (0x0, end of envp)
[SP + 8*(argc+1+envc+2)] = auxv[0].tag (first auxv tag)
[SP + 8*(argc+1+envc+3)] = auxv[0].value (first auxv value)
[SP + 8*(argc+1+envc+4)] = auxv[1].tag (second auxv tag)
[SP + 8*(argc+1+envc+5)] = auxv[1].value (second auxv value)
...
[SP + 8*(argc+1+envc+2*N)]   = AT_NULL (0)
[SP + 8*(argc+1+envc+2*N+1)] = 0
```

### Auxiliary Vector (auxv) Structure

The auxiliary vector is an array of (tag, value) pairs, each 8 bytes:

```go
type AuxvPair struct {
    Tag   uint64  // Type identifier (AT_PAGESZ, AT_RANDOM, etc.)
    Value uint64  // Value for this tag
}
```

**Critical auxv entries** that the Go runtime needs:

| Tag | Name | Value | Purpose |
|-----|------|-------|---------|
| 6 | AT_PAGESZ | 4096 | Physical page size (CRITICAL for memory allocator) |
| 25 | AT_RANDOM | pointer | 16 random bytes for security |
| 23 | AT_SECURE | 0 or 1 | Secure mode flag |
| 15 | AT_PLATFORM | pointer | Platform name string ("aarch64") |
| 16 | AT_HWCAP | bits | Hardware capabilities |
| 0 | AT_NULL | 0 | Terminator (REQUIRED) |

**Most critical**: `AT_PAGESZ` **MUST** be present and set to 4096 (or the actual page size). The Go runtime reads this to set `physPageSize`, which is used by the memory allocator. If this is missing or zero, memory allocation will fail.

## How Go Runtime Accesses This Data

### Reading argc/argv

From `_rt0_arm64_linux`:
```assembly
ldr   x0, [sp]           # argc
add   x1, sp, #0x8       # argv = sp + 8
```

### Walking to envp

From `runtime.goenvs_unix()`:
```go
n := int32(0)
for argv_index(argv, argc+1+n) != nil {
    n++  // Count environment variables
}

// Now argv_index(argv, argc+1+i) points to envp[i]
envs = make([]string, n)
for i := int32(0); i < n; i++ {
    envs[i] = gostring(argv_index(argv, argc+1+i))
}
```

### Walking to auxv

From `runtime.sysargs()`:
```go
n := argc + 1  // Start after argv

// Skip over argv and envp to reach auxv
for argv_index(argv, n) != nil {
    n++
}

// Skip NULL separator
n++

// Now argv + n*8 points to first auxv pair
auxvp := (*[1 << 28]uintptr)(add(unsafe.Pointer(argv), uintptr(n)*8))
```

### Parsing auxv

From `runtime.sysauxv()` (not shown in detail here, but it):
1. Iterates through (tag, value) pairs
2. Stops when it finds `AT_NULL` (tag = 0)
3. Extracts important values:
   - `AT_PAGESZ` → `physPageSize`
   - `AT_RANDOM` → used for hash seed
   - `AT_SECURE` → security mode
4. Returns number of pairs found

## What Mazboot Must Do

To properly load and start kmazarin, mazboot must:

### 1. Allocate Stack Space

```go
// Allocate at least 64KB for initial stack
stackSize := 64 * 1024
stackTop := allocateStack(stackSize)
stackPtr := stackTop  // Stack grows down
```

### 2. Build Argument Strings

```go
// Example arguments for kmazarin
args := []string{"kmazarin", "--debug"}
argStrings := make([]uintptr, len(args))

for i, arg := range args {
    // Allocate space for string + null terminator
    str := allocateString(arg)
    argStrings[i] = str
}
```

### 3. Build Environment Strings

```go
// Minimal environment
envs := []string{
    "PATH=/bin",
    "HOME=/root",
}
envStrings := make([]uintptr, len(envs))

for i, env := range envs {
    str := allocateString(env)
    envStrings[i] = str
}
```

### 4. Allocate Random Bytes

```go
// 16 random bytes for AT_RANDOM
randomBytes := make([]byte, 16)
// Fill with random data (or pseudo-random for now)
for i := range randomBytes {
    randomBytes[i] = byte(i * 17)  // Simple pattern for testing
}
randomPtr := uintptr(unsafe.Pointer(&randomBytes[0]))
```

### 5. Build Auxiliary Vector

```go
type AuxvPair struct {
    Tag   uint64
    Value uint64
}

auxv := []AuxvPair{
    {Tag: 6, Value: 4096},              // AT_PAGESZ
    {Tag: 25, Value: uint64(randomPtr)}, // AT_RANDOM
    {Tag: 23, Value: 0},                 // AT_SECURE (not secure)
    {Tag: 0, Value: 0},                  // AT_NULL (terminator)
}
```

### 6. Build Stack Layout

```go
// Work backwards from stack top
sp := stackTop

// Push auxv
for i := len(auxv) - 1; i >= 0; i-- {
    sp -= 8
    *(*uint64)(unsafe.Pointer(sp)) = auxv[i].Value
    sp -= 8
    *(*uint64)(unsafe.Pointer(sp)) = auxv[i].Tag
}

// Push NULL after envp
sp -= 8
*(*uint64)(unsafe.Pointer(sp)) = 0

// Push envp pointers
for i := len(envStrings) - 1; i >= 0; i-- {
    sp -= 8
    *(*uint64)(unsafe.Pointer(sp)) = uint64(envStrings[i])
}

// Push NULL after argv
sp -= 8
*(*uint64)(unsafe.Pointer(sp)) = 0

// Push argv pointers
for i := len(argStrings) - 1; i >= 0; i-- {
    sp -= 8
    *(*uint64)(unsafe.Pointer(sp)) = uint64(argStrings[i])
}

// Push argc
sp -= 8
*(*uint64)(unsafe.Pointer(sp)) = uint64(len(args))

// sp now points to the base of the stack structure
```

### 7. Set Registers and Jump

```go
// Set up entry registers
X0 := len(args)          // argc (loaded by entry point from [sp])
X1 := sp + 8             // argv (calculated by entry point)
SP := sp                 // Stack pointer to argc

// Jump to kmazarin entry point
jumpToEntryPoint(entryPoint, SP)
```

**Note**: We don't actually set X0 and X1 - the entry point code (`_rt0_arm64_linux`) loads them from the stack itself. We only need to set SP correctly.

## Assembly Implementation

In assembly, the jump looks like:

```assembly
// Assume sp points to the constructed stack
// Assume x0 holds the entry point address

mov sp, x19        // Set stack pointer (x19 = constructed stack pointer)
br  x0             // Jump to entry point (kmazarin's _rt0_arm64_linux)

// Entry point will execute:
//   ldr x0, [sp]       # Load argc
//   add x1, sp, #0x8   # Calculate argv
//   bl  main           # Continue initialization
```

## Critical Requirements

### Memory Layout

**All string data must be valid and accessible**:
- Argument strings must be null-terminated C strings
- Environment strings must be null-terminated C strings
- All pointers in argv/envp must point to valid memory
- Random bytes buffer must be valid memory

### Alignment

**All pointers must be 8-byte aligned**:
- Stack pointer must be 16-byte aligned (ARM64 ABI requirement)
- String buffers can be 1-byte aligned (C strings)
- auxv entries are naturally 8-byte aligned

### Auxiliary Vector

**AT_PAGESZ is absolutely required**:
```go
// This entry is CRITICAL - without it, physPageSize = 0
{Tag: 6, Value: 4096}  // AT_PAGESZ
```

**AT_NULL terminator is required**:
```go
// This marks the end of the auxv array
{Tag: 0, Value: 0}  // AT_NULL
```

## Testing Strategy

### Minimal Test

Start with the absolute minimum:
```go
args := []string{"kmazarin"}
envs := []string{}
auxv := []AuxvPair{
    {Tag: 6, Value: 4096},   // AT_PAGESZ
    {Tag: 0, Value: 0},       // AT_NULL
}
```

This should be enough for the Go runtime to:
1. Read argc = 1
2. Read argv[0] = "kmazarin"
3. Find empty envp
4. Parse auxv and get `physPageSize = 4096`
5. Initialize memory allocator successfully

### Incremental Additions

Once the minimal test works, add:
1. **AT_RANDOM** - 16 random bytes for hash security
2. **Multiple arguments** - Test argc > 1
3. **Environment variables** - Test envp parsing
4. **AT_SECURE** - Security flag
5. **AT_PLATFORM** - Platform string

## Debugging Tips

### If kmazarin crashes immediately:

1. **Check SP alignment**: Must be 16-byte aligned
   ```assembly
   and x0, sp, #15   # Check low 4 bits
   cbnz x0, error    # Should be zero
   ```

2. **Check argc value**: Should be > 0
   ```go
   if argc == 0 {
       panic("argc is zero - stack not set up correctly")
   }
   ```

3. **Check argv[0]**: Should be valid pointer
   ```go
   if argv == nil {
       panic("argv is nil")
   }
   if argv_index(argv, 0) == nil {
       panic("argv[0] is nil")
   }
   ```

4. **Check auxv AT_PAGESZ**: Should be 4096
   ```go
   if physPageSize == 0 {
       panic("physPageSize not set - auxv missing AT_PAGESZ")
   }
   ```

### Logging Points

Add debug logging at these locations:
- Before constructing stack: "Building stack for kmazarin"
- After constructing stack: "Stack ready at 0x%x, argc=%d"
- Before jump: "Jumping to entry point at 0x%x"
- In kmazarin (if possible): "kmazarin started, argc=%d"

## References

- [Linux kernel fs/binfmt_elf.c](https://github.com/torvalds/linux/blob/master/fs/binfmt_elf.c) - `create_elf_tables()` function
- [glibc sysdeps/generic/ldsodefs.h](https://sourceware.org/git/?p=glibc.git) - Auxiliary vector definitions
- Go runtime source:
  - `runtime/asm_arm64.s` - `_rt0_arm64_linux` entry point
  - `runtime/runtime1.go` - `args()` and `goargs()` functions
  - `runtime/os_linux.go` - `sysargs()` and auxiliary vector parsing
  - `runtime/os_linux.go` - AT_* constant definitions

## Summary of Stack Structure

```
[SP + 0]     argc
[SP + 8]     argv[0], argv[1], ..., argv[argc-1], NULL
[SP + ?]     envp[0], envp[1], ..., envp[N-1], NULL
[SP + ?]     auxv: (tag, value) pairs, ending with (AT_NULL, 0)
```

**Key insight**: Everything is contiguous in memory, with NULL (0) separators between sections. The runtime walks through the structure linearly, counting entries until it finds the NULLs.
