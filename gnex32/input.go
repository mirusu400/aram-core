package gnex32

// Mobile C key values delivered through EVENT_KEYPRESS/swData. Numeric keys
// occupy 1..12, followed by handset and navigation keys. A release reports the
// dedicated sentinel instead of the identity of the released key.
const (
	Key1 uint16 = iota + 1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	Key0
	KeyAsterisk
	KeyPound
	KeyClear
	KeySend
	KeyEnd
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyOK
	KeySoft1
	KeySoft2

	KeyRelease uint16 = 0xff
)

// ValidPhysicalKey reports whether code identifies a key that can be pressed.
// KeyRelease is an event sentinel and therefore is not a physical key.
func ValidPhysicalKey(code uint16) bool {
	return code >= Key1 && code <= KeySoft2
}
