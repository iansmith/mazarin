# Makefile for cardinal - Go Native Toolchain Build
# Uses Go's internal linker to build bare-metal ARM64 kernel

# Set default target
.DEFAULT_GOAL := all

# Go compiler and tools
# Default to 'go' from PATH, can be overridden: make GO=/path/to/go cardinal
GO ?= go
GOARCH = arm64
GOOS = linux
REQUIRED_GO_VERSION = 1.26.2

# Version check - verify Go version is exactly 1.26.2
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

# Build output directory structure
BUILD_DIR = build

# Output files
CARDINAL_BINARY = $(BUILD_DIR)/cardinal.elf
KMAZARIN_BINARY = $(BUILD_DIR)/kmazarin.elf
KMAZARIN_OVERLAY = $(BUILD_DIR)/kmazarin-overlay.json

# Generated embedded data (relative to project root)
KMAZARIN_DATA_ASM = cardinal/asm/dev/kmazarin_data_arm64.s
BOOT_IMAGE_DATA_ASM = cardinal/asm/dev/boot_mazarin_data_arm64.s

# Boot image source
BOOT_IMAGE_BIN = assets/boot-mazarin.bin

# Ensure build directory exists
$(BUILD_DIR):
	@mkdir -p $@

# =========================================
# Kmazarin Embedding
# =========================================
# Generate Go assembly with embedded kmazarin binary

$(KMAZARIN_DATA_ASM): $(KMAZARIN_BINARY)
	@echo "Generating embedded kmazarin data..."
	@GOTOOLCHAIN=local $(GO) tool incbin2goasm -sym kmazarin_binary -global $< > $@
	@echo "Generated $(KMAZARIN_DATA_ASM) ($$(wc -l < $@ | tr -d ' ') lines)"

# Generate embedded boot image data
$(BOOT_IMAGE_DATA_ASM): $(BOOT_IMAGE_BIN)
	@echo "Generating embedded boot image data..."
	@GOTOOLCHAIN=local $(GO) tool incbin2goasm -sym _binary_boot_mazarin_bin -global $< > $@
	@echo "Generated $(BOOT_IMAGE_DATA_ASM) ($$(wc -l < $@ | tr -d ' ') lines)"

# =========================================
# Cardinal Build (Go Native Toolchain)
# =========================================
# Uses Go's internal linker with -T flag to set load address
# Then patches entry point and linker symbol values

# cardinal-elf: PHONY target that always runs Go build
# Go's build cache makes this fast when nothing has changed
.PHONY: cardinal-elf
cardinal-elf: | $(BUILD_DIR)
	@echo "Building cardinal with Go native toolchain..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build \
			$(GCFLAGS) \
			-ldflags="-checklinkname=0 -T 0x40100000" \
			-o $(CARDINAL_BINARY) \
			./cardinal/main
	@echo "Patching entry point to _cardinal_boot..."
	@GOTOOLCHAIN=local $(GO) tool patch-entry $(CARDINAL_BINARY) _cardinal_boot
	@echo "Patching linker values..."
	@GOTOOLCHAIN=local $(GO) tool compute-linker-values -patch -kmazarin $(KMAZARIN_BINARY) $(CARDINAL_BINARY)
	@echo "Fixing ELF for QEMU compatibility..."
	@GOTOOLCHAIN=local $(GO) tool fix-go-elf $(CARDINAL_BINARY)
	@echo "cardinal ready at $(CARDINAL_BINARY)"

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

# Runtime patch files for overlay
RUNTIME_PATCH_CGO_MMAP = $(RUNTIME_PATCHES_DIR)/cgo_mmap.go
RUNTIME_PATCH_MALLOC = $(RUNTIME_PATCHES_DIR)/malloc.go
RUNTIME_PATCH_MCACHE = $(RUNTIME_PATCHES_DIR)/mcache.go
RUNTIME_PATCH_OS_LINUX_ARM64 = $(RUNTIME_PATCHES_DIR)/os_linux_arm64.go
RUNTIME_PATCH_PREEMPT = $(RUNTIME_PATCHES_DIR)/preempt.go
RUNTIME_PATCH_TAGPTR = $(RUNTIME_PATCHES_DIR)/tagptr_64bit.go
RUNTIME_PATCH_SYSCALL = $(RUNTIME_PATCHES_DIR)/syscall/syscall_linux.go

