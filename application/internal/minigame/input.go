package minigame

import (
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/profile"
	"sort"
	"time"
)

const (
	maxPendingInput      = 1024
	pointerEvent         = uint32(0x050e)
	pointerPacketAddress = eadsPaletteAddress + 1024
)

type keyEvent struct {
	event uint32
	key   uint32
	at    time.Duration
}

// InputKeyCode translates frontend controls to the OEM event dispatcher's keys.
func InputKeyCode(control string) (uint32, bool) {
	key, ok := guest.InputKeyCode(control)
	if !ok {
		return 0, false
	}
	if key >= profile.Key0 && key <= profile.Key9 {
		return uint32(key - profile.Key0), true
	}
	switch key {
	case profile.KeyAsterisk:
		return 0x0e, true
	case profile.KeyPound:
		return 0x0f, true
	case profile.KeyUp:
		return 0x0c, true
	case profile.KeyDown:
		return 0x0d, true
	case profile.KeyLeft:
		return 0x0a, true
	case profile.KeyRight:
		return 0x0b, true
	case profile.KeySelect:
		return 0x13, true
	case profile.KeySoft1:
		return 0x11, true
	case profile.KeySoft2:
		return 0x12, true
	case profile.KeySend:
		return 0x10, true
	case profile.KeyEnd:
		return 0x14, true
	case profile.KeyClear:
		return 0x12, true
	}
	return 0, false
}

// QueueKey returns false when the bounded queue must be drained first.
func (r *Runtime) QueueKey(key uint32, pressed bool) bool {
	return r.QueueKeyAt(key, pressed, 0)
}

func (r *Runtime) QueueKeyAt(key uint32, pressed bool, at time.Duration) bool {
	if key > 0x15 || at < 0 {
		return false
	}
	// Modern OEM dispatchers receive the key itself as r0. They do not
	// consume a host release as a second key; 0x15 is the handset clear key.
	if !r.legacy && !pressed {
		return true
	}
	event, argument := key, uint32(0)
	if r.legacy {
		argument = key
		event = 6
		if pressed {
			event = 4
		}
	}
	return r.queueInput(keyEvent{event: event, key: argument, at: at})
}

// EnablePointerInput is a profile capability, set before execution begins.
func (r *Runtime) EnablePointerInput()   { r.pointerEnabled = !r.legacy }
func (r *Runtime) SupportsPointer() bool { return r.pointerEnabled }

func (r *Runtime) QueuePointer(x, y int, pressed bool, at time.Duration) bool {
	if !r.pointerEnabled || x < 0 || y < 0 || x >= r.width || y >= r.height || at < 0 {
		return false
	}
	phase := uint32(2)
	if pressed {
		phase = 1
	}
	return r.queueInput(keyEvent{event: phase<<16 | pointerEvent,
		key: uint32(x) | uint32(y)<<16, at: at})
}

func (r *Runtime) queueInput(event keyEvent) bool {
	r.inputMu.Lock()
	defer r.inputMu.Unlock()
	if len(r.pendingKeys) >= maxPendingInput {
		return false
	}
	index := sort.Search(len(r.pendingKeys), func(i int) bool { return r.pendingKeys[i].at > event.at })
	r.pendingKeys = append(r.pendingKeys, keyEvent{})
	copy(r.pendingKeys[index+1:], r.pendingKeys[index:])
	r.pendingKeys[index] = event
	return true
}

func (r *Runtime) popInput() (keyEvent, bool) {
	r.inputMu.Lock()
	defer r.inputMu.Unlock()
	if len(r.pendingKeys) == 0 || r.pendingKeys[0].at > r.clockNS {
		return keyEvent{}, false
	}
	event := r.pendingKeys[0]
	r.pendingKeys = r.pendingKeys[1:]
	return event, true
}

func (r *Runtime) inputArgs(initialize bool) ([4]uint32, error) {
	args := [4]uint32{r.activeEvent, r.activeKey}
	if r.activeInput && r.activeEvent&0xffff == pointerEvent {
		args[0], args[1] = pointerEvent, pointerPacketAddress
		if initialize {
			var packet [8]byte
			packet[0] = byte(r.activeEvent >> 16)
			binary.LittleEndian.PutUint16(packet[2:], uint16(r.activeKey))
			binary.LittleEndian.PutUint16(packet[4:], uint16(r.activeKey>>16))
			if err := r.cpu.WriteMemory(pointerPacketAddress, packet[:]); err != nil {
				return args, err
			}
		}
	}
	return args, nil
}
