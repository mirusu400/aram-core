package runtime

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type MediaLimits struct {
	MaxClips         uint32
	MaxSourceBytes   uint64
	MaxQueuedSamples uint32
	OutputSampleRate uint32
	OutputChannels   uint8
}

func DefaultMediaLimits() MediaLimits {
	return MediaLimits{
		MaxClips:         256,
		MaxSourceBytes:   64 << 20,
		MaxQueuedSamples: 2_000_000,
		OutputSampleRate: 44_100,
		OutputChannels:   2,
	}
}

func (l MediaLimits) Validate() error {
	if l.MaxClips == 0 || l.MaxSourceBytes == 0 ||
		l.MaxQueuedSamples == 0 || l.OutputSampleRate < 8_000 ||
		l.OutputSampleRate > 192_000 ||
		(l.OutputChannels != 1 && l.OutputChannels != 2) {
		return fmt.Errorf("%w: invalid media limits", ErrInvalidArgument)
	}
	return nil
}

type ClipPlaybackState uint8

const (
	ClipStopped ClipPlaybackState = iota
	ClipPlaying
	ClipPaused
	ClipRecording
)

func (s ClipPlaybackState) Valid() bool {
	return s <= ClipRecording
}

type ClipInfo struct {
	ID             ServiceID
	Owner          OwnerID
	MediaType      string
	Capacity       uint64
	Position       time.Duration
	Duration       time.Duration
	State          ClipPlaybackState
	Volume         uint8
	Muted          bool
	Pan            int8
	RemainingPlays int32
	Decoded        bool
}

type ClipState struct {
	ID             ServiceID
	Owner          OwnerID
	MediaType      string
	Capacity       uint64
	Source         []byte
	PositionNS     int64
	State          ClipPlaybackState
	Volume         uint8
	Muted          bool
	Pan            int8
	RemainingPlays int32
}

type MediaState struct {
	Limits          MediaLimits
	GlobalVolume    uint8
	GlobalMute      bool
	OutputRemainder uint64
	QueuedPCM16     []int16
	Clips           []ClipState
}

type AudioBuffer struct {
	SampleRate int
	Channels   int
	PCM16      []int16
}

type decodedPCM struct {
	sampleRate uint32
	channels   uint8
	samples    []int16
	duration   time.Duration
	smaf       *smafRenderStream
}

type mediaClip struct {
	id             ServiceID
	owner          OwnerID
	mediaType      string
	capacity       uint64
	source         []byte
	decoded        *decodedPCM
	position       time.Duration
	state          ClipPlaybackState
	volume         uint8
	muted          bool
	pan            int8
	remainingPlays int32
}

// Media is a deterministic, headless clip timeline and bounded PCM16 mixer.
// It never opens a host audio device.
type Media struct {
	registry        *Registry
	limits          MediaLimits
	clips           map[ServiceID]*mediaClip
	globalVolume    uint8
	globalMute      bool
	outputRemainder uint64
	queuedPCM16     []int16
}

func NewMedia(registry *Registry, limits MediaLimits) (*Media, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: media registry is nil", ErrInvalidArgument)
	}
	if limits == (MediaLimits{}) {
		limits = DefaultMediaLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Media{
		registry:     registry,
		limits:       limits,
		clips:        make(map[ServiceID]*mediaClip),
		globalVolume: 100,
	}, nil
}

func (m *Media) CreateClip(
	owner OwnerID,
	mediaType string,
	capacity uint64,
) (ServiceID, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if len(mediaType) > 127 || strings.IndexByte(mediaType, 0) >= 0 ||
		capacity > m.limits.MaxSourceBytes {
		return 0, fmt.Errorf("%w: invalid media clip definition", ErrInvalidArgument)
	}
	if uint32(len(m.clips)) >= m.limits.MaxClips {
		return 0, fmt.Errorf("%w: media clip count reached %d", ErrLimitExceeded, m.limits.MaxClips)
	}
	id, err := m.registry.Create(owner, KindClip)
	if err != nil {
		return 0, err
	}
	m.clips[id] = &mediaClip{
		id:        id,
		owner:     owner,
		mediaType: mediaType,
		capacity:  capacity,
		volume:    100,
	}
	return id, nil
}

func (m *Media) DestroyClip(owner OwnerID, id ServiceID, bus *EventBus) error {
	if _, err := m.get(owner, id); err != nil {
		return err
	}
	if err := m.registry.Destroy(id, owner, KindClip); err != nil {
		return err
	}
	delete(m.clips, id)
	if bus != nil {
		bus.RemoveService(id)
	}
	return nil
}

