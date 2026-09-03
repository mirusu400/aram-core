package systemmachine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestSCHW830PrivateReferenceUsesHeadlessMachineAPI(t *testing.T) {
	directory := schw830ReferenceDirectory(t)
	set := openSamsungSCHReferenceSet(t, directory)
	machine, err := New(set, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })

	identity := machine.Identity()
	if identity.Model != "SCH-W830" ||
		identity.FirmwareBuildID != samsung.SCHW830DL21ProfileID &&
			identity.FirmwareBuildID != samsung.SCHW830DA18ProfileID {
		t.Fatalf("unexpected machine identity: %+v", identity)
	}
	if got := machine.Framebuffer().Bounds(); got.Dx() != 240 || got.Dy() != 320 {
		t.Fatalf("framebuffer bounds = %v, want 240x320", got)
	}
	controls := make(map[string]bool)
	for _, id := range machine.Controls() {
		controls[id] = true
	}
	for _, id := range []string{
		"soft-left", "soft-right", "up", "down", "left", "right", "ok", "back", "send", "end",
		"volume-up", "volume-down", "digit-0",
	} {
		if !controls[id] {
			t.Fatalf("control list has no %q", id)
		}
		if err := machine.SetKey(id, true); err != nil {
			t.Fatalf("press %s: %v", id, err)
		}
		if err := machine.SetKey(id, false); err != nil {
			t.Fatalf("release %s: %v", id, err)
		}
	}
	if err := machine.SetKey("not-a-profiled-key", true); err == nil {
		t.Fatal("headless machine accepted an unknown key ID")
	}

	initial := machine.Position()
	if initial.PC != 0x00080028 || initial.Mode != cpu.ModeARM || initial.Instructions != 0 {
		t.Fatalf("initial position = %+v", initial)
	}
	result := machine.Run(context.Background(), 1_195_629)
	if result.Reason != cpu.StopBudget || result.Err != nil || result.PC != 0x000a07d8 {
		t.Fatalf("unexpected QCSBL boundary: %+v", result)
	}
	if position := machine.Position(); position.PC != result.PC ||
		position.Instructions != result.Instructions {
		t.Fatalf("position after run = %+v, result = %+v", position, result)
	}

	media, err := machine.SaveMedia()
	if err != nil {
		t.Fatal(err)
	}
	if media.FirmwareBuildID != identity.FirmwareBuildID || len(media.Flash) == 0 || len(media.NAND) == 0 {
		t.Fatalf("invalid persistent media snapshot: build=%q flash=%d NAND=%d",
			media.FirmwareBuildID, len(media.Flash), len(media.NAND))
	}
	wrongMedia := media
	wrongMedia.FirmwareBuildID = "samsung.sch-w830.wrong"
	if err := machine.LoadMedia(wrongMedia); !errors.Is(err, ErrIncompatibleMedia) {
		t.Fatalf("incompatible media error = %v", err)
	}
	if err := machine.LoadMedia(media); err != nil {
		t.Fatal(err)
	}
	if position := machine.Position(); position != initial {
		t.Fatalf("position after media load/power cycle = %+v, want %+v", position, initial)
	}
	if err := machine.FactoryReset(); err != nil {
		t.Fatal(err)
	}
	if position := machine.Position(); position != initial {
		t.Fatalf("position after factory reset = %+v, want %+v", position, initial)
	}
}

