# Cross-compiler path
CC = /Users/iansmith/mazzy/bin/target-gcc

# Go compiler and tools
# Use ~/mazzy/bin/go as bootstrap, which will use the toolchain from go.mod
GO = /Users/iansmith/mazzy/bin/go
GOARCH = arm64
GOOS = linux

# Runtime patching tool
PATCH_RUNTIME = ../../tools/runtime/patch_runtime.py

# Runtime function replacements: <old_func> <new_func> pairs
# These functions will have their symbols weakened and calls patched to use our implementations
RUNTIME_REPLACEMENTS = runtime.gcWriteBarrier2 runtime.gcWriteBarrier2 \
                       runtime.gcWriteBarrier3 runtime.gcWriteBarrier3 \
                       runtime.gcWriteBarrier4 runtime.gcWriteBarrier4 \
                       gcWriteBarrier gcWriteBarrier

# Compiler flags
CFLAGS = -mcpu=cortex-a72 -march=armv8-a -fpic -ffreestanding -std=gnu99 -O2 -Wall -Wextra -g
ASFLAGS = -mcpu=cortex-a72 -march=armv8-a -ffreestanding -g
LDFLAGS = -T linker.ld -ffreestanding -O2 -nostdlib -g

# Go build flags for c-archive mode with external linker
# Use go build -buildmode=c-archive to create a C-compatible archive
GO_GCFLAGS ?= "all=-N -l"
GO_BUILD_FLAGS = -buildmode=c-archive -gcflags $(GO_GCFLAGS)

# Source files - Assembly in asm/aarch64/ directory
BOOT_SRC = asm/aarch64/boot.s
LIB_SRC = asm/aarch64/lib.s
WRITEBARRIER_SRC = asm/aarch64/writebarrier.s
EXCEPTIONS_SRC = asm/aarch64/exceptions.s
IMAGE_SRC = asm/aarch64/image.s
GOROUTINE_SRC = asm/aarch64/goroutine.s
LINKER_SCRIPT = linker.ld

# Asset generation tools and sources
IMAGECONVERT_TOOL = ../../tools/imageconvert/main.go
IMAGECONVERT_GO_MOD = ../../tools/imageconvert/go.mod
# FONTRENDER_TOOL = ../../tools/fontrender/fontrender.py  # Not currently used in build
BOOT_IMAGE_BIN = ../../assets/boot-mazarin.bin
BOOT_IMAGE_SOURCES = ../../assets/mazarin-original.png ../../assets/mazarin50.png

# Go package location
GO_PACKAGE_DIR = go/mazboot

# Go source files (all files - build tags determine which are included)
# Note: Build tags (qemu/!qemu) determine which files are actually compiled,
# but we list all files here so make knows to rebuild when any file changes
GO_SRC = $(GO_PACKAGE_DIR)/kernel.go \
         $(GO_PACKAGE_DIR)/page.go \
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

# Bitfield code generation
BITFIELD_GEN = bitfield/page_flags_gen.go
BITFIELD_GEN_SRC = bitfield/gen.go
BITFIELD_FLAGS_SRC = bitfield/page_flags.go

# Build output directory
BUILD_DIR = ../../build/mazboot

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
MAZBOOT_BINARY = $(BUILD_DIR)/mazboot
QEMU_KERNEL_OUT = ../../docker/builtin/kernel.elf

# Default target: build mazboot for QEMU
# This automatically triggers all dependencies including bitfield code generation
# Dependency chain: mazboot -> (boot.o, lib.o, exceptions.o, kernel_go_qemu.o) -> (asm sources, Go sources, bitfield gen)

# Generate bitfield unpacking code
# This is automatically triggered as a dependency of kernel_go.o
$(BITFIELD_GEN): $(BITFIELD_GEN_SRC) $(BITFIELD_FLAGS_SRC)
	@echo "Generating bitfield code..."
	@GOTOOLCHAIN=auto $(GO) run $(BITFIELD_GEN_SRC) > $(BITFIELD_GEN)

