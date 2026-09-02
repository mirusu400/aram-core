//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// White-box tests for the native JIT's software TLB. cpu/conformance cannot
// reach these: it maps a fixed three-page layout and compares two backends that
// share the interpreter's memory path, so a bug in what the TLB is *allowed* to
// cache - a page holding translated code, a page only partly inside its region,
// two pages that collide in the direct-mapped table - would either be invisible
// or present in both backends. These map their own layouts and assert the
// behaviour directly.

func nativeBackend(t *testing.T) *Backend {
	t.Helper()
	b := NewNativeJIT()
	if b.nativeBlocks == nil {
		t.Skip("native JIT unavailable on this host")
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func run(t *testing.T, b *Backend, pc uint32, budget uint64) cpu.Result {
	t.Helper()
	return b.Run(context.Background(), pc, cpu.ModeThumb, budget)
}

// TestNativeInlineStoreInvalidatesSelfModifiedCode is the safety property the
// whole write-half design exists for. Native blocks store to guest memory
// directly, without passing through smcInvalidate, so a page holding translated
// code must never be reachable that way.
//
// Getting a real test out of that needs care. The guest rewrites the
// instruction its own block was translated from, once per run with a different
// replacement, so a stale translation shows up as the previous generation's
// value surviving into the next run. A single rewrite would prove nothing: the
// first store always misses the cold TLB and goes through the interpreter
// anyway. The rewrite is also preceded by a store to a FAR address on the same
// page - past the end of the translated code, so it does not invalidate
// anything. That is what makes the page's write entry a live candidate at the
// moment the self-modifying store runs, which is exactly the situation the
// write half must refuse to cache.
func TestNativeInlineStoreInvalidatesSelfModifiedCode(t *testing.T) {
	b := nativeBackend(t)
	mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)

	if err := b.WriteMemory(0x1000, []byte{
		0x01, 0x20, // movs r0, #1     ; the instruction that gets rewritten
		0x0a, 0x80, // strh r2, [r1]   ; far store: same page, outside the code
		0x23, 0x80, // strh r3, [r4]   ; self-modifying store, back at 0x1000
		0x00, 0xbe, // bkpt
	}); err != nil {
		t.Fatal(err)
	}
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR1: 0x1f80, cpu.RegisterR2: 0xffff, cpu.RegisterR4: 0x1000,
	} {
		if err := b.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	// Each run executes the instruction the previous run wrote, then writes the
	// next one.
	want := uint32(1)
	for _, next := range []uint32{9, 7, 5, 3} {
		if err := b.WriteRegister(cpu.RegisterR3, 0x2000|next); err != nil { // movs r0,#next
			t.Fatal(err)
		}
		if r := run(t, b, 0x1000, 16); r.Err != nil || r.Reason != cpu.StopBreakpoint {
			t.Fatalf("run writing movs r0,#%d = %+v", next, r)
		}
		if got := register(t, b, cpu.RegisterR0); got != want {
			t.Fatalf("r0 = %d, want %d (a self-modifying store was served inline, "+
				"leaving a stale translation)", got, want)
		}
		want = next
	}
}

// TestNativeInlineStoreToExecutableDataStaysCoherent is the other half of the
// same property: a store elsewhere in the same read-write-execute region - the
// KTF/WIPI mapping, where the guest blitter writes pixels into the image it
// executes from - must be served inline and still be visible to the host.
func TestNativeInlineStoreToExecutableDataStaysCoherent(t *testing.T) {
	b := nativeBackend(t)
	mustMap(t, b, 0x1000, 0x4000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)

	// loop: str r0,[r1]; adds r1,#4; subs r2,#1; bne loop; bkpt
	if err := b.WriteMemory(0x1000, []byte{
		0x08, 0x60, // str  r0, [r1, #0]
		0x04, 0x31, // adds r1, #4
		0x01, 0x3a, // subs r2, #1
		0xfb, 0xd1, // bne  loop
		0x00, 0xbe, // bkpt
	}); err != nil {
		t.Fatal(err)
	}
	const count = 64
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR0: 0xa5a5a5a5, cpu.RegisterR1: 0x3000, cpu.RegisterR2: count,
	} {
		if err := b.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	if r := run(t, b, 0x1000, 10000); r.Err != nil || r.Reason != cpu.StopBreakpoint {
		t.Fatalf("run = %+v", r)
	}
	got := make([]byte, count*4)
	if err := b.ReadMemory(0x3000, got); err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if v != 0xa5 {
			t.Fatalf("byte %d = %#x, want 0xa5 (inline store into the executable region lost)", i, v)
		}
	}
}

