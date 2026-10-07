package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

const brewMediaOwner shared.OwnerID = 1

type brewSoundPlayer struct {
	callback  uint32
	context   uint32
	input     uint32
	data      uint32
	size      uint32
	clip      shared.ServiceID
	mediaType string
}

func decodeBREWAudioResource(data []byte) (string, []byte, bool) {
	if len(data) < 4 {
		return "", nil, false
	}
	offset := int(binary.LittleEndian.Uint16(data[:2]))
	if offset < 3 || offset >= len(data) {
		return "", nil, false
	}
	mime := strings.ToLower(strings.TrimSpace(string(bytes.TrimRight(data[2:offset], "\x00"))))
	if !strings.HasPrefix(mime, "audio/") && !strings.HasPrefix(mime, "sound/") {
		return "", nil, false
	}
	return mime, append([]byte(nil), data[offset:]...), true
}

func (r *Runtime) replaceSoundPlayerSource(mediaType string, data []byte) error {
	if r.soundPlayer.clip != 0 {
		if info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip); err == nil &&
			(info.State == shared.ClipPlaying || info.State == shared.ClipPaused || info.State == shared.ClipRecording) {
			_ = r.media.Stop(brewMediaOwner, r.soundPlayer.clip)
		}
		_ = r.media.DestroyClip(brewMediaOwner, r.soundPlayer.clip, r.mediaEvents)
		r.soundPlayer.clip = 0
	}
	if len(data) == 0 {
		r.soundPlayer.mediaType = ""
		return nil
	}
	clip, err := r.media.CreateClip(brewMediaOwner, mediaType, uint64(len(data)))
	if err != nil {
		return fmt.Errorf("create BREW sound clip: %w", err)
	}
	if _, err := r.media.Append(brewMediaOwner, clip, data); err != nil {
		_ = r.media.DestroyClip(brewMediaOwner, clip, r.mediaEvents)
		return fmt.Errorf("load BREW sound clip: %w", err)
	}
	if err := r.media.SetClipGain(brewMediaOwner, clip, uint8(r.soundVolume), false, 0); err != nil {
		_ = r.media.DestroyClip(brewMediaOwner, clip, r.mediaEvents)
		return fmt.Errorf("set BREW sound volume: %w", err)
	}
	r.soundPlayer.clip = clip
	r.soundPlayer.mediaType = mediaType
	return nil
}

func (r *Runtime) setSoundPlayerInput(input, pointer, size uint32) error {
	r.soundPlayer.input = input
	r.soundPlayer.data = pointer
	r.soundPlayer.size = size

	switch input {
	case 0: // SDT_NONE
		return r.replaceSoundPlayerSource("", nil)
	case 1: // SDT_FILE
		if pointer == 0 {
			return r.replaceSoundPlayerSource("", nil)
		}
		name, err := r.readCString(pointer)
		if err != nil {
			return err
		}
		contents, resolved, ok := r.lookupGuestFile(normalizeGuestPath(name))
		if !ok {
			return r.replaceSoundPlayerSource("", nil)
		}
		return r.replaceSoundPlayerSource(soundMediaType(resolved), contents)
	case 2: // SDT_BUFFER
		if pointer == 0 {
			return r.replaceSoundPlayerSource("", nil)
		}
		if size == 0 {
			size = r.soundBufferSize(pointer)
			r.soundPlayer.size = size
		}
		// The deprecated Set(SDT_BUFFER, ...) ABI carries no byte count. Native
		// BREW could only guess a small prefix, so leave an unbounded pointer
		// configured but silent; SetInfo is the reliable buffer API.
		if size == 0 {
			return r.replaceSoundPlayerSource("", nil)
		}
		data := make([]byte, size)
		if err := r.cpu.ReadMemory(pointer, data); err != nil {
			return fmt.Errorf("read BREW sound buffer: %w", err)
		}
		mediaType := ""
		if wrappedType, payload, ok := decodeBREWAudioResource(data); ok {
			mediaType, data = wrappedType, payload
		}
		return r.replaceSoundPlayerSource(mediaType, data)
	case 3: // SDT_VOICEPROMPT is not an encoded media source.
		return r.replaceSoundPlayerSource("", nil)
	default:
		return fmt.Errorf("unsupported BREW sound input %d", input)
	}
}

