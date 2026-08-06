// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// MAZ-179 forensic dump-at-fatal (NOT FOR MERGE).
//
// When a corruption-detecting throw fires in a shepherd's Go runtime, we
// hexdump the corrupted region so the writer can be identified BY CONTENT
// offline: ELF magic, ASCII strings, and ARM64 code all survive in the
// dump and can be grepped back to the exact source file + offset.
//
// The whole MAZ-179 fingerprint is "file content lands in shepherd Go heaps
// (mark bitmaps, objects, once-module BSS) with no kernel write path caught
// by any tripwire". Every write-checking probe has come back clean, so the
// strategy pivots to catching the DAMAGE after the fact and reading the
// payload directly.
//
// Everything here uses only local stack buffers and gwrite, so it does no
// heap allocation and emits no write barriers — safe from the GC/sweep
// contexts these hooks fire in.

package runtime

import "unsafe"

const maz179hexdigits = "0123456789abcdef"

// maz179RawSVC issues a raw Mazzy SVC (asm stub in sys_linux_arm64.s).
// [MAZ-179 tier-14] used only by the stale-TLB check below.
//
//go:noescape
func maz179RawSVC(num, a0, a1 uintptr) uintptr

// maz179TLBCheck compares a word read through the VA (via the TLB) with the
// same word read by the kernel through the linear map at the freshly-walked
// PA (SVC 0x104A). A mismatch proves this CPU's load used a stale TLB
// translation — the one mechanism that makes "same VA, two reads, different
// plausible contents, no writer" literally true. Runs FIRST in the dump
// sequence, before print traffic churns the TLB. One-way test: a match does
// not fully refute (the stale entry may already be evicted); a mismatch is
// conclusive.
func maz179TLBCheck(label string, addr uintptr) {
	if !maz179plausible(addr) {
		return
	}
	a := addr &^ 7
	viaVA := *(*uint64)(unsafe.Pointer(a))
	pa := maz179RawSVC(0x104A, a, 0)
	if pa == 0 {
		print("[MAZ179TLB] ", label, " va=", hex(a), " UNMAPPED viaVA=", hex(viaVA), "\n")
		return
	}
	viaPA := uint64(maz179RawSVC(0x104A, a, 1))
	if viaPA != viaVA {
		print("[MAZ179TLB] ", label, " va=", hex(a), " pa=", hex(uint64(pa)),
			" viaVA=", hex(viaVA), " viaPA=", hex(viaPA), " *** STALE-TLB MISMATCH ***\n")
	} else {
		print("[MAZ179TLB] ", label, " va=", hex(a), " pa=", hex(uint64(pa)),
			" word=", hex(viaVA), " match\n")
	}
}

// maz179plausible reports whether addr is not obviously bogus. It is a cheap
// sanity gate, not a page-table check: callers only ever pass addresses the
// runtime already handed them (mspan/gcBits/object interiors), which the
// detecting code has already dereferenced without faulting.
func maz179plausible(addr uintptr) bool { return addr >= 0x1000 }

// maz179dumpRegion emits a hex+ASCII dump of [addr, addr+n) via gwrite,
// 16 bytes per row, each row prefixed by its offset within the region.
func maz179dumpRegion(label string, addr uintptr, n uintptr) {
	print("[MAZ179DUMP] ", label, " @", hex(addr), " len=", n, "\n")
	if !maz179plausible(addr) {
		print("[MAZ179DUMP]   (skipped: null/low addr)\n")
		return
	}
	for off := uintptr(0); off < n; off += 16 {
		cnt := n - off
		if cnt > 16 {
			cnt = 16
		}
		var line [96]byte
		p := 0
		line[p] = '+'
		p++
		line[p] = maz179hexdigits[(off>>12)&0xf]
		p++
		line[p] = maz179hexdigits[(off>>8)&0xf]
		p++
		line[p] = maz179hexdigits[(off>>4)&0xf]
		p++
		line[p] = maz179hexdigits[off&0xf]
		p++
		line[p] = ' '
		p++
		base := addr + off
		for i := uintptr(0); i < cnt; i++ {
			b := *(*byte)(unsafe.Pointer(base + i))
			line[p] = maz179hexdigits[b>>4]
			p++
			line[p] = maz179hexdigits[b&0xf]
			p++
			line[p] = ' '
			p++
		}
		for i := cnt; i < 16; i++ {
			line[p] = ' '
			line[p+1] = ' '
			line[p+2] = ' '
			p += 3
		}
		line[p] = '|'
		p++
		for i := uintptr(0); i < cnt; i++ {
			b := *(*byte)(unsafe.Pointer(base + i))
			if b >= 0x20 && b < 0x7f {
				line[p] = b
			} else {
				line[p] = '.'
			}
			p++
		}
		line[p] = '|'
		p++
		line[p] = '\n'
		p++
		gwrite(line[:p])
	}
}

