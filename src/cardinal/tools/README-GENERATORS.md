# Code Generation Tools

This directory contains tools for automatically generating Go code from assembly files.

## Tools

### `generate-linknames.go`
Scans assembly files for `.global` symbols and generates `//go:linkname` declarations for Go code to call assembly functions.

**Usage:**
```bash
# From project root
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-linknames.go src/mazboot/asm/aarch64 > output.txt

# Or from src/mazboot directory
cd src/mazboot
GOTOOLCHAIN=local go run tools/generate-linknames.go asm/aarch64 > output.txt
```

**Output:** Generates Go function declarations with `//go:linkname` directives, outputting only the content that goes between `//{{ LINKNAME START}}` and `//{{ LINKNAME END}}` markers.

**Example output:**
```go
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)

//go:linkname uart_putc_pl011 uart_putc_pl011
//go:nosplit
func uart_putc_pl011(c byte)
```

### `generate-main-calls.go`
Scans assembly files for Go functions called from assembly (via `.extern main.*` or `bl main.*`) and generates dummy function calls to prevent dead code elimination.

**Usage:**
```bash
# From project root
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-main-calls.go src/mazboot/asm/aarch64 src/mazboot/go/mazboot > output.txt

# Or from src/mazboot directory
cd src/mazboot
GOTOOLCHAIN=local go run tools/generate-main-calls.go asm/aarch64 go/mazboot > output.txt
```

**Output:** Generates dummy function calls with appropriate zero/nil parameters, outputting only the content that goes between `//{{ LINKNAME START}}` and `//{{ LINKNAME END}}` markers.

**Example output:**
```go
	// Call kernel initialization functions to ensure they're compiled
	// This will never execute in bare metal, but ensures the functions exist
	KernelMain(0, 0, 0)

	// Reference interrupt handlers to prevent optimization
	// These are called from assembly interrupt handlers and must not be optimized away
	// This will never execute in bare metal, but ensures the functions exist
	uartTransmitHandler() // UART TX interrupt handler
```

### `insert-between-markers.py`
Helper script that inserts generated content between markers in target files.

**Usage:**
```bash
python3 tools/insert-between-markers.py <target_file> <start_marker> <end_marker> <content_file>
```

**Example:**
```bash
python3 tools/insert-between-markers.py \
    go/mazboot/linknames.go \
    "//{{ LINKNAME START}}" \
    "//{{ LINKNAME END}}" \
    build/mazboot/linknames_content.tmp
```

## Makefile Integration

The tools are integrated into the build system via Makefile targets:

### Generate all code (bitfields + assembly-to-Go)
```bash
cd src/mazboot
make generate
```

### Generate only assembly-to-Go linking code
```bash
cd src/mazboot
make generateasm2go
```

### Generate only bitfield code
```bash
cd src/mazboot
make generate-bitfield
```

### Generate individual files
```bash
cd src/mazboot
make go/mazboot/linknames.go
make go/mazboot/main.go
```

## Example Commands

### Test generate-linknames.go
```bash
# From project root
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-linknames.go src/mazboot/asm/aarch64 | head -30

# See all generated linknames
GOTOOLCHAIN=local go run src/mazboot/tools/generate-linknames.go src/mazboot/asm/aarch64 | wc -l

# Save to file for inspection
GOTOOLCHAIN=local go run src/mazboot/tools/generate-linknames.go src/mazboot/asm/aarch64 > /tmp/linknames_output.txt
cat /tmp/linknames_output.txt
```

### Test generate-main-calls.go
```bash
# From project root
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-main-calls.go src/mazboot/asm/aarch64 src/mazboot/go/mazboot

# Save to file for inspection
GOTOOLCHAIN=local go run src/mazboot/tools/generate-main-calls.go src/mazboot/asm/aarch64 src/mazboot/go/mazboot > /tmp/main_calls_output.txt
cat /tmp/main_calls_output.txt
```

### Test full generation workflow
```bash
# From src/mazboot directory
cd src/mazboot

# Generate everything
make generate

# Check what was generated
git diff go/mazboot/linknames.go
git diff go/mazboot/main.go

# Regenerate just assembly-to-Go code
make generateasm2go

# Regenerate just one file
make go/mazboot/linknames.go
```

### Manual testing with insert script
```bash
cd src/mazboot

# Generate linknames content
GOTOOLCHAIN=local go run tools/generate-linknames.go asm/aarch64 > build/mazboot/linknames_test.tmp

# Insert into file (backup first!)
cp go/mazboot/linknames.go go/mazboot/linknames.go.bak
python3 tools/insert-between-markers.py \
    go/mazboot/linknames.go \
    "//{{ LINKNAME START}}" \
    "//{{ LINKNAME END}}" \
    build/mazboot/linknames_test.tmp

# Check the result
diff go/mazboot/linknames.go.bak go/mazboot/linknames.go
```

## How It Works

1. **generate-linknames.go**:
   - Scans all `.s` files in `asm/aarch64/`
   - Finds `.global` symbols
   - Filters out excluded symbols (entry points, data labels, etc.)
   - Parses function signatures from comments
   - Converts C types to Go types
   - Generates `//go:linkname` declarations

2. **generate-main-calls.go**:
   - Scans all `.s` files in `asm/aarch64/`
   - Finds `.extern main.*` declarations and `bl main.*` calls
   - Parses Go source files to find function signatures
   - Generates dummy calls with appropriate parameter values

3. **insert-between-markers.py**:
   - Reads target file
   - Finds start and end markers
   - Replaces content between markers with new content
   - Writes result back to file

## Notes

- Both generators output **only** the content between markers (not the full file)
- The insert script handles file replacement
- The Makefile orchestrates the full workflow
- Generated files are marked with `// DO NOT EDIT THIS FILE. It is generated by the build system.`




