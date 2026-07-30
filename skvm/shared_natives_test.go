package skvm

import (
	"bytes"
	"context"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func invokeTestNative(
	t *testing.T,
	vm *VM,
	class, name, descriptor string,
	receiver uint32,
	args ...Value,
) Value {
	t.Helper()
	native := vm.natives[nativeKey{
		class: class, name: name, descriptor: descriptor,
	}]
	if native == nil {
		t.Fatalf("native %s.%s%s is missing", class, name, descriptor)
	}
	value, _, err := native(context.Background(), vm, receiver, args)
	if err != nil {
		t.Fatalf("native %s.%s%s: %v", class, name, descriptor, err)
	}
	return value
}

func TestSKVMFileNativesUseSharedStorage(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	name := vm.NewString("save/game.dat")
	file := vm.NewObject("com/xce/io/XFile", nil)
	invokeTestNative(
		t,
		vm,
		"com/xce/io/XFile",
		"<init>",
		"(Ljava/lang/String;I)V",
		file,
		ReferenceValue(name),
		IntValue(1),
	)
	data := vm.NewByteArray([]byte("persistent"))
	invokeTestNative(
		t,
		vm,
		"com/xce/io/XFile",
		"write",
		"([BII)I",
		file,
		ReferenceValue(data),
		IntValue(0),
		IntValue(10),
	)
	stored, err := vm.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save/game.dat",
	)
	if err != nil || !bytes.Equal(stored, []byte("persistent")) {
		t.Fatalf("shared file = %q, %v", stored, err)
	}
	state, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/save/game.dat",
		[]byte("changed"),
	); err != nil {
		t.Fatal(err)
	}
	if err := vm.UnmarshalBinary(state); err != nil {
		t.Fatal(err)
	}
	stored, err = vm.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save/game.dat",
	)
	if err != nil || !bytes.Equal(stored, []byte("persistent")) {
		t.Fatalf("restored shared file = %q, %v", stored, err)
	}
}

func TestSKVMDeviceAndAudioNativesUseSharedServices(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Device",
		"setBacklightEnabled",
		"(Z)V",
		0,
		IntValue(1),
	)
	enabled, _ := vm.services.Device.Backlight()
	if !enabled {
		t.Fatal("SKVM backlight native did not update the device service")
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Vibration",
		"start",
		"(II)V",
		0,
		IntValue(75),
		IntValue(500),
	)
	level, until := vm.services.Device.Vibration()
	if level != 75 || until != 500*time.Millisecond {
		t.Fatalf("vibration = %d until %s", level, until)
	}
	target := vm.NewString("https://example.invalid/")
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Device",
		"invokeWapBrowser",
		"(Ljava/lang/String;)V",
		0,
		ReferenceValue(target),
	)
	requests := vm.services.Device.Requests()
	if len(requests) != 1 || requests[0].Kind != shared.RequestBrowser {
		t.Fatalf("device requests = %+v", requests)
	}

	name := vm.NewString("tone.wav")
	clipValue := invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioSystem",
		"getAudioClip",
		"(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		0,
		ReferenceValue(name),
	)
	clipReference, err := clipValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"play",
		"()V",
		clipReference,
	)
	clip, err := vm.audioClip(clipReference)
	if err != nil {
		t.Fatal(err)
	}
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	if err != nil || info.State != shared.ClipPlaying {
		t.Fatalf("shared audio clip = %+v, %v", info, err)
	}
}

func TestSKVMAudioClipCanReopenAfterClose(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	name := vm.NewString("tone.wav")
	clipValue := invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioSystem",
		"getAudioClip",
		"(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		0,
		ReferenceValue(name),
	)
	clipReference, err := clipValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	clip, err := vm.audioClip(clipReference)
	if err != nil {
		t.Fatal(err)
	}
	closedService := clip.clip
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"close",
		"()V",
		clipReference,
	)
	if clip.clip != 0 {
		t.Fatalf("closed clip service = %s, want zero", clip.clip)
	}

	payload := []byte("reopened")
	data := vm.NewByteArray(payload)
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"open",
		"([BII)V",
		clipReference,
		ReferenceValue(data),
		IntValue(0),
		IntValue(int32(len(payload))),
	)
	if clip.clip == 0 || clip.clip == closedService {
		t.Fatalf("reopened clip service = %s, closed service = %s", clip.clip, closedService)
	}
	source, err := vm.services.Media.Source(vm.serviceOwner, clip.clip)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, payload) {
		t.Fatalf("reopened clip source = %q, want %q", source, payload)
	}
}