func TestSCHW830PrivateReferenceEndKeyReturnsToHome(t *testing.T) {
	prefix := os.Getenv("ARAM_SCHW830_PHONE_KEYS_SNAPSHOT_PREFIX")
	if prefix == "" {
		t.Skip("ARAM_SCHW830_PHONE_KEYS_SNAPSHOT_PREFIX is not configured")
	}
	set := openSamsungSCHReferenceSet(t, schw830ReferenceDirectory(t))
	machine, err := New(set, Options{BackendMode: CPUBackendJIT})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if buildID := machine.Identity().FirmwareBuildID; buildID != samsung.SCHW830DL21ProfileID {
		t.Fatalf("END-key snapshot regression requires DL21, got %q", buildID)
	}
	loadSystemMachineSnapshot(t, machine, prefix)

	const (
		homeHash       = "071704b77e6b59ccd6d3f98225136cb6f8bb8101ff30619c4ed7ed496c545d25"
		endKeyHomeHash = "8396a13fd0e8c9ed1872c1f7a6ce3a90d3e2b826a2778964c00475bd93a873c5"
	)
	if hash := machine.FrameSHA256(); hash != homeHash {
		t.Fatalf("loaded phone-key frame hash = %s, want home %s", hash, homeHash)
	}
	for _, action := range []struct {
		id      string
		pressed bool
		budget  uint64
	}{
		{id: "soft-left", pressed: true, budget: 5_000_000},
		{id: "soft-left", pressed: false, budget: 5_000_000},
	} {
		if err := machine.SetKey(action.id, action.pressed); err != nil {
			t.Fatal(err)
		}
		runSCHW830Budget(t, machine, action.budget, "menu key transition")
	}
	runSCHW830Budget(t, machine, 35_000_000, "menu settle")
	if hash := machine.FrameSHA256(); hash == homeHash {
		t.Fatal("soft-left did not open the menu before END-key regression")
	}
	for _, action := range []struct {
		pressed bool
		budget  uint64
	}{
		{pressed: true, budget: 5_000_000},
		{pressed: false, budget: 5_000_000},
	} {
		if err := machine.SetKey("end", action.pressed); err != nil {
			t.Fatal(err)
		}
		runSCHW830Budget(t, machine, action.budget, "END key transition")
	}
	runSCHW830Budget(t, machine, 35_000_000, "END key settle")
	if hash := machine.FrameSHA256(); hash != endKeyHomeHash {
		t.Fatalf("END key returned frame hash %s, want post-END home %s", hash, endKeyHomeHash)
	}
}

