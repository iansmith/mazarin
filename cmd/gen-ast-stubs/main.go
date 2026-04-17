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
	shepherdSrc   []byte          // In shepherd mode: text-modified source (nil = use AST printer)
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
	flagMode          = flag.String("mode", "thin", "Mode: 'thin' generates stub bodies for .maz, 'shepherd' adds //go:noinline + keep-alive for shepherd builds")
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

	// Dispatch based on mode
	switch *flagMode {
	case "thin":
		runThinMode(packages)
	case "shepherd":
		runShepherdMode(packages)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown mode %q (use 'thin' or 'shepherd')\n", *flagMode)
		os.Exit(1)
	}
}

// runThinMode generates thin stub overlay files for .maz builds.
// Each stubbable function's body is replaced with _thinStubPanic + return.
func runThinMode(packages []packageInfo) {
	var allStubs []FuncStub
	totalFiles := 0
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

	if err := writeOverlayJSON(*flagOverlayOut, overlayReplace); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating overlay: %v\n", err)
		os.Exit(1)
	}

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

// keepAliveEntry tracks a function that needs to be referenced in _keepForMaz.
type keepAliveEntry struct {
	receiverExpr string // "" for functions, "(*Type)" or "Type" for methods
	funcName     string // function or method name
}

// runShepherdMode generates overlay files for shepherd (host) builds.
// Each stubbable function gets //go:noinline added (body unchanged).
// A _keepForMaz() function is appended per sub-package to prevent DCE.
func runShepherdMode(packages []packageInfo) {
	totalFiles := 0
	totalNoinline := 0
	overlayReplace := make(map[string]string)

	for _, pkg := range packages {
		if *flagVerbose {
			fmt.Printf("Processing package (shepherd): %s (source: %s)\n", pkg.name, pkg.sourceDir)
		}

		noinlineCount, fileCount, err := processPackageForShepherd(pkg, overlayReplace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing package %s: %v\n", pkg.name, err)
			os.Exit(1)
		}

		totalNoinline += noinlineCount
		totalFiles += fileCount
	}

	if err := writeOverlayJSON(*flagOverlayOut, overlayReplace); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating overlay: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %d shepherd overlay files with %d //go:noinline functions across %d packages\n",
		totalFiles, totalNoinline, len(packages))
	fmt.Printf("Overlay written to: %s\n", *flagOverlayOut)
}

