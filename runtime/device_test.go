package runtime

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/profile"
)

func TestDeviceTimedStateAndRequestsRoundTrip(t *testing.T) {
	device, err := NewDevice(DeviceConfig{}, DeviceLimits{})
	check(t, err)
	check(t, device.Vibrate(75, 100*time.Millisecond, time.Second))
	check(t, device.SetBacklight(true, 200*time.Millisecond, time.Second))
	device.SetNetworkAvailable(true)
	if _, err := device.Request(
		4,
		RequestBrowser,
		"https://example.invalid/",
		[]byte("opaque"),
		time.Second,
	); err != nil {
		t.Fatal(err)
	}
	state := device.Snapshot()
	clone, err := NewDevice(DeviceConfig{}, DeviceLimits{})
	check(t, err)
	check(t, clone.Restore(state))
	if !reflect.DeepEqual(clone.Snapshot(), state) {
		t.Fatal("device state did not round-trip")
	}
	if !clone.NetworkAvailable() {
		t.Fatal("network availability did not round-trip")
	}
	check(t, clone.Advance(1150*time.Millisecond))
	level, _ := clone.Vibration()
	backlight, _ := clone.Backlight()
	if level != 0 || !backlight {
		t.Fatalf("timed state at 1.15s = vibration %d backlight %v", level, backlight)
	}
	check(t, clone.Advance(1250*time.Millisecond))
	backlight, _ = clone.Backlight()
	if backlight {
		t.Fatal("backlight remained on past deadline")
	}
}

func TestConfigFromProfileCarriesCapabilitiesAndLimits(t *testing.T) {
	resolved := profile.Profile{
		ID:          "device/example",
		Version:     profile.Version121,
		Carrier:     profile.CarrierSKT,
		Screen:      profile.Screen{Width: 176, Height: 220},
		TitleSHA256: strings.Repeat("ab", 32),
		Layers:      []string{"standard", "carrier", "title"},
		Properties:  map[string]string{"microedition.locale": "ko-KR"},
		Capabilities: profile.Capabilities{
			profile.CapabilityAudio:   true,
			profile.CapabilityNetwork: false,
		},
		Quirks: map[string]bool{
			"legacy-timer": true,
		},
		Limits: profile.Limits{
			profile.LimitSurfaceCount: 12,
			profile.LimitStorageBytes: 1024,
			profile.LimitSockets:      7,
			profile.LimitHTTPRequests: 5,
			profile.LimitSerialPorts:  3,
			profile.LimitTimers:       19,
			profile.LimitAssets:       23,
		},
	}
	config, err := ConfigFromProfile(resolved)
	check(t, err)
	if config.Device.ScreenWidth != 176 ||
		config.Device.ScreenHeight != 220 ||
		config.Device.TitleSHA256 != strings.Repeat("ab", 32) ||
		!reflect.DeepEqual(
			config.Device.ProfileLayers,
			[]string{"standard", "carrier", "title"},
		) ||
		config.Limits.Graphics.MaxSurfaces != 12 ||
		config.Limits.Storage.MaxStorageBytes != 1024 ||
		config.Limits.Network.MaxSockets != 7 ||
		config.Limits.Network.MaxHTTPRequests != 5 ||
		config.Limits.Network.MaxSerialPorts != 3 ||
		config.Limits.MaxTimers != 19 ||
		config.Limits.Assets.MaxAssets != 23 ||
		!config.Device.Capabilities[0].Enabled ||
		len(config.Device.Quirks) != 1 ||
		config.Device.Quirks[0] != (DeviceQuirk{
			Name: "legacy-timer", Enabled: true,
		}) {
		t.Fatalf("resolved service config = %+v", config)
	}

	other := resolved
	other.TitleSHA256 = strings.Repeat("cd", 32)
	otherConfig, err := ConfigFromProfile(other)
	check(t, err)
	if otherConfig.ProfileHash == config.ProfileHash {
		t.Fatal("title SHA-256 did not affect service profile identity")
	}
}

func TestDeviceStatusResponseRecordsAndDrivesPlayback(t *testing.T) {
	config := DefaultConfig()
	config.ReplayMode = ReplayRecord
	recorded, err := NewServices(config)
	check(t, err)
	check(t, recorded.UpdateDeviceStatus(
		3,
		72,
		45,
		true,
		time.Second,
	))
	log := recorded.Replay.Snapshot()
	log.Mode = ReplayPlayback
	log.Cursor = 0

	playbackConfig := config
	playbackConfig.ReplayMode = ReplayPlayback
	playback, err := NewServices(playbackConfig)
	check(t, err)
	check(t, playback.Replay.Restore(log))
	check(t, playback.UpdateDeviceStatus(
		3,
		1,
		2,
		false,
		time.Second,
	))
	battery, signal, available := playback.Device.Status()
	if battery != 72 || signal != 45 || !available {
		t.Fatalf(
			"replayed device status = %d/%d/%v",
			battery,
			signal,
			available,
		)
	}
}

func TestConfigFromProfileRetainsDefaultsForUnspecifiedIdentityFields(t *testing.T) {
	config, err := ConfigFromProfile(profile.Profile{
		ID:     "device/minimal",
		Screen: profile.Screen{Width: 240, Height: 320},
	})
	check(t, err)
	defaults := DefaultDeviceConfig()
	if config.Device.WIPIVersion != defaults.WIPIVersion ||
		config.Device.Carrier != defaults.Carrier {
		t.Fatalf("minimal profile identity = %+v", config.Device)
	}
}

// TestDeviceOutboxDropsItsOldestRatherThanTheTitle covers a defect random key
// fuzzing found on an SKT title: nothing in the product acknowledges external
// requests, so the outbox filled and every later dial or browser open failed.
// The SKT natives propagate that failure as a fatal VM error, so a menu the
// player can reach repeatedly killed the title after 256 visits.
func TestDeviceOutboxDropsItsOldestRatherThanTheTitle(t *testing.T) {
	limits := DeviceLimits{MaxRequests: 4, MaxRequestData: 64}
	device, err := NewDevice(DeviceConfig{}, limits)
	check(t, err)
	for index := 0; index < 100; index++ {
		if _, err := device.Request(
			1, RequestPhone, "01000000000", nil, time.Second,
		); err != nil {
			t.Fatalf("request %d refused: %v", index, err)
		}
	}
	requests := device.Requests()
	if uint32(len(requests)) != limits.MaxRequests {
		t.Fatalf("outbox holds %d requests, want %d", len(requests), limits.MaxRequests)
	}
	// What survives is the newest, which is what a host would want to act on.
	if requests[len(requests)-1].Sequence != 100 {
		t.Errorf("newest sequence is %d, want 100", requests[len(requests)-1].Sequence)
	}
	if requests[0].Sequence != 97 {
		t.Errorf("oldest sequence is %d, want 97", requests[0].Sequence)
	}
	if dropped := device.DroppedRequests(); dropped != 96 {
		t.Errorf("dropped %d requests, want 96", dropped)
	}
	// Dropping is only for a full outbox; a malformed request is still an error.
	if _, err := device.Request(1, RequestPhone, "", nil, time.Second); err == nil {
		t.Error("an empty target was accepted")
	}
}
