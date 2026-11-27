package system

// IMA ADPCM as carried in RIFF/WAVE format 0x11: each block starts with one
// four-byte header per channel (initial sample, step index, reserved) and
// continues with four-bit codes, low nibble first, in runs of eight samples
// per channel.

var imaStepTable = [89]int32{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118, 130, 143, 157, 173, 190, 209, 230,
	253, 279, 307, 337, 371, 408, 449, 494, 544, 598, 658, 724, 796, 876, 963,
	1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066, 2272, 2499, 2749, 3024, 3327,
	3660, 4026, 4428, 4871, 5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442,
	11487, 12635, 13899, 15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794,
	32767,
}

var imaIndexTable = [16]int32{-1, -1, -1, -1, 2, 4, 6, 8, -1, -1, -1, -1, 2, 4, 6, 8}

type imaChannelState struct {
	predictor int32
	index     int32
}

func (s *imaChannelState) decode(code uint8) int16 {
	step := imaStepTable[s.index]
	delta := step >> 3
	if code&4 != 0 {
		delta += step
	}
	if code&2 != 0 {
		delta += step >> 1
	}
	if code&1 != 0 {
		delta += step >> 2
	}
	if code&8 != 0 {
		s.predictor -= delta
	} else {
		s.predictor += delta
	}
	if s.predictor > 32767 {
		s.predictor = 32767
	} else if s.predictor < -32768 {
		s.predictor = -32768
	}
	s.index += imaIndexTable[code]
	if s.index < 0 {
		s.index = 0
	} else if s.index > 88 {
		s.index = 88
	}
	return int16(s.predictor)
}

// decodeIMAADPCMBlock decodes one WAVE IMA ADPCM block into interleaved
// 16-bit samples. A block too short to hold its headers yields nothing.
func decodeIMAADPCMBlock(block []byte, channels int) []int16 {
	if channels < 1 || channels > 2 || len(block) < 4*channels {
		return nil
	}
	states := make([]imaChannelState, channels)
	samplesPerChannel := 1 + (len(block)-4*channels)/(4*channels)*8
	out := make([]int16, samplesPerChannel*channels)
	for channel := 0; channel < channels; channel++ {
		header := block[channel*4:]
		states[channel].predictor = int32(int16(uint16(header[0]) | uint16(header[1])<<8))
		states[channel].index = int32(header[2])
		if states[channel].index > 88 {
			states[channel].index = 88
		}
		out[channel] = int16(states[channel].predictor)
	}
	data := block[4*channels:]
	for group := 0; (group+1)*4*channels <= len(data); group++ {
		for channel := 0; channel < channels; channel++ {
			chunk := data[(group*channels+channel)*4:]
			for nibble := 0; nibble < 8; nibble++ {
				code := chunk[nibble/2] >> (uint(nibble&1) * 4) & 0x0f
				out[(1+group*8+nibble)*channels+channel] = states[channel].decode(code)
			}
		}
	}
	return out
}
