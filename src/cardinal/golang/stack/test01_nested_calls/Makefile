# Makefile for ARM64 calling convention study

# Go compiler settings
GOOS := linux
GOARCH := arm64
GO := GOOS=$(GOOS) GOARCH=$(GOARCH) go

# Output files
BINARY := test
ASM_OUTPUT := asm_output.txt
DISASM := disasm.txt
ANALYSIS := analysis.md

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

# Quick view of specific functions
.PHONY: view-main view-level1 view-level2 view-level3 view-level4

view-main: $(DISASM)
	@echo "=== main.main ==="
	@grep -A 100 "<main.main>:" $(DISASM) | head -n 50

view-level1: $(DISASM)
	@echo "=== main.level1 ==="
	@grep -A 100 "<main.level1>:" $(DISASM) | head -n 50

view-level2: $(DISASM)
	@echo "=== main.level2 ==="
	@grep -A 100 "<main.level2>:" $(DISASM) | head -n 50

view-level3: $(DISASM)
	@echo "=== main.level3 ==="
	@grep -A 100 "<main.level3>:" $(DISASM) | head -n 50

view-level4: $(DISASM)
	@echo "=== main.level4 ==="
	@grep -A 100 "<main.level4>:" $(DISASM) | head -n 50

# Clean build artifacts
clean:
	rm -f $(BINARY) $(ASM_OUTPUT) $(DISASM) $(ANALYSIS)

# Help
help:
	@echo "ARM64 Calling Convention Study - test01_nested_calls"
	@echo ""
	@echo "Targets:"
	@echo "  all          - Build binary, generate asm and disasm"
	@echo "  asm          - Generate Go compiler assembly output"
	@echo "  disasm       - Generate objdump disassembly"
	@echo "  view-main    - Quick view of main() disassembly"
	@echo "  view-level1  - Quick view of level1() disassembly"
	@echo "  view-level2  - Quick view of level2() disassembly"
	@echo "  view-level3  - Quick view of level3() disassembly"
	@echo "  view-level4  - Quick view of level4() disassembly"
	@echo "  clean        - Remove build artifacts"
	@echo ""
	@echo "Files generated:"
	@echo "  $(BINARY)      - Compiled ARM64 binary"
	@echo "  $(ASM_OUTPUT)  - Go compiler assembly output"
	@echo "  $(DISASM)      - objdump disassembly"
