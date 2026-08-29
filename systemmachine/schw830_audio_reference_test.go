package systemmachine

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/mirusu400/aram-core/loader/samsung"
)

const (
	schw830AudioIdleBudget    = uint64(60_000_000)
	schw830AudioKeyBudget     = uint64(5_000_000)
	schw830AudioSettleBudget  = uint64(50_000_000)
	schw830AudioSilenceBudget = uint64(10_000_000)
)

// TestSCHW830PrivateReferenceOutputsFirmwarePCM is opt-in because its input is
// a user-owned DL21 snapshot. It proves the complete native path without
// retaining firmware or decoded media: the real menu interaction publishes an
// MMMD descriptor, pulses the codec window, and produces frontend-ready PCM.
func TestSCHW830PrivateReferenceOutputsFirmwarePCM(t *testing.T) {
	machine := openSCHW830AudioReferenceMachine(
		t,
		"ARAM_SCHW830_AUDIO_SNAPSHOT_PREFIX",
	)
	runSCHW830Budget(t, machine, schw830AudioIdleBudget, "audio idle settle")
	_ = machine.DrainAudio()
	runSCHW830AudioInput(t, machine, "down")

	length, ok := machine.audio.readWord(schw830AudioSourceLengthAddress)
	if !ok || length < 12 {
		t.Fatalf("DL21 audio source length = %#x, readable=%t", length, ok)
	}
	address, ok := machine.audio.readWord(schw830AudioSourcePointerAddress)
	if !ok {
		t.Fatal("DL21 audio source address is unreadable")
	}
	header := make([]byte, 8)
	if err := readSCHW830GuestBlock(machine.bus, address, header); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(header[:4], []byte("MMMD")) ||
		binary.BigEndian.Uint32(header[4:])+8 != length {
		t.Fatalf("DL21 audio descriptor length=%#x header=%x", length, header)
	}
	assertSCHW830FirmwareGain(t, machine, 7, 0, 100, false)

	chunk := machine.DrainAudio()
	if err := chunk.Validate(); err != nil {
		t.Fatal(err)
	}
	if chunk.SampleRate != 44_100 || chunk.Channels != 2 ||
		len(chunk.PCM16) == 0 || schw830AudioPeak(chunk.PCM16) == 0 {
		t.Fatalf(
			"DL21 firmware PCM rate=%d channels=%d samples=%d peak=%d",
			chunk.SampleRate,
			chunk.Channels,
			len(chunk.PCM16),
			schw830AudioPeak(chunk.PCM16),
		)
	}
}

// TestSCHW830PrivateReferenceAppliesFirmwareVolume starts on the sound-volume
// row and uses the original left-key handler. DL21 stores 6 after lowering its
// seven-step setting, and the mixer must expose the corresponding 85% gain.
func TestSCHW830PrivateReferenceAppliesFirmwareVolume(t *testing.T) {
	machine := openSCHW830AudioReferenceMachine(
		t,
		"ARAM_SCHW830_AUDIO_VOLUME_SNAPSHOT_PREFIX",
	)
	runSCHW830Budget(t, machine, schw830AudioIdleBudget, "volume idle settle")
	_ = machine.DrainAudio()
	runSCHW830AudioInput(t, machine, "left")
	assertSCHW830FirmwareGain(t, machine, 6, 0, 85, false)
	if chunk := machine.DrainAudio(); len(chunk.PCM16) == 0 || schw830AudioPeak(chunk.PCM16) == 0 {
		t.Fatal("lowered DL21 firmware volume produced no PCM")
	}
}

// TestSCHW830PrivateReferenceMutesFirmwarePCM follows the original ring-mode
// handler from ringtone (0) to mute (4). Transitional PCM is discarded, then a
// further guest interval must remain silent while the selected clip advances.
func TestSCHW830PrivateReferenceMutesFirmwarePCM(t *testing.T) {
	machine := openSCHW830AudioReferenceMachine(
		t,
		"ARAM_SCHW830_AUDIO_MUTE_SNAPSHOT_PREFIX",
	)
	runSCHW830Budget(t, machine, schw830AudioIdleBudget, "ring-mode idle settle")
	_ = machine.DrainAudio()
	runSCHW830AudioInput(t, machine, "left")
	assertSCHW830FirmwareGain(t, machine, 7, 4, 100, true)
	_ = machine.DrainAudio()
	runSCHW830Budget(t, machine, schw830AudioSilenceBudget, "muted PCM settle")
	if chunk := machine.DrainAudio(); len(chunk.PCM16) != 0 {
		t.Fatalf("muted DL21 firmware produced %d PCM samples", len(chunk.PCM16))
	}
}

func openSCHW830AudioReferenceMachine(t *testing.T, snapshotEnvironment string) *Machine {
	t.Helper()
	if os.Getenv("ARAM_SCHW830_AUDIO_E2E") == "" {
		t.Skip("ARAM_SCHW830_AUDIO_E2E is not configured")
	}
	prefix := os.Getenv(snapshotEnvironment)
	if prefix == "" {
		t.Skip(snapshotEnvironment + " is not configured")
	}
	set := openSamsungSCHReferenceSet(t, schw830ReferenceDirectory(t))
	machine, err := New(set, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if machine.identity.FirmwareBuildID != samsung.SCHW830DL21ProfileID || machine.audio == nil {
		t.Fatalf("audio reference machine = %+v, audio=%t", machine.identity, machine.audio != nil)
	}
	loadSystemMachineSnapshot(t, machine, prefix)
	return machine
}

func runSCHW830AudioInput(t *testing.T, machine *Machine, control string) {
	t.Helper()
	if err := machine.SetKey(control, true); err != nil {
		t.Fatal(err)
	}
	runSCHW830Budget(t, machine, schw830AudioKeyBudget, control+" press")
	if err := machine.SetKey(control, false); err != nil {
		t.Fatal(err)
	}
	runSCHW830Budget(t, machine, schw830AudioKeyBudget, control+" release")
	runSCHW830Budget(t, machine, schw830AudioSettleBudget, control+" settle")
}

func assertSCHW830FirmwareGain(
	t *testing.T,
	machine *Machine,
	wantLevel, wantMode uint32,
	wantVolume uint8,
	wantMute bool,
) {
	t.Helper()
	level, levelOK := machine.audio.readWord(schw830AudioVolumeAddress)
	mode, modeOK := machine.audio.readWord(schw830AudioRingModeAddress)
	state := machine.audio.media.Snapshot()
	if !levelOK || !modeOK || level != wantLevel || mode != wantMode ||
		state.GlobalVolume != wantVolume || state.GlobalMute != wantMute {
		t.Fatalf(
			"firmware gain level=%d/%t mode=%d/%t mixer=%d/%t, want %d/%d %d/%t",
			level,
			levelOK,
			mode,
			modeOK,
			state.GlobalVolume,
			state.GlobalMute,
			wantLevel,
			wantMode,
			wantVolume,
			wantMute,
		)
	}
}
