# Detailed Implementation Plan: Assembly-to-Go Code Generation Tools

This document provides a detailed, step-by-step plan for implementing and maintaining the assembly-to-Go code generation tools. Each step includes specific file paths, function names, line numbers, and code snippets.

## Overview

Two tools generate Go code from assembly files:
1. `generate-linknames.go` - Generates `//go:linkname` declarations for assembly functions
2. `generate-main-calls.go` - Generates dummy function calls to prevent dead code elimination

Both tools output to stdout, and a Python script inserts the output between markers in target files.

## File Structure

```
src/mazboot/
├── tools/
│   ├── generate-linknames.go      (Lines 1-437)
│   ├── generate-main-calls.go     (Lines 1-314)
│   ├── insert-between-markers.py  (Lines 1-94)
│   └── README-GENERATORS.md       (Documentation)
├── go/mazboot/
│   ├── linknames.go               (Generated content between markers)
│   └── main.go                    (Generated content between markers)
├── asm/aarch64/
│   ├── lib.s                      (Assembly functions with .global)
│   ├── exceptions.s               (Contains .extern and bl main.* calls)
│   └── *.s                        (Other assembly files)
└── Makefile                       (Lines 75-315, generation rules)
```

## Tool 1: generate-linknames.go

### Purpose
Scans assembly files for `.global` symbols and generates Go function declarations with `//go:linkname` directives.

### File Location
`src/mazboot/tools/generate-linknames.go`

### Main Function (Lines 22-34)
```go
func main() {
    asmDir := "asm/aarch64"
    if len(os.Args) > 1 {
        asmDir = os.Args[1]
    }
    globalSymbols := findGlobalSymbols(asmDir)
    generateLinknames(globalSymbols, asmDir)
}
```

### Key Functions

#### 1. findGlobalSymbols (Lines 400-425)
**Location:** `src/mazboot/tools/generate-linknames.go:400-425`

**Purpose:** Scans all `.s` files in the assembly directory for `.global` directives.

**Implementation Details:**
- Uses regex: `^\.global\s+([a-zA-Z_][a-zA-Z0-9_.]*)`
- Walks directory using `filepath.Walk`
- Returns list of symbol names as strings

**Code Pattern:**
```go
globalRe := regexp.MustCompile(`^\.global\s+([a-zA-Z_][a-zA-Z0-9_.]*)`)
// ... walk files and match regex ...
symbols = append(symbols, matches[1])
```

#### 2. generateLinknames (Lines 36-130)
**Location:** `src/mazboot/tools/generate-linknames.go:36-130`

**Purpose:** Main generation function that filters symbols and outputs Go code.

**Key Steps:**
1. **Exclude symbols (Lines 39-60):** Filter out entry points, data labels, runtime symbols
   - Excluded symbols list: `_start`, `kernel_main`, `exception_vectors*`, `runtime.*`, etc.
   - Functions already declared elsewhere: `read_elr_el1`, `read_esr_el1`, `read_far_el1`, `read_id_aa64pfr0_el1`, `read_scr_el3`

2. **Parse signatures (Line 87):** Call `parseFunctionSignatures(asmDir)` to get function parameter/return types

3. **Convert names (Line 84):** Call `convertToGoName(sym)` to convert assembly names to Go names
   - Special cases in `convertToGoName` (Lines 132-150):
     - `store_pointer_nobarrier` → `storePointerNoBarrier`
     - `verify_stack_pointer_reading` → `verifyStackPointerReading`
     - Most others kept as-is

4. **Generate output (Lines 111-130):** Print `//go:linkname`, `//go:nosplit`, and function signature

#### 3. parseFunctionSignatures (Lines 162-223)
**Location:** `src/mazboot/tools/generate-linknames.go:162-223`

**Purpose:** Parses assembly file comments to extract function signatures.

**Regex Patterns:**
- Function signature: `^//\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\(([^)]*)\)` (Line 167)
- Return type: `returns\s+([a-zA-Z_][a-zA-Z0-9_\s*]*)` (Line 168)

**Process:**
1. Walk all `.s` files (Line 170)
2. For each line, match signature regex (Line 183)
3. Check next line for return type (Lines 188-197)
4. Convert C types to Go types using `convertCParamsToGo` and `convertCTypeToGo`
5. Store in `signatures` map

**Example Assembly Comment:**
```assembly
// mmio_write(uintptr_t reg, uint32_t data)
// x0 = register address, w1 = data (32-bit)
.global mmio_write
```

**Parsed Result:**
- Function name: `mmio_write`
- Parameters: `reg uintptr, data uint32`
- Return type: (empty)

