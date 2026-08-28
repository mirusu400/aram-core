package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	stdimage "image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/raptor"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestFactoryLoadsKTFPackageWithLeadingCoverImage(t *testing.T) {
	client := syntheticKTFClient()
	jar := testZIP(t, map[string][]byte{
		"client.bin4096": client,
	})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__": []byte(
			"PID:PD000001\nAID:01020304\nMClass:GameMain\n",
		),
	})
	archive = append([]byte{0xff, 0xd8, 0xff, 0xd9}, archive...)
	source := machinecore.Source{
		Name:     "covered.zip",
		ReaderAt: bytes.NewReader(archive),
		Size:     int64(len(archive)),
	}

	created, err := NewFactory().Create(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)
	if info := machine.ImageInfo(); info.SourceKind != loader.KindKTF {
		t.Fatalf("source kind = %q", info.SourceKind)
	}
}

func TestFactorySizesKTFFramebufferFromDescriptor(t *testing.T) {
	build := func(displaySize string) *Machine {
		t.Helper()
		jar := testZIP(t, map[string][]byte{
			"client.bin4096": syntheticKTFClient(),
		})
		descriptor := "PID:PD000001\nAID:01020304\nMClass:GameMain\n" + displaySize
		archive := testZIP(t, map[string][]byte{
			"01020304.jar": jar,
			"__adf__":      []byte(descriptor),
		})
		created, err := NewFactory().Create(
			context.Background(),
			machinecore.Source{
				Name:     "sized.zip",
				ReaderAt: bytes.NewReader(archive),
				Size:     int64(len(archive)),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = created.Close() })
		return created.(*Machine)
	}

	machine := build("DisplaySize:176*220\n")
	if bounds := machine.Framebuffer().Bounds(); bounds.Dx() != 176 ||
		bounds.Dy() != 220 {
		t.Fatalf("declared framebuffer = %v", bounds)
	}
	if got := machine.ktf.DisplayWidth(); got != 176 {
		t.Fatalf("display width = %d", got)
	}
	if got := machine.ktf.DefaultCardHeight(); got != 220 {
		t.Fatalf("default card height = %d", got)
	}

	// A descriptor without the field keeps the factory's own framebuffer.
	if bounds := build("").Framebuffer().Bounds(); bounds.Dx() != 240 ||
		bounds.Dy() != 320 {
		t.Fatalf("default framebuffer = %v", bounds)
	}
}

