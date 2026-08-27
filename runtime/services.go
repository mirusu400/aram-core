package runtime

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"time"
)

const ServicesSchemaVersion = uint32(1)

type Limits struct {
	MaxObjects    uint32
	MaxEvents     uint32
	MaxEventData  uint32
	MaxControls   uint32
	MaxTimers     uint32
	Graphics      GraphicsLimits
	Assets        AssetLimits
	Storage       StorageLimits
	Media         MediaLimits
	Device        DeviceLimits
	Network       NetworkLimits
	Replay        ReplayLimits
	Coordinator   CoordinatorLimits
	Text          TextLimits
	MaxTrace      uint32
	MaxTraceData  uint32
	MaxRNGStreams uint32
}

func DefaultLimits() Limits {
	return Limits{
		MaxObjects:    DefaultMaxObjects,
		MaxEvents:     DefaultMaxEvents,
		MaxEventData:  DefaultMaxEventData,
		MaxControls:   128,
		MaxTimers:     1024,
		Graphics:      DefaultGraphicsLimits(),
		Assets:        DefaultAssetLimits(),
		Storage:       DefaultStorageLimits(),
		Media:         DefaultMediaLimits(),
		Device:        DefaultDeviceLimits(),
		Network:       DefaultNetworkLimits(),
		Replay:        DefaultReplayLimits(),
		Coordinator:   DefaultCoordinatorLimits(),
		Text:          DefaultTextLimits(),
		MaxTrace:      DefaultMaxTraceEvents,
		MaxTraceData:  DefaultMaxTraceData,
		MaxRNGStreams: DefaultMaxStreams,
	}
}

func (l Limits) Validate() error {
	if l.MaxObjects == 0 || l.MaxEvents == 0 || l.MaxEventData == 0 ||
		l.MaxControls == 0 || l.MaxTimers == 0 ||
		l.MaxTrace == 0 || l.MaxTraceData == 0 || l.MaxRNGStreams == 0 {
		return fmt.Errorf("%w: invalid shared service limits", ErrInvalidArgument)
	}
	if err := l.Graphics.Validate(); err != nil {
		return err
	}
	if err := l.Assets.Validate(); err != nil {
		return err
	}
	if err := l.Storage.Validate(); err != nil {
		return err
	}
	if err := l.Media.Validate(); err != nil {
		return err
	}
	if err := l.Device.Validate(); err != nil {
		return err
	}
	if err := l.Network.Validate(); err != nil {
		return err
	}
	if err := l.Replay.Validate(); err != nil {
		return err
	}
	if err := l.Coordinator.Validate(); err != nil {
		return err
	}
	if err := l.Text.Validate(); err != nil {
		return err
	}
	return nil
}

type Config struct {
	Limits                Limits
	RandomSeed            uint64
	ProfileHash           [sha256.Size]byte
	WallEpochMillis       int64
	TimezoneOffsetMinutes int32
	Locale                string
	FallbackFont          string
	RepeatDelay           time.Duration
	RepeatPeriod          time.Duration
	FrameDuration         time.Duration
	Device                DeviceConfig
	ReplayMode            ReplayMode
}

func DefaultConfig() Config {
	device := DefaultDeviceConfig()
	return Config{
		Limits:                DefaultLimits(),
		WallEpochMillis:       DefaultWallEpochMillis,
		TimezoneOffsetMinutes: device.TimezoneMins,
		Locale:                device.Locale,
		FallbackFont:          defaultHandsetFontName,
		RepeatDelay:           500 * time.Millisecond,
		RepeatPeriod:          50 * time.Millisecond,
		FrameDuration:         DefaultFrameDuration,
		Device:                device,
	}
}

