# Mazarin Kernel Architecture Guide

This document describes the architectural decisions and patterns used in the Mazarin bare-metal kernel project. All code changes should follow these guidelines.

## Table of Contents

1. [Go + Assembly Integration](#go--assembly-integration)
2. [Bitfield Management](#bitfield-management)
3. [Build Process](#build-process)
4. [Memory Management](#memory-management)
5. [Linker Symbols](#linker-symbols)
6. [Bare-Metal Considerations](#bare-metal-considerations)

---

## Go + Assembly Integration

### Core Principle: No CGO

This project uses **pure Go with assembly**, avoiding CGO entirely. This allows us to:
- Build bare-metal kernels without OS dependencies
- Maintain full control over memory layout
- Avoid C runtime overhead

### Calling Assembly from Go

**Pattern:** Use `//go:linkname` to link Go functions to assembly symbols.

```go
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)
```

**Rules:**
- Always use `//go:nosplit` for functions that call assembly (prevents stack checks)
- Use `uintptr` for memory addresses (ensures correct size on 64-bit)
- Assembly functions must be defined in `.s` files with `.global` symbols

### Calling Go from Assembly

**Pattern:** Use symbol promotion via `objcopy` to make Go functions globally visible.

1. Define Go function with `//go:noinline` to prevent optimization:
```go
//go:nosplit
//go:noinline
func KernelMain(r0, r1, atags uint32) {
    // ...
}
```

2. Promote symbol in Makefile using `objcopy --globalize-symbol`:
```makefile
$(OBJCOPY) --globalize-symbol=main.KernelMain $(KERNEL_GO_TEMP) $@
```

3. Call from assembly using `.extern`:
```assembly
.extern main.KernelMain
kernel_main:
    b main.KernelMain
```

**Rules:**
- Go functions called from assembly must use `//go:nosplit`
- Use `//go:noinline` to ensure symbol exists
- Reference the function in `main()` to prevent optimization removal

### Assembly Function Conventions

**AArch64 Calling Convention:**
- Parameters: `x0, x1, x2, ...` (64-bit) or `w0, w1, w2, ...` (32-bit)
- Return values: `x0`/`w0`
- Caller-saved registers: `x0-x7`
- Callee-saved registers: `x19-x28`

**Example:**
```assembly
// mmio_write(uintptr_t reg, uint32_t data)
// x0 = register address, w1 = data (32-bit)
.global mmio_write
mmio_write:
    str w1, [x0]        // Store 32-bit value from w1 to address in x0
    ret                 // Return
```

---

## Bitfield Management

### Approach: Code Generation with Struct Tags

We use a **code generation approach** for bitfields, allowing natural Go struct syntax while ensuring efficient packing.

### Defining Bitfields

**Pattern:** Define struct with `bitfield` tags:

```go
type PageFlags struct {
    Allocated  bool   `bitfield:",1"`   // 1 bit
    KernelPage bool   `bitfield:",1"`   // 1 bit
    Reserved   uint32 `bitfield:",30"`  // 30 bits
}
```

**Rules:**
- **Always use 32 bits** for flags (works on both 32-bit and 64-bit processors)
- Tag format: `bitfield:",bits"` or `bitfield:"methodName,bits"`
- Total bits must not exceed the target size (32 bits)
- Use `uint32` for the packed result, not `uint64`

### Code Generation

**Pattern:** Generate unpacking code at build time.

1. Define struct in `bitfield/page_flags.go`
2. Generator reads struct and produces unpacking functions
3. Makefile runs generator before compilation

**Generated Functions:**
```go
// PackPageFlags packs a PageFlags struct into a uint32
func PackPageFlags(flags PageFlags) (uint32, error)

// UnpackPageFlags unpacks a uint32 into a PageFlags struct
func UnpackPageFlags(packed uint32) PageFlags
```

**Rules:**
- Generated code is in `bitfield/*_gen.go` files
- Never edit generated files directly
- Regenerate by running `make generate` or `make` (auto-regenerates)

### Usage

```go
// Pack flags
flags := bitfield.PageFlags{
    Allocated:  true,
    KernelPage: false,
    Reserved:   0,
}
packed, err := bitfield.PackPageFlags(flags)

// Store packed value (efficient 32-bit storage)
page.Flags = packed

// Later, unpack to read
unpacked := bitfield.UnpackPageFlags(page.Flags)
if unpacked.Allocated {
    // ...
}
```

---

## Build Process

### Build Flow

1. **Code Generation:** Generate bitfield unpacking code
2. **Assembly Compilation:** Compile `.s` files with `target-gcc`
3. **Go Compilation:** Compile Go with `go build -buildmode=c-archive`
4. **Symbol Promotion:** Use `objcopy` to promote Go symbols
5. **Linking:** Link all objects with `target-gcc` using linker script

### Key Makefile Targets

- `make` or `make all`: Full build (generates code, compiles, links)
- `make generate`: Generate bitfield code only
- `make test`: Run Go tests
- `make clean`: Remove all build artifacts
- `make push`: Build and copy kernel.elf to docker/builtin/

### Build Rules

**Rules:**
- Always generate code before compiling Go
- Use `GOTOOLCHAIN=local` for code generation
- Use `GOTOOLCHAIN=auto GOARCH=arm64 GOOS=linux` for kernel compilation
- Kernel entry point is at `0x200000` (64-bit Raspberry Pi 4)
- Stack pointer is set to `0x400000` (above kernel)

---

## Memory Management

### Memory Layout

**Kernel Load Address:** `0x200000` (2MB) - 64-bit Raspberry Pi 4 entry point

**Memory Regions:**
- `0x200000` - `__end`: Kernel code, data, BSS
- `__end` - `__end + page_metadata_size`: Page metadata array
- `0x400000`: Stack pointer (1MB+ stack for Go runtime)

### Page Management

**Page Size:** 4KB (4096 bytes)

**Page Metadata Structure:**
```go
type Page struct {
    VaddrMapped uintptr  // Virtual address this page maps to
    Flags       uint32   // Packed PageFlags (32 bits)
}
```

**Rules:**
- Always reserve pages from `0x200000` to `__end` as kernel pages
- Use `__end` symbol from linker script to find end of kernel
- Page metadata array starts immediately after kernel
- Calculate available memory: `total_memory - kernel_size`

---

## Linker Symbols

### Accessing Linker Symbols from Go

**Pattern:** Use `//go:linkname` to access linker script symbols.

```go
//go:linkname __end __end
var __end uintptr
```

**Available Symbols:**
- `__start`: Kernel start address (`0x200000`)
- `__text_start`, `__text_end`: Text section bounds
- `__data_start`, `__data_end`: Data section bounds
- `__bss_start`, `__bss_end`: BSS section bounds
- `__end`: End of kernel (use for page metadata start)

**Rules:**
- Linker symbols are defined in `linker.ld`
- Use `uintptr` type for addresses
- Symbols are set by linker, not initialized in code

---

## Bare-Metal Considerations

### Stack Management

**Pattern:** Set stack pointer early in boot code.

```assembly
// Set stack pointer to 0x400000 (above kernel)
movz x0, #0x40, lsl #16    // Load 0x400000
mov sp, x0
```

**Rules:**
- Stack must be above kernel and page metadata
- Go runtime needs significant stack space (1MB+)
- Stack grows downward (decrementing addresses)

### CPU Initialization

**Pattern:** Only CPU 0 runs kernel code, others halt.

```assembly
// Get CPU ID
mrs x1, mpidr_el1
and x1, x1, #0xFF
cmp x1, #0
bne cpu_halt_loop  // CPUs 1-3 halt here

// CPU 0 continues...
```

**Rules:**
- Always check CPU ID in boot code
- Halt other CPUs in tight loop with `wfe` (wait for event)
- Only CPU 0 initializes BSS, sets stack, calls kernel

### Go Runtime Considerations

**Pattern:** Use `//go:nosplit` for all kernel functions.

```go
//go:nosplit
func uartInit() {
    // No stack checks, no GC pauses
}
```

**Rules:**
- All kernel functions must use `//go:nosplit`
- Avoid Go runtime features that require OS (goroutines, channels, etc.)
- Use `-buildmode=c-archive` to include minimal Go runtime
- Go runtime will still be present but limited

### Memory-Mapped I/O

**Pattern:** Use assembly functions for MMIO access.

```go
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)

// Usage
mmio_write(UART0_DR, uint32('H'))
```

**Raspberry Pi 4 Base Addresses:**
- `PERIPHERAL_BASE = 0xFE000000`
- `GPIO_BASE = 0xFE200000`
- `UART0_BASE = 0xFE201000`

**Rules:**
- Always use `uintptr` for register addresses
- Use `uint32` for register data (most MMIO is 32-bit)
- MMIO functions must be `//go:nosplit`

---

## Testing

### Running Tests

```bash
make test
```

**Rules:**
- Tests run on host machine (not in QEMU)
- Use `GOTOOLCHAIN=local` for tests
- Tests verify bitfield packing/unpacking correctness
- All tests must pass before committing

---

## Code Style

### Function Annotations

**Required annotations for kernel functions:**
```go
//go:nosplit        // Prevents stack checks (required for assembly calls)
//go:noinline       // Prevents inlining (ensures symbol exists)
```

### Naming Conventions

- Assembly functions: `snake_case` (e.g., `mmio_write`, `kernel_main`)
- Go functions: `CamelCase` (e.g., `KernelMain`, `UartInit`)
- Constants: `UPPER_CASE` (e.g., `UART0_BASE`, `PERIPHERAL_BASE`)

### File Organization

- `boot.s`: CPU initialization, BSS clearing, entry point
- `lib.s`: Assembly utility functions (MMIO, delays)
- `kernel.go`: Main kernel logic in Go
- `bitfield/`: Bitfield code generation and types
- `linker.ld`: Memory layout and section definitions

---

## Summary of Key Rules

1. **No CGO** - Use `//go:linkname` for assembly integration
2. **Always `//go:nosplit`** - For functions calling assembly or doing MMIO
3. **32-bit flags** - Use `uint32` for packed flags, works on all architectures
4. **Code generation** - Regenerate bitfield code before building
5. **Symbol promotion** - Use `objcopy` to make Go functions globally visible
6. **Stack above kernel** - Set stack pointer to `0x400000` or higher
7. **CPU 0 only** - Halt other CPUs in boot code
8. **Linker symbols** - Use `//go:linkname` to access `__end`, etc.
9. **MMIO via assembly** - All hardware access through assembly functions
10. **Test before commit** - Run `make test` to verify changes

---

## References

- [Raspberry Pi 4 Memory Map](https://www.raspberrypi.com/documentation/computers/raspberry-pi.html)
- [AArch64 Calling Convention](https://github.com/ARM-software/abi-aa/blob/main/aapcs64/aapcs64.rst)
- [Go Linkname Directive](https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives)
- [Bare Metal Go](https://github.com/tinygo-org/tinygo) (reference, but we use standard Go)

---

*Last updated: Based on project state as of implementation*

