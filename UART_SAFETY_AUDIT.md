# UART Safety Audit Report

## Date: 2025-12-27

## Summary

✅ **All assembly UART calls are SAFE** - No unsafe UART operations found that could corrupt X0 or other critical registers.

## Audit Results by Category

### 1. Direct UART Writes (`strb` to UART address)

**Found:**
- `exceptions.s:146` - Inside `DEBUG_PUTC_SAFE` macro ✅ (protected by DEBUG_SAVE_REGS)
- `exceptions.s:238` - Inside `UART_PUTC` macro ✅ (saves/restores x0, x1)
- `exceptions.s:254` - Inside `PRINT_HEX_NIBBLE` macro ✅ (saves/restores x0, x1)
- `exceptions.s:282` - Inside `print_hex64` function ✅ (saves/restores ALL registers)
- `exceptions.s:308` - Inside `print_string` function ✅ (saves/restores ALL registers)
- `lib.s:896` - Inside `uart_putc_pl011` function ✅ (leaf function, standard calling convention)

**Status:** ✅ All safe - proper register preservation

### 2. Calls to Go UART Functions

**Found:**
- `exceptions.s:188` - Inside `DEBUG_CALL_GO_PUTC` macro ✅ (protected)
- `exceptions.s:200` - Inside `DEBUG_CALL_GO_PUTS` macro ✅ (protected)
- `exceptions.s:212` - Inside `DEBUG_CALL_GO_PUTHEX64` macro ✅ (protected)
- `exceptions.s:221` - Inside `DEBUG_CALL_GO_PUTHEX32` macro ✅ (protected)

**Status:** ✅ All safe - within safe wrapper macros

### 3. Calls to Assembly Print Functions

**Found:**
- `exceptions.s:156` - Inside `DEBUG_PRINT_HEX64_SAFE` macro ✅ (protected)
- `exceptions.s:165` - Inside `DEBUG_PRINT_HEX32_SAFE` macro ✅ (protected)
- `exceptions.s:406` - Inside `test_print_functions_preserve_registers` ℹ️ (test code)
- `exceptions.s:425, 469` - Inside test success/failure handlers ℹ️ (test code)

**Status:** ✅ All safe - test code intentionally doesn't wrap (testing the functions themselves)

### 4. Usage of Safe Macros in Production Code

**Current Usage:**
- `exceptions.s:1629` - `DEBUG_PUTC_SAFE #'M'` in syscall_mmap handler ✅

**Status:** ✅ Correctly using safe macro

## Safe UART Macros Available

The following macros are available for safe UART output that preserves ALL registers:

### Direct UART Output (Low-Level)
- **`DEBUG_PUTC_SAFE char`** - Print single character
  ```asm
  DEBUG_PUTC_SAFE #'A'
  ```

- **`DEBUG_PRINT_HEX64_SAFE`** - Print x0 as 64-bit hex
  ```asm
  mov x0, x8
  DEBUG_PRINT_HEX64_SAFE
  ```

- **`DEBUG_PRINT_HEX32_SAFE`** - Print w0 as 32-bit hex
  ```asm
  mov w0, w8
  DEBUG_PRINT_HEX32_SAFE
  ```

### Go Function Calls (High-Level)
- **`DEBUG_CALL_GO_PUTC char`** - Call `uartPutcDirect(c)` safely
  ```asm
  DEBUG_CALL_GO_PUTC #'X'
  ```

- **`DEBUG_CALL_GO_PUTS`** - Call `uartPutsDirect(s)` safely
  ```asm
  ldr x0, =my_string
  DEBUG_CALL_GO_PUTS
  ```

- **`DEBUG_CALL_GO_PUTHEX64`** - Call `uartPutHex64Direct(v)` safely
  ```asm
  mov x0, x8
  DEBUG_CALL_GO_PUTHEX64
  ```

- **`DEBUG_CALL_GO_PUTHEX32`** - Call `uartPutHex32Direct(v)` safely
  ```asm
  mov w0, w8
  DEBUG_CALL_GO_PUTHEX32
  ```

## Register Safety Guarantees

All safe macros use `DEBUG_SAVE_REGS` / `DEBUG_RESTORE_REGS` which:

1. **Save:** x0-x15 (16 registers × 8 bytes = 128 bytes on stack)
2. **Execute:** UART operation
3. **Restore:** x0-x15 to exact original values

**Critical:** X0 is ALWAYS preserved, making these safe to use in syscall handlers where X0 contains the return value.

## Non-UART Assembly Functions That Are Safe

The following assembly functions properly save/restore registers:
- `print_hex64` - Saves/restores ALL registers (x0-x30)
- `print_string` - Saves/restores ALL registers (x0-x30)
- `uart_putc_pl011` - Leaf function, standard calling convention

## Recommendations

1. ✅ **ALWAYS use safe macros** in exception handlers and syscall paths
2. ✅ **Test code can use direct calls** (like `test_print_functions_preserve_registers`)
3. ✅ **Document any new debug output** with comments explaining register safety
4. ✅ **Prefer `DEBUG_PUTC_SAFE`** for simple characters (lowest overhead)
5. ✅ **Use Go wrapper macros** when you need string formatting or complex output

## Verification

To verify UART safety after adding new debug code:

1. **Build and run:**
   ```bash
   make && NOGRAPHIC=1 timeout 10 bin/run-mazboot
   ```

2. **Check for corruption:**
   - Syscall return values should be correct (not zero)
   - No unexpected crashes or register corruption
   - Output appears as expected

3. **GDB verification** (if needed):
   ```bash
   # Terminal 1: Start QEMU with debug
   NOGRAPHIC=1 DEBUG=1 bin/run-mazboot

   # Terminal 2: Run GDB
   ~/mazzy/bin/target-gdb -x debug_catch_svc.gdb flash/mazboot.elf
   (gdb) target remote :1234
   (gdb) continue
   ```

## Audit Conclusion

✅ **PASS** - All assembly UART calls are properly protected. The codebase is safe from X0 corruption due to UART debug output.

**Auditor:** Claude Sonnet 4.5
**Date:** 2025-12-27
**Files Audited:**
- src/mazboot/asm/aarch64/exceptions.s
- src/mazboot/asm/aarch64/lib.s
- src/mazboot/asm/aarch64/boot.s
- src/mazboot/asm/aarch64/timer.s
- src/mazboot/asm/aarch64/goroutine.s
- src/mazboot/asm/aarch64/async_preempt.s
- src/mazboot/asm/aarch64/get_caller_sp.s
- src/mazboot/asm/aarch64/writebarrier.s
- src/mazboot/asm/aarch64/image.s
- src/mazboot/asm/aarch64/linker_symbols.s
- src/mazboot/asm/aarch64/kmazarin_embed.s
