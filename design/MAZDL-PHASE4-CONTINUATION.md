# mazdl Phase 4 — Continuation Pointer (retired)

Both the original reason for this pointer doc (unblocking Phase 4
SIGILL) and its follow-up (the loader-side `rewriteHostFuncvals`
workaround) have closed. As of 2026-04-18:

- mazlink option A landed on both arm64 and amd64 — see
  `mazlink-patches/cmd/link/internal/{arm64,amd64}/asm.go` and
  `design/MAZARIN-DLOPEN.md §3 "Host-policy funcvals"`. The host
  side also has a `mangleTypeSym` patch so host and plugin agree
  on hashed `type:.<hash>` dynsym names.
- `rewriteHostFuncvals` has been removed from
  `mazarin/mazdl/open.go`. `$GO tool task mazlink-smoke` still
  passes Phase 4 exits #1–#4 on arm64 without any loader-side
  workaround.

Open Phase 4 work continues in the canonical design doc:

- **design/MAZARIN-DLOPEN.md §9 Phase 4 "Open work to close
  Phase 4"** — amd64 runtime validation (`reloc_amd64.go` +
  container arch toggle in `mazlink-smoke`).
- **memory/mazlink_funcval_dead_reloc_bug.md** — historical
  post-mortem of the original SIGILL; retained for reference.

This file is kept as a breadcrumb; pick up new work from the
design doc.
