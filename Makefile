# Makefile for mazboot - assumes we are in project root directory

# Set default target
.DEFAULT_GOAL := all

# Cross-compiler path
CC = /Users/iansmith/mazzy/bin/target-gcc

# Go compiler and tools
# Use ~/mazzy/bin/go as bootstrap, which will use the toolchain from go.mod
GO = /Users/iansmith/mazzy/bin/go
GOARCH = arm64
GOOS = linux

# IMPORTANT: CGO Policy
# We NEVER use CGO in this project. All Go builds must explicitly set CGO_ENABLED=0
# to ensure static binaries without C dependencies for our bare-metal environment.

# Runtime patching tool (Go version that scans .s files)
PATCH_RUNTIME = src/mazboot/tools/patch-runtime.go

# Source directory
MAZBOOT_SRC = src/mazboot

# Source files - Assembly in asm/aarch64/ directory (relative to src/mazboot)
BOOT_SRC = $(MAZBOOT_SRC)/asm/aarch64/boot.s
LIB_SRC = $(MAZBOOT_SRC)/asm/aarch64/lib.s
WRITEBARRIER_SRC = $(MAZBOOT_SRC)/asm/aarch64/writebarrier.s
EXCEPTIONS_SRC = $(MAZBOOT_SRC)/asm/aarch64/exceptions.s
IMAGE_SRC = $(MAZBOOT_SRC)/asm/aarch64/image.s
GOROUTINE_SRC = $(MAZBOOT_SRC)/asm/aarch64/goroutine.s
LINKER_SYMBOLS_SRC = $(MAZBOOT_SRC)/asm/aarch64/linker_symbols.s
KMAZARIN_EMBED_SRC = $(MAZBOOT_SRC)/asm/aarch64/kmazarin_embed.s
LINKER_SCRIPT = $(MAZBOOT_SRC)/linker.ld

# Asset generation tools and sources
IMAGECONVERT_TOOL = tools/imageconvert/main.go
IMAGECONVERT_GO_MOD = tools/imageconvert/go.mod
BOOT_IMAGE_BIN = assets/boot-mazarin.bin
BOOT_IMAGE_SOURCES = assets/mazarin-original.png assets/mazarin50.png

# Go package location (new golang layout)
GO_PACKAGE_DIR = $(MAZBOOT_SRC)/golang/main
ASM_PACKAGE_DIR = $(MAZBOOT_SRC)/golang/asm