func normalizeConfig(config Config) (Config, error) {
	defaults := DefaultConfig()
	deviceDefaulted := reflect.DeepEqual(config.Device, DeviceConfig{})
	localeSpecified := config.Locale != ""
	timezoneSpecified := config.TimezoneOffsetMinutes != 0 || localeSpecified
	if reflect.DeepEqual(config.Limits, Limits{}) {
		config.Limits = defaults.Limits
	}
	if config.WallEpochMillis == 0 {
		config.WallEpochMillis = defaults.WallEpochMillis
	}
	if deviceDefaulted {
		config.Device = defaults.Device
	}
	if !localeSpecified {
		config.Locale = config.Device.Locale
	}
	if !timezoneSpecified {
		config.TimezoneOffsetMinutes = config.Device.TimezoneMins
	}
	if deviceDefaulted {
		config.Device.Locale = config.Locale
		config.Device.TimezoneMins = config.TimezoneOffsetMinutes
	}
	if config.RepeatDelay == 0 {
		config.RepeatDelay = defaults.RepeatDelay
	}
	if config.RepeatPeriod == 0 {
		config.RepeatPeriod = defaults.RepeatPeriod
	}
	if config.FrameDuration == 0 {
		config.FrameDuration = defaults.FrameDuration
	}
	// Canonicalize the fallback font so an empty or unknown selection maps to
	// the default. The name is part of the hashed configuration, so it must be
	// deterministic for identical inputs.
	config.FallbackFont = lookupHandsetFont(config.FallbackFont).name
	config.Device = cloneDeviceConfig(config.Device)
	if err := config.Limits.Validate(); err != nil {
		return Config{}, err
	}
	if err := config.Device.Validate(); err != nil {
		return Config{}, err
	}
	if config.RepeatDelay <= 0 || config.RepeatPeriod <= 0 ||
		config.FrameDuration <= 0 {
		return Config{}, fmt.Errorf("%w: invalid service duration configuration", ErrInvalidArgument)
	}
	screenPixels := uint64(config.Device.ScreenWidth) *
		uint64(config.Device.ScreenHeight)
	screenBytes := screenPixels *
		uint64(config.Device.ScreenFormat.BytesPerPixel())
	if config.Locale != config.Device.Locale ||
		config.TimezoneOffsetMinutes != config.Device.TimezoneMins ||
		config.Device.ScreenWidth > config.Limits.Graphics.MaxWidth ||
		config.Device.ScreenHeight > config.Limits.Graphics.MaxHeight ||
		screenPixels > config.Limits.Graphics.MaxPixels ||
		screenBytes > config.Limits.Graphics.MaxBytes {
		return Config{}, fmt.Errorf(
			"%w: device and shared service configuration disagree",
			ErrInvalidArgument,
		)
	}
	providedHash := config.ProfileHash
	hashConfig := config
	hashConfig.RandomSeed = 0
	hashConfig.ProfileHash = [sha256.Size]byte{}
	hashConfig.ReplayMode = ReplayOff
	encoded, err := MarshalStateComponent(hashConfig)
	if err != nil {
		return Config{}, fmt.Errorf(
			"%w: encode profile configuration: %v",
			ErrInvalidArgument,
			err,
		)
	}
	canonicalHash := sha256.Sum256(encoded)
	if providedHash != [sha256.Size]byte{} &&
		providedHash != canonicalHash {
		return Config{}, fmt.Errorf(
			"%w: profile configuration hash mismatch",
			ErrInvalidArgument,
		)
	}
	config.ProfileHash = canonicalHash
	return config, nil
}

type ServicesState struct {
	Schema      uint32
	Config      Config
	Registry    RegistryState
	Clock       ClockState
	Random      RandomState
	Events      EventBusState
	Input       InputState
	Timers      TimersState
	Graphics    GraphicsState
	Assets      AssetsState
	Storage     StorageState
	Media       MediaState
	Device      DeviceState
	Network     NetworkState
	Replay      ReplayState
	Coordinator CoordinatorState
	Text        TextState
}

// Services is the shared deterministic device state used by runtime adapters.
// Every member is usable without an ARM CPU backend, Java VM, display, audio
// device, network, or host filesystem.
type Services struct {
	Config      Config
	Registry    *Registry
	Clock       *Clock
	Random      *Random
	Events      *EventBus
	Input       *Input
	Timers      *Timers
	Graphics    *Graphics
	Assets      *Assets
	Storage     *Storage
	Media       *Media
	Device      *Device
	Network     *Network
	Replay      *Replay
	Coordinator *Coordinator
	Text        *Text
	Trace       *Trace

	// rbEvents is a reusable event-bus rollback buffer for the per-frame Advance
	// transaction, so the common (no-error) path does not allocate a fresh event
	// slice every frame. Advance is never re-entered concurrently (the machine
	// serialises execution), so a single shared buffer is safe.
	rbEvents EventBusState
	// rbMedia is the matching reusable media rollback record; see
	// mediaAdvanceState for why the full snapshot is too expensive here.
	rbMedia  mediaAdvanceState
	rbInput  inputAdvanceState
	rbTimers timersAdvanceState
	rbDevice deviceAdvanceState
	rbReplay replayAdvanceState
}

