# Unicorn Engine licensing notice

This directory contains an independently written runtime adapter. It does not
vendor, copy, or redistribute Unicorn Engine source code, headers, binaries, or
test fixtures.

Unicorn Engine is a separate upstream project distributed under GNU GPL v2;
some upstream headers and bindings carry additional notices. Installing,
linking, loading, or redistributing Unicorn may therefore create obligations
that do not apply to ARAM's default pure-Go build. Review Unicorn's current
upstream `COPYING`, `COPYING_GLIB`, and related notices for the exact version
you use. Do not ship this development adapter or a Unicorn binary as part of an
ARAM product without an explicit licensing review.

The adapter contains a small cgo shim that declares only the public function
signatures it needs. The shim resolves a user-installed shared library through
the operating system loader and invokes those functions C-to-C; it does not
link or bundle Unicorn at build time.

The Windows shim also serializes calls on a native worker thread so Unicorn's
own handled translation-buffer paging faults do not run on a Go-managed
thread. This is interoperability glue only; it does not reproduce Unicorn
implementation code.

The high-level idea of using Unicorn as a differential oracle was informed by
public emulator architecture discussions. No source code or fixtures were
copied from `msm5xxx-emulator`.
