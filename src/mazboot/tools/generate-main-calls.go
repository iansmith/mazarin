//go:build ignore
// +build ignore

// Code generator for main.go dummy calls
// Scans assembly files to find Go functions called from assembly
// Generates dummy function calls to prevent dead code elimination
// Reads existing file, preserves non-generated content, and inserts generated code between markers.
// Formats output with gofmt.
//
// Usage: go run generate-main-calls.go [-asm <dir>] [-file <path>] [-go <dir>]
//   -asm: Assembly source directory (default: asm/aarch64)
//   -file: Target Go file to modify (default: main/main.go)
//   -go: Go source directory for signature discovery (default: main)

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	// Parse command line flags
	var targetFile string
	var asmDir string
	var goSourceDir string
	flag.StringVar(&asmDir, "asm", "asm/aarch64", "Assembly source directory")
	flag.StringVar(&targetFile, "file", "main/main.go", "Target Go file to modify")
	flag.StringVar(&goSourceDir, "go", "main", "Go source directory for signature discovery")
	flag.Parse()

	// Find all Go functions called from assembly
	goFunctionsCalled := findGoFunctionsCalled(asmDir)

	// Read existing file and extract before/after content
	beforeContent, afterContent, err := readFileMarkers(targetFile, "//{{ LINKNAME START}}", "//{{ LINKNAME END}}")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Generate main calls content
	var generated strings.Builder
	generateMainCallsContent(goFunctionsCalled, goSourceDir, &generated)

	// Combine all content
	combined := beforeContent + generated.String() + afterContent

	// Format with gofmt
	formatted, err := formatWithGofmt(combined)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: gofmt failed, using unformatted output: %v\n", err)
		formatted = combined
	}

	// Write to file
	if err := ioutil.WriteFile(targetFile, []byte(formatted), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", targetFile, err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Generated %s\n", targetFile)
}

// readFileMarkers reads a file and returns content before markers, after markers
// If file doesn't exist, returns empty before content and default structure
func readFileMarkers(filename, startMarker, endMarker string) (string, string, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, create default structure
			header := `package main

`
			footer := "\n"
			return header, footer, nil
		}
		return "", "", err
	}

	fileStr := string(content)

	// Find marker positions
	startIdx := strings.Index(fileStr, startMarker)
	endIdx := strings.Index(fileStr, endMarker)

	if startIdx == -1 || endIdx == -1 {
		// Markers not found, return whole file as "before"
		return fileStr, "", nil
	}

	// Extract content before, between, and after markers
	before := fileStr[:startIdx+len(startMarker)]
	after := fileStr[endIdx:]

	return before + "\n", "\n" + after, nil
}

