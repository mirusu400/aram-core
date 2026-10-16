package minigame

import "fmt"

const maxAudioHandles = 256

func (r *Runtime) playMusic(id, source uint32) error {
	if r.audioHandles == nil {
		r.audioHandles = make(map[uint32]uint32)
	}
	if r.audioHandles[id] == 0 && len(r.audioHandles) >= maxAudioHandles {
		return fmt.Errorf("OEM audio channel limit")
	}
	handle, err := r.public.OEMPlaySMAF(r.audioHandles[id], source)
	if err != nil {
		return err
	}
	if handle != 0 {
		r.audioHandles[id] = handle
	}
	return nil
}

func (r *Runtime) stopMusic() {
	for _, handle := range r.audioHandles {
		r.public.OEMStopClip(handle)
	}
	clear(r.audioHandles)
}