# Generate boot image binary from PNG source
# The imageconvert tool converts PNG to raw ARGB8888 binary format
# Note: Using mazarin50.png as the source (50x50 version for boot logo)
# The binary is committed to git but will be regenerated if sources change
$(BOOT_IMAGE_BIN): $(BOOT_IMAGE_SOURCES) $(IMAGECONVERT_TOOL) $(IMAGECONVERT_GO_MOD)
	@echo "Generating boot image binary from PNG..."
	@cd ../../tools/imageconvert && $(GO) run main.go ../../../assets/mazarin50.png ../../../assets/boot-mazarin.bin

# Compile boot.s from asm/aarch64/ directory
$(BOOT_OBJ): $(BOOT_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile lib.s from asm/aarch64/ directory
$(LIB_OBJ): $(LIB_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile exceptions.s from asm/aarch64/ directory
$(EXCEPTIONS_OBJ): $(EXCEPTIONS_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile writebarrier.s from asm/aarch64/ directory
$(WRITEBARRIER_OBJ): $(WRITEBARRIER_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile image.s from asm/aarch64/ directory
# Add the image binary as dependency so changes trigger rebuild
# Dependency chain: PNG sources + imageconvert tool → boot-mazarin.bin → image.o
$(IMAGE_OBJ): $(IMAGE_SRC) $(BOOT_IMAGE_BIN)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile goroutine.s from asm/aarch64/ directory
$(GOROUTINE_OBJ): $(GOROUTINE_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile get_caller_sp.s from asm/aarch64/ directory
GET_CALLER_SP_SRC = asm/aarch64/get_caller_sp.s
$(GET_CALLER_SP_OBJ): $(GET_CALLER_SP_SRC)
	@mkdir -p $(BUILD_DIR)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile kernel Go sources from go/mazboot package using go build with c-archive mode
# Then use objcopy to promote local symbols to global
KERNEL_GO_ARCHIVE = $(BUILD_DIR)/kernel_go
KERNEL_GO_TEMP = $(BUILD_DIR)/kernel_go_temp.o
OBJCOPY = /Users/iansmith/mazzy/bin/target-objcopy

# Build tags: REQUIRED flags for platform and architecture selection
# PLATFORM: qemuvirt (required for now, raspi4/raspi5 to be added later)
# ARCH: aarch64 (required for now, riscv64 to be added later)
# Compile will FAIL if these are not provided - this ensures we're always explicit about target
PLATFORM ?= 
ARCH ?= 

$(KERNEL_GO_OBJ): $(BITFIELD_GEN) go.mod $(GO_SRC)
	@mkdir -p $(BUILD_DIR)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) $(BUILD_DIR)/go.o $(BUILD_DIR)/kernel_go.h $(BUILD_DIR)/__.SYMDEF
	@# Verify required build flags are set
	@if [ -z "$(PLATFORM)" ]; then \
		echo "ERROR: PLATFORM is required. Use: make PLATFORM=qemuvirt ARCH=aarch64"; \
		exit 1; \
	fi
	@if [ -z "$(ARCH)" ]; then \
		echo "ERROR: ARCH is required. Use: make PLATFORM=qemuvirt ARCH=aarch64"; \
		exit 1; \
	fi
	@# Build Go package from go/mazboot directory with required build tags
	@echo "Building with PLATFORM=$(PLATFORM) ARCH=$(ARCH)"
	@GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "$(PLATFORM) $(ARCH)" $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) ./$(GO_PACKAGE_DIR)
	@# Extract the actual object file (go.o) from the C archive
	@cd $(BUILD_DIR) && ar x $(notdir $(KERNEL_GO_ARCHIVE)) go.o
	@mv $(BUILD_DIR)/go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote Go functions to global symbols
	@# This allows the linker to resolve them from assembly
	$(OBJCOPY) --globalize-symbol=main.KernelMain \
	           --globalize-symbol=main.GoEventLoopEntry \
	           --globalize-symbol=main.uartTransmitHandler \
	           $(KERNEL_GO_TEMP) $@ || \
	 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbols")
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) $(BUILD_DIR)/kernel_go.h $(BUILD_DIR)/__.SYMDEF

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)/*

# QEMU build target - rebuilds Go object with qemuvirt and aarch64 tags
KERNEL_GO_OBJ_QEMU = $(BUILD_DIR)/kernel_go_qemu.o
$(KERNEL_GO_OBJ_QEMU): $(BITFIELD_GEN) go.mod $(GO_SRC)
	@mkdir -p $(BUILD_DIR)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) $(BUILD_DIR)/go.o $(BUILD_DIR)/kernel_go.h $(BUILD_DIR)/__.SYMDEF
	@# Build Go package from go/mazboot directory with required tags
	@echo "Building for QEMU with tags: qemuvirt aarch64"
	@GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "qemuvirt aarch64" $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) ./$(GO_PACKAGE_DIR)
	@# Extract the actual object file (go.o) from the C archive
	@cd $(BUILD_DIR) && ar x $(notdir $(KERNEL_GO_ARCHIVE)) go.o
	@mv $(BUILD_DIR)/go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote main functions and variables from local to global symbols
	@# This allows the linker to resolve them from assembly
	@$(OBJCOPY) --globalize-symbol=main.KernelMain \
	             --globalize-symbol=main.GrowStackForCurrent \
	             --globalize-symbol=main.interruptHandlers \
	             --globalize-symbol=main.GoEventLoopEntry \
	             --globalize-symbol=main.kernelMainBodyWrapper \
	             --globalize-symbol=fb_putc_irq \
	             --globalize-symbol=ExceptionHandler \
	             --globalize-symbol=main.uartTransmitHandler \
	             $(KERNEL_GO_TEMP) $@ || \
	 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbols")
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
	@# TEMP: Keep kernel_go_temp.o for inspection

# Build mazboot (default: QEMU build with qemuvirt and aarch64 tags)
$(MAZBOOT_BINARY): $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(GOROUTINE_OBJ) $(GET_CALLER_SP_OBJ) $(KERNEL_GO_OBJ_QEMU) $(LINKER_SCRIPT) $(PATCH_RUNTIME)
	@mkdir -p $(BUILD_DIR)
	@# Link exceptions.o, then writebarrier.o so our global symbols override Go runtime's
	@# Our writebarrier.s provides global (T) symbols that should take precedence
	$(CC) $(LDFLAGS) -o $@.tmp $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(KERNEL_GO_OBJ_QEMU) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(GOROUTINE_OBJ) $(GET_CALLER_SP_OBJ)
	@# Patch the binary to redirect calls from Go runtime functions to our implementations
	@echo "Patching runtime function calls..."
	@python3 $(PATCH_RUNTIME) $@.tmp $(RUNTIME_REPLACEMENTS) && mv $@.tmp $@ || \
	 (echo "Warning: Could not patch binary, using unpatched version" && mv $@.tmp $@)

# Push mazboot to docker/builtin directory
push: $(MAZBOOT_BINARY)
	@mkdir -p ../../docker/builtin
	cp $(MAZBOOT_BINARY) ../../docker/builtin/kernel.elf

# Build QEMU kernel and push to docker/builtin directory (used by mazboot)
# This target builds mazboot and copies it to docker/builtin/kernel.elf
qemu: $(QEMU_KERNEL_OUT)
	@echo "QEMU kernel ready at $(QEMU_KERNEL_OUT)"

# Rule to build and copy mazboot to docker/builtin
$(QEMU_KERNEL_OUT): $(MAZBOOT_BINARY)
	@mkdir -p ../../docker/builtin
	cp $< $@
	@echo "Copied mazboot to docker/builtin/kernel.elf"

# Test target - run Go tests
test: $(BITFIELD_GEN)
	@echo "Running tests..."
	@GOTOOLCHAIN=local $(GO) test -v ./bitfield

# Phony targets
.PHONY: all clean push qemu generate test regenerate-assets

# Alias for backward compatibility (all just builds mazboot)
all: $(MAZBOOT_BINARY)

# Generate code target (can be run separately)
generate: $(BITFIELD_GEN)

# Regenerate binary assets from source images/fonts
# Useful when PNG sources or conversion tools are modified
regenerate-assets: $(BOOT_IMAGE_BIN)
	@echo "Assets regenerated successfully"