#### 4. convertCParamsToGo (Lines 225-350)
**Location:** `src/mazboot/tools/generate-linknames.go:225-350`

**Purpose:** Converts C-style parameter list to Go-style.

**Input Examples:**
- `uintptr_t reg, uint32_t data` → `reg uintptr, data uint32`
- `dest *unsafe.Pointer, value unsafe.Pointer` → `dest *unsafe.Pointer, value unsafe.Pointer`
- `void *ptr, uint32_t size` → `ptr unsafe.Pointer, size uint32`

**Key Logic:**
- Handles pointer patterns: `*type name`, `type* name`, `type *name`, `name *type` (Lines 249-330)
- Detects if format is `type name` (C style) or `name type` (Go style) using heuristic (Lines 278-295)
- Calls `convertCTypeToGo` for each type (Line 345)

#### 5. convertCTypeToGo (Lines 352-395)
**Location:** `src/mazboot/tools/generate-linknames.go:352-395`

**Purpose:** Converts individual C types to Go types.

**Type Mapping (Lines 354-365):**
```go
typeMap := map[string]string{
    "uintptr_t":      "uintptr",
    "uint32_t":       "uint32",
    "uint64_t":       "uint64",
    "uint16_t":       "uint16",
    "uint8_t":        "uint8",
    "int32_t":        "int32",
    "int":            "int",
    "void":           "",
    "byte":           "byte",
    "size_t":         "uint32",
    "unsafe.Pointer": "unsafe.Pointer",
}
```

**Process:**
1. Check exact match in typeMap (Line 358)
2. Remove `_t` suffix and check again (Line 363)
3. Return mapped type or original if not found

## Tool 2: generate-main-calls.go

### Purpose
Scans assembly files for Go functions called from assembly and generates dummy calls to prevent dead code elimination.

### File Location
`src/mazboot/tools/generate-main-calls.go`

### Main Function (Lines 26-43)
```go
func main() {
    asmDir := "asm/aarch64"
    goSourceDir := "go/mazboot"
    if len(os.Args) > 1 {
        asmDir = os.Args[1]
    }
    if len(os.Args) > 2 {
        goSourceDir = os.Args[2]
    }
    goFunctionsCalled := findGoFunctionsCalled(asmDir)
    generateMainCalls(goFunctionsCalled, goSourceDir)
}
```

### Key Functions

#### 1. findGoFunctionsCalled (Lines 250-314)
**Location:** `src/mazboot/tools/generate-main-calls.go:250-314`

**Purpose:** Finds Go functions called from assembly.

**Regex Patterns:**
- `.extern` declaration: `\.extern\s+main\.([a-zA-Z_][a-zA-Z0-9_]*)` (Line 252)
- `bl` call: `bl\s+main\.([a-zA-Z_][a-zA-Z0-9_]*)` (Line 253)

**Process:**
1. Walk all `.s` files (Line 256)
2. Skip comment-only lines to avoid false positives (Lines 264-267)
3. Match `.extern` declarations (Lines 270-274)
4. Match `bl main.*` calls (only in non-comment lines) (Lines 277-282)
5. Deduplicate using `seen` map (Line 255)

**Example Assembly Code:**
```assembly
.extern main.KernelMain
// ... later ...
bl main.KernelMain
```

**Parsed Result:**
- Function name: `KernelMain` (without `main.` prefix)

#### 2. generateMainCalls (Lines 45-120)
**Location:** `src/mazboot/tools/generate-main-calls.go:45-120`

**Purpose:** Generates dummy function calls grouped by category.

**Known Signatures (Lines 48-53):**
```go
knownSignatures := map[string][]string{
    "KernelMain":            {"uint32", "uint32", "uint32"},
    "uartTransmitHandler":   {},
    "kernelMainBodyWrapper": {},
    "GrowStackForCurrent":   {},
}
```

**Process:**
1. Look up or parse function signatures (Lines 55-66)
2. Group functions by category (Lines 68-78):
   - Kernel functions: `KernelMain`
   - Interrupt handlers: functions with "handler" in name
   - Other functions: everything else
3. Generate calls with comments (Lines 82-119)

**Output Format:**
```go
// Call kernel initialization functions...
KernelMain(0, 0, 0)

// Reference interrupt handlers...
uartTransmitHandler() // UART TX interrupt handler

// Reference other functions...
GrowStackForCurrent()
```

#### 3. findFunctionSignature (Lines 160-230)
**Location:** `src/mazboot/tools/generate-main-calls.go:160-230`