// processPackageForShepherd processes a package in shepherd mode: adds //go:noinline
// to stubbable functions and generates MazKeepAliveSymbols() per sub-package.
func processPackageForShepherd(pkg packageInfo, overlayReplace map[string]string) (int, int, error) {
	files, err := findSourceFiles(pkg.sourceDir)
	if err != nil {
		return 0, 0, fmt.Errorf("finding source files: %w", err)
	}

	// Use `go list` to get the authoritative set of files the compiler
	// would include for each sub-package. This handles all build constraints
	// (goexperiment, asan, race, etc.) correctly.
	compiledFileCache := make(map[string]map[string]bool) // subPkgDir → set of basenames
	getCompiled := func(filePath string) bool {
		// Determine sub-package directory relative to pkg.sourceDir
		dir := filepath.Dir(filePath)
		if cached, ok := compiledFileCache[dir]; ok {
			return cached[filepath.Base(filePath)]
		}

		// Compute import path from directory
		rel, _ := filepath.Rel(filepath.Dir(pkg.sourceDir), dir)
		importPath := strings.ReplaceAll(rel, string(filepath.Separator), "/")
		if importPath == "" {
			importPath = pkg.name
		}

		compiled, err := getCompiledFiles(*flagGo, importPath)
		if err != nil {
			if *flagVerbose {
				fmt.Printf("  WARNING: go list %s failed: %v (skipping keep-alive for this sub-package)\n", importPath, err)
			}
			compiledFileCache[dir] = nil
			return false
		}
		compiledFileCache[dir] = compiled
		return compiled[filepath.Base(filePath)]
	}

	// Collect keep-alive entries per sub-package.
	type subPkgInfo struct {
		entries   []keepAliveEntry
		firstFile string // first output file (for appending MazKeepAliveSymbols)
	}
	subPackages := make(map[string]*subPkgInfo)

	noinlineCount := 0
	fileCount := 0

	for _, path := range files {
		if strings.HasSuffix(path, ".s") {
			rel, _ := filepath.Rel(pkg.sourceDir, path)
			outputPath := filepath.Join(pkg.outputDir, rel)
			if err := copyFile(path, outputPath); err != nil {
				return 0, 0, fmt.Errorf("copying %s: %w", path, err)
			}
			absOrig, _ := filepath.Abs(path)
			absStub, _ := filepath.Abs(outputPath)
			overlayReplace[absOrig] = absStub
			fileCount++
			continue
		}

		result, count, err := transformFileForShepherd(path)
		if err != nil {
			return 0, 0, fmt.Errorf("transforming %s: %w", path, err)
		}

		rel, _ := filepath.Rel(pkg.sourceDir, path)
		outputPath := filepath.Join(pkg.outputDir, rel)

		if result.shepherdSrc != nil {
			if err := writeStubFile(result, outputPath); err != nil {
				return 0, 0, fmt.Errorf("writing %s: %w", outputPath, err)
			}
		} else {
			if err := copyFile(path, outputPath); err != nil {
				return 0, 0, fmt.Errorf("copying %s: %w", path, err)
			}
		}

		// Track entries for keep-alive, grouped by sub-package
		pkgName := result.File.Name.Name
		if subPackages[pkgName] == nil {
			subPackages[pkgName] = &subPkgInfo{}
		}
		sp := subPackages[pkgName]
		if sp.firstFile == "" {
			sp.firstFile = outputPath
		}

		// Only include functions from files that `go list` confirms will be
		// compiled for the target platform. This correctly handles all build
		// constraints including goexperiment flags.
		if len(result.StubFuncs) > 0 && getCompiled(path) {
			for _, stub := range result.StubFuncs {
				entry := keepAliveEntry{funcName: stub.Name}
				if stub.Receiver != "" {
					entry.receiverExpr = stub.Receiver
				}
				sp.entries = append(sp.entries, entry)
			}
		}

		absOrig, _ := filepath.Abs(path)
		absStub, _ := filepath.Abs(outputPath)
		overlayReplace[absOrig] = absStub

		noinlineCount += count
		fileCount++

		if *flagVerbose {
			fmt.Printf("  %s/%s: %d noinline\n", pkg.name, rel, count)
		}
	}

	// Append MazKeepAliveSymbols() to first file of each sub-package.
	// This exported function references all stubbable functions from
	// unconditionally-compiled files, preventing the linker from DCE'ing them.
	for pkgName, sp := range subPackages {
		if len(sp.entries) == 0 || sp.firstFile == "" {
			continue
		}
		if err := appendMazKeepAliveSymbolsFunc(sp.firstFile, sp.entries); err != nil {
			return 0, 0, fmt.Errorf("appending MazKeepAliveSymbols for %s: %w", pkgName, err)
		}
		if *flagVerbose {
			fmt.Printf("  %s: MazKeepAliveSymbols with %d entries\n", pkgName, len(sp.entries))
		}
	}

	fmt.Printf("  %s: %d files, %d //go:noinline functions\n",
		pkg.name, fileCount, noinlineCount)
	return noinlineCount, fileCount, nil
}

// shepherdPostCallInjections maps a call expression (as it appears in source)
// to a line of code to insert immediately after it. This replaces fragile sed
// post-processing in Taskfile.yml with a versioned, greppable table.
var shepherdPostCallInjections = map[string]string{
	"worldStarted()": "\tsyncMazWriteBarriers()",
}

