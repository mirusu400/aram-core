package brewrt

import (
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
	"time"

	"bytes"
	"golang.org/x/image/bmp"

	"github.com/mirusu400/aram-core/cpu"
)

func newSyntheticRuntime(t *testing.T) *Runtime {
	t.Helper()
	module := make([]byte, 20)
	for offset, instruction := range []uint32{0xe59f0008, 0xe5820000, 0xe3a00000, 0xe12fff1e, heapBase} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func TestRuntimeBootstrapsSyntheticARMModule(t *testing.T) {
	// ldr r0,[pc,#8]; str r0,[r2]; mov r0,#0; bx lr; .word heapBase
	module := make([]byte, 20)
	for offset, instruction := range []uint32{0xe59f0008, 0xe5820000, 0xe3a00000, 0xe12fff1e, heapBase} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap synthetic ARM module: %v", err)
	}
	if got := runtime.ModuleObject(); got != heapBase {
		t.Fatalf("module object = 0x%08x, want 0x%08x", got, heapBase)
	}
}

func TestDisplayUpdateCommitsDetachedBlackFramebuffer(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	black := make([]byte, framebufferBytes)
	if err := runtime.cpu.WriteMemory(framebufferBase, black); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(displayTrapBase + 7*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IDisplay Update handled=%v err=%v", handled, err)
	}
	if count, valid := runtime.FrameStats(); count != 1 || !valid {
		t.Fatalf("frame stats count=%d valid=%v, want 1/true", count, valid)
	}

	changed := append([]byte(nil), black...)
	changed[0], changed[1] = 0xff, 0xff
	if err := runtime.cpu.WriteMemory(framebufferBase, changed); err != nil {
		t.Fatal(err)
	}
	frame, presented, err := runtime.Framebuffer()
	if err != nil || !presented {
		t.Fatalf("committed framebuffer presented=%v err=%v", presented, err)
	}
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("uncommitted guest write leaked into frame: pixel=%#v", got)
	}
}

func TestRunCallbacksHonorsRequestedDelay(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.timers = []brewCallback{{function: returnTrap | 1, context: 7, remaining: 100 * time.Millisecond}}
	if err := runtime.RunCallbacks(context.Background(), 16*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(runtime.timers) != 1 || runtime.timers[0].remaining != 84*time.Millisecond {
		t.Fatalf("timers after 16ms = %#v, want one timer with 84ms remaining", runtime.timers)
	}
}

func TestDecodeSplashUsesEmbeddedBMPContract(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 120, 61))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 4), B: 0x55, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	mif := make([]byte, mifSplashOffset+encoded.Len())
	copy(mif[mifSplashOffset:], encoded.Bytes())
	decoded, err := decodeSplash(mif)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", decoded.Bounds(), source.Bounds())
	}
}

func TestMatchRejectsEveryUnauthenticatedArchive(t *testing.T) {
	if _, matched, err := Match([]byte("PK\x03\x04synthetic")); err != nil || matched {
		t.Fatalf("unauthenticated archive matched=%v err=%v", matched, err)
	}
}
