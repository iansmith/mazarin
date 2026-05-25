// Package transferpayload holds the magic byte payload used by the
// MAZ-50 boot-time Mode 1 smoke test. Shared between the transfertest
// (server) and transferprobe (client) shepherds so the payload stays in
// one place — drift between client write and server check would make
// the test silently fail.
package transferpayload

// Magic is what transferprobe writes via Reserve+Writer; transfertest's
// server-side check asserts bytes.HasPrefix(slab.Bytes(), Magic).
var Magic = []byte("hello mode-1 transfer\x00")
