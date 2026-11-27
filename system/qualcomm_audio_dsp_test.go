package system

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func testAudioDSPProfile() QualcommAudioDSPProfile {
	return QualcommAudioDSPProfile{
		MemoryID:      "dsp",
		CommandOffset: 0x0400, ArgumentOffset: 0x0402,
		ChannelOffset: 0x0500, ChannelStride: 0x4a, Channels: 2,
		ServiceOffset: 0x0600, EventOffset: 0x0602, EventValue: 0x0311,
		ControlFlagOffset: 0x0610, ControlFlags: 2,
		PrivateStateOffset:         0x9000,
		InterruptGroupStatusOffset: 0x84, InterruptGroupMask: 0x01,
		// One output frame per instruction keeps the arithmetic visible.
		InstructionsPerSecond: 8000, OutputSampleRate: 8000,
	}
}

type audioDSPHarness struct {
	t         *testing.T
	bus       *Bus
	audio     *QualcommAudioDSP
	interrupt *graphicsDSPInterruptProbe
}

func newAudioDSPHarness(t *testing.T, graphics bool) *audioDSPHarness {
	t.Helper()
	board := BoardProfile{
		ID: "test.board",
		Memory: []MemoryRegionProfile{
			{ID: "ram", Kind: MemoryRAM, Address: 0x00000000, Size: 0x00100000},
			{ID: "dsp", Kind: MemorySparseRAM, Address: 0x70000000, Size: 0x00200000},
		},
	}
	profile := testAudioDSPProfile()
	check(t, profile.validate(board.Memory))
	board.AudioDSP = &profile
	if graphics {
		graphicsProfile := testGraphicsDSPProfile()
		board.GraphicsDSP = &graphicsProfile
	}
	bus := NewBus()
	check(t, bus.MapRAM("ram", 0x00000000, 0x00100000))
	check(t, bus.MapSparseRAM("dsp", 0x70000000, 0x00200000))
	interrupt := &graphicsDSPInterruptProbe{}
	audio, err := board.AttachDSPModules(bus, interrupt)
	check(t, err)
	if audio == nil {
		t.Fatal("no audio module was attached")
	}
	return &audioDSPHarness{t: t, bus: bus, audio: audio, interrupt: interrupt}
}

func (h *audioDSPHarness) write16(address uint32, value uint16) {
	h.t.Helper()
	var raw [2]byte
	binary.LittleEndian.PutUint16(raw[:], value)
	check(h.t, h.bus.Write(address, raw[:], cpu.PermissionWrite))
}

func (h *audioDSPHarness) read16(address uint32) uint16 {
	h.t.Helper()
	var raw [2]byte
	check(h.t, h.bus.Read(address, raw[:], cpu.PermissionRead))
	return binary.LittleEndian.Uint16(raw[:])
}

func (h *audioDSPHarness) command(word uint16, arguments ...uint16) {
	h.t.Helper()
	for index, argument := range arguments {
		h.write16(0x70000402+uint32(index)*2, argument)
	}
	h.write16(0x70000400, word)
}

// lend hands the channel-0 slot a buffer the way the driver does.
func (h *audioDSPHarness) lend(slot uint32, address uint32, data []byte, sequence uint16) {
	h.t.Helper()
	check(h.t, h.bus.WriteMemory(address, data, cpu.PermissionWrite))
	base := 0x70000500 + 0x0e + slot*8
	h.write16(base, uint16(address>>17))
	h.write16(base+2, uint16(address>>1))
	h.write16(base+4, uint16(len(data)/2))
	h.write16(base+6, sequence)
	h.write16(0x70000500+0x22, 0)
}

func audioTestBlock(seed byte) []byte {
	block := make([]byte, 36) // 65 mono samples
	block[0], block[1], block[2] = seed, 0, 20
	for index := 4; index < len(block); index++ {
		block[index] = byte(index)*37 + seed
	}
	return block
}

func stereo(samples []int16) []int16 {
	out := make([]int16, 0, len(samples)*2)
	for _, sample := range samples {
		out = append(out, sample, sample)
	}
	return out
}

