package ktf

import "testing"

// A KTF native runs to completion inside the host call that entered it -
// nothing about it is resumable - so its instruction bound has to cover the
// longest single piece of work a title does in one, which is a level load
// rather than a frame. 이노티아연대기2 spends 113,515,438 instructions in the
// one Clet.handleInput that loads a scenario from its own files, so a bound at
// the bootstrap figure faulted the title at the first thing a player does
// (issue #147). This is the measurement that decides the constant; lowering it
// below that figure puts the title back where it was.
func TestKTFJavaNativeBudgetCoversAScenarioLoad(t *testing.T) {
	const measuredScenarioLoad = uint64(113_515_438)
	if ktfJavaNativeInstructionMax <= measuredScenarioLoad {
		t.Fatalf(
			"KTF native budget %d does not cover the %d instructions "+
				"이노티아연대기2 spends loading a scenario",
			ktfJavaNativeInstructionMax,
			measuredScenarioLoad,
		)
	}
	if ktfJavaNativeInstructionMax < ktfBootstrapInstructionMax {
		t.Fatalf(
			"KTF native budget %d is below the bootstrap budget %d",
			ktfJavaNativeInstructionMax,
			ktfBootstrapInstructionMax,
		)
	}
}

// KTF 미니게임천국5 (AID 0104261C) decompresses its complete resource
// set inside startClet. The exact user-authorized archive crossed 282 million
// instructions before returning; a 282,000,000-instruction bound still faulted
// in the decompressor while 283,000,000 completed. Keep the product bound above
// that measured lower limit with room for backend-independent bookkeeping.
func TestKTFJavaNativeBudgetCoversMinigameHeaven5Boot(t *testing.T) {
	const measuredBootLowerLimit = uint64(282_000_000)
	if ktfJavaNativeInstructionMax <= measuredBootLowerLimit {
		t.Fatalf(
			"KTF native budget %d does not cover the greater-than-%d-instruction "+
				"Minigame Heaven 5 startClet resource load",
			ktfJavaNativeInstructionMax,
			measuredBootLowerLimit,
		)
	}
}
