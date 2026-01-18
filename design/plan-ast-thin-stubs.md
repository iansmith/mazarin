# Plan: AST-Based Thin Client Stubs

## Overview

This document details how to implement thin client stubs using Go's `go/ast` package to parse and transform runtime source files. This approach replaces the failed ELF+DWARF approach.

**Goal**: Reduce thin client binary size from ~2MB to ~100KB by replacing Go runtime functions with minimal stubs that trampoline to priest's runtime.

## Background: Why the ELF+DWARF Approach Failed

The ELF+DWARF approach attempted to:
1. Parse priest.elf to extract runtime function addresses
2. Generate assembly stubs that jump to those addresses
3. Patch userspace binaries post-compilation to redirect runtime functions

**Problems**:
1. Functions need minimum 12 bytes for trampolines, but some runtime functions are smaller
2. Post-compilation patching is fragile and requires complex ELF manipulation
3. The approach still compiled the full runtime, then tried to redirect it

## The AST-Based Approach

**Key Insight**: Instead of post-processing compiled binaries, intercept at compile time by providing stub source files via Go's `-overlay` mechanism.

### High-Level Flow

```
COMPILE TIME:
  Go Source Files
        ↓
  -overlay=thin-overlay.json
        ↓
  [Replace runtime/*.go with stub versions]
        ↓
  Go Compiler
        ↓
  thin-client.elf (~100KB, has stub functions)

LOAD TIME (by priest):
  priest.elf (full runtime)
  thin-client.elf (stub runtime)
        ↓
  patchruntime tool
        ↓
  Patch stub entry points → trampoline to priest's functions
        ↓
  thin-client.elf (ready to run)
```

## Files to Remove (ELF+DWARF Approach)

The following files from the ELF+DWARF approach must be removed:

| File | Reason |
|------|--------|
| `tools/gen-runtime-stubs.go` | Generates stubs from ELF symbols |
| `tools/patchruntime.go` | Post-compilation ELF patching |
| `tools/patchpriest.go` | Patches syscall handler pointer |
| `src/mazarin/overlay/userspace/runtime/table.go` | RuntimeTable for indirect dispatch |

**Keep**:
- `src/mazarin/overlay/userspace/syscall_linux.go` - Still needed for syscall routing

## Files to Create (AST Approach)

| File | Purpose |
|------|--------|
| `tools/gen-ast-stubs.go` | Main tool: parse runtime, generate stubs |
| `tools/patch-thin-client.go` | Simple patcher for stub entry points |
| `build/thin-overlay/*.go` | Generated stub files (at build time) |
| `build/thin-overlay.json` | Generated overlay config (at build time) |

## Detailed Implementation

### Step 1: Create gen-ast-stubs.go Structure

```go
// tools/gen-ast-stubs.go
package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "go/ast"
    "go/parser"
    "go/printer"
    "go/token"
    "os"
    "path/filepath"
    "sort"
    "strings"
)

// Config holds generation configuration
type Config struct {
    RuntimeDir string // $GOROOT/src/runtime
    OutputDir  string // build/thin-overlay
    OverlayOut string // build/thin-overlay.json
    Verbose    bool
}

// FuncStub holds information about a stubbed function
type FuncStub struct {
    Package  string // "runtime" or "runtime/internal/atomic" etc.
    Name     string // Function name
    Receiver string // Receiver type if method, empty otherwise
    AsmName  string // Assembly symbol name (runtime·mallocgc)
}
```

### Step 2: Find Runtime Source Files

```go
// findSourceFiles returns all .go files for linux/arm64
func findSourceFiles(runtimeDir string) ([]string, error) {
    var files []string

    err := filepath.Walk(runtimeDir, func(path string, info os.FileInfo, err error) error {
        if err != nil || info.IsDir() {
            return err
        }

        // Only .go files
        if !strings.HasSuffix(path, ".go") {
            return nil
        }

        // Skip test files
        if strings.HasSuffix(path, "_test.go") {
            return nil
        }

        base := filepath.Base(path)

        // Skip wrong platforms
        if containsAny(base, "_windows", "_darwin", "_plan9", "_js", "_wasm",
            "_freebsd", "_openbsd", "_netbsd", "_dragonfly", "_solaris", "_aix") {
            return nil
        }

        // Skip wrong architectures
        if containsAny(base, "_amd64", "_386", "_arm.", "_mips", "_ppc64",
            "_riscv64", "_s390x", "_wasm") {
            return nil
        }

        files = append(files, path)
        return nil
    })

    return files, err
}

func containsAny(s string, patterns ...string) bool {
    for _, p := range patterns {
        if strings.Contains(s, p) {
            return true
        }
    }
    return false
}
```

