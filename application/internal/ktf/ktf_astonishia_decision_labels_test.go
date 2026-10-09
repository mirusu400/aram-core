package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func syntheticAstonishiaClient() []byte {
	last := astonishiaDecisionLabelPatches[len(astonishiaDecisionLabelPatches)-1]
	client := make([]byte, last.offset+len(last.original))
	for _, patch := range astonishiaDecisionLabelPatches {
		copy(client[patch.offset:], patch.original)
	}
	return client
}

func TestAstonishiaDecisionLabelsRequireAuthenticatedInstructions(t *testing.T) {
	client := syntheticAstonishiaClient()
	original := bytes.Clone(client)
	unmatched, enabled, err := patchAstonishiaDecisionLabels(client)
	if err != nil || enabled || !bytes.Equal(unmatched, original) {
		t.Fatalf("unmatched client patched: enabled=%t err=%v", enabled, err)
	}
	patched, enabled, err := patchAstonishiaDecisionLabelsForDigest(astonishiaClientSHA256, client)
	if err != nil || !enabled {
		t.Fatalf("authenticated client not patched: enabled=%t err=%v", enabled, err)
	}
	if !bytes.Equal(client, original) {
		t.Fatal("patch mutated package client bytes")
	}
	for _, patch := range astonishiaDecisionLabelPatches {
		if !bytes.Equal(patched[patch.offset:patch.offset+len(patch.patched)], patch.patched) {
			t.Fatalf("caption instructions at 0x%x not patched", patch.offset)
		}
	}
	again, enabled, err := patchAstonishiaDecisionLabelsForDigest(astonishiaPatchedClientSHA256, patched)
	if err != nil || !enabled || !bytes.Equal(again, patched) {
		t.Fatalf("patched client not accepted: enabled=%t err=%v", enabled, err)
	}
	for _, patch := range astonishiaDecisionLabelPatches {
		bad := bytes.Clone(client)
		bad[patch.offset] ^= 1
		before := bytes.Clone(bad)
		if _, _, err := patchAstonishiaDecisionLabelsForDigest(astonishiaClientSHA256, bad); err == nil {
			t.Fatalf("changed instructions at 0x%x accepted", patch.offset)
		}
		if !bytes.Equal(bad, before) {
			t.Fatal("failed patch mutated client")
		}
		if _, _, err := patchAstonishiaDecisionLabelsForDigest(astonishiaClientSHA256, client[:patch.offset]); err == nil {
			t.Fatalf("client truncated at 0x%x accepted", patch.offset)
		}
	}
}

