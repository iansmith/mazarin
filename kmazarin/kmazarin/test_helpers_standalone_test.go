//go:build test_stubs

package main

import "testing"

// TestDAIFPair tests the DAIF lock/unlock pair
func TestDAIFPairStandalone(t *testing.T) {
	pair := &DAIFPair{}

	// Initially balanced
	if !pair.Balanced() {
		t.Error("Initially should be balanced")
	}

	// Disable
	saved := pair.DisableAndSaveDAIF()
	if pair.Balanced() {
		t.Error("After disable, should not be balanced")
	}

	// Enable (restore)
	pair.EnableAndRestoreDAIF(saved)
	if !pair.Balanced() {
		t.Error("After restore, should be balanced")
	}
}

// TestTestClock tests the test clock
func TestTestClockStandalone(t *testing.T) {
	times := []uint64{100, 200, 300}
	clock := NewTestClock(times)

	// CurrentTime returns cumulative sums: 100, 300 (100+200), 600 (100+200+300)
	if got := clock.CurrentTime(0); got != 100 {
		t.Errorf("Expected 100, got %d", got)
	}
	if got := clock.CurrentTime(0); got != 300 { // 100+200
		t.Errorf("Expected 300 (cumulative), got %d", got)
	}
	if got := clock.CurrentTime(0); got != 600 { // 100+200+300
		t.Errorf("Expected 600 (cumulative), got %d", got)
	}
}
