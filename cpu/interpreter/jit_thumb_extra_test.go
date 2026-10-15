package interpreter

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// The cold dispatcher must return immediately after a precise alias fallback,
// even when it changes CPU mode without a stop reason. Otherwise its caller
// could continue interpreting the ARM destination as another Thumb block.
func TestThumbExtraAliasFallbackIsTerminal(t *testing.T) {
	for _, alias := range []struct {
		kind  jitExtraLoopKind
		stack bool
	}{{jitBytePaletteLoop, false}, {jitMaskedBlendLoop, false}, {jitNibblePaletteLoop, false}, {jitNibblePaletteLoop, true}} {
		kind := alias.kind
		for _, raw := range []uint16{0xbe00, 0x4710, 0x6818} { // BKPT, BX r2, LDR r0,[r3]
			for _, traced := range []bool{false, true} {
				for _, budget := range []uint64{1, 16} {
					t.Run(fmt.Sprintf("kind=%d/stack=%t/raw=%04x/trace=%t/budget=%d", kind, alias.stack, raw, traced, budget), func(t *testing.T) {
						precise, jit := New(), NewJIT()
						for _, b := range []*Backend{precise, jit} {
							t.Cleanup(func() { _ = b.Close() })
							check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
							check(t, b.WriteMemory(0x1000, thumbTestCode(raw, 0xbe00)))
							check(t, b.WriteRegister(cpu.RegisterR1, 0x1000))
							check(t, b.WriteRegister(cpu.RegisterR2, 0x2000))
							check(t, b.WriteRegister(cpu.RegisterR3, 0x3000))
							if alias.stack {
								check(t, b.WriteRegister(cpu.RegisterR1, 0x5000))
								check(t, b.WriteRegister(cpu.RegisterSP, 0x1000))
							}
							check(t, b.WriteRegister(cpu.RegisterCPSR, flagC|flagV|cpu.StatusThumb|uint32(processorModeSystem)))
							check(t, b.WriteRegister(cpu.RegisterPC, 0x1000))
							if traced {
								b.SetPCHistoryLimit(16)
							}
						}
						extra := &jitExtraLoop{kind: kind}
						extra.bytePalette.destination, extra.bytePalette.start, extra.bytePalette.words = 1, 0x1000, 1
						extra.maskedBlend.destination, extra.maskedBlend.start = 1, 0x1000
						extra.nibblePalette.destination, extra.nibblePalette.start, extra.nibblePalette.words = 1, 0x1000, 1
						wantN, wantReason, wantErr := precise.runThumb(budget)
						gotN, done, gotReason, gotErr := jit.runThumbExtraLoop(extra, budget, false, false, traced)
						sameReason := gotReason == nil && wantReason == nil || gotReason != nil && wantReason != nil && *gotReason == *wantReason
						if !done || gotN != wantN || !sameReason || !sameError(gotErr, wantErr) || jit.mode != precise.mode {
							t.Fatalf("fallback got=(%d,%t,%v,%v,%v), want=(%d,true,%v,%v,%v)", gotN, done, gotReason, gotErr, jit.mode, wantN, wantReason, wantErr, precise.mode)
						}
						for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
							if got, want := armOracleRegisterValue(t, jit, register), armOracleRegisterValue(t, precise, register); got != want {
								t.Fatalf("register %d: got=%08x want=%08x", register, got, want)
							}
						}
						if !bytes.Equal(precise.regions[0].data, jit.regions[0].data) {
							t.Fatal("fallback changed code memory")
						}
					})
				}
			}
		}
	}
}