func NewServices(config Config) (*Services, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	registry := NewRegistry(normalized.Limits.MaxObjects)
	clock, err := NewClock(
		normalized.WallEpochMillis,
		normalized.TimezoneOffsetMinutes,
		normalized.Locale,
	)
	if err != nil {
		return nil, err
	}
	graphics, err := NewGraphics(registry, normalized.Limits.Graphics)
	if err != nil {
		return nil, err
	}
	assets, err := NewAssets(registry, graphics, normalized.Limits.Assets)
	if err != nil {
		return nil, err
	}
	storage, err := NewStorage(registry, clock, normalized.Limits.Storage)
	if err != nil {
		return nil, err
	}
	media, err := NewMedia(registry, normalized.Limits.Media)
	if err != nil {
		return nil, err
	}
	device, err := NewDevice(normalized.Device, normalized.Limits.Device)
	if err != nil {
		return nil, err
	}
	network, err := NewNetwork(registry, normalized.Limits.Network)
	if err != nil {
		return nil, err
	}
	replay, err := NewReplay(
		normalized.Limits.Replay,
		normalized.ReplayMode,
		normalized.RandomSeed,
		normalized.Device.ProfileID,
	)
	if err != nil {
		return nil, err
	}
	replay.setProfileHash(normalized.ProfileHash)
	coordinator, err := NewCoordinator(normalized.Limits.Coordinator)
	if err != nil {
		return nil, err
	}
	text, err := NewText(registry, graphics, normalized.Limits.Text, normalized.FallbackFont)
	if err != nil {
		return nil, err
	}
	return &Services{
		Config:   normalized,
		Registry: registry,
		Clock:    clock,
		Random: NewRandom(
			normalized.RandomSeed,
			normalized.Limits.MaxRNGStreams,
		),
		Events: NewEventBus(
			normalized.Limits.MaxEvents,
			normalized.Limits.MaxEventData,
		),
		Input: NewInput(
			normalized.Limits.MaxControls,
			normalized.RepeatDelay,
			normalized.RepeatPeriod,
		),
		Timers:      NewTimers(registry, normalized.Limits.MaxTimers),
		Graphics:    graphics,
		Assets:      assets,
		Storage:     storage,
		Media:       media,
		Device:      device,
		Network:     network,
		Replay:      replay,
		Coordinator: coordinator,
		Text:        text,
		Trace: NewTrace(
			normalized.Limits.MaxTrace,
			normalized.Limits.MaxTraceData,
		),
	}, nil
}

func (s *Services) Advance(owner OwnerID, delta time.Duration) error {
	if s == nil {
		return fmt.Errorf("%w: services are nil", ErrInvalidArgument)
	}
	clockState := s.Clock.Snapshot()
	s.Events.SnapshotInto(&s.rbEvents)
	eventState := s.rbEvents
	s.rbInput.controls = s.rbInput.controls[:0]
	s.rbTimers.timers = s.rbTimers.timers[:0]
	s.Media.captureAdvance(&s.rbMedia)
	s.Device.captureAdvance(&s.rbDevice)
	s.Replay.captureAdvance(&s.rbReplay)
	if s.Replay.Mode() == ReplayPlayback {
		if err := s.Replay.Consume(ReplayEntry{
			AtNS: int64(s.Clock.Monotonic()), Kind: ReplayClockAdvance,
			Owner: owner, Value: int64(delta),
		}); err != nil {
			return err
		}
	}

	if err := s.Clock.Advance(delta); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	now := s.Clock.Monotonic()
	if err := s.Media.advanceLocked(
		time.Duration(clockState.MonotonicNanos),
		now,
		s.Events,
	); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	if err := s.Input.advanceLocked(s.Events, owner, now, &s.rbInput); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	if err := s.Timers.advanceLocked(now, s.Events, &s.rbTimers); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	if err := s.Device.Advance(now); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	if err := s.Replay.RecordAdvance(owner, time.Duration(clockState.MonotonicNanos), delta); err != nil {
		s.restoreAdvance(clockState, eventState)
		return err
	}
	return nil
}

func (s *Services) AdvanceFrame(owner OwnerID) error {
	return s.Advance(owner, s.Config.FrameDuration)
}