// TestNativeTLBAliasingPages checks the tag comparison. The table is
// direct-mapped over the low bits of the page number, so two pages exactly
// nativeTLBEntries apart share a slot; alternating between them must keep
// producing each page's own bytes rather than the other page's host pointer.
func TestNativeTLBAliasingPages(t *testing.T) {
	const stride = uint32(nativeTLBEntries) << tlbPageBits // 16 MiB
	b := nativeBackend(t)
	rw := cpu.PermissionRead | cpu.PermissionWrite
	mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
	mustMap(t, b, 0x2000, 0x1000, rw)
	mustMap(t, b, 0x2000+stride, 0x1000, rw)
	if 0x2000>>tlbPageBits&nativeTLBMask != (0x2000+stride)>>tlbPageBits&nativeTLBMask {
		t.Fatal("test setup: the two regions do not collide in the TLB")
	}
	if err := b.WriteMemory(0x2000, []byte{0x11, 0x22, 0x33, 0x44}); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteMemory(0x2000+stride, []byte{0xaa, 0xbb, 0xcc, 0xdd}); err != nil {
		t.Fatal(err)
	}
	// loop: ldr r3,[r0]; ldr r4,[r1]; str r3,[r1]; str r4,[r0]; subs r2,#1; bne loop
	// Swaps the two words every iteration, so an aliased entry shows up as both
	// slots holding the same value.
	if err := b.WriteMemory(0x1000, []byte{
		0x03, 0x68, // ldr  r3, [r0, #0]
		0x0c, 0x68, // ldr  r4, [r1, #0]
		0x0b, 0x60, // str  r3, [r1, #0]
		0x04, 0x60, // str  r4, [r0, #0]
		0x01, 0x3a, // subs r2, #1
		0xf9, 0xd1, // bne  loop
		0x00, 0xbe, // bkpt
	}); err != nil {
		t.Fatal(err)
	}
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x2000, cpu.RegisterR1: 0x2000 + stride, cpu.RegisterR2: 9,
	} {
		if err := b.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	if r := run(t, b, 0x1000, 10000); r.Err != nil || r.Reason != cpu.StopBreakpoint {
		t.Fatalf("run = %+v", r)
	}
	// An odd number of swaps leaves the words exchanged.
	for _, want := range []struct {
		address uint32
		bytes   []byte
	}{
		{0x2000, []byte{0xaa, 0xbb, 0xcc, 0xdd}},
		{0x2000 + stride, []byte{0x11, 0x22, 0x33, 0x44}},
	} {
		got := make([]byte, 4)
		if err := b.ReadMemory(want.address, got); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i] != want.bytes[i] {
				t.Fatalf("0x%08x = % x, want % x (aliased TLB slot served the wrong page)",
					want.address, got, want.bytes)
			}
		}
	}
}

