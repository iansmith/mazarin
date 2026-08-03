package ksync

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNoWordEncodedDAIFOps is the MAZ-167 grep gate: DAIF must be touched only
// via assembler mnemonics (MRS DAIF, R0 / MSR R0, DAIF / MSR $imm, DAIFSet|DAIFClr),
// never via hand-encoded WORDs. Hand encodings are how the MAZ-128 typo class
// happened twice (op2=0 selects NZCV, op2=1 selects DAIF — one hex digit apart),
// causing MAZ-166's machine-wide IRQ blackouts. The mnemonic forms make the
// mistake unwritable: the assembler validates the register name.
//
//	D53B42.. = MRS Xn, {NZCV|DAIF}    D51B42.. = MSR {NZCV|DAIF}, Xn
//	D5034... = MSR DAIFSet/DAIFClr, #imm  (the imm4 is the third nibble —
//	           match all 16, or mask widths other than I-only escape the gate)
func TestNoWordEncodedDAIFOps(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locating repo root: %v", err)
	}
	daifWord := regexp.MustCompile(`(?i)WORD\s+\$0x(D53B42|D51B42|D5034[0-9A-F])`)

	var hits []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			// bin/ holds a full vendored GOROOT and build/ holds artifacts;
			// runtime-patches/ is stock Go runtime asm, whose NZCV use is
			// legitimate. .claude/ holds stale worktree copies of this tree.
			case ".git", ".claude", "build", "bin", "runtime-patches", ".slopstop":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".s") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if daifWord.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking tree: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("found %d WORD-encoded DAIF op(s); use mnemonics (MRS DAIF, R0 / MSR R0, DAIF / MSR $imm, DAIFSet):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// repoRoot walks up from the package dir to the directory containing go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