func (s *Services) restoreAdvance(
	clock ClockState,
	events EventBusState,
) {
	s.Replay.restoreAdvance(&s.rbReplay)
	s.Device.restoreAdvance(&s.rbDevice)
	s.Timers.restoreAdvance(&s.rbTimers)
	s.Input.restoreAdvance(&s.rbInput)
	s.Media.restoreAdvance(&s.rbMedia)
	_ = s.Events.Restore(events)
	_ = s.Clock.Restore(clock)
}

func (s *Services) Snapshot() ServicesState {
	return ServicesState{
		Schema:      ServicesSchemaVersion,
		Config:      cloneConfig(s.Config),
		Registry:    s.Registry.Snapshot(),
		Clock:       s.Clock.Snapshot(),
		Random:      s.Random.Snapshot(),
		Events:      s.Events.Snapshot(),
		Input:       s.Input.Snapshot(),
		Timers:      s.Timers.Snapshot(),
		Graphics:    s.Graphics.Snapshot(),
		Assets:      s.Assets.Snapshot(),
		Storage:     s.Storage.Snapshot(),
		Media:       s.Media.Snapshot(),
		Device:      s.Device.Snapshot(),
		Network:     s.Network.Snapshot(),
		Replay:      s.Replay.Snapshot(),
		Coordinator: s.Coordinator.Snapshot(),
		Text:        s.Text.Snapshot(),
	}
}

func cloneConfig(config Config) Config {
	config.Device = cloneDeviceConfig(config.Device)
	return config
}

// Restore validates the complete component graph on an isolated candidate
// before mutating any live service.
func (s *Services) Restore(state ServicesState) error {
	if s == nil {
		return fmt.Errorf("%w: services are nil", ErrInvalidArgument)
	}
	candidate, err := servicesFromState(state)
	if err != nil {
		return err
	}
	trace := s.Trace
	if trace == nil {
		trace = candidate.Trace
	}
	if s.Registry == nil || s.Clock == nil || s.Random == nil ||
		s.Events == nil || s.Input == nil || s.Timers == nil ||
		s.Graphics == nil || s.Assets == nil || s.Storage == nil ||
		s.Media == nil || s.Device == nil || s.Network == nil ||
		s.Replay == nil || s.Coordinator == nil || s.Text == nil {
		*s = *candidate
		s.Trace = trace
		return nil
	}

	*s.Registry = *candidate.Registry
	*s.Clock = *candidate.Clock
	*s.Random = *candidate.Random
	*s.Events = *candidate.Events
	*s.Input = *candidate.Input
	*s.Timers = *candidate.Timers
	*s.Graphics = *candidate.Graphics
	*s.Assets = *candidate.Assets
	*s.Storage = *candidate.Storage
	*s.Media = *candidate.Media
	*s.Device = *candidate.Device
	*s.Network = *candidate.Network
	*s.Replay = *candidate.Replay
	*s.Coordinator = *candidate.Coordinator
	*s.Text = *candidate.Text

	s.Config = candidate.Config
	s.Timers.registry = s.Registry
	s.Graphics.registry = s.Registry
	s.Assets.registry = s.Registry
	s.Assets.graphics = s.Graphics
	s.Storage.registry = s.Registry
	s.Storage.clock = s.Clock
	s.Media.registry = s.Registry
	s.Network.registry = s.Registry
	s.Text.registry = s.Registry
	s.Text.graphics = s.Graphics
	s.Trace = trace
	return nil
}