# Generate overlay JSON for kmazarin runtime patches
# This allows using vanilla Go with our runtime patches:
#   - cgo_mmap.go: route runtime mmap/munmap through dispatcher (no SVC)
#   - malloc.go: high-memory heap support (arenaBaseOffset for TTBR1 space)
#   - mcache.go: accept stale cached spans in refill (sweepgen+1 in addition to sweepgen+3)
#   - os_linux_arm64.go: parse custom auxv entries for heap bounds
#   - preempt.go: expose preemption offsets for kernel IRQ handling (added GetPreemptOffsets)
#   - tagptr_64bit.go: use sign extension for arm64 to support TTBR1 addresses
#   - syscall_linux.go: RawSyscall6 calls dispatcher directly (no SVC, pure Go)
# GOTOOLCHAIN=local ensures we get the actual GOROOT, not a downloaded toolchain
$(KMAZARIN_OVERLAY): $(RUNTIME_PATCH_CGO_MMAP) $(RUNTIME_PATCH_MALLOC) $(RUNTIME_PATCH_MCACHE) $(RUNTIME_PATCH_OS_LINUX_ARM64) $(RUNTIME_PATCH_PREEMPT) $(RUNTIME_PATCH_TAGPTR) $(RUNTIME_PATCH_SYSCALL) | $(BUILD_DIR)
	@echo "Generating runtime overlay for kmazarin..."
	@GOROOT=$$(GOTOOLCHAIN=local $(GO) env GOROOT) && \
		echo "{\"Replace\":{\
\"$$GOROOT/src/runtime/cgo_mmap.go\":\"$(abspath $(RUNTIME_PATCH_CGO_MMAP))\",\
\"$$GOROOT/src/runtime/malloc.go\":\"$(abspath $(RUNTIME_PATCH_MALLOC))\",\
\"$$GOROOT/src/runtime/mcache.go\":\"$(abspath $(RUNTIME_PATCH_MCACHE))\",\
\"$$GOROOT/src/runtime/os_linux_arm64.go\":\"$(abspath $(RUNTIME_PATCH_OS_LINUX_ARM64))\",\
\"$$GOROOT/src/runtime/preempt.go\":\"$(abspath $(RUNTIME_PATCH_PREEMPT))\",\
\"$$GOROOT/src/runtime/tagptr_64bit.go\":\"$(abspath $(RUNTIME_PATCH_TAGPTR))\",\
\"$$GOROOT/src/syscall/syscall_linux.go\":\"$(abspath $(RUNTIME_PATCH_SYSCALL))\"\
}}" > $(KMAZARIN_OVERLAY)
	@echo "  Overlay: $(KMAZARIN_OVERLAY)"

# kmazarin-elf: PHONY target that always runs Go build
# Go's build cache makes this fast when nothing has changed
.PHONY: kmazarin-elf
kmazarin-elf: $(KMAZARIN_OVERLAY) | $(BUILD_DIR)
	$(eval KMAZARIN_LOAD_ADDR := $(shell GOTOOLCHAIN=local $(GO) tool print-kmazarin-addr))
	@echo "Building kmazarin kernel (static Go binary at $(KMAZARIN_LOAD_ADDR))..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(KMAZARIN_OVERLAY)) $(GCFLAGS) \
			-ldflags="-checklinkname=0 -T $(KMAZARIN_LOAD_ADDR)" \
			-o $(KMAZARIN_BINARY) \
			./kmazarin/kmazarin
	@echo "Fixing kmazarin ELF for QEMU compatibility..."
	@GOTOOLCHAIN=local $(GO) tool fix-go-elf $(KMAZARIN_BINARY)
	@echo "Relocating kmazarin to high memory (0xFFFFFFFF41800000)..."
	@GOTOOLCHAIN=local $(GO) tool relocate-kmazarin $(KMAZARIN_BINARY) $(KMAZARIN_BINARY).tmp
	@mv $(KMAZARIN_BINARY).tmp $(KMAZARIN_BINARY)
	@echo "Kmazarin kernel built and relocated at $(KMAZARIN_BINARY)"

