package proc

import "testing"

// MAZ-196: handler-blob code only ever executes at EL1h, so a saved
// context pairing a PC inside the exception-vector blob with SPSR mode
// bits != EL1h (SPSR&0xF != 5) is poisoned by definition — exactly what
// the MAZ-193 SPSR hardcode produced (tier-17 witness caught the real
// poisoned continuation). The guard must halt these before the ERET.
func TestBadResumeARM64(t *testing.T) {
	const (
		blobLo   = 0x45917800 // injected bounds, shaped like the real blob
		blobHi   = 0x45919ef0
		inBlob   = blobLo + 0x100
		modeEL0t = 0x0
		modeEL1t = 0x4
		modeEL1h = 0x5
	)
	cases := []struct {
		name           string
		pc, spsr       uint64
		blobLo, blobHi uint64
		want           bool
	}{
		// The MAZ-193 poison: handler PC saved with EL1t mode.
		{"poisoned pair: blob PC + EL1t", inBlob, modeEL1t, blobLo, blobHi, true},
		// Any non-EL1h mode with a blob PC is poisoned, EL0 included.
		{"poisoned pair: blob PC + EL0t", inBlob, 0x80000000 | modeEL0t, blobLo, blobHi, true},
		// Blob boundary conditions.
		{"poisoned at blobLo", blobLo, modeEL1t, blobLo, blobHi, true},
		{"blobHi is exclusive", blobHi, modeEL1t, blobLo, blobHi, false},
		// A genuine EL1h handler continuation is NOT the guard's business.
		{"healthy: blob PC + EL1h", inBlob, 0x3C0 | modeEL1h, blobLo, blobHi, false},
		// Ordinary contexts stay resumable regardless of mode.
		{"healthy: kernel PC + EL1t", 0x45900000, modeEL1t, blobLo, blobHi, false},
		{"healthy: user PC + EL0t", 0x10000, modeEL0t, blobLo, blobHi, false},
		// The pre-existing impossible-PC check must survive.
		{"PC==0 always bad", 0, modeEL1h, blobLo, blobHi, true},
		{"PC==0 bad even without bounds", 0, modeEL1h, 0, 0, true},
		// Unpublished bounds (blobLo==0) skip the vector check entirely.
		{"no bounds: poisoned shape passes", inBlob, modeEL1t, 0, 0, false},
	}
	for _, c := range cases {
		if got := BadResumeARM64(c.pc, c.spsr, c.blobLo, c.blobHi); got != c.want {
			t.Errorf("%s: BadResumeARM64(pc=%#x, spsr=%#x, lo=%#x, hi=%#x) = %v, want %v",
				c.name, c.pc, c.spsr, c.blobLo, c.blobHi, got, c.want)
		}
	}
}
