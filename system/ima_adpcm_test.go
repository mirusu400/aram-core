package system

import "testing"

func TestDecodeIMAADPCMBlock(t *testing.T) {
	// Header: predictor 100, step index 0 (step 7). Codes, low nibble first:
	// 0x7 (+7+3+1+0 = +11), 0xf (-11 at step index 8 -> step 16: 2+16+8+4 = -30),
	// then zeros, each adding step>>3 as the index decays.
	block := []byte{100, 0, 0, 0, 0xf7, 0x00, 0x00, 0x00}
	samples := decodeIMAADPCMBlock(block, 1)
	if len(samples) != 9 {
		t.Fatalf("decoded %d samples, want 9", len(samples))
	}
	if samples[0] != 100 || samples[1] != 111 || samples[2] != 81 {
		t.Fatalf("first samples = %v", samples[:3])
	}
	// A 256-byte mono block carries 505 samples, as the handset's clips do.
	if n := len(decodeIMAADPCMBlock(make([]byte, 256), 1)); n != 505 {
		t.Fatalf("256-byte block decodes to %d samples", n)
	}
	// Saturation at both rails and a clamped step index.
	loud := make([]byte, 4+252)
	loud[0], loud[1], loud[2] = 0xff, 0x7f, 200
	for index := 4; index < len(loud); index++ {
		loud[index] = 0x77
	}
	if out := decodeIMAADPCMBlock(loud, 1); out[len(out)-1] != 32767 {
		t.Fatalf("positive saturation = %d", out[len(out)-1])
	}
	for index := 4; index < len(loud); index++ {
		loud[index] = 0xff
	}
	if out := decodeIMAADPCMBlock(loud, 1); out[len(out)-1] != -32768 {
		t.Fatalf("negative saturation = %d", out[len(out)-1])
	}
	// Stereo interleaves per-channel headers and four-byte groups.
	stereo := []byte{10, 0, 0, 0, 0xec, 0xff, 0, 0, 0x07, 0, 0, 0, 0x00, 0, 0, 0}
	out := decodeIMAADPCMBlock(stereo, 2)
	if len(out) != 18 || out[0] != 10 || out[1] != -20 || out[2] != 21 || out[3] != -20 {
		t.Fatalf("stereo samples = %v", out)
	}
	if decodeIMAADPCMBlock([]byte{1, 2, 3}, 1) != nil || decodeIMAADPCMBlock(block, 3) != nil {
		t.Fatal("malformed block was decoded")
	}
}