func TestKTFSaveStateRestoresAdapterAndSharedServices(t *testing.T) {
	client := syntheticKTFClient()
	jar := testZIP(t, map[string][]byte{
		"client.bin4096": client,
	})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__": []byte(
			"PID:PD000001\nAID:01020304\nMClass:GameMain\n",
		),
	})
	created, err := NewFactory().Create(
		context.Background(),
		machinecore.Source{
			Name: "state.zip", ReaderAt: bytes.NewReader(archive),
			Size: int64(len(archive)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })

	allocation, err := machine.ktf.Heap.Allocate(16, true)
	if err != nil || allocation == 0 {
		t.Fatalf("allocate KTF state fixture = 0x%08x, %v", allocation, err)
	}
	if err := machine.cpu.WriteMemory(
		allocation,
		[]byte{1, 2, 3, 4},
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.Services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/state.dat",
		[]byte("saved"),
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.FileData["/state.dat"] = []byte("saved")
	if err := machine.ktf.Services.Advance(
		machine.ktf.ServiceOwner,
		17*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.TickMS = 17
	sleepingTask, err := machine.ktf.NewTask(ktfrt.ImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	sleepingTask.WakeAtMS = 77
	machine.ktf.Tasks = []*ktfrt.Task{sleepingTask}
	graphics, err := machine.ktf.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	smallFont, err := machine.ktf.EnsureKTFFont(ktfrt.JavaFont{
		Size: ktfrt.JavaFontSizeSmall,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.WriteJavaFieldWord(
		graphics,
		0,
		smallFont,
	); err != nil {
		t.Fatal(err)
	}
	wipicScreen, err := machine.ktf.EnsureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	machine.ktf.WipicScreenPending = true
	savedPixel := color.RGBA{R: 1, G: 2, B: 3, A: 0xff}
	machine.frame.SetRGBA(2, 3, savedPixel)
	machine.ktf.Graphics[graphics].PixelsDirty = true

	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(
		allocation,
		[]byte{9, 9, 9, 9},
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.Services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/state.dat",
		[]byte("changed"),
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.FileData = map[string][]byte{}
	machine.ktf.TickMS = 99
	machine.ktf.Tasks[0].WakeAtMS = 0
	machine.ktf.WipicScreenPending = false
	if err := machine.ktf.WriteJavaFieldWord(graphics, 0, 0); err != nil {
		t.Fatal(err)
	}

	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	var memory [4]byte
	if err := machine.cpu.ReadMemory(allocation, memory[:]); err != nil {
		t.Fatal(err)
	}
	if memory != [4]byte{1, 2, 3, 4} {
		t.Fatalf("restored KTF heap bytes = %v", memory)
	}
	stored, err := machine.ktf.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/state.dat",
	)
	if err != nil || string(stored) != "saved" {
		t.Fatalf("restored KTF storage = %q, %v", stored, err)
	}
	if machine.ktf.TickMS != 17 ||
		machine.ktf.Services.Clock.Monotonic() != 17*time.Millisecond ||
		string(machine.ktf.FileData["/state.dat"]) != "saved" {
		t.Fatalf(
			"restored KTF mirrors = tick %d, clock %s, file %q",
			machine.ktf.TickMS,
			machine.ktf.Services.Clock.Monotonic(),
			machine.ktf.FileData["/state.dat"],
		)
	}
	if len(machine.ktf.Tasks) != 1 ||
		machine.ktf.Tasks[0].WakeAtMS != 77 {
		t.Fatalf("restored KTF sleep deadlines = %+v", machine.ktf.Tasks)
	}
	if machine.ktf.WipicScreenFramebuffer != wipicScreen ||
		!machine.ktf.WipicScreenPending {
		t.Fatalf(
			"restored pending WIPI-C screen = framebuffer 0x%08x pending %t",
			machine.ktf.WipicScreenFramebuffer,
			machine.ktf.WipicScreenPending,
		)
	}
	if got := machine.frame.RGBAAt(2, 3); got != savedPixel {
		t.Fatalf("restored KTF framebuffer pixel = %#v", got)
	}
	restoredFont, err := machine.ktf.KtfGraphicsFont(graphics)
	if err != nil {
		t.Fatal(err)
	}
	if restoredFont != smallFont {
		t.Fatalf(
			"restored KTF graphics font = 0x%08x, want 0x%08x",
			restoredFont,
			smallFont,
		)
	}
	fontMetrics, err := machine.ktf.Services.Text.Metrics(
		machine.ktf.ServiceOwner,
		machine.ktf.FontServices[restoredFont],
	)
	if err != nil {
		t.Fatal(err)
	}
	if fontMetrics.Height != 8 {
		t.Fatalf(
			"restored KTF graphics font height = %d, want 8",
			fontMetrics.Height,
		)
	}
	surfacePixels, err := machine.ktf.Services.Graphics.RGBA(
		machine.ktf.ServiceOwner,
		machine.ktf.GraphicsServices[graphics],
	)
	if err != nil {
		t.Fatal(err)
	}
	offset := (3*machine.frame.Bounds().Dx() + 2) * 4
	if !bytes.Equal(
		surfacePixels[offset:offset+4],
		[]byte{savedPixel.R, savedPixel.G, savedPixel.B, savedPixel.A},
	) {
		t.Fatalf(
			"restored KTF shared pixel = %v",
			surfacePixels[offset:offset+4],
		)
	}
}

func TestKTFResetRebuildsAdapterAndServices(t *testing.T) {
	client := syntheticKTFClient()
	jar := testZIP(t, map[string][]byte{"client.bin4096": client})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__": []byte(
			"PID:PD000001\nAID:01020304\nMClass:GameMain\n",
		),
	})
	created, err := NewFactory().Create(
		context.Background(),
		machinecore.Source{
			Name: "reset.zip", ReaderAt: bytes.NewReader(archive),
			Size: int64(len(archive)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	allocation, err := machine.ktf.Heap.Allocate(8, true)
	if err != nil || allocation == 0 {
		t.Fatalf("allocate KTF reset fixture = 0x%08x, %v", allocation, err)
	}
	if err := machine.cpu.WriteMemory(allocation, []byte{0xaa}); err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.Services.Advance(
		machine.ktf.ServiceOwner,
		25*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.TickMS = 25
	machine.ktfStarted = true
	if err := machine.ktf.Services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/persist.dat",
		[]byte("persistent KTF file"),
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.FileData["/persist.dat"] = []byte("persistent KTF file")
	if err := machine.ktf.Services.Storage.WriteFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
		[]byte("temporary"),
	); err != nil {
		t.Fatal(err)
	}
	database := &ktfrt.Database{
		Name:       "scores",
		RecordSize: 4,
		Records:    [][]byte{{1, 2, 3, 4}},
	}
	databaseService, err := machine.ktf.Services.Storage.CreateRecordStore(
		machine.ktf.ServiceOwner,
		database.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.Services.Storage.ReplaceRecords(
		machine.ktf.ServiceOwner,
		databaseService,
		1,
		map[uint32][]byte{0: {1, 2, 3, 4}},
	); err != nil {
		t.Fatal(err)
	}
	machine.ktf.DatabaseStores[database.Name] = database
	machine.ktf.DatabaseServices[database.Name] = databaseService

	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	var restored [1]byte
	if err := machine.cpu.ReadMemory(allocation, restored[:]); err != nil {
		t.Fatal(err)
	}
	if restored[0] != 0 || machine.ktf.TickMS != 0 {
		t.Fatalf(
			"reset KTF state = byte %#x allocations %d tick %d",
			restored[0],
			len(machine.ktf.Heap.Allocations),
			machine.ktf.TickMS,
		)
	}
	if machine.ktfStarted || machine.State() != machinecore.StateReady ||
		machine.ktf.Services.Clock.Monotonic() != 0 {
		t.Fatalf(
			"reset KTF lifecycle = started %t state %s clock %s",
			machine.ktfStarted,
			machine.State(),
			machine.ktf.Services.Clock.Monotonic(),
		)
	}
	persisted, err := machine.ktf.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/persist.dat",
	)
	if err != nil || string(persisted) != "persistent KTF file" ||
		string(machine.ktf.FileData["/persist.dat"]) != "persistent KTF file" {
		t.Fatalf(
			"reset KTF persistent file = %q, mirror %q, %v",
			persisted,
			machine.ktf.FileData["/persist.dat"],
			err,
		)
	}
	if _, err := machine.ktf.Services.Storage.ReadFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
	); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("reset KTF temporary file error = %v", err)
	}
	persistedDatabase := machine.ktf.DatabaseStores["scores"]
	persistedService, err := machine.ktf.Services.Storage.OpenRecordStore(
		machine.ktf.ServiceOwner,
		"scores",
	)
	if err != nil {
		t.Fatal(err)
	}
	persistedRecord, err := machine.ktf.Services.Storage.Record(
		machine.ktf.ServiceOwner,
		persistedService,
		0,
	)
	if err != nil || persistedDatabase == nil ||
		persistedDatabase.RecordSize != 4 ||
		len(persistedDatabase.Records) != 1 ||
		!bytes.Equal(persistedDatabase.Records[0], []byte{1, 2, 3, 4}) ||
		!bytes.Equal(persistedRecord, []byte{1, 2, 3, 4}) {
		t.Fatalf(
			"reset KTF database = %+v, service record %v, %v",
			persistedDatabase,
			persistedRecord,
			err,
		)
	}
}

func TestFactoryMapsAndExecutesEADSEntryPoint(t *testing.T) {
	data := syntheticEADS()
	source := machinecore.Source{
		Name:     "synthetic.dat",
		Format:   "wipi-dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	}
	created, err := NewFactory().Create(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	machine, ok := created.(*Machine)
	if !ok {
		t.Fatalf("machine type = %T", created)
	}

	info := machine.ImageInfo()
	if info.Name != "SyntheticEADS" ||
		info.EntryPoint != 0x02000001 ||
		info.Mode != cpu.ModeThumb ||
		info.ProfileID != DefaultProfileID {
		t.Fatalf("image info = %+v", info)
	}
	if got := register(t, machine, cpu.RegisterPC); got != 0x02000000 {
		t.Fatalf("initial pc = 0x%08x", got)
	}
	if got := register(t, machine, cpu.RegisterCPSR); got&cpu.StatusThumb == 0 {
		t.Fatalf("initial CPSR = 0x%08x, Thumb bit is clear", got)
	}
	if created.State() != machinecore.StateReady {
		t.Fatalf("initial state = %s", created.State())
	}

	if err := created.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := machine.LastResult()
	if result.Reason != cpu.StopBudget ||
		result.Instructions != 1 ||
		result.PC != 0x02000002 {
		t.Fatalf("entry execution = %+v", result)
	}
	if got := register(t, machine, cpu.RegisterSP); got != 0x7ffffffc {
		t.Fatalf("sp after entry PUSH = 0x%08x", got)
	}
	if created.State() != machinecore.StatePaused {
		t.Fatalf("state after entry = %s", created.State())
	}
}

func TestFactoryInstallsResourcesAndResetRestoresThem(t *testing.T) {
	data := syntheticEADS()
	payload := []byte{1, 2, 3}
	factory := NewFactory()
	factory.Resources = map[string][]byte{"config.bin": payload}
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "resources.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	payload[0] = 0xff
	if resource := machine.wipi.Resources["config.bin"]; resource == nil ||
		!bytes.Equal(resource.Data, []byte{1, 2, 3}) {
		t.Fatalf("installed resource = %+v", resource)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resource := machine.wipi.Resources["config.bin"]; resource == nil ||
		!bytes.Equal(resource.Data, []byte{1, 2, 3}) {
		t.Fatalf("reset resource = %+v", resource)
	}
}

func TestMachineLifecycleResumeStopResetAndClose(t *testing.T) {
	machine := newSyntheticMachine(t)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if machine.LastResult().Reason != cpu.StopBreakpoint ||
		machine.State() != machinecore.StatePaused {
		t.Fatalf("state after Resume = %s, result %+v", machine.State(), machine.LastResult())
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped {
		t.Fatalf("state after Stop = %s", machine.State())
	}
	if err := machine.Start(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Start after Stop error = %v", err)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateReady {
		t.Fatalf("state after Reset = %s", machine.State())
	}
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := machine.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up"}); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("QueueInput after Close error = %v", err)
	}
}

func TestMachineTreatsReturnToZeroSentinelAsCleanExit(t *testing.T) {
	data := syntheticEADS()
	copy(data[0xb0:], []byte{
		0x00, 0xb5, // push {lr}
		0x00, 0xbd, // pop {pc}
	})
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "returning.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	machine.runBudget = DefaultHandsetRunBudget

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped ||
		machine.LastResult().Reason != cpu.StopExited ||
		machine.LastResult().PC != 0 {
		t.Fatalf("return result = %+v, state %s",
			machine.LastResult(), machine.State())
	}
}

func TestFactoryRejectsSourceWithoutEADS(t *testing.T) {
	data := []byte("not a WIPI container")
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "invalid.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if !errors.Is(err, ErrUnsupportedSource) &&
		!errors.Is(err, loader.ErrNoContainerRecords) {
		t.Fatalf("Create error = %v", err)
	}
}

func TestFactoryDoesNotMisclassifyMalformedJavaArchiveAsRaptor(t *testing.T) {
	data := []byte("PK\x03\x04synthetic Java archive placeholder")
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "unsupported.jar",
		Format:   string(loader.KindJava),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err == nil {
		t.Fatal("Factory accepted a malformed Java archive")
	}
	var formatErr *raptor.FormatError
	if errors.As(err, &formatErr) {
		t.Fatalf("malformed Java archive was classified as Raptor: %v", err)
	}
	var skvmFormatErr *skloader.FormatError
	if errors.As(err, &skvmFormatErr) {
		t.Fatalf("unclaimed Java archive was classified as SKVM: %v", err)
	}
	if !errors.Is(err, ErrUnsupportedSource) &&
		!errors.Is(err, loader.ErrNoContainerRecords) {
		t.Fatalf("malformed Java archive error = %v", err)
	}
}

// Issue #69, 크아비엔비2011: an Android APK is a ZIP, so it survives every WIPI
// loader and reached the container scan, which reported "no valid ABHS or EADS
// records" — the person who opened it had no way to tell that ARAM simply does
// not run Android applications.
func TestFactoryNamesAndroidPackagesInsteadOfBlamingTheContainer(t *testing.T) {
	archive := testZIP(t, map[string][]byte{
		"AndroidManifest.xml": []byte("binary xml"),
		"classes.dex":         []byte("dex 035 dalvik"),
	})
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "android.apk",
		Format:   string(loader.KindJava),
		ReaderAt: bytes.NewReader(archive),
		Size:     int64(len(archive)),
	})
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("Android package error = %v", err)
	}
	if !strings.Contains(err.Error(), "Android application package") {
		t.Fatalf("Android package error does not name the format: %v", err)
	}
	if errors.Is(err, loader.ErrNoContainerRecords) {
		t.Fatalf("Android package was reported as a broken container: %v", err)
	}
}

func TestFactoryClassifiesEncryptedKTFPackageAsUnsupported(t *testing.T) {
	encrypted := make([]byte, 16+32)
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": testOMADCF(2, 32, encrypted),
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "protected.zip",
		Format:   string(loader.KindJava),
		ReaderAt: bytes.NewReader(archive),
		Size:     int64(len(archive)),
	})
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("encrypted KTF package error = %v", err)
	}
}

