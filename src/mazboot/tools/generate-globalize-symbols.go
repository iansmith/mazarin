//go:build ignore
// +build ignore

// Tool to discover which Go symbols need to be globalized (strengthened) before linking.
// Scans assembly files for .extern main.* declarations and bl main.* calls to determine
// which Go functions are called from assembly and need --globalize-symbol treatment.
//
// Usage: go run generate-globalize-symbols.go -asm <asm_dir> -o <output_file>
//
// Output: One symbol per line, formatted as "main.FunctionName" for use with objcopy

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	var outputFile string
	var asmDir string
	flag.StringVar(&outputFile, "o", "", "Output file path (required)")
	flag.StringVar(&asmDir, "asm", "asm/aarch64", "Assembly source directory")
	flag.Parse()

	if outputFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -o flag is required\n")
		fmt.Fprintf(os.Stderr, "Usage: %s -o <output_file> [-asm <asm_dir>]\n", os.Args[0])
		os.Exit(1)
	}

	// Find all Go functions called from assembly
	symbols := findGoFunctionsCalledFromAssembly(asmDir)

	// Sort for consistent output
	sort.Strings(symbols)

	// Write to file, one symbol per line
	content := strings.Join(symbols, "\n") + "\n"
	if err := ioutil.WriteFile(outputFile, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", outputFile, err)
		os.Exit(1)
	}

	fmt.Printf("Found %d symbol(s) that need globalizing:\n", len(symbols))
	for _, sym := range symbols {
		fmt.Printf("  %s\n", sym)
	}
}

// findGoFunctionsCalledFromAssembly finds Go functions called from assembly
// Returns list of symbols in format "main.FunctionName" for objcopy --globalize-symbol
func findGoFunctionsCalledFromAssembly(asmDir string) []string {
	var symbols []string
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
				symbols = append(symbols, "main."+matches[1])
				seen[matches[1]] = true
			}

			// Check for bl calls (but skip if it's in a comment)
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				matches = blRe.FindStringSubmatch(line)
				if len(matches) > 1 && !seen[matches[1]] {
					symbols = append(symbols, "main."+matches[1])
					seen[matches[1]] = true
				}
			}
		}

		return nil
	})

	return symbols
}



