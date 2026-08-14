package guest

import (
	"strings"

	"github.com/mirusu400/aram-core/profile"
)

// InputKeyCode translates the frontend's backend-neutral control names to the
// signed WIPI handset key codes used by both the Java and C runtimes.
func InputKeyCode(control string) (profile.KeyCode, bool) {
	switch strings.ToLower(strings.TrimSpace(control)) {
	case "up":
		return profile.KeyUp, true
	case "down":
		return profile.KeyDown, true
	case "left":
		return profile.KeyLeft, true
	case "right":
		return profile.KeyRight, true
	case "ok", "fire", "select":
		return profile.KeySelect, true
	case "soft-left":
		return profile.KeySoft1, true
	case "soft-right":
		return profile.KeySoft2, true
	case "menu":
		return profile.KeySoft3, true
	case "back", "clear":
		return profile.KeyClear, true
	case "send", "call":
		return profile.KeySend, true
	case "end":
		return profile.KeyEnd, true
	case "star", "asterisk":
		return profile.KeyAsterisk, true
	case "hash", "pound":
		return profile.KeyPound, true
	case "num0", "0":
		return profile.Key0, true
	case "num1", "1":
		return profile.Key1, true
	case "num2", "2":
		return profile.Key2, true
	case "num3", "3":
		return profile.Key3, true
	case "num4", "4":
		return profile.Key4, true
	case "num5", "5":
		return profile.Key5, true
	case "num6", "6":
		return profile.Key6, true
	case "num7", "7":
		return profile.Key7, true
	case "num8", "8":
		return profile.Key8, true
	case "num9", "9":
		return profile.Key9, true
	default:
		return profile.KeyInvalid, false
	}
}
