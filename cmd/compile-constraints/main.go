// compile-constraints compiles .vgo source files into sibling .vbc.go files
// containing *vm.Program literals. Each .vgo file contains one function in
// the restricted Go subset accepted by mazarin/vm/compile.
//
// For each input file /a/b/c/foo.vgo the compiler produces /a/b/c/foo.vbc.go.
//
// Usage:
//
//	go tool compile-constraints -pkg main column_height.vgo
//	go tool compile-constraints identity_i64.vgo center_in_parent.vgo
//
// If -pkg is not specified, the package name for each output file defaults
// to the name of the directory that contains that input file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/compile"
)

func main() {
	pkg := flag.String("pkg", "", "package name for generated files (default: directory name of input)")
	vgolib := flag.String("vgolib", "", "directory containing .vgo library files (resolved by import statements)")
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compile-constraints [-pkg PKG] [-vgolib DIR] file.vgo ...")
		os.Exit(1)
	}

	// Validate all inputs have .vgo extension.
	for _, path := range files {
		if filepath.Ext(path) != ".vgo" {
			fmt.Fprintf(os.Stderr, "error: %s does not have .vgo extension\n", path)
			os.Exit(1)
		}
	}

	for _, path := range files {
		pkgName := *pkg
		if pkgName == "" {
			pkgName = pkgNameFromPath(path)
		}
		if err := compileOne(path, pkgName, *vgolib); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

// pkgNameFromPath derives the Go package name from the directory containing path.
func pkgNameFromPath(path string) string {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
			os.Exit(1)
		}
		return filepath.Base(wd)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs %s: %v\n", dir, err)
		os.Exit(1)
	}
	return filepath.Base(absDir)
}

// resolveImports scans source for import "name" lines, reads the corresponding
// .vgo files from libDir, and returns the combined library source plus the
// user source with import lines removed. Transitive imports in library files
// are resolved recursively.
func resolveImports(userSrc, libDir string) (string, error) {
	loaded := make(map[string]string) // name → source (imports stripped)
	var order []string                // topological order of lib names

	// resolveOne loads a lib by name, recursively resolving its imports.
	var resolveOne func(name string) error
	resolveOne = func(name string) error {
		if _, ok := loaded[name]; ok {
			return nil // already loaded
		}
		if libDir == "" {
			return fmt.Errorf("import %q requires --vgolib flag", name)
		}
		libPath := filepath.Join(libDir, name+".vgo")
		libSrc, err := os.ReadFile(libPath)
		if err != nil {
			return fmt.Errorf("import %q: %w", name, err)
		}
		// Mark as loading (to break cycles).
		loaded[name] = ""

		// Scan for transitive imports and strip import lines.
		var cleanedLines []string
		for _, line := range strings.Split(string(libSrc), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import ") {
				depName := strings.TrimPrefix(trimmed, "import ")
				depName = strings.Trim(depName, "\" ")
				if depName == "" {
					return fmt.Errorf("empty import in %s", name)
				}
				if err := resolveOne(depName); err != nil {
					return err
				}
				continue // strip import line
			}
			cleanedLines = append(cleanedLines, line)
		}
		loaded[name] = strings.Join(cleanedLines, "\n")
		order = append(order, name)
		return nil
	}

	// Scan user source for imports.
	var userCleanedLines []string
	for _, line := range strings.Split(userSrc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import ") {
			name := strings.TrimPrefix(trimmed, "import ")
			name = strings.Trim(name, "\" ")
			if name == "" {
				return "", fmt.Errorf("empty import")
			}
			if err := resolveOne(name); err != nil {
				return "", err
			}
			continue
		}
		userCleanedLines = append(userCleanedLines, line)
	}

	if len(order) == 0 {
		return userSrc, nil
	}

	// Library functions first (dependencies before dependents),
	// then user source (entry = last function).
	var combined strings.Builder
	for _, name := range order {
		combined.WriteString(loaded[name])
		combined.WriteString("\n")
	}
	combined.WriteString(strings.Join(userCleanedLines, "\n"))
	return combined.String(), nil
}

// compileOne reads a .vgo file, compiles it, and writes the sibling .vbc.go file.
func compileOne(path, pkgName, libDir string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	combinedSrc, err := resolveImports(string(src), libDir)
	if err != nil {
		return fmt.Errorf("imports: %w", err)
	}

	result, err := compile.Compile(combinedSrc)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// Derive variable name from filename: column_height.vgo → ProgColumnHeight
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".vgo")
	varName := "Prog" + snakeToPascal(base)

	// Build output.
	var buf strings.Builder
	buf.WriteString("// Code generated by compile-constraints from " + filepath.Base(path) + ". DO NOT EDIT.\n")
	buf.WriteString("package " + pkgName + "\n\n")
	buf.WriteString("import \"mazzy/mazarin/vm\"\n\n")
	emitProgram(&buf, varName, result.Program)

	// Write sibling .vbc.go file.
	outPath := strings.TrimSuffix(path, ".vgo") + ".vbc.go"
	if err := os.WriteFile(outPath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

// snakeToPascal converts "bounds_from_ulwh" → "BoundsFromUlwh".
func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	var buf strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		buf.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			buf.WriteString(p[1:])
		}
	}
	return buf.String()
}

func emitProgram(buf *strings.Builder, name string, prog *vm.Program) {
	buf.WriteString("var " + name + " = &vm.Program{\n")

	// Code
	buf.WriteString("\tCode: []vm.Inst{\n")
	for _, inst := range prog.Code {
		buf.WriteString(fmt.Sprintf("\t\t{Opcode: 0x%02x, Typ: 0x%02x, Op1: %d, Op2: %d, Flags: %d, Imm: 0x%x},\n",
			inst.Opcode, inst.Typ, inst.Op1, inst.Op2, inst.Flags, inst.Imm))
	}
	buf.WriteString("\t},\n")

	// Strings
	buf.WriteString("\tStrings: []string{")
	if len(prog.Strings) > 0 {
		buf.WriteString("\n")
		for _, s := range prog.Strings {
			buf.WriteString(fmt.Sprintf("\t\t%q,\n", s))
		}
		buf.WriteString("\t")
	}
	buf.WriteString("},\n")

	// NumArgs
	buf.WriteString(fmt.Sprintf("\tNumArgs: %d,\n", prog.NumArgs))

	// ArgTypes
	buf.WriteString("\tArgTypes: []uint8{")
	for i, t := range prog.ArgTypes {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(fmt.Sprintf("0x%02x", t))
	}
	buf.WriteString("},\n")

	// Funcs
	if len(prog.Funcs) > 0 {
		buf.WriteString("\tFuncs: []vm.FuncInfo{\n")
		for _, fi := range prog.Funcs {
			buf.WriteString(fmt.Sprintf("\t\t{Name: %q, PC: %d, NumArgs: %d, NumLocals: %d, LocalBase: %d},\n",
				fi.Name, fi.PC, fi.NumArgs, fi.NumLocals, fi.LocalBase))
		}
		buf.WriteString("\t},\n")
	}

	// Entry
	buf.WriteString(fmt.Sprintf("\tEntry: %d,\n", prog.Entry))

	buf.WriteString("}\n")
}
