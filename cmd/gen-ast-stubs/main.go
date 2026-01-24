// tools/gen-ast-stubs.go
//
// Generates thin client stubs by parsing Go runtime source files with go/ast
// and replacing function bodies with minimal `for {}` loops.
// The resulting overlay allows compilation of tiny userspace binaries.
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

// FuncStub holds information about a stubbed function
type FuncStub struct {
	Package  string // "runtime" or "runtime/internal/atomic" etc.
	Name     string // Function name
	Receiver string // Receiver type if method, empty otherwise
	AsmName  string // Assembly symbol name (runtime·mallocgc)
}

// TransformResult holds the result of transforming a file
type TransformResult struct {
	File        *ast.File
	FileSet     *token.FileSet
	StubFuncs   []FuncStub      // Functions that were stubbed
	UsedImports map[string]bool // Imports actually used outside function bodies
}

var (
	flagRuntimeDir = flag.String("runtime", "", "Path to Go runtime source directory ($GOROOT/src/runtime)")
	flagOutputDir  = flag.String("output", "", "Output directory for stub files")
	flagOverlayOut = flag.String("overlay", "", "Output path for overlay JSON")
	flagManifest   = flag.String("manifest", "", "Output path for stub manifest")
	flagVerbose    = flag.Bool("v", false, "Verbose output")
)

func main() {
	flag.Parse()

	if *flagRuntimeDir == "" || *flagOutputDir == "" || *flagOverlayOut == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -runtime=<dir> -output=<dir> -overlay=<file.json> [-manifest=<file>] [-v]\n", os.Args[0])
		os.Exit(1)
	}

	// Find all runtime source files
	files, err := findSourceFiles(*flagRuntimeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding source files: %v\n", err)
		os.Exit(1)
	}

	if *flagVerbose {
		fmt.Printf("Found %d source files\n", len(files))
	}

	// Process each file
	var allStubs []FuncStub
	var processedFiles []string

	for _, path := range files {
		result, err := transformFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error transforming %s: %v\n", path, err)
			os.Exit(1)
		}

		// Calculate output path
		rel, err := filepath.Rel(*flagRuntimeDir, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error computing relative path for %s: %v\n", path, err)
			os.Exit(1)
		}
		outputPath := filepath.Join(*flagOutputDir, rel)

		// Write transformed file
		if err := writeStubFile(result, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputPath, err)
			os.Exit(1)
		}

		allStubs = append(allStubs, result.StubFuncs...)
		processedFiles = append(processedFiles, path)

		if *flagVerbose {
			fmt.Printf("  %s: %d stubs\n", rel, len(result.StubFuncs))
		}
	}

	// Generate overlay JSON
	if err := generateOverlay(*flagRuntimeDir, *flagOutputDir, *flagOverlayOut, processedFiles); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating overlay: %v\n", err)
		os.Exit(1)
	}

	// Generate manifest if requested
	if *flagManifest != "" {
		if err := generateManifest(allStubs, *flagManifest); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating manifest: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Generated %d stub files with %d stubbed functions\n", len(processedFiles), len(allStubs))
	fmt.Printf("Overlay written to: %s\n", *flagOverlayOut)
}

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

		// Skip wrong platforms (keep linux, skip others)
		if containsAny(base, "_windows", "_darwin", "_plan9", "_js", "_wasm",
			"_freebsd", "_openbsd", "_netbsd", "_dragonfly", "_solaris", "_aix") {
			return nil
		}

		// Skip wrong architectures (keep arm64, skip others)
		if containsAny(base, "_amd64", "_386", "_arm.", "_mips", "_ppc64",
			"_riscv64", "_s390x", "_wasm", "_loong64") {
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

// transformFile parses and transforms a single file
func transformFile(path string) (*TransformResult, error) {
	fset := token.NewFileSet()

	// Parse with comments to preserve directives
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Create a comment map to track comment associations
	cmap := ast.NewCommentMap(fset, file, file.Comments)

	result := &TransformResult{
		File:        file,
		FileSet:     fset,
		UsedImports: make(map[string]bool),
	}

	// First pass: collect imports used outside function bodies
	collectNonFuncImports(file, result.UsedImports)

	// Transform functions - replace bodies with minimal stubs
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		if shouldStub(fn) {
			stub := createStub(fn, file.Name.Name)
			result.StubFuncs = append(result.StubFuncs, stub)

			// Remove comments associated with the old body
			// This prevents directives from getting misplaced
			if fn.Body != nil {
				delete(cmap, fn.Body)
				for _, stmt := range fn.Body.List {
					delete(cmap, stmt)
				}
			}

			// Replace body with minimal stub
			fn.Body = createMinimalBody(fset, fn)
		}
	}

	// Update file comments from the filtered comment map
	file.Comments = cmap.Comments()

	// Remove unused imports
	removeUnusedImports(file, result.UsedImports)

	return result, nil
}

