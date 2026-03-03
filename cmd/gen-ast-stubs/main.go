// tools/gen-ast-stubs.go
//
// Generates thin client stubs by parsing Go standard library source files
// with go/ast and replacing function bodies with minimal `for {}` loops.
// The resulting overlay allows compilation of tiny userspace binaries.
//
// Supports multiple packages (e.g., runtime and syscall) via the -packages flag.
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
	"os/exec"
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

// packageInfo holds source and output directories for one package being processed.
type packageInfo struct {
	name      string // e.g., "runtime", "syscall"
	sourceDir string // e.g., $GOROOT/src/runtime
	outputDir string // e.g., build/thin-overlay/runtime
}

var (
	flagRuntimeDir    = flag.String("runtime", "", "Path to Go runtime source directory ($GOROOT/src/runtime)")
	flagOutputDir     = flag.String("output", "", "Output directory for stub files")
	flagOverlayOut    = flag.String("overlay", "", "Output path for overlay JSON")
	flagManifest      = flag.String("manifest", "", "Output path for stub manifest")
	flagVerbose       = flag.Bool("v", false, "Verbose output")
	flagGo            = flag.String("go", "", "Path to Go binary (used with -runtime-from-go)")
	flagRuntimeFromGo = flag.Bool("runtime-from-go", false, "Discover runtime dir via 'go env GOROOT' using the -go binary")
	flagPackages      = flag.String("packages", "runtime", "Comma-separated list of packages to stub (e.g., runtime,syscall)")
)

func main() {
	flag.Parse()

	// Discover GOROOT if -runtime-from-go is set
	var goroot string
	if *flagRuntimeFromGo {
		if *flagGo == "" {
			fmt.Fprintf(os.Stderr, "Error: -runtime-from-go requires -go=<path-to-go-binary>\n")
			os.Exit(1)
		}
		out, err := exec.Command(*flagGo, "env", "GOROOT").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error running '%s env GOROOT': %v\n", *flagGo, err)
			os.Exit(1)
		}
		goroot = strings.TrimSpace(string(out))

		// For backward compatibility: set -runtime to GOROOT/src/runtime
		// if not explicitly provided
		if *flagRuntimeDir == "" {
			runtimeDir := filepath.Join(goroot, "src", "runtime")
			flagRuntimeDir = &runtimeDir
		}
	}

	if *flagOutputDir == "" || *flagOverlayOut == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -runtime=<dir> -output=<dir> -overlay=<file.json> [-manifest=<file>] [-v]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "   or: %s -go=<path> -runtime-from-go -output=<dir> -overlay=<file.json> [-packages=runtime,syscall] [-manifest=<file>] [-v]\n", os.Args[0])
		os.Exit(1)
	}

	// Build the list of packages to process
	packages := buildPackageList(goroot)

	if len(packages) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no packages to process (set -runtime or -packages with -runtime-from-go)\n")
		os.Exit(1)
	}

	// Process each package
	var allStubs []FuncStub
	totalFiles := 0
	// overlayReplace accumulates all overlay mappings across packages
	overlayReplace := make(map[string]string)

	for _, pkg := range packages {
		if *flagVerbose {
			fmt.Printf("Processing package: %s (source: %s)\n", pkg.name, pkg.sourceDir)
		}

		stubs, fileCount, err := processPackage(pkg, overlayReplace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing package %s: %v\n", pkg.name, err)
			os.Exit(1)
		}

		allStubs = append(allStubs, stubs...)
		totalFiles += fileCount
	}

	// Generate combined overlay JSON
	if err := writeOverlayJSON(*flagOverlayOut, overlayReplace); err != nil {
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

	fmt.Printf("Generated %d stub files with %d stubbed functions across %d packages\n",
		totalFiles, len(allStubs), len(packages))
	fmt.Printf("Overlay written to: %s\n", *flagOverlayOut)
}