### Step 3: Parse and Transform Files

This is the critical step. For each source file:

1. Parse with `go/parser`
2. Identify functions that should be stubbed
3. Create stub bodies (NOT nil - that causes import issues)
4. Track imports that are actually used

```go
// TransformResult holds the result of transforming a file
type TransformResult struct {
    File        *ast.File
    FileSet     *token.FileSet
    StubFuncs   []FuncStub     // Functions that were stubbed
    UsedImports map[string]bool // Imports actually used (not just in func bodies)
}

// transformFile parses and transforms a single file
func transformFile(path string) (*TransformResult, error) {
    fset := token.NewFileSet()

    // Parse with comments to preserve directives
    file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
    if err != nil {
        return nil, fmt.Errorf("parse %s: %w", path, err)
    }

    result := &TransformResult{
        File:        file,
        FileSet:     fset,
        UsedImports: make(map[string]bool),
    }

    // First pass: collect imports used in type/var/const declarations
    collectNonFuncImports(file, result.UsedImports)

    // Second pass: transform functions
    for _, decl := range file.Decls {
        fn, ok := decl.(*ast.FuncDecl)
        if !ok {
            continue
        }

        if shouldStub(fn) {
            stub := createStub(fn, file.Name.Name)
            result.StubFuncs = append(result.StubFuncs, stub)

            // Replace body with minimal stub
            fn.Body = createMinimalBody(fn)
        }
    }

    // Third pass: remove truly unused imports
    removeUnusedImports(file, result.UsedImports)

    return result, nil
}
```

### Step 4: Determine Which Functions to Stub

```go
// shouldStub returns true if this function should be stubbed
func shouldStub(fn *ast.FuncDecl) bool {
    name := fn.Name.Name

    // NEVER stub init functions
    if name == "init" {
        return false
    }

    // NEVER stub main function
    if name == "main" {
        return false
    }

    // NEVER stub generic functions (have type parameters)
    if fn.Type.TypeParams != nil && fn.Type.TypeParams.NumFields() > 0 {
        return false
    }

    // NEVER stub assembly-implemented functions (body is nil in source)
    if fn.Body == nil {
        return false
    }

    // Stub everything else
    return true
}
```

### Step 5: Create Minimal Function Bodies

**CRITICAL**: We cannot use `fn.Body = nil` because Go expects assembly implementation.
Instead, create a minimal body that compiles but will never execute.

```go
// createMinimalBody creates a function body that compiles but is minimal
// The body is: for { } (infinite loop that will be patched)
// This avoids import issues and is easy to identify for patching
func createMinimalBody(fn *ast.FuncDecl) *ast.BlockStmt {
    // Create: for { }
    // This compiles, doesn't need imports, and has predictable assembly
    forStmt := &ast.ForStmt{
        Body: &ast.BlockStmt{
            List: []ast.Stmt{},
        },
    }

    return &ast.BlockStmt{
        List: []ast.Stmt{forStmt},
    }
}
```

**Alternative minimal bodies** (listed for completeness):

```go
// Option A: Empty infinite loop (chosen - simplest)
// for { }
// Assembly: B .-0 (branch to self)

// Option B: panic call (requires unsafe import handling)
// panic("stub")

// Option C: Return zero values (requires type analysis)
// return 0, nil, ...
```

### Step 6: Collect Non-Function Import Usage

