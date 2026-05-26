// Package sharepayload holds constants shared between the sharetest (sender)
// and shareprobe (consumer) shepherds. Both sides must agree on the magic
// patterns to avoid silent test failures.
package sharepayload

const (
	// PatternA is written by the sender into the Slab before sharing.
	// shareprobe verifies this pattern is visible through Share.AsBytes().
	PatternA = byte(0xA1)

	// PatternB is written by shareprobe through Share.AsBytes() before
	// calling Release. sharetest verifies this pattern is visible in the
	// Slab after the Release IPC arrives (proves bidirectional mapping).
	PatternB = byte(0xB2)

	// SubRangeOffset and SubRangeLength define the byte range used for the
	// sub-range share test. The offset is deliberately not page-aligned so
	// the boundary-page exposure is exercised, and the length is sub-page.
	SubRangeOffset = 100
	SubRangeLength = 200
)
