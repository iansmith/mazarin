package ksync

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These gates enforce one rule: an ARM64 instruction that has an assembler
// mnemonic must be written with it, never hand-encoded. Hand encodings are
// invisible to the assembler AND read as correct to a human, because the comment
// above them says what the author meant rather than what the bits do. That has
// now cost this project twice — MAZ-128/166 (a DAIF op that wrote NZCV, causing
// machine-wide IRQ blackouts) and MAZ-169 (an idle loop that yielded instead of
// waiting, burning a host core). Both were one hex digit.

// TestNoWordEncodedDAIFOps is the MAZ-167 gate: DAIF must be touched only via
// mnemonics (MRS DAIF, R0 / MSR R0, DAIF / MSR $imm, DAIFSet|DAIFClr).
//
//	D53B42.. = MRS Xn, {NZCV|DAIF}    D51B42.. = MSR {NZCV|DAIF}, Xn
//	D5034... = MSR DAIFSet/DAIFClr, #imm  (the imm4 is the third nibble —
//	           match all 16, or mask widths other than I-only escape the gate)
func TestNoWordEncodedDAIFOps(t *testing.T) {
	hits := scanTreeAsm(t, regexp.MustCompile(`(?i)WORD\s+\$0x(D53B42|D51B42|D5034[0-9A-F])`))
	if len(hits) > 0 {
		t.Errorf("found %d WORD-encoded DAIF op(s); use mnemonics (MRS DAIF, R0 / MSR R0, DAIF / MSR $imm, DAIFSet):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// TestNoBareHintImmediates is the MAZ-169 gate. `HINT $n` for n in 0..5 has a
// dedicated mnemonic, and using the raw immediate invites exactly the MAZ-169
// bug: three sites wrote HINT $1 (YIELD) under a comment claiming WFI, so the
// kernel's idle loop spun at full speed instead of parking the core — an
// unflagged divergence from amd64, which parks with a real HLT.
//
//	$0 NOOP   $1 YIELD   $2 WFE   $3 WFI   $4 SEV   $5 SEVL
//
// All six are accepted by Go's arm64 assembler (verified against Go 1.26.4), so
// this gate never demands something unwritable. Immediates ≥ 6 are left alone:
// those encode BTI/PAC/etc. hints that genuinely lack a plain mnemonic here.
func TestNoBareHintImmediates(t *testing.T) {
	hits := scanTreeAsm(t, regexp.MustCompile(`(?i)^\s*HINT\s+\$[0-5]\s*(//.*)?$`))
	if len(hits) > 0 {
		t.Errorf("found %d bare HINT immediate(s); use the mnemonic (NOOP/YIELD/WFE/WFI/SEV/SEVL for $0-$5):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// scanTreeAsm returns "path:line: text" for every line of every tracked .s file
// in the repo matching re.
func scanTreeAsm(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locating repo root: %v", err)
	}

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
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking tree: %v", err)
	}
	return hits
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
