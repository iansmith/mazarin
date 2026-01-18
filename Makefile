# Makefile for cardinal - Go Native Toolchain Build
# Uses Go's internal linker to build bare-metal ARM64 kernel

# Set default target
.DEFAULT_GOAL := all

# Go compiler and tools
# Default to 'go' from PATH, can be overridden: make GO=/path/to/go cardinal
GO ?= go
GOARCH = arm64
GOOS = linux
REQUIRED_GO_VERSION = 1.25.5

# Version check - verify Go version is exactly 1.25.5
# This runs as a dependency of the main targets
.PHONY: check-go-version
check-go-version:
	@GOTOOLCHAIN=local $(GO) version > /dev/null 2>&1 || { \
		echo ""; \
		echo "ERROR: Go compiler not found."; \
		echo ""; \
		echo "Please install Go $(REQUIRED_GO_VERSION) or set GO to point to it:"; \
		echo "  make GO=/path/to/go$(REQUIRED_GO_VERSION) cardinal"; \
		echo ""; \
		exit 1; \
	}
	@GO_VERSION=$$(GOTOOLCHAIN=local $(GO) version | grep -oE 'go[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/go//'); \
	if [ "$$GO_VERSION" != "$(REQUIRED_GO_VERSION)" ]; then \
		echo ""; \
		echo "ERROR: Go version mismatch."; \
		echo ""; \
		echo "  Required: go$(REQUIRED_GO_VERSION)"; \
		echo "  Found:    go$$GO_VERSION (from: $(GO))"; \
		echo ""; \
		echo "Please install Go $(REQUIRED_GO_VERSION) or set GO to point to it:"; \
		echo "  make GO=/path/to/go$(REQUIRED_GO_VERSION) cardinal"; \
		echo ""; \
		exit 1; \
	fi
	@echo "Go version check passed: go$(REQUIRED_GO_VERSION)"

# Runtime overlay for kmazarin - patches malloc.go for high-memory heap support
# The overlay JSON is generated dynamically based on GOROOT
KMAZARIN_OVERLAY = $(BUILD_DIR)/kmazarin-overlay.json
RUNTIME_PATCHES_DIR = runtime-patches

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

# Host tools directory (compiled binaries for local system, not target)
# All tools are built with host GOOS/GOARCH, not linux/arm64
TOOLS_BIN_DIR = $(BUILD_DIR)/tools

# Tool binaries (compiled from tools/*.go)
TOOL_PATCH_ENTRY = $(TOOLS_BIN_DIR)/patch-entry
TOOL_COMPUTE_LINKER = $(TOOLS_BIN_DIR)/compute-linker-values
TOOL_INCBIN2GOASM = $(TOOLS_BIN_DIR)/incbin2goasm
TOOL_FIX_GO_ELF = $(TOOLS_BIN_DIR)/fix-go-elf
TOOL_PRINT_KMAZARIN_ADDR = $(TOOLS_BIN_DIR)/print-kmazarin-addr
TOOL_RELOCATE_KMAZARIN = $(TOOLS_BIN_DIR)/relocate-kmazarin
TOOL_BUILD = $(TOOLS_BIN_DIR)/build
TOOL_RUN = $(TOOLS_BIN_DIR)/run
TOOL_STOP = $(TOOLS_BIN_DIR)/stop

# Generated embedded data
KMAZARIN_DATA_ASM = $(ASM_PACKAGE_DIR)/dev/kmazarin_data_arm64.s
BOOT_IMAGE_DATA_ASM = $(ASM_PACKAGE_DIR)/dev/boot_mazarin_data_arm64.s

# Boot image source
BOOT_IMAGE_BIN = assets/boot-mazarin.bin

# Ensure build directory exists
$(BUILD_DIR):
	@mkdir -p $@

# =========================================
# Kmazarin Embedding
# =========================================
# Generate Go assembly with embedded kmazarin binary

