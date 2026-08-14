package profile

import "fmt"

// KeyCode is the signed 32-bit MH_KeyCode value delivered by the WIPI HAL.
type KeyCode int32

const (
	KeyInvalid  KeyCode = 0
	Key0        KeyCode = '0'
	Key1        KeyCode = '1'
	Key2        KeyCode = '2'
	Key3        KeyCode = '3'
	Key4        KeyCode = '4'
	Key5        KeyCode = '5'
	Key6        KeyCode = '6'
	Key7        KeyCode = '7'
	Key8        KeyCode = '8'
	Key9        KeyCode = '9'
	KeyAsterisk KeyCode = '*'
	KeyPound    KeyCode = '#'

	KeyUp       KeyCode = -1
	KeyDown     KeyCode = -2
	KeyLeft     KeyCode = -3
	KeyRight    KeyCode = -4
	KeySelect   KeyCode = -5
	KeySoft1    KeyCode = -6
	KeySoft2    KeyCode = -7
	KeySoft3    KeyCode = -8
	KeySend     KeyCode = -10
	KeyEnd      KeyCode = -11
	KeyPower    KeyCode = -12
	KeySideUp   KeyCode = -13
	KeySideDown KeyCode = -14
	KeySideSel  KeyCode = -15
	KeyClear    KeyCode = -16
	KeyFlipDown KeyCode = -17
	KeyFlipUp   KeyCode = -18
)

func (k KeyCode) Valid() bool {
	switch k {
	case KeyInvalid,
		Key0, Key1, Key2, Key3, Key4, Key5, Key6, Key7, Key8, Key9,
		KeyAsterisk, KeyPound,
		KeyUp, KeyDown, KeyLeft, KeyRight, KeySelect,
		KeySoft1, KeySoft2, KeySoft3, KeySend, KeyEnd, KeyPower,
		KeySideUp, KeySideDown, KeySideSel, KeyClear, KeyFlipDown, KeyFlipUp:
		return true
	default:
		return false
	}
}

type VirtualKey int32

const (
	VirtualUp         VirtualKey = 1
	VirtualLeft       VirtualKey = 2
	VirtualRight      VirtualKey = 5
	VirtualDown       VirtualKey = 6
	VirtualFire       VirtualKey = 8
	VirtualGameA      VirtualKey = 9
	VirtualGameB      VirtualKey = 10
	VirtualGameC      VirtualKey = 11
	VirtualGameD      VirtualKey = 12
	VirtualSideUp     VirtualKey = 96
	VirtualSideDown   VirtualKey = 97
	VirtualSideSelect VirtualKey = 98
	VirtualSideClear  VirtualKey = 99
)

func (k VirtualKey) Valid() bool {
	switch k {
	case VirtualUp, VirtualLeft, VirtualRight, VirtualDown, VirtualFire,
		VirtualGameA, VirtualGameB, VirtualGameC, VirtualGameD,
		VirtualSideUp, VirtualSideDown, VirtualSideSelect, VirtualSideClear:
		return true
	default:
		return false
	}
}

type KeyMap map[VirtualKey]KeyCode

func (m KeyMap) Validate() error {
	physical := make(map[KeyCode]VirtualKey, len(m))
	for virtual, key := range m {
		if !virtual.Valid() {
			return fmt.Errorf("invalid virtual key %d", virtual)
		}
		if !key.Valid() || key == KeyInvalid {
			return fmt.Errorf("invalid physical key %d for virtual key %d", key, virtual)
		}
		if previous, exists := physical[key]; exists {
			return fmt.Errorf(
				"physical key %d maps to virtual keys %d and %d",
				key,
				previous,
				virtual,
			)
		}
		physical[key] = virtual
	}
	return nil
}
