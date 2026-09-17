package brewrt

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
	"time"

	"bytes"
	"golang.org/x/image/bmp"

	"github.com/mirusu400/aram-core/cpu"
	loaderbrew "github.com/mirusu400/aram-core/loader/brew"
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

func TestMatchAcceptsGenericSingleModulePackage(t *testing.T) {
	module := make([]byte, 8)
	binary.LittleEndian.PutUint32(module[0:], 0xe92d400c)
	mif := make([]byte, 64)
	for offset, value := range map[int]uint32{0: 0x00010011, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(mif[offset:], value)
	}
	binary.LittleEndian.PutUint32(mif[40:], 48)
	binary.LittleEndian.PutUint32(mif[44:], 64)
	binary.LittleEndian.PutUint32(mif[48:], 0x01023456)
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, data := range map[string][]byte{"game.mif": mif, "bin/game.mod": module} {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	pkg, matched, err := Match(archive.Bytes())
	if err != nil || !matched {
		t.Fatalf("generic archive matched=%v err=%v", matched, err)
	}
	if len(pkg.ClassIDs) != 1 || pkg.ClassIDs[0] != 0x01023456 || pkg.Splash != nil {
		t.Fatalf("generic package = %+v", pkg)
	}
}

func TestMIFApplicationClassIDRejectsUnboundedOrNonApplicationRecord(t *testing.T) {
	metadata := loaderbrew.Metadata{IndexOffset: 32, IndexCount: 1, DataOffset: 40, DataSize: 8}
	data := make([]byte, 48)
	binary.LittleEndian.PutUint32(data[32:], 40)
	binary.LittleEndian.PutUint32(data[36:], 48)
	binary.LittleEndian.PutUint32(data[40:], DisplayClassID)
	if _, ok := mifApplicationClassID(metadata, data); ok {
		t.Fatal("service ClassID was accepted as an application ClassID")
	}
	binary.LittleEndian.PutUint32(data[40:], 0x01023456)
	binary.LittleEndian.PutUint32(data[36:], 52)
	if _, ok := mifApplicationClassID(metadata, data); ok {
		t.Fatal("out-of-bounds final MIF record was accepted")
	}
}

func TestLookupGuestFileUsesUniqueRelativeSuffix(t *testing.T) {
	runtime := &Runtime{files: map[string][]byte{"1234/data.bin": {1, 2, 3}}}
	data, name, ok := runtime.lookupGuestFile("data.bin")
	if !ok || name != "1234/data.bin" || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("relative lookup data=%v name=%q ok=%v", data, name, ok)
	}
	runtime.files["other/data.bin"] = []byte{4}
	if _, _, ok := runtime.lookupGuestFile("data.bin"); ok {
		t.Fatal("ambiguous relative package path was accepted")
	}
}
