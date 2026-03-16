//go:build linux && riscv64

package sys

// LoadMazByName returns the platform-appropriate filename for a dynamic module.
// On RISC-V, the Go compiler does not support -buildmode=pie, so modules are
// built as ET_EXEC binaries with fixed load addresses (using -T <slot_addr>).
// These use the .mzr extension to distinguish them from true PIE .maz binaries.
//
// basePath should be the path without extension, e.g. "/fs" or "/helloworld".
func LoadMazByName(basePath string) string {
	return basePath + ".mzr"
}
