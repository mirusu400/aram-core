package wipi

import (
	"encoding/binary"
	"fmt"
	shared "github.com/mirusu400/aram-core/runtime"
)

// OEMPlaySMAF bridges a bounded native MMMD object to the shared media service.
// Its ordinary clip handle and playback state use the existing WIPI state format.
func (r *Runtime) OEMPlaySMAF(previous, source uint32) (uint32, error) {
	if source == 0 {
		return 0, nil
	}
	var header [8]byte
	if err := r.CPU.ReadMemory(source, header[:]); err != nil {
		return 0, err
	}
	size := uint64(binary.BigEndian.Uint32(header[4:])) + 8
	if string(header[:4]) != "MMMD" || size < 12 || size > uint64(maxWIPIString) || uint64(source)+size > 1<<32 {
		return 0, nil
	}
	handle, err := r.RaptorCreateClip("audio/mmf", int32(size), 0)
	if err != nil || handle == 0 {
		return 0, err
	}
	if !r.RaptorPutClipData(handle, source, int32(size)) {
		r.RaptorStopClip(handle, true)
		return 0, fmt.Errorf("read OEM SMAF object")
	}
	if previous != 0 {
		r.OEMStopClip(previous)
	}
	if !r.RaptorPlayClip(handle, false) {
		r.RaptorStopClip(handle, true)
		return 0, nil
	}
	return handle, nil
}

func (r *Runtime) OEMStopClip(handle uint32) { r.RaptorStopClip(handle, true) }

func (r *Runtime) OEMClipPlaying(handle uint32) bool {
	service := r.MediaServices[handle]
	if service == 0 {
		return false
	}
	info, err := r.Services.Media.Info(r.ServiceOwner, service)
	return err == nil && info.State == shared.ClipPlaying
}
