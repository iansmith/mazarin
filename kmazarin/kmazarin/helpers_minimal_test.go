//go:build arm64

package main

import "testing"

// Test just the helper utilities without any kernel dependencies

func TestDAIFPairMinimal(t *testing.T) {
	pair := &DAIFPair{}

	if !pair.Balanced() {
		t.Error("Initially should be balanced")
	}

	saved := pair.DisableAndSaveDAIF()
	if pair.Balanced() {
		t.Error("After disable, should not be balanced")
	}

	pair.EnableAndRestoreDAIF(saved)
	if !pair.Balanced() {
		t.Error("After restore, should be balanced")
	}

	// Test multiple levels
	saved1 := pair.DisableAndSaveDAIF()
	saved2 := pair.DisableAndSaveDAIF()
	if pair.Balanced() {
		t.Error("After 2 disables, should not be balanced")
	}

	pair.EnableAndRestoreDAIF(saved2)
	pair.EnableAndRestoreDAIF(saved1)
	if !pair.Balanced() {
		t.Error("After matching restores, should be balanced")
	}
}

func TestTestClockMinimal(t *testing.T) {
	times := []uint64{1000, 2000, 3000}
	clock := NewTestClock(times)

	// CurrentTime takes a fakeTime arg which is ignored by TestClock
	if got := clock.CurrentTime(0); got != 1000 {
		t.Errorf("Expected 1000, got %d", got)
	}
	if got := clock.CurrentTime(0); got != 3000 { // Cumulative: 1000+2000=3000
		t.Errorf("Expected 3000 (cumulative), got %d", got)
	}
	if got := clock.CurrentTime(0); got != 6000 { // Cumulative: 1000+2000+3000=6000
		t.Errorf("Expected 6000 (cumulative), got %d", got)
	}
}
