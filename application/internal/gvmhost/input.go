package gvmhost

import "github.com/mirusu400/aram-core/profile"

// SKTGuestCode maps the authenticated five-way pad and numeric keypad paths to
// the selected GVM guest codes. Stateful soft-key wrappers remain excluded.
func SKTGuestCode(key profile.KeyCode) (uint16, bool) {
	switch key {
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
