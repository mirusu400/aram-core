package systemmachine

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/system"
)

const (
	schw830TestSourceLength = uint32(0x0100)
	schw830TestSourceWord   = uint32(0x0104)
	schw830TestVolume       = uint32(0x0108)
	schw830TestRingMode     = uint32(0x010c)
	schw830TestSource       = uint32(0x1000)
	schw830TestCommand      = uint32(0x0020)
	schw830TestClock        = uint64(1_000_000)
)

func TestSCHW830AudioDecodesFirmwareSelectedScore(t *testing.T) {
	audio, command := newSCHW830TestAudio(t, 7, 0)
	score := schw830TestSMAF()
	installSCHW830TestSource(t, audio.bus, score)

	// A falling edge alone is only the end of the hardware pulse.
	if err := command.Write(schw830TestCommand, system.Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if chunk := audio.drain(); len(chunk.PCM16) != 0 {
		t.Fatalf("falling edge produced %d PCM samples", len(chunk.PCM16))
	}

	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil { // latch the command at 2 ms
		t.Fatal(err)
	}
	if err := audio.Advance(100_000); err != nil { // render 100 ms
		t.Fatal(err)
	}
	chunk := audio.drain()
	if err := chunk.Validate(); err != nil {
		t.Fatal(err)
	}
	if chunk.SampleRate != 44_100 || chunk.Channels != 2 ||
		chunk.StartGuestNS != int64(2*time.Millisecond) || chunk.Generation != 1 {
		t.Fatalf("PCM chunk metadata = %+v", chunk)
	}
	if got, want := len(chunk.PCM16), 44_100*2/10; got != want {
		t.Fatalf("PCM samples = %d, want %d", got, want)
	}
	if peak := schw830AudioPeak(chunk.PCM16); peak < 100 {
		t.Fatalf("PCM peak = %d, want audible output", peak)
	}
}

func TestSCHW830AudioDecodesFirmwareSelectedWaveEffect(t *testing.T) {
	audio, command := newSCHW830TestAudio(t, 7, 0)
	installSCHW830TestSource(t, audio.bus, schw830TestWave())
	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(50_000); err != nil {
		t.Fatal(err)
	}
	chunk := audio.drain()
	if chunk.SampleRate != 44_100 || chunk.Channels != 2 ||
		len(chunk.PCM16) == 0 || schw830AudioPeak(chunk.PCM16) < 500 {
		t.Fatalf(
			"wave-effect PCM rate=%d channels=%d samples=%d peak=%d",
			chunk.SampleRate,
			chunk.Channels,
			len(chunk.PCM16),
			schw830AudioPeak(chunk.PCM16),
		)
	}
}

func TestSCHW830AudioAppliesFirmwareVolumeAndMute(t *testing.T) {
	render := func(level, mode uint32) []int16 {
		t.Helper()
		audio, command := newSCHW830TestAudio(t, level, mode)
		installSCHW830TestSource(t, audio.bus, schw830TestSMAF())
		if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
			t.Fatal(err)
		}
		if err := audio.Advance(1_000); err != nil {
			t.Fatal(err)
		}
		if err := audio.Advance(100_000); err != nil {
			t.Fatal(err)
		}
		return audio.drain().PCM16
	}

	full := schw830AudioPeak(render(7, 0))
	lower := schw830AudioPeak(render(3, 0))
	if full < 100 || lower == 0 || lower >= full {
		t.Fatalf("volume peaks full=%d lower=%d", full, lower)
	}
	if samples := render(7, 2); schw830AudioPeak(samples) == 0 {
		t.Fatal("ring+vibrate mode muted an audible ringtone")
	}
	for _, test := range []struct {
		name  string
		level uint32
		mode  uint32
	}{
		{name: "zero-volume", level: 0, mode: 0},
		{name: "vibrate", level: 7, mode: 1},
		{name: "mute", level: 7, mode: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			if samples := render(test.level, test.mode); len(samples) != 0 {
				t.Fatalf("muted path produced %d PCM samples", len(samples))
			}
		})
	}
}

