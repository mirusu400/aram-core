package system

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

// QualcommAudioDSPProfile locates the wave-playback ("ADEC") interface of an
// application-DSP audio image. The ARM-side driver parses a sound file's
// header itself, opens one of a few DSP channels with the stream's format and
// then lends the DSP the address of each encoded data buffer; the DSP decodes
// at the stream's rate and releases a buffer when it has played it. All
// offsets are relative to the region named by MemoryID.
type QualcommAudioDSPProfile struct {
	MemoryID string

	// CommandOffset is the 16-bit command word; its 16-bit arguments follow at
	// ArgumentOffset and are stored before it.
	CommandOffset  uint32
	ArgumentOffset uint32

	// ChannelOffset is the first per-channel block; Channels blocks follow one
	// another ChannelStride bytes apart.
	ChannelOffset uint32
	ChannelStride uint32
	Channels      uint8

	// ServiceOffset is the sticky "a channel wants data" word and EventOffset
	// the interrupt reason word, which carries EventValue for such a request.
	ServiceOffset uint32
	EventOffset   uint32
	EventValue    uint16

	// ControlFlagOffset is the first of ControlFlags 16-bit words through
	// which the ARM hands over positional-audio control buffers. The DSP
	// clears a word when it has taken the buffer.
	ControlFlagOffset uint32
	ControlFlags      uint8

	// PrivateStateOffset is DSP-internal memory holding each channel's
	// format and play position, so a machine snapshot carries them.
	PrivateStateOffset uint32

	// InterruptGroupStatusOffset and InterruptGroupMask name the second-level
	// interrupt child the image pulses with a data request.
	InterruptGroupStatusOffset uint32
	InterruptGroupMask         uint32

	// InstructionsPerSecond converts retired instructions to stream time.
	InstructionsPerSecond uint64
	// OutputSampleRate is the rate of the mixed stereo output.
	OutputSampleRate uint32
}

const (
	qualcommAudioCommandStart   = 0x0320
	qualcommAudioCommandControl = 0x0321

	qualcommAudioStartImmediately = 0x8000
	qualcommAudioCodecIMAADPCM    = 0
	qualcommAudioCodecPCM         = 2

	qualcommAudioControlStart  = 0x01
	qualcommAudioControlFlush  = 0x02
	qualcommAudioControlPause  = 0x04
	qualcommAudioControlResume = 0x08
	qualcommAudioControlStop   = 0x10

	// Channel block fields.
	qualcommAudioChannelState   = 0x02
	qualcommAudioChannelSlots   = 0x0e // two slots of address hi, lo, length, ready
	qualcommAudioChannelSlot    = 0x08
	qualcommAudioChannelRequest = 0x22
	qualcommAudioChannelVolume  = 0x28
	qualcommAudioUnityVolume    = 0x2000

	qualcommAudioMaximumChannels  = 8
	qualcommAudioChannelStateSize = 20
	qualcommAudioPrivateSize      = qualcommAudioMaximumChannels * qualcommAudioChannelStateSize
	qualcommAudioMaximumBlock     = 4096
	qualcommAudioOutputChannels   = 2
	qualcommAudioRetainedSeconds  = 1
)

