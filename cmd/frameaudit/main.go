// Command frameaudit verifies that every field of an architecture's
// ThreadContext struct is handled by every context save/restore site, so that
// adding a field cannot be silently missed by one path. This is the structural
// guard for the MAZ-135 bug class: the TLSG field was added to the struct and
// to two of three save sites, leaving SaveCurrentThreadContext (and two asm
// restore tails) reading the stale R14 g-home instead.
//
// The struct is the single source of truth for the field set. frameaudit reads
// it with go/parser, then checks two kinds of site:
//
//   - Go save functions, marked with a "frameaudit:save" doc-comment directive.
//     Each must reference every persisted field as a ".Field" selector. The
//     historical bug is exactly a field that appears nowhere in the function.
//
//   - Assembly save/restore regions, bracketed by line comments:
//       // FRAME-SAVE-BEGIN [label]     ...  // FRAME-SAVE-END
//       // FRAME-RESTORE-BEGIN [label]  ...  // FRAME-RESTORE-END
//     Each region must reference every persisted field's go_asm.h symbol
//     (<Prefix>_<Field>), either directly or via a list macro named by -list
//     (e.g. CTX_GPRS) whose membership is read from the DSL header (-dsl). A
//     region that invokes the list macro is credited with every list field.
//
// A field may be exempted from a direction with a field doc comment:
//
//	R12 uint64 // frameaudit:exempt-asm base pointer, restored positionally
//
// Exit status is non-zero if any site is missing any non-exempt field.
//
// Usage:
//
//	frameaudit -struct FILE -prefix NAME [-go FILE,...] [-asm FILE,...] [-dsl FILE -list NAME]
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
)

func main() {
	structFile := flag.String("struct", "", "Go file declaring the ThreadContext struct (required)")
	structName := flag.String("type", "ThreadContext", "name of the saved-context struct type")
	prefix := flag.String("prefix", "ThreadContext", "go_asm.h symbol prefix for struct fields")
	goFiles := flag.String("go", "", "comma-separated Go files holding frameaudit:save functions")
	asmFiles := flag.String("asm", "", "comma-separated .s files holding FRAME-SAVE/RESTORE regions")
	dslFile := flag.String("dsl", "", "DSL header declaring the list macro (-list)")
	listMacro := flag.String("list", "", "name of the list macro covering the uniform field set (e.g. CTX_GPRS)")
	flag.Parse()

	if *structFile == "" {
		fmt.Fprintln(os.Stderr, "frameaudit: -struct is required")
		os.Exit(2)
	}

	fields, exempt, err := parseStruct(*structFile, *structName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frameaudit: %v\n", err)
		os.Exit(2)
	}
	if len(fields) == 0 {
		fmt.Fprintf(os.Stderr, "frameaudit: no fields found in type %s in %s\n", *structName, *structFile)
		os.Exit(2)
	}

	var listFields map[string]bool
	if *dslFile != "" && *listMacro != "" {
		listFields, err = parseListMacro(*dslFile, *listMacro, *prefix)
		if err != nil {
			fmt.Fprintf(os.Stderr, "frameaudit: %v\n", err)
			os.Exit(2)
		}
	}

	var gaps []string

	// Go save functions: each must reference every persisted field.
	for _, f := range splitList(*goFiles) {
		fnGaps, err := auditGoSaves(f, fields, exempt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "frameaudit: %v\n", err)
			os.Exit(2)
		}
		gaps = append(gaps, fnGaps...)
	}

	// Assembly regions: each must reference every persisted field's symbol.
	for _, f := range splitList(*asmFiles) {
		regGaps, err := auditAsmRegions(f, *prefix, *listMacro, fields, listFields, exempt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "frameaudit: %v\n", err)
			os.Exit(2)
		}
		gaps = append(gaps, regGaps...)
	}

	if len(gaps) > 0 {
		fmt.Fprintf(os.Stderr, "frameaudit: %d context field(s) unhandled by a save/restore site:\n", len(gaps))
		for _, g := range gaps {
			fmt.Fprintf(os.Stderr, "  - %s\n", g)
		}
		os.Exit(1)
	}
	fmt.Printf("frameaudit: OK — all %d %s fields handled by every audited site\n", len(fields), *structName)
}

// parseStruct returns the ordered field names of the named struct and a map of
// direction-specific exemptions keyed by field name ("go", "asm", or "all").
func parseStruct(file, typeName string) (fields []string, exempt map[string]map[string]bool, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	exempt = map[string]map[string]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil, nil, fmt.Errorf("%s is not a struct", typeName)
			}
			for _, fld := range st.Fields.List {
				for _, name := range fld.Names {
					if name.Name == "_" {
						continue
					}
					fields = append(fields, name.Name)
					if dirs := exemptDirs(fld.Comment); len(dirs) > 0 {
						exempt[name.Name] = dirs
					}
				}
			}
		}
	}
	return fields, exempt, nil
}

// exemptDirs reads "frameaudit:exempt[-go|-asm]" directives from a field's
// trailing comment, returning the set of directions exempted.
func exemptDirs(cg *ast.CommentGroup) map[string]bool {
	dirs := map[string]bool{}
	for _, tok := range directiveTokens(cg) {
		switch tok {
		case "frameaudit:exempt":
			dirs["all"] = true
		case "frameaudit:exempt-go":
			dirs["go"] = true
		case "frameaudit:exempt-asm":
			dirs["asm"] = true
		}
	}
	return dirs
}