func TestSCHW830AudioCoalescesCodecSetupPulseAndResetsTimeline(t *testing.T) {
	audio, command := newSCHW830TestAudio(t, 7, 0)
	installSCHW830TestSource(t, audio.bus, schw830TestSMAF())
	for range 3 {
		if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
			t.Fatal(err)
		}
		if err := command.Write(schw830TestCommand, system.Width16, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	firstClip := audio.clip
	if !firstClip.Valid() {
		t.Fatal("codec pulse did not create a clip")
	}
	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if audio.clip != firstClip {
		t.Fatalf("duplicate setup edge restarted clip %s as %s", firstClip, audio.clip)
	}

	if err := audio.Advance(10_000); err != nil {
		t.Fatal(err)
	}
	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if audio.clip == firstClip {
		t.Fatal("later playback command did not restart the selected score")
	}

	oldGeneration := audio.generation
	if err := audio.resetAtInstructions(5 * schw830TestClock); err != nil {
		t.Fatal(err)
	}
	if audio.generation == oldGeneration || len(audio.drain().PCM16) != 0 {
		t.Fatal("reset did not invalidate buffered PCM")
	}
	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(10_000); err != nil {
		t.Fatal(err)
	}
	chunk := audio.drain()
	if chunk.Generation != audio.generation || chunk.StartGuestNS != int64(5*time.Second+time.Millisecond) ||
		chunk.StartSample != 44 {
		t.Fatalf("post-reset PCM metadata = %+v", chunk)
	}
}

func TestSCHW830AudioCommandWindowKeepsLatchedStateContract(t *testing.T) {
	audio, command := newSCHW830TestAudio(t, 7, 0)
	if err := command.Write(0x40, system.Width16, 0x3456); err != nil {
		t.Fatal(err)
	}
	state, err := command.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if string(state[:4]) != "LRWN" || binary.LittleEndian.Uint32(state[4:8]) != 1 {
		t.Fatalf("command-window state header = %x", state[:8])
	}
	restored, err := newSCHW830AudioCommandWindow(0x100, system.Width16, audio)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if value, err := restored.Read(0x40, system.Width16); err != nil || value != 0x3456 {
		t.Fatalf("restored command register = %#x, %v", value, err)
	}
}

func TestSCHW830AudioRejectsMalformedSharedDescriptors(t *testing.T) {
	mmmd := make([]byte, 12)
	copy(mmmd, "MMMD")
	binary.BigEndian.PutUint32(mmmd[4:8], 24)
	wave := make([]byte, 12)
	copy(wave, "RIFF")
	binary.LittleEndian.PutUint32(wave[4:8], 24)
	copy(wave[8:12], "WAVE")
	midi := make([]byte, 12)
	copy(midi, "MThd")
	binary.BigEndian.PutUint32(midi[4:8], 6)
	for _, test := range []struct {
		name       string
		header     []byte
		descriptor uint32
		maximum    uint32
		want       uint32
		ok         bool
	}{
		{name: "SMAF", header: mmmd, descriptor: 32, maximum: 64, want: 32, ok: true},
		{name: "wave", header: wave, descriptor: 32, maximum: 64, want: 32, ok: true},
		{name: "MIDI", header: midi, descriptor: 48, maximum: 64, want: 48, ok: true},
		{name: "truncated", header: mmmd, descriptor: 31, maximum: 64},
		{name: "over-limit", header: mmmd, descriptor: 32, maximum: 31},
		{name: "unknown", header: []byte("NOPE00000000"), descriptor: 12, maximum: 64},
		{name: "short-header", header: mmmd[:8], descriptor: 32, maximum: 64},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := schw830EncodedLength(test.header, test.descriptor, test.maximum)
			if got != test.want || ok != test.ok {
				t.Fatalf("encoded length = %d/%t, want %d/%t", got, ok, test.want, test.ok)
			}
		})
	}
}