func (p QualcommAudioDSPProfile) validate(memory []MemoryRegionProfile) error {
	var region *MemoryRegionProfile
	for index := range memory {
		if memory[index].ID == p.MemoryID {
			region = &memory[index]
			break
		}
	}
	if region == nil || region.Kind != MemorySparseRAM && region.Kind != MemoryRAM {
		return fmt.Errorf("Qualcomm audio DSP memory region %q is not ordinary memory", p.MemoryID)
	}
	inside := func(offset, size uint32) bool {
		return offset%2 == 0 && uint64(offset)+uint64(size) <= uint64(region.Size)
	}
	if p.Channels == 0 || p.Channels > qualcommAudioMaximumChannels ||
		p.ChannelStride < qualcommAudioChannelVolume+2 || p.ChannelStride%2 != 0 ||
		!inside(p.CommandOffset, 2) || !inside(p.ArgumentOffset, 12) ||
		!inside(p.ChannelOffset, uint32(p.Channels)*p.ChannelStride) ||
		!inside(p.ServiceOffset, 2) || !inside(p.EventOffset, 2) ||
		!inside(p.ControlFlagOffset, uint32(p.ControlFlags)*2) ||
		!inside(p.PrivateStateOffset, qualcommAudioPrivateSize) ||
		p.InterruptGroupMask == 0 || p.InstructionsPerSecond == 0 ||
		p.OutputSampleRate < 8000 || p.OutputSampleRate > 48000 ||
		p.InstructionsPerSecond > math.MaxUint64/uint64(p.OutputSampleRate)/2 {
		return fmt.Errorf("invalid Qualcomm audio DSP profile for %q", p.MemoryID)
	}
	return nil
}

// qualcommAudioChannel is one wave channel. Everything except the decoded
// sample queue is mirrored in DSP-private memory.
type qualcommAudioChannel struct {
	active       bool
	paused       bool
	codec        uint16
	rate         uint16
	channels     uint16
	blockSamples uint16 // IMA: samples per block; PCM: sample-format code
	slot         uint16 // slot being played plus one, zero when none
	position     uint32 // bytes of that slot already taken
	partial      uint16 // bytes of an unfinished block carried from earlier buffers
	skip         uint32 // bytes to discard to regain block alignment

	carry  []byte   // the unfinished block itself
	fifo   []int16  // decoded stereo frames not yet mixed
	phase  uint64   // resampling phase, in units of 1/OutputSampleRate source frames
	last   [2]int16 // most recent frame, for interpolation across pulls
	next   [2]int16
	primed bool
	// draining marks a stopped channel that is still playing out the data it
	// had already been lent.
	draining bool
}

func (c *qualcommAudioChannel) blockBytes() int {
	channels := int(c.channels)
	if channels != 1 && channels != 2 {
		channels = 1
	}
	if c.codec == qualcommAudioCodecIMAADPCM {
		if c.blockSamples < 1 {
			return 4 * channels
		}
		size := ((int(c.blockSamples)-1)/2 + 4) * channels
		if size > qualcommAudioMaximumBlock {
			size = qualcommAudioMaximumBlock
		}
		return size
	}
	if c.codec == qualcommAudioCodecPCM && (c.blockSamples == 14 || c.blockSamples == 15) {
		return channels
	}
	return 2 * channels
}

// decode turns one whole block into stereo frames appended to the queue.
func (c *qualcommAudioChannel) decode(block []byte) {
	channels := int(c.channels)
	if channels != 1 && channels != 2 {
		channels = 1
	}
	var samples []int16
	switch {
	case c.codec == qualcommAudioCodecIMAADPCM:
		samples = decodeIMAADPCMBlock(block, channels)
	case c.codec == qualcommAudioCodecPCM:
		samples = make([]int16, channels)
		for channel := 0; channel < channels; channel++ {
			switch c.blockSamples {
			case 14:
				samples[channel] = int16(int8(block[channel])) << 8
			case 15:
				samples[channel] = int16(int(block[channel])-128) << 8
			case 12:
				samples[channel] = int16(binary.LittleEndian.Uint16(block[channel*2:]) ^ 0x8000)
			default:
				samples[channel] = int16(binary.LittleEndian.Uint16(block[channel*2:]))
			}
		}
	default:
		// An encoding the model cannot decode still plays for its length.
		samples = make([]int16, channels)
	}
	for index := 0; index+channels <= len(samples); index += channels {
		left := samples[index]
		right := left
		if channels == 2 {
			right = samples[index+1]
		}
		c.fifo = append(c.fifo, left, right)
	}
}

