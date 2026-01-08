# Makefile for cardinal - Go Native Toolchain Build
# Uses Go's internal linker to build bare-metal ARM64 kernel

# Set default target
.DEFAULT_GOAL := all

# Go compiler and tools
GO = /Users/iansmith/mazzy/bin/go
GOARCH = arm64
GOOS = linux

# Debug build: disable optimizations and inlining for better GDB debugging
# Usage: make cardinal DEBUG=1
DEBUG ?= 0
ifeq ($(DEBUG),1)
    GCFLAGS = -gcflags="all=-N -l"
else
    GCFLAGS =
endif

# IMPORTANT: CGO Policy
# We NEVER use CGO in this project. All Go builds must explicitly set CGO_ENABLED=0
# to ensure static binaries without C dependencies for our bare-metal environment.

# Source directory
CARDINAL_SRC = src/cardinal

# Go package locations
GO_PACKAGE_DIR = $(CARDINAL_SRC)/golang/main
ASM_PACKAGE_DIR = $(CARDINAL_SRC)/golang/asm

# Go source files (all files in golang/main and golang/asm packages)
GO_SRC = $(wildcard $(GO_PACKAGE_DIR)/*.go)
GO_NATIVE_SRC = $(wildcard $(GO_PACKAGE_DIR)/*.go) \
                $(wildcard $(ASM_PACKAGE_DIR)/*.go) \
                $(wildcard $(ASM_PACKAGE_DIR)/arch/arm64/*.go) \
                $(wildcard $(ASM_PACKAGE_DIR)/arch/arm64/*.s) \
                $(wildcard $(ASM_PACKAGE_DIR)/dev/*.go) \
                $(wildcard $(ASM_PACKAGE_DIR)/dev/*.s) \
                $(wildcard $(ASM_PACKAGE_DIR)/kernel/*.go) \
                $(wildcard $(ASM_PACKAGE_DIR)/kernel/*.s)

# Build output directory structure
BUILD_DIR = build

# Output files
CARDINAL_BINARY = $(BUILD_DIR)/cardinal.elf

# Kmazarin source and binary
KMAZARIN_SRC = src/kmazarin/golang/kmazarin
KMAZARIN_BINARY = $(BUILD_DIR)/kmazarin.elf

# Tools
PATCH_ENTRY_TOOL = $(CARDINAL_SRC)/tools/patch-entry.go
COMPUTE_LINKER_VALUES_TOOL = $(CARDINAL_SRC)/tools/compute-linker-values.go
INCBIN2GOASM_TOOL = $(CARDINAL_SRC)/tools/incbin2goasm.go
FIX_GO_ELF_TOOL = tools/fix-go-elf.py

# Generated embedded data
KMAZARIN_DATA_ASM = $(ASM_PACKAGE_DIR)/dev/kmazarin_data_arm64.s

# Ensure build directory exists
$(BUILD_DIR):
	@mkdir -p $@

# =========================================
# Kmazarin Embedding
# =========================================
# Generate Go assembly with embedded kmazarin binary

$(KMAZARIN_DATA_ASM): $(KMAZARIN_BINARY)
	@echo "Generating embedded kmazarin data..."
	@$(GO) run $(INCBIN2GOASM_TOOL) -sym kmazarin_binary -global $< > $@
	@echo "Generated $(KMAZARIN_DATA_ASM) ($$(wc -l < $@ | tr -d ' ') lines)"

# =========================================
# Cardinal Build (Go Native Toolchain)
# =========================================
# Uses Go's internal linker with -T flag to set load address
# Then patches entry point and linker symbol values

$(CARDINAL_BINARY): $(GO_NATIVE_SRC) $(CARDINAL_SRC)/golang/go.mod $(KMAZARIN_BINARY) $(KMAZARIN_DATA_ASM) $(CARDINAL_SRC)/golang/constants/layout.go $(COMPUTE_LINKER_VALUES_TOOL) | $(BUILD_DIR)
	@echo "Building cardinal with Go native toolchain..."
	@cd $(CARDINAL_SRC)/golang && \
		CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build \
			-tags "qemuvirt aarch64" \
			$(GCFLAGS) \
			-ldflags="-checklinkname=0 -T 0x40100000" \
			-o $(abspath $@) \
			./main
	@echo "Patching entry point to _cardinal_boot..."
	@$(GO) run $(PATCH_ENTRY_TOOL) $@ _cardinal_boot
	@echo "Patching linker values..."
	@cd $(CARDINAL_SRC)/golang && $(GO) run ../tools/compute-linker-values.go -patch -kmazarin $(abspath $(KMAZARIN_BINARY)) $(abspath $@)
	@echo "Fixing ELF for QEMU compatibility..."
	@python3 $(FIX_GO_ELF_TOOL) $@
	@echo "cardinal ready at $@"

# =========================================
# Kmazarin Kernel Build
# =========================================
# Builds kmazarin as a static Go binary with high-memory relocation
#
# Build process:
# 1. Build kmazarin ELF at low memory (0x41800000) - Go linker limitation
# 2. Fix ELF headers for QEMU compatibility (negative offsets)
# 3. **CRITICAL**: Relocate to high memory (0xFFFFFFFF41800000) for kernel/user separation
#    - Updates ELF entry point and segment VMAs
#    - Relocates all pointers in data sections (.data, .rodata, .gopclntab, etc.)
#    - PC-relative instructions (ADRP) work unchanged (relative distances preserved)
#    - Takes ~0.7ms, ~15k relocations
# =========================================

# Kmazarin package directories (main + sub-packages)
KMAZARIN_KMEM_SRC = src/kmazarin/golang/kmem
KMAZARIN_KSYSCALL_SRC = src/kmazarin/golang/ksyscall

$(KMAZARIN_BINARY): $(wildcard $(KMAZARIN_SRC)/*.go) $(wildcard $(KMAZARIN_SRC)/*.s) \
                    $(wildcard $(KMAZARIN_KMEM_SRC)/*.go) $(wildcard $(KMAZARIN_KMEM_SRC)/*.s) \
                    $(wildcard $(KMAZARIN_KSYSCALL_SRC)/*.go) $(wildcard $(KMAZARIN_KSYSCALL_SRC)/*.s) \
                    tools/kmazarin-entry.sh tools/print-kmazarin-addr.go tools/relocate-kmazarin.go src/cardinal/golang/constants/layout.go | $(BUILD_DIR)
	$(eval KMAZARIN_LOAD_ADDR := $(shell ./tools/kmazarin-entry.sh))
	@echo "Building kmazarin kernel (static Go binary at $(KMAZARIN_LOAD_ADDR))..."
	@cd $(KMAZARIN_SRC) && \
		CGO_ENABLED=0 \
		GOTOOLCHAIN=auto \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -tags "qemuvirt aarch64" $(GCFLAGS) -ldflags="-T $(KMAZARIN_LOAD_ADDR)" -o $(abspath $(KMAZARIN_BINARY)) .
	@echo "Fixing kmazarin ELF for QEMU compatibility..."
	@python3 $(FIX_GO_ELF_TOOL) $(KMAZARIN_BINARY)
	@echo "Relocating kmazarin to high memory (0xFFFFFFFF41800000)..."
	@$(GO) run tools/relocate-kmazarin.go $(KMAZARIN_BINARY) $(KMAZARIN_BINARY).tmp
	@mv $(KMAZARIN_BINARY).tmp $(KMAZARIN_BINARY)
	@echo "Kmazarin kernel built and relocated at $(KMAZARIN_BINARY)"

# =========================================
# Main Targets
# =========================================

# Build cardinal
cardinal: $(CARDINAL_BINARY)

# Build kmazarin
kmazarin: $(KMAZARIN_BINARY)

# Default: build both
all: cardinal kmazarin

# Test target - run Go tests
test:
	@echo "Running tests..."
	@cd $(CARDINAL_SRC)/golang && GOTOOLCHAIN=auto $(GO) test -v ./bitfield

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f $(KMAZARIN_DATA_ASM)
	@echo "Cleaned."

# Phony targets
.PHONY: all clean cardinal kmazarin test
