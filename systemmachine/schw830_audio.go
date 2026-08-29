package systemmachine

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	aramruntime "github.com/mirusu400/aram-core/runtime"
	"github.com/mirusu400/aram-core/system"
)

const (
	schw830AudioCommandWindowID      = "external-16bit-bank-0"
	schw830AudioCommandOffset        = uint32(0x2186)
	schw830AudioSourceLengthAddress  = uint32(0x035010e0)
	schw830AudioSourcePointerAddress = uint32(0x035010e8)
	schw830AudioVolumeAddress        = uint32(0x027b611c)
	schw830AudioRingModeAddress      = uint32(0x02b70d10)

	schw830AudioInstructionsPerSecond = uint64(60_000_000)
	schw830AudioMaximumSourceBytes    = uint32(16 << 20)
	schw830AudioGainPollInterval      = 10 * time.Millisecond
	schw830AudioDuplicateWindow       = 5 * time.Millisecond
	schw830AudioRetention             = 500 * time.Millisecond

	schw830AudioOwner aramruntime.OwnerID = 0x830
)

// DL21's multimedia parser publishes the complete encoded object through a
// small descriptor in EBI RAM before its codec setup code pulses bank 0 at
// offset 0x2186. The addresses below are deliberately kept in a config so the
// transport and timing can be covered with compact synthetic RAM in tests.
type schw830AudioConfig struct {
	instructionsPerSecond uint64
	commandOffset         uint32
	sourceLengthAddress   uint32
	sourcePointerAddress  uint32
	volumeAddress         uint32
	ringModeAddress       uint32
	maximumSourceBytes    uint32
	gainPollInstructions  uint64
	duplicateWindow       time.Duration
}

func defaultSCHW830AudioConfig(instructionsPerSecond uint64) schw830AudioConfig {
	if instructionsPerSecond == 0 {
		instructionsPerSecond = schw830AudioInstructionsPerSecond
	}
	gainPollInstructions := instructionsPerSecond *
		uint64(schw830AudioGainPollInterval) / uint64(time.Second)
	if gainPollInstructions == 0 {
		gainPollInstructions = 1
	}
	return schw830AudioConfig{
		instructionsPerSecond: instructionsPerSecond,
		commandOffset:         schw830AudioCommandOffset,
		sourceLengthAddress:   schw830AudioSourceLengthAddress,
		sourcePointerAddress:  schw830AudioSourcePointerAddress,
		volumeAddress:         schw830AudioVolumeAddress,
		ringModeAddress:       schw830AudioRingModeAddress,
		maximumSourceBytes:    schw830AudioMaximumSourceBytes,
		gainPollInstructions:  gainPollInstructions,
		duplicateWindow:       schw830AudioDuplicateWindow,
	}
}

// schw830Audio is a host-side presentation device for the original DL21
// multimedia path. It does not replace the firmware parser or command queue:
// those still run and populate guest RAM. It only turns the encoded object the
// firmware selected into the PCM that a headless system machine can expose.
type schw830Audio struct {
	bus    *system.Bus
	config schw830AudioConfig

	media  *aramruntime.Media
	events *aramruntime.EventBus
	clip   aramruntime.ServiceID

	commandPending bool
	timeRemainder  uint64
	now            time.Duration
	mediaNow       time.Duration
	epoch          time.Duration
	generation     uint64

	lastSignature    [sha256.Size]byte
	hasLastSignature bool
	lastTrigger      time.Duration
	gainPoll         uint64

	queued        []core.AudioChunk
	queuedHead    int
	queuedSamples int
	audioCursor   uint64
	cursorValid   bool
}

func newSCHW830Audio(bus *system.Bus, config schw830AudioConfig) (*schw830Audio, error) {
	if bus == nil || config.instructionsPerSecond == 0 ||
		config.instructionsPerSecond > math.MaxUint64/uint64(time.Second) ||
		config.maximumSourceBytes < 12 || config.gainPollInstructions == 0 ||
		config.duplicateWindow < 0 {
		return nil, fmt.Errorf("invalid SCH-W830 audio configuration")
	}
	audio := &schw830Audio{bus: bus, config: config}
	if err := audio.resetAtInstructions(0); err != nil {
		return nil, err
	}
	return audio, nil
}