Imports are tricky. We must:
1. Keep imports used in type definitions, var/const declarations
2. Remove imports ONLY used in function bodies (which we're replacing)

```go
// collectNonFuncImports finds imports used outside function bodies
func collectNonFuncImports(file *ast.File, used map[string]bool) {
    for _, decl := range file.Decls {
        switch d := decl.(type) {
        case *ast.GenDecl:
            switch d.Tok {
            case token.TYPE:
                // Type definitions
                for _, spec := range d.Specs {
                    ts := spec.(*ast.TypeSpec)
                    collectTypeImports(ts.Type, used)
                }
            case token.VAR, token.CONST:
                // Variable/constant declarations
                for _, spec := range d.Specs {
                    vs := spec.(*ast.ValueSpec)
                    if vs.Type != nil {
                        collectTypeImports(vs.Type, used)
                    }
                    for _, v := range vs.Values {
                        collectExprImports(v, used)
                    }
                }
            }
        }
    }
}

// collectTypeImports finds package references in a type expression
func collectTypeImports(expr ast.Expr, used map[string]bool) {
    ast.Inspect(expr, func(n ast.Node) bool {
        if sel, ok := n.(*ast.SelectorExpr); ok {
            if ident, ok := sel.X.(*ast.Ident); ok {
                used[ident.Name] = true
            }
        }
        return true
    })
}

// collectExprImports finds package references in an expression
func collectExprImports(expr ast.Expr, used map[string]bool) {
    ast.Inspect(expr, func(n ast.Node) bool {
        if sel, ok := n.(*ast.SelectorExpr); ok {
            if ident, ok := sel.X.(*ast.Ident); ok {
                used[ident.Name] = true
            }
        }
        return true
    })
}
```

### Step 7: Remove Unused Imports

```go
// removeUnusedImports removes imports not in the used map
func removeUnusedImports(file *ast.File, used map[string]bool) {
    // Always keep these
    used["unsafe"] = true // Often needed by compiler
    used["_"] = true      // Blank imports

    var newDecls []ast.Decl

    for _, decl := range file.Decls {
        gen, ok := decl.(*ast.GenDecl)
        if !ok || gen.Tok != token.IMPORT {
            newDecls = append(newDecls, decl)
            continue
        }

        var newSpecs []ast.Spec
        for _, spec := range gen.Specs {
            imp := spec.(*ast.ImportSpec)

            // Get local name (alias or last path component)
            name := importLocalName(imp)

            if used[name] {
                newSpecs = append(newSpecs, spec)
            }
        }

        if len(newSpecs) > 0 {
            gen.Specs = newSpecs
            newDecls = append(newDecls, gen)
        }
    }

    file.Decls = newDecls
}

// importLocalName returns the local name for an import
func importLocalName(imp *ast.ImportSpec) string {
    if imp.Name != nil {
        return imp.Name.Name
    }
    // Extract last component from path
    path := strings.Trim(imp.Path.Value, `"`)
    if idx := strings.LastIndex(path, "/"); idx >= 0 {
        return path[idx+1:]
    }
    return path
}
```

### Step 8: Write Transformed Files

```go
// writeStubFile writes a transformed AST to a file
func writeStubFile(result *TransformResult, outputPath string) error {
    // Create directory
    if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
        return err
    }

    // Create file
    f, err := os.Create(outputPath)
    if err != nil {
        return err
    }
    defer f.Close()

    // Write with proper formatting
    cfg := printer.Config{
        Mode:     printer.UseSpaces | printer.TabIndent,
        Tabwidth: 8,
    }

    return cfg.Fprint(f, result.FileSet, result.File)
}
```

### Step 9: Generate Overlay JSON

```go
// generateOverlay creates the overlay JSON file
func generateOverlay(runtimeDir, outputDir, overlayPath string, files []string) error {
    overlay := struct {
        Replace map[string]string `json:"Replace"`
    }{
        Replace: make(map[string]string),
    }

    for _, origPath := range files {
        // Calculate relative path from runtime dir
        rel, _ := filepath.Rel(runtimeDir, origPath)

        // Output path
        stubPath := filepath.Join(outputDir, rel)

        // Absolute paths for overlay
        absOrig, _ := filepath.Abs(origPath)
        absStub, _ := filepath.Abs(stubPath)

        overlay.Replace[absOrig] = absStub
    }

    // Write JSON
    f, err := os.Create(overlayPath)
    if err != nil {
        return err
    }
    defer f.Close()

    enc := json.NewEncoder(f)
    enc.SetIndent("", "  ")
    return enc.Encode(overlay)
}
```

### Step 10: Generate Manifest for Patching

Generate a manifest file listing all stubbed functions for the patcher:

```go
// generateManifest creates a manifest of all stubbed functions
func generateManifest(stubs []FuncStub, outputPath string) error {
    // Sort by assembly name for deterministic output
    sort.Slice(stubs, func(i, j int) bool {
        return stubs[i].AsmName < stubs[j].AsmName
    })

    f, err := os.Create(outputPath)
    if err != nil {
        return err
    }
    defer f.Close()

    fmt.Fprintf(f, "# Thin client stub manifest\n")
    fmt.Fprintf(f, "# Generated by gen-ast-stubs\n")
    fmt.Fprintf(f, "# Total: %d functions\n", len(stubs))
    fmt.Fprintf(f, "#\n")
    fmt.Fprintf(f, "# Format: asm_name\n\n")

    for _, stub := range stubs {
        fmt.Fprintln(f, stub.AsmName)
    }

    return nil
}
```

### Step 11: Create Simple Patcher (patch-thin-client.go)

The patcher is much simpler than the ELF approach:

```go
// tools/patch-thin-client.go
package main

