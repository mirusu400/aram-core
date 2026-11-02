package gvmhost

import "github.com/mirusu400/aram-core/profile"

// SKTGuestCode maps the authenticated five-way pad, first soft key, SEND and
// numeric keypad paths to the selected GVM guest codes. Titles treat code 13
// as CLR, so the clear key shares it with the first soft key, and ask for the
// SEND key where code 14 opens their WAP pages. Code 15 (END) never reaches
// the input entry: GVM2X runs the script's end entry and closes it instead,
// which the machine owns.
func SKTGuestCode(key profile.KeyCode) (uint16, bool) {
	switch key {
	case profile.KeySoft1, profile.KeyClear:
		return 13, true
	case profile.KeySend:
		return 14, true
	case profile.KeyUp:
		return 16, true
	case profile.KeyDown:
		return 17, true
	case profile.KeyLeft:
		return 18, true
	case profile.KeyRight:
		return 19, true
	case profile.KeySelect:
		return 20, true
	default:
		return SKTNumericGuestCode(key)
	}
}

// SKTNumericGuestCode maps the authenticated numeric keypad subset to the
// selected GVM guest codes.
func SKTNumericGuestCode(key profile.KeyCode) (uint16, bool) {
	switch key {
	case profile.Key1:
		return 1, true
	case profile.Key2:
		return 2, true
	case profile.Key3:
		return 3, true
	case profile.Key4:
		return 4, true
	case profile.Key5:
		return 5, true
	case profile.Key6:
		return 6, true
	case profile.Key7:
		return 7, true
	case profile.Key8:
		return 8, true
	case profile.Key9:
		return 9, true
	case profile.Key0:
		return 10, true
	case profile.KeyAsterisk:
		return 11, true
	case profile.KeyPound:
		return 12, true
	default:
		return 0, false
	}
}
