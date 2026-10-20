package application

import (
	"testing"

	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/profile"
)

func TestGNEX32GuestCodeCoversMobileCKeySet(t *testing.T) {
	tests := []struct {
		key  profile.KeyCode
		want uint16
	}{
		{profile.Key1, gnex32.Key1}, {profile.Key2, gnex32.Key2},
		{profile.Key3, gnex32.Key3}, {profile.Key4, gnex32.Key4},
		{profile.Key5, gnex32.Key5}, {profile.Key6, gnex32.Key6},
		{profile.Key7, gnex32.Key7}, {profile.Key8, gnex32.Key8},
		{profile.Key9, gnex32.Key9}, {profile.Key0, gnex32.Key0},
		{profile.KeyAsterisk, gnex32.KeyAsterisk}, {profile.KeyPound, gnex32.KeyPound},
		{profile.KeyClear, gnex32.KeyClear}, {profile.KeySend, gnex32.KeySend},
		{profile.KeyEnd, gnex32.KeyEnd}, {profile.KeyLeft, gnex32.KeyLeft},
		{profile.KeyRight, gnex32.KeyRight}, {profile.KeyUp, gnex32.KeyUp},
		{profile.KeyDown, gnex32.KeyDown}, {profile.KeySelect, gnex32.KeyOK},
		{profile.KeySoft1, gnex32.KeySoft1}, {profile.KeySoft2, gnex32.KeySoft2},
	}
	for _, test := range tests {
		got, ok := gnex32GuestCode(test.key)
		if !ok || got != test.want {
			t.Errorf("gnex32GuestCode(%d) = (%#x, %v), want (%#x, true)", test.key, got, ok, test.want)
		}
	}
	for _, key := range []profile.KeyCode{
		profile.KeyInvalid, profile.KeySoft3, profile.KeyPower,
		profile.KeySideUp, profile.KeySideDown, profile.KeySideSel,
		profile.KeyFlipDown, profile.KeyFlipUp,
	} {
		if got, ok := gnex32GuestCode(key); ok {
			t.Errorf("gnex32GuestCode(%d) = (%#x, true), want unsupported", key, got)
		}
	}
}
