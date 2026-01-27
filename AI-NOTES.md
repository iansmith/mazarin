# AI Development Notes

Notes and observations about working with AI assistants on this codebase.

## Best Practices

* **Closed loop testing** - Be sure to get the AI to make changes to the source and then test its own changes. This catches errors immediately rather than letting them accumulate.

* **AI strengths for tedious work** - Some things are easy for the AI, like picking through large output of breadcrumbs or running tests carefully after each change and never assuming something will work. Example: "Here are the breadcrumbs from several hundred system calls, find the one that has mismatched entry and exit behavior."

* **Using plans is critical for money saving** - Planning before implementation reduces wasted tokens on wrong approaches.

* **Using todos is great for being specific about which things in what order** - Keeps both the AI and human aligned on progress and next steps.

* **Git worktrees tradeoffs** - Git worktrees are probably not as valuable if you are building things yourself because they are equivalent to commits on the "main" branch if you are the only person using the worktrees. However, the AI is good at untangling worktree blunders like having 5 worktrees with uncommitted changes from the same source commit.

* **Incremental changes with working code** - Incrementally changing something while keeping the code working is easier because the cost of "dead work" such as building transpilers and shims is close to zero. Example: the transition of the assembly code from GCC assembly to Plan9/Go assembly.


* ** Go has no readonly enforcement of its values other than the language and
   there are language constructions such as unsafe.Pointer that can break the
   "read only" nature of the ro segment. We have a solution that efficiently 
   allows us to enforce read only with the VM system at a cost of no extra space
   and error exposure (leaving RO actually as RW) of about 0.3%.


* A key thing to use the AI for is low inventiveness, high tediousness implementation tasks.
  Example, write a tool that uses go's lexer/parser of go language to find a all the functions
  in a given package(s) and then rewrite them to be different. This implies looking for 
  dead variables, dead types, and unused imports.

* Tried to use sonnet to make a plan and then I had opus evaluate the problem and it
was polite but basically pointed out all the mistakes in the plan and a major bit
of surgery was needed.

---

## Technical Patterns: Setting Read-Only Memory Protection on .rodata

This section documents the approach used to set the `.rodata` section as read-only in page tables during MMU initialization.

### Problem

When using Go's native toolchain (rather than GCC), linker symbol values like `__rodata_start` and `__rodata_end` are not directly accessible from Go code. We need to:
1. Discover these values from the ELF binary after linking
2. Make them accessible from Go code during MMU setup
3. Apply proper page table attributes (read-only, no execute)

### Solution Architecture

The solution has four components:

#### 1. Linker Script Defines Section Boundaries (`src/cardinal/linker.ld`)

```ld
/* Align .rodata to 16 bytes for efficient ldp/stp access */
. = ALIGN(16);
__rodata_start = .;
.rodata : ALIGN(16) SUBALIGN(16)
{
    *(.rodata .rodata.*)
}
__rodata_end = .;
```

#### 2. Go Variables for Patching (`src/cardinal/golang/main/layout.go`)

Variables are initialized to 1 (not 0) so they're placed in `.data` section (not `.bss`), making them patchable:

```go
var (
    // Section boundaries - PATCHED POST-BUILD by compute-linker-values tool
    LinkerRodataStart uint64 = 1 // __rodata_start (patched)
    LinkerRodataEnd   uint64 = 1 // __rodata_end (patched)
    // ... other section boundaries
)
```

#### 3. Assembly Accessors (`src/cardinal/golang/asm/kernel/linker_symbols_arm64.s`)

Assembly functions read the Go variables and return them to callers:

```asm
// get_rodata_start_addr() returns uintptr
TEXT ·get_rodata_start_addr(SB), NOSPLIT, $0-8
    MOVD    main·LinkerRodataStart(SB), R0
    MOVD    R0, ret+0(FP)
    RET

// get_rodata_end_addr() returns uintptr
TEXT ·get_rodata_end_addr(SB), NOSPLIT, $0-8
    MOVD    main·LinkerRodataEnd(SB), R0
    MOVD    R0, ret+0(FP)
    RET
```

These are wrapped in Go functions via `//go:linkname`:

```go
// In asm/kernel/kernel.go
func GetRodataStartAddr() uintptr { return get_rodata_start_addr() }
func GetRodataEndAddr() uintptr   { return get_rodata_end_addr() }
```

#### 4. MMU Initialization Uses the Accessors (`src/cardinal/golang/main/mmu.go`)

During `initMMU()`, the rodata section is mapped with read-only permissions:

```go
// Get section boundaries from linker symbols via assembly helpers
// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
// because getLinkerSymbol() uses string comparisons that access .rodata!
rodataStart := asm.GetRodataStartAddr()
rodataEnd := asm.GetRodataEndAddr()

if rodataStart != 0 && rodataEnd != 0 {
    mapRegionInitMMU(rodataStart, rodataEnd, rodataStart,
        PTE_ATTR_NORMAL,   // Normal memory (cacheable)
        PTE_AP_RO_EL1,     // Read-only at EL1, no access at EL0
        PTE_EXEC_NEVER)    // Not executable
}
```

### Key Insights

1. **Why assembly accessors?** - During MMU initialization, we cannot call Go functions that access `.rodata` (like `getLinkerSymbol()` which uses string comparisons). Assembly functions access the memory layout variables without string operations.

2. **Why initialize to 1?** - Variables initialized to 0 go in `.bss` which is zeroed at startup and cannot be patched. Variables initialized to non-zero go in `.data` and can be patched by the post-build tool.

3. **Post-build patching** - The `compute-linker-values` tool (`src/cardinal/tools/compute-linker-values.go`) reads the ELF binary, finds actual section boundaries, and patches the Go variables in the binary.

4. **Page table attribute bits**:
   - `PTE_AP_RO_EL1 = 3 << 6` - Read-only at EL1, no access at EL0
   - `PTE_EXEC_NEVER = PTE_PXN | PTE_UXN` - No execute at any privilege level

### Files Involved

- `src/cardinal/linker.ld` - Defines `__rodata_start` and `__rodata_end`
- `src/cardinal/golang/main/layout.go` - Declares `LinkerRodataStart`, `LinkerRodataEnd`
- `src/cardinal/golang/asm/kernel/linker_symbols_arm64.s` - Assembly accessors
- `src/cardinal/golang/asm/kernel/kernel.go` - Go wrappers for assembly
- `src/cardinal/golang/main/mmu.go` - Uses values during MMU init
- `src/cardinal/tools/compute-linker-values.go` - Post-build patcher