// maz179classify scans [addr, addr+n) for recognizable file signatures and
// prints a one-line verdict naming what kind of content is present. This is
// the "identify the writer by content" summary that survives even if the raw
// dump scrolls: ELF-magic count pins loader/image bytes, a long printable run
// pins text (bleve store, source, JSON), and the .maz magic pins plugin images.
func maz179classify(label string, addr uintptr, n uintptr) {
	if !maz179plausible(addr) || n < 4 {
		return
	}
	elf := 0
	maz := 0
	printable := 0
	run := 0
	longest := 0
	for i := uintptr(0); i < n; i++ {
		b := *(*byte)(unsafe.Pointer(addr + i))
		if b >= 0x20 && b < 0x7f {
			printable++
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
		if i+4 <= n {
			b0 := b
			b1 := *(*byte)(unsafe.Pointer(addr + i + 1))
			b2 := *(*byte)(unsafe.Pointer(addr + i + 2))
			b3 := *(*byte)(unsafe.Pointer(addr + i + 3))
			if b0 == 0x7f && b1 == 'E' && b2 == 'L' && b3 == 'F' {
				elf++
			}
			// ".maz" plugin image magic, seen as an ASCII tag in headers.
			if b0 == '.' && b1 == 'm' && b2 == 'a' && b3 == 'z' {
				maz++
			}
		}
	}
	print("[MAZ179DUMP] classify ", label, ": elfmagic=", elf, " mazmagic=", maz,
		" printable=", printable, "/", n, " longestASCIIrun=", longest, "\n")
}

// maz179DumpSpanMap prints, for the page containing p and its neighbours, which
// mheap span the arena span-map returns and whether that span actually contains
// the page. A live object whose page maps to a span that does NOT contain it
// proves the span-map / heapArena metadata is corrupt (as opposed to the object
// payload being scribbled). Reads only arena metadata, never p's own memory, so
// it is safe to call even when p itself is unmapped.
func maz179DumpSpanMap(p uintptr) {
	for d := -2; d <= 2; d++ {
		q := p + uintptr(d)*pageSize
		s := spanOf(q)
		if s == nil {
			print("[MAZ179DUMP] spanmap d=", d, " q=", hex(q), " span=nil\n")
			continue
		}
		contains := q >= s.base() && q < s.limit
		print("[MAZ179DUMP] spanmap d=", d, " q=", hex(q),
			" -> span=", hex(uintptr(unsafe.Pointer(s))),
			" base=", hex(s.base()), " limit=", hex(s.limit),
			" state=", uint8(s.state.get()), " spanclass=", uint8(s.spanclass),
			" elemsize=", s.elemsize, " contains=", contains, "\n")
	}
}

// maz179DumpSweep is invoked from mgcsweep.go right before the
// "sweep increased allocation count" throw. countAlloc read dense garbage out
// of s.gcmarkBits (nalloc >> nelems), so the damaged memory is the span's
// metadata and/or its mark-bit arena. Dump the mspan struct, both bit arenas,
// and the span's object data, classifying each so the payload is named.
func maz179DumpSweep(s *mspan) {
	print("[MAZ179DUMP] ===== SWEEP-INCREASED =====\n")
	if s == nil {
		print("[MAZ179DUMP] s=nil\n")
		return
	}
	// [tier-14] stale-TLB discriminator FIRST, before print traffic
	// churns the TLB: mspan struct + the popcounted gcmarkBits.
	maz179TLBCheck("mspan", uintptr(unsafe.Pointer(s)))
	if gm := uintptr(unsafe.Pointer(s.gcmarkBits)); maz179plausible(gm) {
		maz179TLBCheck("gcmarkBits", gm)
		// [tier-15b] which epoch list owns these bits right now?
		maz179GCBWhichList(gm)
	}
	print("[MAZ179DUMP] mspan s=", hex(uintptr(unsafe.Pointer(s))),
		" base=", hex(s.startAddr), " npages=", s.npages,
		" nelems=", s.nelems, " freeindex=", s.freeindex,
		" allocCount=", s.allocCount, " elemsize=", s.elemsize,
		" spanclass=", uint8(s.spanclass), " state=", uint8(s.state.get()),
		" allocBits=", hex(uintptr(unsafe.Pointer(s.allocBits))),
		" gcmarkBits=", hex(uintptr(unsafe.Pointer(s.gcmarkBits))), "\n")

	// The mspan struct itself — reveals whether nelems/gcmarkBits/allocCount
	// are file content vs sane values.
	sp := uintptr(unsafe.Pointer(s))
	maz179dumpRegion("mspan-struct", sp, 0xA0)
	maz179classify("mspan-struct", sp, 0xA0)

	// The gcmarkBits arena bytes — this is exactly what countAlloc popcounted
	// into the impossible nalloc.
	if gm := uintptr(unsafe.Pointer(s.gcmarkBits)); maz179plausible(gm) {
		maz179dumpRegion("gcmarkBits", gm, 256)
		maz179classify("gcmarkBits", gm, 256)
	}
	if ab := uintptr(unsafe.Pointer(s.allocBits)); maz179plausible(ab) {
		maz179dumpRegion("allocBits", ab, 128)
		maz179classify("allocBits", ab, 128)
	}

	// Arena span-map consistency around the span's own base — emit BEFORE the
	// objdata read, which touches 0x2040… heap pages that can take a demand
	// fault and truncate the rest of the dump (as seen in tier-9b sweep dumps).
	if s.startAddr != 0 {
		maz179DumpSpanMap(s.startAddr)
	}
	// The span's object data — in case the payload landed in live objects too.
	// LAST, for the truncation reason above.
	if s.startAddr != 0 {
		maz179dumpRegion("objdata", s.startAddr, 256)
		maz179classify("objdata", s.startAddr, 256)
	}
	print("[MAZ179DUMP] ===== END SWEEP-INCREASED =====\n")
}

// maz179DumpBadPointer is invoked from mbitmap.go badPointer right before the
// "found bad pointer in Go heap" throw. The stock path already dumps the object
// word-by-word (gcDumpObject); this adds a wide byte+ASCII view of the whole
// object plus a content classification, so a code/ELF/text payload is named.
func maz179DumpBadPointer(s *mspan, p, refBase, refOff uintptr) {
	print("[MAZ179DUMP] ===== BAD-POINTER p=", hex(p),
		" refBase=", hex(refBase), " refOff=", hex(refOff), " =====\n")
	// [tier-14] stale-TLB discriminator FIRST: the referencing slot (the
	// word that held the bad pointer), the bad target, and the mspan.
	if refBase != 0 {
		maz179TLBCheck("badptr-slot", refBase+refOff)
	}
	maz179TLBCheck("badptr-target", p)
	if s != nil {
		maz179TLBCheck("mspan", uintptr(unsafe.Pointer(s)))
	}
	if s != nil {
		print("[MAZ179DUMP] span base=", hex(s.base()), " limit=", hex(s.limit),
			" elemsize=", s.elemsize, " spanclass=", uint8(s.spanclass), "\n")
	}
	if refBase != 0 {
		n := uintptr(512)
		if s != nil && s.elemsize != 0 && s.elemsize < n {
			n = s.elemsize
		}
		maz179dumpRegion("badptr-object", refBase, n)
		maz179classify("badptr-object", refBase, n)
	}
	// Arena span-map consistency around the bad pointer — reads only arena
	// metadata, so emit this BEFORE touching p's own memory (which may be
	// unmapped and would fault, truncating the rest of the dump).
	maz179DumpSpanMap(p)
	// The pointed-to region itself, LAST: is what p points at file content, and
	// is it even readable? A fault here still leaves the span-map above intact.
	maz179dumpRegion("badptr-target", p, 256)
	maz179classify("badptr-target", p, 256)
	print("[MAZ179DUMP] ===== END BAD-POINTER =====\n")
}

// maz179GCBTrips caps [MAZ179GCB] reporting (approximate — plain counter,
// races only affect the cap, not correctness).
var maz179GCBTrips uint32

// maz179GCBCheckZero enforces the newMarkBits zero-at-handout invariant.
// [MAZ-179 tier-15] A nonzero mark-bits chunk at handout = the gcBits
// arena was recycled while a live span still points into it (epoch
// machinery raced) — the corruption caught at the moment of aliasing,
// with the aliased content in hand. Bounded scan (bytes = ceil(nelems/64)*8).
func maz179GCBCheckZero(p *gcBits, bytes uintptr) {
	base := uintptr(unsafe.Pointer(p))
	for off := uintptr(0); off < bytes; off += 8 {
		w := *(*uint64)(unsafe.Pointer(base + off))
		if w != 0 {
			maz179GCBTrips++
			if maz179GCBTrips <= 8 {
				print("[MAZ179GCB] NONZERO-AT-HANDOUT p=", hex(base),
					" bytes=", bytes, " off=", off, " word=", hex(w), "\n")
				maz179TLBCheck("gcb-handout", base+off)
			}
			return
		}
	}
}

// maz179GCBListScan reports which gcBitsArenas list contains addr, walking
// each chain (bounded). [MAZ-179 tier-15b] At sweep-throw time the span's
// gcmarkBits MUST lie in the current-epoch arenas; "previous" means the
// span is sweeping with LAST epoch's bits (epoch raced past it), "free"
// means the arena was recycled under the span's feet, "none" means the
// pointer is into memory gcBitsArenas no longer owns at all.
func maz179GCBWhichList(addr uintptr) {
	print("[MAZ179GCB] whichlist addr=", hex(addr),
		" next=", maz179GCBIn(gcBitsArenas.next, addr),
		" current=", maz179GCBIn(gcBitsArenas.current, addr),
		" previous=", maz179GCBIn(gcBitsArenas.previous, addr),
		" free=", maz179GCBIn(gcBitsArenas.free, addr), "\n")
}

func maz179GCBIn(a *gcBitsArena, addr uintptr) bool {
	for n := 0; a != nil && n < 64; n++ {
		base := uintptr(unsafe.Pointer(a))
		if addr >= base && addr < base+unsafe.Sizeof(*a) {
			return true
		}
		a = a.next
	}
	return false
}