func TestFactoryVerifiesSourceHashAndMemoryLimit(t *testing.T) {
	data := syntheticEADS()
	source := machinecore.Source{
		Name:     "synthetic.dat",
		SHA256:   strings.Repeat("00", 32),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	}
	if _, err := NewFactory().Create(context.Background(), source); err == nil {
		t.Fatal("Factory accepted a mismatched source hash")
	}

	factory := NewFactory()
	factory.MemoryLimit = uint64(DefaultStackSize) + 0x1000
	source.SHA256 = ""
	if _, err := factory.Create(context.Background(), source); err == nil {
		t.Fatal("Factory accepted an application beyond its guest memory limit")
	}
}

func TestTitleRuntimeRequiresKnownSourceHash(t *testing.T) {
	data := syntheticEADS()
	copy(data[0x80+0x20:0x80+0x30], "MinigameQVGAOEM")
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "lookalike.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if _, ok := machine.EADSFrameStats(); ok {
		t.Fatal("name-only lookalike selected the title-specific runtime")
	}
	if machine.ImageInfo().ProfileID != DefaultProfileID {
		t.Fatalf("lookalike profile = %q", machine.ImageInfo().ProfileID)
	}
}

func TestMachineDispatchesPublicWIPITrampoline(t *testing.T) {
	machine := newSyntheticMachine(t)
	const source = guest.HeapBase + 0x200
	if err := machine.cpu.WriteMemory(source, []byte("public-wipi\x00")); err != nil {
		t.Fatal(err)
	}
	stub, ok := machine.wipi.Layout.StubByName["strlen"]
	if !ok {
		t.Fatal("strlen trampoline is absent")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0:   source,
		cpu.RegisterLR:   machine.info.EntryPoint,
		cpu.RegisterPC:   stub &^ 1,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		if err := machine.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := register(t, machine, cpu.RegisterR0); got != 11 {
		t.Fatalf("strlen result = %d", got)
	}
	if machine.LastResult().Reason != cpu.StopBudget ||
		machine.LastResult().PC != machine.info.EntryPoint&^1 {
		t.Fatalf("public dispatch result = %+v", machine.LastResult())
	}
	stats, ok := machine.WIPIFrameStats()
	if !ok || stats.APICalls != 1 || stats.ImplementedCalls != 1 ||
		stats.LastAPI != "strlen" {
		t.Fatalf("public WIPI stats = %+v, %v", stats, ok)
	}
	coverage, ok := machine.WIPIAPICoverage()
	if !ok || coverage.Cataloged != 239 || coverage.DispatchWired != 239 ||
		coverage.SemanticallyModeled != 239 ||
		coverage.Observed != 1 {
		t.Fatalf("public WIPI coverage = %+v, %v", coverage, ok)
	}
	if got := machine.WIPIObservedAPIs(); !slices.Equal(got, []string{"strlen"}) {
		t.Fatalf("observed public WIPI APIs = %v", got)
	}
	machine.raptor = &raptorrt.Runtime{Public: machine.wipi}
	machine.wipi.Observed["RAPTOR.sndCreate"] = 1
	if got := machine.WIPIObservedAPIs(); !slices.Equal(got, []string{
		"MC_mdaClipCreate",
		"strlen",
	}) {
		t.Fatalf("Raptor-observed public WIPI APIs = %v", got)
	}
}

func TestKTFWIPIFrameStatsCountSampledHostCalls(t *testing.T) {
	runtime := &ktfrt.Runtime{}
	if err := runtime.SetTraceMode(ktfrt.KTFTraceCounters); err != nil {
		t.Fatal(err)
	}
	entry := "java.method.org/kwis/msp/lcdui/Graphics.setRGBPixels(IIII[III)V"
	total := ktfrt.HostTraceSampleInterval + 1
	for range total {
		runtime.TraceHostCall(entry)
	}
	machine := &Machine{ktf: runtime}
	stats, ok := machine.WIPIFrameStats()
	if !ok {
		t.Fatal("KTF WIPI stats are absent")
	}
	if stats.APICalls != uint64(total) ||
		stats.ImplementedCalls != uint64(total) {
		t.Fatalf("KTF WIPI stats = %+v, want %d calls", stats, total)
	}
}

func TestStepFrameAdvancesClockAndInvokesDueWIPITimer(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callbackAddress = uint32(0x04000000)
	if err := machine.cpu.Map(
		callbackAddress,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callbackAddress, []byte{
		0x2a, 0x22, // movs r2, #42
		0x0a, 0x60, // str r2, [r1]
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	timer, err := machine.wipi.Heap.Allocate(28, true)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := machine.wipi.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, machine.wipi, "MC_knlDefTimer", timer, callbackAddress|1)
	if result := dispatchPublicAPI(
		t,
		machine.wipi,
		"MC_knlSetTimer",
		timer,
		0,
		16,
		0,
		marker,
	); result.Low != 0 {
		t.Fatalf("MC_knlSetTimer = %d", int32(result.Low))
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterR2, 0x99); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterPC, machine.info.EntryPoint&^1); err != nil {
		t.Fatal(err)
	}

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err := machine.wipi.ReadU32(marker)
	if err != nil || value != 42 {
		t.Fatalf("timer callback marker = %d, %v", value, err)
	}
	if machine.wipi.TickMS != 16 || len(machine.wipi.Timers) != 0 {
		t.Fatalf("timer runtime = tick %d, timers %v", machine.wipi.TickMS, machine.wipi.Timers)
	}
	active, err := machine.wipi.ReadU32(timer + 24)
	if err != nil || active != 0 {
		t.Fatalf("timer active flag = %d, %v", active, err)
	}
	if got := register(t, machine, cpu.RegisterR2); got != 0x99 {
		t.Fatalf("callback leaked r2 = 0x%08x", got)
	}
	if machine.LastResult().Reason != cpu.StopBudget ||
		machine.LastResult().PC != machine.info.EntryPoint&^1+2 {
		t.Fatalf("post-callback main execution = %+v", machine.LastResult())
	}

	if err := machine.wipi.WriteU32(marker, 0); err != nil {
		t.Fatal(err)
	}
	machine.wipi.EnqueueCallback(callbackAddress|1, 0, marker)
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err = machine.wipi.ReadU32(marker)
	if err != nil || value != 42 || len(machine.wipi.PendingCallbacks) != 0 {
		t.Fatalf(
			"queued callback = marker %d, pending %v, %v",
			value,
			machine.wipi.PendingCallbacks,
			err,
		)
	}
}

func TestWIPISynchronousGuestCallbackReturnsR0AndRestoresContext(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callbackAddress = uint32(0x04000000)
	if err := machine.cpu.Map(
		callbackAddress,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callbackAddress, []byte{
		0x2a, 0x20, // movs r0, #42
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterR0, 0x11223344); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterPC, machine.info.EntryPoint&^1); err != nil {
		t.Fatal(err)
	}

	value, err := machine.wipi.CallGuestFunction(callbackAddress|1, 7, 8, 9)
	if err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatalf("callback return value = %d", value)
	}
	if got := register(t, machine, cpu.RegisterR0); got != 0x11223344 {
		t.Fatalf("callback leaked r0 = 0x%08x", got)
	}
	if got := register(t, machine, cpu.RegisterPC); got != machine.info.EntryPoint&^1 {
		t.Fatalf("callback leaked PC = 0x%08x", got)
	}
}

