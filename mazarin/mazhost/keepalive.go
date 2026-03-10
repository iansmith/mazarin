// keepalive.go forces the Go linker to retain runtime.MazKeepAliveSymbols
// and transitively all runtime functions it references. Without this,
// the linker DCEs runtime functions that the priest binary doesn't call
// directly but that .maz modules need via thin stub imports.
//
// The priest overlay defines runtime.MazKeepAliveSymbols which stores
// function references into an exported global array. By calling it from
// a //go:noinline init function here, the linker must include it and
// all functions it references.
//
// NOTE: This file requires the priest overlay. Without it,
// runtime.MazKeepAliveSymbols doesn't exist and the build will fail.
// Any priest that loads .maz modules must import this package
// (use import _ "mazzy/mazarin/mazhost" if not otherwise needed).
package mazhost

import "runtime"

// MazSymbolKeepAlive holds a reference to runtime.MazKeepAliveSymbols.
// Exported so the linker cannot eliminate stores to it.
var MazSymbolKeepAlive any

//go:noinline
func init() {
	MazSymbolKeepAlive = runtime.MazKeepAliveSymbols
}
