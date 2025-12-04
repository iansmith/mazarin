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

# Source files
BOOT_SRC = boot.s
LIB_SRC = lib.s
KERNEL_GO_SRC = kernel.go
KERNEL_C_SRC = kernel.c
LINKER_SCRIPT = linker.ld

# Bitfield code generation
BITFIELD_GEN = bitfield/page_flags_gen.go
BITFIELD_GEN_SRC = bitfield/gen.go
BITFIELD_FLAGS_SRC = bitfield/page_flags.go

# Object files
BOOT_OBJ = boot.o
LIB_OBJ = lib.o
KERNEL_GO_OBJ = kernel_go.o
KERNEL_C_OBJ = kernel_c.o

# Output file
KERNEL_ELF = kernel.elf

# Default: use Go version
KERNEL_OBJ = $(KERNEL_GO_OBJ)

# Generate bitfield unpacking code
$(BITFIELD_GEN): $(BITFIELD_GEN_SRC) $(BITFIELD_FLAGS_SRC)
	@echo "Generating bitfield code..."
	@GOTOOLCHAIN=local $(GO) run $(BITFIELD_GEN_SRC) > $(BITFIELD_GEN)

# Default target
all: $(BITFIELD_GEN) $(KERNEL_ELF)

# Link kernel using external linker (target-gcc)
# We use Go toolchain for compilation, but external linker for final linking
$(KERNEL_ELF): $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_OBJ) $(LINKER_SCRIPT)
	$(CC) $(LDFLAGS) -o $@ $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_OBJ)

# Compile boot.s
$(BOOT_OBJ): $(BOOT_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile lib.s
$(LIB_OBJ): $(LIB_SRC)
	$(CC) $(ASFLAGS) -c $< -o $@

# Compile kernel.go using go build with c-archive mode
# Then use objcopy to promote local symbols to global
KERNEL_GO_ARCHIVE = kernel_go
KERNEL_GO_TEMP = kernel_go_temp.o
OBJCOPY = /Users/iansmith/mazzy/bin/target-objcopy
$(KERNEL_GO_OBJ): $(KERNEL_GO_SRC) $(BITFIELD_GEN) go.mod
	GOTOOLCHAIN=auto GOARCH=$(GOARCH) GOOS=$(GOOS) $(GO) build $(GO_BUILD_FLAGS) -o $(KERNEL_GO_ARCHIVE) $<
	@# Extract the actual object file (go.o) from the C archive
	ar x $(KERNEL_GO_ARCHIVE) go.o
	mv go.o $(KERNEL_GO_TEMP)
	@# Use objcopy to promote main.KernelMain from local to global symbol
	@# This allows the linker to resolve it from assembly
	$(OBJCOPY) --globalize-symbol=main.KernelMain $(KERNEL_GO_TEMP) $@ || \
	 (cp $(KERNEL_GO_TEMP) $@ && echo "Warning: Could not promote symbol")
	@rm -f $(KERNEL_GO_ARCHIVE) $(KERNEL_GO_TEMP) kernel_go.h __.SYMDEF

# Alternative: Compile kernel.c (for comparison/testing)
$(KERNEL_C_OBJ): $(KERNEL_C_SRC)
	$(CC) $(CFLAGS) -c $< -o $@

# Clean build artifacts
clean:
	rm -f $(BOOT_OBJ) $(LIB_OBJ) $(KERNEL_GO_OBJ) $(KERNEL_C_OBJ) $(KERNEL_ELF) kernel_go.pkg __.PKGDEF kernel_go kernel_go.h __.SYMDEF
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

# Generate code target (can be run separately)
generate: $(BITFIELD_GEN)