// buildPackageList constructs the list of packages to process from flags.
func buildPackageList(goroot string) []packageInfo {
	pkgNames := strings.Split(*flagPackages, ",")

	var packages []packageInfo
	for _, name := range pkgNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		var srcDir, outDir string
		if name == "runtime" && *flagRuntimeDir != "" {
			// Use explicit -runtime flag for backward compatibility
			srcDir = *flagRuntimeDir
			outDir = filepath.Join(*flagOutputDir, "runtime")
		} else if goroot != "" {
			// Derive source dir from GOROOT
			srcDir = filepath.Join(goroot, "src", name)
			outDir = filepath.Join(*flagOutputDir, name)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: cannot locate source for package %q (need -runtime-from-go)\n", name)
			continue
		}

		packages = append(packages, packageInfo{
			name:      name,
			sourceDir: srcDir,
			outputDir: outDir,
		})
	}

	return packages
}

// processPackage processes a single package: finds files, transforms them,
// writes stubs, and adds overlay entries. Returns stubs and file count.
func processPackage(pkg packageInfo, overlayReplace map[string]string) ([]FuncStub, int, error) {
	// Find source files
	files, err := findSourceFiles(pkg.sourceDir)
	if err != nil {
		return nil, 0, fmt.Errorf("finding source files: %w", err)
	}

	if *flagVerbose {
		fmt.Printf("  Found %d source files in %s\n", len(files), pkg.name)
	}

	var stubs []FuncStub
	fileCount := 0

	for _, path := range files {
		// Skip assembly files - they can't be parsed by go/parser
		if strings.HasSuffix(path, ".s") {
			if *flagVerbose {
				rel, _ := filepath.Rel(pkg.sourceDir, path)
				fmt.Printf("  %s/%s: skipped (assembly)\n", pkg.name, rel)
			}
			continue
		}

		result, err := transformFile(path)
		if err != nil {
			return nil, 0, fmt.Errorf("transforming %s: %w", path, err)
		}

		// Calculate output path
		rel, err := filepath.Rel(pkg.sourceDir, path)
		if err != nil {
			return nil, 0, fmt.Errorf("computing relative path for %s: %w", path, err)
		}
		outputPath := filepath.Join(pkg.outputDir, rel)

		// Write transformed file
		if err := writeStubFile(result, outputPath); err != nil {
			return nil, 0, fmt.Errorf("writing %s: %w", outputPath, err)
		}

		// Add overlay mapping: original → stub
		absOrig, _ := filepath.Abs(path)
		absStub, _ := filepath.Abs(outputPath)
		overlayReplace[absOrig] = absStub

		stubs = append(stubs, result.StubFuncs...)
		fileCount++

		if *flagVerbose {
			fmt.Printf("  %s/%s: %d stubs\n", pkg.name, rel, len(result.StubFuncs))
		}
	}

	fmt.Printf("  %s: %d files, %d stubbed functions\n", pkg.name, fileCount, len(stubs))
	return stubs, fileCount, nil
}

// findSourceFiles returns all .go and .s files for the target platform
// Platform is determined by build tags in the filename
func findSourceFiles(runtimeDir string) ([]string, error) {
	var files []string

	// Determine target platform from flags
	goos := os.Getenv("TARGET_GOOS")
	if goos == "" {
		goos = "linux" // Default
	}
	goarch := os.Getenv("TARGET_GOARCH")
	if goarch == "" {
		goarch = "arm64" // Default
	}

	err := filepath.Walk(runtimeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		// Only .go and .s files
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".s") {
			return nil
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		base := filepath.Base(path)

		// Platform filtering based on TARGET_GOOS and TARGET_GOARCH
		if goos == "windows" && goarch == "amd64" {
			// For windows/amd64: skip non-windows, non-amd64 files
			// Keep: *_windows.go, *_windows_amd64.go, *_amd64.go, generic files
			// Skip: *_linux.go, *_darwin.go, *_arm64.go, *_386.go, etc.

			// Skip other OS files
			if containsAny(base, "_linux", "_darwin", "_plan9", "_js", "_wasm",
				"_freebsd", "_openbsd", "_netbsd", "_dragonfly", "_solaris", "_aix") {
				return nil
			}

			// Skip wrong architectures (keep amd64, skip others)
			if containsAny(base, "_386", "_arm64", "_arm.", "_mips", "_ppc64",
				"_riscv64", "_s390x", "_loong64") {
				return nil
			}
		} else {
			// For linux/arm64: original behavior
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

// writeOverlayJSON writes the combined overlay JSON file from accumulated replacements.
func writeOverlayJSON(overlayPath string, replace map[string]string) error {
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: replace,
	}

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