# =========================================
# Main Targets
# =========================================
# All ELF targets are PHONY - they ALWAYS run Go build.
# Go's build cache handles dependency tracking far better than Make can.
# This ensures binaries are always up-to-date without complex Make dependencies.

# Build cardinal (includes Go version check)
# Depends on kmazarin-elf being built first (for embedding)
cardinal: check-go-version kmazarin-elf $(KMAZARIN_DATA_ASM) $(BOOT_IMAGE_DATA_ASM) cardinal-elf

# Build kmazarin (includes Go version check)
kmazarin: check-go-version kmazarin-elf

# =========================================
# Flock Userspace Programs
# =========================================
# Priest: uses real SVC syscalls (no overlay)
# Normal programs: use userspace overlay (routes syscalls to priest)

# Userspace overlay - routes syscalls through interceptable function pointer
# This overlay is used by BOTH priest and userspace programs:
# - Priest: uses defaultSyscallHandler (real SVC) during bootstrap
# - Userspace programs: PriestSyscallEntry gets patched to point to priest
USERSPACE_OVERLAY = $(BUILD_DIR)/userspace-overlay.json
USERSPACE_OVERLAY_DIR = mazarin/overlay/userspace
USERSPACE_PATCH_SYSCALL = $(USERSPACE_OVERLAY_DIR)/syscall_linux.go
USERSPACE_PATCH_SYSCALL_ASM = $(USERSPACE_OVERLAY_DIR)/asm_linux_arm64.s
USERSPACE_PATCH_RUNTIME_MMAP = $(USERSPACE_OVERLAY_DIR)/runtime/cgo_mmap.go
USERSPACE_PATCH_LOCK_SPINBIT = $(USERSPACE_OVERLAY_DIR)/runtime/lock_spinbit.go

# Generate overlay JSON for userspace programs
# Overrides:
# - syscall/syscall_linux.go - routes syscalls through PriestSyscallEntry
# - syscall/asm_linux_arm64.s - provides defaultSyscallHandler (real SVC)
# - runtime/cgo_mmap.go - routes runtime mmap through syscall package
# - runtime/lock_spinbit.go - adds diagnostic prints to lockVerifyMSize
$(USERSPACE_OVERLAY): $(USERSPACE_PATCH_SYSCALL) $(USERSPACE_PATCH_SYSCALL_ASM) $(USERSPACE_PATCH_RUNTIME_MMAP) $(USERSPACE_PATCH_LOCK_SPINBIT) | $(BUILD_DIR)
	@echo "Generating runtime overlay for userspace..."
	@GOROOT=$$(GOTOOLCHAIN=local $(GO) env GOROOT) && \
		echo "{\"Replace\":{\
\"$$GOROOT/src/syscall/syscall_linux.go\":\"$(abspath $(USERSPACE_PATCH_SYSCALL))\",\
\"$$GOROOT/src/syscall/asm_linux_arm64.s\":\"$(abspath $(USERSPACE_PATCH_SYSCALL_ASM))\",\
\"$$GOROOT/src/runtime/cgo_mmap.go\":\"$(abspath $(USERSPACE_PATCH_RUNTIME_MMAP))\",\
\"$$GOROOT/src/runtime/lock_spinbit.go\":\"$(abspath $(USERSPACE_PATCH_LOCK_SPINBIT))\"\
}}" > $(USERSPACE_OVERLAY)
	@echo "  Overlay: $(USERSPACE_OVERLAY)"

