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
	hits := scanTreeAsm(t, regexp.MustCompile(`(?i)^\s*HINT\s+\$[0-5]\s*(?://.*)?$`))
	if len(hits) > 0 {
		t.Errorf("found %d bare HINT immediate(s); use the mnemonic (NOOP/YIELD/WFE/WFI/SEV/SEVL for $0-$5):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// TestNoWordEncodedHintsOrBarriers is the MAZ-174 gate: hint and barrier ops
// must be written as mnemonics (NOOP/YIELD/WFE/WFI/SEV/SEVL, DSB/DMB/ISB $imm),
// never as WORDs. This space is where both prior bugs in this family lived, and
// writing this gate found a third: a DSB SY hand-encoding commented "ISB" after
// a CSSELR_EL1 write (asm_barriers_arm64.s), one nibble from what the comment
// promised.
//
//	D50320xx = HINT #imm    D5033x9F = DSB    D5033xBF = DMB    D5033xDF = ISB
//	(x is CRm, the barrier domain: F=SY, B=ISH, 3=OSH, 2=OSHST, ...)
//
// The immediate is parsed numerically rather than pattern-matched as a hex
// literal, so an alternate spelling of the same word (decimal, octal, binary)
// cannot slip past — the single-spelling weakness CodeRabbit flagged on the
// HINT gate in PR #97.
func TestNoWordEncodedHintsOrBarriers(t *testing.T) {
	wordRe := regexp.MustCompile(`(?i)WORD\s+\$([0-9A-Za-z_]+)`)
	var hits []string
	for _, hit := range scanTreeAsm(t, wordRe) {
		v, err := strconv.ParseUint(wordRe.FindStringSubmatch(hit)[1], 0, 64)
		if err != nil {
			continue // symbolic operand, not a numeric literal
		}
		switch {
		case v&^uint64(0xFF) == 0xD5032000, // HINT #imm
			v&0xFFFFF0FF == 0xD503309F, // DSB, any domain
			v&0xFFFFF0FF == 0xD50330BF, // DMB, any domain
			v&0xFFFFF0FF == 0xD50330DF: // ISB
			hits = append(hits, hit)
		}
	}
	if len(hits) > 0 {
		t.Errorf("found %d WORD-encoded hint/barrier op(s); use mnemonics (DSB/DMB/ISB $imm, or the named hints):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// TestGPUIRQMaskingJustified is the MAZ-173 gate: the virtio-gpu driver may
// not mask CPU interrupts around a GPU operation unless the call site carries
// an explicit justification marker. The driver used to wrap every screen flush
// in saveAndDisableIRQs and then busy-poll the used ring — an IRQ-masked spin
// on every repaint (the MAZ-127/146 hazard class). The only legitimate masking
// left is cursor-queue state shared with the tablet IRQ top-half, where the
// mask IS the synchronization; such a site must say so with a comment
// containing "IRQ-MASK-JUSTIFIED(MAZ-" within the three lines above the call.
func TestGPUIRQMaskingJustified(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locating repo root: %v", err)
	}
	dir := filepath.Join(root, "kmazarin", "device", "virtio", "gpu")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var hits []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "saveAndDisableIRQs()") ||
				strings.Contains(line, "func saveAndDisableIRQs") {
				continue
			}
			justified := false
			for j := i - 3; j < i; j++ {
				if j >= 0 && strings.Contains(lines[j], "IRQ-MASK-JUSTIFIED(MAZ-") {
					justified = true
				}
			}
			if !justified {
				hits = append(hits, e.Name()+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("found %d unjustified IRQ-masking site(s) in device/virtio/gpu; either remove the mask (gpuLock serializes the control queue) or add an IRQ-MASK-JUSTIFIED(MAZ-nnn) comment explaining which IRQ-context state the mask synchronizes:\n  %s",
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
