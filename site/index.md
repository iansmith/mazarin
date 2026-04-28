---
layout: default
title: mazarin, an introduction
author: iansmith
---

## [News (last updated Apr 28)](news.md). Go-linker plugins, Mail client, Full-text search, Go 1.26.2, and File-backed mmap!

## [Mancini API Reference](mancini/index.md)

## What It Means

mazarin now has its own patched Go linker that emits relocatable `.maz`
plugins without ever falling back to the system linker -- every
shepherd in `startup.toml` is loaded by a single generic
`/shepherd.elf` host via a runtime loader called mazdl. There is a
working email client backed by a maildb shepherd (BadgerDB +
mbox import) and an fti shepherd (full-text search via bleve). The
toolchain is on Go 1.26.2. `MAP_SHARED` writable mmap is supported
with dirty-page write-back to ext2 on munmap and shepherd death --
which is what makes BadgerDB and bleve usable. Rachel (the window
manager) gained focused/unfocused title bars, click-drag window moves
with screen clipping, and alpha-composited drag previews. Sadly, we
also removed RISC-V support -- see the news page for details.


## [Quick Start: build and run mazarin](quickstart.md)


# mazarin, what this is
mazarin is a go-centric and indeed go-only operating system.  (mazarin is not 
capitalized.)  In the future mazarin may 
support languages like Javascript or Python for which interpreters written in go
are available. Similarly, programs in Webassembly format may be supported for some
compiled languages as there are Webassembly virtual machines written in go.
Support for languages other than go is possible, but not a priority.

# mazarin, what this is not
mazarin is not unix. mazarin does not argue that everything should be a file--it
argues that everything is part of the UI. mazarin is not byte streams which work 
by read/write/open/close and file descriptors. If that is what you want, I suggest 
you try Linux; it is quite good.  mazarin may 
offer some compatibility with Linux in an effort to accelerate support for features
that are important to mazarin, but the compatibilty is not the goal.

# mazarin, what this is
mazarin's model is that everything is part of the UI.  The UI is not only
the most important thing in mazarin, but it is nearly the _only_ thing.  The UI
of mazarin does not offer the programming model of any existing window system.

## No C
mazarin does no use C code at all--nor cgo.  go or go assembly is used in
the build and boot processes.
All the tools for building mazarin are written in go.  The boot process uses bootloaders 
written in go for x86_64 and arm64.  If you want to get _pedantic_, you could argue that the code
before the bootloaders--such as firmware on x86_64 and DTB construction on arm64--
is written in C, but this is far from mazarin's concern.  

I come to bury C, not to praise it. I harbor no ill-will towards C code, it can
be highly useful and, with a strong linter, fairly clean and straightforward.  I have
partly built Mazarin to show you dont _need_ C anymore.  The world has changed
a great deal since C's debut in the 1970s.

# mazarin, what this is
mazarin runs on bare metal and has a go-centric programming model. It is not just
that the code is written go, it is that it does not offer the c/unix programming
model.  Most of programming model is focused on channels and goroutines.  mazarin
runs on paravirtualized hardware via a hypervisor but a middle-term goal is to
run on a Raspberry Pi W zero version 2.

# mazarin, what this is not
mazarin is not optimized.  Linux has been being optimized for the last
25 years and that continues today.  Linux's performance is truly astonishing--but 
performance is not currently an area of focus for mazarin.

# mazarin, what this is and what this is not
mazarin was written by AI.  I have written precisely zero lines of code in
this repository.  I have written some of the documentation, such as this file,
because what it communicates is too important to allow an AI to write it. (This
may be human pride at work.) You may have concerns that the AI may have put code with
intended or unintended security flaws in mazarin. You are wise to have have such
concerns. I would recommend that you only run this code under a hypervisor that
you have confidence in.

> Honey, look! I vibecoded an operating system!

