#!/bin/sh
# Inside-container smoke driver.
#
# Mounted at /work/smoke/run-smoke.sh. Expects cross-compiled mazlink/mazgo
# binaries at /work/bin/{mazlink,mazgo}.linux-{amd64,arm64} (both arches
# are built by the host; this script picks the one matching the container).
#
# The script runs two tracks in order:
#
#   1. Reference track  — build plugin.so with stock Go + cgo + external linker.
#                         This is the known-good Go plugin shape we are
#                         trying to reproduce. Always runs.
#
#   2. Candidate track  — swap mazlink into $GOTOOLDIR/link and build the
#                         same source as plugin.maz via mazgo with
#                         CGO_ENABLED=0 and -linkmode=internal. May fail
#                         while mazlink's patches are incomplete; that is
#                         the point — failures here enumerate the remaining
#                         gaps.
#
# After both tracks, if both plugins exist, run elf-diff to summarize the
# structural delta. Finally, attempt to dlopen plugin.maz from the host and
# call Hello().
set -eu

UNAME_M="$(uname -m)"
case "${UNAME_M}" in
    x86_64)  GOARCH=amd64 ;;
    aarch64) GOARCH=arm64 ;;
    *)       echo "unsupported container arch: ${UNAME_M}" >&2; exit 1 ;;
esac

MAZLINK="/work/bin/mazlink.linux-${GOARCH}"
MAZGO="/work/bin/mazgo.linux-${GOARCH}"
ELFDIFF="/work/bin/elf-diff.linux-${GOARCH}"

TOOLDIR="$(go env GOTOOLDIR)"

echo "==> container go version: $(go version)"
echo "==> container arch: ${UNAME_M} (GOARCH=${GOARCH})"
echo "==> GOTOOLDIR: ${TOOLDIR}"
echo "==> mazlink:   ${MAZLINK}"
echo "==> mazgo:     ${MAZGO}"

# ---------------------------------------------------------------------------
# Reference: build plugin.so with stock Go + cgo (external linker).
# ---------------------------------------------------------------------------
echo
echo "==> REFERENCE: building plugin.so (stock go, -buildmode=plugin, cgo)"
cd /work/smoke/plugin
CGO_ENABLED=1 go build -buildmode=plugin -o /tmp/plugin.so .
file /tmp/plugin.so

# ---------------------------------------------------------------------------
# Host: stock go + stock link, ordinary cgo program that dlopens the plugin.
# ---------------------------------------------------------------------------
echo
echo "==> building host (stock go + stock link)"
cd /work/smoke/host
go build -o /tmp/host .
file /tmp/host

# ---------------------------------------------------------------------------
# Candidate: swap mazlink in, build plugin.maz with mazgo + -linkmode=internal.
# ---------------------------------------------------------------------------
echo
echo "==> CANDIDATE: swapping mazlink into \${GOTOOLDIR}/link"
cp "${MAZLINK}" "${TOOLDIR}/link"

# GOROOT is baked into the mazgo binary at cross-compile time — override to
# the container's GOROOT so package lookups succeed.
GOROOT="$(go env GOROOT)"
export GOROOT
echo "==> using GOROOT=${GOROOT}"

echo "==> building plugin.maz (mazgo -buildmode=plugin -linkmode=internal -dlopen-host-packages=..., CGO_ENABLED=0)"
cd /work/smoke/plugin
MAZLINK_BUILT=1
# Phase 2: -dlopen-host-packages points the linker at the policy file that
# lists Go packages resolved against the host at plugin load time (runtime,
# internal/runtime/..., etc.). Plugin should ship zero runtime code; callers
# of runtime.* are routed through .plt/JUMP_SLOT. See design/MAZARIN-DLOPEN.md.
POLICY=/work/mazlink-patches/policy/dlopen-host-packages.txt
CGO_ENABLED=0 "${MAZGO}" build \
    -buildmode=plugin \
    "-ldflags=-linkmode=internal -dlopen-host-packages=${POLICY}" \
    -o /tmp/plugin.maz . || MAZLINK_BUILT=0

