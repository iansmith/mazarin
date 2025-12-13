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
LINKER_SCRIPT = $(MAZBOOT_SRC)/linker.ld

# Asset generation tools and sources
IMAGECONVERT_TOOL = tools/imageconvert/main.go
IMAGECONVERT_GO_MOD = tools/imageconvert/go.mod
BOOT_IMAGE_BIN = assets/boot-mazarin.bin
BOOT_IMAGE_SOURCES = assets/mazarin-original.png assets/mazarin50.png

# Go package location (new golang layout)
GO_PACKAGE_DIR = $(MAZBOOT_SRC)/golang/main
ASM_PACKAGE_DIR = $(MAZBOOT_SRC)/golang/asm

# Go source files (all files - build tags determine which are included)
GO_SRC = $(GO_PACKAGE_DIR)/kernel.go \
         $(GO_PACKAGE_DIR)/page.go \
         $(GO_PACKAGE_DIR)/page_flags.go \
         $(GO_PACKAGE_DIR)/heap.go \
         $(GO_PACKAGE_DIR)/gpu.go \
         $(GO_PACKAGE_DIR)/mailbox.go \
         $(GO_PACKAGE_DIR)/memory.go \
         $(GO_PACKAGE_DIR)/framebuffer_common.go \
         $(GO_PACKAGE_DIR)/framebuffer_rpi.go \
         $(GO_PACKAGE_DIR)/framebuffer_qemu.go \
         $(GO_PACKAGE_DIR)/framebuffer_text.go \
         $(GO_PACKAGE_DIR)/colors.go \
         $(GO_PACKAGE_DIR)/framebuffer_font.go \
         $(GO_PACKAGE_DIR)/pci_qemu.go \
         $(GO_PACKAGE_DIR)/gic_qemu.go \
         $(GO_PACKAGE_DIR)/timer_qemu.go \
         $(GO_PACKAGE_DIR)/ramfb_qemu.go \
         $(GO_PACKAGE_DIR)/runtime_stub.go \
         $(GO_PACKAGE_DIR)/uart_rpi.go \
         $(GO_PACKAGE_DIR)/uart_qemu.go \
         $(GO_PACKAGE_DIR)/goroutine.go \
         $(GO_PACKAGE_DIR)/runtime_types.go \
         $(GO_PACKAGE_DIR)/stack_growth.go

# Code generation tools and outputs
GLOBALIZE_SYMBOLS_GEN_SRC = $(MAZBOOT_SRC)/tools/generate-globalize-symbols.go
GLOBALIZE_SYMBOLS_GEN = $(BUILD_DIR)/generate-globalize-symbols
GLOBALIZE_SYMBOLS_LIST = $(BUILD_DIR)/globalize_symbols.txt

# Note: linknames.go and main.go are generated via //go:generate in their respective files.
# They are automatically regenerated when 'go build' is invoked.

# Build output directory
BUILD_DIR = build/mazboot

# Object files (all in build/mazboot/)
BOOT_OBJ = $(BUILD_DIR)/boot.o
LIB_OBJ = $(BUILD_DIR)/lib.o
WRITEBARRIER_OBJ = $(BUILD_DIR)/writebarrier.o
EXCEPTIONS_OBJ = $(BUILD_DIR)/exceptions.o
IMAGE_OBJ = $(BUILD_DIR)/image.o
GOROUTINE_OBJ = $(BUILD_DIR)/goroutine.o
GET_CALLER_SP_OBJ = $(BUILD_DIR)/get_caller_sp.o
KERNEL_GO_OBJ = $(BUILD_DIR)/kernel_go.o

# Assembly object files list
ASM_OBJECTS = $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(GOROUTINE_OBJ) $(GET_CALLER_SP_OBJ)

# Output file
MAZBOOT_BINARY = $(BUILD_DIR)/mazboot.elf
FLASH_DIR = flash
FLASH_BINARY = $(FLASH_DIR)/mazboot.elf
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

# Build generator tool for globalizing symbols
$(GLOBALIZE_SYMBOLS_GEN): $(GLOBALIZE_SYMBOLS_GEN_SRC)
	@echo "Building generate-globalize-symbols tool..."
	@mkdir -p $(BUILD_DIR)
	@GOTOOLCHAIN=local $(GO) build -o $@ $(GLOBALIZE_SYMBOLS_GEN_SRC)

# Note: linknames.go and main.go are now generated via //go:generate directives
# in their respective files (asm/linknames.go and main/main.go).
# They are automatically regenerated when 'go build' is invoked during
# the KERNEL_GO_OBJ_QEMU build step.

# Generate boot image binary from PNG source
$(BOOT_IMAGE_BIN): $(BOOT_IMAGE_SOURCES) $(IMAGECONVERT_TOOL) $(IMAGECONVERT_GO_MOD)
	@echo "Generating boot image binary from PNG..."
	@cd tools/imageconvert && $(GO) run main.go ../../assets/mazarin50.png ../../assets/boot-mazarin.bin

