// tools/gen-test-stubs.go
//
// Generates test stub files for packages that have forward declarations
// and assembly implementations. Scans for functions without bodies and:
// 1. Generates stub implementations with //go:build test_stubs tags
// 2. Adds //go:build !test_stubs tags to source files with forward declarations
package main

import (
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

var (
	flagPackageDir = flag.String("package", "", "Path to package directory to scan")
	flagOutput     = flag.String("output", "", "Output file path for stubs (e.g., stubs_test.go)")
	flagAddTags    = flag.Bool("add-tags", false, "Add //go:build !test_stubs to source files")
	flagVerbose    = flag.Bool("v", false, "Verbose output")
)

// StubFunc represents a function that needs a stub
type StubFunc struct {
	Name       string
	Params     string // Function signature parameters
	Results    string // Function signature results
	IsExported bool
	SourceFile string // Which file it came from
}

func main() {
	flag.Parse()

	if *flagPackageDir == "" || *flagOutput == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -package=<dir> -output=<file.go> [-add-tags] [-v]\n", os.Args[0])
		os.Exit(1)
	}

	// Find all .go files in package (not assembly, not tests)
	files, err := findGoFiles(*flagPackageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding files: %v\n", err)
		os.Exit(1)
	}

	if *flagVerbose {
		fmt.Printf("Found %d Go files in %s\n", len(files), *flagPackageDir)
	}

	// Parse files and find forward declarations
	stubs, packageName, filesWithDecls, err := findForwardDeclarations(files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing files: %v\n", err)
		os.Exit(1)
	}

	if *flagVerbose {
		fmt.Printf("Found %d forward declarations in %d files\n", len(stubs), len(filesWithDecls))
		for _, stub := range stubs {
			fmt.Printf("  %s%s%s (from %s)\n", stub.Name, stub.Params, stub.Results, filepath.Base(stub.SourceFile))
		}
	}

	// Add build tags to source files if requested
	if *flagAddTags && len(filesWithDecls) > 0 {
		for _, file := range filesWithDecls {
			if err := addBuildTag(file); err != nil {
				fmt.Fprintf(os.Stderr, "Error adding build tag to %s: %v\n", file, err)
				os.Exit(1)
			}
			if *flagVerbose {
				fmt.Printf("Added //go:build !test_stubs to %s\n", filepath.Base(file))
			}
		}
	}

	// Generate stub file
	if err := generateStubFile(packageName, stubs, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating stub file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s with %d stubs\n", *flagOutput, len(stubs))
	if !*flagAddTags && len(filesWithDecls) > 0 {
		fmt.Printf("\nNote: Run with -add-tags to automatically add build tags to source files:\n")
		for _, file := range filesWithDecls {
			fmt.Printf("  %s\n", filepath.Base(file))
		}
	}
}

// findGoFiles finds all .go files (not tests, not generated) in directory
func findGoFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}

		// Skip test files
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		files = append(files, filepath.Join(dir, name))
	}

	return files, nil
}

// findForwardDeclarations parses files and finds functions without bodies
// Returns: stubs, packageName, filesWithDeclarations, error
func findForwardDeclarations(files []string) ([]StubFunc, string, []string, error) {
	var stubs []StubFunc
	var packageName string
	filesWithDecls := make(map[string]bool)

	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, "", nil, fmt.Errorf("parse %s: %w", path, err)
		}

		if packageName == "" {
			packageName = file.Name.Name
		}

		hasDecls := false

		// Find functions without bodies
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			// Forward declaration = no body
			if fn.Body != nil {
				continue
			}

			// Skip methods (receiver functions) - we only stub package-level functions
			if fn.Recv != nil {
				continue
			}

			hasDecls = true

			stub := StubFunc{
				Name:       fn.Name.Name,
				IsExported: ast.IsExported(fn.Name.Name),
				SourceFile: path,
			}

			// Build parameter string
			if fn.Type.Params != nil {
				stub.Params = formatFieldList(fset, fn.Type.Params)
			} else {
				stub.Params = "()"
			}

			// Build result string
			if fn.Type.Results != nil {
				stub.Results = " " + formatFieldList(fset, fn.Type.Results)
			}

			stubs = append(stubs, stub)
		}

		if hasDecls {
			filesWithDecls[path] = true
		}
	}

	// Convert map to slice
	var declFiles []string
	for file := range filesWithDecls {
		declFiles = append(declFiles, file)
	}

	return stubs, packageName, declFiles, nil
}

// formatFieldList formats a parameter or result list
func formatFieldList(fset *token.FileSet, fields *ast.FieldList) string {
	var buf strings.Builder
	buf.WriteString("(")

	for i, field := range fields.List {
		if i > 0 {
			buf.WriteString(", ")
		}

		// Write parameter names if present
		for j, name := range field.Names {
			if j > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(name.Name)
			buf.WriteString(" ")
		}

		// Write type
		printer.Fprint(&buf, fset, field.Type)
	}

	buf.WriteString(")")
	return buf.String()
}

// addBuildTag adds //go:build !test_stubs to a file if not already present
func addBuildTag(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// Check if already has the build tag
	if strings.Contains(string(content), "//go:build !test_stubs") ||
		strings.Contains(string(content), "//go:build test_stubs") ||
		strings.Contains(string(content), "// +build !test_stubs") {
		return nil // Already tagged
	}

	// Add build tag at the top
	newContent := "//go:build !test_stubs\n\n" + string(content)

	return os.WriteFile(filePath, []byte(newContent), 0644)
}

// generateStubFile generates the stub file with implementations
func generateStubFile(packageName string, stubs []StubFunc, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write header
	fmt.Fprintf(f, "//go:build test_stubs\n\n")
	fmt.Fprintf(f, "// Code generated by tools/gen-test-stubs.go. DO NOT EDIT.\n\n")
	fmt.Fprintf(f, "package %s\n\n", packageName)

	// Group stubs by exported vs unexported
	var exported, unexported []StubFunc
	for _, stub := range stubs {
		if stub.IsExported {
			exported = append(exported, stub)
		} else {
			unexported = append(unexported, stub)
		}
	}

	// Write exported stubs
	if len(exported) > 0 {
		fmt.Fprintf(f, "// Exported functions (forward declarations replaced with stubs)\n")
		for _, stub := range exported {
			writeStubFunc(f, stub)
		}
		fmt.Fprintf(f, "\n")
	}

	// Write unexported stubs
	if len(unexported) > 0 {
		fmt.Fprintf(f, "// Unexported functions (forward declarations replaced with stubs)\n")
		for _, stub := range unexported {
			writeStubFunc(f, stub)
		}
	}

	return nil
}

// writeStubFunc writes a single stub function
func writeStubFunc(f *os.File, stub StubFunc) {
	fmt.Fprintf(f, "func %s%s%s {", stub.Name, stub.Params, stub.Results)

	// Generate appropriate return value based on result types
	if strings.Contains(stub.Results, "int") || strings.Contains(stub.Results, "uint") {
		fmt.Fprintf(f, " return 0 ")
	} else if strings.Contains(stub.Results, "uintptr") {
		fmt.Fprintf(f, " return 0 ")
	} else if strings.Contains(stub.Results, "bool") {
		fmt.Fprintf(f, " return false ")
	} else if strings.Contains(stub.Results, "string") {
		fmt.Fprintf(f, " return \"\" ")
	} else if stub.Results != "" && stub.Results != " ()" {
		// Generic fallback for other types
		fmt.Fprintf(f, " return nil ")
	}

	fmt.Fprintf(f, "}\n")
}
