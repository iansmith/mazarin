package textshape

import (
	"testing"
)

// TestCloseTemporaryFontPreservesPermanentSlot exercises the bug at
// rasterize.go where CloseTemporaryFont used to nil any in-range slot,
// including ones that had been allocated by OpenFont. Long-lived
// callers (e.g. louis14's pkg/text.fontIDCache) rely on permanent
// slots staying alive across CloseTemporaryFont passes.
func TestCloseTemporaryFontPreservesPermanentSlot(t *testing.T) {
	p := NewDirectGlyphProvider(fontDir(t))
	// Permanent open (e.g. layout side).
	mPerm, err := p.OpenFont(OpenFontRequest{Family: testFontLatin, Size: 18})
	if err != nil {
		t.Fatalf("OpenFont permanent: %v", err)
	}
	if p.fonts[mPerm.FontID] == nil {
		t.Fatalf("permanent slot %d is nil after OpenFont", mPerm.FontID)
	}
	if p.fonts[mPerm.FontID].isTemporary {
		t.Errorf("permanent slot %d marked isTemporary", mPerm.FontID)
	}

	// Temporary open of the SAME key (e.g. render side) — permanent-first
	// returns the same fontID. CloseTemporaryFont must NOT nil this slot.
	mTemp, err := p.OpenTemporaryFont(OpenFontRequest{Family: testFontLatin, Size: 18}, nil)
	if err != nil {
		t.Fatalf("OpenTemporaryFont same key: %v", err)
	}
	if mTemp.FontID != mPerm.FontID {
		t.Errorf("OpenTemporaryFont returned fontID %d, want %d (permanent-first)",
			mTemp.FontID, mPerm.FontID)
	}
	if err := p.CloseTemporaryFont(mTemp.FontID); err != nil {
		t.Fatalf("CloseTemporaryFont: %v", err)
	}
	if p.fonts[mPerm.FontID] == nil {
		t.Fatalf("permanent slot %d nilled by CloseTemporaryFont", mPerm.FontID)
	}
	// Permanent metrics still readable — proves fontData isn't dangling.
	m := p.metricsFor(mPerm.FontID)
	if m.Ascent <= 0 {
		t.Errorf("permanent slot Ascent = %d after temp close, want > 0", m.Ascent)
	}
}

// TestCloseTemporaryFontPreservesSharedFontData exercises the cross-size
// sharing path: OpenFont's findExistingFont reuses a sibling slot's mmap'd
// fontData by slice reference. CloseTemporaryFont used to Munmap that
// region unconditionally, dangling the sibling. The fix: only Munmap when
// no other slot still references the same base address.
func TestCloseTemporaryFontPreservesSharedFontData(t *testing.T) {
	p := NewDirectGlyphProvider(fontDir(t))
	// Permanent open at size 18 — mmaps the file.
	mPerm, err := p.OpenFont(OpenFontRequest{Family: testFontLatin, Size: 18})
	if err != nil {
		t.Fatalf("OpenFont size 18: %v", err)
	}
	if p.fonts[mPerm.FontID] == nil || len(p.fonts[mPerm.FontID].fontData) == 0 {
		t.Fatalf("permanent slot has no fontData")
	}
	permData := p.fonts[mPerm.FontID].fontData

	// Temporary open at DIFFERENT size — findExistingFont reuses fontData.
	mTemp, err := p.OpenTemporaryFont(OpenFontRequest{Family: testFontLatin, Size: 24}, nil)
	if err != nil {
		t.Fatalf("OpenTemporaryFont size 24: %v", err)
	}
	if mTemp.FontID == mPerm.FontID {
		t.Fatalf("OpenTemporaryFont reused permanent fontID %d for different size", mTemp.FontID)
	}
	tempSlot := p.fonts[mTemp.FontID]
	if tempSlot == nil {
		t.Fatalf("temporary slot is nil after OpenTemporaryFont")
	}
	if !tempSlot.isTemporary {
		t.Errorf("temporary slot %d not marked isTemporary", mTemp.FontID)
	}
	if &tempSlot.fontData[0] != &permData[0] {
		t.Errorf("temporary slot fontData base %p != permanent fontData base %p — sharing path not exercised",
			&tempSlot.fontData[0], &permData[0])
	}

	// Close the temporary. The permanent slot's mmap must still be live.
	if err := p.CloseTemporaryFont(mTemp.FontID); err != nil {
		t.Fatalf("CloseTemporaryFont: %v", err)
	}
	if p.fonts[mTemp.FontID] != nil {
		t.Errorf("temporary slot %d not nilled by CloseTemporaryFont", mTemp.FontID)
	}
	// Reading the permanent slot's metrics should not segfault — pre-fix
	// behavior was Munmap of the shared region, dangling the slice.
	m := p.metricsFor(mPerm.FontID)
	if m.Ascent <= 0 {
		t.Errorf("permanent slot Ascent = %d after temp close, want > 0", m.Ascent)
	}
}

// TestOpenFontUpgradesTemporaryToPermanent exercises the upgrade path:
// when OpenFont's cache hit lands on a slot previously allocated by
// OpenTemporaryFont, the slot is upgraded to permanent so the eventual
// CloseTemporaryFont becomes a no-op for it.
func TestOpenFontUpgradesTemporaryToPermanent(t *testing.T) {
	p := NewDirectGlyphProvider(fontDir(t))
	// Render side opens temporarily first.
	mTemp, err := p.OpenTemporaryFont(OpenFontRequest{Family: testFontLatin, Size: 18}, nil)
	if err != nil {
		t.Fatalf("OpenTemporaryFont: %v", err)
	}
	if !p.fonts[mTemp.FontID].isTemporary {
		t.Fatalf("slot %d not marked temporary", mTemp.FontID)
	}

	// Layout side opens via OpenFont — same key, cache hit, must upgrade.
	mPerm, err := p.OpenFont(OpenFontRequest{Family: testFontLatin, Size: 18})
	if err != nil {
		t.Fatalf("OpenFont upgrade: %v", err)
	}
	if mPerm.FontID != mTemp.FontID {
		t.Errorf("OpenFont returned different fontID (%d != %d) — cache hit failed",
			mPerm.FontID, mTemp.FontID)
	}
	if p.fonts[mPerm.FontID].isTemporary {
		t.Errorf("slot %d still temporary after OpenFont upgrade", mPerm.FontID)
	}

	// Close the original tempID. Slot must survive — layout still holds it.
	if err := p.CloseTemporaryFont(mTemp.FontID); err != nil {
		t.Fatalf("CloseTemporaryFont: %v", err)
	}
	if p.fonts[mPerm.FontID] == nil {
		t.Fatalf("upgraded slot %d nilled by CloseTemporaryFont", mPerm.FontID)
	}
}
