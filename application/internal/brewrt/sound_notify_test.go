package brewrt

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func soundNotifyRuntime(t *testing.T) *Runtime {
	t.Helper()
	module := make([]byte, 32)
	for i, word := range []uint32{
		0xe5800004, // str r0, [r0, #4]: callback context
		0xe5801008, // str r1, [r0, #8]: command
		0xe580200c, // str r2, [r0, #12]: status
		0xe5803010, // str r3, [r0, #16]: optional data
		0xe5901000, // ldr r1, [r0]
		0xe2811001, // add r1, r1, #1
		0xe5801000, // str r1, [r0]: callback count
		0xe12fff1e, // bx lr
	} {
		binary.LittleEndian.PutUint32(module[i*4:], word)
	}
	r, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	invokeSoundPlayerMethod(t, r, 2, moduleBase, outputAddr)
	if err := r.replaceSoundPlayerSource("audio/wav", brewTestWave(make([]int16, 800))); err != nil {
		t.Fatal(err)
	}
	return r
}

func invokeSoundPlayerMethod(t *testing.T, r *Runtime, slot, arg1, arg2 uint32) {
	t.Helper()
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR0: soundPlayerObject, cpu.RegisterR1: arg1,
		cpu.RegisterR2: arg2, cpu.RegisterLR: returnTrap | 1,
	} {
		if err := r.cpu.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	if handled, _, _, err := r.handleAppletMethodTrap(soundPlayerTrapBase + slot*2 + 2); err != nil || !handled {
		t.Fatalf("sound player slot %d: handled=%v err=%v", slot, handled, err)
	}
}

func checkSoundNotification(t *testing.T, r *Runtime, count, status uint32) {
	t.Helper()
	var encoded [20]byte
	if err := r.cpu.ReadMemory(outputAddr, encoded[:]); err != nil {
		t.Fatal(err)
	}
	var got [5]uint32
	for i := range got {
		got[i] = binary.LittleEndian.Uint32(encoded[i*4:])
	}
	want := [5]uint32{count, outputAddr, 1, status, 0}
	if count == 0 {
		want = [5]uint32{}
	}
	if got != want {
		t.Fatalf("sound notification = %x, want %x", got, want)
	}
}

func TestSoundPlayerNotifiesAcceptedPlayAndNaturalCompletion(t *testing.T) {
	r := soundNotifyRuntime(t)
	invokeSoundPlayerMethod(t, r, 4, 0, 0)
	checkSoundNotification(t, r, 0, 0)
	if err := r.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	checkSoundNotification(t, r, 1, 1) // AEE_SOUNDPLAYER_SUCCESS
	if err := r.RunCallbacks(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	checkSoundNotification(t, r, 2, 14) // AEE_SOUNDPLAYER_DONE
	if err := r.RunCallbacks(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	checkSoundNotification(t, r, 2, 14)
}

func TestSoundPlayerNotifiesStoppedAndReplacedPlayback(t *testing.T) {
	for _, replace := range []bool{false, true} {
		r := soundNotifyRuntime(t)
		invokeSoundPlayerMethod(t, r, 4, 0, 0)
		if err := r.RunCallbacks(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		if replace {
			if err := r.replaceSoundPlayerSource("audio/wav", brewTestWave(make([]int16, 800))); err != nil {
				t.Fatal(err)
			}
		} else {
			invokeSoundPlayerMethod(t, r, 5, 0, 0)
			invokeSoundPlayerMethod(t, r, 5, 0, 0) // Stop on an idle player adds no abort.
		}
		checkSoundNotification(t, r, 1, 1)
		if err := r.RunCallbacks(context.Background(), time.Second); err != nil {
			t.Fatal(err)
		}
		checkSoundNotification(t, r, 2, 12) // AEE_SOUNDPLAYER_ABORTED
	}
}

func TestSoundPlayerDropsNotificationsForReplacedRegistration(t *testing.T) {
	r := soundNotifyRuntime(t)
	invokeSoundPlayerMethod(t, r, 4, 0, 0)
	invokeSoundPlayerMethod(t, r, 2, 0, 0)
	// Re-register the same address: pending work belongs to the old registration.
	invokeSoundPlayerMethod(t, r, 2, moduleBase, outputAddr)
	if err := r.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	checkSoundNotification(t, r, 0, 0)
}

func TestSoundPlayerCompletionCanReplayWithoutRecursiveNotification(t *testing.T) {
	r := soundNotifyRuntime(t)
	// Continue after recording the callback. Only DONE starts another play.
	var encoded [40]byte
	for i, word := range []uint32{
		0xe352000e, // cmp r2, #14
		0x112fff1e, // bxne lr
		0xe92d4010, // push {r4, lr}
		0xe59f000c, // ldr r0, [pc, #12]
		0xe59f300c, // ldr r3, [pc, #12]
		0xe12fff33, // blx r3
		0xe8bd4010, // pop {r4, lr}
		0xe12fff1e, // bx lr
		soundPlayerObject, soundPlayerTrapBase + 4*2 | 1,
	} {
		binary.LittleEndian.PutUint32(encoded[i*4:], word)
	}
	if err := r.cpu.WriteMemory(moduleBase+28, encoded[:]); err != nil {
		t.Fatal(err)
	}
	invokeSoundPlayerMethod(t, r, 4, 0, 0)
	for _, step := range []struct {
		elapsed       time.Duration
		count, status uint32
	}{{0, 1, 1}, {100 * time.Millisecond, 2, 14}, {0, 3, 1}, {100 * time.Millisecond, 4, 14}} {
		if err := r.RunCallbacks(context.Background(), step.elapsed); err != nil {
			t.Fatal(err)
		}
		checkSoundNotification(t, r, step.count, step.status)
		if info, err := r.media.Info(brewMediaOwner, r.soundPlayer.clip); err != nil || info.State != shared.ClipPlaying {
			t.Fatalf("callback did not restart sound: info=%+v err=%v", info, err)
		}
	}
}

func TestSoundPlayerReportsUndecodablePlaybackAsFailure(t *testing.T) {
	r := soundNotifyRuntime(t)
	if err := r.replaceSoundPlayerSource("audio/wav", []byte("undecodable input")); err != nil {
		t.Fatal(err)
	}
	invokeSoundPlayerMethod(t, r, 4, 0, 0)
	if err := r.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	checkSoundNotification(t, r, 1, 15) // AEE_SOUNDPLAYER_FAILURE, not SUCCESS.
}
