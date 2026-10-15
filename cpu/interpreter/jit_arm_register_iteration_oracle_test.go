package interpreter

import (
	"math/bits"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Every possible list is executed through public New and NewJIT backends. The
// precise implementation deliberately retains its independent sixteen-slot
// register scan. Reusing the mappings bounds both bitmap memory and allocation
// costs while code writes invalidate each preceding decoded instruction.
func TestARMRegisterIterationEveryMaskMatchesPrecise(t *testing.T) {
	for _, load := range []bool{false, true} {
		name := "stm"
		if load {
			name = "ldm"
		}
		t.Run(name, func(t *testing.T) {
			precise, jit := armPrivateTransferPair(t)
			if precise.armJITBlocks != nil || precise.nativeARMBlocks != nil ||
				jit.armJITBlocks == nil || jit.nativeARMBlocks != nil || jit.systemBus != nil ||
				jit.physicalAccess || jit.tlb != nil || jit.tracing() || len(jit.executionTraps) != 0 ||
				jit.instructionCacheEnabled() || jit.loopAcceleration {
				t.Fatal("public backends must select scalar New and portable application ARM JIT")
			}
			var currentMask uint16
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("failing register list: %04x", currentMask)
				}
			})
			const rn, base = uint32(14), uint32(0x3080)
			// Execute the empty (unsupported) list last: a negative decoded
			// cache entry has no code span for subsequent host invalidation.
			for rawMask := uint32(1); rawMask <= 0x10000; rawMask++ {
				mask := uint16(rawMask)
				currentMask = mask
				instruction := armPrivateTransferInstruction(false, true, true, load, rn, mask)
				count := bits.OnesCount16(mask)
				for _, b := range []*Backend{precise, jit} {
					prepareARMPrivateTransfer(t, b, instruction, base, false)
					if load && mask&(1<<cpu.RegisterPC) != 0 {
						// PC is the last ascending access. Its deferred exchange
						// must also preserve the loaded base when RN is listed.
						check(t, b.WriteMemory(base+uint32(count-1)*4, armApplicationCode(0x1103)))
					}
				}
				block := jit.armJITBlockAt(0x1000)
				if mask == 0 {
					if block != nil && len(block.arm) != 0 && block.arm[0].op == armMicroBlockTransfer {
						t.Fatal("empty list unexpectedly entered the optimized executor")
					}
				} else {
					if block == nil || len(block.arm) == 0 || block.arm[0].op != armMicroBlockTransfer ||
						block.arm[0].exec != nil || block.arm[0].raw != instruction || block.countedLoop != nil {
						t.Fatalf("mask=%04x: transfer did not enter the micro-op executor", mask)
					}
					warmARMPrivateTransfer(t, jit, base, load)
					if _, _, hit := jit.armPrivateBlockTransferSpan(base, count, load); !hit {
						t.Fatalf("mask=%04x: positive private span proof was vacuous", mask)
					}
				}
				runARMPrivateTransferOracle(t, precise, jit, 0x1000, cpu.ModeARM, 1)
			}
		})
	}
}