func TestAstonishiaDecisionCaptionsPreserveCoordinatesAndStack(t *testing.T) {
	client, _, err := patchAstonishiaDecisionLabelsForDigest(astonishiaClientSHA256, syntheticAstonishiaClient())
	check(t, err)
	yes, no := astonishiaDecisionLabelPatches[0], astonishiaDecisionLabelPatches[1]
	// Synthetic caller with two outgoing Y arguments, a clobbering callee between
	// them, and an ordinary callee-saved register/stack epilogue.
	code := []byte{
		0x50, 0xb5, // push {r4, r6, lr}
		0x8e, 0xb0, // sub sp, #56
		0x0b, 0x92, // str r2, [sp, #44]: retain button top
		0x0c, 0x96, // str r6, [sp, #48]: retain button X
	}
	code = append(code, client[yes.offset:yes.offset+len(yes.patched)]...)
	code = append(code, 0x00, 0x92, 0x00, 0xbe) // str r2, [sp]; bkpt
	noStart := len(code)
	code = append(code, client[no.offset:no.offset+len(no.patched)]...)
	code = append(code,
		0x0c, 0x9e, // ldr r6, [sp, #48]
		0x00, 0x90, // str r0, [sp]
		0x21, 0x36, // adds r6, #33
		0x33, 0x1c, // adds r3, r6, #0
		0x00, 0xbe, // bkpt
	)
	epilogue := len(code)
	code = append(code, 0x0e, 0xb0, 0x50, 0xbd) // add sp, #56; pop {r4, r6, pc}
	const entry, stack, returnTrap = uint32(0x1000), uint32(0x3000), uint32(0x1100)
	for _, point := range []struct{ x, y uint32 }{{0, 0}, {62, 103}, {500, 240}, {0xfffffff0, 0xffffffe0}} {
		backend := interpreter.New()
		check(t, backend.Map(entry, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, backend.Map(stack-0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, backend.WriteMemory(entry, code))
		check(t, backend.WriteMemory(returnTrap, []byte{0, 0xbe}))
		for reg, value := range map[uint32]uint32{
			cpu.RegisterR2: point.y, cpu.RegisterR4: 0x12345678, cpu.RegisterR6: point.x,
			cpu.RegisterSP: stack, cpu.RegisterLR: returnTrap | 1,
		} {
			check(t, backend.WriteRegister(reg, value))
		}
		assertCall := func(pc, wantX uint32) {
			t.Helper()
			result := backend.Run(context.Background(), pc, cpu.ModeThumb, 32)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("caption execution=%+v", result)
			}
			x, err := backend.ReadRegister(cpu.RegisterR3)
			check(t, err)
			sp, err := backend.ReadRegister(cpu.RegisterSP)
			check(t, err)
			var y, top [4]byte
			check(t, backend.ReadMemory(sp, y[:]))
			check(t, backend.ReadMemory(sp+44, top[:]))
			if x != wantX || binary.LittleEndian.Uint32(y[:]) != point.y+16 || binary.LittleEndian.Uint32(top[:]) != point.y {
				t.Fatalf("caption=(%d,%d), button top=%d; want (%d,%d), top=%d", x, binary.LittleEndian.Uint32(y[:]), binary.LittleEndian.Uint32(top[:]), wantX, point.y+16, point.y)
			}
		}
		assertCall(entry, point.x+6)
		for reg := uint32(0); reg < 4; reg++ {
			check(t, backend.WriteRegister(reg, 0xdeadbeef))
		}
		assertCall(entry+uint32(noStart), point.x+33)
		result := backend.Run(context.Background(), entry+uint32(epilogue), cpu.ModeThumb, 8)
		if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != returnTrap+2 {
			t.Fatalf("caption epilogue=%+v", result)
		}
		for reg, want := range map[uint32]uint32{cpu.RegisterSP: stack, cpu.RegisterR4: 0x12345678, cpu.RegisterR6: point.x} {
			got, err := backend.ReadRegister(reg)
			check(t, err)
			if got != want {
				t.Fatalf("register %d=%08x, want %08x", reg, got, want)
			}
		}
		check(t, backend.Close())
	}
}

func TestRestoreAstonishiaDecisionLabelsValidatesBothSites(t *testing.T) {
	for _, test := range []struct {
		name                                 string
		enabled, firstPatched, secondPatched bool
		corrupt                              int
	}{
		{"old snapshot", true, false, false, -1},
		{"current snapshot", true, true, true, -1},
		{"mixed snapshot", true, true, false, -1},
		{"other title", false, false, false, -1},
		{"first site changed", true, false, false, 0},
		{"second site changed", true, false, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			client := syntheticAstonishiaClient()
			for index, patched := range []bool{test.firstPatched, test.secondPatched} {
				if patched {
					patch := astonishiaDecisionLabelPatches[index]
					copy(client[patch.offset:], patch.patched)
				}
			}
			if test.corrupt >= 0 {
				client[astonishiaDecisionLabelPatches[test.corrupt].offset] ^= 1
			}
			check(t, backend.Map(ImageBase, uint32(len(client)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, backend.WriteMemory(ImageBase, client))
			runtime := &Runtime{CPU: backend, astonishiaDecisionLabels: test.enabled}
			err := runtime.RestoreKnownClientPatches()
			if (err != nil) != (test.corrupt >= 0) {
				t.Fatalf("restore error=%v", err)
			}
			got := make([]byte, len(client))
			check(t, backend.ReadMemory(ImageBase, got))
			if !test.enabled || test.corrupt >= 0 {
				if !bytes.Equal(got, client) {
					t.Fatal("disabled or rejected restore mutated instructions")
				}
			} else {
				for _, patch := range astonishiaDecisionLabelPatches {
					if !bytes.Equal(got[patch.offset:patch.offset+len(patch.patched)], patch.patched) {
						t.Fatalf("snapshot caption at 0x%x not upgraded", patch.offset)
					}
				}
				check(t, runtime.RestoreKnownClientPatches())
			}
		})
	}
}
