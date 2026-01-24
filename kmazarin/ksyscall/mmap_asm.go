//go:build !test_stubs

package ksyscall

// getRuntimeConfig is provided by main package via go:linkname.
func getRuntimeConfig() interface{}