// shepherdFuncInfo holds info about a function that needs //go:noinline in shepherd mode.
type shepherdFuncInfo struct {
	line int      // 1-based line number of the func keyword
	stub FuncStub // stub info for keep-alive generation
}

// transformFileForShepherd parses a file to find stubbable functions, then does
// text-based insertion of //go:noinline before each one. This avoids go/ast
// comment positioning issues that cause "misplaced compiler directive" errors.
// Returns a TransformResult (for keep-alive tracking) and the noinline count.
func transformFileForShepherd(path string) (*TransformResult, int, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}

	result := &TransformResult{
		File:        file,
		FileSet:     fset,
		UsedImports: make(map[string]bool),
	}

	// Collect functions that need //go:noinline
	var funcs []shepherdFuncInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !shouldStub(fn) {
			continue
		}

		// Check if already has //go:noinline
		alreadyHas := false
		if fn.Doc != nil {
			for _, c := range fn.Doc.List {
				if strings.TrimSpace(c.Text) == "//go:noinline" {
					alreadyHas = true
					break
				}
			}
		}

		stub := createStub(fn, file.Name.Name)
		result.StubFuncs = append(result.StubFuncs, stub)

		if !alreadyHas {
			// Use the line of the func keyword for insertion
			pos := fset.Position(fn.Pos())
			funcs = append(funcs, shepherdFuncInfo{
				line: pos.Line,
				stub: stub,
			})
		}
	}

	if len(funcs) == 0 {
		// No modifications needed — just copy the file
		return result, 0, nil
	}

	// Read original file and insert //go:noinline directives
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", path, err)
	}

	lines := strings.Split(string(srcBytes), "\n")

	// Build set of lines that need //go:noinline inserted before them
	insertBefore := make(map[int]bool) // 1-based line numbers
	for _, fi := range funcs {
		insertBefore[fi.line] = true
	}

	// Rebuild the file with insertions
	var out strings.Builder
	for i, line := range lines {
		lineNum := i + 1 // 1-based
		if insertBefore[lineNum] {
			out.WriteString("//go:noinline\n")
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
		// Post-call injections: insert extra line after matching call sites
		trimmed := strings.TrimSpace(line)
		if inject, ok := shepherdPostCallInjections[trimmed]; ok {
			out.WriteString(inject)
			out.WriteString("\n")
		}
	}

	// Store the modified source for writing
	result.shepherdSrc = []byte(out.String())

	return result, len(funcs), nil
}

// getCompiledFiles uses `go list` to get the authoritative list of Go files
// that the compiler would include for a package on the target platform.
// This handles all build constraints correctly, including goexperiment flags.
func getCompiledFiles(goBinary, pkgPath string) (map[string]bool, error) {
	goos := os.Getenv("TARGET_GOOS")
	if goos == "" {
		goos = "linux"
	}
	goarch := os.Getenv("TARGET_GOARCH")
	if goarch == "" {
		goarch = "arm64"
	}

	cmd := exec.Command(goBinary, "list", "-f", `{{join .GoFiles "\n"}}`, pkgPath)
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w", pkgPath, err)
	}

	files := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files[line] = true
		}
	}
	return files, nil
}