func isExempt(exempt map[string]map[string]bool, field, dir string) bool {
	d := exempt[field]
	return d != nil && (d["all"] || d[dir])
}

// auditGoSaves checks every frameaudit:save function in file references all
// non-exempt fields as a .Field selector.
func auditGoSaves(file string, fields []string, exempt map[string]map[string]bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var gaps []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !hasDirective(fn.Doc, "frameaudit:save") {
			continue
		}
		seen := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				seen[sel.Sel.Name] = true
			}
			return true
		})
		for _, fld := range fields {
			if isExempt(exempt, fld, "go") {
				continue
			}
			if !seen[fld] {
				pos := fset.Position(fn.Pos())
				gaps = append(gaps, fmt.Sprintf("%s:%d save func %s never references .%s",
					file, pos.Line, fn.Name.Name, fld))
			}
		}
	}
	return gaps, nil
}

func hasDirective(cg *ast.CommentGroup, directive string) bool {
	for _, tok := range directiveTokens(cg) {
		if tok == directive {
			return true
		}
	}
	return false
}

// directiveTokens returns the whitespace-separated tokens of every comment in a
// group, with each comment's leading "/" slashes stripped. Both the doc-comment
// directive scan (frameaudit:save) and the field-comment exemption scan read
// directives this way.
func directiveTokens(cg *ast.CommentGroup) []string {
	if cg == nil {
		return nil
	}
	var toks []string
	for _, c := range cg.List {
		toks = append(toks, strings.Fields(strings.TrimLeft(c.Text, "/"))...)
	}
	return toks
}

type asmRegion struct {
	kind  string // "SAVE" or "RESTORE"
	label string
	line  int
	body  string
}

var (
	beginRe = regexp.MustCompile(`//\s*FRAME-(SAVE|RESTORE)-BEGIN(.*)`)
	endRe   = regexp.MustCompile(`//\s*FRAME-(SAVE|RESTORE)-END`)
)

// auditAsmRegions checks every marked region in file references all non-exempt
// field symbols, directly or via the list macro.
func auditAsmRegions(file, prefix, listMacro string, fields []string, listFields map[string]bool, exempt map[string]map[string]bool) ([]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var regions []asmRegion
	var cur *asmRegion
	for i, ln := range lines {
		if m := beginRe.FindStringSubmatch(ln); m != nil {
			if cur != nil {
				return nil, fmt.Errorf("%s:%d nested FRAME-%s-BEGIN (previous %s unterminated)", file, i+1, m[1], cur.kind)
			}
			cur = &asmRegion{kind: m[1], label: strings.TrimSpace(m[2]), line: i + 1}
			continue
		}
		if endRe.MatchString(ln) {
			if cur == nil {
				return nil, fmt.Errorf("%s:%d FRAME-END with no open region", file, i+1)
			}
			regions = append(regions, *cur)
			cur = nil
			continue
		}
		if cur != nil {
			// Scan only code, not comments: a comment mentioning a field
			// symbol must not mask a genuinely-missing reference.
			cur.body += stripComment(ln) + "\n"
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("%s:%d FRAME-%s-BEGIN unterminated", file, cur.line, cur.kind)
	}

	var gaps []string
	for _, r := range regions {
		usesList := listMacro != "" && strings.Contains(r.body, listMacro+"(")
		for _, fld := range fields {
			if isExempt(exempt, fld, "asm") {
				continue
			}
			if usesList && listFields[fld] {
				continue
			}
			if !strings.Contains(r.body, prefix+"_"+fld) {
				name := r.label
				if name == "" {
					name = fmt.Sprintf("@%d", r.line)
				}
				gaps = append(gaps, fmt.Sprintf("%s:%d %s region %q missing %s_%s",
					file, r.line, r.kind, name, prefix, fld))
			}
		}
	}
	return gaps, nil
}

// parseListMacro extracts the set of <prefix>_<Field> members referenced in the
// body of a multi-line #define macroName(...) in the DSL header.
func parseListMacro(file, macroName, prefix string) (map[string]bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	defRe := regexp.MustCompile(`^\s*#define\s+` + regexp.QuoteMeta(macroName) + `\b`)
	memberRe := regexp.MustCompile(regexp.QuoteMeta(prefix) + `_([A-Za-z0-9]+)`)
	out := map[string]bool{}
	collecting := false
	for _, ln := range lines {
		if defRe.MatchString(ln) {
			collecting = true
		}
		if collecting {
			for _, m := range memberRe.FindAllStringSubmatch(ln, -1) {
				out[m[1]] = true
			}
			if !strings.HasSuffix(strings.TrimRight(ln, " \t"), `\`) {
				collecting = false
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: list macro %s not found or has no %s_ members", file, macroName, prefix)
	}
	return out, nil
}

// stripComment removes a "//" line comment from an assembly source line so the
// region scan considers code only.
func stripComment(ln string) string {
	code, _, _ := strings.Cut(ln, "//")
	return code
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
