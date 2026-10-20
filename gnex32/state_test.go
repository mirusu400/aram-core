package gnex32

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	stdimage "image"
	"image/color"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32RuntimeStateRoundTripPreservesCompleteState(t *testing.T) {
	image := gnex32StateTestImage()
	runtime, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	runtime.vm.pc = 2
	runtime.vm.calls = []int{4}
	runtime.vm.marks = []int{1}
	runtime.vm.stack = []uint32{0x12345678, 0xabcdef01}
	runtime.vm.refs[1] = gnex32SymbolRef{symbol: 0, element: 1}
	runtime.vm.halted = true
	runtime.vm.fault = errors.New("saved fault sentinel")
	if err := runtime.vm.memory.WriteWord(0, 1, 0xfeedface); err != nil {
		t.Fatal(err)
	}
	if err := runtime.vm.media.Replace(0, []byte("mutable guest media")); err != nil {
		t.Fatal(err)
	}
	runtime.display.Clear(color.RGBA{R: 1, G: 2, B: 3, A: 255})
	runtime.display.SetClip(2, 3, 40, 50)
	runtime.display.SaveLCD()
	runtime.display.Flush()
	runtime.display.FillRect(4, 5, 6, 7, color.RGBA{R: 200, A: 255})
	if err := runtime.display.SetGamma(5); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setBacklight(BacklightOff); err != nil {
		t.Fatal(err)
	}
	runtime.keyToneOn = false
	runtime.tick = 77
	runtime.timers = [3]runtimeTimer{{due: 1234, period: 9, repeat: true, active: true}}
	runtime.presents = 33
	runtime.nv = [UserNVWords]uint32{1, 2, 3, 0xffffffff}
	runtime.randomState = 0x76543210
	runtime.paint = color.RGBA{R: 10, G: 20, B: 30, A: 255}
	runtime.paintSource = runtime.paint
	runtime.paintMapped = false
	runtime.fontSize = 4
	runtime.fontColor = monoColor(3)
	runtime.fontIndex = 3
	runtime.fontAlign = 2
	runtime.fontBack = monoColor(4)
	runtime.fontBackIdx = 4
	if err := runtime.playSound(1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.playKeyTone(5); err != nil {
		t.Fatal(err)
	}
	if err := runtime.services.AdvanceFrame(runtime.owner); err != nil {
		t.Fatal(err)
	}

	want, err := runtime.snapshotState()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	got, err := restored.snapshotState()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored runtime state differs\n got: %#v\nwant: %#v", got, want)
	}
	if restored.vm.service == nil || restored.vm.copyImage == nil || restored.vm.copyImageDir == nil || restored.vm.copyImageEx == nil {
		t.Fatal("runtime callbacks were not rebound after restore")
	}
}

func TestGNEX32RuntimeStateRejectsCorruptionAtomically(t *testing.T) {
	image := gnex32StateTestImage()
	runtime, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.nv[0] = 99
	runtime.randomState = 123
	valid, err := runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	corrupt := bytes.Clone(valid)
	corrupt[len(gnex32RuntimeStateMagic)+8] ^= 0x80
	wrongVersion := bytes.Clone(valid)
	binary.LittleEndian.PutUint32(wrongVersion[len(gnex32RuntimeStateMagic):], gnex32RuntimeStateVersion+1)
	checksum := sha256.Sum256(wrongVersion[:len(wrongVersion)-sha256.Size])
	copy(wrongVersion[len(wrongVersion)-sha256.Size:], checksum[:])
	for name, state := range map[string][]byte{
		"truncated":     valid[:len(valid)-1],
		"checksum":      corrupt,
		"wrong version": wrongVersion,
		"trailing":      append(bytes.Clone(valid), 0),
	} {
		t.Run(name, func(t *testing.T) {
			before, err := runtime.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.UnmarshalBinary(state); err == nil {
				t.Fatal("accepted invalid runtime state")
			}
			after, err := runtime.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("failed load mutated runtime state")
			}
		})
	}

	otherImage := image
	otherImage.Symbols = append(otherImage.Symbols, gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)})
	other, err := NewRuntime(otherImage, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.UnmarshalBinary(valid); err == nil {
		t.Fatal("accepted runtime state with a different image shape")
	}
}