**Purpose:** Parses Go source files to find function signatures.

**Process:**
1. Parse Go files using `go/parser` (Line 165)
2. Walk AST using `ast.Inspect` (Line 169)
3. Find `*ast.FuncDecl` nodes (Line 171)
4. Extract parameter types using `exprToString` (Line 186)
5. Return `FunctionSignature` struct

**Example Go Function:**
```go
func KernelMain(r0, r1, atags uint32) {
    // ...
}
```

**Parsed Result:**
- Name: `KernelMain`
- Params: `[]string{"uint32", "uint32", "uint32"}`

#### 4. generateFunctionCall (Lines 122-135)
**Location:** `src/mazboot/tools/generate-main-calls.go:122-135`

**Purpose:** Generates function call string with dummy arguments.

**Process:**
1. If no parameters, return `FunctionName()`
2. Otherwise, generate arguments using `generateDummyArg` for each parameter type
3. Join with commas: `FunctionName(arg1, arg2, arg3)`

#### 5. generateDummyArg (Lines 137-165)
**Location:** `src/mazboot/tools/generate-main-calls.go:137-165`

**Purpose:** Generates dummy argument value based on type.

**Type Mappings:**
- `uint32`, `uint`, `uintptr` → `"0"`
- `uint64` → `"0"`
- `uint16` → `"0"`
- `uint8`, `byte` → `"0"`
- `int32`, `int` → `"0"`
- `int64` → `"0"`
- `bool` → `"false"`
- `string` → `""`
- `unsafe.Pointer` → `"nil"`
- Pointer types (`*type`) → `"nil"`
- Unknown types → `"0"` or `"nil"` based on prefix

## Tool 3: insert-between-markers.py

### Purpose
Inserts generated content between markers in target files.

### File Location
`src/mazboot/tools/insert-between-markers.py`

### Main Function (Lines 15-84)
**Process:**
1. Read target file (Lines 20-26)
2. Read content file (Lines 29-35)
3. Find start marker (Line 38)
4. Find end marker (Line 39)
5. Find line boundaries (Lines 42-50)
6. Replace content between markers (Lines 53-57)
7. Write back to file (Lines 60-64)

### Usage
```bash
python3 tools/insert-between-markers.py \
    go/mazboot/linknames.go \
    "//{{ LINKNAME START}}" \
    "//{{ LINKNAME END}}" \
    build/mazboot/linknames_content.tmp
```

## Makefile Integration

### File Location
`src/mazboot/Makefile`

### Variable Definitions (Lines 82-88)
```makefile
LINKNAMES_GEN = tools/generate-linknames.go
MAIN_CALLS_GEN = tools/generate-main-calls.go
INSERT_SCRIPT = tools/insert-between-markers.py
LINKNAMES_GO = $(GO_PACKAGE_DIR)/linknames.go
MAIN_GO = $(GO_PACKAGE_DIR)/main.go
```

### Generation Rules

#### linknames.go Rule (Lines 120-125)
```makefile
$(LINKNAMES_GO): $(LINKNAMES_GEN) $(wildcard asm/aarch64/*.s) $(INSERT_SCRIPT)
	@echo "Generating linknames.go..."
	@mkdir -p $(BUILD_DIR)
	@cd ../.. && GOTOOLCHAIN=local $(GO) run src/mazboot/$(LINKNAMES_GEN) src/mazboot/asm/aarch64 > src/mazboot/$(BUILD_DIR)/linknames_content.tmp
	@python3 $(INSERT_SCRIPT) $(LINKNAMES_GO) "//{{ LINKNAME START}}" "//{{ LINKNAME END}}" $(BUILD_DIR)/linknames_content.tmp
	@rm -f $(BUILD_DIR)/linknames_content.tmp
```

**Steps:**
1. Dependencies: generator tool, all `.s` files, insert script
2. Run generator, redirect stdout to temp file
3. Run insert script to replace content between markers
4. Clean up temp file

#### main.go Rule (Lines 127-133)
```makefile
$(MAIN_GO): $(MAIN_CALLS_GEN) $(wildcard asm/aarch64/*.s) $(filter-out $(MAIN_GO),$(wildcard $(GO_PACKAGE_DIR)/*.go)) $(INSERT_SCRIPT)
	@echo "Generating main.go calls..."
	@mkdir -p $(BUILD_DIR)
	@cd ../.. && GOTOOLCHAIN=local $(GO) run src/mazboot/$(MAIN_CALLS_GEN) src/mazboot/asm/aarch64 src/mazboot/$(GO_PACKAGE_DIR) > src/mazboot/$(BUILD_DIR)/main_calls_content.tmp
	@python3 $(INSERT_SCRIPT) $(MAIN_GO) "//{{ LINKNAME START}}" "//{{ LINKNAME END}}" $(BUILD_DIR)/main_calls_content.tmp
	@rm -f $(BUILD_DIR)/main_calls_content.tmp
```

