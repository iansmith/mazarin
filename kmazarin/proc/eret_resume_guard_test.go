package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// MAZ-197 Phase 0 — structural red test for the ARM64 asm pre-ERET resume
// guard.
//
// Honest scope: MAZ-196 gave the resume-guard predicate a host-testable
// pure-Go definition (proc.BadResumeARM64, pinned by resume_guard_test.go)
// and wired it into the Go-side scheduler resume path. The MAZ-179 probe
// record proved that path is NOT where the MAZ-193 poison actually reached
// ERET — it reached ERET via the raw asm exception-return paths, which never
// consult the Go-side guard. MAZ-197 closes that consumption-waist by adding
// an asm-level check immediately before every ERET in kmazarin, so a
// poisoned (ELR_EL1, SPSR_EL1) pair is rejected at the last instant it is
// still data (mirroring amd64's eret_rip_ok / bad_iretq_dump guard in
// exceptions_amd64.s).
//
// There is no poison-producer knob on master (the MAZ-193 producer bug is
// already fixed), so there is no way to drive a poisoned pair through a
// live ERET and observe the halt end-to-end — the failure mode this ticket
// defends against is a boot-time halt, not something a conventional test
// harness can trigger. What IS feasible, and what this test pins, is DoD
// item 2 itself: "a guard sequence referencing vectorBlobLo/vectorBlobHi
// precedes every ERET." This test builds the real ARM64 kmazarin.elf's
// disassembly (via bin/target-objdump), locates each of the five ERET
// instructions kmazarin executes (four inside the woven
// main.ExceptionVectorTable blob — sync_return, irq_return, the el0_not_svc
// inline ERET, el0_return — and one in main.YieldToReadyThread.abi0), and
// asserts that in the instructions immediately preceding each one, the code
// computes the addresses of BOTH main.vectorBlobLo and main.vectorBlobHi
// (the ADRP+LDR/STR/ADD idiom Go asm uses to reference a global) AND that a
// conditional branch is data-flow-linked to the loaded bounds — a
// cmp/subs/cmn/ccmp naming a register that received a blob value followed
// by a b.cond, or a cbz/cbnz/tbz/tbnz directly on such a register — so a
// guard that loads the bounds but never actually compares and branches on
// them does not get credit (the windows already contain unrelated
// conditional branches, so mere branch presence proves nothing). On current (unguarded) code
// this is false for all five sites — that is the red this phase
// establishes. It does not, and cannot, prove the SPSR mask comparison or
// the halt routing are wired correctly; that part of the DoD (no false halt
// on any legitimate resume) is signed off by the 10x180s soak called for in
// the ticket, not by this structural probe, and the true-positive property
// (the guard actually firing on a real poisoned pair) is unverified by
// anything in this DoD — there is no poison-producer knob to drive it.
//
// ERET census: kmazarin's ARM64 asm contains SIX ERET instructions. The
// sixth, ksyscall/launch_arm64.s jumpToUserspace, is deliberately excluded
// from this test (and from the guard): its SPSR is the hardcoded immediate
// 0 (fresh EL0t entry) and its ELR is the caller-supplied entry point —
// neither is restored from a saved ThreadContext/exception frame, so it
// cannot carry the poisoned-context corruption this ticket targets.
//
// Environment dependency, documented rather than hidden: this test shells
// out to the project's prebuilt ARM64 cross-toolchain (bin/target-objdump,
// bin/target-nm) and reads the last-built build/kmazarin.elf. It does not
// invoke `$GO tool task` itself — rebuilding the kernel from a Go test
// would violate the project's own build-pipeline rules (no bare go build,
// always through the Taskfile's overlay/gen-overlay steps) — so it trusts
// whatever ELF is already on disk. If any of the three paths is absent
// (e.g. a fresh checkout before the first `$GO tool task` build), the test
// skips cleanly rather than failing on an environment gap it did not
// create; run `$GO tool task` first to exercise it for real.

