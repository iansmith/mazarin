# Makefile for Go ↔ Assembly bridge study

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
$(BINARY): main.go bridge.s
	$(GO) build -o $(BINARY) .

# Generate compiler assembly output
asm: main.go bridge.s
	$(GO) build -gcflags='-S' . 2>&1 | tee $(ASM_OUTPUT)

# Generate disassembly of final binary
disasm: $(BINARY)
	$(OBJDUMP) -d $(BINARY) > $(DISASM)

# Quick view of specific functions
.PHONY: view-main view-bridge view-helper view-bridge-asm

view-main: $(DISASM)
	@echo "=== main.main ==="
	@grep -A 50 "<main.main>:" $(DISASM) | head -n 50

view-bridge: $(DISASM)
	@echo "=== main.AsmBridge (Go compiler output) ==="
	@grep -A 50 "main.AsmBridge STEXT" $(ASM_OUTPUT) | head -n 60

view-bridge-asm: $(DISASM)
	@echo "=== main.AsmBridge (final disassembly) ==="
	@grep -A 80 "<main.AsmBridge>:" $(DISASM) | head -n 80

view-helper: $(DISASM)
	@echo "=== main.GoHelper ==="
	@grep -A 80 "<main.GoHelper>:" $(DISASM) | head -n 80

# Clean build artifacts
clean:
	rm -f $(BINARY) $(ASM_OUTPUT) $(DISASM) $(ANALYSIS)

# Help
help:
	@echo "Go ↔ Assembly Bridge Study - test02_asm_bridge"
	@echo ""
	@echo "Targets:"
	@echo "  all              - Build binary, generate asm and disasm"
	@echo "  asm              - Generate Go compiler assembly output"
	@echo "  disasm           - Generate objdump disassembly"
	@echo "  view-main        - Quick view of main() disassembly"
	@echo "  view-bridge      - View AsmBridge Go compiler output"
	@echo "  view-bridge-asm  - View AsmBridge final disassembly"
	@echo "  view-helper      - View GoHelper disassembly"
	@echo "  clean            - Remove build artifacts"
	@echo ""
	@echo "Files generated:"
	@echo "  $(BINARY)      - Compiled ARM64 binary"
	@echo "  $(ASM_OUTPUT)  - Go compiler assembly output"
	@echo "  $(DISASM)      - objdump disassembly"
