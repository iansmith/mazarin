//go:build linux

package sys

// LoadMazByName returns the platform-appropriate filename for a dynamic module.
// On ARM64 and AMD64, the Go compiler supports -buildmode=pie, so modules are
// built as ET_DYN (PIE) binaries that can be loaded at any userspace address.
// These use the .maz extension.
//
// basePath should be the path without extension, e.g. "/fs" or "/helloworld".
func LoadMazByName(basePath string) string {
	return basePath + ".maz"
}