func (a *schw830Audio) resetAtInstructions(instructions uint64) error {
	media, err := aramruntime.NewMedia(
		aramruntime.NewRegistry(4),
		aramruntime.DefaultMediaLimits(),
	)
	if err != nil {
		return fmt.Errorf("create SCH-W830 audio mixer: %w", err)
	}
	now, remainder, err := schw830AudioTime(instructions, a.config.instructionsPerSecond)
	if err != nil {
		return err
	}
	a.media = media
	a.events = aramruntime.NewEventBus(32, 64)
	a.clip = 0
	a.commandPending = false
	a.timeRemainder = remainder
	a.now = now
	a.mediaNow = now
	a.epoch = now
	if a.generation == 0 || a.generation == math.MaxUint64 {
		a.generation = 1
	} else {
		a.generation++
	}
	a.lastSignature = [sha256.Size]byte{}
	a.hasLastSignature = false
	a.lastTrigger = 0
	a.gainPoll = 0
	a.clearQueuedAudio()
	a.audioCursor = 0
	a.cursorValid = false
	return nil
}

func schw830AudioTime(instructions, instructionsPerSecond uint64) (time.Duration, uint64, error) {
	if instructionsPerSecond == 0 ||
		instructionsPerSecond > math.MaxUint64/uint64(time.Second) {
		return 0, 0, fmt.Errorf("SCH-W830 audio clock rate is invalid")
	}
	seconds := instructions / instructionsPerSecond
	if seconds > uint64(math.MaxInt64)/uint64(time.Second) {
		return 0, 0, fmt.Errorf("SCH-W830 audio clock exceeds duration range")
	}
	partial := instructions % instructionsPerSecond
	numerator := partial * uint64(time.Second)
	nanoseconds := seconds*uint64(time.Second) + numerator/instructionsPerSecond
	if nanoseconds > uint64(math.MaxInt64) {
		return 0, 0, fmt.Errorf("SCH-W830 audio clock exceeds duration range")
	}
	return time.Duration(nanoseconds), numerator % instructionsPerSecond, nil
}

// observeCodecWrite receives the evidenced three 1/0 setup pulses without
// doing any bus work while the MMIO window is locked. Multiple rising edges in
// one runner slice coalesce; the content/time check handles edges split across
// adjacent slices.
func (a *schw830Audio) observeCodecWrite(offset uint32, width system.Width, value uint32) {
	if offset == a.config.commandOffset && width == system.Width16 && value == 1 {
		a.commandPending = true
	}
}

func (a *schw830Audio) Advance(retiredInstructions uint64) error {
	delta, remainder, err := schw830AudioTimeDelta(
		retiredInstructions,
		a.config.instructionsPerSecond,
		a.timeRemainder,
	)
	if err != nil {
		return err
	}
	if delta > 0 && a.now > time.Duration(math.MaxInt64)-delta {
		return fmt.Errorf("SCH-W830 audio timeline overflow")
	}
	end := a.now + delta
	a.timeRemainder = remainder
	a.now = end
	if a.clip.Valid() && (a.gainPollDue(retiredInstructions) || a.commandPending) {
		if err := a.renderUntil(end); err != nil {
			return err
		}
	}
	if a.commandPending {
		a.commandPending = false
		a.handlePlaybackCommand()
	}
	return nil
}

func (a *schw830Audio) renderUntil(end time.Duration) error {
	if end < a.mediaNow {
		return fmt.Errorf("SCH-W830 PCM timeline moved backwards")
	}
	a.applyFirmwareGain()
	if end == a.mediaNow {
		return nil
	}
	start := a.mediaNow
	if err := a.media.Advance(start, end, a.events); err != nil {
		return fmt.Errorf("advance SCH-W830 PCM mixer: %w", err)
	}
	a.publish(a.media.Drain(), start)
	a.mediaNow = end
	for {
		if _, ok := a.events.PopReady(end); !ok {
			break
		}
	}
	info, err := a.media.Info(schw830AudioOwner, a.clip)
	if err == nil && info.State == aramruntime.ClipStopped {
		_ = a.media.DestroyClip(schw830AudioOwner, a.clip, a.events)
		a.clip = 0
	}
	return nil
}

