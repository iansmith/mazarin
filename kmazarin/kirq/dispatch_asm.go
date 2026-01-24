//go:build !test_stubs

package kirq

// Forward declarations for functions provided via go:linkname from main package.
// These are excluded during test builds and replaced by stubs.

// breadcrumbString is provided by main package via linkname
// Used for direct UART output to avoid console recursion in panic paths
func breadcrumbString(s string)

// breadcrumbHex is provided by main package via linkname
// Writes a 64-bit hex value directly to UART
func breadcrumbHex(val uint64)
