package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/profile"
)

type DeviceProperty struct {
	Name  string
	Value string
}

type DeviceCapability struct {
	Name    string
	Enabled bool
}

type DeviceQuirk struct {
	Name    string
	Enabled bool
}

type DeviceKey struct {
	Virtual  int32
	Physical int32
}

type DeviceConfig struct {
	ProfileID        string
	TitleSHA256      string
	ProfileLayers    []string
	WIPIVersion      string
	Carrier          string
	Manufacturer     string
	Model            string
	ScreenWidth      int32
	ScreenHeight     int32
	ScreenFormat     PixelFormat
	Locale           string
	TimezoneMins     int32
	PhoneNumber      string
	VolumeSteps      uint8
	LEDCount         uint8
	Properties       []DeviceProperty
	Capabilities     []DeviceCapability
	Quirks           []DeviceQuirk
	Keys             []DeviceKey
	SupportedCodecs  []string
	NetworkAvailable bool
}

func DefaultDeviceConfig() DeviceConfig {
	return DeviceConfig{
		ProfileID:        "wipi-1.2.1/generic",
		WIPIVersion:      "1.2.1",
		Carrier:          "unknown",
		ScreenWidth:      240,
		ScreenHeight:     320,
		ScreenFormat:     PixelRGBA8888,
		Locale:           "ko-KR",
		TimezoneMins:     9 * 60,
		VolumeSteps:      11,
		LEDCount:         1,
		NetworkAvailable: false,
	}
}

func (c DeviceConfig) Validate() error {
	if strings.TrimSpace(c.ProfileID) == "" || len(c.ProfileID) > 255 ||
		(c.TitleSHA256 != "" &&
			(len(c.TitleSHA256) != 2*sha256.Size ||
				!validHexDigest(c.TitleSHA256) ||
				c.TitleSHA256 != strings.ToLower(c.TitleSHA256))) ||
		len(c.ProfileLayers) > 16 ||
		len(c.WIPIVersion) > 64 || len(c.Carrier) > 64 ||
		len(c.Manufacturer) > 127 || len(c.Model) > 127 ||
		c.ScreenWidth <= 0 || c.ScreenHeight <= 0 ||
		!c.ScreenFormat.Valid() || len(c.Locale) > 64 ||
		c.TimezoneMins < -24*60 || c.TimezoneMins > 24*60 ||
		len(c.PhoneNumber) > 64 || c.VolumeSteps == 0 ||
		c.LEDCount > 64 || len(c.Properties) > 1024 ||
		len(c.Capabilities) > 1024 || len(c.Keys) > 1024 ||
		len(c.Quirks) > 1024 ||
		len(c.SupportedCodecs) > 256 ||
		containsNUL(
			c.ProfileID,
			c.TitleSHA256,
			c.WIPIVersion,
			c.Carrier,
			c.Manufacturer,
			c.Model,
			c.Locale,
			c.PhoneNumber,
		) {
		return fmt.Errorf("%w: invalid device configuration", ErrInvalidArgument)
	}
	seenLayers := make(map[string]struct{}, len(c.ProfileLayers))
	for index, layer := range c.ProfileLayers {
		if strings.TrimSpace(layer) == "" || len(layer) > 255 ||
			strings.IndexByte(layer, 0) >= 0 {
			return fmt.Errorf("%w: invalid profile layer %d", ErrInvalidArgument, index)
		}
		if _, duplicate := seenLayers[layer]; duplicate {
			return fmt.Errorf("%w: duplicate profile layer %q", ErrInvalidArgument, layer)
		}
		seenLayers[layer] = struct{}{}
	}
	previous := ""
	for index, property := range c.Properties {
		if strings.TrimSpace(property.Name) == "" ||
			len(property.Name) > 255 || len(property.Value) > 4096 ||
			strings.IndexByte(property.Name, 0) >= 0 ||
			strings.IndexByte(property.Value, 0) >= 0 ||
			(index != 0 && property.Name <= previous) {
			return fmt.Errorf("%w: invalid device property %d", ErrInvalidArgument, index)
		}
		previous = property.Name
	}
	previous = ""
	for index, capability := range c.Capabilities {
		if strings.TrimSpace(capability.Name) == "" ||
			len(capability.Name) > 64 ||
			strings.IndexByte(capability.Name, 0) >= 0 ||
			(index != 0 && capability.Name <= previous) {
			return fmt.Errorf("%w: invalid device capability %d", ErrInvalidArgument, index)
		}
		previous = capability.Name
	}
	var previousVirtual int32 = math.MinInt32
	for index, key := range c.Keys {
		if index != 0 && key.Virtual <= previousVirtual {
			return fmt.Errorf("%w: invalid device key %d", ErrInvalidArgument, index)
		}
		previousVirtual = key.Virtual
	}
	previous = ""
	for index, quirk := range c.Quirks {
		if strings.TrimSpace(quirk.Name) == "" ||
			len(quirk.Name) > 255 ||
			strings.IndexByte(quirk.Name, 0) >= 0 ||
			(index != 0 && quirk.Name <= previous) {
			return fmt.Errorf("%w: invalid device quirk %d", ErrInvalidArgument, index)
		}
		previous = quirk.Name
	}
	previous = ""
	for index, codec := range c.SupportedCodecs {
		if strings.TrimSpace(codec) == "" || len(codec) > 127 ||
			strings.IndexByte(codec, 0) >= 0 ||
			(index != 0 && codec <= previous) {
			return fmt.Errorf("%w: invalid device codec %d", ErrInvalidArgument, index)
		}
		previous = codec
	}
	return nil
}

