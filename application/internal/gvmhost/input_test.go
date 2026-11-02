package gvmhost

import (
	"testing"

	"github.com/mirusu400/aram-core/profile"
)

func TestSKTNumericGuestCode(t *testing.T) {
	tests := []struct {
		key  profile.KeyCode
		want uint16
	}{
		{profile.Key1, 1}, {profile.Key2, 2}, {profile.Key3, 3},
		{profile.Key4, 4}, {profile.Key5, 5}, {profile.Key6, 6},
		{profile.Key7, 7}, {profile.Key8, 8}, {profile.Key9, 9},
		{profile.Key0, 10}, {profile.KeyAsterisk, 11}, {profile.KeyPound, 12},
	}
	for _, test := range tests {
		got, ok := SKTNumericGuestCode(test.key)
		if !ok || got != test.want {
			t.Fatalf("SKTNumericGuestCode(%d) = (%d,%v), want (%d,true)", test.key, got, ok, test.want)
		}
	}
	for _, key := range []profile.KeyCode{profile.KeyInvalid, profile.KeyUp, profile.KeySelect, profile.KeySoft1} {
		if got, ok := SKTNumericGuestCode(key); ok {
			t.Fatalf("SKTNumericGuestCode(%d) = (%d,true)", key, got)
		}
	}
}

func TestSKTDirectGuestCode(t *testing.T) {
	tests := []struct {
		key  profile.KeyCode
		want uint16
	}{
		{profile.KeySoft1, 13},
		// Titles label guest code 13 as CLR ("(OK/CLR)", "CLR EXIT") and
		// treat it as cancel, so the clear/back key reaches it too.
		{profile.KeyClear, 13},
		// Titles ask for "send키" where code 14 opens their WAP pages.
		{profile.KeySend, 14},
		{profile.KeyUp, 16},
		{profile.KeyDown, 17},
		{profile.KeyLeft, 18},
		{profile.KeyRight, 19},
		{profile.KeySelect, 20},
	}
	for _, test := range tests {
		got, ok := SKTGuestCode(test.key)
		if !ok || got != test.want {
			t.Fatalf("SKTGuestCode(%d) = (%d,%v), want (%d,true)", test.key, got, ok, test.want)
		}
	}
	// END is the machine's script close, not a guest code.
	for _, key := range []profile.KeyCode{profile.KeySoft2, profile.KeySoft3, profile.KeyEnd} {
		if got, ok := SKTGuestCode(key); ok {
			t.Fatalf("SKTGuestCode(%d) = (%d,true)", key, got)
		}
	}
}
