package main

import _ "embed"

// Staged from config/kernel.amd64.toml by the root Taskfile's
// stage-kernel-config-x86_64 task. Per-arch staged name — see MAZ-153.
//
//go:embed kernel_amd64.toml
var EmbeddedKernelConfig []byte