# Priest - syscall router (uses overlay with defaultSyscallHandler for real SVC)
# The overlay routes runtime mmap through syscall package, allowing interception.
# PriestSyscallEntry defaults to defaultSyscallHandler which does real SVC.
PRIEST_BINARY = $(BUILD_DIR)/priest.elf

# priest-elf: PHONY target that always runs Go build
# Go's build cache makes this fast when nothing has changed
.PHONY: priest-elf
priest-elf: $(USERSPACE_OVERLAY) | $(BUILD_DIR)
	@echo "Building priest (syscall router with overlay)..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(USERSPACE_OVERLAY)) $(GCFLAGS) \
			-o $(PRIEST_BINARY) \
			./maz/priest
	@echo "Priest built at $(PRIEST_BINARY)"

# Helloworld - test program (uses userspace overlay for syscall routing)
# Note: Thin client support (reduced binary size) will be added via AST-based stubs
HELLOWORLD_BINARY = $(BUILD_DIR)/helloworld.elf

# helloworld-elf: PHONY target that always runs Go build
# Go's build cache makes this fast when nothing has changed
.PHONY: helloworld-elf
helloworld-elf: $(USERSPACE_OVERLAY) | $(BUILD_DIR)
	@echo "Building helloworld (userspace program)..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(USERSPACE_OVERLAY)) $(GCFLAGS) \
			-o $(HELLOWORLD_BINARY) \
			./maz/helloworld
	@echo "Helloworld built at $(HELLOWORLD_BINARY) ($$(ls -lh $(HELLOWORLD_BINARY) | awk '{print $$5}'))"

# Build priest (includes Go version check)
priest: check-go-version priest-elf

# Priest2 - goroutine scheduling test program (prints 1s and 2s)
# Similar to priest but just for testing scheduling between userspace programs
PRIEST2_BINARY = $(BUILD_DIR)/priest2.elf

# priest2-elf: PHONY target that always runs Go build
# Go's build cache makes this fast when nothing has changed
.PHONY: priest2-elf
priest2-elf: $(USERSPACE_OVERLAY) | $(BUILD_DIR)
	@echo "Building priest2 (scheduling test program)..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(USERSPACE_OVERLAY)) $(GCFLAGS) \
			-o $(PRIEST2_BINARY) \
			./maz/priest2
	@echo "Priest2 built at $(PRIEST2_BINARY) ($$(ls -lh $(PRIEST2_BINARY) | awk '{print $$5}'))"

# Build priest2 (includes Go version check)
priest2: check-go-version priest2-elf

# Build helloworld (includes Go version check)
helloworld: check-go-version priest-elf helloworld-elf

# =========================================
# Thin Client Build (AST-based stubs)
# =========================================
# Thin clients have minimal runtime stubs that will be patched
# to trampoline to priest's full runtime implementation.
# This dramatically reduces binary size (~100KB vs ~2MB).

THIN_OVERLAY_DIR = $(BUILD_DIR)/thin-overlay/runtime
THIN_OVERLAY_JSON = $(BUILD_DIR)/thin-overlay.json
THIN_MANIFEST = $(BUILD_DIR)/thin-stubs.manifest

# Generate thin overlay from Go runtime source files
# Parses all runtime/*.go files and replaces function bodies with `for {}`
$(THIN_OVERLAY_JSON): | $(BUILD_DIR)
	@echo "Generating thin client overlay..."
	@GOROOT=$$(GOTOOLCHAIN=local $(GO) env GOROOT) && \
		GOTOOLCHAIN=local $(GO) tool gen-ast-stubs \
			-runtime=$$GOROOT/src/runtime \
			-output=$(THIN_OVERLAY_DIR) \
			-overlay=$(THIN_OVERLAY_JSON) \
			-manifest=$(THIN_MANIFEST)
	@echo "  Overlay: $(THIN_OVERLAY_JSON)"
	@echo "  Manifest: $(THIN_MANIFEST)"

