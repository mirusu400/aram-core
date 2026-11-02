package runtime

import "testing"

func TestSKTGammaColorPhaseZeroIsTheSteadyRow(t *testing.T) {
	for gamma := uint8(0); gamma <= 6; gamma++ {
		for index := 0; index <= 181; index++ {
			want, wantOK := SKTGammaColor(gamma, uint8(index))
			got, ok := SKTGammaColorPhase(gamma, uint8(index), 0)
			if got != want || ok != wantOK {
				t.Fatalf("gamma %d index %d phase 0 = %02x,%v want %02x,%v", gamma, index, got, ok, want, wantOK)
			}
		}
	}
}

func TestSKTGammaColorPhaseBlinks(t *testing.T) {
	for _, tc := range []struct {
		gamma, index, phase uint8
		want                byte
	}{
		{3, 5, 1, 0x92}, {3, 5, 2, 0xff}, // white/light-gray blink
		{3, 140, 1, 0xe0}, {3, 140, 2, 0x00}, // red/black blink
		{3, 158, 3, 0xe0}, {3, 158, 5, 0x40}, // red pulse
		{3, 100, 4, 0},    // steady colors stay put
		{3, 4, 3, 0},      // transparent
		{4, 141, 1, 0x49}, // gamma curve applies to the phase row
	} {
		steady, _ := SKTGammaColor(tc.gamma, tc.index)
		if tc.index == 100 {
			tc.want = steady
		}
		got, ok := SKTGammaColorPhase(tc.gamma, tc.index, tc.phase)
		if !ok || got != tc.want {
			t.Errorf("gamma %d index %d phase %d = %02x,%v want %02x", tc.gamma, tc.index, tc.phase, got, ok, tc.want)
		}
	}
	if _, ok := SKTGammaColorPhase(3, 5, SKTPalettePhases); ok {
		t.Fatal("phase past the table accepted")
	}
	if _, ok := SKTGammaColorPhase(3, 182, 1); ok {
		t.Fatal("selector past the row accepted")
	}
}