import (
    "debug/elf"
    "encoding/binary"
    "fmt"
    "os"
)

// Each stub function compiles to: B .-0 (branch to self)
// We replace with: B <priest_func>

func main() {
    if len(os.Args) != 4 {
        fmt.Fprintf(os.Stderr, "Usage: %s <priest.elf> <client.elf> <output.elf>\n", os.Args[0])
        os.Exit(1)
    }

    // Read priest's runtime function addresses
    priestFuncs := readRuntimeFunctions(os.Args[1])

    // Read client's stub function addresses
    clientStubs := readStubFunctions(os.Args[2])

    // Match and patch
    copyAndPatch(os.Args[2], os.Args[3], priestFuncs, clientStubs)
}

// generateTrampoline creates a 12-byte trampoline to target
// MOVZ X16, #(addr & 0xFFFF)
// MOVK X16, #((addr >> 16) & 0xFFFF), LSL #16
// BR   X16
func generateTrampoline(target uint64) []byte {
    code := make([]byte, 12)

    // MOVZ X16, #imm16
    movz := uint32(0xD2800010) | (uint32(target&0xFFFF) << 5)
    binary.LittleEndian.PutUint32(code[0:4], movz)

    // MOVK X16, #imm16, LSL #16
    movk := uint32(0xF2A00010) | (uint32((target>>16)&0xFFFF) << 5)
    binary.LittleEndian.PutUint32(code[4:8], movk)

    // BR X16
    binary.LittleEndian.PutUint32(code[8:12], 0xD61F0200)

    return code
}
```

### Step 12: Update Makefile

```makefile
# Thin client overlay
THIN_OVERLAY_DIR = $(BUILD_DIR)/thin-overlay
THIN_OVERLAY_JSON = $(BUILD_DIR)/thin-overlay.json
THIN_MANIFEST = $(BUILD_DIR)/thin-stubs.manifest

# Build tools
TOOL_GEN_AST_STUBS = $(TOOLS_BIN_DIR)/gen-ast-stubs
TOOL_PATCH_THIN = $(TOOLS_BIN_DIR)/patch-thin-client

# Build gen-ast-stubs
$(TOOL_GEN_AST_STUBS): tools/gen-ast-stubs.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# Build patch-thin-client
$(TOOL_PATCH_THIN): tools/patch-thin-client.go | $(TOOLS_BIN_DIR)
	@echo "Building $@..."
	@GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -o $@ $<

# Generate thin overlay
$(THIN_OVERLAY_JSON): $(TOOL_GEN_AST_STUBS) | $(BUILD_DIR)
	@echo "Generating thin client overlay..."
	@GOROOT=$$(GOTOOLCHAIN=local $(GO) env GOROOT) && \
		$(TOOL_GEN_AST_STUBS) \
			-runtime=$$GOROOT/src/runtime \
			-output=$(THIN_OVERLAY_DIR) \
			-overlay=$(THIN_OVERLAY_JSON) \
			-manifest=$(THIN_MANIFEST)

# Build thin helloworld
$(HELLOWORLD_BINARY): $(HELLOWORLD_ALL_SRC) $(THIN_OVERLAY_JSON) $(PRIEST_BINARY) $(TOOL_PATCH_THIN) | $(BUILD_DIR)
	@echo "Building helloworld (thin client)..."
	@cd $(HELLOWORLD_SRC) && \
		CGO_ENABLED=0 GOTOOLCHAIN=local GOARCH=$(GOARCH) GOOS=$(GOOS) \
		$(GO) build -overlay=$(abspath $(THIN_OVERLAY_JSON)) \
			$(GCFLAGS) -o $(abspath $@) .
	@echo "Pre-patch size: $$(ls -lh $@ | awk '{print $$5}')"
	@echo "Patching stubs to trampoline to priest..."
	@$(TOOL_PATCH_THIN) $(PRIEST_BINARY) $@ $@
	@echo "Post-patch size: $$(ls -lh $@ | awk '{print $$5}')"
```

## Testing Plan

### Test 1: Verify Stub Generation

```bash
# Generate overlay
GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make build/thin-overlay.json