func schw830AudioTimeDelta(
	instructions, instructionsPerSecond, carried uint64,
) (time.Duration, uint64, error) {
	if instructionsPerSecond == 0 || carried >= instructionsPerSecond {
		return 0, 0, fmt.Errorf("invalid SCH-W830 audio clock state")
	}
	seconds := instructions / instructionsPerSecond
	if seconds > uint64(math.MaxInt64)/uint64(time.Second) {
		return 0, 0, fmt.Errorf("SCH-W830 audio advance exceeds duration range")
	}
	partial := instructions % instructionsPerSecond
	// partial is strictly below the clock rate. DL21's 60 MHz rate keeps this
	// product well inside uint64; reject a synthetic rate that would not.
	if partial != 0 && uint64(time.Second) > (math.MaxUint64-carried)/partial {
		return 0, 0, fmt.Errorf("SCH-W830 audio clock conversion overflow")
	}
	numerator := partial*uint64(time.Second) + carried
	nanoseconds := seconds*uint64(time.Second) + numerator/instructionsPerSecond
	if nanoseconds > uint64(math.MaxInt64) {
		return 0, 0, fmt.Errorf("SCH-W830 audio advance exceeds duration range")
	}
	return time.Duration(nanoseconds), numerator % instructionsPerSecond, nil
}

func (a *schw830Audio) gainPollDue(retired uint64) bool {
	interval := a.config.gainPollInstructions
	remainder := retired % interval
	due := retired >= interval
	if remainder >= interval-a.gainPoll {
		a.gainPoll = remainder - (interval - a.gainPoll)
		return true
	}
	a.gainPoll += remainder
	return due
}

func (a *schw830Audio) handlePlaybackCommand() {
	source, ok := a.readEncodedSource()
	if !ok {
		return
	}
	signature := sha256.Sum256(source)
	if a.hasLastSignature && signature == a.lastSignature &&
		a.now-a.lastTrigger < a.config.duplicateWindow {
		return
	}

	clip, err := a.media.CreateClip(schw830AudioOwner, "application/octet-stream", uint64(len(source)))
	if err != nil {
		return
	}
	discard := func() {
		_ = a.media.DestroyClip(schw830AudioOwner, clip, a.events)
	}
	if _, err := a.media.Append(schw830AudioOwner, clip, source); err != nil {
		discard()
		return
	}
	if err := a.media.Play(schw830AudioOwner, clip, 1); err != nil {
		discard()
		return
	}
	info, err := a.media.Info(schw830AudioOwner, clip)
	if err != nil || !info.Decoded || info.Duration <= 0 {
		discard()
		return
	}
	if a.clip.Valid() {
		_ = a.media.DestroyClip(schw830AudioOwner, a.clip, a.events)
	}
	a.clip = clip
	a.mediaNow = a.now
	a.lastSignature = signature
	a.hasLastSignature = true
	a.lastTrigger = a.now
	a.gainPoll = 0
	a.applyFirmwareGain()
}

func (a *schw830Audio) readEncodedSource() ([]byte, bool) {
	length, ok := a.readWord(a.config.sourceLengthAddress)
	if !ok || length < 12 || length > a.config.maximumSourceBytes {
		return nil, false
	}
	address, ok := a.readWord(a.config.sourcePointerAddress)
	if !ok || uint64(address)+uint64(length) > 1<<32 {
		return nil, false
	}
	header := make([]byte, 12)
	if err := readSCHW830GuestBlock(a.bus, address, header); err != nil {
		return nil, false
	}
	length, ok = schw830EncodedLength(header, length, a.config.maximumSourceBytes)
	if !ok {
		return nil, false
	}
	source := make([]byte, int(length))
	if err := readSCHW830GuestBlock(a.bus, address, source); err != nil {
		return nil, false
	}
	return source, true
}

func schw830EncodedLength(header []byte, descriptorLength, maximum uint32) (uint32, bool) {
	if len(header) < 12 || descriptorLength < 12 || descriptorLength > maximum {
		return 0, false
	}
	switch {
	case bytes.Equal(header[:4], []byte("MMMD")):
		body := binary.BigEndian.Uint32(header[4:8])
		if body > maximum-8 {
			return 0, false
		}
		total := body + 8
		if total < 12 || total > descriptorLength {
			return 0, false
		}
		return total, true
	case bytes.Equal(header[:4], []byte("MThd")):
		return descriptorLength, binary.BigEndian.Uint32(header[4:8]) == 6
	case bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")):
		body := binary.LittleEndian.Uint32(header[4:8])
		if body > maximum-8 {
			return 0, false
		}
		total := body + 8
		if total < 12 || total > descriptorLength {
			return 0, false
		}
		return total, true
	default:
		return 0, false
	}
}