$(KMAZARIN_DATA_ASM): $(KMAZARIN_BINARY) $(TOOL_INCBIN2GOASM)
	@echo "Generating embedded kmazarin data..."
	@$(TOOL_INCBIN2GOASM) -sym kmazarin_binary -global $< > $@
	@echo "Generated $(KMAZARIN_DATA_ASM) ($$(wc -l < $@ | tr -d ' ') lines)"

# Generate embedded boot image data
$(BOOT_IMAGE_DATA_ASM): $(BOOT_IMAGE_BIN) $(TOOL_INCBIN2GOASM)
	@echo "Generating embedded boot image data..."
	@$(TOOL_INCBIN2GOASM) -sym _binary_boot_mazarin_bin -global $< > $@
	@echo "Generated $(BOOT_IMAGE_DATA_ASM) ($$(wc -l < $@ | tr -d ' ') lines)"

# =========================================
# Cardinal Build (Go Native Toolchain)
# =========================================
# Uses Go's internal linker with -T flag to set load address
# Then patches entry point and linker symbol values

$(CARDINAL_BINARY): $(GO_NATIVE_SRC) $(CARDINAL_SRC)/golang/go.mod $(KMAZARIN_BINARY) $(KMAZARIN_DATA_ASM) $(BOOT_IMAGE_DATA_ASM) $(CARDINAL_SRC)/golang/constants/layout.go \
                    $(TOOL_PATCH_ENTRY) $(TOOL_COMPUTE_LINKER) $(TOOL_FIX_GO_ELF) | $(BUILD_DIR)
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
	@$(TOOL_PATCH_ENTRY) $@ _cardinal_boot
	@echo "Patching linker values..."
	@$(TOOL_COMPUTE_LINKER) -patch -kmazarin $(KMAZARIN_BINARY) $@
	@echo "Fixing ELF for QEMU compatibility..."
	@$(TOOL_FIX_GO_ELF) $@
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

# Kmazarin package directories (main + all sub-packages)
KMAZARIN_BASE = src/kmazarin/golang
KMAZARIN_KMEM_SRC = $(KMAZARIN_BASE)/kmem
KMAZARIN_KSYSCALL_SRC = $(KMAZARIN_BASE)/ksyscall
KMAZARIN_KIRQ_SRC = $(KMAZARIN_BASE)/kirq
KMAZARIN_KTHREAD_SRC = $(KMAZARIN_BASE)/kthread
KMAZARIN_DEVICE_SRC = $(KMAZARIN_BASE)/device
KMAZARIN_DTB_SRC = $(KMAZARIN_BASE)/dtb
KMAZARIN_UART_SRC = $(KMAZARIN_BASE)/uart
KMAZARIN_CONSOLE_SRC = $(KMAZARIN_BASE)/console
KMAZARIN_DEVICEAPI_SRC = $(KMAZARIN_BASE)/deviceapi
KMAZARIN_GIC_SRC = $(KMAZARIN_BASE)/arch/arm64/gic
KMAZARIN_VIRTIO_SRC = $(KMAZARIN_BASE)/virtio
KMAZARIN_RTC_SRC = $(KMAZARIN_BASE)/rtc