# Check generated files
ls -la build/thin-overlay/
head -50 build/thin-overlay/malloc.go
cat build/thin-overlay.json | head -30
```

### Test 2: Verify Compilation

```bash
# Build thin client
GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make helloworld

# Check size
ls -la build/helloworld.elf
# Expected: ~100-200KB instead of ~2MB
```

### Test 3: Verify Patching

```bash
# Disassemble a stubbed function before patching
bin/target-objdump -d build/helloworld.elf.unpached | grep -A5 'runtime.mallocgc'
# Expected: B .-0 (branch to self)

# After patching
bin/target-objdump -d build/helloworld.elf | grep -A5 'runtime.mallocgc'
# Expected: MOVZ/MOVK/BR trampoline
```

### Test 4: Run in QEMU

```bash
QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 build/tools/run 5
# Expected: helloworld runs and calls GetTime successfully
```

## Implementation Checklist

### Phase 1: Cleanup (Remove ELF+DWARF Approach)

- [ ] Remove `tools/gen-runtime-stubs.go`
- [ ] Remove `tools/patchruntime.go`
- [ ] Remove `tools/patchpriest.go`
- [ ] Remove `src/mazarin/overlay/userspace/runtime/table.go`
- [ ] Remove `tools/gen-thin-stubs.go` (incomplete AST attempt)
- [ ] Update Makefile to remove references to removed tools
- [ ] Commit cleanup

### Phase 2: Core Tool Implementation

- [ ] Create `tools/gen-ast-stubs.go` with basic structure
- [ ] Implement `findSourceFiles()`
- [ ] Implement `transformFile()` with proper import handling
- [ ] Implement `collectNonFuncImports()`
- [ ] Implement `removeUnusedImports()`
- [ ] Implement `writeStubFile()`
- [ ] Implement `generateOverlay()`
- [ ] Implement `generateManifest()`
- [ ] Test: Verify overlay generation compiles

### Phase 3: Patcher Implementation

- [ ] Create `tools/patch-thin-client.go`
- [ ] Implement symbol reading from priest.elf
- [ ] Implement symbol reading from client.elf
- [ ] Implement trampoline generation
- [ ] Implement patching
- [ ] Test: Verify patching works

### Phase 4: Integration

- [ ] Update Makefile with new targets
- [ ] Test full build pipeline
- [ ] Verify binary size reduction
- [ ] Test execution in QEMU
- [ ] Document any issues or edge cases

## Troubleshooting Guide

### Issue: "imported and not used" errors

**Cause**: Import is only used in function bodies, not type/var/const decls.
**Fix**: The `collectNonFuncImports()` function should identify which imports are needed. Debug by adding verbose output to see what's being tracked.

### Issue: "undefined: X" errors

**Cause**: Import was removed that IS needed.
**Fix**: The import analysis missed a usage. Add the pattern to `collectNonFuncImports()`.

### Issue: "generic function is missing function body"

**Cause**: Generic functions (with type parameters) were stubbed.
**Fix**: The `shouldStub()` function should skip these. Verify the check.

### Issue: Binary size still large (~1MB+)

**Cause**: Not all functions being stubbed, or overlay not being used.
**Fix**:
1. Verify overlay JSON has correct absolute paths
2. Verify `-overlay` flag is being passed to go build
3. Check that `for {}` bodies are generating minimal code

### Issue: Crashes at runtime

**Cause**: A function that must NOT be stubbed was stubbed.
**Fix**: Add the function to the exclusion list in `shouldStub()`. Common culprits:
- `init` functions
- Assembly-implemented functions (body is nil in source)
- Functions with `//go:linkname` that are called from assembly

## Success Criteria

1. **Compilation succeeds** - `make helloworld` completes without errors
2. **Size reduced by >80%** - From ~2MB to <400KB
3. **Runtime works** - Helloworld runs and GetTime syscall succeeds
4. **No crashes** - Program completes normally
5. **Clean code** - No warnings, no hacks, maintainable

## Notes for Implementer

1. **Start with cleanup** - Remove the old approach first to avoid confusion
2. **Test incrementally** - Test each phase before moving to the next
3. **Keep verbose output** - Print what's being processed for debugging
4. **Preserve directives** - `//go:nosplit`, `//go:linkname` etc. must be preserved
5. **Absolute paths** - Overlay JSON must use absolute paths
6. **Build in project root** - The `-overlay` path must work from any `go build` location
