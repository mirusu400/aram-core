package skvmhost

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The Java host must honour the handset font the factory selects instead of
// always rendering guest text with the default face, and a state round trip
// must not fall back to the default.
func TestJavaMachineHonoursFallbackFont(t *testing.T) {
	source := machinecore.Source{ProfileID: "j2me-1.0/generic/generic", SHA256: strings.Repeat("0", 64)}
	for _, test := range []struct{ requested, want string }{
		{"", "galmuri9"},
		{"mulmaru", "mulmaru"},
		{"neodgm", "neodgm"},
	} {
		m, err := newJavaMachine(context.Background(), source, Application{}, nil, image.Pt(240, 320), 0, 0, 0, false, test.requested)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.services.Config.FallbackFont; got != test.want {
			t.Errorf("fallback font %q: services use %q, want %q", test.requested, got, test.want)
		}
		var saved bytes.Buffer
		if err := m.SaveState(&saved); err != nil {
			t.Fatal(err)
		}
		if err := m.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		if got := m.services.Config.FallbackFont; got != test.want {
			t.Errorf("fallback font %q after state load: services use %q, want %q", test.requested, got, test.want)
		}
		m.Close()
	}
}