func (a *schw830Audio) applyFirmwareGain() {
	level, ok := a.readWord(a.config.volumeAddress)
	if !ok {
		return
	}
	if level > 7 {
		level = 7
	}
	mode, ok := a.readWord(a.config.ringModeAddress)
	if !ok {
		return
	}
	// The traced settings values are 0=ringtone, 1=vibrate, and 4=mute.
	// Modes 2 and 3 include an audible ringtone and therefore keep PCM enabled.
	muted := level == 0 || mode == 1 || mode == 4
	_ = a.media.SetGlobalGain(uint8(level*100/7), muted)
}

func (a *schw830Audio) readWord(address uint32) (uint32, bool) {
	var data [4]byte
	if err := readSCHW830GuestBlock(a.bus, address, data[:]); err != nil {
		return 0, false
	}
	return binary.LittleEndian.Uint32(data[:]), true
}

// readSCHW830GuestBlock reads what the firmware published, never what a device
// would answer. The descriptor pointer is guest data, so a stale or partly
// written word can address MMIO; running that through the ordinary read path
// would let this host-side device clear the boot-control SBI completion status
// or advance a legacy UART the firmware is still using, and would put host
// bookkeeping into an attributed trace.
func readSCHW830GuestBlock(bus *system.Bus, address uint32, destination []byte) error {
	return bus.ReadMemory(address, destination, cpu.PermissionRead)
}

func (a *schw830Audio) publish(audio aramruntime.AudioBuffer, start time.Duration) {
	if audio.SampleRate <= 0 || audio.Channels <= 0 || len(audio.PCM16) == 0 ||
		len(audio.PCM16)%audio.Channels != 0 {
		return
	}
	if start < a.epoch {
		start = a.epoch
	}
	startSample := a.audioStartSample(start-a.epoch, audio.SampleRate)
	chunk := core.AudioChunk{
		SampleRate:   audio.SampleRate,
		Channels:     audio.Channels,
		PCM16:        audio.PCM16,
		StartGuestNS: int64(start),
		StartSample:  startSample,
		Generation:   a.generation,
	}
	frames := uint64(len(chunk.PCM16) / chunk.Channels)
	a.audioCursor = startSample + frames
	a.cursorValid = true

	if len(a.queued) > a.queuedHead {
		last := &a.queued[len(a.queued)-1]
		lastEnd := last.StartSample + uint64(len(last.PCM16)/last.Channels)
		if last.Generation == chunk.Generation && last.SampleRate == chunk.SampleRate &&
			last.Channels == chunk.Channels && lastEnd == chunk.StartSample {
			last.PCM16 = append(last.PCM16, chunk.PCM16...)
			a.queuedSamples += len(chunk.PCM16)
			a.retainQueuedAudio(audio.SampleRate, audio.Channels)
			return
		}
	}
	a.queued = append(a.queued, chunk)
	a.queuedSamples += len(chunk.PCM16)
	a.retainQueuedAudio(audio.SampleRate, audio.Channels)
}

func (a *schw830Audio) audioStartSample(elapsed time.Duration, sampleRate int) uint64 {
	anchor := schw830SampleCursor(elapsed, sampleRate)
	if !a.cursorValid {
		return anchor
	}
	slack := schw830SampleCursor(time.Millisecond, sampleRate)
	if anchor >= a.audioCursor {
		if anchor-a.audioCursor <= slack {
			return a.audioCursor
		}
		return anchor
	}
	if a.audioCursor-anchor <= slack {
		return a.audioCursor
	}
	return anchor
}

func schw830SampleCursor(elapsed time.Duration, sampleRate int) uint64 {
	if elapsed <= 0 || sampleRate <= 0 {
		return 0
	}
	seconds := uint64(elapsed / time.Second)
	remainder := uint64(elapsed % time.Second)
	return seconds*uint64(sampleRate) + remainder*uint64(sampleRate)/uint64(time.Second)
}

