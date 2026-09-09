package skvm

import (
	"context"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestMIDPPlayerLifecycleUsesSharedMedia(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	data := synthesizeToneWAV(69, 100, 80)
	stream := vm.NewObject("java/io/ByteArrayInputStream", &inputStreamState{data: data})
	value := invokeTestNative(t, vm, "javax/microedition/media/Manager", "createPlayer", "(Ljava/io/InputStream;Ljava/lang/String;)Ljavax/microedition/media/Player;", 0,
		ReferenceValue(stream), ReferenceValue(vm.NewString("audio/wav")))
	player, err := value.Reference()
	check(t, err)
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/media/Player", "getState", "()I", player)); got != playerUnrealized {
		t.Fatalf("initial player state = %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/media/Player", "prefetch", "()V", player)
	invokeTestNative(t, vm, "javax/microedition/media/Player", "start", "()V", player)
	object, clip, err := vm.player(player)
	check(t, err)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.State != shared.ClipPlaying {
		t.Fatalf("shared clip state = %v", info.State)
	}
	if state, _ := object.Fields[playerStateField].Int(); state != playerStarted {
		t.Fatalf("Player state = %d", state)
	}
	check(t, vm.Advance(context.Background(), 200*time.Millisecond, nil))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/media/Player", "getState", "()I", player)); got != playerPrefetched {
		t.Fatalf("completed player state = %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/media/Player", "close", "()V", player)
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/media/Player", "getState", "()I", player)); got != playerClosed {
		t.Fatalf("closed player state = %d", got)
	}
}

func TestMIDPVolumeControlChangesClipGain(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	value, _, err := vm.newMIDPPlayer("audio/wav", synthesizeToneWAV(60, 20, 100))
	check(t, err)
	player, err := value.Reference()
	check(t, err)
	controlValue := invokeTestNative(t, vm, "javax/microedition/media/Player", "getControl", "(Ljava/lang/String;)Ljavax/microedition/media/Control;", player, ReferenceValue(vm.NewString("VolumeControl")))
	control, err := controlValue.Reference()
	check(t, err)
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/media/control/VolumeControl", "setLevel", "(I)I", control, IntValue(37))); got != 37 {
		t.Fatalf("setLevel returned %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/media/control/VolumeControl", "setMute", "(Z)V", control, IntValue(1))
	_, clip, err := vm.player(player)
	check(t, err)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.Volume != 37 || !info.Muted {
		t.Fatalf("clip gain = volume %d muted %v", info.Volume, info.Muted)
	}
}

func TestMIDPManagerPlayToneCreatesDecodableClip(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	invokeTestNative(t, vm, "javax/microedition/media/Manager", "playTone", "(III)V", 0, IntValue(69), IntValue(50), IntValue(75))
	value := vm.hostStatic[fieldStorageKey("javax/microedition/media/Manager", "$media.managerTone", "Ljavax/microedition/media/Player;")]
	player, err := value.Reference()
	check(t, err)
	_, clip, err := vm.player(player)
	check(t, err)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if !info.Decoded || info.State != shared.ClipPlaying {
		t.Fatalf("tone clip decoded=%v state=%v", info.Decoded, info.State)
	}
}

func TestMIDPToneSequenceRendersBlocksRepeatsAndSilence(t *testing.T) {
	sequence := []byte{
		0xfe, 1,
		0xfd, 30,
		0xfc, 64,
		0xfb, 1,
		60, 64,
		0xfa, 1,
		0xf9, 1,
		0xf7, 2, 62, 32,
		0xff, 64,
	}
	wav, err := renderToneSequence(sequence)
	check(t, err)
	const expectedSamples = 8000 * 3 / 2
	if len(wav) != 44+expectedSamples*2 {
		t.Fatalf("tone WAV length = %d, want %d", len(wav), 44+expectedSamples*2)
	}
	for _, sample := range wav[len(wav)-8000:] {
		if sample != 0 {
			t.Fatal("SILENCE event produced non-zero PCM")
		}
	}
}