// formatWithGofmt formats Go code using gofmt
func formatWithGofmt(code string) (string, error) {
	cmd := exec.Command("gofmt", "-s")
	cmd.Stdin = strings.NewReader(code)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// generateMainCallsContent generates dummy function calls for Go functions called from assembly
// Writes to the provided writer
func generateMainCallsContent(goFunctionsCalled []string, goSourceDir string, w io.Writer) {
	// Map of known function signatures (function name -> parameter types)
	knownSignatures := map[string][]string{
		"KernelMain":            {"uint32", "uint32", "uint32"},
		"UartTransmitHandler":   {},
		"kernelMainBodyWrapper": {},
		"GrowStackForCurrent":   {},
		"ExceptionHandler":      {"uint64", "uint64", "uint64", "uint64", "uint32"},
		"HandleSyscall":         {"uint64", "uint64", "uint64", "uint64", "uint64", "uint64", "uint64"},
	}

	// Parse Go source to find signatures for unknown functions
	for _, funcName := range goFunctionsCalled {
		if _, exists := knownSignatures[funcName]; !exists {
			sig, err := findFunctionSignature(goSourceDir, funcName)
			if err == nil && sig != nil {
				knownSignatures[funcName] = sig.Params
			} else {
				// Default to no parameters if we can't find it
				knownSignatures[funcName] = []string{}
			}
		}
	}

	// Group functions by category
	var kernelFunctions []string
	var interruptHandlers []string
	var otherFunctions []string

	for _, funcName := range goFunctionsCalled {
		if funcName == "KernelMain" {
			kernelFunctions = append(kernelFunctions, funcName)
		} else if strings.Contains(strings.ToLower(funcName), "handler") {
			interruptHandlers = append(interruptHandlers, funcName)
		} else {
			otherFunctions = append(otherFunctions, funcName)
		}
	}

	// Sort for consistent output
	sort.Strings(kernelFunctions)
	sort.Strings(interruptHandlers)
	sort.Strings(otherFunctions)

	// Generate kernel function calls
	if len(kernelFunctions) > 0 {
		fmt.Fprintf(w, "\t// Call kernel initialization functions to ensure they're compiled\n")
		fmt.Fprintf(w, "\t// This will never execute in bare metal, but ensures the functions exist\n")
		for _, funcName := range kernelFunctions {
			params := knownSignatures[funcName]
			call := generateFunctionCall(funcName, params)
			fmt.Fprintf(w, "\t%s\n", call)
		}
		fmt.Fprintf(w, "\n")
	}

	// Generate interrupt handler calls
	if len(interruptHandlers) > 0 {
		fmt.Fprintf(w, "\t// Reference interrupt handlers to prevent optimization\n")
		fmt.Fprintf(w, "\t// These are called from assembly interrupt handlers and must not be optimized away\n")
		fmt.Fprintf(w, "\t// This will never execute in bare metal, but ensures the functions exist\n")
		for _, funcName := range interruptHandlers {
			params := knownSignatures[funcName]
			call := generateFunctionCall(funcName, params)
			comment := getFunctionComment(funcName)
			if comment != "" {
				fmt.Fprintf(w, "\t%s // %s\n", call, comment)
			} else {
				fmt.Fprintf(w, "\t%s\n", call)
			}
		}
		fmt.Fprintf(w, "\n")
	}

	// Generate other function calls
	if len(otherFunctions) > 0 {
		fmt.Fprintf(w, "\t// Reference other functions called from assembly\n")
		fmt.Fprintf(w, "\t// This will never execute in bare metal, but ensures the functions exist\n")
		for _, funcName := range otherFunctions {
			params := knownSignatures[funcName]
			call := generateFunctionCall(funcName, params)
			fmt.Fprintf(w, "\t%s\n", call)
		}
	}
}

// generateFunctionCall generates a function call string with appropriate dummy parameters
func generateFunctionCall(funcName string, paramTypes []string) string {
	if len(paramTypes) == 0 {
		return fmt.Sprintf("%s()", funcName)
	}

	var args []string
	for i, paramType := range paramTypes {
		arg := generateDummyArg(paramType, i)
		args = append(args, arg)
	}

	return fmt.Sprintf("%s(%s)", funcName, strings.Join(args, ", "))
}

// generateDummyArg generates a dummy argument value based on the parameter type
func generateDummyArg(paramType string, index int) string {
	// Remove pointer/array qualifiers for type matching
	baseType := strings.TrimLeft(paramType, "*[]")

	switch baseType {
	case "uint32", "uint", "uintptr":
		return "0"
	case "uint64":
		return "0"
	case "uint16":
		return "0"
	case "uint8", "byte":
		return "0"
	case "int32", "int":
		return "0"
	case "int64":
		return "0"
	case "bool":
		return "false"
	case "string":
		return `""`
	case "unsafe.Pointer":
		return "nil"
	default:
		// For unknown types, try nil or 0
		if strings.HasPrefix(paramType, "*") {
			return "nil"
		}
		return "0"
	}
}

// getFunctionComment returns a descriptive comment for a function
func getFunctionComment(funcName string) string {
	comments := map[string]string{
		"UartTransmitHandler":   "UART TX interrupt handler",
		"KernelMain":            "Kernel entry point",
		"kernelMainBodyWrapper": "Goroutine entry wrapper",
	}
	return comments[funcName]
}

// FunctionSignature represents a Go function's signature
type FunctionSignature struct {
	Name   string
	Params []string // Parameter types
}

// findFunctionSignature parses Go source files to find a function's signature
func findFunctionSignature(sourceDir string, funcName string) (*FunctionSignature, error) {
	fset := token.NewFileSet()

	// Parse all .go files in the directory
	pkgs, err := parser.ParseDir(fset, sourceDir, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var foundSig *FunctionSignature

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}

				if fn.Name == nil || fn.Name.Name != funcName {
					return true
				}

				// Found the function!
				sig := &FunctionSignature{
					Name: funcName,
				}

				if fn.Type.Params != nil {
					for _, param := range fn.Type.Params.List {
						paramType := exprToString(param.Type)
						// Handle multiple names for same type (e.g., "a, b int")
						numNames := len(param.Names)
						if numNames == 0 {
							numNames = 1 // Anonymous parameter
						}
						for i := 0; i < numNames; i++ {
							sig.Params = append(sig.Params, paramType)
						}
					}
				}

				foundSig = sig
				return false // Stop searching
			})

			if foundSig != nil {
				break
			}
		}
		if foundSig != nil {
			break
		}
	}

	if foundSig == nil {
		return nil, fmt.Errorf("function %s not found", funcName)
	}

	return foundSig, nil
}

// exprToString converts an ast.Expr to a string representation of the type
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.ArrayType:
		return "[]" + exprToString(e.Elt)
	default:
		return "unknown"
	}
}

// findGoFunctionsCalled finds Go functions called from assembly
func findGoFunctionsCalled(asmDir string) []string {
	var functions []string
	externRe := regexp.MustCompile(`\.extern\s+main\.([a-zA-Z_][a-zA-Z0-9_]*)`)
	blRe := regexp.MustCompile(`bl\s+main\.([a-zA-Z_][a-zA-Z0-9_]*)`)

	seen := make(map[string]bool)

	filepath.Walk(asmDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".s") {
			return nil
		}

		content, err := ioutil.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			// Skip comment-only lines (they might contain example code)
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") && !strings.Contains(trimmed, ".extern") {
				continue
			}

			// Check for .extern declarations
			matches := externRe.FindStringSubmatch(line)
			if len(matches) > 1 && !seen[matches[1]] {
				functions = append(functions, matches[1])
				seen[matches[1]] = true
			}

			// Check for bl calls (but skip if it's in a comment)
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				matches = blRe.FindStringSubmatch(line)
				if len(matches) > 1 && !seen[matches[1]] {
					functions = append(functions, matches[1])
					seen[matches[1]] = true
				}
			}
		}

		return nil
	})

	return functions
}
