// Phase 0 red tests for MAZ-72 (linux shepherd per-PID record table).
//
// These tests describe the expected behavior of Table and PerPIDRecord.
// They fail against the stub implementation in record.go — that's the
// point. The Phase B implementation agent's job is to make them pass.

package processrecord

import (
	"errors"
	"testing"
)

// TestNewTableIsEmpty verifies a fresh table starts empty.
func TestNewTableIsEmpty(t *testing.T) {
	tbl := New()
	if got := tbl.Len(); got != 0 {
		t.Errorf("new Table Len() = %d, want 0", got)
	}
	if _, ok := tbl.Get(42); ok {
		t.Errorf("Get on empty table returned ok=true; want false")
	}
}

// TestCreateAndGetRoundtrip verifies Create returns a usable pointer
// and Get returns the same record.
func TestCreateAndGetRoundtrip(t *testing.T) {
	tbl := New()
	rec, err := tbl.Create(42)
	if err != nil {
		t.Fatalf("Create(42) returned %v, want nil", err)
	}
	if rec == nil {
		t.Fatalf("Create(42) returned nil record")
	}
	if rec.PID != 42 {
		t.Errorf("Created record PID = %d, want 42", rec.PID)
	}
	got, ok := tbl.Get(42)
	if !ok {
		t.Fatalf("Get(42) after Create returned ok=false")
	}
	if got != rec {
		t.Errorf("Get returned different pointer than Create; want same record")
	}
}

// TestCreateExistingPIDReturnsErr verifies that creating a record for
// an already-known PID returns ErrPIDExists and does not overwrite.
func TestCreateExistingPIDReturnsErr(t *testing.T) {
	tbl := New()
	rec1, err := tbl.Create(42)
	if err != nil {
		t.Fatalf("first Create(42) returned %v", err)
	}
	rec1.CWD = "/marker"
	rec2, err := tbl.Create(42)
	if !errors.Is(err, ErrPIDExists) {
		t.Errorf("second Create(42) returned %v, want ErrPIDExists", err)
	}
	if rec2 != nil {
		t.Errorf("second Create(42) returned non-nil record; want nil")
	}
	// Original record should be untouched.
	got, ok := tbl.Get(42)
	if !ok {
		t.Fatalf("Get(42) after failed second Create returned ok=false")
	}
	if got.CWD != "/marker" {
		t.Errorf("Original record CWD = %q, want %q (must not be overwritten)", got.CWD, "/marker")
	}
}

// TestRemoveThenGetReturnsFalse verifies Remove deletes a record.
func TestRemoveThenGetReturnsFalse(t *testing.T) {
	tbl := New()
	_, _ = tbl.Create(42)
	tbl.Remove(42)
	if _, ok := tbl.Get(42); ok {
		t.Errorf("Get(42) after Remove returned ok=true; want false")
	}
	if got := tbl.Len(); got != 0 {
		t.Errorf("Len after Remove = %d, want 0", got)
	}
}

// TestRemoveNonExistentIsNoop verifies Remove on a missing PID does not
// panic and does not affect other records.
func TestRemoveNonExistentIsNoop(t *testing.T) {
	tbl := New()
	_, _ = tbl.Create(42)
	tbl.Remove(99) // never created
	if _, ok := tbl.Get(42); !ok {
		t.Errorf("Get(42) after irrelevant Remove(99) returned ok=false")
	}
	if got := tbl.Len(); got != 1 {
		t.Errorf("Len after irrelevant Remove = %d, want 1", got)
	}
}

// TestMultiplePIDsCoexist verifies the table holds independent records
// for distinct PIDs.
func TestMultiplePIDsCoexist(t *testing.T) {
	tbl := New()
	pids := []PID{10, 20, 30, 40}
	for _, pid := range pids {
		rec, err := tbl.Create(pid)
		if err != nil {
			t.Fatalf("Create(%d) returned %v", pid, err)
		}
		rec.CWD = "/proc/" + pidString(pid)
	}
	if got := tbl.Len(); got != len(pids) {
		t.Errorf("Len after 4 Creates = %d, want %d", got, len(pids))
	}
	for _, pid := range pids {
		got, ok := tbl.Get(pid)
		if !ok {
			t.Errorf("Get(%d) returned ok=false", pid)
			continue
		}
		want := "/proc/" + pidString(pid)
		if got.CWD != want {
			t.Errorf("Record(%d).CWD = %q, want %q", pid, got.CWD, want)
		}
	}
}

// TestLenTracksCount verifies Len grows on Create and shrinks on Remove.
func TestLenTracksCount(t *testing.T) {
	tbl := New()
	for i := PID(1); i <= 5; i++ {
		_, _ = tbl.Create(i)
		if got := tbl.Len(); got != int(i) {
			t.Errorf("Len after Create(%d) = %d, want %d", i, got, i)
		}
	}
	for i := PID(5); i >= 1; i-- {
		tbl.Remove(i)
		want := int(i) - 1
		if got := tbl.Len(); got != want {
			t.Errorf("Len after Remove(%d) = %d, want %d", i, got, want)
		}
	}
}

// TestFieldsAreWritable verifies that all placeholder fields on
// PerPIDRecord can be set and read back via the table's pointer.
func TestFieldsAreWritable(t *testing.T) {
	tbl := New()
	rec, err := tbl.Create(42)
	if err != nil {
		t.Fatalf("Create returned %v", err)
	}
	rec.CWD = "/home/test"
	rec.Environ = []byte("PATH=/bin\x00")
	rec.SigMask = 0xFF00
	rec.PendingSigs = 0x0F

	got, ok := tbl.Get(42)
	if !ok {
		t.Fatalf("Get(42) returned ok=false after field writes")
	}
	if got.CWD != "/home/test" {
		t.Errorf("CWD = %q, want %q", got.CWD, "/home/test")
	}
	if string(got.Environ) != "PATH=/bin\x00" {
		t.Errorf("Environ = %q, want %q", got.Environ, "PATH=/bin\x00")
	}
	if got.SigMask != 0xFF00 {
		t.Errorf("SigMask = %#x, want %#x", got.SigMask, 0xFF00)
	}
	if got.PendingSigs != 0x0F {
		t.Errorf("PendingSigs = %#x, want %#x", got.PendingSigs, 0x0F)
	}
}

// pidString is a small helper that converts PID to string without
// importing strconv (kept tiny to match nosplit-style discipline that
// might apply to similar code elsewhere; this is just user-code so it
// could use strconv — but small helper is fine).
func pidString(p PID) string {
	if p == 0 {
		return "0"
	}
	neg := false
	if p < 0 {
		neg = true
		p = -p
	}
	digits := make([]byte, 0, 10)
	for p > 0 {
		digits = append([]byte{byte('0' + p%10)}, digits...)
		p /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