// TestNativePartialPageRegionStaysCorrect covers regions that do not cover a
// whole page. tlbNote refuses to install such a page, because the emitted code
// checks only that an access stays inside its page - not inside the region - so
// caching a partial page would let an access run off the end of the mapping and
// into unrelated host memory instead of faulting. Both ways a region can fall
// short are covered: one that starts part-way into its page and one that ends
// part-way through it.
//
// The loop deliberately runs one iteration too many, inside the SAME run as the
// valid ones. Checking the overrun in a separate run would not be reliable: the
// write half is dropped whenever the translated-code span grows, so by the next
// run a wrongly-cached page may have been evicted for an unrelated reason and
// the access would fault by luck rather than by design.
func TestNativePartialPageRegionStaysCorrect(t *testing.T) {
	for _, layout := range []struct {
		name string
		base uint32
		size uint32
	}{
		{"starts-mid-page", 0x2040, 0x80},
		{"ends-mid-page", 0x2000, 0x80},
	} {
		t.Run(layout.name, func(t *testing.T) {
			b := nativeBackend(t)
			mustMap(t, b, 0x1000, 0x1000,
				cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
			mustMap(t, b, layout.base, layout.size, cpu.PermissionRead|cpu.PermissionWrite)
			// loop: str r0,[r1]; ldr r3,[r1]; adds r1,#4; subs r2,#1; bne loop; bkpt
			if err := b.WriteMemory(0x1000, []byte{
				0x08, 0x60, // str  r0, [r1, #0]
				0x0b, 0x68, // ldr  r3, [r1, #0]
				0x04, 0x31, // adds r1, #4
				0x01, 0x3a, // subs r2, #1
				0xfa, 0xd1, // bne  loop
				0x00, 0xbe, // bkpt
			}); err != nil {
				t.Fatal(err)
			}
			for reg, value := range map[uint32]uint32{
				cpu.RegisterR0: 0x5a5a5a5a,
				cpu.RegisterR1: layout.base,
				cpu.RegisterR2: layout.size/4 + 1, // one word past the region
			} {
				if err := b.WriteRegister(reg, value); err != nil {
					t.Fatal(err)
				}
			}
			r := run(t, b, 0x1000, 10000)
			if r.Reason != cpu.StopFault {
				t.Fatalf("run = %+v, want a fault on the word past the region "+
					"(a partial page must never be cached in the TLB)", r)
			}
			got := make([]byte, layout.size)
			if err := b.ReadMemory(layout.base, got); err != nil {
				t.Fatal(err)
			}
			for i, v := range got {
				if v != 0x5a {
					t.Fatalf("byte %d = %#x, want 0x5a", i, v)
				}
			}
		})
	}
}

// TestNativeBailRestoresExactBudget pins the accounting the bail path has to get
// right. A block's budget gate subtracts the whole block up front, so a bail
// part-way through must give back exactly the instructions that did not run.
// Running the same program over a sweep of budgets and comparing every result
// against the interpreter catches an off-by-one that a single budget would hide.
func TestNativeBailRestoresExactBudget(t *testing.T) {
	// Alternating loads from two regions a TLB stride apart, so most accesses
	// miss and bail at varying offsets inside the block.
	const stride = uint32(nativeTLBEntries) << tlbPageBits
	code := []byte{
		0x03, 0x68, // ldr  r3, [r0, #0]
		0x0c, 0x68, // ldr  r4, [r1, #0]
		0x01, 0x30, // adds r0, #1
		0x03, 0x68, // ldr  r3, [r0, #0]
		0x0c, 0x68, // ldr  r4, [r1, #0]
		0x01, 0x3a, // subs r2, #1
		0xf8, 0xd1, // bne  loop
		0x00, 0xbe, // bkpt
	}
	for budget := uint64(1); budget <= 64; budget++ {
		var results [2]cpu.Result
		var regs [2][17]uint32
		for i, make := range []func() *Backend{New, NewNativeJIT} {
			b := make()
			rw := cpu.PermissionRead | cpu.PermissionWrite
			mustMap(t, b, 0x1000, 0x1000, rw|cpu.PermissionExecute)
			mustMap(t, b, 0x2000, 0x1000, rw)
			mustMap(t, b, 0x2000+stride, 0x1000, rw)
			if err := b.WriteMemory(0x1000, code); err != nil {
				t.Fatal(err)
			}
			for reg, value := range map[uint32]uint32{
				cpu.RegisterR0: 0x2000, cpu.RegisterR1: 0x2000 + stride, cpu.RegisterR2: 4,
			} {
				if err := b.WriteRegister(reg, value); err != nil {
					t.Fatal(err)
				}
			}
			results[i] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			for id := uint32(0); id < 17; id++ {
				regs[i][id] = register(t, b, id)
			}
			_ = b.Close()
		}
		if results[0].Instructions != results[1].Instructions ||
			results[0].Reason != results[1].Reason || results[0].PC != results[1].PC {
			t.Fatalf("budget %d: interpreter %+v, native %+v", budget, results[0], results[1])
		}
		if regs[0] != regs[1] {
			t.Fatalf("budget %d: registers diverged: %v vs %v", budget, regs[0], regs[1])
		}
	}
}

// TestNativeBranchLinkSpansFourBytes guards the translated-code span against
// BL, the one instruction the interpreter retires as a single step but which
// occupies four bytes. If a block records only two bytes for it, the second
// halfword falls outside the span and a write there - which changes the branch
// target - never invalidates the translation.
//
// The BL is placed above everything it can reach, so it owns the top of the
// span and an off-by-two is the difference between invalidating and not.
func TestNativeBranchLinkSpansFourBytes(t *testing.T) {
	b := nativeBackend(t)
	mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
	if err := b.WriteMemory(0x1000, []byte{
		0x01, 0x20, // 0x1000: movs r0, #1
		0x00, 0xbe, // 0x1002: bkpt
		0x07, 0x20, // 0x1004: movs r0, #7
		0x00, 0xbe, // 0x1006: bkpt
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteMemory(0x1010, []byte{
		0xff, 0xf7, 0xf6, 0xff, // 0x1010: bl 0x1000
	}); err != nil {
		t.Fatal(err)
	}
	if r := run(t, b, 0x1010, 16); r.Err != nil || r.Reason != cpu.StopBreakpoint {
		t.Fatalf("first run = %+v", r)
	}
	if got := register(t, b, cpu.RegisterR0); got != 1 {
		t.Fatalf("r0 after first run = %d, want 1", got)
	}
	// Retarget the BL by rewriting only its second halfword.
	if err := b.WriteMemory(0x1012, []byte{0xf8, 0xff}); err != nil { // bl 0x1004
		t.Fatal(err)
	}
	if r := run(t, b, 0x1010, 16); r.Err != nil || r.Reason != cpu.StopBreakpoint {
		t.Fatalf("second run = %+v", r)
	}
	if got := register(t, b, cpu.RegisterR0); got != 7 {
		t.Fatalf("r0 after retargeting the BL = %d, want 7 "+
			"(the second halfword of BL is outside the translated-code span)", got)
	}
}

// TestNativeTLBSurvivesACPSRWriteThatKeepsTheMode pins the cost fix for issue
// #93. Writing the CPSR used to empty the whole 32768-entry table, but only the
// processor mode can invalidate a cached translation: under the MMU it is the
// privilege level that decides a page's permissions, while the flags, the
// Thumb bit and the interrupt masks describe execution only.
//
// A KTF title writes the CPSR on every host-call return. 귀혼무사편 makes enough
// of them that the sweep was 70% of its entire run time on the native tier -
// the title ran at a quarter of real time, and the "fastest core" setting
// picked exactly that backend.
func TestNativeTLBSurvivesACPSRWriteThatKeepsTheMode(t *testing.T) {
	backend := nativeBackend(t)
	if err := backend.Map(0x1000, tlbPageSize*2,
		cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	// Install the page the way the interpreter's memory path does after a
	// successful guest access. This is a white-box test of the invalidation
	// policy, so priming it directly keeps the guest program out of it.
	region := make([]byte, tlbPageSize*2)
	backend.tlbNote(0x1000, 0x1000, region, cpu.PermissionRead|cpu.PermissionWrite)
	if !backend.tlbHit(0x1000, cpu.PermissionRead) {
		t.Fatal("the read half did not cache the page")
	}

	mode := uint32(processorModeSystem)
	if err := backend.WriteRegister(cpu.RegisterCPSR, mode); err != nil {
		t.Fatal(err)
	}
	if !backend.tlbHit(0x1000, cpu.PermissionRead) {
		t.Fatal("entering the mode already selected dropped the cached page")
	}
	// Flags and the Thumb bit change execution, not translation; 1<<29 is the
	// architectural C flag.
	if err := backend.WriteRegister(
		cpu.RegisterCPSR,
		mode|cpu.StatusThumb|(1<<29),
	); err != nil {
		t.Fatal(err)
	}
	if !backend.tlbHit(0x1000, cpu.PermissionRead) {
		t.Fatal("a flag-only CPSR write dropped the cached page")
	}

	// A real mode change must still empty the table: the privilege level it
	// selects is what a page's permissions are resolved against.
	if err := backend.WriteRegister(
		cpu.RegisterCPSR,
		uint32(processorModeUser)|cpu.StatusThumb,
	); err != nil {
		t.Fatal(err)
	}
	if backend.tlbHit(0x1000, cpu.PermissionRead) {
		t.Fatal("switching the processor mode kept a cached translation")
	}
}
