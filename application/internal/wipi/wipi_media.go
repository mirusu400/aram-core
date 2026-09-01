package wipi

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"time"
)

func (r *Runtime) dispatchMedia(name string) (guest.WIPIReturn, bool, error) {
	count := mediaArgumentCount(name)
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	clip := func() *wipiMediaClip { return r.MediaClips[arg(0)] }
	switch name {
	case "MC_mdaClipCreate":
		mediaType, err := r.ReadCString(arg(0))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		capacity := int32(arg(1))
		if capacity < 0 || capacity > int32(maxWIPIString) {
			return guest.WIPIReturn{}, true, nil
		}
		handle, err := r.Heap.Allocate(64, true)
		if err != nil || handle == 0 {
			return guest.WIPIReturn{}, true, err
		}
		r.MediaClips[handle] = &wipiMediaClip{
			Handle:    handle,
			mediaType: append([]byte(nil), mediaType...),
			capacity:  capacity,
			Callback:  arg(2),
			volume:    100,
		}
		serviceID, serviceErr := r.Services.Media.CreateClip(
			r.ServiceOwner,
			string(mediaType),
			uint64(max(0, int(capacity))),
		)
		if serviceErr != nil {
			delete(r.MediaClips, handle)
			r.Heap.Release(handle)
			return guest.WIPIReturn{}, true, nil
		}
		r.MediaServices[handle] = serviceID
		return guest.WIPIReturn{Low: handle}, true, nil
	case "MC_mdaClipFree":
		if clip() == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.MediaServices[arg(0)]; serviceID != 0 {
			if err := r.Services.Media.DestroyClip(
				r.ServiceOwner,
				serviceID,
				r.Services.Events,
			); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			delete(r.MediaServices, arg(0))
		}
		delete(r.MediaClips, arg(0))
		r.Heap.Release(arg(0))
		return guest.WIPIReturn{}, true, nil
	case "MC_mdaClipGetType":
		current := clip()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		count, err := r.writeCString(arg(1), current.mediaType, int32(arg(2)))
		return guest.WIPIReturn{Low: count}, true, err
	case "MC_mdaClipPutData":
		return r.putMediaData(clip(), arg(1), int32(arg(2)))
	case "MC_mdaClipPutDataByFile":
		current := clip()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		name, err := r.guestPath(arg(1), int32(arg(3)))
		if err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		data, ok := r.Files[name]
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		size := min(len(data), max(0, int(int32(arg(2)))))
		return r.appendMediaData(current, data[:size])
	case "MC_mdaClipGetData":
		current := clip()
		length := int(int32(arg(2)))
		if current == nil || length < 0 {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		count := min(length, len(current.Data))
		if err := r.CPU.WriteMemory(arg(1), current.Data[:count]); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		current.Data = append(current.Data[:0], current.Data[count:]...)
		if serviceID := r.MediaServices[current.Handle]; serviceID != 0 {
			if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			if _, err := r.Services.Media.Append(
				r.ServiceOwner,
				serviceID,
				current.Data,
			); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		return guest.WIPIReturn{Low: uint32(count)}, true, nil
	case "MC_mdaClipAvailableDataSize":
		if current := clip(); current != nil {
			return guest.WIPIReturn{Low: uint32(len(current.Data))}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_mdaClipClearData":
		if current := clip(); current != nil {
			if serviceID := r.MediaServices[current.Handle]; serviceID != 0 {
				if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
			current.Data = nil
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_mdaClipSetPosition":
		if current := clip(); current != nil {
			current.position = int32(arg(1))
			if serviceID := r.MediaServices[current.Handle]; serviceID != 0 &&
				current.position >= 0 {
				_ = r.Services.Media.Seek(
					r.ServiceOwner,
					serviceID,
					time.Duration(current.position)*time.Millisecond,
				)
			}
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_mdaClipGetVolume":
		if current := clip(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.volume)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_mdaClipSetVolume":
		if current := clip(); current != nil {
			current.volume = int32(guest.Clamp(int(int32(arg(1))), 0, 100))
			if serviceID := r.MediaServices[current.Handle]; serviceID != 0 {
				if err := r.Services.Media.SetClipGain(
					r.ServiceOwner,
					serviceID,
					uint8(current.volume),
					false,
					0,
				); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_mdaPlay":
		if current := clip(); current != nil {
			current.State = 1
			current.Repeat = arg(1) != 0
			plays := int32(1)
			if current.Repeat {
				plays = -1
			}
			if serviceID := r.MediaServices[current.Handle]; serviceID != 0 {
				if err := r.Services.Media.Play(
					r.ServiceOwner,
					serviceID,
					plays,
				); err != nil {
					return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
				}
			}
			r.EnqueueCallback(current.Callback, current.Handle, uint32(current.State))
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_mdaPause":
		return r.setMediaState(clip(), 2)
	case "MC_mdaResume":
		return r.setMediaState(clip(), 1)
	case "MC_mdaStop":
		return r.setMediaState(clip(), 0)
	case "MC_mdaRecord":
		return r.setMediaState(clip(), 3)
	case "MC_mdaGetVolume":
		return guest.WIPIReturn{Low: uint32(r.mediaVolume)}, true, nil
	case "MC_mdaSetVolume":
		r.mediaVolume = int32(guest.Clamp(int(int32(arg(0))), 0, 100))
		if err := r.Services.Media.SetGlobalGain(
			uint8(r.mediaVolume),
			false,
		); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_mdaVibrator":
		r.vibratorLevel = int32(arg(0))
		r.vibratorTimeout = max(0, int32(arg(1)))
		level := uint8(guest.Clamp(int(r.vibratorLevel), 0, 100))
		if err := r.Services.Device.Vibrate(
			level,
			time.Duration(r.vibratorTimeout)*time.Millisecond,
			r.Services.Clock.Monotonic(),
		); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_mdaSetMuteState":
		// MC_mdaSetMuteState(source, bmute) silences one of the handset's own
		// sound sources - the key tone and the other system beeps a title does
		// not want chirping over its music - and not the application's clips.
		//
		// A title states that by muting a source for the length of its run:
		// 제노니아1 reads MC_mdaGetMuteState(6) into its settings block at
		// startup, calls MC_mdaSetMuteState(6, M_TRUE), and restores the saved
		// value on the way out; 테일즈위버 막시민편 mutes source 0 once during
		// init and never unmutes it, while its own option screen still drives
		// MC_mdaSetVolume in five steps and it ships 64 SMAF tracks
		// (aram-core #119).
		//
		// Folding those sources into the mixer's master mute is therefore
		// wrong twice over: it silences audio the handset still plays, and
		// because the mute is never lifted the title stays silent for the rest
		// of the session. The state is kept per source so MC_mdaGetMuteState
		// round trips - the save-and-restore pattern depends on it - and the
		// mixer is left alone.
		r.mediaMute[int32(arg(0))] = arg(1) != 0
		return guest.WIPIReturn{}, true, nil
	case "MC_mdaGetMuteState":
		if r.mediaMute[int32(arg(0))] {
			return guest.WIPIReturn{Low: 1}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func mediaArgumentCount(name string) int {
	switch name {
	case "MC_mdaGetVolume":
		return 0
	case "MC_mdaClipFree", "MC_mdaClipAvailableDataSize", "MC_mdaClipClearData",
		"MC_mdaClipGetVolume", "MC_mdaPause", "MC_mdaResume", "MC_mdaStop",
		"MC_mdaRecord", "MC_mdaSetVolume", "MC_mdaGetMuteState":
		return 1
	case "MC_mdaClipSetPosition", "MC_mdaClipSetVolume", "MC_mdaPlay",
		"MC_mdaVibrator", "MC_mdaSetMuteState":
		return 2
	case "MC_mdaClipCreate", "MC_mdaClipPutData", "MC_mdaClipGetType",
		"MC_mdaClipGetData":
		return 3
	case "MC_mdaClipPutDataByFile":
		return 4
	default:
		return 0
	}
}

func (r *Runtime) putMediaData(clip *wipiMediaClip, source uint32, length int32) (guest.WIPIReturn, bool, error) {
	if clip == nil || length < 0 || length > int32(maxWIPIString) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.CPU.ReadMemory(source, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	return r.appendMediaData(clip, data)
}

func (r *Runtime) appendMediaData(clip *wipiMediaClip, data []byte) (guest.WIPIReturn, bool, error) {
	if clip.capacity > 0 && len(clip.Data)+len(data) > int(clip.capacity) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if serviceID := r.MediaServices[clip.Handle]; serviceID != 0 {
		if _, err := r.Services.Media.Append(r.ServiceOwner, serviceID, data); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
	}
	clip.Data = append(clip.Data, data...)
	return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
}

func (r *Runtime) setMediaState(clip *wipiMediaClip, state uint8) (guest.WIPIReturn, bool, error) {
	if clip == nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	clip.State = state
	if state == 0 {
		clip.Repeat = false
	}
	if serviceID := r.MediaServices[clip.Handle]; serviceID != 0 {
		var err error
		switch state {
		case 0:
			err = r.Services.Media.Stop(r.ServiceOwner, serviceID)
		case 1:
			err = r.Services.Media.Resume(r.ServiceOwner, serviceID)
		case 2:
			err = r.Services.Media.Pause(r.ServiceOwner, serviceID)
		case 3:
			// Recording is modeled in the adapter until an input provider is
			// explicitly supplied; no host microphone is opened.
		}
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
	}
	r.EnqueueCallback(clip.Callback, clip.Handle, uint32(state))
	return guest.WIPIReturn{}, true, nil
}

// The LGT Raptor sound API (private import ordinals 1200-1221) drives the same
// clip machinery the public MC_mda* calls use, so these helpers let the Raptor
// runtime reuse the decoder-backed media service and its completion callbacks
// without exposing the clip bookkeeping. Handles are the same guest-visible
// allocations MC_mdaClipCreate hands out, and completion callbacks flow through
// the shared EventAudioComplete pump.

// RaptorCreateClip opens a clip with a completion callback and returns its
// guest handle, or 0 on failure.
func (r *Runtime) RaptorCreateClip(mediaType string, capacity int32, callback uint32) (uint32, error) {
	if capacity < 0 || capacity > int32(maxWIPIString) {
		return 0, nil
	}
	handle, err := r.Heap.Allocate(64, true)
	if err != nil || handle == 0 {
		return 0, err
	}
	serviceID, serviceErr := r.Services.Media.CreateClip(
		r.ServiceOwner,
		mediaType,
		uint64(max(0, int(capacity))),
	)
	if serviceErr != nil {
		r.Heap.Release(handle)
		return 0, nil
	}
	r.MediaClips[handle] = &wipiMediaClip{
		Handle:    handle,
		mediaType: []byte(mediaType),
		capacity:  capacity,
		Callback:  callback,
		volume:    100,
	}
	r.MediaServices[handle] = serviceID
	return handle, nil
}

// RaptorPutClipData appends encoded audio bytes to a clip's source buffer.
func (r *Runtime) RaptorPutClipData(handle, source uint32, length int32) bool {
	clip := r.MediaClips[handle]
	if clip == nil || length < 0 {
		return false
	}
	data := make([]byte, length)
	if err := r.CPU.ReadMemory(source, data); err != nil {
		return false
	}
	clip.Data = append(clip.Data, data...)
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		if _, err := r.Services.Media.Append(r.ServiceOwner, serviceID, data); err != nil {
			return false
		}
	}
	return true
}

// RaptorClearClipData empties a clip's source buffer so the next
// MC_mdaClipPutData starts a new track instead of appending to the last one.
//
// LGT titles keep a single clip for the whole session: they stop it, clear it,
// rewind it, put the next track's bytes in, and play again. With the clear
// missing the buffer only ever grew, so the decoder kept replaying the first
// track it was ever given, and once the accumulated tracks passed the clip's
// declared capacity every later MC_mdaClipPutData failed outright and the
// title fell silent for the rest of the session (issue #65).
func (r *Runtime) RaptorClearClipData(handle uint32) bool {
	clip := r.MediaClips[handle]
	if clip == nil {
		return false
	}
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
			return false
		}
	}
	clip.Data = nil
	clip.position = 0
	return true
}

// RaptorRewindClip seeks a clip back to its start.
func (r *Runtime) RaptorRewindClip(handle uint32) {
	clip := r.MediaClips[handle]
	if clip == nil {
		return
	}
	clip.position = 0
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		_ = r.Services.Media.Seek(r.ServiceOwner, serviceID, 0)
	}
}

// RaptorSetClipVolume sets a clip's playback gain (0-100).
func (r *Runtime) RaptorSetClipVolume(handle uint32, volume int32) {
	clip := r.MediaClips[handle]
	if clip == nil {
		return
	}
	clip.volume = int32(guest.Clamp(int(volume), 0, 100))
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		_ = r.Services.Media.SetClipGain(
			r.ServiceOwner,
			serviceID,
			uint8(clip.volume),
			false,
			0,
		)
	}
}

// RaptorPlayClip starts (or loops) playback of a clip.
func (r *Runtime) RaptorPlayClip(handle uint32, loop bool) bool {
	clip := r.MediaClips[handle]
	if clip == nil {
		return false
	}
	clip.State = 1
	clip.Repeat = loop
	plays := int32(1)
	if loop {
		plays = -1
	}
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		if err := r.Services.Media.Play(r.ServiceOwner, serviceID, plays); err != nil {
			return false
		}
	}
	return true
}

// RaptorClipEndCode is the status LGT Raptor's media provider reports to a
// clip's completion callback once the clip stops sounding, whether it reached
// its end or was stopped on request.
//
// 제노니아1's callback keeps the status in a four-bit field, so it first
// rewrites -1 into its own "finished" state — the only state that frees the
// clip. The statuses it stores verbatim leave the clip owned and playing, and
// its allocator refuses to build a second clip while the first handle is still
// held, so a title told anything else plays one sound and then stays silent
// for the rest of the session (issue #49).
const RaptorClipEndCode = ^uint32(0)

// RaptorStopClip stops playback and, when free is set, releases the clip and
// its guest handle. A stop reports completion to the clip's callback the way
// the handset does, which is how a title learns it may release the handle and
// load its next track.
func (r *Runtime) RaptorStopClip(handle uint32, free bool) {
	clip := r.MediaClips[handle]
	if clip == nil {
		return
	}
	if serviceID := r.MediaServices[handle]; serviceID != 0 {
		if free {
			_ = r.Services.Media.DestroyClip(r.ServiceOwner, serviceID, r.Services.Events)
			delete(r.MediaServices, handle)
		} else {
			_ = r.Services.Media.Stop(r.ServiceOwner, serviceID)
		}
	}
	if free {
		delete(r.MediaClips, handle)
		r.Heap.Release(handle)
		return
	}
	playing := clip.State != 0
	clip.State = 0
	clip.Repeat = false
	if playing && clip.Callback != 0 {
		r.EnqueueCallback(clip.Callback, handle, RaptorClipEndCode)
	}
}
