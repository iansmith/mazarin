package sys

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// SetupScratchDir creates a per-shepherd scratch directory under /tmp and
// chdirs into it. Every ordinary shepherd should call this once, immediately
// after WaitForCoreServices, before issuing any relative-path file I/O.
//
// Path format:
//   systemDebugging=true:  /tmp/<name>-<sid>           (predictable; for hand-inspection)
//   systemDebugging=false: /tmp/<name>-<sid>-<rand32>  (collision-free across SID reuse)
//
// Under normal operation systemDebugging should be false; the predictable
// form is the development affordance. SIDs are aggressively reused, so the
// random suffix is what guarantees a fresh, empty directory across the
// lifetime of a shepherd-name + SID pair.
//
// Cleanup of stale directories is not handled here — /tmp is a ramdisk that
// resets on reboot, and we do not yet have a controlled-shutdown path.
//
// Returns the absolute path of the created directory.
func SetupScratchDir(systemDebugging bool) (string, error) {
	name := selfShortName()
	sid := os.Getpid()

	var path string
	if systemDebugging {
		path = fmt.Sprintf("/tmp/%s-%d", name, sid)
	} else {
		var rand32 [4]byte
		if _, err := rand.Read(rand32[:]); err != nil {
			return "", fmt.Errorf("scratchdir: getrandom: %w", err)
		}
		path = fmt.Sprintf("/tmp/%s-%d-%s", name, sid, hex.EncodeToString(rand32[:]))
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("scratchdir: mkdir %s: %w", path, err)
	}
	if err := os.Chdir(path); err != nil {
		return "", fmt.Errorf("scratchdir: chdir %s: %w", path, err)
	}
	return path, nil
}

// selfShortName extracts the shepherd's own short name from os.Args[0].
// runshepherd sets Args[0] to the launch filename, e.g. "/fti.elf" or
// "/mail.maz". We strip the leading slash and any .elf/.maz suffix.
func selfShortName() string {
	n := os.Args[0]
	if i := strings.LastIndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	n = strings.TrimSuffix(n, ".elf")
	n = strings.TrimSuffix(n, ".maz")
	if n == "" {
		return "unknown"
	}
	return n
}
