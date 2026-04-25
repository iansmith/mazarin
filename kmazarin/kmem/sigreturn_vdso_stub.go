package kmem

// SigreturnVDSOVA is unused on ARM64 and x86_64.
// ARM64: EL0 can execute kernel pages via TTBR1 (no UXN bit set).
// x86_64: Signal delivery uses a different mechanism.
const SigreturnVDSOVA uintptr = 0

// SigreturnVDSOPA is unused on non-RISC-V architectures.
var SigreturnVDSOPA uintptr

// InitSigreturnVDSO is a no-op on ARM64 and x86_64.
func InitSigreturnVDSO() {}

// mapSigreturnVDSOInProcessL0 is a no-op on ARM64 and x86_64.
func mapSigreturnVDSOInProcessL0(l0PA uintptr) {}
