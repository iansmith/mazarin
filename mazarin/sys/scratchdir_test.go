package sys

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestSetupScratchDirClearsStaleDir — MAZ-206: with systemDebugging=true the
// path is the predictable /tmp/<name>-<sid>, and SIDs are reused, so a
// previous shepherd's directory (with its files) can already be there.
// SetupScratchDir must still succeed and hand back an empty directory that
// is the cwd.
func TestSetupScratchDirClearsStaleDir(t *testing.T) {
	stale := fmt.Sprintf("/tmp/%s-%d", selfShortName(), os.Getpid())
	if err := os.MkdirAll(filepath.Join(stale, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stale) })
	t.Chdir(t.TempDir()) // restores the test's cwd afterwards

	got, err := SetupScratchDir(true)
	if err != nil {
		t.Fatalf("SetupScratchDir = %v, want nil", err)
	}
	if got != stale {
		t.Fatalf("path = %q, want %q", got, stale)
	}
	ents, err := os.ReadDir(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("scratch dir has %d stale entries (first %q), want empty", len(ents), ents[0].Name())
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// macOS /tmp is a symlink to /private/tmp; compare resolved paths.
	want, err := filepath.EvalSymlinks(stale)
	if err != nil {
		t.Fatal(err)
	}
	if wd != want {
		t.Fatalf("cwd = %q, want %q", wd, want)
	}
}
