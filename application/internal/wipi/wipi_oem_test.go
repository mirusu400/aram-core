package wipi

import (
	"encoding/binary"
	"image"
	"testing"
	"time"
)

func TestOEMTextUsesSharedFontAndClip(t *testing.T) {
	runtime := newPublicRuntime(t)
	pixels, err := runtime.Heap.Allocate(32*16*4, true)
	check(t, err)
	clip := image.Rect(4, 3, 20, 12)
	width, err := runtime.OEMText(pixels, 32, 16, 0, 0, 0xffffff, []byte("TEST"), 12, clip)
	check(t, err)
	if width <= 0 {
		t.Fatal("OEM font measured empty text")
	}
	var raw [32 * 16 * 4]byte
	check(t, runtime.CPU.ReadMemory(pixels, raw[:]))
	drawn := false
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			value := binary.LittleEndian.Uint32(raw[(y*32+x)*4:])
			if value != 0 {
				drawn = true
				if !image.Pt(x, y).In(clip) {
					t.Fatal("OEM text escaped its clip")
				}
			}
		}
	}
	if !drawn {
		t.Fatal("OEM text did not draw any glyph")
	}
}

func TestOEMMusicUsesSharedSMAFAndReleasesReplacement(t *testing.T) {
	runtime := newPublicRuntime(t)
	chunk := func(id string, body []byte) []byte {
		data := make([]byte, 8)
		copy(data, id)
		binary.BigEndian.PutUint32(data[4:], uint32(len(body)))
		return append(data, body...)
	}
	track := append([]byte{2, 0, 2, 2}, make([]byte, 16)...)
	track = append(track, chunk("Mtsq", []byte{0, 0x90, 60, 110, 25, 25, 0xff, 0x2f})...)
	data := chunk("MMMD", chunk("MTR\x00", track))
	source, err := runtime.Heap.Allocate(uint32(len(data)), true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(source, data))
	handle, err := runtime.OEMPlaySMAF(0, source)
	check(t, err)
	if handle == 0 || !runtime.OEMClipPlaying(handle) {
		t.Fatal("OEM SMAF did not start")
	}
	check(t, runtime.Services.Media.Advance(0, 40*time.Millisecond, runtime.Services.Events))
	pcm := runtime.Services.Media.Drain().PCM16
	nonzero := false
	for _, sample := range pcm {
		if sample != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("OEM SMAF produced no audible samples")
	}
	replacement, err := runtime.OEMPlaySMAF(handle, source)
	check(t, err)
	if replacement == 0 || len(runtime.MediaClips) != 1 {
		t.Fatal("replacing OEM music leaked its clip")
	}
	runtime.OEMStopClip(replacement)
	if len(runtime.MediaClips) != 0 || runtime.OEMClipPlaying(replacement) {
		t.Fatal("stopped OEM clip remained active")
	}
}

func TestOEMFilesUseSharedStorage(t *testing.T) {
	runtime := newPublicRuntime(t)
	name, err := runtime.Heap.Allocate(32, true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(name, []byte("native-save.dat\x00")))
	payload, err := runtime.Heap.Allocate(8, true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(payload, []byte{1, 2, 3, 4}))
	call := func(slot uint32, args ...uint32) uint32 {
		t.Helper()
		value, err := runtime.OEMFile(slot, args)
		check(t, err)
		return value
	}
	if call(0x48, name) != 0 || call(0x10, name, 0) != 0 {
		t.Fatal("missing OEM file was reported as present")
	}
	handle := call(0x10, name, 2)
	if handle == 0 {
		t.Fatal("OEM create did not return a handle")
	}
	if call(0x1c, handle, payload, 4) != 4 || call(0x28, handle, 0, 2) != 4 || call(0x28, handle, 0, 0) != 0 {
		t.Fatal("OEM write/seek failed")
	}
	if call(0x18, handle, payload+4, 4) != 4 {
		t.Fatal("OEM read failed")
	}
	var actual [4]byte
	check(t, runtime.CPU.ReadMemory(payload+4, actual[:]))
	if actual != [4]byte{1, 2, 3, 4} {
		t.Fatalf("OEM file content=%v", actual)
	}
	call(0x14, handle)
	if call(0x48, name) != 1 || len(runtime.Services.Storage.Snapshot().Files) != 1 {
		t.Fatal("OEM save bypassed shared storage")
	}
}