# Go source files (all files in golang/main - build tags determine which are included)
# IMPORTANT: Use wildcard so that adding new Go files (e.g., pci_ecam_base_qemu.go, dtb_qemu.go)
# automatically triggers a rebuild of the Go object when they change.
GO_SRC = $(wildcard $(GO_PACKAGE_DIR)/*.go)

# Build output directory structure
BUILD_DIR = build
BUILD_TOOLS_DIR = $(BUILD_DIR)/tools
BUILD_MAZBOOT_DIR = $(BUILD_DIR)/mazboot
BUILD_KMAZARIN_DIR = $(BUILD_DIR)/kmazarin

# Code generation tools and outputs
GLOBALIZE_SYMBOLS_GEN_SRC = $(MAZBOOT_SRC)/tools/generate-globalize-symbols.go
GLOBALIZE_SYMBOLS_GEN = $(BUILD_TOOLS_DIR)/generate-globalize-symbols
GLOBALIZE_SYMBOLS_LIST = $(BUILD_MAZBOOT_DIR)/globalize_symbols.txt

# Generated Go files and their dependencies
LINKNAMES_GO = $(ASM_PACKAGE_DIR)/linknames.go
LINKNAMES_GEN = $(MAZBOOT_SRC)/tools/generate-linknames.go
MAIN_GO = $(GO_PACKAGE_DIR)/main.go
MAIN_GEN = $(MAZBOOT_SRC)/tools/generate-main-calls.go

# Assembly source files that generators depend on
ASM_SOURCES = $(wildcard $(MAZBOOT_SRC)/asm/aarch64/*.s)
GOASM_SOURCES = $(wildcard $(MAZBOOT_SRC)/asm/goasm/*.s)

# Object files (all in build/mazboot/)
BOOT_OBJ = $(BUILD_MAZBOOT_DIR)/boot.o
LIB_OBJ = $(BUILD_MAZBOOT_DIR)/lib.o
WRITEBARRIER_OBJ = $(BUILD_MAZBOOT_DIR)/writebarrier.o
EXCEPTIONS_OBJ = $(BUILD_MAZBOOT_DIR)/exceptions.o
IMAGE_OBJ = $(BUILD_MAZBOOT_DIR)/image.o
GOROUTINE_OBJ = $(BUILD_MAZBOOT_DIR)/goroutine.o
ASYNC_PREEMPT_OBJ = $(BUILD_MAZBOOT_DIR)/async_preempt.o
LIB_GETTERS_OBJ = $(BUILD_MAZBOOT_DIR)/lib_getters.o
LIB_MMIO_OBJ = $(BUILD_MAZBOOT_DIR)/lib_mmio.o
LIB_BARRIERS_OBJ = $(BUILD_MAZBOOT_DIR)/lib_barriers.o
GET_CALLER_SP_OBJ = $(BUILD_MAZBOOT_DIR)/get_caller_sp.o
LINKER_SYMBOLS_OBJ = $(BUILD_MAZBOOT_DIR)/linker_symbols.o
KMAZARIN_EMBED_OBJ = $(BUILD_MAZBOOT_DIR)/kmazarin_embed.o
KERNEL_GO_OBJ = $(BUILD_MAZBOOT_DIR)/kernel_go.o

# Transpiled Go assembly object (from Go Plan 9 syntax)
GOASM_TEST_SRC = $(MAZBOOT_SRC)/asm/goasm/goasm_test.s
GOASM_TEST_OBJ = $(BUILD_MAZBOOT_DIR)/goasm_test.o
GOASM2GNU_SRC = $(MAZBOOT_SRC)/tools/goasm2gnu.go
GOASM2GNU = $(BUILD_TOOLS_DIR)/goasm2gnu

# Kmazarin symbol extraction tool
EXTRACT_SYMBOLS_SRC = $(MAZBOOT_SRC)/tools/extract-kmazarin-symbols.go
EXTRACT_SYMBOLS = $(BUILD_TOOLS_DIR)/extract-kmazarin-symbols
KMAZARIN_SYMBOLS_S = $(BUILD_MAZBOOT_DIR)/kmazarin_symbols.s

# Binary to Go assembly converter (for embedding binary files in Go assembly)
INCBIN2GOASM_SRC = $(MAZBOOT_SRC)/tools/incbin2goasm.go
INCBIN2GOASM = $(BUILD_TOOLS_DIR)/incbin2goasm

# Binary to ELF converter (direct binary embedding)
BIN2ELF_SRC = $(MAZBOOT_SRC)/tools/bin2elf.go
BIN2ELF = $(BUILD_TOOLS_DIR)/bin2elf

# Go assembly sources (Plan 9 syntax, in asm/goasm/)
IMAGE_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/image.s
IMAGE_DATA_OBJ = $(BUILD_MAZBOOT_DIR)/image_data.o

KMAZARIN_EMBED_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/kmazarin_embed.s
KMAZARIN_EMBED_DATA_OBJ = $(BUILD_MAZBOOT_DIR)/kmazarin_embed_data.o

GOROUTINE_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/goroutine.s

# Assembly object files list
ASM_OBJECTS = $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(IMAGE_DATA_OBJ) $(GOROUTINE_OBJ) $(ASYNC_PREEMPT_OBJ) $(GET_CALLER_SP_OBJ) $(LINKER_SYMBOLS_OBJ) $(KMAZARIN_EMBED_OBJ) $(KMAZARIN_EMBED_DATA_OBJ) $(GOASM_TEST_OBJ)

# Output files
MAZBOOT_BINARY = $(BUILD_DIR)/mazboot.elf
QEMU_KERNEL_OUT = docker/builtin/kernel.elf

# Compiler flags
CFLAGS = -mcpu=cortex-a72 -march=armv8-a -fpic -ffreestanding -std=gnu99 -O2 -Wall -Wextra -g
ASFLAGS = -mcpu=cortex-a72 -march=armv8-a -ffreestanding -g
LDFLAGS = -T $(LINKER_SCRIPT) -ffreestanding -O2 -nostdlib -g

# Go build flags for c-archive mode with external linker
GO_GCFLAGS ?= "all=-N -l"
GO_BUILD_FLAGS = -buildmode=c-archive -gcflags $(GO_GCFLAGS)

# Object file tools
OBJCOPY = /Users/iansmith/mazzy/bin/target-objcopy

# Default target: build mazboot for QEMU
# This automatically triggers all dependencies including code generation
# Dependency chain: mazboot -> (boot.o, lib.o, exceptions.o, kernel_go_qemu.o) -> (asm sources, Go sources)
# Note: Bitfield code generation is handled by //go:generate in page_flags.go

# Ensure build directories exist
$(BUILD_DIR) $(BUILD_TOOLS_DIR) $(BUILD_MAZBOOT_DIR) $(BUILD_KMAZARIN_DIR):
	@mkdir -p $@

# Build generator tool for globalizing symbols
$(GLOBALIZE_SYMBOLS_GEN): $(GLOBALIZE_SYMBOLS_GEN_SRC) | $(BUILD_TOOLS_DIR)
	@echo "Building generate-globalize-symbols tool..."
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $(GLOBALIZE_SYMBOLS_GEN_SRC)

# Build goasm2gnu tool for transpiling Go assembly to ELF
$(GOASM2GNU): $(GOASM2GNU_SRC) | $(BUILD_TOOLS_DIR)
	@echo "Building goasm2gnu tool..."
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $(GOASM2GNU_SRC)

# Build extract-kmazarin-symbols tool for extracting symbol addresses from kmazarin.elf
$(EXTRACT_SYMBOLS): $(EXTRACT_SYMBOLS_SRC) | $(BUILD_TOOLS_DIR)
	@echo "Building extract-kmazarin-symbols tool..."
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $(EXTRACT_SYMBOLS_SRC)

# Build incbin2goasm tool for converting binary files to Go assembly
$(INCBIN2GOASM): $(INCBIN2GOASM_SRC) | $(BUILD_TOOLS_DIR)
	@echo "Building incbin2goasm tool..."
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $(INCBIN2GOASM_SRC)

# Build bin2elf tool for converting binary files directly to ELF
$(BIN2ELF): $(BIN2ELF_SRC) | $(BUILD_TOOLS_DIR)
	@echo "Building bin2elf tool..."
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $(BIN2ELF_SRC)

# Note: linknames.go and main.go are now generated via //go:generate directives
# in their respective files (asm/linknames.go and main/main.go).
# They are automatically regenerated when 'go build' is invoked during
# the KERNEL_GO_OBJ_QEMU build step.

# Generate boot image binary from PNG source
$(BOOT_IMAGE_BIN): $(BOOT_IMAGE_SOURCES) $(IMAGECONVERT_TOOL) $(IMAGECONVERT_GO_MOD)
	@echo "Generating boot image binary from PNG..."
	@cd tools/imageconvert && $(GO) run main.go ../../assets/mazarin50.png ../../assets/boot-mazarin.bin

# Compile assembly source files
# All assembly files depend on linker.ld since they may use linker symbols
$(BOOT_OBJ): $(BOOT_SRC) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(LIB_OBJ): $(LIB_SRC) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(EXCEPTIONS_OBJ): $(EXCEPTIONS_SRC) $(LINKER_SCRIPT) $(KMAZARIN_SYMBOLS_S)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(WRITEBARRIER_OBJ): $(WRITEBARRIER_SRC) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Convert image data binary directly to ELF object
$(IMAGE_DATA_OBJ): $(BOOT_IMAGE_BIN) $(BIN2ELF)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Converting image binary to ELF object: $<"
	$(BIN2ELF) -sym _binary_boot_mazarin_bin -o $@ $<

# Transpile image accessor functions Go assembly to ELF
$(IMAGE_OBJ): $(IMAGE_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling image Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Transpile goroutine Go assembly to ELF
$(GOROUTINE_OBJ): $(GOROUTINE_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling goroutine Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Transpile async_preempt Go assembly to ELF
ASYNC_PREEMPT_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/async_preempt.s
$(ASYNC_PREEMPT_OBJ): $(ASYNC_PREEMPT_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling async_preempt Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Transpile lib_getters Go assembly to ELF
LIB_GETTERS_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/lib_getters.s
$(LIB_GETTERS_OBJ): $(LIB_GETTERS_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling lib_getters Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Transpile lib_mmio Go assembly to ELF
LIB_MMIO_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/lib_mmio.s
$(LIB_MMIO_OBJ): $(LIB_MMIO_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling lib_mmio Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Transpile lib_barriers Go assembly to ELF
LIB_BARRIERS_GOASM_SRC = $(MAZBOOT_SRC)/asm/goasm/lib_barriers.s
$(LIB_BARRIERS_OBJ): $(LIB_BARRIERS_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling lib_barriers Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

GET_CALLER_SP_SRC = $(MAZBOOT_SRC)/asm/aarch64/get_caller_sp.s
$(GET_CALLER_SP_OBJ): $(GET_CALLER_SP_SRC) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(LINKER_SYMBOLS_OBJ): $(LINKER_SYMBOLS_SRC) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Transpile Go assembly (Plan 9 syntax) to ELF using goasm2gnu tool
$(GOASM_TEST_OBJ): $(GOASM_TEST_SRC) $(GOASM2GNU)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Convert kmazarin binary directly to ELF object (in .kmazarin section)
$(KMAZARIN_EMBED_DATA_OBJ): $(KMAZARIN_BINARY) $(BIN2ELF)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Converting kmazarin binary to ELF object: $(KMAZARIN_BINARY)"
	$(BIN2ELF) -sym kmazarin_binary -section .kmazarin -o $@ $(KMAZARIN_BINARY)

# Transpile kmazarin_embed accessor functions Go assembly to ELF
$(KMAZARIN_EMBED_OBJ): $(KMAZARIN_EMBED_GOASM_SRC) $(GOASM2GNU) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@echo "Transpiling kmazarin_embed Go assembly: $<"
	$(GOASM2GNU) -elf -o $@ $<

# Compile kernel Go sources from golang/main package using go build with c-archive mode
KERNEL_GO_ARCHIVE = $(BUILD_MAZBOOT_DIR)/kernel_go
KERNEL_GO_TEMP = $(BUILD_MAZBOOT_DIR)/kernel_go_temp.o

# Generate list of symbols that need globalizing (discovered from assembly files)
$(GLOBALIZE_SYMBOLS_LIST): $(GLOBALIZE_SYMBOLS_GEN) $(wildcard $(MAZBOOT_SRC)/asm/aarch64/*.s)
	@echo "Discovering symbols that need globalizing..."
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@cd $(MAZBOOT_SRC) && $(abspath $(GLOBALIZE_SYMBOLS_GEN)) -asm asm/aarch64 -o $(abspath $(GLOBALIZE_SYMBOLS_LIST))

# Generate linknames.go from assembly files (both GCC and Go/Plan9 assembly)
# This file contains //go:linkname directives to link Go functions to assembly symbols
$(LINKNAMES_GO): $(LINKNAMES_GEN) $(ASM_SOURCES) $(GOASM_SOURCES)
	@echo "Regenerating linknames.go from assembly sources..."
	@CGO_ENABLED=0 GOTOOLCHAIN=auto $(GO) run $(LINKNAMES_GEN) \
		-asm $(MAZBOOT_SRC)/asm/aarch64 \
		-goasm $(MAZBOOT_SRC)/asm/goasm \
		-o $(LINKNAMES_GO)

# Convenience target to regenerate linknames
.PHONY: generate-linknames
generate-linknames:
	@CGO_ENABLED=0 GOTOOLCHAIN=auto $(GO) run $(LINKNAMES_GEN) \
		-asm $(MAZBOOT_SRC)/asm/aarch64 \
		-goasm $(MAZBOOT_SRC)/asm/goasm \
		-o $(LINKNAMES_GO)

# Generate main.go from assembly and Go files
# This file ensures all assembly-called functions are referenced so they're not optimized away
$(MAIN_GO): $(MAIN_GEN) $(ASM_SOURCES) $(GOASM_SOURCES) $(filter-out $(MAIN_GO), $(GO_SRC))
	@echo "Regenerating main.go from assembly and Go sources..."
	@CGO_ENABLED=0 GOTOOLCHAIN=auto $(GO) run $(MAIN_GEN) \
		-asm $(MAZBOOT_SRC)/asm/aarch64 \
		-goasm $(MAZBOOT_SRC)/asm/goasm \
		-go $(GO_PACKAGE_DIR) \
		-o $(MAIN_GO)

# Phony target for manual regeneration
.PHONY: generate-main
generate-main:
	@CGO_ENABLED=0 GOTOOLCHAIN=auto $(GO) run $(MAIN_GEN) \
		-asm $(MAZBOOT_SRC)/asm/aarch64 \
		-goasm $(MAZBOOT_SRC)/asm/goasm \
		-go $(GO_PACKAGE_DIR) \
		-o $(MAIN_GO)

# QEMU build target - rebuilds Go object with qemuvirt and aarch64 tags
# NOTE: This depends on LINKNAMES_GO and MAIN_GO, which are regenerated when their sources change.
# Also depends on LINKER_SCRIPT because memory.go uses linker symbols.
KERNEL_GO_OBJ_QEMU = $(BUILD_MAZBOOT_DIR)/kernel_go_qemu.o
$(KERNEL_GO_OBJ_QEMU): $(MAZBOOT_SRC)/golang/go.mod $(GO_SRC) $(LINKNAMES_GO) $(MAIN_GO) $(GLOBALIZE_SYMBOLS_LIST) $(LINKER_SCRIPT)
	@mkdir -p $(BUILD_MAZBOOT_DIR)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) $(BUILD_MAZBOOT_DIR)/go.o $(BUILD_MAZBOOT_DIR)/kernel_go.h $(BUILD_MAZBOOT_DIR)/__.SYMDEF
	@# Run go generate for page_flags_gen.go only (linknames.go and main.go are generated via Makefile rules)
	@# Note: go generate must run on host architecture, not target
	@echo "Running go generate to regenerate code..."
	@cd $(MAZBOOT_SRC)/golang && CGO_ENABLED=0 GOTOOLCHAIN=auto $(GO) generate ./main/page_flags.go 2>/dev/null || true
	@# Build Go package from golang/main directory with required tags
	@echo "Building for QEMU with tags: qemuvirt aarch64"
	@cd $(MAZBOOT_SRC)/golang && CGO_ENABLED=0 GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "qemuvirt aarch64" $(GO_BUILD_FLAGS) -o $(abspath $(KERNEL_GO_ARCHIVE)) ./main
	@# Extract the actual object file (go.o) from the C archive
	@cd $(BUILD_MAZBOOT_DIR) && ar x $(notdir $(KERNEL_GO_ARCHIVE)) go.o
	@mv $(BUILD_MAZBOOT_DIR)/go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote main functions from local to global symbols
	@# Symbols are discovered automatically by scanning assembly files
	@echo "Globalizing symbols discovered from assembly files..."
	@echo "DEBUG: Checking if $(GLOBALIZE_SYMBOLS_LIST) exists and has content..."
	@if [ -s $(GLOBALIZE_SYMBOLS_LIST) ]; then \
		echo "DEBUG: Found $(GLOBALIZE_SYMBOLS_LIST) with content:"; \
		head -5 $(GLOBALIZE_SYMBOLS_LIST); \
		echo "DEBUG: Building objcopy command..."; \
		SYMBOLS=$$(cat $(GLOBALIZE_SYMBOLS_LIST) | sed 's/^/--globalize-symbol=/' | tr '\n' ' ' | sed 's/[[:space:]]*$$//'); \
		echo "DEBUG: objcopy command will be: $(OBJCOPY) $$SYMBOLS $(KERNEL_GO_TEMP) $@"; \
		echo "DEBUG: Checking symbols in $(KERNEL_GO_TEMP) before objcopy:"; \
		target-nm $(KERNEL_GO_TEMP) | grep -E "(main\.UartTransmitHandler|main\.TimerHandler)" | head -3 || echo "  (symbols not found)"; \
		$(OBJCOPY) $$SYMBOLS $(KERNEL_GO_TEMP) $@ || \
		 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbols"); \
		echo "DEBUG: Checking symbols in $@ after objcopy:"; \
		target-nm $@ | grep -E "(main\.UartTransmitHandler|main\.TimerHandler)" | head -3 || echo "  (symbols not found)"; \
	else \
		echo "Warning: No symbols to globalize found (file empty or missing)"; \
		cp $(KERNEL_GO_TEMP) $@; \
	fi
	@# Weaken Go runtime's write barrier symbols so our strong global versions override them
	@# This allows our writebarrier.s implementations to be used instead
	@echo "Weakening Go runtime write barrier symbols..."
	@$(OBJCOPY) --weaken-symbol=runtime.gcWriteBarrier2 \
	             --weaken-symbol=runtime.gcWriteBarrier3 \
	             --weaken-symbol=runtime.gcWriteBarrier4 \
	             --weaken-symbol=gcWriteBarrier \
	             $@ $@.tmp && mv $@.tmp $@ || \
	 (echo "Warning: Could not weaken write barrier symbols")
	@rm -f $(KERNEL_GO_ARCHIVE) $(BUILD_MAZBOOT_DIR)/kernel_go.h $(BUILD_MAZBOOT_DIR)/__.SYMDEF

# Build mazboot (default: QEMU build with qemuvirt and aarch64 tags)
# NOTE: Depends on KMAZARIN_EMBED_OBJ and KMAZARIN_EMBED_DATA_OBJ which embed the kmazarin kernel binary
$(MAZBOOT_BINARY): $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(IMAGE_DATA_OBJ) $(GOROUTINE_OBJ) $(ASYNC_PREEMPT_OBJ) $(LIB_GETTERS_OBJ) $(LIB_MMIO_OBJ) $(LIB_BARRIERS_OBJ) $(GET_CALLER_SP_OBJ) $(LINKER_SYMBOLS_OBJ) $(KMAZARIN_EMBED_OBJ) $(KMAZARIN_EMBED_DATA_OBJ) $(GOASM_TEST_OBJ) $(KERNEL_GO_OBJ_QEMU) $(LINKER_SCRIPT) $(PATCH_RUNTIME)
	@mkdir -p $(BUILD_DIR)
	@# Link exceptions.o, then writebarrier.o so our global symbols override Go runtime's
	@# Our writebarrier.s provides global (T) symbols that should take precedence
	@# GOASM_TEST_OBJ contains transpiled Go assembly (from Plan 9 syntax)
	$(CC) $(LDFLAGS) -o $@.tmp $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(KERNEL_GO_OBJ_QEMU) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(IMAGE_DATA_OBJ) $(GOROUTINE_OBJ) $(ASYNC_PREEMPT_OBJ) $(LIB_GETTERS_OBJ) $(LIB_MMIO_OBJ) $(LIB_BARRIERS_OBJ) $(GET_CALLER_SP_OBJ) $(LINKER_SYMBOLS_OBJ) $(KMAZARIN_EMBED_OBJ) $(KMAZARIN_EMBED_DATA_OBJ) $(GOASM_TEST_OBJ)
	@# Patch the binary to redirect calls from Go runtime functions to our implementations
	@# The Go tool scans .s files to determine which symbols need patching
	@echo "Patching runtime function calls..."
	@GOTOOLCHAIN=local $(GO) run $(PATCH_RUNTIME) $@.tmp $(MAZBOOT_SRC)/asm/aarch64 && mv $@.tmp $@ || \
	 (echo "Warning: Could not patch binary, using unpatched version" && mv $@.tmp $@)

# Push mazboot to docker/builtin directory
push: $(MAZBOOT_BINARY)
	@mkdir -p docker/builtin
	cp $(MAZBOOT_BINARY) docker/builtin/kernel.elf

# Build mazboot: compile binary
mazboot: $(MAZBOOT_BINARY)
	@echo "mazboot ready at $(MAZBOOT_BINARY)"

# Test target - run Go tests
test:
	@echo "Running tests..."
	@cd $(MAZBOOT_SRC)/golang && GOTOOLCHAIN=auto $(GO) test -v ./bitfield

# Clean build artifacts and generated files
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	@echo "Removing generated files (linknames.go and main.go)..."
	@rm -f $(LINKNAMES_GO) $(MAIN_GO)
	@echo "Cleaned. Generated files will be regenerated on next build."

# Check if auto-generated files exist
.PHONY: check-generated
check-generated:
	@echo "Checking generated files..."
	@if [ -f $(LINKNAMES_GO) ]; then \
		echo "✓ linknames.go: EXISTS ($$(wc -l < $(LINKNAMES_GO)) lines)"; \
	else \
		echo "✗ linknames.go: MISSING"; \
	fi
	@if [ -f $(MAIN_GO) ]; then \
		echo "✓ main.go: EXISTS ($$(wc -l < $(MAIN_GO)) lines)"; \
	else \
		echo "✗ main.go: MISSING"; \
	fi

# =========================================
# Kmazarin Kernel Build Configuration
# =========================================

# Kmazarin source directory
KMAZARIN_SRC = src/kmazarin/golang/kmazarin

# Kmazarin binary output
KMAZARIN_BINARY = $(BUILD_KMAZARIN_DIR)/kmazarin.elf

# Build kmazarin kernel as a static binary using Go's internal linker
# The load address is extracted from src/mazboot/linker.ld using kmazarin-entry.sh
# This ensures the Makefile and linker script stay in sync automatically.
#   - DTB:            0x40000000-0x40100000 (1MB, QEMU fixed)
#   - Mazboot + heap: 0x40100000-0x41000000 (15MB allocated)
#   - Kmazarin:       0x41000000-...        (loaded by ELF loader, Span 2)
#   - Bump region:    Starts after kmazarin, 2GB for mmap/brk (Span 3)
# The ELF loader in mazboot will load segments at their specified virtual addresses
$(KMAZARIN_BINARY): $(wildcard $(KMAZARIN_SRC)/*.go) src/mazboot/linker.ld tools/kmazarin-entry.sh
	@mkdir -p $(BUILD_KMAZARIN_DIR)
	$(eval KMAZARIN_LOAD_ADDR := $(shell ./tools/kmazarin-entry.sh))
	@echo "Building kmazarin kernel (static Go binary at $(KMAZARIN_LOAD_ADDR))..."
	@cd $(KMAZARIN_SRC) && \
		CGO_ENABLED=0 \
		GOTOOLCHAIN=auto \
		GOARCH=$(GOARCH) \
		GOOS=$(GOOS) \
		$(GO) build -ldflags="-T $(KMAZARIN_LOAD_ADDR)" -o $(abspath $(KMAZARIN_BINARY)) .
	@echo "Kmazarin kernel built at $(KMAZARIN_BINARY)"

# Extract symbol addresses from kmazarin.elf and generate assembly constants file
# This MUST run after kmazarin.elf is built and BEFORE mazboot assembly files are compiled
$(KMAZARIN_SYMBOLS_S): $(KMAZARIN_BINARY) $(EXTRACT_SYMBOLS)
	@echo "Extracting symbols from kmazarin.elf..."
	$(EXTRACT_SYMBOLS) $(KMAZARIN_BINARY) $@

# Build target for kmazarin
kmazarin: $(KMAZARIN_BINARY)
	@echo "kmazarin ready at $(KMAZARIN_BINARY)"

# Phony targets
.PHONY: all clean push mazboot kmazarin test regenerate-assets isvirgin

# Default target: build both mazboot and mazarin
all: mazboot kmazarin

# Regenerate binary assets from source images/fonts
regenerate-assets: $(BOOT_IMAGE_BIN)
	@echo "Assets regenerated successfully"