func (a *schw830Audio) retainQueuedAudio(sampleRate, channels int) {
	limit := int(int64(sampleRate) * int64(channels) * int64(schw830AudioRetention) / int64(time.Second))
	if limit < channels {
		limit = channels
	}
	for a.queuedHead < len(a.queued) && a.queuedSamples > limit {
		chunk := &a.queued[a.queuedHead]
		overflow := a.queuedSamples - limit
		drop := min(overflow, len(chunk.PCM16))
		drop -= drop % chunk.Channels
		if drop == 0 {
			break
		}
		if drop == len(chunk.PCM16) {
			a.queuedSamples -= drop
			*chunk = core.AudioChunk{}
			a.queuedHead++
			continue
		}
		frames := drop / chunk.Channels
		chunk.PCM16 = chunk.PCM16[drop:]
		chunk.StartSample += uint64(frames)
		chunk.StartGuestNS += int64(time.Duration(int64(frames) * int64(time.Second) / int64(chunk.SampleRate)))
		a.queuedSamples -= drop
	}
	a.compactQueuedAudio()
}

func (a *schw830Audio) drain() core.AudioChunk {
	if a.queuedHead >= len(a.queued) {
		return core.AudioChunk{}
	}
	chunk := a.queued[a.queuedHead]
	a.queued[a.queuedHead] = core.AudioChunk{}
	a.queuedHead++
	a.queuedSamples -= len(chunk.PCM16)
	a.compactQueuedAudio()
	return chunk
}

func (a *schw830Audio) compactQueuedAudio() {
	if a.queuedHead == len(a.queued) {
		a.queued = a.queued[:0]
		a.queuedHead = 0
	} else if a.queuedHead >= 32 && a.queuedHead*2 >= len(a.queued) {
		copy(a.queued, a.queued[a.queuedHead:])
		a.queued = a.queued[:len(a.queued)-a.queuedHead]
		a.queuedHead = 0
	}
}

func (a *schw830Audio) clearQueuedAudio() {
	for index := a.queuedHead; index < len(a.queued); index++ {
		a.queued[index] = core.AudioChunk{}
	}
	a.queued = a.queued[:0]
	a.queuedHead = 0
	a.queuedSamples = 0
}

// schw830AudioCommandWindow preserves the profile's exact LRWN snapshot bytes
// while adding the single evidenced codec side effect.
type schw830AudioCommandWindow struct {
	window *system.LatchedRegisterWindow
	audio  *schw830Audio
}

func newSCHW830AudioCommandWindow(
	size uint32,
	width system.Width,
	audio *schw830Audio,
) (*schw830AudioCommandWindow, error) {
	window, err := system.NewLatchedRegisterWindow(size, width)
	if err != nil {
		return nil, err
	}
	if audio == nil {
		return nil, fmt.Errorf("SCH-W830 audio command window requires audio device")
	}
	return &schw830AudioCommandWindow{window: window, audio: audio}, nil
}

func (d *schw830AudioCommandWindow) Reset() error {
	return d.window.Reset()
}

func (d *schw830AudioCommandWindow) Read(offset uint32, width system.Width) (uint32, error) {
	return d.window.Read(offset, width)
}

func (d *schw830AudioCommandWindow) Write(offset uint32, width system.Width, value uint32) error {
	if err := d.window.Write(offset, width, value); err != nil {
		return err
	}
	d.audio.observeCodecWrite(offset, width, value)
	return nil
}

func (d *schw830AudioCommandWindow) SaveState() ([]byte, error) {
	return d.window.SaveState()
}

func (d *schw830AudioCommandWindow) LoadState(state []byte) error {
	return d.window.LoadState(state)
}

// DrainAudio implements the same PCM stream contract as the application
// machine, allowing the existing frontend path to consume native firmware
// audio without a system-machine-specific output API.
func (m *Machine) DrainAudio() core.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() || m.audio == nil {
		return core.AudioChunk{}
	}
	return m.audio.drain()
}

var (
	_ system.ClockedDevice  = (*schw830Audio)(nil)
	_ system.Device         = (*schw830AudioCommandWindow)(nil)
	_ system.StatefulDevice = (*schw830AudioCommandWindow)(nil)
)