func (m *Media) Append(owner OwnerID, id ServiceID, data []byte) (int, error) {
	clip, err := m.get(owner, id)
	if err != nil {
		return 0, err
	}
	size := uint64(len(clip.source)) + uint64(len(data))
	limit := m.limits.MaxSourceBytes
	if clip.capacity != 0 {
		limit = min(limit, clip.capacity)
	}
	if size < uint64(len(clip.source)) || size > limit {
		return 0, fmt.Errorf("%w: media source exceeds %d bytes", ErrLimitExceeded, limit)
	}
	clip.source = append(clip.source, data...)
	clip.decoded = decodeWavePCM16(clip.source)
	if clip.decoded != nil && clip.position > clip.decoded.duration {
		clip.position = clip.decoded.duration
	}
	return len(data), nil
}

func (m *Media) Source(owner OwnerID, id ServiceID) ([]byte, error) {
	clip, err := m.get(owner, id)
	if err != nil {
		return nil, err
	}
	return cloneBytes(clip.source), nil
}

func (m *Media) Clear(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	clip.source = nil
	clip.decoded = nil
	clip.position = 0
	clip.state = ClipStopped
	clip.remainingPlays = 0
	return nil
}

func (m *Media) Play(owner OwnerID, id ServiceID, plays int32) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if plays == 0 || plays < -1 {
		return fmt.Errorf("%w: invalid media play count %d", ErrInvalidArgument, plays)
	}
	if clip.decoded == nil && looksLikeSMAF(clip.source) {
		clip.decoded = decodeSMAFLazyPCM16(
			clip.source,
			m.limits.OutputSampleRate,
		)
	}
	if clip.decoded != nil && clip.position >= clip.decoded.duration {
		clip.position = 0
	}
	clip.remainingPlays = plays
	clip.state = ClipPlaying
	return nil
}

func (m *Media) Pause(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state == ClipPlaying {
		clip.state = ClipPaused
	}
	return nil
}

func (m *Media) Resume(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state == ClipPaused {
		clip.state = ClipPlaying
	}
	return nil
}

func (m *Media) Stop(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	clip.state = ClipStopped
	clip.remainingPlays = 0
	return nil
}

func (m *Media) Seek(owner OwnerID, id ServiceID, position time.Duration) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if position < 0 || (clip.decoded != nil && position > clip.decoded.duration) {
		return fmt.Errorf("%w: invalid clip position %s", ErrInvalidArgument, position)
	}
	clip.position = position
	return nil
}

func (m *Media) SetClipGain(
	owner OwnerID,
	id ServiceID,
	volume uint8,
	muted bool,
	pan int8,
) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if volume > 100 || pan < -100 || pan > 100 {
		return fmt.Errorf("%w: invalid clip gain", ErrInvalidArgument)
	}
	clip.volume, clip.muted, clip.pan = volume, muted, pan
	return nil
}

func (m *Media) SetGlobalGain(volume uint8, muted bool) error {
	if volume > 100 {
		return fmt.Errorf("%w: invalid global volume %d", ErrInvalidArgument, volume)
	}
	m.globalVolume, m.globalMute = volume, muted
	return nil
}

func (m *Media) Info(owner OwnerID, id ServiceID) (ClipInfo, error) {
	clip, err := m.get(owner, id)
	if err != nil {
		return ClipInfo{}, err
	}
	info := ClipInfo{
		ID:             clip.id,
		Owner:          clip.owner,
		MediaType:      clip.mediaType,
		Capacity:       clip.capacity,
		Position:       clip.position,
		State:          clip.state,
		Volume:         clip.volume,
		Muted:          clip.muted,
		Pan:            clip.pan,
		RemainingPlays: clip.remainingPlays,
		Decoded:        clip.decoded != nil,
	}
	if clip.decoded != nil {
		info.Duration = clip.decoded.duration
	}
	return info, nil
}