// collectNonFuncImports finds imports used outside stubbed function bodies
// This includes: type/var/const declarations, function signatures, and
// bodies of functions that will NOT be stubbed (init, main, generic functions)
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
					// Also check type parameters for generic types
					if ts.TypeParams != nil {
						for _, field := range ts.TypeParams.List {
							if field.Type != nil {
								collectTypeImports(field.Type, used)
							}
						}
					}
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
		case *ast.FuncDecl:
			// Collect imports from function signatures
			if d.Recv != nil {
				for _, field := range d.Recv.List {
					collectTypeImports(field.Type, used)
				}
			}
			if d.Type.Params != nil {
				for _, field := range d.Type.Params.List {
					collectTypeImports(field.Type, used)
				}
			}
			if d.Type.Results != nil {
				for _, field := range d.Type.Results.List {
					collectTypeImports(field.Type, used)
				}
			}
			if d.Type.TypeParams != nil {
				for _, field := range d.Type.TypeParams.List {
					if field.Type != nil {
						collectTypeImports(field.Type, used)
					}
				}
			}

			// For functions that will NOT be stubbed, also collect imports from their bodies
			// These include: init, main, generic functions, and assembly-declared functions
			if !shouldStub(d) && d.Body != nil {
				collectBodyImports(d.Body, used)
			}
		}
	}
}

// collectBodyImports finds package references in a function body
func collectBodyImports(body *ast.BlockStmt, used map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})
}

// collectTypeImports finds package references in a type expression
func collectTypeImports(expr ast.Expr, used map[string]bool) {
	if expr == nil {
		return
	}
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
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})
}

// removeUnusedImports removes imports not in the used map
func removeUnusedImports(file *ast.File, used map[string]bool) {
	// Always keep blank imports (they're used for side effects)
	used["_"] = true

	// Check if file has //go:linkname directives - these require unsafe import
	hasLinkname := false
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "go:linkname") {
				hasLinkname = true
				break
			}
		}
		if hasLinkname {
			break
		}
	}

	// Track if unsafe is genuinely used (not just for linkname)
	unsafeGenuinelyUsed := used["unsafe"]

	// If file has linkname, we need to keep unsafe import
	if hasLinkname {
		used["unsafe"] = true
	}

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

			// For unsafe imports in files with go:linkname where unsafe
			// is not genuinely used, convert to blank import
			if name == "unsafe" && hasLinkname && !unsafeGenuinelyUsed {
				// Convert to blank import: import _ "unsafe"
				imp.Name = &ast.Ident{Name: "_"}
				newSpecs = append(newSpecs, spec)
				continue
			}

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

	// Also update file.Imports
	var newImports []*ast.ImportSpec
	for _, imp := range file.Imports {
		name := importLocalName(imp)
		if used[name] || (name == "_" && strings.Contains(imp.Path.Value, "unsafe")) {
			newImports = append(newImports, imp)
		}
	}
	file.Imports = newImports
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

// createStub creates a FuncStub from a function declaration
func createStub(fn *ast.FuncDecl, pkg string) FuncStub {
	stub := FuncStub{
		Package: pkg,
		Name:    fn.Name.Name,
	}

	// Get receiver type if it's a method
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		stub.Receiver = typeString(fn.Recv.List[0].Type)
	}

	// Build assembly name (runtime·funcName or runtime·(*Type)·Method)
	if stub.Receiver != "" {
		stub.AsmName = fmt.Sprintf("%s·%s·%s", pkg, stub.Receiver, stub.Name)
	} else {
		stub.AsmName = fmt.Sprintf("%s·%s", pkg, stub.Name)
	}

	return stub
}

// typeString returns a string representation of a type expression
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "(*" + typeString(t.X) + ")"
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	default:
		return "?"
	}
}

// createMinimalBody creates a function body that compiles but is minimal
// The body is: for { } (infinite loop that will be patched)
// This avoids import issues and is easy to identify for patching
func createMinimalBody(fset *token.FileSet, fn *ast.FuncDecl) *ast.BlockStmt {
	// Get position info from the original body to help with comment placement
	var pos token.Pos
	if fn.Body != nil {
		pos = fn.Body.Lbrace
	}

	// Create: for { }
	// This compiles, doesn't need imports, and has predictable assembly
	forStmt := &ast.ForStmt{
		For: pos,
		Body: &ast.BlockStmt{
			Lbrace: pos,
			List:   []ast.Stmt{},
			Rbrace: pos,
		},
	}

	return &ast.BlockStmt{
		Lbrace: pos,
		List:   []ast.Stmt{forStmt},
		Rbrace: pos,
	}
}

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
