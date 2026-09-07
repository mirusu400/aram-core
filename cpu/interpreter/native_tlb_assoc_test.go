//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestNativeTLBKeepsTwoAliasingPagesResident pins the two-way behaviour: two
// pages that share a set both stay resident after being installed in turn, a
// third page in the same set replaces the oldest, and a loop that touches two
// aliasing pages every iteration runs natively rather than bailing on each.
// A handset image lays hot pages 16 MiB apart (a table and the code page whose
// literal loads read it, the stack top and an image page), and the direct-mapped
// table let those evict each other on every pass until the bail heuristic
// retired the instructions to the interpreter.
func TestNativeTLBKeepsTwoAliasingPagesResident(t *testing.T) {
	const stride = uint32(nativeTLBSets) << tlbPageBits // pages one set apart
	b := nativeBackend(t)
	rw := cpu.PermissionRead | cpu.PermissionWrite
	mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
	for _, base := range []uint32{0x2000, 0x2000 + stride, 0x2000 + 2*stride} {
		mustMap(t, b, base, 0x1000, rw)
	}
	if 0x2000>>tlbPageBits&nativeTLBSetMask != (0x2000+stride)>>tlbPageBits&nativeTLBSetMask {
		t.Fatal("test setup: the regions do not share a set")
	}
	// Touch two aliasing pages the way guest code does (the host-side
	// ReadMemory does not fill the TLB); both must be resident.
	guestRead := func(address uint32) {
		t.Helper()
		if _, err := b.read32(address, cpu.PermissionRead); err != nil {
			t.Fatal(err)
		}
	}
	guestRead(0x2000)
	guestRead(0x2000 + stride)
	if !b.tlbHit(0x2000, cpu.PermissionRead) || !b.tlbHit(0x2000+stride, cpu.PermissionRead) {
		t.Fatal("two pages sharing a set did not both stay resident")
	}
	// A third replaces the oldest (first installed), not the newest.
	guestRead(0x2000 + 2*stride)
	if b.tlbHit(0x2000, cpu.PermissionRead) {
		t.Fatal("the oldest page survived a third page's install")
	}
	if !b.tlbHit(0x2000+stride, cpu.PermissionRead) || !b.tlbHit(0x2000+2*stride, cpu.PermissionRead) {
		t.Fatal("the newer two pages did not both stay resident")
	}

	// loop: ldr r3,[r0]; ldr r4,[r1]; adds r3,r4; str r3,[r0]; subs r2,#1; bne loop
	// r0 and r1 point at pages one set apart. After the first iteration both
	// pages are resident, so no later iteration may bail.
	if err := b.WriteMemory(0x1000, []byte{
		0x03, 0x68, // ldr  r3, [r0, #0]
		0x0c, 0x68, // ldr  r4, [r1, #0]
		0x23, 0x44, // add  r3, r4
		0x03, 0x60, // str  r3, [r0, #0]
		0x01, 0x3a, // subs r2, #1
		0xf9, 0xd1, // bne  loop
		0x00, 0xbe, // bkpt
	}); err != nil {
		t.Fatal(err)
	}
	check(t, b.WriteMemory(0x2000+stride, []byte{1, 0, 0, 0}))
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x2000, cpu.RegisterR1: 0x2000 + stride, cpu.RegisterR2: 1000,
	} {
		check(t, b.WriteRegister(reg, value))
	}
	before := b.nativeSlow[nativeLinkKey{mode: cpu.ModeThumb, pc: 0x1000}]
	r := b.Run(context.Background(), 0x1000, cpu.ModeThumb, 100000)
	if r.Err != nil || r.Reason != cpu.StopBreakpoint {
		t.Fatalf("run = %+v", r)
	}
	var word [4]byte
	check(t, b.ReadMemory(0x2000, word[:]))
	if got := uint32(word[0]) | uint32(word[1])<<8; got != 1000 {
		t.Fatalf("accumulated %d, want 1000", got)
	}
	for _, pc := range []uint32{0x1000, 0x1002, 0x1006} {
		state := b.nativeSlow[nativeLinkKey{mode: cpu.ModeThumb, pc: pc}]
		if state.count > 1 || (pc == 0x1000 && state.count > before.count+1) {
			t.Fatalf("pc 0x%04x bailed %d times while both pages were resident", pc, state.count)
		}
	}
}
