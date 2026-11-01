package application

import "testing"

// A Raptor Java Card's paint() runs to completion inside the host call that
// repaints the card - it is not resumable across frames - so the synchronous
// callback bound has to cover the longest single paint a title does. Eight
// fuzz seeds measured one LGT 월드장기체스 Card.paint() call at 341,399,568 to
// 342,812,803 instructions, and the old 256M bound faulted the title with
// "WIPI callback 0x0001be00 did not return". The call finishes once the bound
// is lifted, so it is finite work, not a hang. This is the measurement that
// decides the constant; lowering it below that figure puts the title back
// where it was.
func TestRaptorCallbackLimitCoversLongestMeasuredPaint(t *testing.T) {
	const measuredPaint = uint64(342_812_803)
	if raptorCallbackInstructionLimit <= measuredPaint {
		t.Fatalf(
			"Raptor callback limit %d does not cover the %d instructions "+
				"월드장기체스 spends in one Card.paint",
			raptorCallbackInstructionLimit,
			measuredPaint,
		)
	}
}