func newSCHW830TestAudio(
	t *testing.T,
	volume, ringMode uint32,
) (*schw830Audio, *schw830AudioCommandWindow) {
	t.Helper()
	bus := system.NewBus()
	if err := bus.MapRAM("ram", 0, 0x4000); err != nil {
		t.Fatal(err)
	}
	config := schw830AudioConfig{
		instructionsPerSecond: schw830TestClock,
		commandOffset:         schw830TestCommand,
		sourceLengthAddress:   schw830TestSourceLength,
		sourcePointerAddress:  schw830TestSourceWord,
		volumeAddress:         schw830TestVolume,
		ringModeAddress:       schw830TestRingMode,
		maximumSourceBytes:    0x2000,
		gainPollInstructions:  1_000,
		duplicateWindow:       5 * time.Millisecond,
	}
	audio, err := newSCHW830Audio(bus, config)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newSCHW830AudioCommandWindow(0x100, system.Width16, audio)
	if err != nil {
		t.Fatal(err)
	}
	writeSCHW830TestWord(t, bus, schw830TestVolume, volume)
	writeSCHW830TestWord(t, bus, schw830TestRingMode, ringMode)
	return audio, command
}

func installSCHW830TestSource(t *testing.T, bus *system.Bus, source []byte) {
	t.Helper()
	writeSCHW830TestWord(t, bus, schw830TestSourceLength, uint32(len(source)))
	writeSCHW830TestWord(t, bus, schw830TestSourceWord, schw830TestSource)
	if ok, err := bus.WriteBlock(schw830TestSource, source, cpu.PermissionWrite); err != nil || !ok {
		t.Fatalf("write test score: direct=%t error=%v", ok, err)
	}
}

func writeSCHW830TestWord(t *testing.T, bus *system.Bus, address, value uint32) {
	t.Helper()
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	if err := bus.Write(address, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
}

func schw830TestSMAF() []byte {
	sequence := []byte{
		0x00, 0xb0, 0x07, 0x7f,
		0x00, 0xc0, 0x00,
		0x00, 0x90, 60, 110, 125,
		0x7d, 0x80, 64, 125,
		0x7d, 0xff, 0x2f,
	}
	trackBody := []byte{0x02, 0x00, 0x02, 0x02}
	trackBody = append(trackBody, make([]byte, 16)...)
	trackBody = appendSCHW830TestSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSCHW830TestSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

func schw830TestWave() []byte {
	const (
		sampleRate = uint32(8_000)
		frames     = 800
	)
	dataSize := frames * 2
	result := make([]byte, 44+dataSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], sampleRate)
	binary.LittleEndian.PutUint32(result[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	for frame := range frames {
		sample := int16(2_000)
		if frame%2 != 0 {
			sample = -sample
		}
		binary.LittleEndian.PutUint16(result[44+frame*2:], uint16(sample))
	}
	return result
}

func appendSCHW830TestSMAFChunk(destination, id, body []byte) []byte {
	destination = append(destination, id...)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(body)))
	destination = append(destination, size[:]...)
	return append(destination, body...)
}

func schw830AudioPeak(samples []int16) int32 {
	var peak int32
	for _, sample := range samples {
		value := int32(sample)
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	return peak
}

// countingSCHW830Device stands in for the boot-control and UART registers that
// answer a read by clearing or advancing their own state.
type countingSCHW830Device struct{ reads int }

func (d *countingSCHW830Device) Reset() error { return nil }

func (d *countingSCHW830Device) Read(uint32, system.Width) (uint32, error) {
	d.reads++
	return 0x444d4d4d, nil
}

func (d *countingSCHW830Device) Write(uint32, system.Width, uint32) error { return nil }

func TestSCHW830AudioLeavesDevicesAloneForAnMMIODescriptor(t *testing.T) {
	audio, command := newSCHW830TestAudio(t, 7, 0)
	device := &countingSCHW830Device{}
	if err := audio.bus.MapMMIO("registers", 0x8000, 0x1000, device); err != nil {
		t.Fatal(err)
	}
	writeSCHW830TestWord(t, audio.bus, schw830TestSourceLength, 0x40)
	writeSCHW830TestWord(t, audio.bus, schw830TestSourceWord, 0x8000)

	if err := command.Write(schw830TestCommand, system.Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := audio.Advance(1_000); err != nil {
		t.Fatal(err)
	}
	if audio.clip.Valid() {
		t.Fatal("an MMIO descriptor produced a clip")
	}
	if device.reads != 0 {
		t.Fatalf("descriptor read reached the device %d times", device.reads)
	}
}
