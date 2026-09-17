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

func TestCommonHelperContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	call := func(slot uint32, r0, r1, r2 uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: r0, cpu.RegisterR1: r1, cpu.RegisterR2: r2,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + slot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("helper slot %d handled=%v err=%v", slot, handled, err)
		}
		value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	source, destination := heapBase+0x100, heapBase+0x200
	if err := runtime.cpu.WriteMemory(source, []byte("ab\x00tail")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrncpySlot, destination, source, 6); got != destination {
		t.Fatalf("strncpy return = 0x%08x, want destination", got)
	}
	copied := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(destination, copied); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, []byte{'a', 'b', 0, 0, 0, 0}) {
		t.Fatalf("strncpy bytes = %v", copied)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("ac\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStrncmpSlot, source, destination, 3)); got != -1 {
		t.Fatalf("strncmp result = %d, want -1", got)
	}
	if err := runtime.cpu.WriteMemory(source, []byte("Prefix/Needle/Suffix\x00")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("needle\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStricmpSlot, destination, heapBase+0x400, 0)); got == 0 {
		t.Fatal("stricmp treated different strings as equal")
	}
	if err := runtime.cpu.WriteMemory(heapBase+0x400, []byte("NEEDLE\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStricmpSlot, destination, heapBase+0x400, 0)); got != 0 {
		t.Fatalf("stricmp result = %d, want equal", got)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("Needle\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrstrSlot, source, destination, 0); got != source+7 {
		t.Fatalf("strstr result = 0x%08x, want 0x%08x", got, source+7)
	}
	if err := runtime.cpu.WriteMemory(heapBase+0x500, []byte(" \t-123tail\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperAtoiSlot, heapBase+0x500, 0, 0)); got != -123 {
		t.Fatalf("atoi result = %d, want -123", got)
	}

	randomAt := heapBase + 0x300
	if got := call(helperGetRandSlot, randomAt, 4, 0); got != 0 {
		t.Fatalf("GetRand result = %d, want success", got)
	}
	random := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(randomAt, random); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(random, make([]byte, 4)) {
		t.Fatal("GetRand left the destination unchanged")
	}

	if err := runtime.RunCallbacks(context.Background(), 2500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := call(helperGetUpTimeMSSlot, 0, 0, 0); got != 2500 {
		t.Fatalf("GetUpTimeMS = %d, want 2500", got)
	}
	if got := call(helperGetSecondsSlot, 0, 0, 0); got != 630_720_002 {
		t.Fatalf("GetSeconds = %d, want deterministic calendar time", got)
	}
}

func TestLegacySoundAndActiveAppletContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.activeApplet = heapBase + 0x900
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 8*2 + 2)
	if err != nil || !handled {
		t.Fatalf("ActiveApplet handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != runtime.activeApplet {
		t.Fatalf("ActiveApplet = 0x%08x err=%v", got, err)
	}

	out := heapBase + 0xa00
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: Sound10ClassID,
		cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != soundObject {
		t.Fatalf("Sound10 object = 0x%08x, want 0x%08x", got, soundObject)
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