func servicesFromState(state ServicesState) (*Services, error) {
	if state.Schema != ServicesSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported services schema %d", ErrInvalidState, state.Schema)
	}
	config, err := normalizeConfig(state.Config)
	if err != nil || !reflect.DeepEqual(config, state.Config) {
		return nil, fmt.Errorf("%w: invalid services configuration", ErrInvalidState)
	}
	candidate, err := NewServices(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	if state.Registry.Limit != config.Limits.MaxObjects ||
		state.Events.MaxEvents != config.Limits.MaxEvents ||
		state.Events.MaxEventData != config.Limits.MaxEventData ||
		state.Input.MaxControls != config.Limits.MaxControls ||
		state.Input.RepeatDelayNS != int64(config.RepeatDelay) ||
		state.Input.RepeatPeriodNS != int64(config.RepeatPeriod) ||
		state.Timers.MaxTimers != config.Limits.MaxTimers ||
		state.Graphics.Limits != config.Limits.Graphics ||
		state.Assets.Limits != config.Limits.Assets ||
		state.Storage.Limits != config.Limits.Storage ||
		state.Media.Limits != config.Limits.Media ||
		state.Device.Limits != config.Limits.Device ||
		!reflect.DeepEqual(state.Device.Config, config.Device) ||
		state.Network.Limits != config.Limits.Network ||
		state.Replay.Limits != config.Limits.Replay ||
		state.Replay.RandomSeed != config.RandomSeed ||
		state.Replay.ProfileID != config.Device.ProfileID ||
		state.Replay.ProfileHash != config.ProfileHash ||
		state.Coordinator.Limits != config.Limits.Coordinator ||
		state.Text.Limits != config.Limits.Text ||
		state.Random.MaxStreams != config.Limits.MaxRNGStreams {
		return nil, fmt.Errorf("%w: component limits do not match service configuration", ErrInvalidState)
	}
	if err := candidate.Registry.Restore(state.Registry); err != nil {
		return nil, err
	}
	if err := candidate.Clock.Restore(state.Clock); err != nil {
		return nil, err
	}
	if err := candidate.Random.Restore(state.Random); err != nil {
		return nil, err
	}
	if err := candidate.Events.Restore(state.Events); err != nil {
		return nil, err
	}
	if err := candidate.Input.Restore(state.Input); err != nil {
		return nil, err
	}
	if err := candidate.Timers.Restore(state.Timers); err != nil {
		return nil, err
	}
	if err := candidate.Graphics.Restore(state.Graphics); err != nil {
		return nil, err
	}
	if err := candidate.Assets.Restore(state.Assets); err != nil {
		return nil, err
	}
	if err := candidate.Storage.Restore(state.Storage); err != nil {
		return nil, err
	}
	if err := candidate.Media.Restore(state.Media); err != nil {
		return nil, err
	}
	if err := candidate.Device.Restore(state.Device); err != nil {
		return nil, err
	}
	if err := candidate.Network.Restore(state.Network); err != nil {
		return nil, err
	}
	if err := candidate.Replay.Restore(state.Replay); err != nil {
		return nil, err
	}
	if err := validateServiceReplayState(
		state.Replay,
		config.Limits.Network,
	); err != nil {
		return nil, err
	}
	if err := candidate.Coordinator.Restore(state.Coordinator); err != nil {
		return nil, err
	}
	if err := candidate.Text.Restore(state.Text); err != nil {
		return nil, err
	}
	if err := validateServiceRegistryGraph(state); err != nil {
		return nil, err
	}
	return candidate, nil
}

