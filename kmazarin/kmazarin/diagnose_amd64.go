//go:build amd64 && !test_stubs

package main

// launchAllgsDiagnostic is available on amd64 for debugging allgs corruption.
// Currently disabled — allgs corruption appears fixed by Thread 0 CR3/TLS fixes.
func launchAllgsDiagnostic() {}