# Helloworld thin - uses thin overlay for minimal binary size
HELLOWORLD_THIN_BINARY = $(BUILD_DIR)/helloworld-thin.elf

# helloworld-thin-elf: PHONY target that always runs Go build
.PHONY: helloworld-thin-elf
helloworld-thin-elf: $(THIN_OVERLAY_JSON) | $(BUILD_DIR)
	@echo "Building helloworld-thin (minimal runtime stubs)..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(THIN_OVERLAY_JSON)) $(GCFLAGS) \
			-o $(HELLOWORLD_THIN_BINARY) \
			./maz/helloworld
	@echo "Helloworld-thin built at $(HELLOWORLD_THIN_BINARY) ($$(ls -lh $(HELLOWORLD_THIN_BINARY) | awk '{print $$5}'))"

# Build helloworld-thin (includes Go version check)
helloworld-thin: check-go-version priest-elf helloworld-thin-elf

# Generate thin overlay only (for testing/debugging)
thin-overlay: check-go-version $(THIN_OVERLAY_JSON)

# =========================================
# .maz Programs (Direct SVC, no overlay)
# =========================================
# .maz programs make direct SVC syscalls to the kernel.
# These do NOT use priest or the userspace overlay - they're standalone.
# Use this for testing userspace syscall handling without overlay complexity.

HELLOWORLD_MAZ_BINARY = $(BUILD_DIR)/helloworld.maz

# helloworld-maz-elf: PHONY target that always runs Go build
.PHONY: helloworld-maz-elf
helloworld-maz-elf: | $(BUILD_DIR)
	@echo "Building helloworld.maz (direct SVC, no overlay)..."
	@CGO_ENABLED=0 \
		GOTOOLCHAIN=local \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build $(GCFLAGS) \
			-o $(HELLOWORLD_MAZ_BINARY) \
			./maz/helloworld
	@echo "Helloworld.maz built at $(HELLOWORLD_MAZ_BINARY) ($$(ls -lh $(HELLOWORLD_MAZ_BINARY) | awk '{print $$5}'))"

# Build helloworld.maz (includes Go version check)
helloworld-maz: check-go-version helloworld-maz-elf

# =========================================
# Disk Image for VirtIO Block
# =========================================
# FAT32 disk image containing maz binaries for kmazarin to load
# Uses PHONY ELF targets to ensure binaries are always rebuilt first

DISK_IMAGE = $(BUILD_DIR)/disk.img

# disk-image: PHONY target that always rebuilds the disk image after ELF builds
.PHONY: disk-image
disk-image: priest-elf priest2-elf helloworld-maz-elf | $(BUILD_DIR)
	@echo "Creating FAT32 disk image..."
	@GOTOOLCHAIN=local $(GO) tool mkfat32 -o $(DISK_IMAGE) $(PRIEST_BINARY) $(PRIEST2_BINARY) $(HELLOWORLD_MAZ_BINARY)
	@echo "Disk image created at $(DISK_IMAGE) ($$(ls -lh $(DISK_IMAGE) | awk '{print $$5}'))"

# Build disk image (includes maz programs)
disk: check-go-version disk-image

# Default: build both
all: cardinal kmazarin

# Test target - run Go tests
test: check-go-version
	@echo "Running tests..."
	@GOTOOLCHAIN=local $(GO) test -v ./cardinal/bitfield

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f $(KMAZARIN_DATA_ASM)
	rm -f $(BOOT_IMAGE_DATA_ASM)
	@echo "Cleaned."

# Phony targets
# All *-elf targets are PHONY to ensure Go build always runs (Go handles caching)
.PHONY: all clean cardinal kmazarin priest priest2 helloworld helloworld-thin helloworld-maz thin-overlay disk test
.PHONY: cardinal-elf kmazarin-elf priest-elf priest2-elf helloworld-elf helloworld-thin-elf helloworld-maz-elf disk-image