// QualcommAudioDSP models the wave-playback half of the audio image. It is a
// memory coprocessor for the commands and a clocked device for playback, and
// it queues the mixed PCM for the host.
type QualcommAudioDSP struct {
	profile   QualcommAudioDSPProfile
	base      uint32
	bus       *Bus
	interrupt QualcommGraphicsDSPInterrupt

	channels [qualcommAudioMaximumChannels]qualcommAudioChannel
	synced   bool
	running  bool

	frameRemainder uint64
	frames         uint64 // output frames since the generation began
	instructions   uint64
	epoch          uint64 // instruction count at which the generation began
	generation     uint64
	announce       bool

	mono        bool
	queued      []int16
	queuedStart uint64
	mix         []int32
}

func newQualcommAudioDSP(
	profile QualcommAudioDSPProfile,
	base uint32,
	bus *Bus,
	interrupt QualcommGraphicsDSPInterrupt,
) *QualcommAudioDSP {
	return &QualcommAudioDSP{profile: profile, base: base, bus: bus, interrupt: interrupt, generation: 1}
}

// busCoprocessorMemory is the unlocked counterpart of the view a coprocessor
// is handed inside a store; the clocked half of the model uses it.
type busCoprocessorMemory struct{ bus *Bus }

func (m busCoprocessorMemory) ReadMemory(address uint32, destination []byte) bool {
	return m.bus.ReadMemory(address, destination, cpu.PermissionRead) == nil
}

func (m busCoprocessorMemory) WriteMemory(address uint32, source []byte) bool {
	return m.bus.WriteMemory(address, source, cpu.PermissionWrite) == nil
}

func (d *QualcommAudioDSP) channelBase(channel int) uint32 {
	return d.profile.ChannelOffset + uint32(channel)*d.profile.ChannelStride
}

func (d *QualcommAudioDSP) privateBase(channel int) uint32 {
	return d.profile.PrivateStateOffset + uint32(channel)*qualcommAudioChannelStateSize
}

func (d *QualcommAudioDSP) store(dsp qualcommGraphicsDSPMemory, channel int) {
	state := &d.channels[channel]
	flags := uint16(0)
	if state.active && !state.draining {
		flags |= 1
	}
	if state.paused {
		flags |= 2
	}
	base := d.privateBase(channel)
	for index, value := range [...]uint16{
		flags, state.codec, state.rate, state.channels, state.blockSamples, state.slot,
		uint16(state.position), uint16(state.position >> 16), state.partial,
	} {
		dsp.write16(base+uint32(index)*2, value)
	}
}

// sync rebuilds the channel mirror from DSP-private memory after a reset or
// a restored snapshot. A block that was split across buffers cannot be
// rebuilt, so the rest of it is skipped to stay aligned.
func (d *QualcommAudioDSP) sync(dsp qualcommGraphicsDSPMemory) {
	d.synced = true
	d.running = false
	for channel := 0; channel < int(d.profile.Channels); channel++ {
		base := d.privateBase(channel)
		var words [9]uint16
		for index := range words {
			words[index] = dsp.read16(base + uint32(index)*2)
		}
		state := qualcommAudioChannel{
			active: words[0]&1 != 0, paused: words[0]&2 != 0,
			codec: words[1], rate: words[2], channels: words[3], blockSamples: words[4],
			slot: words[5], position: uint32(words[6]) | uint32(words[7])<<16,
		}
		if state.slot > 2 || state.rate == 0 {
			state = qualcommAudioChannel{}
		}
		if skip := int(words[8]); state.active && skip > 0 && skip < state.blockBytes() {
			state.skip = uint32(state.blockBytes() - skip)
		}
		d.channels[channel] = state
		d.running = d.running || state.active
	}
}

