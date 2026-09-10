package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"slices"
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
		OutputChannels:   1,
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
	WaitingForData bool
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
	AudioMixMode    bool
	BGMVoice        *BGMVoiceState
	BGMVoiceSig     uint64

	// Legacy detached-voice fields remain in schema v3 so existing save states
	// can be decoded and validated. Restore retires them instead of reviving an
	// unowned audible voice.
	BGMEndedSig       uint64
	BGMEndedElapsedNS int64
	BGMEndedValid     bool
}

// BGMVoiceState is the legacy schema-v3 detached music payload. New snapshots
// never populate it.
type BGMVoiceState struct {
	MediaType  string
	Source     []byte
	PositionNS int64
	Volume     uint8
	Pan        int8
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

type smafDecodeCacheKey struct {
	digest     [sha256.Size]byte
	sampleRate uint32
}

type smafDecodeCacheEntry struct {
	decoded *decodedPCM
}

const maxSMAFDecodeCacheEntries = 64

// maxSMAFDecodeCacheSamples bounds the PCM the cache may retain. An entry count
// is not a bound on its own: a lazy score renders on demand up to
// maxSMAFSeconds of stereo int16, so 64 fully played tracks would hold
// gigabytes resident on a handset. Retention is re-checked on every insert
// because an entry keeps growing after it is cached.
const maxSMAFDecodeCacheSamples = 16 << 20

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
	waitingForData bool
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
	dropped         uint64
	outputRevision  uint64

	// mixMode enables simultaneous playback of registered clips. All sounding
	// voices remain owned by those clips.
	mixMode bool

	voiceIDs      []ServiceID
	voiceScratch  []*mediaClip
	mixScratch    []int64
	smafCache     map[smafDecodeCacheKey]smafDecodeCacheEntry
	smafCacheFIFO []smafDecodeCacheKey
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

// SetAudioMixMode records the audio playback policy. The two policies no longer
// differ: the detached music voice the mixing policy used to create violated
// clip lifetime and stop semantics and has been removed, and concurrent clips
// mix in both settings. The flag is kept so existing settings and save states
// still load, and because a title-specific compatibility quirk is the right
// home for any future "ignore the guest's stop" behaviour.
func (m *Media) SetAudioMixMode(on bool) {
	if m.mixMode == on {
		return
	}
	m.mixMode = on
}

// AudioMixMode reports the compatibility policy selection. Both policies keep
// voices attached to registered clips.
func (m *Media) AudioMixMode() bool { return m.mixMode }

// MusicVoiceActive is retained for debug/API compatibility. Detached music
// voices are no longer created, so it always reports false for new state.
func (m *Media) MusicVoiceActive() bool { return false }

// playbackVoices lists every sounding registered source in deterministic order.
func (m *Media) playbackVoices() []*mediaClip {
	m.voiceIDs = m.voiceIDs[:0]
	for id := range m.clips {
		m.voiceIDs = append(m.voiceIDs, id)
	}
	slices.Sort(m.voiceIDs)
	m.voiceScratch = m.voiceScratch[:0]
	for _, id := range m.voiceIDs {
		m.voiceScratch = append(m.voiceScratch, m.clips[id])
	}
	return m.voiceScratch
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
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state != ClipStopped {
		return fmt.Errorf("%w: destroy media clip while %v", ErrInvalidState, clip.state)
	}
	if err := m.registry.Destroy(id, owner, KindClip); err != nil {
		return err
	}
	delete(m.clips, id)
	m.invalidateOutput()
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
	hadDecoded := clip.decoded != nil
	clip.source = append(clip.source, data...)
	decoded := decodeWavePCM16(clip.source)
	if decoded == nil && (hadDecoded || clip.waitingForData) &&
		looksLikeSequencedScore(clip.source) {
		decoded = m.decodeScore(clip.source)
	}
	clip.decoded = decoded
	if decoded != nil {
		clip.waitingForData = false
	} else if clip.state == ClipPlaying {
		// Streaming titles reuse one handle and push the next track behind the
		// sound still playing, so an appended buffer routinely leaves the clip
		// undecodable part-way through. That is a stall waiting for the rest of
		// the data, not a fault: advanceLocked treats a playing clip with no
		// decoder and no wait flag as a broken invariant and fails the whole
		// tick, which the machines turn into StateFaulted. The prefix test is
		// deliberately not applied here - a WAV clip that receives SMAF bytes
		// keeps a "RIFF" prefix and would otherwise kill the title.
		clip.waitingForData = true
	}
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

// ReplaceSource atomically installs a stopped clip's encoded buffer.
func (m *Media) ReplaceSource(owner OwnerID, id ServiceID, data []byte) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state != ClipStopped {
		return fmt.Errorf("%w: replace media buffer while %v", ErrInvalidState, clip.state)
	}
	limit := m.limits.MaxSourceBytes
	if clip.capacity != 0 {
		limit = min(limit, clip.capacity)
	}
	if uint64(len(data)) > limit {
		return fmt.Errorf("%w: media source exceeds %d bytes", ErrLimitExceeded, limit)
	}
	clip.source = cloneBytes(data)
	clip.decoded = decodeWavePCM16(clip.source)
	clip.position = 0
	clip.remainingPlays = 0
	clip.waitingForData = false
	m.invalidateOutput()
	return nil
}

// AvailableBytes reports the consumable encoded bytes remaining at the
// playback cursor. The encoded source is retained for deterministic decode and
// replay, while this view supplies WIPI's decreasing buffer watermark.
func (m *Media) AvailableBytes(owner OwnerID, id ServiceID) (uint64, error) {
	clip, err := m.get(owner, id)
	if err != nil {
		return 0, err
	}
	return uint64(len(m.bufferedSource(clip))), nil
}

func (m *Media) bufferedSource(clip *mediaClip) []byte {
	if clip == nil || len(clip.source) == 0 || clip.decoded == nil ||
		clip.decoded.duration <= 0 || clip.position <= 0 {
		if clip == nil {
			return nil
		}
		return clip.source
	}
	if clip.position >= clip.decoded.duration {
		return clip.source[len(clip.source):]
	}
	// A 64 MB source (MaxSourceBytes) longer than about 275 seconds overflows
	// a plain uint64 product, so the watermark is computed in 128 bits. The
	// quotient always fits: position is below duration, so it stays under
	// len(source).
	high, low := bits.Mul64(uint64(len(clip.source)), uint64(clip.position))
	consumed, _ := bits.Div64(high, low, uint64(clip.decoded.duration))
	return clip.source[min(consumed, uint64(len(clip.source))):]
}

// TakeBuffered removes and returns bytes from the current consumable buffer.
// Reading encoded data while playback owns the cursor is invalid.
func (m *Media) TakeBuffered(owner OwnerID, id ServiceID, count uint64) ([]byte, error) {
	clip, err := m.get(owner, id)
	if err != nil {
		return nil, err
	}
	if clip.state != ClipStopped {
		return nil, fmt.Errorf("%w: read media buffer while %v", ErrInvalidState, clip.state)
	}
	available := m.bufferedSource(clip)
	count = min(count, uint64(len(available)))
	if count == 0 {
		// A zero-length read is the size probe titles make before allocating
		// their own buffer. It must not consume the clip: rewriting the source
		// to the tail here would strip a WAV's header and leave the clip
		// undecodable for its next Play.
		return nil, nil
	}
	result := cloneBytes(available[:count])
	clip.source = cloneBytes(available[count:])
	clip.position = 0
	clip.decoded = decodeWavePCM16(clip.source)
	clip.waitingForData = false
	m.invalidateOutput()
	return result, nil
}

func (m *Media) Clear(owner OwnerID, id ServiceID) error {
	return m.ReplaceSource(owner, id, nil)
}

// ResetForReconcile returns a clip to the empty stopped state whatever it is
// doing now. It exists for the adapters' save/restore reconcilers, which
// rebuild each shared clip from their own mirror and therefore own the state
// they are overwriting. Guest-facing clears must keep using Clear, which
// enforces WIPI's precondition that a sounding clip cannot be cleared.
func (m *Media) ResetForReconcile(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	clip.source = nil
	clip.decoded = nil
	clip.position = 0
	clip.state = ClipStopped
	clip.remainingPlays = 0
	clip.waitingForData = false
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
	if clip.state != ClipStopped {
		return fmt.Errorf("%w: play media clip while %v", ErrInvalidState, clip.state)
	}
	if clip.decoded == nil && looksLikeSequencedScore(clip.source) {
		clip.decoded = m.decodeScore(clip.source)
	}
	if clip.decoded == nil && looksLikeSequencedPrefix(clip.source) {
		clip.waitingForData = true
	} else if clip.decoded == nil || clip.decoded.duration <= 0 {
		return fmt.Errorf("%w: %q clip cannot be decoded", ErrMediaUnsupported, clip.mediaType)
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
	if clip.state != ClipPlaying {
		return fmt.Errorf("%w: pause media clip while %v", ErrInvalidState, clip.state)
	}
	clip.state = ClipPaused
	m.invalidateOutput()
	return nil
}

func (m *Media) Resume(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state != ClipPaused {
		return fmt.Errorf("%w: resume media clip while %v", ErrInvalidState, clip.state)
	}
	clip.state = ClipPlaying
	return nil
}

func (m *Media) Stop(owner OwnerID, id ServiceID) error {
	clip, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if clip.state != ClipPlaying && clip.state != ClipPaused && clip.state != ClipRecording {
		return fmt.Errorf("%w: stop media clip while %v", ErrInvalidState, clip.state)
	}
	clip.state = ClipStopped
	clip.remainingPlays = 0
	clip.waitingForData = false
	m.invalidateOutput()
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
	if clip.position == position {
		// Seeking to where the cursor already sits is not a discontinuity, and
		// the state reconcilers re-seek every clip to its current position on
		// every save. Invalidating here would drop buffered PCM and start a new
		// audio generation each time a title is saved.
		return nil
	}
	clip.position = position
	m.invalidateOutput()
	return nil
}

// OutputRevision changes whenever buffered audio becomes stale because of a
// guest-visible discontinuity. Frontends use it to discard already-published
// chunks before accepting newly mixed samples.
func (m *Media) OutputRevision() uint64 { return m.outputRevision }

func (m *Media) invalidateOutput() {
	m.queuedPCM16 = m.queuedPCM16[:0]
	m.outputRemainder = 0
	m.outputRevision++
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
	if clip.volume == volume && clip.muted == muted && clip.pan == pan {
		return nil
	}
	clip.volume, clip.muted, clip.pan = volume, muted, pan
	m.invalidateOutput()
	return nil
}

func (m *Media) SetGlobalGain(volume uint8, muted bool) error {
	if volume > 100 {
		return fmt.Errorf("%w: invalid global volume %d", ErrInvalidArgument, volume)
	}
	if m.globalVolume == volume && m.globalMute == muted {
		return nil
	}
	m.globalVolume, m.globalMute = volume, muted
	m.invalidateOutput()
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
		WaitingForData: clip.waitingForData,
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
	droppedBefore := m.dropped
	revisionBefore := m.outputRevision
	if err := m.advanceLocked(start, end, bus); err != nil {
		_ = m.Restore(mediaBefore)
		_ = bus.Restore(busBefore)
		m.dropped = droppedBefore
		// A fully undone advance published nothing, so it is not a
		// discontinuity. Restore raises the revision because loading a save
		// state is one; a rollback must not make the host tear down its audio
		// generation for a tick that never happened.
		m.outputRevision = revisionBefore
		return err
	}
	return nil
}

// advanceLocked mixes and advances the media timeline without taking its own
// rollback snapshot. Services.Advance already snapshots the whole service state
// (media and the event bus included) for its per-frame transaction, so it calls
// this directly to avoid cloning the event queue and clip sources twice every
// frame — that duplicate snapshot dominated per-frame allocation. On error it
// leaves partial state; the caller (public Advance or Services.Advance) owns the
// restore. Standalone callers use the public Advance wrapper above.
func (m *Media) advanceLocked(start, end time.Duration, bus *EventBus) error {
	if start < 0 || end < start || bus == nil {
		return fmt.Errorf("%w: invalid media advance", ErrInvalidArgument)
	}
	delta := end - start
	voices := m.playbackVoices()
	activeDecoded := false
	audible := false
	for _, clip := range voices {
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
	// The mixer retains the newest MaxQueuedSamples samples. Frames older than
	// that window cannot survive this advance, so they are neither mixed nor
	// stored: rendering them would only produce work that retention discards.
	firstFrame := uint64(0)
	if maxFrames := m.retainedFrames(); audible && frameCount > maxFrames {
		firstFrame = frameCount - maxFrames
	}
	sampleCount := uint64(0)
	if audible {
		sampleCount = (frameCount - firstFrame) * uint64(m.limits.OutputChannels)
	}

	if cap(m.mixScratch) < int(sampleCount) {
		m.mixScratch = make([]int64, int(sampleCount))
	} else {
		m.mixScratch = m.mixScratch[:int(sampleCount)]
		clear(m.mixScratch)
	}
	mixed := m.mixScratch
	for _, clip := range voices {
		if clip.state != ClipPlaying || clip.decoded == nil ||
			clip.decoded.duration <= 0 || clip.muted || m.globalMute ||
			clip.volume == 0 || m.globalVolume == 0 {
			continue
		}
		for frame := firstFrame; frame < frameCount; frame++ {
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
			slot := frame - firstFrame
			if m.limits.OutputChannels == 1 {
				mixed[slot] += int64(left)/2 + int64(right)/2
			} else {
				mixed[slot*2] += int64(left)
				mixed[slot*2+1] += int64(right)
			}
		}
	}
	m.dropped += firstFrame * uint64(m.limits.OutputChannels)
	m.queueOutput(mixed)
	if activeDecoded {
		m.outputRemainder = remainder
	}

	for _, clip := range voices {
		if clip.state != ClipPlaying {
			continue
		}
		if clip.decoded == nil || clip.decoded.duration <= 0 {
			if clip.waitingForData {
				continue
			}
			return fmt.Errorf("%w: playing clip has no decoder", ErrMediaUnsupported)
		}
		// A decoded clip advances by the audio actually rendered for it, not
		// by the wall time the frame covered. The two differ by a fraction of
		// a frame, and the mixer used to spend that fraction twice: the frame
		// count came from an exact carrying accumulator while the read cursor
		// was recomputed from the clip's nanosecond position, so the two
		// disagreed by a sample at most block boundaries. At a 16 ms frame
		// that put a repeated or skipped sample into the stream about sixty
		// times a second, and the comb it left sat only 19 dB below the music
		// it was riding on. Stepping the position by the rendered frames keeps
		// the next block's cursor exactly where this one stopped.
		if err := m.advanceClipTimeline(
			clip,
			clipFrameStep(clip.position, frameCount, m.limits.OutputSampleRate),
			start,
			bus,
		); err != nil {
			return err
		}
	}
	return nil
}

// retainedFrames reports how many output frames fit in the retention window.
func (m *Media) retainedFrames() uint64 {
	return uint64(m.limits.MaxQueuedSamples) / uint64(m.limits.OutputChannels)
}

// queueOutput stores mixed samples, keeping the newest MaxQueuedSamples and
// discarding whatever no longer fits. A host that stops draining audio - a
// headless probe, a minimised window, a frontend between presentations - must
// not be able to fault the guest, so the queue behaves as a bounded ring
// rather than as a hard limit.
func (m *Media) queueOutput(mixed []int64) {
	retention := int(m.limits.MaxQueuedSamples)
	if len(mixed) >= retention {
		m.dropped += uint64(len(m.queuedPCM16) + len(mixed) - retention)
		mixed = mixed[len(mixed)-retention:]
		m.queuedPCM16 = m.queuedPCM16[:0]
	} else if overflow := len(m.queuedPCM16) + len(mixed) - retention; overflow > 0 {
		m.dropped += uint64(overflow)
		m.queuedPCM16 = append(m.queuedPCM16[:0], m.queuedPCM16[overflow:]...)
	}
	for _, sample := range mixed {
		m.queuedPCM16 = append(m.queuedPCM16, clampInt16(sample))
	}
}

// DroppedSamples reports how many mixed samples retention has discarded
// because the host did not drain them in time.
func (m *Media) DroppedSamples() uint64 {
	return m.dropped
}

func (m *Media) Drain() AudioBuffer {
	result := AudioBuffer{
		SampleRate: int(m.limits.OutputSampleRate),
		Channels:   int(m.limits.OutputChannels),
		PCM16:      append([]int16(nil), m.queuedPCM16...),
	}
	m.queuedPCM16 = m.queuedPCM16[:0]
	return result
}

// mediaAdvanceState is the part of a Media that advanceLocked can change, kept
// so Services.Advance can undo the advance when a later step of the same tick
// fails.
//
// It exists because the rollback used the full Snapshot, which clones every
// clip's source bytes.
// That runs on every tick whether or not anything fails, and on a title with
// audio loaded it was 17% of the frame - all of it copying bytes advanceLocked
// cannot touch. Advancing only moves playback positions and the mixed output
// queue, so only those are captured, into buffers reused across ticks.
//
// Like MediaState, it deliberately does not carry the dropped-sample counter:
// Restore does not reset it either, so a rolled-back advance leaves the same
// diagnostic total it always has.
type mediaAdvanceState struct {
	outputRemainder uint64
	queuedPCM16     []int16
	clips           []mediaClipAdvanceState
}

// mediaClipAdvanceState is one voice's rollback record. The clip is held by
// pointer because advanceLocked never adds or removes a voice, so the set is
// the same when the rollback runs.
type mediaClipAdvanceState struct {
	clip           *mediaClip
	position       time.Duration
	state          ClipPlaybackState
	remainingPlays int32
}

// captureAdvance records what an advance may change, reusing destination's
// buffers.
func (m *Media) captureAdvance(destination *mediaAdvanceState) {
	destination.outputRemainder = m.outputRemainder
	destination.queuedPCM16 = append(
		destination.queuedPCM16[:0],
		m.queuedPCM16...,
	)
	destination.clips = destination.clips[:0]
	for _, clip := range m.clips {
		destination.clips = append(destination.clips, mediaClipAdvanceState{
			clip:           clip,
			position:       clip.position,
			state:          clip.state,
			remainingPlays: clip.remainingPlays,
		})
	}
}

// restoreAdvance puts back what captureAdvance recorded.
func (m *Media) restoreAdvance(saved *mediaAdvanceState) {
	m.outputRemainder = saved.outputRemainder
	m.queuedPCM16 = append(m.queuedPCM16[:0], saved.queuedPCM16...)
	for _, clip := range saved.clips {
		clip.clip.position = clip.position
		clip.clip.state = clip.state
		clip.clip.remainingPlays = clip.remainingPlays
	}
}

func (m *Media) Snapshot() MediaState {
	state := MediaState{
		Limits:          m.limits,
		GlobalVolume:    m.globalVolume,
		GlobalMute:      m.globalMute,
		OutputRemainder: m.outputRemainder,
		QueuedPCM16:     append([]int16(nil), m.queuedPCM16...),
		AudioMixMode:    m.mixMode,
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
		state.BGMEndedElapsedNS < 0 ||
		time.Duration(state.BGMEndedElapsedNS) > 750*time.Millisecond ||
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
		if clip.decoded == nil && looksLikeSequencedScore(clip.source) {
			// Restoring the decoder is not limited to sounding clips. A stopped
			// score clip still reports its duration through Info and still owes
			// a decreasing watermark to AvailableBytes, so leaving it undecoded
			// makes a restored machine disagree with the one it was saved from.
			clip.decoded = m.decodeScoreAtRate(
				clip.source,
				state.Limits.OutputSampleRate,
			)
		}
		if clip.decoded == nil &&
			(saved.State == ClipPlaying || saved.State == ClipPaused) {
			// A sounding clip whose buffer does not decode yet is stalled
			// waiting for the rest of its stream, which is the same state
			// Append leaves behind. Advance skips it; it must not fail the
			// load.
			clip.waitingForData = true
		}
		if clip.decoded != nil && clip.position > clip.decoded.duration {
			return fmt.Errorf("%w: media clip %d position exceeds duration", ErrInvalidState, index)
		}
		clips[saved.ID] = clip
		previous = saved.ID
	}
	if state.BGMVoice != nil {
		v := state.BGMVoice
		if len(v.MediaType) > 127 || strings.IndexByte(v.MediaType, 0) >= 0 ||
			v.MediaType != strings.ToLower(strings.TrimSpace(v.MediaType)) ||
			uint64(len(v.Source)) > state.Limits.MaxSourceBytes ||
			v.PositionNS < 0 || v.Volume > 100 ||
			v.Pan < -100 || v.Pan > 100 {
			return fmt.Errorf("%w: invalid media music voice", ErrInvalidState)
		}
		decoded := decodeWavePCM16(v.Source)
		if decoded == nil && looksLikeSequencedScore(v.Source) {
			decoded = m.decodeScoreAtRate(
				v.Source,
				state.Limits.OutputSampleRate,
			)
		}
		if decoded != nil && time.Duration(v.PositionNS) > decoded.duration {
			return fmt.Errorf("%w: media music voice position exceeds duration", ErrInvalidState)
		}
	}
	m.limits = state.Limits
	m.clips = clips
	m.globalVolume = state.GlobalVolume
	m.globalMute = state.GlobalMute
	m.outputRemainder = state.OutputRemainder
	m.queuedPCM16 = append([]int16(nil), state.QueuedPCM16...)
	m.mixMode = state.AudioMixMode
	// Legacy detached BGM payloads are validated above, then retired because
	// they cannot be represented without violating clip ownership.
	m.outputRevision++
	return nil
}

func looksLikeSequencedPrefix(data []byte) bool {
	return len(data) >= 4 && (string(data[:4]) == "MMMD" || string(data[:4]) == "MThd")
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

func (m *Media) decodeScore(data []byte) *decodedPCM {
	return m.decodeScoreAtRate(data, m.limits.OutputSampleRate)
}

// decodeScoreAtRate shares a deterministic decoded stream for identical score
// bytes, in either container the synthesiser plays. Playback position, gain,
// looping, and completion state live on each mediaClip, while decoded PCM is a
// content property. A lazy stream may append to its shared sample prefix, but
// never changes an existing sample.
func (m *Media) decodeScoreAtRate(data []byte, sampleRate uint32) *decodedPCM {
	key := smafDecodeCacheKey{
		digest:     sha256.Sum256(data),
		sampleRate: sampleRate,
	}
	if cached, ok := m.smafCache[key]; ok {
		return cached.decoded
	}
	decoded := decodeScoreLazyPCM16(data, sampleRate)
	if m.smafCache == nil {
		m.smafCache = make(map[smafDecodeCacheKey]smafDecodeCacheEntry)
	}
	if len(m.smafCacheFIFO) == maxSMAFDecodeCacheEntries {
		m.evictOldestSMAFDecode()
	}
	m.smafCache[key] = smafDecodeCacheEntry{decoded: decoded}
	m.smafCacheFIFO = append(m.smafCacheFIFO, key)
	// The newest entry is always kept: a single score longer than the budget
	// must still be shared by the clip and the persistent music voice.
	for len(m.smafCacheFIFO) > 1 &&
		m.smafCacheSamples() > maxSMAFDecodeCacheSamples {
		m.evictOldestSMAFDecode()
	}
	return decoded
}

// smafCacheSamples reports the PCM the cache holds. A dropped entry keeps
// playing for whichever clip already points at it; only the sharing is lost.
func (m *Media) smafCacheSamples() int {
	total := 0
	for _, key := range m.smafCacheFIFO {
		if entry := m.smafCache[key]; entry.decoded != nil {
			total += len(entry.decoded.samples)
		}
	}
	return total
}

func (m *Media) evictOldestSMAFDecode() {
	if len(m.smafCacheFIFO) == 0 {
		return
	}
	oldest := m.smafCacheFIFO[0]
	copy(m.smafCacheFIFO, m.smafCacheFIFO[1:])
	m.smafCacheFIFO = m.smafCacheFIFO[:len(m.smafCacheFIFO)-1]
	delete(m.smafCache, oldest)
}

// clipFrameStep reports how far a clip's position moves when the mixer has
// rendered the given number of output frames for it. The result names the frame
// after the last one rendered, so that reading the position back - which
// clipSampleAt does by rounding it into a frame index - lands exactly there.
func clipFrameStep(
	position time.Duration,
	frames uint64,
	rate uint32,
) time.Duration {
	if position < 0 {
		return 0
	}
	second := uint64(time.Second)
	played := (uint64(position)*uint64(rate) + second/2) / second
	target := (played + frames) * second / uint64(rate)
	if target <= uint64(position) {
		return 0
	}
	return time.Duration(target - uint64(position))
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
	cursor := uint64(position) * uint64(clip.decoded.sampleRate)
	frame := cursor / uint64(time.Second)
	// A clip already stored at the mixer's rate is read straight out: its
	// frames line up one for one with the output, so filtering it would only
	// blur samples that need no reconstruction. Every SMAF score is rendered
	// at the output rate and takes this path.
	//
	// The frame has to be rounded rather than truncated to get there. The
	// caller names an output frame by the nanosecond it starts at, and a
	// nanosecond cannot name a 44.1 kHz frame exactly: truncating the way back
	// lands on the previous stored frame for every output frame except the
	// 1-in-441 whose nanosecond happens to be exact, so the read both stalled
	// and skipped a sample about a hundred times a second. Rounding recovers
	// the frame the nanosecond was made from, because the two conversions
	// together move the index by well under half a frame.
	if clip.decoded.sampleRate == m.limits.OutputSampleRate {
		frame = (cursor + uint64(time.Second)/2) / uint64(time.Second)
		clip.decoded.ensureFrame(frame)
		frames := uint64(len(clip.decoded.samples)) / uint64(clip.decoded.channels)
		if frame >= frames {
			return 0, 0, false
		}
		if clip.decoded.channels == 1 {
			value := clip.decoded.samples[frame]
			return value, value, true
		}
		return clip.decoded.samples[frame*2], clip.decoded.samples[frame*2+1], true
	}
	// A rate the mixer does not share has to be reconstructed rather than
	// held; see media_resample.go for why the held version sounded wrong.
	kernel := resampleKernelFor(
		clip.decoded.sampleRate,
		m.limits.OutputSampleRate,
	)
	clip.decoded.ensureFrame(frame + uint64(kernel.half))
	frames := uint64(len(clip.decoded.samples)) / uint64(clip.decoded.channels)
	if frame >= frames {
		return 0, 0, false
	}
	fraction := float64(cursor%uint64(time.Second)) / float64(time.Second)
	left, right := clip.decoded.resampleAt(kernel, frame, fraction, frames)
	return left, right, true
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

func (m *Media) advanceClipTimeline(
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