func equalPCM(left, right []int16) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestQualcommAudioDSPPlaysLentADPCMBuffers(t *testing.T) {
	h := newAudioDSPHarness(t, true)
	h.write16(0x70000500+0x28, 0x2000)
	h.audio.Restart(0)
	if chunk := h.audio.DrainAudio(); chunk.Generation == 0 || len(chunk.PCM16) != 0 {
		t.Fatalf("restart marker = %+v", chunk)
	}

	// A positional-audio control buffer is taken at once.
	h.write16(0x70000612, 3)
	if h.read16(0x70000612) != 0 {
		t.Fatal("control flag was not cleared")
	}

	// Start channel 0: IMA ADPCM, 8000 Hz, mono, 65 samples per block.
	h.command(qualcommAudioCommandStart, 0x8000, 0, 8000, 1, 65, 0)
	if h.read16(0x70000502) == 0 || h.read16(0x70000522) == 0 ||
		h.read16(0x70000600) == 0 || h.read16(0x70000602) != 0x0311 || len(h.interrupt.pulses) != 1 {
		t.Fatalf("start did not request data: state %#x request %#x pulses %d",
			h.read16(0x70000502), h.read16(0x70000522), len(h.interrupt.pulses))
	}
	check(t, h.audio.Advance(40))
	if chunk := h.audio.DrainAudio(); len(chunk.PCM16) != 0 {
		t.Fatalf("a starved channel produced %d samples", len(chunk.PCM16))
	}

	// Two buffers that split the second block between them. Sequence 3 plays
	// before sequence 1.
	first, second, third := audioTestBlock(1), audioTestBlock(90), audioTestBlock(7)
	data := append(append(append([]byte(nil), first...), second...), third...)
	h.lend(1, 0x2000, data[:50], 3)
	h.lend(0, 0x3000, data[50:], 1)
	check(t, h.audio.Advance(100))
	chunk := h.audio.DrainAudio()
	want := stereo(append(decodeIMAADPCMBlock(first, 1), decodeIMAADPCMBlock(second, 1)...))
	if chunk.SampleRate != 8000 || chunk.Channels != 2 || chunk.StartSample != 40 ||
		!equalPCM(chunk.PCM16, want[:200]) {
		t.Fatalf("first 100 frames: rate %d channels %d start %d, %d samples",
			chunk.SampleRate, chunk.Channels, chunk.StartSample, len(chunk.PCM16))
	}
	if h.read16(0x7000050e+8+6) != 0 || h.read16(0x7000050e+6) != 1 {
		t.Fatalf("slot flags = %d, %d; the played buffer must be released and the other kept",
			h.read16(0x7000050e+6), h.read16(0x7000050e+8+6))
	}
	if h.read16(0x70000522) == 0 || len(h.interrupt.pulses) < 2 {
		t.Fatal("releasing a buffer did not request more data")
	}

	// A restored snapshot resumes from the mirrored position. The model had
	// decoded ahead into block two, which a restore replays from its start.
	h.audio.Restart(140)
	h.audio.DrainAudio()
	check(t, h.audio.Advance(10))
	chunk = h.audio.DrainAudio()
	if chunk.StartSample != 0 || len(chunk.PCM16) != 20 || !equalPCM(chunk.PCM16, stereo(decodeIMAADPCMBlock(third, 1))[:20]) {
		t.Fatalf("resumed audio: start %d samples %d", chunk.StartSample, len(chunk.PCM16))
	}

	// Stop lets what is lent play out, then the channel goes quiet.
	h.command(qualcommAudioCommandControl, 1, qualcommAudioControlStop)
	if h.read16(0x70000502) != 0 || h.read16(0x7000050e+6) != 0 {
		t.Fatal("stop left the channel open")
	}
	check(t, h.audio.Advance(200))
	chunk = h.audio.DrainAudio()
	if len(chunk.PCM16) != (65-10)*2 || !equalPCM(chunk.PCM16, stereo(decodeIMAADPCMBlock(third, 1))[20:]) {
		t.Fatalf("drained tail has %d samples", len(chunk.PCM16))
	}
	check(t, h.audio.Advance(50))
	if chunk := h.audio.DrainAudio(); len(chunk.PCM16) != 0 {
		t.Fatal("a stopped channel kept playing")
	}
}