// Restart begins a new audio generation at the given instruction count. The
// machine calls it after a reset or a restored snapshot.
func (d *QualcommAudioDSP) Restart(instructions uint64) {
	d.synced = false
	d.frameRemainder = 0
	d.frames = 0
	d.instructions = instructions
	d.epoch = instructions
	d.generation++
	d.announce = true
	d.queued = d.queued[:0]
	d.queuedStart = 0
}

// MemoryWritten implements MemoryCoprocessor.
func (d *QualcommAudioDSP) MemoryWritten(memory CoprocessorMemory, offset uint32, width Width, value uint32) {
	if width != Width16 || value == 0 {
		return
	}
	if flags := d.profile.ControlFlagOffset; offset >= flags && offset < flags+uint32(d.profile.ControlFlags)*2 {
		// Positional-audio controls are accepted and ignored.
		qualcommGraphicsDSPMemory{memory: memory, base: d.base}.write16(offset, 0)
		return
	}
	if offset != d.profile.CommandOffset ||
		value != qualcommAudioCommandStart && value != qualcommAudioCommandControl {
		return
	}
	dsp := qualcommGraphicsDSPMemory{memory: memory, base: d.base}
	if !d.synced {
		d.sync(dsp)
	}
	var arguments [6]uint16
	for index := range arguments {
		arguments[index] = dsp.read16(d.profile.ArgumentOffset + uint32(index)*2)
	}
	if value == qualcommAudioCommandStart {
		channel := int(arguments[0] &^ qualcommAudioStartImmediately)
		if channel >= int(d.profile.Channels) || arguments[2] == 0 {
			return
		}
		d.channels[channel] = qualcommAudioChannel{
			codec: arguments[1], rate: arguments[2], channels: arguments[3], blockSamples: arguments[4],
		}
		if arguments[0]&qualcommAudioStartImmediately != 0 {
			d.open(dsp, channel)
		}
		d.store(dsp, channel)
		return
	}
	for channel := 0; channel < int(d.profile.Channels) && channel < 4; channel++ {
		if arguments[0]>>uint(channel)&1 == 0 {
			continue
		}
		state := &d.channels[channel]
		switch operation := arguments[1+channel]; {
		case operation&qualcommAudioControlStop != 0:
			// The driver stops a channel as soon as its last buffer has been
			// handed over, so the stop takes what is still lent and lets it
			// play out rather than cutting the end of every sound.
			if state.active && !state.paused {
				state.draining = true
				for d.refill(dsp, channel) {
				}
				state.carry = state.carry[:0]
			} else {
				*state = qualcommAudioChannel{}
			}
			for slot := uint32(0); slot < 2; slot++ {
				dsp.write16(d.channelBase(channel)+qualcommAudioChannelSlots+slot*qualcommAudioChannelSlot+6, 0)
			}
			dsp.write16(d.channelBase(channel)+qualcommAudioChannelState, 0)
			dsp.write16(d.channelBase(channel)+qualcommAudioChannelRequest, 0)
		case operation&qualcommAudioControlFlush != 0:
			d.flush(dsp, channel)
		case operation&qualcommAudioControlStart != 0:
			if state.rate != 0 && !state.active {
				d.open(dsp, channel)
			}
		case operation&qualcommAudioControlPause != 0:
			state.paused = true
		case operation&qualcommAudioControlResume != 0:
			state.paused = false
		}
		d.store(dsp, channel)
	}
}

func (d *QualcommAudioDSP) open(dsp qualcommGraphicsDSPMemory, channel int) {
	d.channels[channel].active = true
	d.running = true
	dsp.write16(d.channelBase(channel)+qualcommAudioChannelState, 1)
	d.request(dsp, channel)
}

// flush drops everything a channel holds and returns both lent buffers.
func (d *QualcommAudioDSP) flush(dsp qualcommGraphicsDSPMemory, channel int) {
	state := &d.channels[channel]
	state.slot, state.position, state.partial, state.skip = 0, 0, 0, 0
	state.carry, state.fifo = state.carry[:0], state.fifo[:0]
	state.phase, state.primed = 0, false
	for slot := uint32(0); slot < 2; slot++ {
		dsp.write16(d.channelBase(channel)+qualcommAudioChannelSlots+slot*qualcommAudioChannelSlot+6, 0)
	}
}