if [ "${MAZLINK_BUILT}" = "1" ]; then
    file /tmp/plugin.maz
    # Copy plugin.maz to /work so the host can inspect it after the run.
    cp /tmp/plugin.maz /work/build/plugin.maz 2>/dev/null || true

    # ---- Phase 2 exit criteria (design/MAZARIN-DLOPEN.md §9 Phase 2) ----
    echo
    echo "==> Phase 2 exit criteria for plugin.maz:"
    SIZE=$(stat -c %s /tmp/plugin.maz 2>/dev/null || wc -c < /tmp/plugin.maz)
    echo "    size: ${SIZE} bytes"

    # musl's nm reports dynamic UNDEF entries without the 'U' tag; use
    # readelf --dyn-syms and match on section index "UND" instead.
    UNDEF_ALL=$(readelf --dyn-syms /tmp/plugin.maz 2>/dev/null | awk '$7=="UND" && $8!=""{print $8}' | wc -l | tr -d ' ')
    UNDEF_RT=$(readelf --dyn-syms /tmp/plugin.maz 2>/dev/null | awk '$7=="UND" && $8 ~ /^runtime\./{print $8}' | wc -l | tr -d ' ')
    UNDEF_IRT=$(readelf --dyn-syms /tmp/plugin.maz 2>/dev/null | awk '$7=="UND" && $8 ~ /^internal\/runtime/{print $8}' | wc -l | tr -d ' ')
    echo "    UNDEF dynsym total:                 ${UNDEF_ALL}"
    echo "    UNDEF dynsym runtime.*:             ${UNDEF_RT}"
    echo "    UNDEF dynsym internal/runtime/...:  ${UNDEF_IRT}"

    DEFINED_RT=$(nm /tmp/plugin.maz 2>/dev/null | awk '$2=="T" && $3 ~ /^runtime\./{print $3}' | wc -l | tr -d ' ')
    echo "    DEFINED T runtime.* symbols (want 0): ${DEFINED_RT}"
    if [ "${DEFINED_RT}" != "0" ]; then
        echo "    (first 10):"
        nm /tmp/plugin.maz 2>/dev/null | awk '$2=="T" && $3 ~ /^runtime\./{print "      "$3}' | head -10
    fi

    echo "    readelf -d (dynamic tags we care about):"
    readelf -d /tmp/plugin.maz 2>/dev/null | grep -E 'NEEDED|JMPREL|PLTRELSZ|PLTREL|PLTGOT|SONAME' | sed 's/^/      /' || true
fi

# ---------------------------------------------------------------------------
# Phase 3: host-probe. Build a trivial Go binary with mazgo+mazlink, passing
# -dlopen-host-exports=<policy>, and verify that policy-matched symbols
# appear in its .dynsym as GLOBAL DEFAULT FUNC. This is the Phase 3 exit
# criterion from design/MAZARIN-DLOPEN.md §9 — it does not depend on Phase 4
# (mazdl loader) so it can run alongside Phase 2.
# ---------------------------------------------------------------------------
echo
echo "==> PHASE 3: building host-probe with -dlopen-host-exports"
cd /work/smoke/host-probe
PHASE3_BUILT=1
CGO_ENABLED=0 "${MAZGO}" build \
    "-ldflags=-linkmode=internal -dlopen-host-exports=${POLICY}" \
    -o /tmp/host-probe . || PHASE3_BUILT=0

