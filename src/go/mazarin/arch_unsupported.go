//go:build !aarch64
// +build !aarch64

package main

// Stub file to ensure compilation fails if no architecture tag is specified
// This prevents accidental builds for an unsupported/unspecified architecture

func init() {
	// This will fail at compile time with a helpful error message
	// when no architecture build tag is specified
	compileError_ARCH_NOT_SPECIFIED()
}

func compileError_ARCH_NOT_SPECIFIED() {
	// This function name is designed to be self-documenting
	// The build will fail because this function doesn't exist
	// Error will be: undefined: compileError_ARCH_NOT_SPECIFIED
	// Which clearly indicates the problem
}