// request asks the ARM for more data on a channel.
func (d *QualcommAudioDSP) request(dsp qualcommGraphicsDSPMemory, channel int) {
	dsp.write16(d.channelBase(channel)+qualcommAudioChannelRequest, 1)
	dsp.write16(d.profile.ServiceOffset, 1)
	dsp.write16(d.profile.EventOffset, d.profile.EventValue)
	_ = d.interrupt.SetGroupedSource(
		d.profile.InterruptGroupStatusOffset, d.profile.InterruptGroupMask, true,
	)
}

// Advance implements ClockedDevice: it plays every open channel for the
// stream time the retired instructions represent.
func (d *QualcommAudioDSP) Advance(retiredInstructions uint64) error {
	d.instructions += retiredInstructions
	if retiredInstructions > math.MaxUint64/uint64(d.profile.OutputSampleRate)/2 {
		return fmt.Errorf("Qualcomm audio DSP time step is too large")
	}
	total := d.frameRemainder + retiredInstructions*uint64(d.profile.OutputSampleRate)
	frames := int(total / d.profile.InstructionsPerSecond)
	d.frameRemainder = total % d.profile.InstructionsPerSecond
	if frames == 0 {
		return nil
	}
	dsp := qualcommGraphicsDSPMemory{memory: busCoprocessorMemory{bus: d.bus}, base: d.base}
	if !d.synced {
		d.sync(dsp)
	}
	start := d.frames
	d.frames += uint64(frames)
	if !d.running {
		return nil
	}
	if cap(d.mix) < frames*2 {
		d.mix = make([]int32, frames*2)
	}
	mix := d.mix[:frames*2]
	clear(mix)
	audible, running := 0, false
	for channel := 0; channel < int(d.profile.Channels); channel++ {
		state := &d.channels[channel]
		if !state.active {
			continue
		}
		running = true
		if state.paused {
			continue
		}
		audible = max(audible, d.play(dsp, channel, mix))
	}
	d.running = running
	if audible != 0 {
		d.publish(mix[:audible*2], start)
	}
	return nil
}

// play mixes one channel into mix and reports how many frames it reached.
func (d *QualcommAudioDSP) play(dsp qualcommGraphicsDSPMemory, channel int, mix []int32) int {
	state := &d.channels[channel]
	volume := int32(dsp.read16(d.channelBase(channel) + qualcommAudioChannelVolume))
	if volume > 4*qualcommAudioUnityVolume {
		volume = 4 * qualcommAudioUnityVolume
	}
	output := uint64(d.profile.OutputSampleRate)
	audible, changed := 0, false
	for frame := 0; frame < len(mix)/2; frame++ {
		// The phase counts source frames in units of 1/output; each output
		// frame advances it by the source rate.
		if !state.primed {
			if !d.pull(dsp, channel, &changed) {
				break
			}
			state.last = state.next
			if !d.pull(dsp, channel, &changed) {
				state.next = state.last
			}
			state.primed = true
		}
		starved := false
		for state.phase >= output {
			state.phase -= output
			state.last = state.next
			if !d.pull(dsp, channel, &changed) {
				// The last frame still plays; after it the line is silent
				// until data returns.
				starved = true
				state.phase = 0
				break
			}
		}
		fraction := int32(state.phase * 256 / output)
		for side := 0; side < 2; side++ {
			sample := int32(state.last[side]) + (int32(state.next[side])-int32(state.last[side]))*fraction/256
			mix[frame*2+side] += sample * volume / qualcommAudioUnityVolume
		}
		audible = frame + 1
		if starved {
			state.primed = false
			break
		}
		state.phase += uint64(state.rate)
	}
	if changed {
		d.store(dsp, channel)
	}
	return audible
}