if [ "${PHASE3_BUILT}" = "1" ]; then
    file /tmp/host-probe
    echo
    echo "==> Phase 3 exit criteria for host-probe:"

    # Criterion 1: nm shows runtime.mallocgc as defined T (text).
    if nm /tmp/host-probe 2>/dev/null | awk '$2=="T" && $3=="runtime.mallocgc"{found=1} END{exit !found}'; then
        echo "    [OK] nm: runtime.mallocgc defined (T)"
    else
        echo "    [FAIL] nm: runtime.mallocgc not found as T"
    fi

    # Criterion 2: readelf --dyn-syms shows runtime.mallocgc as GLOBAL DEFAULT FUNC
    # (not UND, not LOCAL, not HIDDEN).
    DYN_LINE=$(readelf --dyn-syms /tmp/host-probe 2>/dev/null | awk '$8=="runtime.mallocgc"{print; exit}')
    if [ -n "${DYN_LINE}" ]; then
        echo "    [OK] dyn-syms entry for runtime.mallocgc:"
        echo "         ${DYN_LINE}"
        # Spot-check: GLOBAL, FUNC, not UND. readelf column order is
        # "Type Bind Vis Ndx Name" so the line reads "FUNC GLOBAL ..." —
        # accept both orderings so the check doesn't depend on readelf's
        # future cosmetic choices.
        case "${DYN_LINE}" in
            *GLOBAL*FUNC*|*FUNC*GLOBAL*) echo "    [OK] GLOBAL FUNC confirmed" ;;
            *)                           echo "    [FAIL] expected GLOBAL FUNC in entry" ;;
        esac
        case "${DYN_LINE}" in
            *UND*runtime.mallocgc*) echo "    [FAIL] runtime.mallocgc is UND (should be defined)" ;;
        esac
    else
        echo "    [FAIL] readelf --dyn-syms has no entry for runtime.mallocgc"
    fi

    # Summary counts by policy category.
    DYN_RT=$(readelf --dyn-syms /tmp/host-probe 2>/dev/null | awk '$7!="UND" && $8 ~ /^runtime\./{print $8}' | wc -l | tr -d ' ')
    DYN_IRT=$(readelf --dyn-syms /tmp/host-probe 2>/dev/null | awk '$7!="UND" && $8 ~ /^internal\/runtime/{print $8}' | wc -l | tr -d ' ')
    DYN_IABI=$(readelf --dyn-syms /tmp/host-probe 2>/dev/null | awk '$7!="UND" && $8 ~ /^internal\/abi\./{print $8}' | wc -l | tr -d ' ')
    echo "    exported dynsym runtime.*:             ${DYN_RT}"
    echo "    exported dynsym internal/runtime/...:  ${DYN_IRT}"
    echo "    exported dynsym internal/abi.*:        ${DYN_IABI}"

    # Smoke run: the program itself should still work.
    echo "    running host-probe..."
    /tmp/host-probe || echo "    [FAIL] host-probe exited non-zero"
else
    echo "    [FAIL] host-probe build failed — skipping Phase 3 checks"
fi

# ---------------------------------------------------------------------------
# Phase 4: build host-mazdl (same role as smoke/host, but uses mazdl.Open
# instead of stdlib plugin.Open) and run it against plugin.maz. This is the
# end-to-end proof that a Phase-2 plugin can be dlopened against a Phase-3
# host with zero runtime duplication.
# ---------------------------------------------------------------------------
echo
echo "==> PHASE 4: building host-mazdl (mazgo + mazlink + -dlopen-host-exports)"
cd /work/smoke/host-mazdl
PHASE4_BUILT=1
CGO_ENABLED=0 "${MAZGO}" build \
    "-ldflags=-linkmode=internal -dlopen-host-exports=${POLICY}" \
    -o /tmp/host-mazdl . || PHASE4_BUILT=0

if [ "${PHASE4_BUILT}" = "1" ]; then
    file /tmp/host-mazdl
    echo
    if [ "${MAZLINK_BUILT}" = "1" ]; then
        echo "==> running host-mazdl against plugin.maz"
        /tmp/host-mazdl /tmp/plugin.maz || echo "    [FAIL] host-mazdl exited non-zero"
    else
        echo "==> plugin.maz not built; skipping host-mazdl load test"
    fi
else
    echo "    [FAIL] host-mazdl build failed"
fi

# ---------------------------------------------------------------------------
# ELF-diff: structural comparison (ref vs candidate).
# ---------------------------------------------------------------------------
echo
if [ -x "${ELFDIFF}" ] && [ "${MAZLINK_BUILT}" = "1" ]; then
    echo "==> running elf-diff plugin.so vs plugin.maz"
    "${ELFDIFF}" /tmp/plugin.so /tmp/plugin.maz || true
elif [ -x "${ELFDIFF}" ]; then
    echo "==> plugin.maz not built — running elf-diff plugin.so vs itself (ref shape only)"
    "${ELFDIFF}" /tmp/plugin.so /tmp/plugin.so || true
else
    echo "==> elf-diff binary not present at ${ELFDIFF}; skipping structural diff"
fi

# ---------------------------------------------------------------------------
# Load test: can the host actually dlopen plugin.maz and call Hello()?
# ---------------------------------------------------------------------------
if [ "${MAZLINK_BUILT}" = "1" ]; then
    echo
    echo "==> running host against plugin.maz"
    /tmp/host /tmp/plugin.maz
else
    echo
    echo "==> plugin.maz was not built; skipping host load test"
    echo "    (the build failure above enumerates the next mazlink gap)"
    exit 1
fi
