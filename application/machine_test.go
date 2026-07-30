package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	stdimage "image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/raptor"
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
	if resource := machine.wipi.resources["config.bin"]; resource == nil ||
		!bytes.Equal(resource.data, []byte{1, 2, 3}) {
		t.Fatalf("installed resource = %+v", resource)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resource := machine.wipi.resources["config.bin"]; resource == nil ||
		!bytes.Equal(resource.data, []byte{1, 2, 3}) {
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

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	const source = guestHeapBase + 0x200
	if err := machine.cpu.WriteMemory(source, []byte("public-wipi\x00")); err != nil {
		t.Fatal(err)
	}
	stub, ok := machine.wipi.layout.StubByName["strlen"]
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
	timer, err := machine.wipi.heap.allocate(28, true)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := machine.wipi.heap.allocate(4, true)
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
	); result.low != 0 {
		t.Fatalf("MC_knlSetTimer = %d", int32(result.low))
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
	value, err := machine.wipi.readU32(marker)
	if err != nil || value != 42 {
		t.Fatalf("timer callback marker = %d, %v", value, err)
	}
	if machine.wipi.tickMS != 16 || len(machine.wipi.timers) != 0 {
		t.Fatalf("timer runtime = tick %d, timers %v", machine.wipi.tickMS, machine.wipi.timers)
	}
	active, err := machine.wipi.readU32(timer + 24)
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

	if err := machine.wipi.writeU32(marker, 0); err != nil {
		t.Fatal(err)
	}
	machine.wipi.enqueueCallback(callbackAddress|1, 0, marker)
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err = machine.wipi.readU32(marker)
	if err != nil || value != 42 || len(machine.wipi.pendingCallbacks) != 0 {
		t.Fatalf(
			"queued callback = marker %d, pending %v, %v",
			value,
			machine.wipi.pendingCallbacks,
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

	value, err := machine.wipi.callGuestFunction(callbackAddress|1, 7, 8, 9)
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
	allocation := dispatchPublicAPI(t, machine.wipi, "MC_knlAlloc", 32).low
	if allocation == 0 {
		t.Fatal("public allocation is null")
	}
	if err := machine.cpu.WriteMemory(allocation, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	machine.wipi.files["/private/save.dat"] = []byte("state")
	machine.wipi.fileTimes["/private/save.dat"] = uint32(wipiEpochUnix)
	resourceID := machine.wipi.registerResource("saved.bin", []byte{5, 6, 7})
	if resourceID < 1 {
		t.Fatalf("resource ID = %d", resourceID)
	}
	programID := machine.wipi.registerProgram(
		"saved-app",
		"Saved App",
		"2.0",
		"ARAM",
		wipiProgramTypeCApp,
		wipiDefaultAccessLevel,
	)
	if programID < 1 {
		t.Fatalf("program ID = %d", programID)
	}
	machine.wipi.programs[programID].running = true
	machine.wipi.programs[programID].parentID = machine.wipi.currentProgram
	machine.wipi.lastExecuteName = "saved-app"
	machine.wipi.lastExecuteArgs = []string{"--restore"}
	machine.wipi.lastExecuted = programID
	machine.wipi.graphicsEvents = []wipiGraphicsEvent{{
		id: 7, kind: 8, param1: 9, param2: 10,
	}}
	machine.wipi.enqueueCallback(0x02000003, 1, 2, 3, 4)
	machine.wipi.databases["0:scores"] = &wipiDatabase{
		name:       "scores",
		recordSize: 4,
		nextRecord: 1,
		records:    map[int32][]byte{0: {7, 8, 9, 10}},
	}
	machine.wipi.databaseHandles[1] = "0:scores"
	machine.wipi.nextDatabase = 2
	uicContext, err := machine.wipi.heap.allocate(64, true)
	if err != nil {
		t.Fatal(err)
	}
	uicClass, err := machine.wipi.heap.allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	uicComponent, err := machine.wipi.heap.allocate(128, true)
	if err != nil {
		t.Fatal(err)
	}
	machine.wipi.uicContexts[uicContext] = true
	machine.wipi.uicClasses["Label"] = uicClass
	machine.wipi.uicClassNames[uicClass] = "Label"
	machine.wipi.uicComponents[uicComponent] = &wipiComponent{
		handle:     uicComponent,
		className:  "Label",
		enabled:    true,
		callbacks:  make(map[int32]wipiUICallback),
		label:      []byte("saved label"),
		activeMenu: -1,
		activeList: -1,
		maxText:    4096,
	}
	machine.wipi.uicRepaints = []wipiUICRepaint{{
		component: uicComponent,
		x:         1,
		y:         2,
		width:     3,
		height:    4,
	}}
	screen := dispatchPublicAPI(t, machine.wipi, "MC_grpGetScreenFrameBuffer", 0).low
	dispatchPublicAPI(t, machine.wipi, "MC_grpFlushLcd", 0, screen, 0, 0, 240, 320)

	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(allocation, []byte{9, 9, 9, 9}); err != nil {
		t.Fatal(err)
	}
	machine.wipi.stats = WIPIFrameStats{}
	machine.wipi.framebuffers = make(map[uint32]wipiFramebuffer)
	machine.wipi.resources = make(map[string]*wipiResource)
	machine.wipi.resourceIDs = make(map[int32]string)
	machine.wipi.programs = defaultWIPIPrograms()
	machine.wipi.lastExecuteArgs = nil
	machine.wipi.graphicsEvents = nil
	machine.wipi.uicRepaints = nil
	machine.wipi.pendingCallbacks = nil
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
	if machine.wipi.stats.PresentCount != 1 ||
		machine.wipi.stats.APICalls != 3 ||
		machine.wipi.framebuffers[screen].handle != screen {
		t.Fatalf("restored public runtime = stats %+v, framebuffers %+v",
			machine.wipi.stats, machine.wipi.framebuffers)
	}
	if got := string(machine.wipi.files["/private/save.dat"]); got != "state" {
		t.Fatalf("restored public filesystem = %q", got)
	}
	resource := machine.wipi.resources["saved.bin"]
	if resource == nil || resource.id != resourceID ||
		!bytes.Equal(resource.data, []byte{5, 6, 7}) {
		t.Fatalf("restored public resource = %+v", resource)
	}
	program := machine.wipi.programs[programID]
	if program == nil || !program.running || program.execName != "saved-app" ||
		program.parentID != 1 || machine.wipi.lastExecuted != programID ||
		machine.wipi.lastExecuteName != "saved-app" ||
		len(machine.wipi.lastExecuteArgs) != 1 ||
		machine.wipi.lastExecuteArgs[0] != "--restore" {
		t.Fatalf(
			"restored public program = %+v, last %d/%q/%v",
			program,
			machine.wipi.lastExecuted,
			machine.wipi.lastExecuteName,
			machine.wipi.lastExecuteArgs,
		)
	}
	if len(machine.wipi.graphicsEvents) != 1 ||
		machine.wipi.graphicsEvents[0] != (wipiGraphicsEvent{
			id: 7, kind: 8, param1: 9, param2: 10,
		}) {
		t.Fatalf("restored graphics events = %+v", machine.wipi.graphicsEvents)
	}
	if len(machine.wipi.pendingCallbacks) != 1 ||
		machine.wipi.pendingCallbacks[0].procedure != 0x02000003 ||
		machine.wipi.pendingCallbacks[0].args != [4]uint32{1, 2, 3, 4} {
		t.Fatalf("restored callbacks = %+v", machine.wipi.pendingCallbacks)
	}
	if got := machine.wipi.databases["0:scores"].records[0]; !bytes.Equal(got, []byte{7, 8, 9, 10}) {
		t.Fatalf("restored public database record = %v", got)
	}
	if got := string(machine.wipi.uicComponents[uicComponent].label); got != "saved label" {
		t.Fatalf("restored public UI component label = %q", got)
	}
	if len(machine.wipi.uicRepaints) != 1 ||
		machine.wipi.uicRepaints[0] != (wipiUICRepaint{
			component: uicComponent,
			x:         1,
			y:         2,
			width:     3,
			height:    4,
		}) {
		t.Fatalf("restored public repaint trace = %+v", machine.wipi.uicRepaints)
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
		image.ProfileID != minigameProfileID {
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
		{Event: eadsBootstrapEvent, Instructions: 1771, APICalls: 46, ReturnValue: 0},
		{Event: eadsSetupEvent, Instructions: 160, APICalls: 16, ReturnValue: 1},
		{Event: eadsStartEvent, Instructions: 1958, APICalls: 1, ReturnValue: 0},
		{Event: eadsFrameEvent, Instructions: 194, APICalls: 4, ReturnValue: 0},
		{Event: eadsFrameEvent, Instructions: 36045, APICalls: 308, ReturnValue: 0},
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
		result.PC != returnSentinel {
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
	if got := register(t, machine, cpu.RegisterPC); got != returnSentinel {
		t.Fatalf("restored reference pc = 0x%08x, want 0x%08x", got, returnSentinel)
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