const (
	vectorBlobLoSym = "main.vectorBlobLo"
	vectorBlobHiSym = "main.vectorBlobHi"
	// wantEretCount is the number of ERET instructions the investigation
	// found in kmazarin's ARM64 asm: sync_return (~exceptions_arm64.s:1414),
	// irq_return (~1970), the el0_not_svc inline ERET (~2327), el0_return
	// (~2617), and abi_stubs_arm64.s's YieldToReadyThread (~324).
	wantEretCount = 5
	// guardWindowInstrs bounds how far back before an ERET the test looks
	// for the vectorBlobLo/vectorBlobHi address computation. It is generous
	// (the amd64 eret_rip_ok mirror is ~15 instructions) and is further
	// bounded so it never crosses into a sibling ERET's own territory or
	// before the enclosing function's start — see findGuardRefsBeforeErets.
	guardWindowInstrs = 150
)

// repoRootFromPackageDir resolves the repository root from this package's
// directory (kmazarin/proc), verified by the presence of go.mod.
func repoRootFromPackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved repo root %q does not look right (no go.mod found): %v", root, err)
	}
	return root
}

// asmLine is one parsed line of objdump -d output: either an instruction
// (addr/mnemonic/operands set, isLabel false) or a function/symbol label
// (addr/name set, isLabel true).
type asmLine struct {
	addr     uint64
	mnemonic string
	operands string
	isLabel  bool
	label    string
}

var (
	instrLineRe = regexp.MustCompile(`^\s*([0-9a-fA-F]+):\s+[0-9a-fA-F]+\s+(\S+)\s*(.*)$`)
	labelLineRe = regexp.MustCompile(`^([0-9a-fA-F]+)\s+<([^>]+)>:\s*$`)
	nmLineRe    = regexp.MustCompile(`^([0-9a-fA-F]+)\s+\S\s+(\S+)\s*$`)
	adrpRe      = regexp.MustCompile(`^(x[0-9]+|sp)\s*,\s*([0-9a-fA-F]+)\b`)
	// Immediates accept both decimal and hex: this objdump prints LDR/STR
	// offsets in decimal but ADD immediates in hex (#0xf28), so a
	// decimal-only pattern silently mis-parses ADRP+ADD address
	// materialization as offset 0 — a false negative with no error.
	baseOffRe = regexp.MustCompile(`\[(x[0-9]+|sp)\s*,\s*#(-?(?:0x[0-9a-fA-F]+|\d+))\]`)
	addImmRe  = regexp.MustCompile(`^(x[0-9]+|sp)\s*,\s*(x[0-9]+|sp)\s*,\s*#(-?(?:0x[0-9a-fA-F]+|\d+))`)
)