**Note:** Uses `filter-out $(MAIN_GO)` to avoid circular dependency.

#### Target Definitions (Lines 308-315)
```makefile
generate: generate-bitfield generateasm2go

generate-bitfield: $(BITFIELD_GEN)

generateasm2go: $(LINKNAMES_GO) $(MAIN_GO)
	@echo "Assembly-to-Go code generation complete"
```

## Target Files Structure

### linknames.go
**Location:** `src/mazboot/go/mazboot/linknames.go`

**Structure:**
```go
// DO NOT EDIT THIS FILE. It is generated by the build system.
package main

import "unsafe"

// {{ LINKNAME START}}
// [Generated content here]
// {{ LINKNAME END}}
```

**Marker Format:** `//{{ LINKNAME START}}` and `//{{ LINKNAME END}}`

### main.go
**Location:** `src/mazboot/go/mazboot/main.go`

**Structure:**
```go
// DO NOT EDIT THIS FILE. It is generated by the build system.
package main

func main() {
	//{{ LINKNAME START}}
	// [Generated content here]
	//{{ LINKNAME END}}
	// ...
}
```

**Marker Format:** `//{{ LINKNAME START}}` and `//{{ LINKNAME END}}`

## Common Issues and Fixes

### Issue 1: Parameter Parsing - "void *ptr" Format
**Problem:** Assembly comment `void *ptr` should become `ptr unsafe.Pointer` in Go.

**Location:** `generate-linknames.go:convertCParamsToGo` (Lines 225-350)

**Fix:** In the 2-part parsing (Lines 277-295), detect `void` as a type and handle `void *name` pattern:
```go
if parts[0] == "void" && strings.HasPrefix(parts[1], "*") {
    paramName = strings.TrimPrefix(parts[1], "*")
    goType = "unsafe.Pointer"
    // ... append to goParams
}
```

### Issue 2: False Positives from Comments
**Problem:** Comments containing `bl main.FunctionName` are matched.

**Location:** `generate-main-calls.go:findGoFunctionsCalled` (Lines 250-314)

**Fix:** Already implemented - skip comment-only lines (Lines 264-267):
```go
if strings.HasPrefix(trimmed, "//") && !strings.Contains(trimmed, ".extern") {
    continue
}
```

### Issue 3: Circular Dependency
**Problem:** `main.go` depends on itself.

**Location:** `Makefile:127`

**Fix:** Use `filter-out $(MAIN_GO)` to exclude main.go from wildcard:
```makefile
$(filter-out $(MAIN_GO),$(wildcard $(GO_PACKAGE_DIR)/*.go))
```

### Issue 4: Return Type Parsing
**Problem:** Return type includes "in w0" suffix from comments.

**Location:** `generate-linknames.go:parseFunctionSignatures` (Lines 188-197)

**Fix:** Strip suffix using regex (Line 195):
```go
returnType = regexp.MustCompile(`\s+in\s+[wx]\d+$`).ReplaceAllString(returnType, "")
```

## Testing Commands

### Test generate-linknames.go
```bash
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-linknames.go src/mazboot/asm/aarch64 | head -30
```

### Test generate-main-calls.go
```bash
cd /Users/iansmith/mazzy
GOTOOLCHAIN=local go run src/mazboot/tools/generate-main-calls.go src/mazboot/asm/aarch64 src/mazboot/go/mazboot
```

### Test Full Generation
```bash
cd src/mazboot
make generateasm2go
```

### Test Individual Files
```bash
cd src/mazboot
make go/mazboot/linknames.go
make go/mazboot/main.go
```

## Implementation Checklist

When implementing or modifying these tools:

- [ ] Update `excluded` map in `generateLinknames` if new symbols need filtering
- [ ] Update `knownSignatures` in `generateMainCalls` if new function signatures are known
- [ ] Update `specialCases` in `convertToGoName` if new name conversions needed
- [ ] Update `typeMap` in `convertCTypeToGo` if new C types encountered
- [ ] Update Makefile dependencies if new source files affect generation
- [ ] Test with `make generateasm2go` after changes
- [ ] Verify generated code compiles: `make ../../build/mazboot/kernel_go_qemu.o`
- [ ] Check for duplicate declarations (functions already in other Go files)