// appendMazKeepAliveSymbolsFunc appends the exported MazKeepAliveSymbols()
// function and its backing array to the given file. Each function reference
// is stored to a unique slot in an exported global array, which the compiler
// cannot eliminate (stores to exported globals are observable side effects).
func appendMazKeepAliveSymbolsFunc(path string, entries []keepAliveEntry) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf strings.Builder

	// Exported global array — stores to exported globals are observable
	// side effects that the compiler cannot eliminate.
	buf.WriteString(fmt.Sprintf("\n\n// MazKeptSymbols holds references to all stubbable functions.\n"))
	buf.WriteString(fmt.Sprintf("// Exported so the compiler cannot prove no one reads it.\n"))
	buf.WriteString(fmt.Sprintf("var MazKeptSymbols [%d]interface{}\n", len(entries)))

	buf.WriteString("\n// MazKeepAliveSymbols stores function references into MazKeptSymbols,\n")
	buf.WriteString("// preventing the linker from eliminating runtime functions that\n")
	buf.WriteString("// .maz modules may need via thin stub imports.\n")
	buf.WriteString("//\n//go:noinline\n")
	buf.WriteString("func MazKeepAliveSymbols() {\n")

	for i, e := range entries {
		if e.receiverExpr != "" {
			buf.WriteString(fmt.Sprintf("\tMazKeptSymbols[%d] = %s.%s\n", i, e.receiverExpr, e.funcName))
		} else {
			buf.WriteString(fmt.Sprintf("\tMazKeptSymbols[%d] = %s\n", i, e.funcName))
		}
	}

	buf.WriteString("}\n")

	_, err = f.WriteString(buf.String())
	return err
}

// copyFile copies a file from src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
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
	sentinelAdded := false

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

		// Append _thinStubSentinel + _thinStubPanic to the first stub file.
		// We can't add new files to stdlib directories via overlay (Go
		// rejects overlays that add files under GOROOT), so we append
		// these declarations to an existing overlaid file instead.
		if !sentinelAdded && len(result.StubFuncs) > 0 {
			if err := appendSentinelCode(outputPath); err != nil {
				return nil, 0, fmt.Errorf("appending sentinel to %s: %w", outputPath, err)
			}
			sentinelAdded = true
			if *flagVerbose {
				fmt.Printf("  %s/%s: sentinel code appended\n", pkg.name, rel)
			}
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

// appendSentinelCode appends _thinStubSentinel and _thinStubPanic
// declarations to an existing stub file. We append to an existing file
// because Go's overlay system cannot add NEW files to standard library
// package directories (it rejects overlays modifying paths under GOROOT).
func appendSentinelCode(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(`

var _thinStubSentinel uint32

//go:noinline
func _thinStubPanic(name string) {
	if _thinStubSentinel == 0 {
		panic("stub called without trampoline: " + name)
	}
}
`)
	return err
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
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip directories that `go build` ignores: leading "_" or ".",
			// and the conventional "testdata" name. Go 1.26 added
			// runtime/_mkmalloc/, a generator tool whose files are not part
			// of the runtime package — walking into it pollutes the overlay
			// with stubs in unrelated packages (e.g. astutil).
			name := info.Name()
			if path != runtimeDir && (strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
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
				"_freebsd", "_openbsd", "_netbsd", "_dragonfly", "_solaris", "_aix",
				"_bsd", "_illumos", "_wasip1") {
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
			fn.Body = createMinimalBody(fset, fn, file.Name.Name)
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

// createMinimalBody creates a stub function body:
//
//	_thinStubPanic("pkg.func")
//	return
//
// _thinStubPanic is defined in the generated _thin_stub_sentinel.go file
// with //go:noinline, so the compiler cannot see that it panics. From
// the compiler's perspective, _thinStubPanic might return, so:
//  1. The stub function is NOT marked NeverReturns — the return statement
//     is reachable. This prevents the compiler from propagating "never
//     returns" to callers (which caused chanrecv2 to be dead-code-eliminated).
//  2. If the stub is ever called without being trampolined, _thinStubPanic
//     panics with the function name for diagnosis.
//
// To enable bare `return`, any unnamed return values in the function
// signature are given synthetic names (_stubR0, _stubR1, ...).
func createMinimalBody(fset *token.FileSet, fn *ast.FuncDecl, pkgName string) *ast.BlockStmt {
	var pos token.Pos
	if fn.Body != nil {
		pos = fn.Body.Lbrace
	}

	// Name any unnamed return values so bare `return` works
	nameReturnValues(fn)

	// Build "pkg.func" or "pkg.(*Type).Method" for the panic message
	funcName := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		funcName = typeString(fn.Recv.List[0].Type) + "." + funcName
	}
	qualName := fmt.Sprintf("%s.%s", pkgName, funcName)

	// _thinStubPanic("pkg.func")
	callStmt := &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun:    &ast.Ident{Name: "_thinStubPanic", NamePos: pos},
			Lparen: pos,
			Args: []ast.Expr{
				&ast.BasicLit{
					Kind:     token.STRING,
					Value:    fmt.Sprintf("%q", qualName),
					ValuePos: pos,
				},
			},
			Rparen: pos,
		},
	}

	// return (bare — uses named return values, all zero-initialized)
	retStmt := &ast.ReturnStmt{Return: pos}

	return &ast.BlockStmt{
		Lbrace: pos,
		List:   []ast.Stmt{callStmt, retStmt},
		Rbrace: pos,
	}
}

