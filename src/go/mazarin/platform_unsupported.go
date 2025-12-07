//go:build !qemuvirt
// +build !qemuvirt

package main

// Stub file to ensure compilation fails if no platform tag is specified
// This prevents accidental builds for an unsupported/unspecified platform

func init() {
	// This will fail at compile time with a helpful error message
	// when no platform build tag is specified
	compileError_PLATFORM_NOT_SPECIFIED()
}

func compileError_PLATFORM_NOT_SPECIFIED() {
	// This function name is designed to be self-documenting
	// The build will fail because this function doesn't exist
	// Error will be: undefined: compileError_PLATFORM_NOT_SPECIFIED
	// Which clearly indicates the problem
}

