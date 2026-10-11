package interpreter

import "github.com/mirusu400/aram-core/cpu"

// These diagnostic operations run only in aram_profile_ops builds and while
// Backend.Run holds b.mu. There are no per-instruction callbacks in products.
func (b *Backend) recordThumbMicro(in *thumbMicroInstr) {
	stats := b.executionStatistics.ThumbMicro
	if stats == nil {
		stats = &cpu.ThumbMicroStatistics{Blocks: make(map[uint32]uint64)}
		b.executionStatistics.ThumbMicro = stats
	}
	if int(in.op) < len(stats.Classes) {
		stats.Classes[in.op]++
	}
	if in.op == thumbALU {
		stats.ALU[(in.raw>>6)&15]++
	}
}

func (b *Backend) recordThumbBlock(block *jitBlock) {
	stats := b.executionStatistics.ThumbMicro
	if stats == nil {
		stats = &cpu.ThumbMicroStatistics{Blocks: make(map[uint32]uint64)}
		b.executionStatistics.ThumbMicro = stats
	}
	stats.BlockCalls++
	if _, present := stats.Blocks[block.start]; present || len(stats.Blocks) < 4096 {
		stats.Blocks[block.start]++
	} else {
		stats.UntrackedBlockCalls++
	}
}

func cloneThumbMicroStats(stats *cpu.ThumbMicroStatistics) *cpu.ThumbMicroStatistics {
	if stats == nil {
		return nil
	}
	result := *stats
	result.Blocks = make(map[uint32]uint64, len(stats.Blocks))
	for pc, calls := range stats.Blocks {
		result.Blocks[pc] = calls
	}
	return &result
}
