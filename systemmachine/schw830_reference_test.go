package systemmachine

import (
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
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
		"soft-left", "soft-right", "up", "down", "left", "right", "ok", "back", "send",
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

func openSamsungSCHReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured reference directory: %v", err)
	}
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !schReferenceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured reference contains %d SCH download pieces, want 4", len(sources))
	}
	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func schReferenceExtension(extension string) bool {
	switch extension {
	case ".wbt", ".wbin", ".dat", ".fnt":
		return true
	default:
		return false
	}
}