func TestQualcommAudioDSPResamplesPCMAndAppliesControls(t *testing.T) {
	h := newAudioDSPHarness(t, false)
	h.write16(0x70000500+0x28, 0x1000) // half volume
	// Signed 16-bit PCM at half the output rate: every source frame is
	// reached on alternate output frames, with the midpoint between.
	h.command(qualcommAudioCommandStart, 0, 2, 4000, 1, 11, 0)
	if h.read16(0x70000502) != 0 {
		t.Fatal("a deferred start opened the channel")
	}
	h.command(qualcommAudioCommandControl, 1, qualcommAudioControlStart)
	if h.read16(0x70000502) == 0 {
		t.Fatal("start control did not open the channel")
	}
	pcm := []int16{0, 1000, 2000, -4000, 800, 800}
	raw := make([]byte, len(pcm)*2)
	for index, sample := range pcm {
		binary.LittleEndian.PutUint16(raw[index*2:], uint16(sample))
	}
	h.lend(0, 0x4000, raw, 1)
	check(t, h.audio.Advance(6))
	chunk := h.audio.DrainAudio()
	want := stereo([]int16{0, 250, 500, 750, 1000, -500})
	if !equalPCM(chunk.PCM16, want) {
		t.Fatalf("resampled PCM = %v, want %v", chunk.PCM16, want)
	}

	// Pause holds the position; resume continues; flush discards.
	h.command(qualcommAudioCommandControl, 1, qualcommAudioControlPause)
	check(t, h.audio.Advance(10))
	if chunk := h.audio.DrainAudio(); len(chunk.PCM16) != 0 {
		t.Fatal("a paused channel played")
	}
	h.command(qualcommAudioCommandControl, 1, qualcommAudioControlResume)
	check(t, h.audio.Advance(1))
	if chunk := h.audio.DrainAudio(); !equalPCM(chunk.PCM16, []int16{-2000, -2000}) || chunk.StartSample != 16 {
		t.Fatalf("resumed frame = %v at %d", chunk.PCM16, chunk.StartSample)
	}
	h.lend(1, 0x5000, raw, 2)
	h.command(qualcommAudioCommandControl, 1, qualcommAudioControlFlush)
	if h.read16(0x7000050e+6) != 0 || h.read16(0x7000050e+8+6) != 0 {
		t.Fatal("flush kept a buffer")
	}
	check(t, h.audio.Advance(4))
	if chunk := h.audio.DrainAudio(); len(chunk.PCM16) != 0 {
		t.Fatal("flushed data played")
	}
}

func TestQualcommAudioDSPProfileRejectsMisplacedVariables(t *testing.T) {
	memory := []MemoryRegionProfile{{ID: "dsp", Kind: MemorySparseRAM, Address: 0x70000000, Size: 0x00200000}}
	for name, mutate := range map[string]func(*QualcommAudioDSPProfile){
		"unknown region":  func(p *QualcommAudioDSPProfile) { p.MemoryID = "missing" },
		"odd command":     func(p *QualcommAudioDSPProfile) { p.CommandOffset = 0x401 },
		"no channels":     func(p *QualcommAudioDSPProfile) { p.Channels = 0 },
		"short stride":    func(p *QualcommAudioDSPProfile) { p.ChannelStride = 0x20 },
		"private outside": func(p *QualcommAudioDSPProfile) { p.PrivateStateOffset = 0x001ffff0 },
		"no interrupt":    func(p *QualcommAudioDSPProfile) { p.InterruptGroupMask = 0 },
		"no clock":        func(p *QualcommAudioDSPProfile) { p.InstructionsPerSecond = 0 },
		"bad rate":        func(p *QualcommAudioDSPProfile) { p.OutputSampleRate = 100 },
	} {
		profile := testAudioDSPProfile()
		mutate(&profile)
		if profile.validate(memory) == nil {
			t.Errorf("%s: profile was accepted", name)
		}
	}
}