func containsNUL(values ...string) bool {
	for _, value := range values {
		if strings.IndexByte(value, 0) >= 0 {
			return true
		}
	}
	return false
}

func validHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

type DeviceLimits struct {
	MaxRequests    uint32
	MaxRequestData uint32
}

func DefaultDeviceLimits() DeviceLimits {
	return DeviceLimits{MaxRequests: 256, MaxRequestData: 4096}
}

func (l DeviceLimits) Validate() error {
	if l.MaxRequests == 0 || l.MaxRequestData == 0 {
		return fmt.Errorf("%w: invalid device limits", ErrInvalidArgument)
	}
	return nil
}

type ExternalRequestKind string

const (
	RequestPhone   ExternalRequestKind = "phone"
	RequestSMS     ExternalRequestKind = "sms"
	RequestBrowser ExternalRequestKind = "browser"
)

type ExternalRequest struct {
	Sequence uint64
	AtNS     int64
	Owner    OwnerID
	Kind     ExternalRequestKind
	Target   string
	Data     []byte
}

type DeviceState struct {
	Config           DeviceConfig
	Limits           DeviceLimits
	VolumeStep       uint8
	VibrationLevel   uint8
	VibrationUntilNS int64
	Backlight        bool
	BacklightUntilNS int64
	LEDs             []int32
	BatteryPercent   uint8
	SignalPercent    uint8
	NetworkAvailable bool
	NextRequest      uint64
	Requests         []ExternalRequest
}

// Device models handset-visible state and records external-action requests.
// It never places calls, opens browsers, or touches host hardware itself.
type Device struct {
	config           DeviceConfig
	limits           DeviceLimits
	volumeStep       uint8
	vibrationLevel   uint8
	vibrationUntil   time.Duration
	backlight        bool
	backlightUntil   time.Duration
	leds             []int32
	batteryPercent   uint8
	signalPercent    uint8
	networkAvailable bool
	nextRequest      uint64
	requests         []ExternalRequest
	droppedRequests  uint64
}

// deviceAdvanceState contains the four fields Device.Advance can mutate. The
// immutable profile, LEDs, and external request log do not belong in a
// per-frame rollback snapshot.
type deviceAdvanceState struct {
	vibrationLevel uint8
	vibrationUntil time.Duration
	backlight      bool
	backlightUntil time.Duration
}

