# mazlink-patches

Source overlays for `mazlink`, our patched copy of `cmd/link` that emits
plugin-shape ELF output without cgo or external linking.

## Layout

Files in this tree mirror `$GOROOT/src/`. For example:

```
mazlink-patches/cmd/link/internal/ld/config.go
```

overlays:

```
$GOROOT/src/cmd/link/internal/ld/config.go
```

The `gen-overlay` tool walks this tree and emits a JSON overlay consumed by
`go build -overlay=...`. An empty tree produces an empty overlay — an
unpatched `bin/mazlink` identical to stock `link`.

## Build

`bin/mazlink` is produced by the root Taskfile target `mazlink-build`,
gated by `test -f bin/mazlink`. The target:

1. Runs `gen-overlay -type mazlink` to produce `build/mazlink-overlay.json`.
2. Runs `go build -overlay=build/mazlink-overlay.json -o bin/mazlink cmd/link`.

## Scope

The design doc (`design/GO-PLUGIN-SPIKE.md`) identifies three gaps in the
Go internal linker that we fill here:

1. `.init_array` entry that calls `runtime.plugin_lastmoduleinit`.
2. Runtime-symbol export to `.dynsym` with GLOBAL DEFAULT visibility.
3. Full dynamic relocation coverage (JUMP_SLOT + TLS) for linux/arm64 and
   linux/amd64.

Plus gate removals in `config.go` to allow `BuildModePlugin` with the
internal linker, and DCE pinning so the runtime surface survives.

riscv64 is out of scope for Phase 1.
