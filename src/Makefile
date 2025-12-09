# Cross-compiler path
CC = /Users/iansmith/mazzy/bin/target-gcc

# Go compiler and tools
# Use ~/mazzy/bin/go as bootstrap, which will use the toolchain from go.mod
GO = /Users/iansmith/mazzy/bin/go
GOARCH = arm64
GOOS = linux

# Runtime patching tool
PATCH_RUNTIME = ../tools/runtime/patch_runtime.py

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

# Source files - Assembly in asm/ directory
BOOT_SRC = asm/boot.s
LIB_SRC = asm/lib.s
WRITEBARRIER_SRC = asm/writebarrier.s
EXCEPTIONS_SRC = asm/exceptions.s
IMAGE_SRC = asm/image.s
LINKER_SCRIPT = linker.ld

# Go package location
GO_PACKAGE_DIR = go/mazarin

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
         go/dummy/dummy.go

# Bitfield code generation
BITFIELD_GEN = bitfield/page_flags_gen.go
BITFIELD_GEN_SRC = bitfield/gen.go
BITFIELD_FLAGS_SRC = bitfield/page_flags.go

# Object files
BOOT_OBJ = boot.o
LIB_OBJ = lib.o
WRITEBARRIER_OBJ = writebarrier.o
EXCEPTIONS_OBJ = exceptions.o
IMAGE_OBJ = image.o
KERNEL_GO_OBJ = kernel_go.o

# Output file
KERNEL_ELF = kernel.elf
QEMU_KERNEL_OUT = ../docker/builtin/kernel.elf

# Default: use Go version
KERNEL_OBJ = $(KERNEL_GO_OBJ)

# Default target: build the kernel
# This automatically triggers all dependencies including bitfield code generation
# Dependency chain: kernel.elf -> (boot.o, lib.o, exceptions.o, kernel_go.o) -> (asm sources, Go sources, bitfield gen)
$(KERNEL_ELF): $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(KERNEL_OBJ) $(LINKER_SCRIPT)
	$(CC) $(LDFLAGS) -o $@ $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(KERNEL_OBJ)

# Generate bitfield unpacking code
# This is automatically triggered as a dependency of kernel_go.o
$(BITFIELD_GEN): $(BITFIELD_GEN_SRC) $(BITFIELD_FLAGS_SRC)
	@echo "Generating bitfield code..."
	@GOTOOLCHAIN=local $(GO) run $(BITFIELD_GEN_SRC) > $(BITFIELD_GEN)

# Compile boot.s from asm/ directory
$(BOOT_OBJ): $(BOOT_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile lib.s from asm/ directory
$(LIB_OBJ): $(LIB_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile exceptions.s from asm/ directory
$(EXCEPTIONS_OBJ): $(EXCEPTIONS_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile writebarrier.s from asm/ directory
$(WRITEBARRIER_OBJ): $(WRITEBARRIER_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile image.s from asm/ directory
# Add the image binary as dependency so changes trigger rebuild
$(IMAGE_OBJ): $(IMAGE_SRC) ../assets/boot-mazarin.bin
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile kernel Go sources from go/mazarin package using go build with c-archive mode
# Then use objcopy to promote local symbols to global
KERNEL_GO_ARCHIVE = kernel_go
KERNEL_GO_TEMP = kernel_go_temp.o
OBJCOPY = /Users/iansmith/mazzy/bin/target-objcopy

# Build tags: REQUIRED flags for platform and architecture selection
# PLATFORM: qemuvirt (required for now, raspi4/raspi5 to be added later)
# ARCH: aarch64 (required for now, riscv64 to be added later)
# Compile will FAIL if these are not provided - this ensures we're always explicit about target
PLATFORM ?= 
ARCH ?= 

$(KERNEL_GO_OBJ): $(BITFIELD_GEN) go.mod $(GO_SRC)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) go.o kernel_go.h __.SYMDEF
	@# Verify required build flags are set
	@if [ -z "$(PLATFORM)" ]; then \
		echo "ERROR: PLATFORM is required. Use: make PLATFORM=qemuvirt ARCH=aarch64"; \
		exit 1; \
	fi
	@if [ -z "$(ARCH)" ]; then \
		echo "ERROR: ARCH is required. Use: make PLATFORM=qemuvirt ARCH=aarch64"; \
		exit 1; \
	fi
	@# Build Go package from go/mazarin directory with required build tags
	@echo "Building with PLATFORM=$(PLATFORM) ARCH=$(ARCH)"
	@GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "$(PLATFORM) $(ARCH)" $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) ./$(GO_PACKAGE_DIR)
	@# Extract the actual object file (go.o) from the C archive
	ar x $(KERNEL_GO_ARCHIVE) go.o
	mv go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote Go functions to global symbols
	@# This allows the linker to resolve them from assembly
	$(OBJCOPY) --globalize-symbol=main.KernelMain \
	           --globalize-symbol=main.IRQHandler \
	           --globalize-symbol=main.FIQHandler \
	           --globalize-symbol=main.SErrorHandler \
	           --globalize-symbol=main.GoEventLoopEntry \
	           $(KERNEL_GO_TEMP) $@ || \
	 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbols")
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) kernel_go.h __.SYMDEF

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(KERNEL_GO_OBJ) $(KERNEL_GO_OBJ_QEMU) $(KERNEL_ELF) kernel-qemu.elf
	rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) go.o
	rm -f kernel_go.pkg __.PKGDEF kernel_go.h __.SYMDEF
	rm -f kernel.o  # Clean up any leftover object files from old builds
	rm -f $(BITFIELD_GEN)

