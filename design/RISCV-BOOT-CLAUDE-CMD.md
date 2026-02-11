# Claude Code Command for Autonomous RISC-V Boot Implementation

## Command

```bash
cd ~/mazzy-riscv && claude --allowedTools 'Bash(run tests:*),Bash(build:*),Bash(git operations:*),Bash(view output:*),Read,Write,Edit,Glob,Grep' -p "You are implementing the RISC-V boot support for the mazzy project's diplomat bootloader. Read the detailed implementation plan at design/RISCV-BOOT-IMPLEMENTATION.md and follow it phase by phase. Also read CLAUDE.md for project conventions.

IMPORTANT ENVIRONMENT:
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

Build command: \$GO tool task diplomat-riscv64
Test command: \$GO tool task run-diplomat-riscv64 TIMEOUT=10
Read serial log: \$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
NEVER read serial logs directly — always use safe-serial-read.

Work through each phase in order. After each phase, build and test. If a build fails, fix the error before moving on. If a test produces no output or crashes, use the QEMU monitor (echo 'info registers' | nc 127.0.0.1 4447) to diagnose.

CRITICAL RULES:
1. Always use //go:build riscv64 tags on new RISC-V files
2. Never break ARM64 or x86_64 builds — use build tags for arch-specific code
3. All low-level functions must be //go:nosplit
4. No Go heap allocations — use dNew[T]() from dmalloc.go
5. CSR instructions in assembly must use WORD encodings
6. Test after every phase before proceeding to the next
7. Commit working code after each successful phase

Start with Phase 1 (assembly entry + UART) and work forward."
```

## Alternative: With max autonomy

```bash
cd ~/mazzy-riscv && claude --allowedTools 'Bash(*),Read,Write,Edit,Glob,Grep' -p "$(cat design/RISCV-BOOT-IMPLEMENTATION.md)

Implement the above plan phase by phase. Environment: GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

Build: \$GO tool task diplomat-riscv64
Test: \$GO tool task run-diplomat-riscv64 TIMEOUT=10
Log: \$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
NEVER read serial logs directly.

Start with Phase 1. Build and test after each phase. Commit after each working phase. Fix errors before moving on."
```

## Notes

- The `--allowedTools` flag pre-approves tool usage so Claude doesn't prompt for permission
- The `-p` flag provides the initial prompt (non-interactive mode)
- `Bash(*)` allows all shell commands; the more restrictive version limits to specific categories
- Claude will auto-commit after each phase if instructed
- Monitor progress by checking git log: `cd ~/mazzy-riscv && git log --oneline -10`
- Check serial output: `$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log`
- If Claude gets stuck, you can resume interactively: `cd ~/mazzy-riscv && claude`