func TestGNEX32MemoryRestoreIsAtomic(t *testing.T) {
	image := gnex32StateTestImage()
	symbols, err := image.NewSymbolMemory()
	if err != nil {
		t.Fatal(err)
	}
	beforeSymbols := symbols.Snapshot()
	badSymbols := symbols.Snapshot()
	badSymbols.Symbols[0] = badSymbols.Symbols[0][:1]
	if err := symbols.Restore(badSymbols); err == nil || !reflect.DeepEqual(symbols.Snapshot(), beforeSymbols) {
		t.Fatalf("invalid symbol restore = %v", err)
	}

	media, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	beforeMedia := media.Snapshot()
	badMedia := media.Snapshot()
	badMedia.Resources[0].Flags = 0x200
	if err := media.Restore(badMedia); err == nil || !reflect.DeepEqual(media.Snapshot(), beforeMedia) {
		t.Fatalf("invalid media restore = %v", err)
	}
}

func TestGNEX32SaveLoadReplayDeterminism(t *testing.T) {
	image := gnex32StateTestImage()
	runtime, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.playSound(1); err != nil {
		t.Fatal(err)
	}
	saved, err := runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		frame, audio, state [sha256.Size]byte
		start               time.Duration
		revision            uint64
		time                [4]uint32
		nv                  [UserNVWords]uint32
		tick                uint64
		timerActive         bool
	}
	exercise := func() result {
		if err := runtime.KeyPressed(0x14, 1); err != nil {
			t.Fatal(err)
		}
		if err := runtime.KeyPressed(0xff, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.service(0x95, []uint32{16, 0}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.StepFrame(1); err != nil {
			t.Fatal(err)
		}
		if err := runtime.vm.memory.WriteWord(1, 0, 0x55667788); err != nil {
			t.Fatal(err)
		}
		runtime.vm.refs[0] = gnex32SymbolRef{symbol: 1}
		if _, err := runtime.service(0x94, []uint32{0, 1}); err != nil {
			t.Fatal(err)
		}
		value, err := runtime.service(0xc9, []uint32{10, 1000})
		if err != nil {
			t.Fatal(err)
		}
		runtime.display.FillRect(1, 1, 3, 3, color.RGBA{R: byte(value.Push[0]), A: 255})
		frame := runtime.display.Flush().(*stdimage.RGBA)
		runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
		if _, err := runtime.service(5, []uint32{0}); err != nil {
			t.Fatal(err)
		}
		timeWords := gnex32TestSymbolWords(t, runtime, 0, 4)
		audio, start, revision := runtime.DrainTimedAudio()
		pcm := make([]byte, len(audio.PCM16)*2)
		for index, sample := range audio.PCM16 {
			binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
		}
		state, err := runtime.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		return result{
			frame: sha256.Sum256(frame.Pix), audio: sha256.Sum256(pcm), state: sha256.Sum256(state),
			start: start, revision: revision,
			time: [4]uint32{timeWords[0], timeWords[1], timeWords[2], timeWords[3]},
			nv:   runtime.CaptureUserNV(), tick: runtime.tick, timerActive: runtime.timers[0].active,
		}
	}

	first := exercise()
	if err := runtime.UnmarshalBinary(saved); err != nil {
		t.Fatal(err)
	}
	second := exercise()
	if second.revision != first.revision+1 {
		t.Fatalf("restored audio revision = %d, want %d", second.revision, first.revision+1)
	}
	first.revision, second.revision = 0, 0
	if first != second {
		t.Fatalf("save/load replay diverged\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func gnex32StateTestImage() gnex.GNEX32Image {
	image := gnex32AudioTestImage([][]byte{[]byte("initial"), gnex32TestWave()})
	image.Code = []byte{0x86, 0, 0x86, 0, 0x86, 0}
	image.Symbols[0].Words = 4
	image.Symbols[0].Data = make([]byte, 16)
	image.Media[0].Flags = 1
	image.CodePointers[2] = image.Entry
	image.CodePointers[3] = image.Entry
	return image
}