func NewDevice(config DeviceConfig, limits DeviceLimits) (*Device, error) {
	if reflect.DeepEqual(config, DeviceConfig{}) {
		config = DefaultDeviceConfig()
	}
	if limits == (DeviceLimits{}) {
		limits = DefaultDeviceLimits()
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Device{
		config:           cloneDeviceConfig(config),
		limits:           limits,
		leds:             make([]int32, config.LEDCount),
		batteryPercent:   100,
		signalPercent:    100,
		networkAvailable: config.NetworkAvailable,
		nextRequest:      1,
	}, nil
}

func (d *Device) Config() DeviceConfig {
	return cloneDeviceConfig(d.config)
}

func (d *Device) NetworkAvailable() bool {
	return d.networkAvailable
}

// SetNetworkAvailable updates modeled radio/network reachability. It never
// opens a host connection; adapters use Network for explicit modeled I/O.
func (d *Device) SetNetworkAvailable(available bool) {
	d.networkAvailable = available
}

func (d *Device) Property(name string) (string, bool) {
	index := sort.Search(len(d.config.Properties), func(index int) bool {
		return d.config.Properties[index].Name >= name
	})
	if index == len(d.config.Properties) || d.config.Properties[index].Name != name {
		return "", false
	}
	return d.config.Properties[index].Value, true
}

func (d *Device) Capability(name string) bool {
	index := sort.Search(len(d.config.Capabilities), func(index int) bool {
		return d.config.Capabilities[index].Name >= name
	})
	return index < len(d.config.Capabilities) &&
		d.config.Capabilities[index].Name == name &&
		d.config.Capabilities[index].Enabled
}

func (d *Device) Quirk(name string) bool {
	index := sort.Search(len(d.config.Quirks), func(index int) bool {
		return d.config.Quirks[index].Name >= name
	})
	return index < len(d.config.Quirks) &&
		d.config.Quirks[index].Name == name &&
		d.config.Quirks[index].Enabled
}

func (d *Device) SetVolumeStep(step uint8) error {
	if step >= d.config.VolumeSteps {
		return fmt.Errorf("%w: volume step %d", ErrInvalidArgument, step)
	}
	d.volumeStep = step
	return nil
}

func (d *Device) VolumeStep() uint8 {
	return d.volumeStep
}

func (d *Device) Vibrate(level uint8, duration, now time.Duration) error {
	if level > 100 || duration < 0 || now < 0 ||
		duration > time.Duration(math.MaxInt64-int64(now)) {
		return fmt.Errorf("%w: invalid vibration state", ErrInvalidArgument)
	}
	d.vibrationLevel = level
	if level == 0 || duration == 0 {
		d.vibrationUntil = 0
		d.vibrationLevel = 0
	} else {
		d.vibrationUntil = now + duration
	}
	return nil
}

func (d *Device) Vibration() (uint8, time.Duration) {
	return d.vibrationLevel, d.vibrationUntil
}

func (d *Device) SetBacklight(on bool, duration, now time.Duration) error {
	if duration < 0 || now < 0 ||
		duration > time.Duration(math.MaxInt64-int64(now)) {
		return fmt.Errorf("%w: invalid backlight state", ErrInvalidArgument)
	}
	d.backlight = on
	if !on || duration == 0 {
		d.backlightUntil = 0
	} else {
		d.backlightUntil = now + duration
	}
	return nil
}

func (d *Device) Backlight() (bool, time.Duration) {
	return d.backlight, d.backlightUntil
}

func (d *Device) SetLED(index uint8, value int32) error {
	if int(index) >= len(d.leds) {
		return fmt.Errorf("%w: LED index %d", ErrInvalidArgument, index)
	}
	d.leds[index] = value
	return nil
}

func (d *Device) LED(index uint8) (int32, error) {
	if int(index) >= len(d.leds) {
		return 0, fmt.Errorf("%w: LED index %d", ErrInvalidArgument, index)
	}
	return d.leds[index], nil
}

func (d *Device) SetStatus(battery, signal uint8, networkAvailable bool) error {
	if battery > 100 || signal > 100 {
		return fmt.Errorf("%w: invalid device status", ErrInvalidArgument)
	}
	d.batteryPercent = battery
	d.signalPercent = signal
	d.networkAvailable = networkAvailable
	return nil
}

func (d *Device) Status() (battery, signal uint8, networkAvailable bool) {
	return d.batteryPercent, d.signalPercent, d.networkAvailable
}

func (d *Device) Request(
	owner OwnerID,
	kind ExternalRequestKind,
	target string,
	data []byte,
	now time.Duration,
) (uint64, error) {
	if kind != RequestPhone && kind != RequestSMS && kind != RequestBrowser ||
		strings.TrimSpace(target) == "" ||
		strings.IndexByte(target, 0) >= 0 ||
		len(target) > int(d.limits.MaxRequestData) ||
		len(data) > int(d.limits.MaxRequestData) || now < 0 {
		return 0, fmt.Errorf("%w: invalid external request", ErrInvalidArgument)
	}
	if d.nextRequest == 0 || d.nextRequest == math.MaxUint64 {
		return 0, fmt.Errorf("%w: external request queue exhausted", ErrLimitExceeded)
	}
	// The queue is an outbox: a host reads it and acknowledges with
	// ClearRequests. A host that has not acknowledged 256 placed calls is not
	// listening, and on the handset none of this queues at all - dialling
	// replaces whatever was on screen. So a full outbox drops its oldest entry
	// rather than refusing the guest, which is what a title cannot survive: a
	// menu that dials or opens the browser is reachable over and over, and
	// refusing it took the title down with the queue.
	for uint32(len(d.requests)) >= d.limits.MaxRequests && len(d.requests) != 0 {
		copy(d.requests, d.requests[1:])
		clear(d.requests[len(d.requests)-1:])
		d.requests = d.requests[:len(d.requests)-1]
		d.droppedRequests++
	}
	sequence := d.nextRequest
	d.nextRequest++
	d.requests = append(d.requests, ExternalRequest{
		Sequence: sequence,
		AtNS:     int64(now),
		Owner:    owner,
		Kind:     kind,
		Target:   target,
		Data:     cloneBytes(data),
	})
	return sequence, nil
}

// DroppedRequests counts entries the outbox discarded because no host
// acknowledged them. It is diagnostic only: a non-zero count means external
// requests are being made and nothing is consuming them.
func (d *Device) DroppedRequests() uint64 { return d.droppedRequests }

func (d *Device) Requests() []ExternalRequest {
	result := append([]ExternalRequest(nil), d.requests...)
	for index := range result {
		result[index].Data = cloneBytes(result[index].Data)
	}
	return result
}

func (d *Device) ClearRequests(through uint64) {
	index := sort.Search(len(d.requests), func(index int) bool {
		return d.requests[index].Sequence > through
	})
	copy(d.requests, d.requests[index:])
	clear(d.requests[len(d.requests)-index:])
	d.requests = d.requests[:len(d.requests)-index]
}

func (d *Device) Advance(now time.Duration) error {
	if now < 0 {
		return fmt.Errorf("%w: invalid device time", ErrInvalidArgument)
	}
	if d.vibrationUntil != 0 && d.vibrationUntil <= now {
		d.vibrationLevel = 0
		d.vibrationUntil = 0
	}
	if d.backlightUntil != 0 && d.backlightUntil <= now {
		d.backlight = false
		d.backlightUntil = 0
	}
	return nil
}

func (d *Device) captureAdvance(destination *deviceAdvanceState) {
	destination.vibrationLevel = d.vibrationLevel
	destination.vibrationUntil = d.vibrationUntil
	destination.backlight = d.backlight
	destination.backlightUntil = d.backlightUntil
}

func (d *Device) restoreAdvance(saved *deviceAdvanceState) {
	if saved == nil {
		return
	}
	d.vibrationLevel = saved.vibrationLevel
	d.vibrationUntil = saved.vibrationUntil
	d.backlight = saved.backlight
	d.backlightUntil = saved.backlightUntil
}

func (d *Device) Snapshot() DeviceState {
	return DeviceState{
		Config:           cloneDeviceConfig(d.config),
		Limits:           d.limits,
		VolumeStep:       d.volumeStep,
		VibrationLevel:   d.vibrationLevel,
		VibrationUntilNS: int64(d.vibrationUntil),
		Backlight:        d.backlight,
		BacklightUntilNS: int64(d.backlightUntil),
		LEDs:             append([]int32(nil), d.leds...),
		BatteryPercent:   d.batteryPercent,
		SignalPercent:    d.signalPercent,
		NetworkAvailable: d.networkAvailable,
		NextRequest:      d.nextRequest,
		Requests:         d.Requests(),
	}
}

func (d *Device) Restore(state DeviceState) error {
	if err := state.Config.Validate(); err != nil ||
		state.Limits.Validate() != nil ||
		state.VolumeStep >= state.Config.VolumeSteps ||
		state.VibrationLevel > 100 ||
		state.VibrationUntilNS < 0 || state.BacklightUntilNS < 0 ||
		(state.VibrationLevel == 0) != (state.VibrationUntilNS == 0) ||
		(!state.Backlight && state.BacklightUntilNS != 0) ||
		len(state.LEDs) != int(state.Config.LEDCount) ||
		state.BatteryPercent > 100 || state.SignalPercent > 100 ||
		state.NextRequest == 0 ||
		len(state.Requests) > int(state.Limits.MaxRequests) {
		return fmt.Errorf("%w: invalid device state", ErrInvalidState)
	}
	requests := make([]ExternalRequest, len(state.Requests))
	var previous uint64
	for index, request := range state.Requests {
		if request.Sequence == 0 || request.Sequence >= state.NextRequest ||
			(index != 0 && request.Sequence <= previous) ||
			request.AtNS < 0 ||
			(request.Kind != RequestPhone && request.Kind != RequestSMS &&
				request.Kind != RequestBrowser) ||
			strings.TrimSpace(request.Target) == "" ||
			strings.IndexByte(request.Target, 0) >= 0 ||
			len(request.Target) > int(state.Limits.MaxRequestData) ||
			len(request.Data) > int(state.Limits.MaxRequestData) {
			return fmt.Errorf("%w: invalid external request %d", ErrInvalidState, index)
		}
		request.Data = cloneBytes(request.Data)
		requests[index] = request
		previous = request.Sequence
	}
	d.config = cloneDeviceConfig(state.Config)
	d.limits = state.Limits
	d.volumeStep = state.VolumeStep
	d.vibrationLevel = state.VibrationLevel
	d.vibrationUntil = time.Duration(state.VibrationUntilNS)
	d.backlight = state.Backlight
	d.backlightUntil = time.Duration(state.BacklightUntilNS)
	d.leds = append([]int32(nil), state.LEDs...)
	d.batteryPercent = state.BatteryPercent
	d.signalPercent = state.SignalPercent
	d.networkAvailable = state.NetworkAvailable
	d.nextRequest = state.NextRequest
	d.requests = requests
	return nil
}

func cloneDeviceConfig(config DeviceConfig) DeviceConfig {
	config.ProfileLayers = append([]string(nil), config.ProfileLayers...)
	config.Properties = append([]DeviceProperty(nil), config.Properties...)
	config.Capabilities = append([]DeviceCapability(nil), config.Capabilities...)
	config.Quirks = append([]DeviceQuirk(nil), config.Quirks...)
	config.Keys = append([]DeviceKey(nil), config.Keys...)
	config.SupportedCodecs = append([]string(nil), config.SupportedCodecs...)
	return config
}

func DeviceConfigFromProfile(resolved profile.Profile) (DeviceConfig, error) {
	if err := resolved.Validate(); err != nil {
		return DeviceConfig{}, err
	}
	config := DefaultDeviceConfig()
	config.ProfileID = resolved.ID
	config.TitleSHA256 = strings.ToLower(resolved.TitleSHA256)
	config.ProfileLayers = append([]string(nil), resolved.Layers...)
	if resolved.Version != "" {
		config.WIPIVersion = string(resolved.Version)
	}
	if resolved.Carrier != "" {
		config.Carrier = string(resolved.Carrier)
	}
	config.Manufacturer = resolved.Manufacturer
	config.Model = resolved.Model
	config.ScreenWidth = int32(resolved.Screen.Width)
	config.ScreenHeight = int32(resolved.Screen.Height)
	config.ScreenFormat = pixelFormatFromProfile(resolved.Screen)
	if value := resolved.Properties["microedition.locale"]; value != "" {
		config.Locale = value
	}
	if value := resolved.Properties["phone-number"]; value != "" {
		config.PhoneNumber = value
	}
	for name, value := range resolved.Properties {
		config.Properties = append(config.Properties, DeviceProperty{Name: name, Value: value})
	}
	sort.Slice(config.Properties, func(i, j int) bool {
		return config.Properties[i].Name < config.Properties[j].Name
	})
	for name, enabled := range resolved.Capabilities {
		config.Capabilities = append(config.Capabilities, DeviceCapability{
			Name: string(name), Enabled: enabled,
		})
	}
	sort.Slice(config.Capabilities, func(i, j int) bool {
		return config.Capabilities[i].Name < config.Capabilities[j].Name
	})
	for name, enabled := range resolved.Quirks {
		config.Quirks = append(config.Quirks, DeviceQuirk{
			Name: name, Enabled: enabled,
		})
	}
	sort.Slice(config.Quirks, func(i, j int) bool {
		return config.Quirks[i].Name < config.Quirks[j].Name
	})
	for virtual, physical := range resolved.Keys {
		config.Keys = append(config.Keys, DeviceKey{
			Virtual: int32(virtual), Physical: int32(physical),
		})
	}
	sort.Slice(config.Keys, func(i, j int) bool {
		return config.Keys[i].Virtual < config.Keys[j].Virtual
	})
	return config, config.Validate()
}

func pixelFormatFromProfile(screen profile.Screen) PixelFormat {
	switch screen.BitsPerPixel {
	case 8:
		if screen.ColorType == profile.ColorGray {
			return PixelGray8
		}
		return PixelIndexed8
	case 15:
		return PixelRGB555
	case 16:
		return PixelRGB565
	case 32:
		return PixelXRGB8888
	default:
		return PixelRGBA8888
	}
}

// ConfigFromProfile applies recognized profile limits while retaining safe
// defaults for unspecified service settings.
func ConfigFromProfile(resolved profile.Profile) (Config, error) {
	device, err := DeviceConfigFromProfile(resolved)
	if err != nil {
		return Config{}, err
	}
	config := DefaultConfig()
	config.Device = device
	config.Locale = device.Locale
	config.TimezoneOffsetMinutes = device.TimezoneMins
	for name, value := range resolved.Limits {
		if value == 0 {
			continue
		}
		switch name {
		case profile.LimitSurfaceCount:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: surface count limit", ErrInvalidArgument)
			}
			config.Limits.Graphics.MaxSurfaces = uint32(value)
		case profile.LimitSurfacePixels:
			config.Limits.Graphics.MaxPixels = value
		case profile.LimitDecodedAssetSize:
			config.Limits.Assets.MaxDecodedBytes = value
		case profile.LimitEventCount:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: event count limit", ErrInvalidArgument)
			}
			config.Limits.MaxEvents = uint32(value)
		case profile.LimitTraceCount:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: trace count limit", ErrInvalidArgument)
			}
			config.Limits.MaxTrace = uint32(value)
		case profile.LimitStorageBytes:
			config.Limits.Storage.MaxStorageBytes = value
			if config.Limits.Storage.MaxFileBytes > value {
				config.Limits.Storage.MaxFileBytes = value
			}
		case profile.LimitOpenFiles:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: open file limit", ErrInvalidArgument)
			}
			config.Limits.Storage.MaxOpenHandles = uint32(value)
		case profile.LimitRecordStores:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: record store limit", ErrInvalidArgument)
			}
			config.Limits.Storage.MaxRecordStores = uint32(value)
		case profile.LimitAudioBytes:
			if value/2 > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: audio queue limit", ErrInvalidArgument)
			}
			config.Limits.Media.MaxQueuedSamples = uint32(max(uint64(1), value/2))
		case profile.LimitMediaClips:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: media clip limit", ErrInvalidArgument)
			}
			config.Limits.Media.MaxClips = uint32(value)
		case profile.LimitSockets:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: socket limit", ErrInvalidArgument)
			}
			config.Limits.Network.MaxSockets = uint32(value)
		case profile.LimitHTTPRequests:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: HTTP request limit", ErrInvalidArgument)
			}
			config.Limits.Network.MaxHTTPRequests = uint32(value)
		case profile.LimitSerialPorts:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: serial port limit", ErrInvalidArgument)
			}
			config.Limits.Network.MaxSerialPorts = uint32(value)
		case profile.LimitNetworkBytes:
			config.Limits.Network.MaxTotalBytes = value
			if config.Limits.Network.MaxBufferBytes > value {
				config.Limits.Network.MaxBufferBytes = value
			}
		case profile.LimitDeviceRequests:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: device request limit", ErrInvalidArgument)
			}
			config.Limits.Device.MaxRequests = uint32(value)
		case profile.LimitTimers:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: timer limit", ErrInvalidArgument)
			}
			config.Limits.MaxTimers = uint32(value)
		case profile.LimitServiceObjects:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: service object limit", ErrInvalidArgument)
			}
			config.Limits.MaxObjects = uint32(value)
		case profile.LimitAssets:
			if value > math.MaxUint32 {
				return Config{}, fmt.Errorf("%w: decoded asset limit", ErrInvalidArgument)
			}
			config.Limits.Assets.MaxAssets = uint32(value)
		}
	}
	return normalizeConfig(config)
}