# Compile assembly source files
$(BOOT_OBJ): $(BOOT_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(LIB_OBJ): $(LIB_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(EXCEPTIONS_OBJ): $(EXCEPTIONS_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(WRITEBARRIER_OBJ): $(WRITEBARRIER_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(IMAGE_OBJ): $(IMAGE_SRC) $(BOOT_IMAGE_BIN)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

$(GOROUTINE_OBJ): $(GOROUTINE_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

GET_CALLER_SP_SRC = $(MAZBOOT_SRC)/asm/aarch64/get_caller_sp.s
$(GET_CALLER_SP_OBJ): $(GET_CALLER_SP_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile kernel Go sources from golang/main package using go build with c-archive mode
KERNEL_GO_ARCHIVE = $(BUILD_DIR)/kernel_go
KERNEL_GO_TEMP = $(BUILD_DIR)/kernel_go_temp.o

# Generate list of symbols that need globalizing (discovered from assembly files)
$(GLOBALIZE_SYMBOLS_LIST): $(GLOBALIZE_SYMBOLS_GEN) $(wildcard $(MAZBOOT_SRC)/asm/aarch64/*.s)
	@echo "Discovering symbols that need globalizing..."
	@mkdir -p $(BUILD_DIR)
	@cd $(MAZBOOT_SRC) && $(abspath $(GLOBALIZE_SYMBOLS_GEN)) -asm asm/aarch64 -o $(abspath $(GLOBALIZE_SYMBOLS_LIST))

# QEMU build target - rebuilds Go object with qemuvirt and aarch64 tags
# NOTE: This depends on GO_SRC, which includes $(LINKNAMES_GO) and $(MAIN_GO).
# Make will automatically generate these files from assembly sources BEFORE building Go code.
KERNEL_GO_OBJ_QEMU = $(BUILD_DIR)/kernel_go_qemu.o
$(KERNEL_GO_OBJ_QEMU): $(MAZBOOT_SRC)/golang/go.mod $(GO_SRC) $(GLOBALIZE_SYMBOLS_LIST)
	@mkdir -p $(BUILD_DIR)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) $(BUILD_DIR)/go.o $(BUILD_DIR)/kernel_go.h $(BUILD_DIR)/__.SYMDEF
	@# Build Go package from golang/main directory with required tags
	@echo "Building for QEMU with tags: qemuvirt aarch64"
	@cd $(MAZBOOT_SRC)/golang && CGO_ENABLED=0 GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "qemuvirt aarch64" $(GO_BUILD_FLAGS) -o $(abspath $(KERNEL_GO_ARCHIVE)) ./main
	@# Extract the actual object file (go.o) from the C archive
	@cd $(BUILD_DIR) && ar x $(notdir $(KERNEL_GO_ARCHIVE)) go.o
	@mv $(BUILD_DIR)/go.o $(KERNEL_GO_TEMP)
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
	@rm -f $(KERNEL_GO_ARCHIVE) $(BUILD_DIR)/kernel_go.h $(BUILD_DIR)/__.SYMDEF

# Build mazboot (default: QEMU build with qemuvirt and aarch64 tags)
$(MAZBOOT_BINARY): $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(GOROUTINE_OBJ) $(GET_CALLER_SP_OBJ) $(KERNEL_GO_OBJ_QEMU) $(LINKER_SCRIPT) $(PATCH_RUNTIME)
	@mkdir -p $(BUILD_DIR)
	@# Link exceptions.o, then writebarrier.o so our global symbols override Go runtime's
	@# Our writebarrier.s provides global (T) symbols that should take precedence
	$(CC) $(LDFLAGS) -o $@.tmp $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(KERNEL_GO_OBJ_QEMU) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(GOROUTINE_OBJ) $(GET_CALLER_SP_OBJ)
	@# Patch the binary to redirect calls from Go runtime functions to our implementations
	@# The Go tool scans .s files to determine which symbols need patching
	@echo "Patching runtime function calls..."
	@GOTOOLCHAIN=local $(GO) run $(PATCH_RUNTIME) $@.tmp $(MAZBOOT_SRC)/asm/aarch64 && mv $@.tmp $@ || \
	 (echo "Warning: Could not patch binary, using unpatched version" && mv $@.tmp $@)

# Push mazboot to docker/builtin directory
push: $(MAZBOOT_BINARY)
	@mkdir -p docker/builtin
	cp $(MAZBOOT_BINARY) docker/builtin/kernel.elf

# Build mazboot: compile binary and copy to flash directory
# This target builds mazboot.elf and copies it to flash/mazboot.elf
mazboot: $(FLASH_BINARY)
	@echo "mazboot ready at $(FLASH_BINARY)"

# Rule to build mazboot.elf and copy to flash directory
$(FLASH_BINARY): $(MAZBOOT_BINARY)
	@mkdir -p $(FLASH_DIR)
	cp $< $@
	@echo "Copied mazboot.elf to flash directory"

# Test target - run Go tests
test:
	@echo "Running tests..."
	@cd $(MAZBOOT_SRC)/golang && GOTOOLCHAIN=auto $(GO) test -v ./bitfield

# Clean build artifacts and generated files
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)/*
	@echo "Removing generator tools..."
	@rm -f $(GLOBALIZE_SYMBOLS_GEN)
	@echo "Note: Generated code in linknames.go and main.go will be regenerated by //go:generate on next build"

# Phony targets
.PHONY: all clean push mazboot test regenerate-assets

# Default target: build mazboot and copy to flash
all: mazboot

# Regenerate binary assets from source images/fonts
regenerate-assets: $(BOOT_IMAGE_BIN)
	@echo "Assets regenerated successfully"


