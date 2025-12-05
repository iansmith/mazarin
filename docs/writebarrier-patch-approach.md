# Write Barrier Patch Approach

## Goal

Replace `gcWriteBarrier` to perform the actual assignment directly, bypassing the GC buffer mechanism.

## Analysis

From disassembly of the calling code:
- `x27` = destination address (heapSegmentListHead) - set before call at `0x27c29c`
- `x2` = new value (pointer to assign) - set before call
- `x1` = old value (loaded for GC tracking) - loaded at `0x27c2a0`
- Call to `runtime.gcWriteBarrier2` at `0x27c2a4`
- After return, calling code also does: `str x2, [x27]` at `0x27c2b0` (backup assignment)

## Implementation

### 1. Custom Write Barrier (`src/asm/writebarrier.s`)

Created our own `runtime.gcWriteBarrier2` implementation:
```assembly
.global runtime.gcWriteBarrier2
runtime.gcWriteBarrier2:
    // x27 = destination address (set by calling code)
    // x2 = new value (pointer to assign)
    // Just write directly to destination - no buffer, no GC tracking
    str x2, [x27]
    ret
```

This performs the assignment directly without any GC buffer mechanism.

### 2. Symbol Weakening (Makefile)

In the Makefile, we weaken the Go runtime's write barrier symbols:
```makefile
@$(OBJCOPY) --weaken-symbol=runtime.gcWriteBarrier2 \
           --weaken-symbol=runtime.gcWriteBarrier3 \
           --weaken-symbol=runtime.gcWriteBarrier4 \
           --weaken-symbol=gcWriteBarrier \
           $@ $@.tmp && mv $@.tmp $@
```

This makes the Go runtime's symbols weak, allowing our strong global symbols to potentially override them.

### 3. Binary Patching (`src/patch_writebarrier.py`)

Since the linker still resolves to the Go runtime's version, we patch the binary after linking:

1. **Find the call site**: The `bl` instruction at virtual address `0x27c2a4`
2. **Calculate file offset**: Use `target-readelf -S` to find `.text` section file offset
3. **Patch the instruction**: Change `bl 0x26ecf0` (Go runtime) to `bl 0x27cbb4` (our function)
4. **Encode correctly**: `bl` uses relative offset: `0x94000000 | ((target - pc) >> 2)`

The script:
- Reads ELF section headers to find `.text` file offset
- Calculates file offset for virtual address `0x27c2a4`
- Verifies current instruction (optional check)
- Writes new `bl` instruction encoding

### 4. Build Integration (Makefile)

The patching is integrated into the build process:
```makefile
kernel-qemu.elf: ...
	$(CC) $(LDFLAGS) -o $@.tmp ...
	@python3 patch_writebarrier.py $@.tmp && mv $@.tmp $@ || \
	 (echo "Warning: Could not patch binary" && mv $@.tmp $@)
```

## Verification

After patching, verify with:
```bash
target-objdump -d kernel-qemu.elf | awk '/27c2a4:/'
```

Should show:
```
27c2a4:	94000244 	bl	27cbb4 <runtime.gcWriteBarrier2>
```

Instead of:
```
27c2a4:	97ffca93 	bl	26ecf0 <runtime.gcWriteBarrier2>
```

## Current Status

✅ **Patching works**: The call successfully redirects to our function at `0x27cbb4`  
✅ **Function executes**: Our `str x2, [x27]` instruction runs  
⚠️ **Assignment may still fail**: Test shows `T2 N`, suggesting the assignment might not be working

## Potential Issues

1. **Register values**: `x27` might not contain the destination address when our function is called
2. **Memory layout**: The destination address might be in a read-only section
3. **Timing**: The assignment might be happening but being overwritten later
4. **Multiple assignments**: The calling code also does `str x2, [x27]` at `0x27c2b0` after our function returns

## Next Steps

1. Add debug output to verify `x27` and `x2` values in our write barrier function
2. Check if the destination address is writable
3. Verify the assignment is actually happening by reading back the value
4. Consider if the backup assignment at `0x27c2b0` is interfering

## Files

- `src/asm/writebarrier.s` - Custom write barrier implementation
- `src/patch_writebarrier.py` - Binary patching script
- `src/Makefile` - Build integration (symbol weakening + patching)
