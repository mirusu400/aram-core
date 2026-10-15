package interpreter

import "github.com/mirusu400/aram-core/cpu"

// Keep the specialized accelerator switch out of the ordinary block dispatcher.
// In particular, Go/WASM otherwise gives that hot function a resume point and
// spill space for every cold accelerator call. A code-alias fallback consumes
// the rest of the batch itself; done preserves that immediate return, including
// a mode switch with no stop reason.
func (b *Backend) runThumbExtraLoop(extra *jitExtraLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) (retired uint64, done bool, reason *cpu.StopReason, err error) {
	switch extra.kind {
	case jitQuotientRefineLoop:
		retired = b.accelerateThumbQuotientRefineLoop(&extra.quotientRefine, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitShiftBoundLoop:
		retired = b.accelerateThumbShiftBoundLoop(&extra.shiftBound, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitBytePairFillLoop:
		if !wholeSystem && b.bytePairFillCodeAlias(&extra.bytePairFill) {
			retired, reason, err = b.runThumb(remaining)
			return retired, true, reason, err
		}
		retired = b.accelerateThumbBytePairFillLoop(&extra.bytePairFill, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitObjectLookupLoop:
		retired = b.accelerateThumbObjectLookup(&extra.objectLookup, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitRecordSearchLoop:
		retired = b.accelerateThumbRecordSearch(&extra.recordSearch, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitIndexedPaletteLoop:
		retired = b.accelerateThumbIndexedPaletteLoop(&extra.indexedPalette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitBytePaletteLoop:
		// A completed store may replace an instruction in the translated prefix.
		if !wholeSystem && colorLoopRangesOverlap(b.regs[extra.bytePalette.destination], 2, extra.bytePalette.start, extra.bytePalette.words*2) {
			retired, reason, err = b.runThumb(remaining)
			return retired, true, reason, err
		}
		retired = b.accelerateThumbBytePaletteLoop(&extra.bytePalette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitNibblePaletteLoop:
		if !wholeSystem && b.nibblePaletteCodeAlias(&extra.nibblePalette) {
			retired, reason, err = b.runThumb(remaining)
			return retired, true, reason, err
		}
		retired = b.accelerateThumbNibblePaletteLoop(&extra.nibblePalette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitMaskedBlendLoop:
		if !wholeSystem && colorLoopRangesOverlap(b.regs[extra.maskedBlend.destination], 2, extra.maskedBlend.start, 32) {
			retired, reason, err = b.runThumb(remaining)
			return retired, true, reason, err
		}
		retired = b.accelerateThumbMaskedBlendLoop(&extra.maskedBlend, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitPrologueBranch:
		retired = b.accelerateThumbPrologueBranch(&extra.prologue, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitLoadMaskBranch:
		retired = b.accelerateThumbLoadMaskBranch(&extra.loadMask, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitTransparentPaletteLoop:
		retired = b.accelerateThumbTransparentPaletteLoop(&extra.transparentPalette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitPaletteLoop:
		retired, err = b.accelerateThumbPaletteLoop(&extra.palette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitStackPaletteLoop:
		retired = b.accelerateThumbStackPaletteLoop(&extra.stackPalette, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitHalfwordFillLoop:
		retired = b.accelerateThumbHalfwordFillLoop(&extra.halfwordFill, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitColorLoop:
		retired = b.accelerateThumbColorLoop(&extra.color, remaining, wholeSystem, hasExecutionTraps, traced)
	case jitFillLoop:
		retired = b.accelerateThumbFillLoop(&extra.fill, remaining, wholeSystem, hasExecutionTraps, traced)
	}
	return
}