func TestMachineValidatesInputAndReturnsFramebufferSnapshot(t *testing.T) {
	data := syntheticEADS()
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "synthetic.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.QueueInput(machinecore.InputEvent{}); err == nil {
		t.Fatal("QueueInput accepted an empty control")
	}

	first := created.Framebuffer()
	mutable, ok := first.(interface {
		Set(int, int, color.Color)
	})
	if !ok {
		t.Fatalf("Framebuffer type %T is not mutable for isolation test", first)
	}
	mutable.Set(0, 0, color.White)
	if got := created.Framebuffer().At(0, 0); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("machine framebuffer was modified through snapshot: %v", got)
	}
}

func TestSaveStateRoundTripRestoresSerializableMachineState(t *testing.T) {
	machine := newSyntheticMachine(t)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(machine.info.BSSAddress, []byte{0x5a}); err != nil {
		t.Fatal(err)
	}
	machine.frame.SetRGBA(3, 4, color.RGBA{R: 1, G: 2, B: 3, A: 4})
	event := machinecore.InputEvent{
		Control: "up",
		Pressed: true,
		At:      25 * time.Millisecond,
	}
	if err := machine.QueueInput(event); err != nil {
		t.Fatal(err)
	}

	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(machine.info.BSSAddress, []byte{0}); err != nil {
		t.Fatal(err)
	}
	machine.frame.SetRGBA(3, 4, color.RGBA{})
	machine.input = nil

	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("restored state = %s, want paused", machine.State())
	}
	if got := register(t, machine, cpu.RegisterR0); got != 0 {
		t.Fatalf("restored r0 = %d, want 0", got)
	}
	var restoredBSS [1]byte
	if err := machine.cpu.ReadMemory(machine.info.BSSAddress, restoredBSS[:]); err != nil {
		t.Fatal(err)
	}
	if restoredBSS[0] != 0x5a {
		t.Fatalf("restored BSS byte = %#x, want 0x5a", restoredBSS[0])
	}
	if got := machine.frame.RGBAAt(3, 4); got != (color.RGBA{R: 1, G: 2, B: 3, A: 4}) {
		t.Fatalf("restored framebuffer pixel = %v", got)
	}
	if len(machine.input) != 1 || machine.input[0] != event {
		t.Fatalf("restored input = %+v", machine.input)
	}
	if machine.LastResult().Reason != cpu.StopBudget ||
		machine.LastResult().Instructions != 1 {
		t.Fatalf("restored result = %+v", machine.LastResult())
	}
}