func TestSCHW860PrivateReferenceUsesDedicatedAdjacentBoardProfile(t *testing.T) {
	directory := os.Getenv("ARAM_SCHW860_DA06_DIR")
	if directory == "" {
		t.Skip("ARAM_SCHW860_DA06_DIR is not configured")
	}
	set := openSamsungSCHReferenceSet(t, directory)
	machine, err := New(set, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if prefix := os.Getenv("ARAM_SCHW860_LOAD_SNAPSHOT_PREFIX"); prefix != "" {
		loadSystemMachineSnapshot(t, machine, prefix)
		if os.Getenv("ARAM_SCHW860_POWER_CYCLE_AFTER_LOAD") != "" {
			if err := machine.PowerCycle(); err != nil {
				t.Fatal(err)
			}
			t.Logf("power-cycled loaded SCH-W860 media from %s", prefix)
		}
	}
	identity := machine.Identity()
	if identity.Model != "SCH-W860" || identity.FirmwareBuildID != samsung.SCHW860DA06ProfileID ||
		identity.BoardID != "samsung.sch-w860" {
		t.Fatalf("unexpected SCH-W860 machine identity: %+v", identity)
	}
	if controls := machine.Controls(); len(controls) != 0 {
		t.Fatalf("unevidenced SCH-W860 controls = %v", controls)
	}
	if err := machine.SetKey("digit-0", true); !errors.Is(err, ErrUnsupportedControl) {
		t.Fatalf("unevidenced SCH-W860 key error = %v", err)
	}
	if text := os.Getenv("ARAM_SCHW860_BREAKPOINT"); text != "" {
		address, parseErr := strconv.ParseUint(text, 0, 32)
		trapBackend, ok := machine.backend.(cpu.ExecutionTrapBackend)
		if parseErr != nil || !ok {
			t.Fatalf("invalid ARAM_SCHW860_BREAKPOINT %q", text)
		}
		if err := trapBackend.SetExecutionTraps([]cpu.ExecutionTrap{{
			Address: uint32(address), Mode: cpu.ModeThumb,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	budget := uint64(20_000_000)
	if text := os.Getenv("ARAM_SCHW860_RUN_BUDGET"); text != "" {
		parsed, err := strconv.ParseUint(text, 0, 64)
		if err != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_SCHW860_RUN_BUDGET %q", text)
		}
		budget = parsed
	}
	result := machine.Run(context.Background(), budget)
	t.Logf("SCH-W860 adjacent-board run: reason=%d instructions=%d pc=0x%08x error=%v frame=%s",
		result.Reason, result.Instructions, result.PC, result.Err, machine.FrameSHA256())
	if os.Getenv("ARAM_SCHW860_LOG_REGISTERS") != "" {
		for register := uint32(cpu.RegisterR0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := machine.backend.ReadRegister(register)
			if readErr != nil {
				t.Fatal(readErr)
			}
			t.Logf("SCH-W860 register r%d=0x%08x", register, value)
		}
	}
	if framePath := os.Getenv("ARAM_SCHW860_FRAME_PATH"); framePath != "" {
		file, err := os.Create(framePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, machine.Framebuffer()); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if snapshotPrefix := os.Getenv("ARAM_SCHW860_SNAPSHOT_PREFIX"); snapshotPrefix != "" {
		snapshot, err := machine.SaveSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		saveSystemMachineSnapshot(t, snapshot, snapshotPrefix)
	}
	if result.Err != nil && os.Getenv("ARAM_SCHW860_ALLOW_FAULT") != "" {
		return
	}
	if os.Getenv("ARAM_SCHW860_BREAKPOINT") != "" &&
		result.Reason == cpu.StopExecutionTrap && result.Err == nil {
		return
	}
	if result.Reason != cpu.StopBudget || result.Err != nil || result.Instructions != budget {
		t.Fatalf("SCH-W860 adjacent-board run = %+v", result)
	}
}

func TestSCHW860PrivateReferenceProvisionsPowerCyclesAndReachesHome(t *testing.T) {
	if os.Getenv("ARAM_SCHW860_E2E") == "" {
		t.Skip("ARAM_SCHW860_E2E is not configured")
	}
	directory := os.Getenv("ARAM_SCHW860_DA06_DIR")
	if directory == "" {
		t.Skip("ARAM_SCHW860_DA06_DIR is not configured")
	}
	set := openSamsungSCHReferenceSet(t, directory)
	machine, err := NewSCHW860(set, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })

	const (
		firstBootBudget = uint64(1_510_000_000)
		firstBootHash   = "ee79d3e64d27371bcafc3cb1680e08277f70e23730c511048da1baf0f0170535"
		coldBootBudget  = uint64(2_100_000_000)
		homeHash        = "8dc35ea188be00fdc6032055f60014cfb37b861c5781ab956b6e26554e26d144"
	)
	if prefix := os.Getenv("ARAM_SCHW860_E2E_FIRSTBOOT_SNAPSHOT_PREFIX"); prefix != "" {
		loadSystemMachineSnapshot(t, machine, prefix)
		if position := machine.Position(); position.Instructions != firstBootBudget {
			t.Fatalf("loaded SCH-W860 first-boot position = %+v", position)
		}
	} else {
		runSystemMachineBudget(t, machine, firstBootBudget, "SCH-W860 native first-boot provisioning")
	}
	if hash := machine.FrameSHA256(); hash != firstBootHash {
		t.Fatalf("SCH-W860 first-boot frame hash = %s, want %s", hash, firstBootHash)
	}
	media, err := machine.SaveMedia()
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if position := machine.Position(); position.Instructions != 0 || position.PC != 0x00080028 {
		t.Fatalf("SCH-W860 position after power cycle = %+v", position)
	}
	runSystemMachineBudget(t, machine, coldBootBudget, "SCH-W860 completed-media cold boot")
	if hash := machine.FrameSHA256(); hash != homeHash {
		t.Fatalf("SCH-W860 home frame hash = %s, want %s", hash, homeHash)
	}
	if framePath := os.Getenv("ARAM_SCHW860_E2E_FRAME_PATH"); framePath != "" {
		file, createErr := os.Create(framePath)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if encodeErr := png.Encode(file, machine.Framebuffer()); encodeErr != nil {
			_ = file.Close()
			t.Fatal(encodeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err := machine.LoadMedia(media); err != nil {
		t.Fatal(err)
	}
	if position := machine.Position(); position.Instructions != 0 || position.PC != 0x00080028 {
		t.Fatalf("SCH-W860 position after completed media reload = %+v", position)
	}
}

func TestSCHW770PrivateReferenceProvisionsColdBootsAndReachesHome(t *testing.T) {
	if os.Getenv("ARAM_SCHW770_E2E") == "" {
		t.Skip("ARAM_SCHW770_E2E is not configured")
	}
	directory := os.Getenv("ARAM_SCHW770_DA05_DIR")
	if directory == "" {
		t.Skip("ARAM_SCHW770_DA05_DIR is not configured")
	}
	set := openSamsungSCHReferenceSet(t, directory)
	firstBoot, err := NewSCHW770(set, Options{BackendMode: CPUBackendJIT})
	if err != nil {
		t.Fatal(err)
	}

	const (
		provisionBudget = uint64(1_600_000_000)
		provisionHash   = "42259913833d6b3e18970334e1b93ba09b83bf809197d72732c0044ab1cad835"
		coldBootBudget  = uint64(1_600_000_000)
		holdBudget      = uint64(34_000_000)
		releaseBudget   = uint64(20_000_000)
		homeHash        = "51ded67b646df3840c58f2b162c374153b83f4436c57763c409eabbac12e7be7"
	)
	runSystemMachineBudget(t, firstBoot, provisionBudget, "SCH-W770 native provisioning")
	if hash := firstBoot.FrameSHA256(); hash != provisionHash {
		t.Fatalf("SCH-W770 provisioned frame hash = %s, want %s", hash, provisionHash)
	}
	media, err := firstBoot.SaveMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(media.SecondaryFlash) == 0 || len(media.OneNANDSpare) == 0 {
		t.Fatalf("SCH-W770 dual-flash media is incomplete: secondary=%d spare=%d",
			len(media.SecondaryFlash), len(media.OneNANDSpare))
	}
	if err := firstBoot.Close(); err != nil {
		t.Fatal(err)
	}
	firstBoot = nil
	runtime.GC()

	// A new machine proves that the guest-created BML/TFS4 media is sufficient;
	// no volatile state or host-side guest-code patch crosses this boundary.
	coldBoot, err := NewSCHW770(set, Options{BackendMode: CPUBackendJIT, Media: &media})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coldBoot.Close() })
	runSystemMachineBudget(t, coldBoot, coldBootBudget, "SCH-W770 saved-media cold boot")
	if err := coldBoot.SetKey("hold", true); err != nil {
		t.Fatal(err)
	}
	runSystemMachineBudget(t, coldBoot, holdBudget, "SCH-W770 long HOLD")
	if err := coldBoot.SetKey("hold", false); err != nil {
		t.Fatal(err)
	}
	runSystemMachineBudget(t, coldBoot, releaseBudget, "SCH-W770 HOLD release")
	if hash := coldBoot.FrameSHA256(); hash != homeHash {
		t.Fatalf("SCH-W770 home frame hash = %s, want %s", hash, homeHash)
	}
	if framePath := os.Getenv("ARAM_SCHW770_E2E_FRAME_PATH"); framePath != "" {
		file, createErr := os.Create(framePath)
		if createErr != nil {
			t.Fatal(createErr)
		}
		encodeErr := png.Encode(file, coldBoot.Framebuffer())
		closeErr := file.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func TestSCHW830PrivateReferenceProvisionsPowerCyclesAndLaunchesApp(t *testing.T) {
	if os.Getenv("ARAM_SCHW830_E2E") == "" {
		t.Skip("ARAM_SCHW830_E2E is not configured")
	}
	set := openSamsungSCHReferenceSet(t, schw830ReferenceDirectory(t))
	machine, err := New(set, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machine.Close() })

	type buildExpectation struct {
		homeBudget       uint64
		homeHash         string
		keyPressBudget   uint64
		keyReleaseBudget uint64
		appSettleBudget  uint64
		appHash          string
	}
	expectations := map[string]buildExpectation{
		samsung.SCHW830DL21ProfileID: {
			homeBudget:       2_051_195_629,
			homeHash:         "071704b77e6b59ccd6d3f98225136cb6f8bb8101ff30619c4ed7ed496c545d25",
			keyPressBudget:   5_000_000,
			keyReleaseBudget: 5_000_000,
			appSettleBudget:  40_000_000,
			appHash:          "288a58871e07ad9dc6572efbccf204fc93dfa076c526e8c86670b7de636d1169",
		},
		samsung.SCHW830DA18ProfileID: {
			homeBudget:       2_101_195_629,
			homeHash:         "e430f086171f5946976cbafb06b5af48c8888e06fd499c9a42772557926579b7",
			keyPressBudget:   3_000_000,
			keyReleaseBudget: 3_000_000,
			appSettleBudget:  40_000_000,
			appHash:          "fd35462e1d748ca755ddcbc51d98a63234f8602433da0bd3207cb07d3dbde1fa",
		},
	}
	buildID := machine.Identity().FirmwareBuildID
	expectation, ok := expectations[buildID]
	if !ok {
		t.Fatalf("no end-to-end expectation for %q", buildID)
	}

	var media MediaState
	if mediaPrefix := os.Getenv("ARAM_SCHW830_E2E_MEDIA_PREFIX"); mediaPrefix != "" {
		flash, readErr := os.ReadFile(mediaPrefix + ".flash")
		if readErr != nil {
			t.Fatal(readErr)
		}
		nand, readErr := os.ReadFile(mediaPrefix + ".nand")
		if readErr != nil {
			t.Fatal(readErr)
		}
		media = MediaState{FirmwareBuildID: buildID, Flash: flash, NAND: nand}
		if err := machine.LoadMedia(media); err != nil {
			t.Fatal(err)
		}
	} else {
		runSCHW830Budget(t, machine, 1_511_195_629, "native first-boot provisioning")
		const firstBootHash = "5a2018cc9f59fd362904308f96e07bc928ce5ea29680b06300c38ec7ab1d739b"
		if hash := machine.FrameSHA256(); hash != firstBootHash {
			t.Fatalf("first-boot frame hash = %s, want %s", hash, firstBootHash)
		}
		media, err = machine.SaveMedia()
		if err != nil {
			t.Fatal(err)
		}
		if err := machine.PowerCycle(); err != nil {
			t.Fatal(err)
		}
	}

	preInteraction, loaded := loadSCHW830E2ESnapshot(t, machine)
	if !loaded {
		runSCHW830Budget(t, machine, expectation.homeBudget, "completed-media cold boot pre-interaction")
		preInteraction, err = machine.SaveSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		saveSCHW830E2ESnapshot(t, preInteraction)
	}
	if err := machine.SetKey("volume-up", true); err != nil {
		t.Fatal(err)
	}
	runSCHW830Budget(t, machine, expectation.keyPressBudget, "volume-up press")
	if err := machine.SetKey("volume-up", false); err != nil {
		t.Fatal(err)
	}
	runSCHW830Budget(t, machine, expectation.keyReleaseBudget, "volume-up release")
	runSCHW830Budget(t, machine, expectation.appSettleBudget, "application UI settle")
	if framePath := os.Getenv("ARAM_SCHW830_E2E_FRAME_PATH"); framePath != "" {
		file, createErr := os.Create(framePath)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if encodeErr := png.Encode(file, machine.Framebuffer()); encodeErr != nil {
			_ = file.Close()
			t.Fatal(encodeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Logf("saved application frame to %s", framePath)
	}
	if hash := machine.FrameSHA256(); hash != expectation.appHash {
		t.Fatalf("application frame hash = %s, want %s", hash, expectation.appHash)
	}
	if err := machine.LoadSnapshot(preInteraction); err != nil {
		t.Fatal(err)
	}
	runSCHW830Budget(t, machine, 10_000_000, "home UI settle")
	if hash := machine.FrameSHA256(); hash != expectation.homeHash {
		t.Fatalf("home frame hash = %s, want %s", hash, expectation.homeHash)
	}

	// Prove that the media captured at the guest's factory-success wait is a
	// standalone persistent input, rather than relying on RAM left by the first
	// run. LoadMedia performs another complete volatile reset.
	if err := machine.LoadMedia(media); err != nil {
		t.Fatal(err)
	}
	if position := machine.Position(); position.Instructions != 0 || position.PC != 0x00080028 {
		t.Fatalf("position after reloading completed media = %+v", position)
	}
}

type schw830SnapshotMetadata struct {
	Schema          string
	FirmwareBuildID string
	BoardID         string
	PlatformID      string
	CPUIdentity     cpu.Identity
	Instructions    uint64
}

func loadSCHW830E2ESnapshot(t *testing.T, machine *Machine) (Snapshot, bool) {
	t.Helper()
	prefix := os.Getenv("ARAM_SCHW830_E2E_SNAPSHOT_PREFIX")
	if prefix == "" {
		return Snapshot{}, false
	}
	metadataBytes, err := os.ReadFile(prefix + ".meta")
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loadSystemMachineSnapshotBytes(t, machine, prefix, metadataBytes)
	t.Logf("loaded pre-interaction snapshot from %s", prefix)
	return snapshot, true
}

func loadSystemMachineSnapshot(t *testing.T, machine *Machine, prefix string) Snapshot {
	t.Helper()
	metadataBytes, err := os.ReadFile(prefix + ".meta")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loadSystemMachineSnapshotBytes(t, machine, prefix, metadataBytes)
	t.Logf("loaded system-machine snapshot from %s", prefix)
	return snapshot
}

func loadSystemMachineSnapshotBytes(
	t *testing.T,
	machine *Machine,
	prefix string,
	metadataBytes []byte,
) Snapshot {
	t.Helper()
	var metadata schw830SnapshotMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	read := func(suffix string) []byte {
		state, readErr := os.ReadFile(prefix + suffix)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return state
	}
	snapshot := Snapshot{
		Schema: metadata.Schema, FirmwareBuildID: metadata.FirmwareBuildID,
		BoardID: metadata.BoardID, PlatformID: metadata.PlatformID,
		CPUIdentity: metadata.CPUIdentity, Instructions: metadata.Instructions,
		CPU: read(".cpu"), Bus: read(".bus"), Flash: read(".flash"),
	}
	if err := machine.LoadSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func saveSCHW830E2ESnapshot(t *testing.T, snapshot Snapshot) {
	t.Helper()
	prefix := os.Getenv("ARAM_SCHW830_E2E_SNAPSHOT_PREFIX")
	if prefix == "" {
		return
	}
	saveSystemMachineSnapshot(t, snapshot, prefix)
}

func saveSystemMachineSnapshot(t *testing.T, snapshot Snapshot, prefix string) {
	t.Helper()
	metadata, err := json.Marshal(schw830SnapshotMetadata{
		Schema: snapshot.Schema, FirmwareBuildID: snapshot.FirmwareBuildID,
		BoardID: snapshot.BoardID, PlatformID: snapshot.PlatformID,
		CPUIdentity: snapshot.CPUIdentity, Instructions: snapshot.Instructions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		t.Fatal(err)
	}
	for suffix, state := range map[string][]byte{
		".meta": metadata, ".cpu": snapshot.CPU, ".bus": snapshot.Bus, ".flash": snapshot.Flash,
	} {
		if err := os.WriteFile(prefix+suffix, state, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("saved system-machine snapshot to %s.{meta,cpu,bus,flash}", prefix)
}

func runSCHW830Budget(t *testing.T, machine *Machine, budget uint64, label string) {
	runSystemMachineBudget(t, machine, budget, label)
}

func runSystemMachineBudget(t *testing.T, machine *Machine, budget uint64, label string) {
	t.Helper()
	result := machine.Run(context.Background(), budget)
	if result.Reason != cpu.StopBudget || result.Err != nil || result.Instructions != budget {
		t.Fatalf("%s stopped unexpectedly: %+v", label, result)
	}
	t.Logf("%s: instructions=%d pc=0x%08x frame=%s",
		label, machine.Position().Instructions, result.PC, machine.FrameSHA256())
}

func schw830ReferenceDirectory(t *testing.T) string {
	t.Helper()
	directory := os.Getenv("ARAM_SCHW830_REFERENCE_DIR")
	if directory != "" {
		return directory
	}
	root := os.Getenv("ARAM_REFERENCE_REPO")
	if root == "" {
		t.Skip("ARAM_REFERENCE_REPO or ARAM_SCHW830_REFERENCE_DIR is not configured")
	}
	return filepath.Join(root, "SCH-W380_DL21")
}

func openSamsungSCHReferenceSet(t *testing.T, path string) firmwareset.Set {
	t.Helper()
	candidates := samsungSCHReferenceSources(t, path)
	if len(candidates) > 20 {
		t.Fatalf("configured reference contains %d candidate pieces, want at most 20", len(candidates))
	}
	// Exact opaque WBIN builds cannot classify that one piece in isolation.
	// A directory/archive containing one complete set can still be selected by
	// the same full-package hash registry before the mixed-corpus subset scan.
	if len(candidates) == 2 || len(candidates) == 4 || len(candidates) == 5 {
		direct, setErr := firmwareset.NewSet(candidates)
		if setErr != nil {
			t.Fatal(setErr)
		}
		if pkg, inspectErr := samsung.Inspect(direct); inspectErr == nil {
			if _, matchErr := samsung.BuiltinRegistry().Match(pkg); matchErr == nil {
				return direct
			}
		}
	}
	// Monolithic firmware/preload pieces intentionally have no filename or
	// embedded wrapper role. Resolve an archive containing unrelated .bin
	// artifacts by exact two-piece registry hashes before the header-based
	// mixed-corpus scan below.
	var exactPair firmwareset.Set
	exactPairMatches := 0
	for left := 0; left < len(candidates); left++ {
		for right := left + 1; right < len(candidates); right++ {
			pair, setErr := firmwareset.NewSet([]firmwareset.Source{
				candidates[left],
				candidates[right],
			})
			if setErr != nil {
				t.Fatal(setErr)
			}
			pkg, inspectErr := samsung.Inspect(pair)
			if inspectErr != nil {
				continue
			}
			if _, matchErr := samsung.BuiltinRegistry().Match(pkg); matchErr != nil {
				continue
			}
			exactPair = pair
			exactPairMatches++
		}
	}
	if exactPairMatches == 1 {
		return exactPair
	}
	if exactPairMatches > 1 {
		t.Fatalf("configured reference has %d exact supported two-piece Samsung firmware sets", exactPairMatches)
	}
	type inspectedCandidate struct {
		family string
		piece  samsung.Piece
	}
	inspected := make([]inspectedCandidate, len(candidates))
	for index, source := range candidates {
		single, setErr := firmwareset.NewSet([]firmwareset.Source{source})
		if setErr != nil {
			t.Fatal(setErr)
		}
		pkg, inspectErr := samsung.Inspect(single)
		if inspectErr != nil || len(pkg.Pieces) != 1 {
			continue
		}
		for _, piece := range pkg.Pieces {
			piece.Index = index
			inspected[index] = inspectedCandidate{family: pkg.Family, piece: piece}
		}
	}
	var matchedIndices []int
	matches := 0
	for mask := 1; mask < 1<<len(candidates); mask++ {
		count := bits.OnesCount(uint(mask))
		if count != 4 && count != 5 {
			continue
		}
		pkg := samsung.Package{Pieces: make(map[samsung.Role]samsung.Piece, count)}
		indices := make([]int, 0, count)
		valid := true
		for index, candidate := range inspected {
			if mask&(1<<index) != 0 {
				if candidate.family == "" {
					valid = false
					break
				}
				if pkg.Family == "" {
					pkg.Family = candidate.family
				} else if pkg.Family != candidate.family {
					valid = false
					break
				}
				role := candidate.piece.Header.Role
				if _, duplicate := pkg.Pieces[role]; duplicate {
					valid = false
					break
				}
				pkg.Pieces[role] = candidate.piece
				indices = append(indices, index)
			}
		}
		if !valid || !pkg.Complete() {
			continue
		}
		if _, matchErr := samsung.BuiltinRegistry().Match(pkg); matchErr != nil {
			continue
		}
		matchedIndices = indices
		matches++
	}
	if matches != 1 {
		t.Fatalf("configured reference has %d exact supported Samsung firmware sets", matches)
	}
	matchedSources := make([]firmwareset.Source, len(matchedIndices))
	for index, candidateIndex := range matchedIndices {
		matchedSources[index] = candidates[candidateIndex]
	}
	matched, err := firmwareset.NewSet(matchedSources)
	if err != nil {
		t.Fatal(err)
	}
	return matched
}

func samsungSCHReferenceSources(t *testing.T, path string) []firmwareset.Source {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("configured reference: %v", err)
	}
	if !info.IsDir() {
		return samsungSCHReferenceArchiveSources(t, path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("configured reference directory: %v", err)
	}
	var candidates []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !schReferenceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, err := os.Open(filepath.Join(path, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	return candidates
}

type samsungSCHArchiveEntry struct {
	path string
	size int64
}

func samsungSCHReferenceArchiveSources(t *testing.T, archive string) []firmwareset.Source {
	t.Helper()
	sevenZip, err := exec.LookPath("7z.exe")
	if err != nil {
		sevenZip, err = exec.LookPath("7z")
	}
	if err != nil {
		t.Fatal("configured reference archive requires 7-Zip")
	}
	listing, err := exec.Command(
		sevenZip,
		"l", "-slt", "-ba", "-sccUTF-8", "--", archive,
	).Output()
	if err != nil {
		t.Fatalf("list configured reference archive: %v", err)
	}
	entries, err := parseSamsungSCHArchiveListing(string(listing))
	if err != nil {
		t.Fatal(err)
	}
	sources := make([]firmwareset.Source, 0, len(entries))
	for _, entry := range entries {
		data, extractErr := exec.Command(
			sevenZip,
			"x", "-so", "-sccUTF-8", "--", archive, entry.path,
		).Output()
		if extractErr != nil {
			t.Fatalf("read configured reference archive entry: %v", extractErr)
		}
		if int64(len(data)) != entry.size {
			t.Fatalf(
				"configured reference archive entry has size %d, want %d",
				len(data), entry.size,
			)
		}
		sources = append(sources, firmwareset.Source{
			ReaderAt: bytes.NewReader(data),
			Size:     entry.size,
		})
	}
	return sources
}

func parseSamsungSCHArchiveListing(listing string) ([]samsungSCHArchiveEntry, error) {
	var entries []samsungSCHArchiveEntry
	fields := make(map[string]string)
	flush := func() error {
		defer clear(fields)
		path := fields["Path"]
		if path == "" || fields["Folder"] == "+" || strings.Contains(fields["Attributes"], "D") ||
			!schReferenceExtension(filepath.Ext(path)) {
			return nil
		}
		size, err := strconv.ParseInt(fields["Size"], 10, 64)
		if err != nil || size <= 0 {
			return fmt.Errorf("configured reference archive has invalid entry size")
		}
		entries = append(entries, samsungSCHArchiveEntry{path: path, size: size})
		return nil
	}
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		parts := strings.SplitN(line, " = ", 2)
		if len(parts) == 2 {
			fields[parts[0]] = parts[1]
		}
	}
	if len(fields) != 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("configured reference archive has no Samsung firmware pieces")
	}
	return entries, nil
}

func schReferenceExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".wbt", ".wbin", ".mbin", ".abin", ".dat", ".fnt", ".bin":
		return true
	default:
		return false
	}
}
