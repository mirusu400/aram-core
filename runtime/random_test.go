package runtime

import "testing"

// Characterization tests locking the observed Java-compatible RNG behavior
// before the Random code moves from clock.go to random.go. Golden values were
// produced by the pre-move implementation.

func TestJavaRandomSeedCharacterization(t *testing.T) {
	cases := []struct {
		seed int64
		want uint64
	}{
		{0, 0x5deece66d},
		{1, 0x5deece66c},
		{-1, 0xfffa21131992},
		{0x5DEECE66D, 0x0},
	}
	for _, c := range cases {
		if got := JavaRandomSeed(c.seed); got != c.want {
			t.Errorf("JavaRandomSeed(%d) = %#x, want %#x", c.seed, got, c.want)
		}
	}
}

func TestJavaRandomBitsCharacterization(t *testing.T) {
	state := JavaRandomSeed(42)
	want := []uint32{0xba419d35, 0x0dfe8af7, 0xaee7bbe1, 0x0c45c028}
	for i, w := range want {
		got, err := JavaRandomBits(&state, 32)
		if err != nil {
			t.Fatalf("JavaRandomBits step %d: %v", i, err)
		}
		if got != w {
			t.Errorf("JavaRandomBits step %d = %#x, want %#x", i, got, w)
		}
	}
}

func TestJavaIntStreamCharacterization(t *testing.T) {
	random := NewRandom(0x1234, 4)
	want := []int32{-1155484576, -723955400, 1033096058, -1690734402}
	for i, w := range want {
		got, err := random.JavaInt("chan")
		if err != nil {
			t.Fatalf("JavaInt step %d: %v", i, err)
		}
		if got != w {
			t.Errorf("JavaInt step %d = %d, want %d", i, got, w)
		}
	}
}

func TestJavaBitsWidthCharacterization(t *testing.T) {
	random := NewRandom(0x1234, 4)
	b10, err := random.JavaBits("chan", 10)
	if err != nil {
		t.Fatalf("JavaBits(10): %v", err)
	}
	b1, err := random.JavaBits("chan", 1)
	if err != nil {
		t.Fatalf("JavaBits(1): %v", err)
	}
	if b10 != 748 || b1 != 1 {
		t.Errorf("JavaBits sequence = %d, %d, want 748, 1", b10, b1)
	}
}