func TestSaveStateRoundTripRestoresPublicWIPIRuntime(t *testing.T) {
	machine := newSyntheticMachine(t)
	allocation := dispatchPublicAPI(t, machine.wipi, "MC_knlAlloc", 32).Low
	if allocation == 0 {
		t.Fatal("public allocation is null")
	}
	if err := machine.cpu.WriteMemory(allocation, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	machine.wipi.Files["/private/save.dat"] = []byte("state")
	machine.wipi.FileTimes["/private/save.dat"] = uint32(wipirt.EpochUnix)
	resourceID := machine.wipi.RegisterResource("saved.bin", []byte{5, 6, 7})
	if resourceID < 1 {
		t.Fatalf("resource ID = %d", resourceID)
	}
	programID := machine.wipi.RegisterProgram(
		"saved-app",
		"Saved App",
		"2.0",
		"ARAM",
		wipirt.ProgramTypeCApp,
		wipirt.DefaultAccessLevel,
	)
	if programID < 1 {
		t.Fatalf("program ID = %d", programID)
	}
	machine.wipi.Programs[programID].Running = true
	machine.wipi.Programs[programID].ParentID = machine.wipi.CurrentProgram
	machine.wipi.LastExecuteName = "saved-app"
	machine.wipi.LastExecuteArgs = []string{"--restore"}
	machine.wipi.LastExecuted = programID
	machine.wipi.GraphicsEvents = []wipirt.GraphicsEvent{{
		ID: 7, Kind: 8, Param1: 9, Param2: 10,
	}}
	machine.wipi.EnqueueCallback(0x02000003, 1, 2, 3, 4)
	machine.wipi.Databases["0:scores"] = &wipirt.Database{
		Name:       "scores",
		RecordSize: 4,
		NextRecord: 1,
		Records:    map[int32][]byte{0: {7, 8, 9, 10}},
	}
	machine.wipi.DatabaseHandles[1] = "0:scores"
	machine.wipi.NextDatabase = 2
	uicContext, err := machine.wipi.Heap.Allocate(64, true)
	if err != nil {
		t.Fatal(err)
	}
	uicClass, err := machine.wipi.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	uicComponent, err := machine.wipi.Heap.Allocate(128, true)
	if err != nil {
		t.Fatal(err)
	}
	machine.wipi.UicContexts[uicContext] = true
	machine.wipi.UicClasses["Label"] = uicClass
	machine.wipi.UicClassNames[uicClass] = "Label"
	machine.wipi.UicComponents[uicComponent] = &wipirt.Component{
		Handle:     uicComponent,
		ClassName:  "Label",
		Enabled:    true,
		Callbacks:  make(map[int32]wipirt.UICallback),
		Label:      []byte("saved label"),
		ActiveMenu: -1,
		ActiveList: -1,
		MaxText:    4096,
	}
	machine.wipi.UicRepaints = []wipirt.UICRepaint{{
		Component: uicComponent,
		X:         1,
		Y:         2,
		Width:     3,
		Height:    4,
	}}
	screen := dispatchPublicAPI(t, machine.wipi, "MC_grpGetScreenFrameBuffer", 0).Low
	dispatchPublicAPI(t, machine.wipi, "MC_grpFlushLcd", 0, screen, 0, 0, 240, 320)

	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(allocation, []byte{9, 9, 9, 9}); err != nil {
		t.Fatal(err)
	}
	machine.wipi.Stats = WIPIFrameStats{}
	machine.wipi.Framebuffers = make(map[uint32]wipirt.Framebuffer)
	machine.wipi.Resources = make(map[string]*wipirt.Resource)
	machine.wipi.ResourceIDs = make(map[int32]string)
	machine.wipi.Programs = wipirt.DefaultPrograms()
	machine.wipi.LastExecuteArgs = nil
	machine.wipi.GraphicsEvents = nil
	machine.wipi.UicRepaints = nil
	machine.wipi.PendingCallbacks = nil
	machine.frame.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})

	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	var restored [4]byte
	if err := machine.cpu.ReadMemory(allocation, restored[:]); err != nil {
		t.Fatal(err)
	}
	if restored != [4]byte{1, 2, 3, 4} {
		t.Fatalf("restored public heap = %v", restored)
	}
	if machine.wipi.Stats.PresentCount != 1 ||
		machine.wipi.Stats.APICalls != 3 ||
		machine.wipi.Framebuffers[screen].Handle != screen {
		t.Fatalf("restored public runtime = stats %+v, framebuffers %+v",
			machine.wipi.Stats, machine.wipi.Framebuffers)
	}
	if got := string(machine.wipi.Files["/private/save.dat"]); got != "state" {
		t.Fatalf("restored public filesystem = %q", got)
	}
	resource := machine.wipi.Resources["saved.bin"]
	if resource == nil || resource.Id != resourceID ||
		!bytes.Equal(resource.Data, []byte{5, 6, 7}) {
		t.Fatalf("restored public resource = %+v", resource)
	}
	program := machine.wipi.Programs[programID]
	if program == nil || !program.Running || program.ExecName != "saved-app" ||
		program.ParentID != 1 || machine.wipi.LastExecuted != programID ||
		machine.wipi.LastExecuteName != "saved-app" ||
		len(machine.wipi.LastExecuteArgs) != 1 ||
		machine.wipi.LastExecuteArgs[0] != "--restore" {
		t.Fatalf(
			"restored public program = %+v, last %d/%q/%v",
			program,
			machine.wipi.LastExecuted,
			machine.wipi.LastExecuteName,
			machine.wipi.LastExecuteArgs,
		)
	}
	if len(machine.wipi.GraphicsEvents) != 1 ||
		machine.wipi.GraphicsEvents[0] != (wipirt.GraphicsEvent{
			ID: 7, Kind: 8, Param1: 9, Param2: 10,
		}) {
		t.Fatalf("restored graphics events = %+v", machine.wipi.GraphicsEvents)
	}
	if len(machine.wipi.PendingCallbacks) != 1 ||
		machine.wipi.PendingCallbacks[0].Procedure != 0x02000003 ||
		machine.wipi.PendingCallbacks[0].Args != [4]uint32{1, 2, 3, 4} {
		t.Fatalf("restored callbacks = %+v", machine.wipi.PendingCallbacks)
	}
	if got := machine.wipi.Databases["0:scores"].Records[0]; !bytes.Equal(got, []byte{7, 8, 9, 10}) {
		t.Fatalf("restored public database record = %v", got)
	}
	if got := string(machine.wipi.UicComponents[uicComponent].Label); got != "saved label" {
		t.Fatalf("restored public UI component label = %q", got)
	}
	if len(machine.wipi.UicRepaints) != 1 ||
		machine.wipi.UicRepaints[0] != (wipirt.UICRepaint{
			Component: uicComponent,
			X:         1,
			Y:         2,
			Width:     3,
			Height:    4,
		}) {
		t.Fatalf("restored public repaint trace = %+v", machine.wipi.UicRepaints)
	}
	if got := machine.frame.RGBAAt(0, 0); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("restored public frame = %#v", got)
	}
}

