# Makefile for program entry point study

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
READELF := ~/mazzy/aarch64-none-elf/bin/aarch64-none-elf-readelf

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

# Find entry point and initialization code
.PHONY: view-entry view-rt0 view-sysargs view-runtime-args find-entry

# View the ELF entry point
find-entry: $(BINARY)
	@echo "=== ELF Entry Point ==="
	@$(READELF) -h $(BINARY) | grep "Entry point"

# View _rt0_arm64_linux (the true entry point)
view-rt0: $(DISASM)
	@echo "=== _rt0_arm64_linux (entry point from Linux kernel) ==="
	@grep -A 60 "<_rt0_arm64_linux>:" $(DISASM) | head -n 60

# View rt0_go (main initialization)
view-rt0-go: $(DISASM)
	@echo "=== runtime.rt0_go (main initialization routine) ==="
	@grep -A 100 "<runtime.rt0_go>:" $(DISASM) | head -n 100

# View runtime.args (processes argc/argv/envp/auxv)
view-runtime-args: $(DISASM)
	@echo "=== runtime.args (reads argc/argv/envp from stack) ==="
	@grep -A 80 "<runtime.args>:" $(DISASM)

# View runtime.sysargs (fallback that reads /proc/self/auxv)
view-sysargs: $(DISASM)
	@echo "=== runtime.sysargs (fallback for auxv) ==="
	@grep -A 50 "<runtime.sysargs>:" $(DISASM)

# View runtime.osinit (sets physPageSize from auxv)
view-osinit: $(DISASM)
	@echo "=== runtime.osinit (uses auxv to set physPageSize) ==="
	@grep -A 80 "<runtime.osinit>:" $(DISASM)

# Clean build artifacts
clean:
	rm -f $(BINARY) $(ASM_OUTPUT) $(DISASM) $(ANALYSIS)

# Help
help:
	@echo "Program Entry Point Study - test04_entry_point"
	@echo ""
	@echo "Targets:"
	@echo "  all              - Build binary, generate asm and disasm"
	@echo "  asm              - Generate Go compiler assembly output"
	@echo "  disasm           - Generate objdump disassembly"
	@echo "  find-entry       - Show ELF entry point address"
	@echo "  view-rt0         - View _rt0_arm64_linux (true entry)"
	@echo "  view-rt0-go      - View runtime.rt0_go (initialization)"
	@echo "  view-runtime-args - View runtime.args (argc/argv/envp parser)"
	@echo "  view-sysargs     - View runtime.sysargs (auxv fallback)"
	@echo "  view-osinit      - View runtime.osinit (physPageSize setup)"
	@echo "  clean            - Remove build artifacts"
	@echo ""
	@echo "Files generated:"
	@echo "  $(BINARY)      - Compiled ARM64 binary"
	@echo "  $(ASM_OUTPUT)  - Go compiler assembly output"
	@echo "  $(DISASM)      - objdump disassembly"
