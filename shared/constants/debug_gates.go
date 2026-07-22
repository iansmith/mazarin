// debug_gates.go - compile-time gates for debug instrumentation shared
// between kernel and userspace.

package constants

// Maz15Debug gates the MAZ-15 debug instrumentation: the buddy free-page
// poison tripwire in kmazarin/kmem/buddy.go and the module-image CRC
// checkpoints in mazarin/mazhost/load.go. Off by default — flip to true to
// re-arm the tripwires when hunting heap-corruption regressions.
const Maz15Debug = false
