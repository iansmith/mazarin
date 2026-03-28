package main

import _ "embed"

//go:embed kernel.toml
var EmbeddedKernelConfig []byte