# QEMU build target - rebuilds Go object with qemuvirt and aarch64 tags
KERNEL_GO_OBJ_QEMU = kernel_go_qemu.o
$(KERNEL_GO_OBJ_QEMU): $(BITFIELD_GEN) go.mod $(GO_SRC)
	@# Clean up any leftover files from previous builds
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) go.o kernel_go.h __.SYMDEF
	@# Build Go package from go/mazarin directory with required tags
	@echo "Building for QEMU with tags: qemuvirt aarch64"
	@GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build -tags "qemuvirt aarch64" $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) ./$(GO_PACKAGE_DIR)
	@# Extract the actual object file (go.o) from the C archive
	@ar x $(KERNEL_GO_ARCHIVE) go.o
	@mv go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote main functions and variables from local to global symbols
	@# This allows the linker to resolve them from assembly
	@$(OBJCOPY) --globalize-symbol=main.KernelMain \
	             --globalize-symbol=main.GrowStackForCurrent \
	             --globalize-symbol=main.interruptHandlers \
	             --globalize-symbol=main.GoEventLoopEntry \
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
	@rm -f $(KERNEL_GO_ARCHIVE) kernel_go.h __.SYMDEF
	@# TEMP: Keep kernel_go_temp.o for inspection

kernel-qemu.elf: $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ) $(KERNEL_GO_OBJ_QEMU) $(LINKER_SCRIPT) $(PATCH_RUNTIME)
	@# Link exceptions.o, then writebarrier.o so our global symbols override Go runtime's
	@# Our writebarrier.s provides global (T) symbols that should take precedence
	$(CC) $(LDFLAGS) -o $@.tmp $(BOOT_OBJ) $(LIB_OBJ) $(EXCEPTIONS_OBJ) $(KERNEL_GO_OBJ_QEMU) $(WRITEBARRIER_OBJ) $(IMAGE_OBJ)
	@# Patch the binary to redirect calls from Go runtime functions to our implementations
	@echo "Patching runtime function calls..."
	@python3 $(PATCH_RUNTIME) $@.tmp $(RUNTIME_REPLACEMENTS) && mv $@.tmp $@ || \
	 (echo "Warning: Could not patch binary, using unpatched version" && mv $@.tmp $@)

# Push kernel.elf to docker/builtin directory
push: $(KERNEL_ELF)
	@mkdir -p ../docker/builtin
	cp $(KERNEL_ELF) ../docker/builtin/

# Build QEMU kernel and push to docker/builtin directory (used by mazboot)
# This target builds kernel-qemu.elf and copies it to ../docker/builtin/kernel.elf
qemu: $(QEMU_KERNEL_OUT)
	@echo "QEMU kernel ready at $(QEMU_KERNEL_OUT)"

# Rule to build and copy QEMU kernel to docker/builtin
$(QEMU_KERNEL_OUT): kernel-qemu.elf
	@mkdir -p ../docker/builtin
	cp $< $@
	@echo "Copied kernel-qemu.elf to docker/builtin/kernel.elf"

# Test target - run Go tests
test: $(BITFIELD_GEN)
	@echo "Running tests..."
	@GOTOOLCHAIN=local $(GO) test -v ./bitfield

# Phony targets
.PHONY: all clean push qemu generate test

# Alias for backward compatibility (all just builds kernel.elf)
all: $(KERNEL_ELF)

# Generate code target (can be run separately)
generate: $(BITFIELD_GEN)

