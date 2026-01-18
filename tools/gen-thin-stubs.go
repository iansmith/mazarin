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
	"strings"
)

// FuncInfo contains information about a function that needs an assembly stub
type FuncInfo struct {
	Name       string // e.g., "mallocgc"
	Receiver   string // e.g., "*mspan" or "" for plain functions
	Package    string // e.g., "runtime"
	FileName   string // Original file name for debugging
}

var (
	runtimeDir  = flag.String("runtime", "", "Path to Go runtime source (e.g., $GOROOT/src/runtime)")
	outputDir   = flag.String("output", "", "Output directory for overlay files")
	overlayJSON = flag.String("overlay", "", "Path to output overlay JSON file")
	verbose     = flag.Bool("v", false, "Verbose output")
)

func main() {
	flag.Parse()

	// Validate flags
	if *runtimeDir == "" || *outputDir == "" || *overlayJSON == "" {
		fmt.Fprintf(os.Stderr, "Usage: gen-thin-stubs -runtime <path> -output <path> -overlay <path>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Step 1: Find all runtime .go files
	files, err := findRuntimeFiles(*runtimeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding runtime files: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("Found %d runtime source files\n", len(files))
	}

	// Step 2: Parse and transform each file
	var stubFunctions []FuncInfo
	overlayFiles := make(map[string]string) // original path -> overlay path

	for _, file := range files {
		if *verbose {
			fmt.Printf("Processing %s\n", file)
		}

		// Parse the file
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", file, err)
			continue
		}

		// Transform to declarations only
		funcs := transformToDeclarations(astFile, filepath.Base(file))
		stubFunctions = append(stubFunctions, funcs...)

		// Calculate output path
		rel, _ := filepath.Rel(*runtimeDir, file)
		outputPath := filepath.Join(*outputDir, "runtime", rel)

		// Write transformed file
		if err := writeOverlayGoFile(astFile, fset, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputPath, err)
			os.Exit(1)
		}

		// Use absolute path in overlay
		absOutputPath, _ := filepath.Abs(outputPath)
		overlayFiles[file] = absOutputPath
	}

	if *verbose {
		fmt.Printf("Generated %d stub functions\n", len(stubFunctions))
	}

	// Step 3: Generate assembly stub file
	asmFile := filepath.Join(*outputDir, "runtime", "mazzy_stubs_arm64.s")
	if err := generateAssemblyStubs(stubFunctions, asmFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating assembly stubs: %v\n", err)
		os.Exit(1)
	}

	// Add assembly file to overlay
	// The overlay maps a non-existent path in GOROOT to our generated file
	asmOverlayPath := filepath.Join(*runtimeDir, "mazzy_stubs_arm64.s")
	absAsmFile, _ := filepath.Abs(asmFile)
	overlayFiles[asmOverlayPath] = absAsmFile

	// Step 4: Generate overlay JSON
	if err := generateOverlayJSON(overlayFiles, *overlayJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating overlay JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully generated thin client overlay:\n")
	fmt.Printf("  %d source files\n", len(overlayFiles)-1)
	fmt.Printf("  %d stub functions\n", len(stubFunctions))
	fmt.Printf("  Assembly: %s\n", asmFile)
	fmt.Printf("  Overlay: %s\n", *overlayJSON)
}

// findRuntimeFiles walks the runtime directory and finds all relevant .go files
func findRuntimeFiles(runtimeDir string) ([]string, error) {
	var files []string

	err := filepath.Walk(runtimeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only include .go files
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		base := filepath.Base(path)

		// Skip files for other platforms
		skipPlatforms := []string{
			"_windows", "_darwin", "_plan9", "_js", "_wasm",
			"_freebsd", "_openbsd", "_netbsd", "_dragonfly", "_solaris", "_aix",
		}
		for _, platform := range skipPlatforms {
			if strings.Contains(base, platform) {
				return nil
			}
		}

		// Skip files for other architectures
		skipArchs := []string{
			"_amd64", "_386", "_arm", "_mips", "_ppc64", "_riscv64", "_s390x",
		}
		for _, arch := range skipArchs {
			if strings.Contains(base, arch) {
				return nil
			}
		}

		files = append(files, path)
		return nil
	})

	return files, err
}

