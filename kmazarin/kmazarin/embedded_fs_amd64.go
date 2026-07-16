package main

import _ "embed"

// Staged by the root Taskfile's stage-embedded-fs-x86_64 task. Each
// architecture embeds from its own staged file so parallel cross-arch builds
// cannot race on a shared destination (MAZ-153).
//
//go:embed fs_amd64.elf
var EmbeddedFSElf []byte
