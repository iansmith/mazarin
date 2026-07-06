package main

import _ "embed"

// Staged from config/kernel.arm64.toml by the root Taskfile's
// stage-kernel-config-arm64 task. Per-arch staged name — see MAZ-153.
//
//go:embed kernel_arm64.toml
var EmbeddedKernelConfig []byte
