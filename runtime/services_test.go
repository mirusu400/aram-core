package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServicesSnapshotRestoresCrossComponentState(t *testing.T) {
	services, err := NewServices(Config{
		RandomSeed:            42,
		TimezoneOffsetMinutes: 9 * 60,
		Locale:                "ko-KR",
	})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(3, SurfaceDescriptor{
		Width:  2,
		Height: 2,
		Format: PixelRGB565,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.SetScreen(3, surface); err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.SetPixel(3, surface, 1, 1, RGB(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if err := services.Storage.WriteFile(
		NamespacePrivate,
		"save.dat",
		[]byte("state"),
	); err != nil {
		t.Fatal(err)
	}
	timer, err := services.Timers.Define(3, "paint")
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Timers.Set(timer, 3, 20*time.Millisecond, 0, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Random.Uint64("java"); err != nil {
		t.Fatal(err)
	}
	if err := services.Input.Change(services.Events, 3, "up", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := services.Advance(3, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()

	if err := services.Graphics.Clear(3, surface, RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := services.Storage.WriteFile(NamespacePrivate, "save.dat", nil); err != nil {
		t.Fatal(err)
	}
	if err := services.AdvanceFrame(3); err != nil {
		t.Fatal(err)
	}
	if err := services.Restore(before); err != nil {
		t.Fatal(err)
	}
	if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("restored service state differs:\n got %+v\nwant %+v", after, before)
	}
}

func TestServicesRestoreRejectsMissingCrossReferenceAtomically(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  1,
		Height: 1,
		Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.SetScreen(1, surface); err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()
	invalid := services.Snapshot()
	invalid.Registry.Entries = nil
	if err := services.Restore(invalid); !errors.Is(err, ErrNotFound) &&
		!errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore invalid service graph error = %v", err)
	}
	if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("services mutated after rejected graph restore")
	}
}

func TestServicesAdvanceRollsBackWhenEventQueueIsFull(t *testing.T) {
	config := DefaultConfig()
	config.Limits.MaxEvents = 1
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := services.Timers.Define(1, "due")
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Timers.Set(timer, 1, time.Millisecond, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Events.Enqueue(Event{
		At:    0,
		Kind:  EventApplication,
		Owner: 1,
	}); err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()
	if err := services.Advance(1, time.Millisecond); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Advance with full queue error = %v", err)
	}
	if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed service advance was not atomic")
	}
}

func TestServicesRestoreDoesNotRestoreObservationalTrace(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	services.Trace.SetEnabled(true)
	services.Trace.Record(TraceEvent{Runtime: "test", Category: "test", Name: "before"})
	state := services.Snapshot()
	services.Trace.Record(TraceEvent{Runtime: "test", Category: "test", Name: "after"})
	if err := services.Restore(state); err != nil {
		t.Fatal(err)
	}
	events := services.Trace.Events()
	if len(events) != 2 || events[1].Name != "after" {
		t.Fatalf("semantic restore changed observational trace: %+v", events)
	}
}

func TestServicesSnapshotDoesNotAliasConfigurationSlices(t *testing.T) {
	config := DefaultConfig()
	config.Device.Properties = []DeviceProperty{{Name: "model", Value: "before"}}
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	state := services.Snapshot()
	state.Config.Device.Properties[0].Value = "after"
	if got := services.Config.Device.Properties[0].Value; got != "before" {
		t.Fatalf("snapshot mutation changed live configuration to %q", got)
	}
}

func TestServicesRestoreRejectsOrphanRegistryEntryAtomically(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()
	invalid := services.Snapshot()
	id := makeServiceID(1, 1)
	invalid.Registry.NextSlot = 2
	invalid.Registry.Generations = []RegistryGenerationState{{
		Slot: 1, Generation: 1,
	}}
	invalid.Registry.Entries = []RegistryEntryState{{
		ID: id, Kind: KindSocket, Owner: 1, Refs: 1,
	}}
	if err := services.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore orphan registry entry error = %v", err)
	}
	if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("orphan registry entry changed live services")
	}
}

func TestZeroValueServicesCanRestoreCompleteState(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Storage.WriteFile(
		NamespacePrivate,
		"save.dat",
		[]byte("persisted"),
	); err != nil {
		t.Fatal(err)
	}
	state := services.Snapshot()
	var restored Services
	if err := restored.Restore(state); err != nil {
		t.Fatal(err)
	}
	if restored.Trace == nil ||
		!reflect.DeepEqual(restored.Snapshot(), state) {
		t.Fatal("zero-value Services did not restore the complete graph")
	}
}

func TestServicesRestoreRejectsQueuedEventServiceKindAtomically(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	timer, err := services.Timers.Define(4, "callback")
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Timers.Set(timer, 4, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := services.Timers.Advance(0, services.Events); err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(4, SurfaceDescriptor{
		Width: 1, Height: 1, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()
	invalid := services.Snapshot()
	invalid.Events.Events[0].ServiceID = surface
	if err := services.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore mismatched queued event error = %v", err)
	}
	if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected queued event graph mutated services")
	}
}

func TestServicesConfigurationIdentityIsCanonical(t *testing.T) {
	config := Config{
		Locale:                "en-US",
		TimezoneOffsetMinutes: -5 * 60,
	}
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	if services.Config.Device.Locale != config.Locale ||
		services.Config.Device.TimezoneMins != config.TimezoneOffsetMinutes ||
		services.Config.ProfileHash == [32]byte{} {
		t.Fatalf("normalized service configuration = %+v", services.Config)
	}

	same, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	if same.Config.ProfileHash != services.Config.ProfileHash {
		t.Fatal("equivalent service configurations produced different hashes")
	}

	changed := config
	changed.Device = services.Config.Device
	changed.Device.Quirks = []DeviceQuirk{{Name: "title-fix", Enabled: true}}
	changedServices, err := NewServices(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedServices.Config.ProfileHash == services.Config.ProfileHash {
		t.Fatal("profile quirk did not affect configuration identity")
	}

	tampered := services.Config
	tampered.ProfileHash[0] ^= 0xff
	if _, err := NewServices(tampered); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewServices tampered profile hash error = %v", err)
	}
}

func TestServicesRejectsDeviceScreenOutsideGraphicsLimits(t *testing.T) {
	config := DefaultConfig()
	config.Limits.Graphics.MaxWidth = config.Device.ScreenWidth - 1
	if _, err := NewServices(config); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewServices oversized device screen error = %v", err)
	}
}