func TestResetRestoresInitialCPUAndMemoryState(t *testing.T) {
	machine := newSyntheticMachine(t)
	if err := machine.cpu.WriteMemory(machine.info.TextAddress, []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(machine.info.BSSAddress, []byte{0xaa}); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(DefaultStackBase, []byte{0xbb}); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	machine.frame.SetRGBA(0, 0, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	if err := machine.QueueInput(machinecore.InputEvent{Control: "fire"}); err != nil {
		t.Fatal(err)
	}

	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	var memory [2]byte
	if err := machine.cpu.ReadMemory(machine.info.TextAddress, memory[:]); err != nil {
		t.Fatal(err)
	}
	if memory != ([2]byte{0x00, 0xb5}) {
		t.Fatalf("reset text = %x", memory)
	}
	if err := machine.cpu.ReadMemory(machine.info.BSSAddress, memory[:1]); err != nil {
		t.Fatal(err)
	}
	if memory[0] != 0 {
		t.Fatalf("reset BSS = %#x", memory[0])
	}
	if err := machine.cpu.ReadMemory(DefaultStackBase, memory[:1]); err != nil {
		t.Fatal(err)
	}
	if memory[0] != 0 {
		t.Fatalf("reset stack = %#x", memory[0])
	}
	if got := register(t, machine, cpu.RegisterR0); got != 0 {
		t.Fatalf("reset r0 = %d", got)
	}
	if len(machine.input) != 0 ||
		machine.frame.RGBAAt(0, 0) != (color.RGBA{A: 0xff}) ||
		machine.State() != machinecore.StateReady {
		t.Fatalf("reset observable state = input %v pixel %v state %s",
			machine.input, machine.frame.RGBAAt(0, 0), machine.State())
	}
}

func TestResetPreservesPublicWIPIPersistence(t *testing.T) {
	machine := newSyntheticMachine(t)
	runtime := machine.wipi
	if err := runtime.Services.Storage.MakeDirectory(
		shared.NamespacePrivate,
		"/saves",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/saves/slot.dat",
		[]byte("persistent WIPI file"),
	); err != nil {
		t.Fatal(err)
	}
	runtime.Directories["/private/saves"] = true
	runtime.Files["/private/saves/slot.dat"] = []byte("persistent WIPI file")
	runtime.FileTimes["/private/saves"] = 123
	runtime.FileTimes["/private/saves/slot.dat"] = 456
	if err := runtime.Services.Storage.WriteFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
		[]byte("temporary"),
	); err != nil {
		t.Fatal(err)
	}
	const databaseKey = "0:scores"
	database := &wipirt.Database{
		Name:       "scores",
		RecordSize: 4,
		Mode:       0,
		NextRecord: 2,
		Records: map[int32][]byte{
			1: {4, 3, 2, 1},
		},
	}
	databaseService, err := runtime.Services.Storage.CreateRecordStore(
		runtime.ServiceOwner,
		databaseKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Services.Storage.ReplaceRecords(
		runtime.ServiceOwner,
		databaseService,
		2,
		map[uint32][]byte{1: {4, 3, 2, 1}},
	); err != nil {
		t.Fatal(err)
	}
	runtime.Databases[databaseKey] = database
	runtime.DatabaseServices[databaseKey] = databaseService
	if err := runtime.Services.Advance(
		runtime.ServiceOwner,
		25*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	runtime.TickMS = 25

	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, err := machine.wipi.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/saves/slot.dat",
	)
	if err != nil || string(persisted) != "persistent WIPI file" ||
		string(machine.wipi.Files["/private/saves/slot.dat"]) !=
			"persistent WIPI file" ||
		!machine.wipi.Directories["/private/saves"] ||
		machine.wipi.FileTimes["/private/saves"] != 123 ||
		machine.wipi.FileTimes["/private/saves/slot.dat"] != 456 {
		t.Fatalf(
			"reset WIPI persistence = file %q mirror %q directory %t times %d/%d, %v",
			persisted,
			machine.wipi.Files["/private/saves/slot.dat"],
			machine.wipi.Directories["/private/saves"],
			machine.wipi.FileTimes["/private/saves"],
			machine.wipi.FileTimes["/private/saves/slot.dat"],
			err,
		)
	}
	if _, err := machine.wipi.Services.Storage.ReadFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
	); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("reset WIPI temporary file error = %v", err)
	}
	persistedDatabase := machine.wipi.Databases[databaseKey]
	persistedService, err := machine.wipi.Services.Storage.OpenRecordStore(
		machine.wipi.ServiceOwner,
		databaseKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	persistedRecord, err := machine.wipi.Services.Storage.Record(
		machine.wipi.ServiceOwner,
		persistedService,
		1,
	)
	if err != nil || persistedDatabase == nil ||
		persistedDatabase.NextRecord != 2 ||
		!bytes.Equal(persistedDatabase.Records[1], []byte{4, 3, 2, 1}) ||
		!bytes.Equal(persistedRecord, []byte{4, 3, 2, 1}) {
		t.Fatalf(
			"reset WIPI database = %+v, service record %v, %v",
			persistedDatabase,
			persistedRecord,
			err,
		)
	}
	if machine.wipi.TickMS != 0 ||
		machine.wipi.Services.Clock.Monotonic() != 0 ||
		machine.State() != machinecore.StateReady {
		t.Fatalf(
			"reset WIPI transient state = tick %d clock %s lifecycle %s",
			machine.wipi.TickMS,
			machine.wipi.Services.Clock.Monotonic(),
			machine.State(),
		)
	}
}

func TestLoadStateRejectsCorruptionBeforeMutation(t *testing.T) {
	machine := newSyntheticMachine(t)
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), saved.Bytes()...)
	corrupt[len(corrupt)/2] ^= 0xff
	if err := machine.cpu.WriteRegister(cpu.RegisterR0, 77); err != nil {
		t.Fatal(err)
	}
	if err := machine.LoadState(bytes.NewReader(corrupt)); err == nil {
		t.Fatal("LoadState accepted a corrupt checksum")
	}
	if got := register(t, machine, cpu.RegisterR0); got != 77 {
		t.Fatalf("failed LoadState changed r0 to %d", got)
	}
}

