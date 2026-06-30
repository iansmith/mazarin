# maz15-catch.gdb — catch the kernel fatal-exception dump for MAZ-15.
#
# MAZ-15 = Bug B class: a page reaches a consumer un-zeroed, retaining stale
# ASCII/log content. Crashes carry X8=0x2064656C69616620 (" failed ") and
# ASCII-laden stacks.
#
# Usage:
#   1. go tool task qemu-for-gdb
#        (boots ARM64+HVF frozen at reset, gdb stub on :1234)
#   2. /Users/iansmith/mazzy/bin/target-gdb build/kmazarin.elf -x tools/gdb/maz15-catch.gdb
#   3. (gdb) continue
#
# Breakpoints use symbolic locations so they survive a kernel rebuild.

set pagination off
set architecture aarch64
target remote localhost:1234

# --- bp1: deterministic fatal-fault catch (the kernel's " EL=" dump printer) ---
# Line tracks `MRS CurrentEL, R11` inside the "Unknown exception" rich-dump
# block. Re-verify after any edit to that file (the line shifts):
#   grep -n 'MRS	CurrentEL, R11' kmazarin/kmazarin/exceptions_arm64.s
break exceptions_arm64.s:496
commands
  printf "\n================  MAZ-15 KERNEL FATAL FAULT  ================\n"
  # Robust dumps first (GPRs + stack always readable, even on HVF gdbstub).
  printf "--- live GPRs (exception-handler context) ---\n"
  info registers
  printf "\n--- exception stack / saved frame (HUNT FOR ASCII: ' failed ', 'mkdir /tmp/') ---\n"
  x/192gx $sp
  # System regs last — QEMU's HVF gdbstub may not expose these by name.
  # If you see "void"/errors here, the saved frame above still has the
  # faulting context; also try 'info all-registers'.
  printf "\n--- fault system registers ---\n"
  printf "ELR_EL1 (faulting PC)   = 0x%lx\n", $ELR_EL1
  printf "FAR_EL1 (faulting addr) = 0x%lx\n", $FAR_EL1
  printf "ESR_EL1 (syndrome)      = 0x%lx\n", $ESR_EL1
  x/16i $ELR_EL1
  printf "=============================================================\n"
  printf "STOPPED. Inspect; 'continue' lets the kernel finish its dump.\n"
end

# --- bp2: kernel-side Go fatal errors (clean Go backtrace) ---
break runtime.throw
commands
  printf "\n=== kernel runtime.throw ===\n"
  printf "msg ptr=0x%lx len=%ld\n", $x0, $x1
  x/s $x0
  backtrace
  printf "STOPPED at kernel runtime.throw.\n"
end

printf "\nmaz15-catch.gdb loaded:\n"
printf "  bp1  exceptions_arm64.s:496   kernel fatal-exception dump path\n"
printf "  bp2  runtime.throw            kernel Go panics\n"
printf "Run 'continue'.\n"