// pull moves the next source frame into state.next, decoding further blocks
// from the lent buffers as needed.
func (d *QualcommAudioDSP) pull(dsp qualcommGraphicsDSPMemory, channel int, changed *bool) bool {
	state := &d.channels[channel]
	for len(state.fifo) < 2 {
		if state.draining {
			state.active, state.draining = false, false
			return false
		}
		if !d.refill(dsp, channel) {
			return false
		}
		*changed = true
	}
	state.next = [2]int16{state.fifo[0], state.fifo[1]}
	state.fifo = state.fifo[2:]
	if len(state.fifo) == 0 {
		state.fifo = state.fifo[:0:0]
	}
	return true
}

// refill decodes one more block, taking bytes from the buffer in play and
// then from the other one. It releases a buffer once all of it is taken.
func (d *QualcommAudioDSP) refill(dsp qualcommGraphicsDSPMemory, channel int) bool {
	state := &d.channels[channel]
	base := d.channelBase(channel) + qualcommAudioChannelSlots
	size := state.blockBytes()
	for len(state.carry) < size {
		if state.slot == 0 {
			first := dsp.read16(base + 6)
			second := dsp.read16(base + qualcommAudioChannelSlot + 6)
			switch {
			case first == 0 && second == 0:
				if state.draining {
					return false
				}
				if dsp.read16(d.channelBase(channel)+qualcommAudioChannelRequest) == 0 {
					d.request(dsp, channel)
				}
				state.partial = uint16(len(state.carry))
				return false
			case second == 0 || first != 0 && second == first%3+1:
				state.slot = 1
			default:
				state.slot = 2
			}
			state.position = 0
		}
		slot := base + uint32(state.slot-1)*qualcommAudioChannelSlot
		address := (uint32(dsp.read16(slot))<<16 | uint32(dsp.read16(slot+2))) << 1
		length := uint32(dsp.read16(slot+4)) * 2
		if discard := min(state.skip, length-min(length, state.position)); discard != 0 {
			state.skip -= discard
			state.position += discard
		}
		if state.position < length {
			take := uint32(size - len(state.carry))
			if take > length-state.position {
				take = length - state.position
			}
			chunk := make([]byte, take)
			if !dsp.memory.ReadMemory(address+state.position, chunk) {
				clear(chunk)
			}
			state.carry = append(state.carry, chunk...)
			state.position += take
		}
		if state.position >= length {
			dsp.write16(slot+6, 0)
			state.slot, state.position = 0, 0
			if !state.draining {
				d.request(dsp, channel)
			}
		}
	}
	state.decode(state.carry)
	state.carry = state.carry[:0]
	state.partial = 0
	return true
}

func (d *QualcommAudioDSP) publish(mix []int32, start uint64) {
	if len(d.queued) != 0 && d.queuedStart+uint64(len(d.queued)/2) != start {
		// A silent gap: the host is told through the next chunk's cursor.
		d.queued = d.queued[:0]
	}
	if len(d.queued) == 0 {
		d.queuedStart = start
	}
	for _, sample := range mix {
		if sample > math.MaxInt16 {
			sample = math.MaxInt16
		} else if sample < math.MinInt16 {
			sample = math.MinInt16
		}
		d.queued = append(d.queued, int16(sample))
	}
	if limit := int(d.profile.OutputSampleRate) * 2 * qualcommAudioRetainedSeconds; len(d.queued) > limit {
		drop := (len(d.queued) - limit) &^ 1
		d.queued = append(d.queued[:0], d.queued[drop:]...)
		d.queuedStart += uint64(drop / 2)
	}
}

// SetOutputChannels selects a mono or stereo host stream; the mix itself is
// always stereo.
func (d *QualcommAudioDSP) SetOutputChannels(channels uint8) {
	d.mono = channels == 1
}