// transformToDeclarations removes function bodies from the AST, leaving only declarations
func transformToDeclarations(file *ast.File, filename string) []FuncInfo {
	var funcs []FuncInfo

	// First pass: stub functions
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Skip functions that should not be stubbed
		if !shouldStubFunction(fn) {
			continue
		}

		// Remove the body - this tells Go to expect assembly implementation
		fn.Body = nil

		// Collect function info for assembly generation
		info := FuncInfo{
			Name:     fn.Name.Name,
			Package:  "runtime",
			FileName: filename,
		}

		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			// Method - extract receiver type
			info.Receiver = formatReceiverType(fn.Recv.List[0].Type)
		}

		funcs = append(funcs, info)
	}

	// Remove imports that are only used in function bodies
	removeUnusedImports(file)

	return funcs
}

// removeUnusedImports removes imports that are typically only used in function bodies
func removeUnusedImports(file *ast.File) {
	// List of imports to remove (these are typically only used in implementations)
	removePatterns := []string{
		"internal/",  // All internal packages
		"runtime/",   // Runtime subpackages (but not "runtime" itself in some cases)
	}

	// Filter import declarations
	var newDecls []ast.Decl
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			newDecls = append(newDecls, decl)
			continue
		}

		// Filter individual import specs
		var newSpecs []ast.Spec
		for _, spec := range genDecl.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok {
				newSpecs = append(newSpecs, spec)
				continue
			}

			// Get import path (remove quotes)
			importPath := importSpec.Path.Value
			importPath = importPath[1 : len(importPath)-1] // Remove quotes

			// Check if this import should be removed
			shouldRemove := false
			for _, pattern := range removePatterns {
				if strings.HasPrefix(importPath, pattern) {
					shouldRemove = true
					break
				}
			}

			if !shouldRemove {
				newSpecs = append(newSpecs, spec)
			}
		}

		// Only keep the import declaration if it has specs left
		if len(newSpecs) > 0 {
			genDecl.Specs = newSpecs
			newDecls = append(newDecls, decl)
		}
	}

	file.Decls = newDecls
}

// shouldStubFunction determines if a function should have its body removed
func shouldStubFunction(fn *ast.FuncDecl) bool {
	name := fn.Name.Name

	// Skip init functions - they must remain in Go
	if name == "init" {
		return false
	}

	// Skip main function
	if name == "main" {
		return false
	}

	// Skip generic functions - they need their bodies
	// Generic functions have type parameters
	if fn.Type != nil && fn.Type.TypeParams != nil && fn.Type.TypeParams.NumFields() > 0 {
		return false
	}

	// For now, stub everything else
	// We can add more exclusions as needed
	return true
}

// formatReceiverType converts an AST expression to a string representation of the receiver type
func formatReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + formatReceiverType(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// writeOverlayGoFile writes the transformed AST to a Go file
func writeOverlayGoFile(file *ast.File, fset *token.FileSet, outputPath string) error {
	// Create output directory
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create output file
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Print AST to file
	cfg := printer.Config{
		Mode:     printer.UseSpaces | printer.TabIndent,
		Tabwidth: 8,
	}

	return cfg.Fprint(out, fset, file)
}

// generateAssemblyStubs generates an assembly file with 8-NOP stubs for all functions
func generateAssemblyStubs(functions []FuncInfo, outputPath string) error {
	// Create output directory
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write header
	fmt.Fprintln(out, "// Code generated by gen-thin-stubs. DO NOT EDIT.")
	fmt.Fprintln(out, "// 8-NOP stubs for runtime functions.")
	fmt.Fprintln(out, "// These will be patched by patchruntime to trampoline to priest's runtime.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "#include \"textflag.h\"")
	fmt.Fprintln(out, "")

	// Write each stub
	for _, fn := range functions {
		symbolName := formatAssemblySymbol(fn)

		fmt.Fprintf(out, "// %s (from %s)\n", fn.Name, fn.FileName)
		fmt.Fprintf(out, "TEXT %s(SB), NOSPLIT|NOFRAME, $0\n", symbolName)
		for i := 0; i < 8; i++ {
			fmt.Fprintln(out, "\tNOP")
		}
		fmt.Fprintln(out, "")
	}

	return nil
}

// formatAssemblySymbol converts a FuncInfo to a Go assembly symbol name
func formatAssemblySymbol(fn FuncInfo) string {
	// Package name with middle dot separator
	pkg := strings.ReplaceAll(fn.Package, "/", "∕")

	if fn.Receiver == "" {
		// Plain function: runtime·mallocgc
		return fmt.Sprintf("%s·%s", pkg, fn.Name)
	}

	// Method: runtime·(*mspan).init
	return fmt.Sprintf("%s·%s.%s", pkg, fn.Receiver, fn.Name)
}

// generateOverlayJSON creates the overlay JSON file for Go's -overlay flag
func generateOverlayJSON(overlayFiles map[string]string, outputPath string) error {
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: overlayFiles,
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(overlay)
}