func (m *Media) Advance(start, end time.Duration, bus *EventBus) error {
	if start < 0 || end < start || bus == nil {
		return fmt.Errorf("%w: invalid media advance", ErrInvalidArgument)
	}
	mediaBefore := m.Snapshot()
	busBefore := bus.Snapshot()
	rollback := func(err error) error {
		_ = m.Restore(mediaBefore)
		_ = bus.Restore(busBefore)
		return err
	}
	delta := end - start
	ids := m.sortedClipIDs()
	activeDecoded := false
	audible := false
	for _, id := range ids {
		clip := m.clips[id]
		if clip.state == ClipPlaying && clip.decoded != nil &&
			clip.decoded.duration > 0 {
			activeDecoded = true
			if !clip.muted && !m.globalMute &&
				clip.volume != 0 && m.globalVolume != 0 {
				audible = true
			}
		}
	}
	if !activeDecoded {
		m.outputRemainder = 0
	}
	var frameCount, remainder uint64
	if activeDecoded {
		rate := uint64(m.limits.OutputSampleRate)
		if uint64(delta) > (math.MaxUint64-m.outputRemainder)/rate {
			return fmt.Errorf("%w: media output frame count overflow", ErrLimitExceeded)
		}
		numerator := m.outputRemainder + uint64(delta)*rate
		frameCount = numerator / uint64(time.Second)
		remainder = numerator % uint64(time.Second)
	}
	if frameCount > uint64(math.MaxInt) {
		return fmt.Errorf("%w: media advance exceeds host limits", ErrLimitExceeded)
	}
	sampleCount := uint64(0)
	if audible {
		sampleCount = frameCount * uint64(m.limits.OutputChannels)
	}
	if sampleCount > uint64(m.limits.MaxQueuedSamples)-uint64(len(m.queuedPCM16)) {
		return fmt.Errorf("%w: queued audio exceeds %d samples", ErrLimitExceeded, m.limits.MaxQueuedSamples)
	}

	mixed := make([]int64, int(sampleCount))
	for _, id := range ids {
		clip := m.clips[id]
		if clip.state != ClipPlaying || clip.decoded == nil ||
			clip.decoded.duration <= 0 || clip.muted || m.globalMute ||
			clip.volume == 0 || m.globalVolume == 0 {
			continue
		}
		for frame := uint64(0); frame < frameCount; frame++ {
			offset := durationForFrame(frame, m.limits.OutputSampleRate)
			left, right, ok := m.clipSampleAt(clip, offset)
			if !ok {
				continue
			}
			left, right = applyGain(
				left,
				right,
				uint32(clip.volume)*uint32(m.globalVolume),
				clip.pan,
			)
			if m.limits.OutputChannels == 1 {
				mixed[frame] += int64(left)/2 + int64(right)/2
			} else {
				mixed[frame*2] += int64(left)
				mixed[frame*2+1] += int64(right)
			}
		}
	}
	for _, sample := range mixed {
		m.queuedPCM16 = append(m.queuedPCM16, clampInt16(sample))
	}
	if activeDecoded {
		m.outputRemainder = remainder
	}

	for _, id := range ids {
		clip := m.clips[id]
		if clip.state != ClipPlaying {
			continue
		}
		if clip.decoded == nil || clip.decoded.duration <= 0 {
			if delta > time.Duration(math.MaxInt64-int64(clip.position)) {
				return rollback(fmt.Errorf("%w: media position overflow", ErrLimitExceeded))
			}
			clip.position += delta
			continue
		}
		if err := advanceClipTimeline(clip, delta, start, bus); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (m *Media) Drain() AudioBuffer {
	result := AudioBuffer{
		SampleRate: int(m.limits.OutputSampleRate),
		Channels:   int(m.limits.OutputChannels),
		PCM16:      append([]int16(nil), m.queuedPCM16...),
	}
	m.queuedPCM16 = nil
	return result
}

func (m *Media) Snapshot() MediaState {
	state := MediaState{
		Limits:          m.limits,
		GlobalVolume:    m.globalVolume,
		GlobalMute:      m.globalMute,
		OutputRemainder: m.outputRemainder,
		QueuedPCM16:     append([]int16(nil), m.queuedPCM16...),
	}
	for _, id := range m.sortedClipIDs() {
		clip := m.clips[id]
		state.Clips = append(state.Clips, ClipState{
			ID:             clip.id,
			Owner:          clip.owner,
			MediaType:      clip.mediaType,
			Capacity:       clip.capacity,
			Source:         cloneBytes(clip.source),
			PositionNS:     int64(clip.position),
			State:          clip.state,
			Volume:         clip.volume,
			Muted:          clip.muted,
			Pan:            clip.pan,
			RemainingPlays: clip.remainingPlays,
		})
	}
	return state
}

func (m *Media) Restore(state MediaState) error {
	if err := state.Limits.Validate(); err != nil ||
		state.GlobalVolume > 100 ||
		state.OutputRemainder >= uint64(time.Second) ||
		len(state.QueuedPCM16) > int(state.Limits.MaxQueuedSamples) ||
		len(state.Clips) > int(state.Limits.MaxClips) {
		return fmt.Errorf("%w: invalid media state limits", ErrInvalidState)
	}
	clips := make(map[ServiceID]*mediaClip, len(state.Clips))
	var previous ServiceID
	for index, saved := range state.Clips {
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			len(saved.MediaType) > 127 ||
			strings.IndexByte(saved.MediaType, 0) >= 0 ||
			saved.MediaType != strings.ToLower(strings.TrimSpace(saved.MediaType)) ||
			saved.Capacity > state.Limits.MaxSourceBytes ||
			uint64(len(saved.Source)) > state.Limits.MaxSourceBytes ||
			(saved.Capacity != 0 && uint64(len(saved.Source)) > saved.Capacity) ||
			saved.PositionNS < 0 || !saved.State.Valid() ||
			saved.Volume > 100 || saved.Pan < -100 || saved.Pan > 100 ||
			saved.RemainingPlays < -1 ||
			m.registry.Validate(saved.ID, saved.Owner, KindClip) != nil {
			return fmt.Errorf("%w: invalid media clip %d", ErrInvalidState, index)
		}
		if (saved.State == ClipStopped || saved.State == ClipRecording) &&
			saved.RemainingPlays != 0 ||
			(saved.State == ClipPlaying || saved.State == ClipPaused) &&
				saved.RemainingPlays != -1 && saved.RemainingPlays <= 0 {
			return fmt.Errorf("%w: invalid media clip %d playback count", ErrInvalidState, index)
		}
		clip := &mediaClip{
			id:             saved.ID,
			owner:          saved.Owner,
			mediaType:      saved.MediaType,
			capacity:       saved.Capacity,
			source:         cloneBytes(saved.Source),
			position:       time.Duration(saved.PositionNS),
			state:          saved.State,
			volume:         saved.Volume,
			muted:          saved.Muted,
			pan:            saved.Pan,
			remainingPlays: saved.RemainingPlays,
		}
		clip.decoded = decodeWavePCM16(clip.source)
		if clip.decoded == nil &&
			(saved.State == ClipPlaying || saved.State == ClipPaused) &&
			looksLikeSMAF(clip.source) {
			clip.decoded = decodeSMAFLazyPCM16(
				clip.source,
				state.Limits.OutputSampleRate,
			)
		}
		if clip.decoded != nil && clip.position > clip.decoded.duration {
			return fmt.Errorf("%w: media clip %d position exceeds duration", ErrInvalidState, index)
		}
		clips[saved.ID] = clip
		previous = saved.ID
	}
	m.limits = state.Limits
	m.clips = clips
	m.globalVolume = state.GlobalVolume
	m.globalMute = state.GlobalMute
	m.outputRemainder = state.OutputRemainder
	m.queuedPCM16 = append([]int16(nil), state.QueuedPCM16...)
	return nil
}

func (m *Media) get(owner OwnerID, id ServiceID) (*mediaClip, error) {
	if err := m.registry.Validate(id, owner, KindClip); err != nil {
		return nil, err
	}
	clip := m.clips[id]
	if clip == nil {
		return nil, fmt.Errorf("%w: media clip %s", ErrInvalidState, id)
	}
	return clip, nil
}

func (m *Media) sortedClipIDs() []ServiceID {
	ids := make([]ServiceID, 0, len(m.clips))
	for id := range m.clips {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func durationForFrame(frame uint64, rate uint32) time.Duration {
	return time.Duration(frame * uint64(time.Second) / uint64(rate))
}

func (m *Media) clipSampleAt(clip *mediaClip, offset time.Duration) (int16, int16, bool) {
	position := clip.position + offset
	duration := clip.decoded.duration
	if duration <= 0 {
		return 0, 0, false
	}
	if position >= duration {
		completions := int64(position / duration)
		if clip.remainingPlays != -1 && completions >= int64(clip.remainingPlays) {
			return 0, 0, false
		}
		position %= duration
	}
	frame := uint64(position) * uint64(clip.decoded.sampleRate) / uint64(time.Second)
	clip.decoded.ensureFrame(frame)
	availableFrames := uint64(len(clip.decoded.samples)) / uint64(clip.decoded.channels)
	if frame >= availableFrames {
		return 0, 0, false
	}
	if clip.decoded.channels == 1 {
		value := clip.decoded.samples[frame]
		return value, value, true
	}
	return clip.decoded.samples[frame*2], clip.decoded.samples[frame*2+1], true
}

func (decoded *decodedPCM) ensureFrame(frame uint64) {
	if decoded == nil || decoded.smaf == nil ||
		frame < uint64(len(decoded.samples))/uint64(decoded.channels) {
		return
	}
	target := frame + 1
	current := uint64(len(decoded.samples)) / uint64(decoded.channels)
	if target < current+2_048 {
		target = current + 2_048
	}
	decoded.samples = decoded.smaf.renderUntil(decoded.samples, target)
}

func applyGain(left, right int16, gain uint32, pan int8) (int16, int16) {
	leftGain, rightGain := int64(gain), int64(gain)
	if pan > 0 {
		leftGain = leftGain * int64(100-pan) / 100
	} else if pan < 0 {
		rightGain = rightGain * int64(100+pan) / 100
	}
	return clampInt16(int64(left) * leftGain / 10_000),
		clampInt16(int64(right) * rightGain / 10_000)
}

func clampInt16(value int64) int16 {
	if value > math.MaxInt16 {
		return math.MaxInt16
	}
	if value < math.MinInt16 {
		return math.MinInt16
	}
	return int16(value)
}

func advanceClipTimeline(
	clip *mediaClip,
	delta time.Duration,
	start time.Duration,
	bus *EventBus,
) error {
	duration := clip.decoded.duration
	if delta > time.Duration(math.MaxInt64-int64(clip.position)) {
		return fmt.Errorf("%w: media position overflow", ErrLimitExceeded)
	}
	total := clip.position + delta
	completions := int64(total / duration)
	if completions == 0 {
		clip.position = total
		return nil
	}
	if clip.remainingPlays == -1 {
		clip.position = total % duration
		return nil
	}
	if completions < int64(clip.remainingPlays) {
		clip.remainingPlays -= int32(completions)
		clip.position = total % duration
		return nil
	}
	untilComplete := duration - clip.position
	if clip.remainingPlays > 1 {
		repeats := int64(clip.remainingPlays - 1)
		if repeats > math.MaxInt64/int64(duration) {
			return fmt.Errorf("%w: media completion time overflow", ErrLimitExceeded)
		}
		untilComplete += time.Duration(repeats) * duration
	}
	clip.position = duration
	clip.state = ClipStopped
	clip.remainingPlays = 0
	_, err := bus.Enqueue(Event{
		At:        start + untilComplete,
		Kind:      EventAudioComplete,
		Owner:     clip.owner,
		ServiceID: clip.id,
		Name:      "complete",
	})
	if err != nil {
		return err
	}
	return nil
}

func decodeWavePCM16(data []byte) *decodedPCM {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil
	}
	var (
		format     uint16
		channels   uint16
		sampleRate uint32
		bits       uint16
		pcm        []byte
	)
	for offset := uint64(12); offset+8 <= uint64(len(data)); {
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if start > uint64(len(data)) || size > uint64(len(data))-start {
			return nil
		}
		chunk := data[start : start+size]
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if len(chunk) < 16 {
				return nil
			}
			format = binary.LittleEndian.Uint16(chunk[0:2])
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bits = binary.LittleEndian.Uint16(chunk[14:16])
		case "data":
			pcm = chunk
		}
		next := start + size + size%2
		if next <= offset {
			return nil
		}
		offset = next
	}
	if format != 1 || (channels != 1 && channels != 2) ||
		sampleRate < 8_000 || sampleRate > 192_000 ||
		(bits != 8 && bits != 16) || len(pcm) == 0 {
		return nil
	}
	bytesPerSample := int(bits / 8)
	frameBytes := int(channels) * bytesPerSample
	if len(pcm)%frameBytes != 0 {
		return nil
	}
	samples := make([]int16, len(pcm)/bytesPerSample)
	for index := range samples {
		if bits == 8 {
			samples[index] = int16(int32(pcm[index])-128) << 8
		} else {
			samples[index] = int16(binary.LittleEndian.Uint16(pcm[index*2:]))
		}
	}
	frames := len(samples) / int(channels)
	duration := time.Duration(uint64(frames) * uint64(time.Second) / uint64(sampleRate))
	if duration <= 0 {
		return nil
	}
	return &decodedPCM{
		sampleRate: sampleRate,
		channels:   uint8(channels),
		samples:    samples,
		duration:   duration,
	}
}