// DrainAudio hands the host the PCM mixed since the last call.
func (d *QualcommAudioDSP) DrainAudio() core.AudioChunk {
	chunk := d.drain()
	if !d.mono || chunk.Channels != qualcommAudioOutputChannels {
		return chunk
	}
	chunk.Channels = 1
	for frame := 0; frame < len(chunk.PCM16)/2; frame++ {
		chunk.PCM16[frame] = int16((int32(chunk.PCM16[frame*2]) + int32(chunk.PCM16[frame*2+1])) / 2)
	}
	chunk.PCM16 = chunk.PCM16[:len(chunk.PCM16)/2]
	return chunk
}

func (d *QualcommAudioDSP) drain() core.AudioChunk {
	rate := uint64(d.profile.OutputSampleRate)
	nanoseconds := func(instructions uint64) int64 {
		whole := instructions / d.profile.InstructionsPerSecond
		part := instructions % d.profile.InstructionsPerSecond
		return int64(whole*1_000_000_000 + part*1_000_000_000/d.profile.InstructionsPerSecond)
	}
	if len(d.queued) == 0 {
		if !d.announce {
			return core.AudioChunk{}
		}
		d.announce = false
		return core.AudioChunk{
			SampleRate: int(rate), Channels: qualcommAudioOutputChannels,
			StartGuestNS: nanoseconds(d.epoch), Generation: d.generation,
		}
	}
	d.announce = false
	chunk := core.AudioChunk{
		SampleRate: int(rate), Channels: qualcommAudioOutputChannels,
		PCM16:        append([]int16(nil), d.queued...),
		StartGuestNS: nanoseconds(d.epoch) + int64(d.queuedStart/rate*1_000_000_000+d.queuedStart%rate*1_000_000_000/rate),
		StartSample:  d.queuedStart,
		Generation:   d.generation,
	}
	d.queuedStart += uint64(len(d.queued) / 2)
	d.queued = d.queued[:0]
	return chunk
}

// qualcommDSPModules fans one region's stores out to every module of the
// DSP image that shares it.
type qualcommDSPModules []MemoryCoprocessor

func (m qualcommDSPModules) MemoryWritten(memory CoprocessorMemory, offset uint32, width Width, value uint32) {
	for _, module := range m {
		module.MemoryWritten(memory, offset, width, value)
	}
}

// AttachDSPModules connects every profiled module of the application-DSP
// image to the address space they share. It returns the audio module, which
// the machine clocks and drains, or nil when the board has none.
func (p BoardProfile) AttachDSPModules(bus *Bus, interrupt QualcommGraphicsDSPInterrupt) (*QualcommAudioDSP, error) {
	if p.AudioDSP == nil {
		return nil, p.AttachGraphicsDSP(bus, interrupt)
	}
	if bus == nil || interrupt == nil {
		return nil, fmt.Errorf("attach board profile %q audio DSP: missing bus or interrupt controller", p.ID)
	}
	for _, region := range p.Memory {
		if region.ID != p.AudioDSP.MemoryID {
			continue
		}
		audio := newQualcommAudioDSP(*p.AudioDSP, region.Address, bus, interrupt)
		modules := qualcommDSPModules{audio}
		if p.GraphicsDSP != nil {
			if p.GraphicsDSP.MemoryID != region.ID {
				return nil, fmt.Errorf("attach board profile %q DSP modules: modules use different regions", p.ID)
			}
			modules = append(modules, newQualcommGraphicsDSP(*p.GraphicsDSP, region.Address, interrupt))
		}
		if err := bus.AttachMemoryCoprocessor(region.ID, modules); err != nil {
			return nil, fmt.Errorf("attach board profile %q DSP modules: %w", p.ID, err)
		}
		return audio, nil
	}
	return nil, fmt.Errorf("attach board profile %q audio DSP: no memory region %q", p.ID, p.AudioDSP.MemoryID)
}