func TestLoadStateRejectsAnotherSource(t *testing.T) {
	first := newSyntheticMachine(t)
	var saved bytes.Buffer
	if err := first.SaveState(&saved); err != nil {
		t.Fatal(err)
	}

	data := syntheticEADS()
	data[0] = 1
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "other.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	second := created.(*Machine)
	t.Cleanup(func() { _ = second.Close() })
	if err := second.LoadState(bytes.NewReader(saved.Bytes())); err == nil {
		t.Fatal("LoadState accepted a state from another source")
	}
}

func TestPausePreservesPausedStateDuringActiveRun(t *testing.T) {
	data := syntheticEADS()
	copy(data[len(data)-2:], []byte{0xfe, 0xe7}) // b .
	factory := NewFactory()
	factory.RunBudget = math.MaxUint64
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "loop.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })

	finished := make(chan error, 1)
	go func() {
		finished <- machine.Start(context.Background())
	}()
	deadline := time.Now().Add(time.Second)
	for machine.State() != machinecore.StateRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatal("machine did not enter running state")
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active run did not stop after Pause")
	}
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("state after Pause = %s, want paused", machine.State())
	}
}

func TestMagicholeReferenceEADSEntryPoint(t *testing.T) {
	reference := os.Getenv("ARAM_REFERENCE_REPO")
	if reference == "" {
		t.Skip("ARAM_REFERENCE_REPO is not set")
	}
	path := filepath.Join(
		reference,
		"SCH-W380_DL21",
		"SCH-W830_DL21_29360128_DL21.dat",
	)
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("reference DAT is unavailable: %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		Format:   "wipi-dat",
		ReaderAt: file,
		Size:     info.Size(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Close()
	machine := created.(*Machine)
	image := machine.ImageInfo()
	if image.Name != "MinigameQVGAOEM" ||
		image.EntryPoint != 0xf4000001 ||
		image.ProfileID != minigame.ProfileID {
		t.Fatalf("reference image = %+v", image)
	}
	if err := created.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, ok := machine.EADSFrameStats()
	if !ok {
		t.Fatal("reference title did not select the EADS runtime")
	}
	expectedEvents := []EADSEventResult{
		{Event: minigame.BootstrapEvent, Instructions: 1771, APICalls: 46, ReturnValue: 0},
		{Event: minigame.SetupEvent, Instructions: 160, APICalls: 16, ReturnValue: 1},
		{Event: minigame.StartEvent, Instructions: 1958, APICalls: 1, ReturnValue: 0},
		{Event: minigame.FrameEvent, Instructions: 194, APICalls: 4, ReturnValue: 0},
		{Event: minigame.FrameEvent, Instructions: 36045, APICalls: 308, ReturnValue: 0},
	}
	if len(stats.Events) != len(expectedEvents) {
		t.Fatalf("EADS event count = %d, want %d", len(stats.Events), len(expectedEvents))
	}
	for index, expected := range expectedEvents {
		if stats.Events[index] != expected {
			t.Fatalf("EADS event %d = %+v, want %+v", index, stats.Events[index], expected)
		}
	}
	if stats.PresentCount != 2 || stats.TickMS != 32 {
		t.Fatalf("EADS presentation stats = %+v", stats)
	}
	frame := machine.Framebuffer().(*stdimage.RGBA)
	if digest := fmt.Sprintf("%x", sha256.Sum256(frame.Pix)); digest !=
		"0ae34e616ac40a0dab1e35d907acfef63fb47bd2b065875f17631f0bbeb915a7" {
		t.Fatalf("EADS RGBA SHA-256 = %s", digest)
	}
	result := machine.LastResult()
	if result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 36045 ||
		result.PC != guest.ReturnSentinel {
		t.Fatalf("reference entry execution = %+v", result)
	}
	var state bytes.Buffer
	if err := machine.SaveState(&state); err != nil {
		t.Fatal(err)
	}
	firstFrameDigest := sha256.Sum256(frame.Pix)
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	resetStats, ok := machine.EADSFrameStats()
	if !ok || len(resetStats.Events) != 0 ||
		resetStats.PresentCount != 0 || resetStats.TickMS != 0 {
		t.Fatalf("reset EADS stats = %+v, present %v", resetStats, ok)
	}
	if err := machine.LoadState(bytes.NewReader(state.Bytes())); err != nil {
		t.Fatal(err)
	}
	if got := register(t, machine, cpu.RegisterPC); got != guest.ReturnSentinel {
		t.Fatalf("restored reference pc = 0x%08x, want 0x%08x", got, guest.ReturnSentinel)
	}
	restoredFrame := machine.Framebuffer().(*stdimage.RGBA)
	if restoredDigest := sha256.Sum256(restoredFrame.Pix); restoredDigest != firstFrameDigest {
		t.Fatalf("restored first-frame digest = %x, want %x",
			restoredDigest, firstFrameDigest)
	}
	restoredStats, _ := machine.EADSFrameStats()
	if len(restoredStats.Events) != len(expectedEvents) ||
		restoredStats.PresentCount != stats.PresentCount ||
		restoredStats.TickMS != stats.TickMS {
		t.Fatalf("restored EADS stats = %+v, want %+v", restoredStats, stats)
	}

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayedOnce := sha256.Sum256(machine.Framebuffer().(*stdimage.RGBA).Pix)
	onceStats, _ := machine.EADSFrameStats()
	onceEvent := onceStats.Events[len(onceStats.Events)-1]
	if err := machine.LoadState(bytes.NewReader(state.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayedTwice := sha256.Sum256(machine.Framebuffer().(*stdimage.RGBA).Pix)
	twiceStats, _ := machine.EADSFrameStats()
	twiceEvent := twiceStats.Events[len(twiceStats.Events)-1]
	if replayedTwice != replayedOnce || twiceEvent != onceEvent {
		t.Fatalf(
			"save-state replay diverged: digest %x/%x event %+v/%+v",
			replayedOnce,
			replayedTwice,
			onceEvent,
			twiceEvent,
		)
	}
}

func TestRaptorJavaSaveStateFailsBeforeWriting(t *testing.T) {
	machine := newSyntheticMachine(t)
	var valid bytes.Buffer
	if err := machine.SaveState(&valid); err != nil {
		t.Fatal(err)
	}
	machine.raptor = &raptorrt.Runtime{Java: &raptorrt.JavaRuntime{}}

	var rejected bytes.Buffer
	err := machine.SaveState(&rejected)
	if err == nil || !strings.Contains(err.Error(), "Raptor Java adapter") {
		t.Fatalf("SaveState error = %v", err)
	}
	if rejected.Len() != 0 {
		t.Fatalf("SaveState wrote %d bytes before rejecting Java state", rejected.Len())
	}
	err = machine.LoadState(bytes.NewReader(valid.Bytes()))
	if err == nil || !strings.Contains(err.Error(), "Raptor Java adapter") {
		t.Fatalf("LoadState error = %v", err)
	}
}

func newSyntheticMachine(t *testing.T) *Machine {
	t.Helper()
	data := syntheticEADS()
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "synthetic.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}

func syntheticEADS() []byte {
	const offset = 0x80
	data := make([]byte, offset+0x30+4)
	copy(data[offset:], "EADS")
	binary.LittleEndian.PutUint32(data[offset+4:], 1)
	binary.LittleEndian.PutUint32(data[offset+8:], 1)
	binary.LittleEndian.PutUint32(data[offset+12:], 0x02000000)
	binary.LittleEndian.PutUint32(data[offset+16:], 4)
	binary.LittleEndian.PutUint32(data[offset+20:], 0x03000000)
	binary.LittleEndian.PutUint32(data[offset+24:], 0x1000)
	copy(data[offset+0x20:], "SyntheticEADS")
	copy(data[offset+0x30:], []byte{
		0x00, 0xb5, // push {lr}
		0x00, 0xbe, // bkpt #0
	})
	return data
}

func testZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, payload := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testOMADCF(algorithm uint16, plaintextBytes uint64, object []byte) []byte {
	const contentID = "00WIPI00000000000001020304"
	var common bytes.Buffer
	common.Write(make([]byte, 4))
	_ = binary.Write(&common, binary.BigEndian, algorithm)
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	_ = binary.Write(&common, binary.BigEndian, plaintextBytes)
	_ = binary.Write(&common, binary.BigEndian, uint16(len(contentID)))
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	common.WriteString(contentID)
	ohdr := testOMADCFBox("ohdr", common.Bytes())

	contentType := []byte("application/java-archive")
	var headers bytes.Buffer
	headers.Write(make([]byte, 4))
	headers.WriteByte(byte(len(contentType)))
	headers.Write(contentType)
	headers.Write(ohdr)
	odhe := testOMADCFBox("odhe", headers.Bytes())

	var content bytes.Buffer
	content.Write(make([]byte, 4))
	_ = binary.Write(&content, binary.BigEndian, uint64(len(object)))
	content.Write(object)
	odda := testOMADCFBox("odda", content.Bytes())

	var container bytes.Buffer
	container.Write(make([]byte, 4))
	container.Write(odhe)
	container.Write(odda)
	odrm := testOMADCFBox("odrm", container.Bytes())
	return append([]byte("odcf\x00\x02\x00\x00"), odrm...)
}

func testOMADCFBox(kind string, payload []byte) []byte {
	output := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(output, uint32(8+len(payload)))
	copy(output[4:], kind)
	return append(output, payload...)
}

func register(t *testing.T, machine *Machine, id uint32) uint32 {
	t.Helper()
	value, err := machine.ReadRegister(id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func syntheticKTFClient() []byte {
	client := make([]byte, 0x200)
	copy(client, []byte{
		0x00, 0x48, // ldr r0, [pc, #0]
		0x70, 0x47, // bx lr
	})
	binary.LittleEndian.PutUint32(client[4:8], ktfrt.ImageBase+0x100)
	copy(client[0x20:], []byte{
		0x00, 0x20, // movs r0, #0
		0x70, 0x47, // bx lr
	})
	binary.LittleEndian.PutUint32(client[0x100:], ktfrt.ImageBase+0x140)
	binary.LittleEndian.PutUint32(client[0x104:], ktfrt.ImageBase+0x180)
	binary.LittleEndian.PutUint32(client[0x114:], (ktfrt.ImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x140:], ktfrt.ImageBase+0x160)
	binary.LittleEndian.PutUint32(client[0x168:], (ktfrt.ImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x170:], (ktfrt.ImageBase+0x20)|1)
	copy(client[0x180:], "SyntheticKTF\x00")
	return client
}