# All kmazarin Go/assembly sources
KMAZARIN_ALL_SRC = $(wildcard $(KMAZARIN_SRC)/*.go) $(wildcard $(KMAZARIN_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_KMEM_SRC)/*.go) $(wildcard $(KMAZARIN_KMEM_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_KSYSCALL_SRC)/*.go) $(wildcard $(KMAZARIN_KSYSCALL_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_KIRQ_SRC)/*.go) $(wildcard $(KMAZARIN_KIRQ_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_KTHREAD_SRC)/*.go) $(wildcard $(KMAZARIN_KTHREAD_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_DEVICE_SRC)/*.go) $(wildcard $(KMAZARIN_DEVICE_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_DTB_SRC)/*.go) $(wildcard $(KMAZARIN_DTB_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_UART_SRC)/*.go) $(wildcard $(KMAZARIN_UART_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_CONSOLE_SRC)/*.go) $(wildcard $(KMAZARIN_CONSOLE_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_DEVICEAPI_SRC)/*.go) $(wildcard $(KMAZARIN_DEVICEAPI_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_GIC_SRC)/*.go) $(wildcard $(KMAZARIN_GIC_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_VIRTIO_SRC)/*.go) $(wildcard $(KMAZARIN_VIRTIO_SRC)/*.s) \
                   $(wildcard $(KMAZARIN_RTC_SRC)/*.go) $(wildcard $(KMAZARIN_RTC_SRC)/*.s)

# Runtime patch files for overlay
RUNTIME_PATCH_MALLOC = $(RUNTIME_PATCHES_DIR)/malloc.go
RUNTIME_PATCH_PREEMPT = $(RUNTIME_PATCHES_DIR)/preempt.go
RUNTIME_PATCH_SYSCALL = $(RUNTIME_PATCHES_DIR)/syscall/syscall_linux.go

# Generate overlay JSON for kmazarin runtime patches
# This allows using vanilla Go with our runtime patches:
#   - malloc.go: high-memory heap support (arenaBaseOffset for TTBR1 space)
#   - preempt.go: expose preemption offsets for kernel IRQ handling (added GetPreemptOffsets)
#   - syscall_linux_arm64.go: RawSyscall6 calls dispatcher directly (no SVC, pure Go)
# GOTOOLCHAIN=local ensures we get the actual GOROOT, not a downloaded toolchain
$(KMAZARIN_OVERLAY): $(RUNTIME_PATCH_MALLOC) $(RUNTIME_PATCH_PREEMPT) $(RUNTIME_PATCH_SYSCALL) | $(BUILD_DIR)
	@echo "Generating runtime overlay for kmazarin..."
	@GOROOT=$$(GOTOOLCHAIN=local $(GO) env GOROOT) && \
		echo "{\"Replace\":{\
\"$$GOROOT/src/runtime/malloc.go\":\"$(abspath $(RUNTIME_PATCH_MALLOC))\",\
\"$$GOROOT/src/runtime/preempt.go\":\"$(abspath $(RUNTIME_PATCH_PREEMPT))\",\
\"$$GOROOT/src/syscall/syscall_linux.go\":\"$(abspath $(RUNTIME_PATCH_SYSCALL))\"\
}}" > $(KMAZARIN_OVERLAY)
	@echo "  Overlay: $(KMAZARIN_OVERLAY)"

$(KMAZARIN_BINARY): $(KMAZARIN_ALL_SRC) $(KMAZARIN_OVERLAY) src/cardinal/golang/constants/layout.go \
                    $(TOOL_PRINT_KMAZARIN_ADDR) $(TOOL_FIX_GO_ELF) $(TOOL_RELOCATE_KMAZARIN) | $(BUILD_DIR)
	$(eval KMAZARIN_LOAD_ADDR := $(shell $(TOOL_PRINT_KMAZARIN_ADDR)))
	@echo "Building kmazarin kernel (static Go binary at $(KMAZARIN_LOAD_ADDR))..."
	@cd $(KMAZARIN_SRC) && \
		CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(KMAZARIN_OVERLAY)) -tags "qemuvirt aarch64" $(GCFLAGS) -ldflags="-T $(KMAZARIN_LOAD_ADDR)" -o $(abspath $(KMAZARIN_BINARY)) .
	@echo "Fixing kmazarin ELF for QEMU compatibility..."
	@$(TOOL_FIX_GO_ELF) $(KMAZARIN_BINARY)
	@echo "Relocating kmazarin to high memory (0xFFFFFFFF41800000)..."
	@$(TOOL_RELOCATE_KMAZARIN) $(KMAZARIN_BINARY) $(KMAZARIN_BINARY).tmp
	@mv $(KMAZARIN_BINARY).tmp $(KMAZARIN_BINARY)
	@echo "Kmazarin kernel built and relocated at $(KMAZARIN_BINARY)"

# =========================================
# Host Tools (for local system)
# =========================================
# These tools run on the build host, not the target.
# Built with GOWORK=off to avoid go.work version requirements.
# IMPORTANT: No GOOS/GOARCH set - uses host defaults.

$(TOOLS_BIN_DIR):
	@mkdir -p $@

# Build-time tools
$(TOOL_PATCH_ENTRY): tools/patch-entry.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# This tool imports cardinal/constants, so must be built from cardinal module
$(TOOL_COMPUTE_LINKER): $(CARDINAL_SRC)/tools/compute-linker-values.go $(CARDINAL_SRC)/golang/constants/layout.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@cd $(CARDINAL_SRC)/golang && GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $(abspath $@) ../tools/compute-linker-values.go

$(TOOL_INCBIN2GOASM): tools/incbin2goasm.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

$(TOOL_FIX_GO_ELF): tools/fix-go-elf.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# This tool imports cardinal/constants, so must be built from cardinal module
$(TOOL_PRINT_KMAZARIN_ADDR): $(CARDINAL_SRC)/tools/print-kmazarin-addr.go $(CARDINAL_SRC)/golang/constants/layout.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@cd $(CARDINAL_SRC)/golang && GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $(abspath $@) ../tools/print-kmazarin-addr.go

$(TOOL_RELOCATE_KMAZARIN): tools/relocate-kmazarin.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# User-facing tools
$(TOOL_BUILD): tools/cmd-build.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

$(TOOL_RUN): tools/cmd-run.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

$(TOOL_STOP): tools/cmd-stop.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# Build all host tools
host-tools: $(TOOL_PATCH_ENTRY) $(TOOL_COMPUTE_LINKER) $(TOOL_INCBIN2GOASM) \
            $(TOOL_FIX_GO_ELF) $(TOOL_PRINT_KMAZARIN_ADDR) $(TOOL_RELOCATE_KMAZARIN) \
            $(TOOL_BUILD) $(TOOL_RUN) $(TOOL_STOP)

# =========================================
# Main Targets
# =========================================

# Build cardinal (includes Go version check)
cardinal: check-go-version $(CARDINAL_BINARY)

# Build kmazarin (includes Go version check)
kmazarin: check-go-version $(KMAZARIN_BINARY)

# =========================================
# Flock Userspace Programs
# =========================================
# Userspace programs use the same syscall overlay as kmazarin
# but don't need high-memory relocation

FLOCK_BASE = src/flock
PRIEST_SRC = $(FLOCK_BASE)/cmd/priest
PRIEST_BINARY = $(BUILD_DIR)/priest.elf

# Priest sources
PRIEST_ALL_SRC = $(wildcard $(PRIEST_SRC)/*.go) \
                 $(wildcard src/mazarin/sys/*.go)

$(PRIEST_BINARY): $(PRIEST_ALL_SRC) | $(BUILD_DIR)
	@echo "Building priest (first userspace program)..."
	@cd $(PRIEST_SRC) && \
		CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -tags "qemuvirt aarch64" $(GCFLAGS) -o $(abspath $@) .
	@echo "Priest built at $@"

# Build priest (includes Go version check)
# Note: No overlay - userspace uses real SVC syscalls that trap to kernel
priest: check-go-version $(PRIEST_BINARY)

# Default: build both
all: cardinal kmazarin

# Test target - run Go tests
test: check-go-version
	@echo "Running tests..."
	@cd $(CARDINAL_SRC)/golang && GOTOOLCHAIN=local $(GO) test -v ./bitfield

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f $(KMAZARIN_DATA_ASM)
	rm -f $(BOOT_IMAGE_DATA_ASM)
	@echo "Cleaned."

# Phony targets
.PHONY: all clean cardinal kmazarin priest test host-tools
