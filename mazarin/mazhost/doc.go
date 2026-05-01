// Package mazhost provides host-side support for loading .maz dynamic
// modules into a shepherd's address space. It wraps the kernel's LoadMaz
// syscall, runtime moduledata registration, and MazarinShepherd interface
// injection.
//
// # Build Tag: mazhost
//
// All files in this package require the "mazhost" build tag. Only shepherds
// that load .maz modules — currently rachel and linux — build with
// -tags mazhost and the shepherd overlay.
//
// Shepherds that do not load .maz modules (clocks, fs, fontsvc, etc.)
// do not import this package and do not need the tag. Plain host builds
// (go build ./mazarin/...) skip these files entirely, avoiding undefined
// symbol errors.
package mazhost