// resolveSymbolAddr runs bin/target-nm over the ELF and returns the address
// of the named symbol, or 0 if not found.
func resolveSymbolAddr(t *testing.T, nmBin, elf, symbol string) uint64 {
	t.Helper()
	out, err := exec.Command(nmBin, elf).Output()
	if err != nil {
		t.Fatalf("%s %s: %v", nmBin, elf, err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := nmLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		if m[2] == symbol {
			addr, err := strconv.ParseUint(m[1], 16, 64)
			if err != nil {
				t.Fatalf("nm: bad address %q for %s: %v", m[1], symbol, err)
			}
			return addr
		}
	}
	return 0
}

// disassemble runs bin/target-objdump -d over the ELF and returns the
// parsed instruction/label stream in file order.
func disassemble(t *testing.T, objdump, elf string) []asmLine {
	t.Helper()
	out, err := exec.Command(objdump, "-d", elf).Output()
	if err != nil {
		t.Fatalf("%s -d %s: %v", objdump, elf, err)
	}
	var lines []asmLine
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Text()
		if m := labelLineRe.FindStringSubmatch(text); m != nil {
			addr, err := strconv.ParseUint(m[1], 16, 64)
			if err != nil {
				continue
			}
			lines = append(lines, asmLine{addr: addr, isLabel: true, label: m[2]})
			continue
		}
		if m := instrLineRe.FindStringSubmatch(text); m != nil {
			addr, err := strconv.ParseUint(m[1], 16, 64)
			if err != nil {
				continue
			}
			lines = append(lines, asmLine{addr: addr, mnemonic: m[2], operands: strings.TrimSpace(m[3])})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning objdump output: %v", err)
	}
	return lines
}

// eretSite is one located ERET instruction with its index in the parsed
// instruction stream and the name of its enclosing function (from the
// nearest preceding label), for diagnostics.
type eretSite struct {
	idx  int
	addr uint64
	fn   string
}

// findErets returns every ERET instruction in the parsed stream, in file
// (address) order, tagged with its enclosing function.
func findErets(lines []asmLine) []eretSite {
	var erets []eretSite
	curFn := "?"
	for i, l := range lines {
		if l.isLabel {
			curFn = l.label
			continue
		}
		if strings.EqualFold(l.mnemonic, "eret") {
			erets = append(erets, eretSite{idx: i, addr: l.addr, fn: curFn})
		}
	}
	return erets
}

// guardRefs records whether, within a site's search window, the code
// computed the address of vectorBlobLo and/or vectorBlobHi, and whether a
// conditional branch is data-flow-linked to the loaded bounds: a
// cmp/subs/cmn/ccmp naming a register that received a blob value, followed
// by a conditional branch — or a cbz/cbnz/tbz/tbnz directly on such a
// register. A branch with no register link to the bounds gets no credit
// (round-2 adversary: every one of the five windows already contains
// unrelated conditional branches today, so mere branch presence is
// satisfiable by dead loads).
type guardRefs struct {
	sawLo         bool
	sawHi         bool
	branchAfter   bool
	firstRefIndex int // stream index of the first blob reference, -1 if none
}

// condBranchRe matches the ARM64 conditional-branch family a guard's
// compare must end in: b.<cond>, cbz/cbnz, tbz/tbnz.
var (
	condBranchRe = regexp.MustCompile(`^(b\.[a-z]{2}|cbz|cbnz|tbz|tbnz)$`)
	// firstRegRe pulls the destination/first-operand register off an
	// instruction's operand string (`x9, [x27, #3880]` → x9).
	firstRegRe = regexp.MustCompile(`^(x[0-9]+|w[0-9]+|xzr|sp)\b`)
	// regTokenRe enumerates every register named anywhere in an operand
	// string, for the cmp-mentions-a-blob-register check.
	regTokenRe = regexp.MustCompile(`\b(x[0-9]+|w[0-9]+)\b`)
)

// normReg folds a W-register name onto its X register so a 32-bit view of a
// blob-holding register still matches.
func normReg(r string) string {
	if strings.HasPrefix(r, "w") {
		return "x" + r[1:]
	}
	return r
}

// findGuardRefsBeforeErets checks, for each ERET site, whether the
// instructions immediately preceding it (bounded by guardWindowInstrs, the
// enclosing function's start, and the previous ERET site — so guard credit
// never crosses from one site to another) compute the address of
// vectorBlobLo and/or vectorBlobHi via the ADRP+LDR/STR/ADD idiom.
func findGuardRefsBeforeErets(lines []asmLine, erets []eretSite, blobLo, blobHi uint64) map[uint64]guardRefs {
	result := make(map[uint64]guardRefs, len(erets))
	for n, e := range erets {
		// Bound the backward window: not past the previous ERET's own
		// instruction, not past guardWindowInstrs back, and never before
		// the start of the file.
		lowerBound := 0
		if e.idx-guardWindowInstrs > lowerBound {
			lowerBound = e.idx - guardWindowInstrs
		}
		if n > 0 && erets[n-1].idx+1 > lowerBound {
			lowerBound = erets[n-1].idx + 1
		}
		// Also never cross the enclosing function's own start label.
		for i := e.idx - 1; i >= lowerBound; i-- {
			if lines[i].isLabel {
				if i+1 > lowerBound {
					lowerBound = i + 1
				}
				break
			}
		}

		regPage := map[string]uint64{}
		// blobRegs tracks registers currently holding a blob value (or
		// blob address); cmpArmed is set by a cmp/subs/cmn/ccmp that names
		// one of them, and consumed by the next conditional branch.
		blobRegs := map[string]bool{}
		cmpArmed := false
		refs := guardRefs{firstRefIndex: -1}
		noteRef := func(i int, destReg string) {
			if refs.firstRefIndex < 0 {
				refs.firstRefIndex = i
			}
			if destReg != "" {
				blobRegs[normReg(destReg)] = true
			}
		}
		for i := lowerBound; i < e.idx; i++ {
			l := lines[i]
			if l.isLabel {
				continue
			}
			mn := strings.ToLower(l.mnemonic)
			if condBranchRe.MatchString(mn) {
				switch mn {
				case "cbz", "cbnz", "tbz", "tbnz":
					// Compare-and-branch in one: credit only if it tests a
					// blob-holding register directly.
					if m := firstRegRe.FindStringSubmatch(l.operands); m != nil && blobRegs[normReg(m[1])] {
						refs.branchAfter = true
					}
				default: // b.<cond> — credit only a compare-armed branch.
					if cmpArmed {
						refs.branchAfter = true
					}
				}
				cmpArmed = false
				continue
			}
			switch mn {
			case "cmp", "subs", "cmn", "ccmp":
				for _, r := range regTokenRe.FindAllString(l.operands, -1) {
					if blobRegs[normReg(r)] {
						cmpArmed = true
					}
				}
			}
			switch mn {
			case "adrp":
				if m := adrpRe.FindStringSubmatch(l.operands); m != nil {
					page, err := strconv.ParseUint(m[2], 16, 64)
					if err == nil {
						regPage[m[1]] = page
					}
				}
			case "ldr", "str", "ldrb", "strb", "ldrh", "strh":
				if m := baseOffRe.FindStringSubmatch(l.operands); m != nil {
					base, off := m[1], m[2]
					if page, ok := regPage[base]; ok {
						offVal, err := strconv.ParseInt(off, 0, 64)
						if err == nil {
							addr := uint64(int64(page) + offVal)
							dest := ""
							if strings.HasPrefix(mn, "ldr") {
								if dm := firstRegRe.FindStringSubmatch(l.operands); dm != nil {
									dest = dm[1]
								}
							}
							if addr == blobLo {
								refs.sawLo = true
								noteRef(i, dest)
							}
							if addr == blobHi {
								refs.sawHi = true
								noteRef(i, dest)
							}
						}
					}
				}
			case "add":
				if m := addImmRe.FindStringSubmatch(l.operands); m != nil {
					src, off := m[2], m[3]
					if page, ok := regPage[src]; ok {
						offVal, err := strconv.ParseInt(off, 0, 64)
						if err == nil {
							addr := uint64(int64(page) + offVal)
							if addr == blobLo {
								refs.sawLo = true
								noteRef(i, m[1])
							}
							if addr == blobHi {
								refs.sawHi = true
								noteRef(i, m[1])
							}
							// Propagate so a chained ADRP+ADD+LDR (address
							// materialized in a second register) is still
							// tracked.
							regPage[m[1]] = addr
						}
					}
				}
			}
		}
		result[e.addr] = refs
	}
	return result
}

// TestEretSitesGuardedByVectorBlobBounds pins DoD item 2 of MAZ-197: every
// ERET in kmazarin's ARM64 asm must be preceded by a guard sequence that
// references both vectorBlobLo and vectorBlobHi (the published bounds of
// the exception-vector handler blob), so a poisoned (ELR, SPSR) pair can be
// rejected before it reaches hardware. See the package-level doc comment
// above for what this test does and does not prove.
func TestEretSitesGuardedByVectorBlobBounds(t *testing.T) {
	root := repoRootFromPackageDir(t)
	objdump := filepath.Join(root, "bin", "target-objdump")
	nmBin := filepath.Join(root, "bin", "target-nm")
	elf := filepath.Join(root, "build", "kmazarin.elf")

	for _, p := range []string{objdump, nmBin, elf} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("MAZ-197: prebuilt path %s not present (%v) — run `$GO tool task` to build ARM64 kmazarin.elf first, then re-run this test", p, err)
		}
	}

	// Staleness guard: this test's verdict is only as fresh as the ELF it
	// disassembles. A kmazarin.elf older than the asm sources it was built
	// from would silently grade yesterday's kernel (this repo has shipped
	// exactly that failure before — see feedback_runtime_overlay_verify).
	// Fail loudly, don't skip: a stale ELF mid-development is an actionable
	// error, not a missing environment.
	elfInfo, err := os.Stat(elf)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", elf, err)
	}
	for _, src := range []string{
		filepath.Join(root, "kmazarin", "kmazarin", "exceptions_arm64.s"),
		filepath.Join(root, "kmazarin", "kmazarin", "abi_stubs_arm64.s"),
	} {
		srcInfo, err := os.Stat(src)
		if err != nil {
			t.Fatalf("os.Stat(%s): %v", src, err)
		}
		if elfInfo.ModTime().Before(srcInfo.ModTime()) {
			t.Fatalf("MAZ-197: build/kmazarin.elf (%s) is OLDER than %s (%s) — the disassembly would grade a stale kernel. Rebuild with `$GO tool task` and re-run", elfInfo.ModTime().Format("2006-01-02 15:04:05"), src, srcInfo.ModTime().Format("2006-01-02 15:04:05"))
		}
	}

	blobLo := resolveSymbolAddr(t, nmBin, elf, vectorBlobLoSym)
	blobHi := resolveSymbolAddr(t, nmBin, elf, vectorBlobHiSym)
	if blobLo == 0 || blobHi == 0 || blobHi <= blobLo {
		t.Fatalf("MAZ-197: could not resolve %s/%s addresses from %s (lo=%#x hi=%#x) — has resume_guard_arm64.go moved or been renamed?", vectorBlobLoSym, vectorBlobHiSym, elf, blobLo, blobHi)
	}

	lines := disassemble(t, objdump, elf)
	erets := findErets(lines)
	if len(erets) != wantEretCount {
		var got []string
		for _, e := range erets {
			got = append(got, fmt.Sprintf("%#x in %s", e.addr, e.fn))
		}
		t.Fatalf("MAZ-197: expected exactly %d ERET instructions in kmazarin.elf (sync_return, irq_return, el0_not_svc, el0_return, YieldToReadyThread) — found %d: %v", wantEretCount, len(erets), got)
	}

	refsByAddr := findGuardRefsBeforeErets(lines, erets, blobLo, blobHi)

	for _, e := range erets {
		refs := refsByAddr[e.addr]
		if !refs.sawLo || !refs.sawHi {
			t.Errorf("MAZ-197: ERET at %#x (in %s) is not preceded by a guard sequence referencing both %s (%#x, seen=%v) and %s (%#x, seen=%v) — the asm pre-ERET resume guard is missing at this site",
				e.addr, e.fn, vectorBlobLoSym, blobLo, refs.sawLo, vectorBlobHiSym, blobHi, refs.sawHi)
			continue
		}
		if !refs.branchAfter {
			t.Errorf("MAZ-197: ERET at %#x (in %s) loads %s/%s but no conditional branch is data-flow-linked to them (cmp/subs/cmn/ccmp on a bounds-holding register followed by b.cond, or cbz/cbnz/tbz/tbnz directly on one) — the bounds are materialized but never compared and acted on; a guard that cannot fire is not a guard",
				e.addr, e.fn, vectorBlobLoSym, vectorBlobHiSym)
		}
	}
}