// nameReturnValues adds synthetic names (_stubR0, _stubR1, ...) to any
// unnamed return values in the function signature. This enables bare
// `return` statements that return zero values without needing to know
// the actual types.
func nameReturnValues(fn *ast.FuncDecl) {
	if fn.Type.Results == nil {
		return
	}
	counter := 0
	for _, field := range fn.Type.Results.List {
		if len(field.Names) == 0 {
			field.Names = []*ast.Ident{{Name: fmt.Sprintf("_stubR%d", counter)}}
			counter++
		} else {
			counter += len(field.Names)
		}
	}
}

// writeStubFile writes a transformed AST to a file
func writeStubFile(result *TransformResult, outputPath string) error {
	// Create directory
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	// In shepherd mode, use pre-built text source if available
	if result.shepherdSrc != nil {
		return os.WriteFile(outputPath, result.shepherdSrc, 0644)
	}

	// Write AST to buffer first, then post-process to insert //go:noinline.
	// The AST-based fn.Doc replacement doesn't work with go/printer
	// (it ignores the new Doc if the comment isn't in file.Comments),
	// so we do text-based insertion instead.
	var buf strings.Builder
	cfg := printer.Config{
		Mode:     printer.UseSpaces | printer.TabIndent,
		Tabwidth: 8,
	}
	if err := cfg.Fprint(&buf, result.FileSet, result.File); err != nil {
		return err
	}

	// Build set of stubbed function names for matching
	stubbedFuncs := make(map[string]bool)
	for _, stub := range result.StubFuncs {
		stubbedFuncs[stub.Name] = true
	}

	// Post-process: insert //go:noinline before stubbed functions.
	// Match lines starting with "func " or "func (" (methods).
	lines := strings.Split(buf.String(), "\n")
	var out strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(stubbedFuncs) > 0 && strings.HasPrefix(trimmed, "func ") {
			// Extract function name: "func Name(" or "func (recv) Name("
			funcName := extractFuncName(trimmed)
			if funcName != "" && stubbedFuncs[funcName] {
				// Check if previous line already has //go:noinline
				if i == 0 || strings.TrimSpace(lines[i-1]) != "//go:noinline" {
					out.WriteString("//go:noinline\n")
				}
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}

	return os.WriteFile(outputPath, []byte(out.String()), 0644)
}

// extractFuncName extracts the function name from a line like
// "func Name(" or "func (r *Type) Name(". Returns "" if not parseable.
func extractFuncName(line string) string {
	// Remove "func " prefix
	rest := strings.TrimPrefix(line, "func ")
	if rest == line {
		return ""
	}

	// Skip receiver: "(*Type) " or "(Type) "
	if strings.HasPrefix(rest, "(") {
		idx := strings.Index(rest, ") ")
		if idx < 0 {
			return ""
		}
		rest = rest[idx+2:]
	}

	// Extract name up to "(" or end
	idx := strings.IndexByte(rest, '(')
	if idx < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:idx])
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
