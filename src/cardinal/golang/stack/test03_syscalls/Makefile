# Makefile for syscall interface study

# Go compiler settings
GOOS := linux
GOARCH := arm64
GO := GOOS=$(GOOS) GOARCH=$(GOARCH) go

# Output files
BINARY := test
ASM_OUTPUT := asm_output.txt
DISASM := disasm.txt
ANALYSIS := ANALYSIS.md

# Toolchain
OBJDUMP := ~/mazzy/aarch64-none-elf/bin/aarch64-none-elf-objdump

.PHONY: all clean asm disasm analyze

all: $(BINARY) asm disasm

# Build the binary
$(BINARY): main.go
	$(GO) build -o $(BINARY) main.go

# Generate compiler assembly output
asm: main.go
	$(GO) build -gcflags='-S' main.go 2>&1 | tee $(ASM_OUTPUT)

# Generate disassembly of final binary
disasm: $(BINARY)
	$(OBJDUMP) -d $(BINARY) > $(DISASM)

# Find specific syscall-related functions
.PHONY: view-create view-write view-close view-open view-read
.PHONY: find-svc find-syscall find-rawsyscall

# View os.Create path
view-create: $(ASM_OUTPUT)
	@echo "=== Looking for os.Create and related syscalls ==="
	@grep -E "(os\.Create|os\.OpenFile|syscall\.Open)" $(ASM_OUTPUT) | head -n 20

# View syscall wrappers
view-write: $(DISASM)
	@echo "=== syscall.Write ==="
	@grep -A 40 "<syscall.Write>:" $(DISASM) | head -n 40

view-close: $(DISASM)
	@echo "=== syscall.Close ==="
	@grep -A 30 "<syscall.Close>:" $(DISASM) | head -n 30

view-open: $(DISASM)
	@echo "=== syscall.Open ==="
	@grep -A 40 "<syscall.Open>:" $(DISASM) | head -n 40

view-read: $(DISASM)
	@echo "=== syscall.Read ==="
	@grep -A 40 "<syscall.Read>:" $(DISASM) | head -n 40

# Find SVC instruction usage
find-svc: $(DISASM)
	@echo "=== Looking for SVC instructions (trap to OS) ==="
	@grep -B 5 "svc" $(DISASM) | head -n 100

# Find syscall and RawSyscall functions
find-syscall: $(DISASM)
	@echo "=== Syscall interface functions ==="
	@grep -E "(Syscall|RawSyscall)" $(DISASM) | head -n 50

find-rawsyscall: $(DISASM)
	@echo "=== RawSyscall6 (the actual syscall interface) ==="
	@grep -A 50 "<syscall.RawSyscall6>:" $(DISASM)

# Clean build artifacts
clean:
	rm -f $(BINARY) $(ASM_OUTPUT) $(DISASM) $(ANALYSIS) /tmp/test_syscall.txt

# Help
help:
	@echo "Syscall Interface Study - test03_syscalls"
	@echo ""
	@echo "Targets:"
	@echo "  all              - Build binary, generate asm and disasm"
	@echo "  asm              - Generate Go compiler assembly output"
	@echo "  disasm           - Generate objdump disassembly"
	@echo "  view-create      - View os.Create path"
	@echo "  view-write       - View syscall.Write"
	@echo "  view-close       - View syscall.Close"
	@echo "  view-open        - View syscall.Open"
	@echo "  view-read        - View syscall.Read"
	@echo "  find-svc         - Find all SVC instructions"
	@echo "  find-syscall     - Find Syscall/RawSyscall functions"
	@echo "  find-rawsyscall  - View RawSyscall6 (low-level interface)"
	@echo "  clean            - Remove build artifacts"
	@echo ""
	@echo "Files generated:"
	@echo "  $(BINARY)      - Compiled ARM64 binary"
	@echo "  $(ASM_OUTPUT)  - Go compiler assembly output"
	@echo "  $(DISASM)      - objdump disassembly"
