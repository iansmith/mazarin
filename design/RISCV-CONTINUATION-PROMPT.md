# RISC-V Boot - Continuation Prompt for Next Session

## Current Status (Commit: 52cf303)

**MAJOR BREAKTHROUGH**: BlockDeviceRaw refactor is complete and working! We can now perform allocation-free FAT32 I/O during early boot.

### What Works ✅
1. VirtIO MMIO block device initialization (must use VIRTIO_TRANSPORT=mmio)
2. FAT32 filesystem mount (allocation-free)
3. LFN filename search (kmazarin-riscv64.elf found)
4. Cluster map building (691 clusters for 2.7MB file)
5. First file read with readFileAtRaw() - ELF header loaded successfully

### Current Blocker 🚧
**File**: `diplomat/main/elf_loader.go`
**Line**: 881 in `LoadKernelNoError()`
**Issue**: After successfully reading ELF header with `readFileAtRaw()`, the function calls regular `LoadKernel(fsys, path)` which returns an `error` interface, causing allocation and hang.

```go
// Line 879-888 in elf_loader.go
// Continue with rest of LoadKernel logic...
// For now, call the regular LoadKernel and ignore the error
kernel, err := LoadKernel(fsys, path)  // ← BLOCKER: error interface allocation
if err != nil {
    printString("ERROR: LoadKernel failed: ")
    printString(err.Error())  // ← Also allocates (string conversion)
    printString("\r\n")
    for {}
}
return kernel
```

### Boot Progress Markers
- `D` `S` `0-9` `T` `C` `J` `E` `W` = Early init
- `G` = VirtIO scan start
- `R` `M` = FAT32 mount
- `L` `a` = File search
- `1` `2` `W` `B` `C` `R` = Directory walk
- `Q` `d` `3` = File found
- `b` = File structure initialized
- `c` = buildClusterMap start
- `M` `L` `Z` `N` `C` = Cluster map complete (read FAT sectors)
- `e` = Before reading ELF header ✅ (reached)
- `d` = After reading ELF header ❌ (never reached - hang occurs here)

## Next Steps

### Task: Complete LoadKernelNoError Implementation

Replace the `LoadKernel()` call at line 881 with allocation-free ELF loading logic.

**Reference**: The existing `LoadKernel()` function (lines ~90-260) shows the complete logic:
1. Read ELF header ✅ (already done with readFileAtRaw at line 857)
2. Validate ELF ✅ (already done at lines 865-877)
3. Read program headers (need readFileAtRaw)
4. Find virtual address range
5. Allocate memory for kernel
6. Copy LOAD segments (need readFileAtRaw via copySegmentToMemory)
7. Find kernel symbols (optional, can skip for now)
8. Return LoadedKernel struct

**Key conversions needed**:
- Replace all `readFileAt()` calls with `readFileAtRaw()`
- Replace error checking `if err != nil` with `if errCode != 0`
- Replace `printString(err.Error())` with error code printing
- Use pre-allocated error structs (already defined) instead of creating new ones

**Function signature stays the same**:
```go
func LoadKernelNoError(fsys *fat32.FileSystem, path string) *LoadedKernel
```

Returns `nil` on failure (infinite loop with printString for debugging).

### Testing

After implementing, test with:
```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task run-diplomat-riscv64 TIMEOUT=10 VIRTIO_TRANSPORT=mmio
```

Look for marker `d` (ELF header read complete) to verify progress past current blocker.

### Architecture Notes

**BlockDeviceRaw Pattern (User-Requested)**:
- `FooRaw() (result, errCode int)` - allocation-free implementation
- `Foo() (result, error)` - wrapper that calls FooRaw() and converts error codes
- Shared FAT32 library works in both early boot (no heap) and kernel context

**Files Modified in This Session**:
- `shared/blockdev/blockdev.go` - BlockDeviceRaw interface
- `shared/fs/fat32/fat32.go` - ReadFATEntryRaw, ReadClusterRaw
- `diplomat/main/uefi_blockdev.go` - ReadBlockRaw, WriteBlockRaw
- `diplomat/main/elf_loader.go` - readFileAtRaw, LoadKernelNoError partial impl

**Critical Rules** (from MEMORY.md):
During diplomat-riscv64 early boot (before full runtime init), **AVOID**:
1. `new()` calls
2. String assignments
3. Error interface returns
4. Interface method calls
5. Method calls on structs with interface fields
6. Local variables containing interfaces
7. Calling through function pointers (struct fields)

**Solutions**:
- Use global structs
- Functions return `bool` or `int` error codes
- Call concrete functions directly
- Use standalone functions, not methods
- Direct function calls, not through `plat.*` struct

### Expected Next Blocker

After completing LoadKernelNoError, the next likely blocker is jumping to the kernel entry point. The current code may need similar allocation-free refactoring for:
- Setting up auxv parameters
- Page table setup (if it allocates)
- Jump to kernel (should be fine, just assembly)

### Quick Start Command

```bash
# Continue from where we left off
cd /Users/iansmith/mazzy-riscv
git log -1 --oneline  # Verify on commit 52cf303
git diff HEAD^ HEAD   # Review changes from previous session

# Start implementing LoadKernelNoError
# File: diplomat/main/elf_loader.go
# Function: LoadKernelNoError (line 843)
# Replace lines 879-888 with full ELF loading logic using Raw methods
```

### Success Criteria

You'll know it's working when:
1. Marker `d` appears in output (ELF header validated)
2. Marker `f` appears (program headers read)
3. Markers for segment loading appear
4. Eventually: kernel jump or new blocker in kernel init

### Resources

- Current LoadKernel() implementation: `diplomat/main/elf_loader.go` lines ~90-260
- readFileAtRaw() implementation: `diplomat/main/elf_loader.go` lines 439-514
- Test output location: `/tmp/diplomat-riscv64-serial.log`
- Safe reader: `$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log`
