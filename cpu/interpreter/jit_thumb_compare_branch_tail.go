package interpreter

// A complete, untraced application block may execute its final CMP/Bcond pair
// without repeated dispatch. Keep the original instructions and code span:
// tracing, partial budgets and SMC still use them.
type jitThumbCompareBranchTail struct {
	branchPC, target, right uint32
	left, condition         uint8
	register                bool
}

func classifyThumbCompareBranchTail(block *jitBlock) *jitThumbCompareBranchTail {
	if len(block.thumb) < 2 {
		return nil
	}
	compare, branch := block.thumb[len(block.thumb)-2], block.thumb[len(block.thumb)-1]
	if uint64(compare.pc)+2 != uint64(branch.pc) || uint64(branch.pc)+2 != uint64(block.end) ||
		branch.op != thumbConditionalBranch || branch.appOp != thumbConditionalBranch ||
		branch.raw&0xf000 != 0xd000 || uint8(branch.raw>>8)&15 >= 14 {
		return nil
	}
	tail := &jitThumbCompareBranchTail{
		branchPC:  branch.pc,
		target:    thumbConditionalTarget(branch.pc, branch.raw),
		condition: uint8(branch.raw>>8) & 15,
	}
	switch {
	case compare.op == thumbCompareImmediate && compare.appOp == thumbCompareImmediate && compare.raw&0xf800 == 0x2800:
		tail.left, tail.right = uint8(compare.raw>>8)&7, uint32(compare.raw&0xff)
	case compare.op == thumbALU && compare.appOp == thumbALU && compare.raw&0xffc0 == 0x4280:
		tail.left, tail.right, tail.register = uint8(compare.raw)&7, uint32(compare.raw>>3)&7, true
	default:
		return nil
	}
	return tail
}