func (r *Runtime) soundBufferSize(pointer uint32) uint32 {
	if size, ok := r.heapAllocated[pointer]; ok {
		return size
	}
	for address, size := range r.heapAllocated {
		if pointer >= address && pointer-address < size {
			return size - (pointer - address)
		}
	}
	return 0
}

func soundMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav":
		return "audio/wav"
	case ".mid", ".midi":
		return "audio/midi"
	case ".mmf", ".smaf":
		return "audio/x-smaf"
	case ".mp3":
		return "audio/mpeg"
	default:
		return ""
	}
}

func (r *Runtime) setSoundPlayerInfo(pointer uint32) error {
	if pointer == 0 {
		return r.setSoundPlayerInput(0, 0, 0)
	}
	var encoded [12]byte
	if err := r.cpu.ReadMemory(pointer, encoded[:]); err != nil {
		return fmt.Errorf("read BREW sound-player info: %w", err)
	}
	return r.setSoundPlayerInput(
		binary.LittleEndian.Uint32(encoded[0:4]),
		binary.LittleEndian.Uint32(encoded[4:8]),
		binary.LittleEndian.Uint32(encoded[8:12]),
	)
}

func (r *Runtime) getSoundPlayerInfo() error {
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	if pointer == 0 {
		return nil
	}
	var encoded [12]byte
	binary.LittleEndian.PutUint32(encoded[0:4], r.soundPlayer.input)
	binary.LittleEndian.PutUint32(encoded[4:8], r.soundPlayer.data)
	binary.LittleEndian.PutUint32(encoded[8:12], r.soundPlayer.size)
	if err := r.cpu.WriteMemory(pointer, encoded[:]); err != nil {
		return fmt.Errorf("write BREW sound-player info: %w", err)
	}
	return nil
}

func (r *Runtime) playSoundPlayer() {
	if r.soundPlayer.clip == 0 {
		return
	}
	info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip)
	if err != nil || info.State != shared.ClipStopped {
		return
	}
	_ = r.media.Play(brewMediaOwner, r.soundPlayer.clip, 1)
}

func (r *Runtime) stopSoundPlayer() {
	if r.soundPlayer.clip == 0 {
		return
	}
	info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip)
	if err == nil && info.State != shared.ClipStopped {
		_ = r.media.Stop(brewMediaOwner, r.soundPlayer.clip)
	}
}

func (r *Runtime) pauseSoundPlayer() {
	if r.soundPlayer.clip == 0 {
		return
	}
	info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip)
	if err == nil && info.State == shared.ClipPlaying {
		_ = r.media.Pause(brewMediaOwner, r.soundPlayer.clip)
	}
}

func (r *Runtime) resumeSoundPlayer() {
	if r.soundPlayer.clip == 0 {
		return
	}
	info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip)
	if err == nil && info.State == shared.ClipPaused {
		_ = r.media.Resume(brewMediaOwner, r.soundPlayer.clip)
	}
}

func (r *Runtime) seekSoundPlayer(forward bool) {
	if r.soundPlayer.clip == 0 {
		return
	}
	deltaMS, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return
	}
	info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip)
	if err != nil {
		return
	}
	delta := time.Duration(deltaMS) * time.Millisecond
	position := info.Position
	if forward {
		position = min(position+delta, info.Duration)
	} else if delta >= position {
		position = 0
	} else {
		position -= delta
	}
	_ = r.media.Seek(brewMediaOwner, r.soundPlayer.clip, position)
}

func (r *Runtime) setSoundPlayerVolume() {
	if r.soundPlayer.clip != 0 {
		_ = r.media.SetClipGain(brewMediaOwner, r.soundPlayer.clip, uint8(r.soundVolume), false, 0)
	}
}

// DrainAudio transfers all PCM mixed through the current deterministic clock.
func (r *Runtime) DrainAudio() (shared.AudioBuffer, time.Duration, uint64) {
	return r.media.Drain(), r.clock, r.media.OutputRevision()
}

// DrainTimedAudio transfers one continuous span with the anchor recorded when
// it was mixed. With no PCM, the timestamp is the current cooperative clock.
// This consumes the same queue as DrainAudio; callers choose one consumer.
func (r *Runtime) DrainTimedAudio() (shared.AudioBuffer, time.Duration, uint64) {
	audio, start := r.media.DrainTimed()
	if len(audio.PCM16) == 0 {
		start = r.clock
	}
	return audio, start, r.media.OutputRevision()
}

func (r *Runtime) AudioOutputRevision() uint64 { return r.media.OutputRevision() }