func validateServiceRegistryGraph(state ServicesState) error {
	type expectedObject struct {
		kind  ObjectKind
		owner OwnerID
	}
	expected := make(map[ServiceID]expectedObject, len(state.Registry.Entries))
	add := func(id ServiceID, owner OwnerID, kind ObjectKind) error {
		if !id.Valid() {
			return fmt.Errorf("%w: invalid %s service ID", ErrInvalidState, kind)
		}
		if previous, duplicate := expected[id]; duplicate {
			return fmt.Errorf(
				"%w: service ID %s is both %s and %s",
				ErrInvalidState,
				id,
				previous.kind,
				kind,
			)
		}
		expected[id] = expectedObject{kind: kind, owner: owner}
		return nil
	}
	for _, current := range state.Graphics.Surfaces {
		if err := add(current.ID, current.Owner, KindSurface); err != nil {
			return err
		}
	}
	for _, current := range state.Assets.Assets {
		if err := add(current.ID, current.Owner, KindImage); err != nil {
			return err
		}
	}
	for _, current := range state.Text.Fonts {
		if err := add(current.ID, current.Owner, KindFont); err != nil {
			return err
		}
	}
	for _, current := range state.Storage.OpenFiles {
		if err := add(current.ID, current.Owner, KindFile); err != nil {
			return err
		}
	}
	for _, current := range state.Storage.RecordStores {
		if err := add(current.ID, current.Owner, KindRecordBase); err != nil {
			return err
		}
	}
	for _, current := range state.Media.Clips {
		if err := add(current.ID, current.Owner, KindClip); err != nil {
			return err
		}
	}
	for _, current := range state.Timers.Timers {
		if err := add(current.ID, current.Owner, KindTimer); err != nil {
			return err
		}
	}
	for _, current := range state.Network.Sockets {
		if err := add(current.ID, current.Owner, KindSocket); err != nil {
			return err
		}
	}
	for _, current := range state.Network.HTTP {
		if err := add(current.ID, current.Owner, KindHTTP); err != nil {
			return err
		}
	}
	for _, current := range state.Network.Serial {
		if err := add(current.ID, current.Owner, KindSerial); err != nil {
			return err
		}
	}
	if len(expected) != len(state.Registry.Entries) {
		return fmt.Errorf(
			"%w: registry has %d entries but services own %d",
			ErrInvalidState,
			len(state.Registry.Entries),
			len(expected),
		)
	}
	for _, entry := range state.Registry.Entries {
		object, ok := expected[entry.ID]
		if !ok || object.kind != entry.Kind || object.owner != entry.Owner {
			return fmt.Errorf(
				"%w: registry entry %s has no matching service object",
				ErrInvalidState,
				entry.ID,
			)
		}
	}
	for index, event := range state.Events.Events {
		object, hasObject := expected[event.ServiceID]
		if event.ServiceID != 0 &&
			(!hasObject || object.owner != event.Owner) {
			return fmt.Errorf(
				"%w: queued event %d has no matching owned service",
				ErrInvalidState,
				index,
			)
		}
		validKind := func(kinds ...ObjectKind) bool {
			if event.ServiceID == 0 || !hasObject {
				return false
			}
			for _, kind := range kinds {
				if object.kind == kind {
					return true
				}
			}
			return false
		}
		switch event.Kind {
		case EventTimer:
			if !validKind(KindTimer) {
				return fmt.Errorf("%w: queued timer event %d", ErrInvalidState, index)
			}
		case EventAudioComplete:
			if !validKind(KindClip) {
				return fmt.Errorf("%w: queued audio event %d", ErrInvalidState, index)
			}
		case EventNetworkReady:
			if !validKind(KindSocket, KindHTTP) {
				return fmt.Errorf("%w: queued network event %d", ErrInvalidState, index)
			}
		case EventStorageComplete:
			if !validKind(KindFile, KindRecordBase) {
				return fmt.Errorf("%w: queued storage event %d", ErrInvalidState, index)
			}
		case EventSerialComplete:
			if !validKind(KindSerial) {
				return fmt.Errorf("%w: queued serial event %d", ErrInvalidState, index)
			}
		case EventRepaint:
			if event.ServiceID != 0 && !validKind(KindSurface) {
				return fmt.Errorf("%w: queued repaint event %d", ErrInvalidState, index)
			}
		case EventInputPress, EventInputRelease, EventInputRepeat, EventLifecycle:
			if event.ServiceID != 0 {
				return fmt.Errorf(
					"%w: queued owner event %d has a service ID",
					ErrInvalidState,
					index,
				)
			}
		}
	}
	for index, adapter := range state.Coordinator.Adapters {
		if adapter.LastSequence >= state.Events.NextSequence {
			return fmt.Errorf(
				"%w: adapter %d references a future event sequence",
				ErrInvalidState,
				index,
			)
		}
	}
	return nil
}

// QueueInput records or verifies a normalized input transition and then
// atomically queues it in the shared input/event services.
func (s *Services) QueueInput(
	owner OwnerID,
	control string,
	pressed bool,
	at time.Duration,
) error {
	if s == nil {
		return fmt.Errorf("%w: services are nil", ErrInvalidArgument)
	}
	// Frontends commonly use a zero timestamp for an immediate transition.
	// Once virtual time has advanced, applying that transition in the past
	// would make Input.Advance synthesize a backlog of repeats that never
	// occurred. Preserve future scheduling, but anchor late input at the
	// current service time.
	if now := s.Clock.Monotonic(); at < now {
		at = now
	}
	eventState := s.Events.Snapshot()
	inputState := s.Input.Snapshot()
	replayState := s.Replay.Snapshot()
	if s.Replay.Mode() == ReplayPlayback {
		if err := s.Replay.Consume(ReplayEntry{
			AtNS: int64(at), Kind: ReplayInput, Owner: owner,
			Name: control, Value: boolValue(pressed),
		}); err != nil {
			return err
		}
	}
	if err := s.Input.Change(s.Events, owner, control, pressed, at); err != nil {
		_ = s.Events.Restore(eventState)
		_ = s.Input.Restore(inputState)
		_ = s.Replay.Restore(replayState)
		return err
	}
	if err := s.Replay.RecordInput(owner, at, control, pressed); err != nil {
		_ = s.Events.Restore(eventState)
		_ = s.Input.Restore(inputState)
		_ = s.Replay.Restore(replayState)
		return err
	}
	return nil
}
