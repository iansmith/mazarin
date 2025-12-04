# Cross-compiler path
CC = /Users/iansmith/mazzy/bin/target-gcc

# Go compiler and tools
# Use ~/mazzy/bin/go as bootstrap, which will use the toolchain from go.mod
GO = /Users/iansmith/mazzy/bin/go
GOARCH = arm64
GOOS = linux

# Compiler flags
CFLAGS = -mcpu=cortex-a72 -march=armv8-a -fpic -ffreestanding -std=gnu99 -O2 -Wall -Wextra -g
ASFLAGS = -mcpu=cortex-a72 -march=armv8-a -ffreestanding -g
LDFLAGS = -T linker.ld -ffreestanding -O2 -nostdlib -g

# Go build flags for c-archive mode with external linker
# Use go build -buildmode=c-archive to create a C-compatible archive
GO_BUILD_FLAGS = -buildmode=c-archive

# Source files - Assembly in asm/ directory
BOOT_SRC = asm/boot.s
LIB_SRC = asm/lib.s
LINKER_SCRIPT = linker.ld

# Go package location
GO_PACKAGE_DIR = go/mazarin

# Bitfield code generation
BITFIELD_GEN = bitfield/page_flags_gen.go
BITFIELD_GEN_SRC = bitfield/gen.go
BITFIELD_FLAGS_SRC = bitfield/page_flags.go

# Object files
BOOT_OBJ = boot.o
LIB_OBJ = lib.o
KERNEL_GO_OBJ = kernel_go.o

# Output file
KERNEL_ELF = kernel.elf

# Default: use Go version
KERNEL_OBJ = $(KERNEL_GO_OBJ)

# Default target: build the kernel
# This automatically triggers all dependencies including bitfield code generation
# Dependency chain: kernel.elf -> kernel_go.o -> bitfield/page_flags_gen.go
$(KERNEL_ELF): $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_OBJ) $(LINKER_SCRIPT)
	$(CC) $(LDFLAGS) -o $@ $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_OBJ)

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

# Compile kernel Go sources from go/mazarin package using go build with c-archive mode
# Then use objcopy to promote local symbols to global
KERNEL_GO_ARCHIVE = kernel_go
KERNEL_GO_TEMP = kernel_go_temp.o
OBJCOPY = /Users/iansmith/mazzy/bin/target-objcopy
$(KERNEL_GO_OBJ): $(BITFIELD_GEN) go.mod
	@# Build Go package from go/mazarin directory
	GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) ./$(GO_PACKAGE_DIR)
	@# Extract the actual object file (go.o) from the C archive
	ar x $(KERNEL_GO_ARCHIVE) go.o
	mv go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote main.KernelMain from local to global symbol
	@# This allows the linker to resolve it from assembly
	$(OBJCOPY) --globalize-symbol=main.KernelMain $(KERNEL_GO_TEMP) $@ || \
	 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbol")
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) kernel_go.h __.SYMDEF

# Clean build artifacts
clean:
	rm -f $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_GO_OBJ) $(KERNEL_ELF) kernel_go.pkg __.PKGDEF kernel_go kernel_go.h __.SYMDEF
	rm -f kernel.o go.o  # Clean up any leftover object files (kernel.o from old builds, go.o from archive extraction)
	rm -f $(BITFIELD_GEN)

# Push kernel.elf to docker/builtin directory
push: $(KERNEL_ELF)
	@mkdir -p ../docker/builtin
	cp $(KERNEL_ELF) ../docker/builtin/

# Test target - run Go tests
test: $(BITFIELD_GEN)
	@echo "Running tests..."
	@GOTOOLCHAIN=local $(GO) test -v ./bitfield

# Phony targets
.PHONY: all clean push generate test

# Alias for backward compatibility (all just builds kernel.elf)
all: $(KERNEL_ELF)

# Generate code target (can be run separately)
generate: $(BITFIELD_GEN)

