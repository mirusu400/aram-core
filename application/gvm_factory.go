package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/gvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gvm"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	// GVMKernelProfileID explicitly opts into bounded initial-dispatch diagnostics.
	// It is not a playability, rendering, input, timer-delivery or save-state profile.
	GVMKernelProfileID = "gvm-kernel-v1/skt/diagnostic"
	// GVMOperationalProfileID opts hash-qualified SKT corpora into event-dispatch
	// execution and presentation. It is not general GNEX support.
	GVMOperationalProfileID           = "gvm-kernel-v1/skt/operational"
	GVMOperationalSHA256              = "97fe208a02530ca21c6b47d7fa73cd60271aeeddd2305d2a972a217c97a4124f"
	GVMHackSignOperationalSHA256      = "3ddab790645e84c2d91ffb675d3842717da2a2c8c02d012ca800b20a7a55c9c0"
	GVMRagnarokOperationalSHA256      = "5c73bf6960bea012368232de7398284cb628b8969c74b8ea1311aea740a49f25"
	GVMNomOperationalSHA256           = "213e52c594bce9110bf9c78a3cb8fbdcf483d6d5c22c6d8c2da7f0cbef15f4e9"
	GVMFruitOperationalSHA256         = "d3cd7bcd6da306bfcc4c22d61f356d657f738c4ce1c52bdf213b64b46ecf4cc5"
	GVMMashiOperationalSHA256         = "5606569896e8e32aac04f9b2c2e09e91e6f3ebbb28b77bb0a2416ddfe2568437"
	GVMFantasyOperationalSHA256       = "c3605afd1699b57b06fd9d244248c6cb8b16d5f8dbe9bbdbd6ae5e8e17fabfb3"
	GVMStripOperationalSHA256         = "010a180b2b62d101d1ed42dfc8d26f4a22e2d84782685b8182fa149b330e286c"
	GVMNorthOperationalSHA256         = "8e1fe3b2690246c5ef0a7c515232aac62fc9e5f64af77b41b25a9d92ed4b1074"
	GVMNomAlternateSHA256             = "192cbe77da2a0bd590de85597dffe8649073867f72a74a7d1da03d16064805d4"
	GVMFruitAlternateSHA256           = "aa8b15a034ee4e5e7b034c733fccbd78f5ba4ce85789524346f1f083000f97e4"
	GVMFantasyAlternateSHA256         = "701358f8c4f0b8b6018630c83a8b5f30cf6fd192592a86c7d75f36f7ba5fb3a5"
	GVMNorthAlternateSHA256           = "8d6f2315f6b470124af8e9757d2d67a4ee9713432d762e52fb291d823497f17d"
	GVMKangHoDongOperationalSHA256    = "e1f8bc9c0e99fd9614427ac36c470777bd2a52df10f2acdd2b276ed72a6986b4"
	GVMCleanTimeOperationalSHA256     = "1500a7a3be1dab83e2e6212ec740a57e157d433068031e10f6dc383cf3465846"
	GVMSwordMasterOperationalSHA256   = "e1b2a614fb1e2d6b850b101fd0d26478e7d7f58cba004cfe3955995a87393792"
	GVMCrowOperationalSHA256          = "b7c4cf261fd128e8b5692fb38b5a56bf74b84cf63bccefc15804e202a9716521"
	GVMHeroLegendOperationalSHA256    = "3200793549cace4f64de37d8fbd851393abca42bc41bb3705f1d3aaf39a7fda8"
	GVMFightOinyongOperationalSHA256  = "f1fe501d0e8e4075ec1c0158d66fc31353fd221caad725bdaa474e5c4b175189"
	GVMFightOinyong2OperationalSHA256 = "a917e494a47d55e140bca181b16a3315d8482d0bff600693bace998d5212937c"
	GVMShiningStoneOperationalSHA256  = "757c56d2ce1ea7616702676929f0bb90f67dedeeb491408cd0c56595876d4974"
	defaultGVMWidth                   = int32(240)
	defaultGVMHeight                  = int32(240)
	operationalGVMWidth               = int32(120)
	operationalGVMHeight              = int32(80)
	defaultGVMBudget                  = uint64(1)
	defaultGVMOperationalBudget       = uint64(1_000_000)
)

type gvmOperationalConfig struct {
	width             int32
	height            int32
	originX           int
	originY           int
	timerDrivenEvents bool
	northMediaSuffix  bool
	nomMediaTail      bool
}

var gvmOperationalCorpora = map[string]gvmOperationalConfig{
	GVMOperationalSHA256: {width: operationalGVMWidth, height: operationalGVMHeight},
	// HackSign draws its 120x68 playfield around (0,0), from (-60,-34).
	GVMHackSignOperationalSHA256:      {width: operationalGVMWidth, height: operationalGVMHeight, originX: 60, originY: 34},
	GVMRagnarokOperationalSHA256:      {width: 120, height: 120},
	GVMNomOperationalSHA256:           {width: 120, height: 80, originX: 60, originY: 40, nomMediaTail: true},
	GVMFruitOperationalSHA256:         {width: 120, height: 80, originX: 60, originY: 40},
	GVMMashiOperationalSHA256:         {width: 120, height: 80, originX: 60, originY: 40},
	GVMFantasyOperationalSHA256:       {width: 120, height: 80, originX: 60, originY: 40},
	GVMStripOperationalSHA256:         {width: 120, height: 80, originX: 60, originY: 40, timerDrivenEvents: true},
	GVMNorthOperationalSHA256:         {width: 120, height: 80, originX: 60, originY: 40, northMediaSuffix: true},
	GVMNomAlternateSHA256:             {width: 120, height: 80, originX: 60, originY: 40, nomMediaTail: true},
	GVMFruitAlternateSHA256:           {width: 120, height: 80, originX: 60, originY: 40},
	GVMFantasyAlternateSHA256:         {width: 120, height: 80, originX: 60, originY: 40},
	GVMNorthAlternateSHA256:           {width: 120, height: 80, originX: 60, originY: 40, northMediaSuffix: true},
	GVMKangHoDongOperationalSHA256:    {width: 120, height: 80, originX: 60, originY: 40},
	GVMCleanTimeOperationalSHA256:     {width: 120, height: 80, originX: 60, originY: 40},
	GVMSwordMasterOperationalSHA256:   {width: 120, height: 80, originX: 60, originY: 40},
	GVMCrowOperationalSHA256:          {width: 120, height: 80, originX: 60, originY: 40},
	GVMHeroLegendOperationalSHA256:    {width: 120, height: 80, originX: 60, originY: 40},
	GVMFightOinyongOperationalSHA256:  {width: 120, height: 80, originX: 60, originY: 40, timerDrivenEvents: true},
	GVMFightOinyong2OperationalSHA256: {width: 120, height: 80, originX: 60, originY: 40, timerDrivenEvents: true},
	GVMShiningStoneOperationalSHA256:  {width: 120, height: 80, originX: 60, originY: 40, timerDrivenEvents: true},
}

var (
	ErrGVMInputUnavailable = errors.New("application: GVM input delivery is unavailable")
	ErrGVMStateUnavailable = errors.New("application: GVM save states are unavailable")
	errGVMTimerBoundary    = errors.New("application: GVM timer delivery boundary")
)

type gvmTimerBoundary struct {
	mode     uint8
	interval int16
	selector uint16
	reached  bool
	terminal bool
	fixed    bool
	frame    uint64
	slots    [3]gvmTimerSlot
}

func (s *gvmTimerBoundary) RequestGVMFixedTimer(interval int16) error {
	s.fixed = true
	s.interval, s.selector = interval, 1
	s.reached = true
	if s.terminal {
		return errGVMTimerBoundary
	}
	// The native fixed timer uses a separate host timer and counter. Its
	// callback timing is not inferred from the guest argument here.
	return nil
}

type gvmTimerSlot struct {
	interval int16
	selector uint16
	nextTick uint64
	active   bool
}

func (s *gvmTimerBoundary) RequestGVMTimer(interval int16, selector uint16) error {
	return s.RequestGVMTimerMode(0, interval, selector)
}

func (s *gvmTimerBoundary) RequestGVMTimerMode(mode uint8, interval int16, selector uint16) error {
	s.mode = mode
	s.interval, s.selector = interval, selector
	s.reached = true
	if s.terminal {
		return errGVMTimerBoundary
	}
	if mode >= uint8(len(s.slots)) {
		return fmt.Errorf("invalid GVM timer mode %d", mode)
	}
	// The native timer installer ignores intervals below 10 ms. A zero or
	// negative interval cancels the slot through its separate cancel path.
	if interval >= 10 {
		s.slots[mode] = gvmTimerSlot{
			interval: interval,
			selector: selector,
			nextTick: s.frame*1000 + uint64(interval)*60,
			active:   true,
		}
	}
	return nil
}

func (s *gvmTimerBoundary) CancelGVMTimer() error {
	s.slots[0] = gvmTimerSlot{}
	s.fixed = false
	s.mode, s.interval, s.selector, s.reached = 0, 0, 0, false
	return nil
}

func (s *gvmTimerBoundary) CancelGVMTimerMode(mode uint8) error {
	if mode < uint8(len(s.slots)) {
		s.slots[mode] = gvmTimerSlot{}
	}
	if s.reached && s.mode == mode {
		s.mode, s.interval, s.selector, s.reached = 0, 0, 0, false
	}
	return nil
}

// advanceFrame delivers at most one due native timer slot per emulated 60 Hz
// frame. A zero selector makes a slot one-shot; other selectors repeat.
func (s *gvmTimerBoundary) advanceFrame() (uint8, bool) {
	s.frame++
	now := s.frame * 1000
	for mode := range s.slots {
		slot := &s.slots[mode]
		if !slot.active || now < slot.nextTick {
			continue
		}
		if slot.selector == 0 {
			slot.active = false
		} else {
			period := uint64(slot.interval) * 60
			for slot.nextTick <= now {
				slot.nextTick += period
			}
		}
		return uint8(mode), true
	}
	return 0, false
}

type gvmFramePublisher struct {
	mu       sync.Mutex
	latest   image.Image
	presents uint64
}

func (p *gvmFramePublisher) PublishGVMFrame(frame image.Image) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.latest = frame
	p.presents++
	return nil
}

func (p *gvmFramePublisher) snapshot() (image.Image, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest, p.presents
}

type gvmDecodedMediaServices struct {
	clock         *shared.Clock
	registry      *shared.Registry
	media         *shared.Media
	events        *shared.EventBus
	owner         shared.OwnerID
	audioType     int32
	revision      uint64
	revisionEpoch time.Duration
}

func newGVMDecodedMediaServices(clock *shared.Clock) (*gvmDecodedMediaServices, error) {
	if clock == nil {
		return nil, fmt.Errorf("initialize GVM audio: %w", shared.ErrInvalidArgument)
	}
	registry := shared.NewRegistry(shared.DefaultMaxObjects)
	media, err := shared.NewMedia(registry, shared.DefaultMediaLimits())
	if err != nil {
		return nil, err
	}
	return &gvmDecodedMediaServices{
		clock:     clock,
		registry:  registry,
		media:     media,
		events:    shared.NewEventBus(shared.DefaultMaxEvents, shared.DefaultMaxEventData),
		owner:     1,
		audioType: 5,
	}, nil
}

func gvmEncodedSound(data []byte) (string, []byte, error) {
	if len(data) < 6 || data[0] != 2 || data[1] != 0 {
		return "", nil, fmt.Errorf("%w: unsupported GVM audio discriminator", shared.ErrMediaUnsupported)
	}
	payload := data[2:]
	switch string(payload[:4]) {
	case "MMMD":
		return "audio/mmf", payload, nil
	case "MThd":
		return "audio/midi", payload, nil
	case "RIFF":
		return "audio/wav", payload, nil
	default:
		return "", nil, fmt.Errorf("%w: unsupported GVM audio payload", shared.ErrMediaUnsupported)
	}
}

func (s *gvmDecodedMediaServices) LoadGVMMedia(index uint16, data []byte) error {
	mediaType, payload, err := gvmEncodedSound(data)
	if err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	registry := shared.NewRegistry(shared.DefaultMaxObjects)
	media, err := shared.NewMedia(registry, shared.DefaultMediaLimits())
	if err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	clip, err := media.CreateClip(s.owner, mediaType, uint64(len(payload)))
	if err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	if _, err := media.Append(s.owner, clip, payload); err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	if err := media.Prepare(s.owner, clip); err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	if err := media.Play(s.owner, clip, 1); err != nil {
		return fmt.Errorf("load GVM media %d: %w", index, err)
	}
	// Install only after the entire replacement has decoded and started. A bad
	// resource therefore cannot tear down the currently sounding clip.
	s.media = media
	s.registry = registry
	s.events = shared.NewEventBus(shared.DefaultMaxEvents, shared.DefaultMaxEventData)
	s.bumpRevision()
	return nil
}

func (s *gvmDecodedMediaServices) ResetGVMAudio(audioType int32) error {
	if audioType != 5 && audioType != 6 {
		return fmt.Errorf("reset GVM audio type %d: %w", audioType, shared.ErrInvalidArgument)
	}
	registry := shared.NewRegistry(shared.DefaultMaxObjects)
	media, err := shared.NewMedia(registry, shared.DefaultMediaLimits())
	if err != nil {
		return err
	}
	s.media = media
	s.registry = registry
	s.events = shared.NewEventBus(shared.DefaultMaxEvents, shared.DefaultMaxEventData)
	s.audioType = audioType
	s.bumpRevision()
	return nil
}

func (s *gvmDecodedMediaServices) bumpRevision() {
	if s.revision == ^uint64(0) {
		s.revision = 1
	} else {
		s.revision++
	}
	s.revisionEpoch = s.clock.Monotonic()
}

func (s *gvmDecodedMediaServices) advance(delta time.Duration) error {
	start := s.clock.Monotonic()
	end := start + delta
	if end < start {
		return fmt.Errorf("advance GVM audio: %w", shared.ErrLimitExceeded)
	}
	if err := s.media.Advance(start, end, s.events); err != nil {
		return err
	}
	if err := s.clock.Advance(delta); err != nil {
		return err
	}
	for {
		if _, ready := s.events.PopReady(end); !ready {
			break
		}
	}
	return nil
}

func (s *gvmDecodedMediaServices) drainTimed() (shared.AudioBuffer, time.Duration, uint64) {
	audio, start := s.media.DrainTimed()
	if len(audio.PCM16) == 0 {
		start = s.revisionEpoch
	}
	return audio, start, s.revision
}

// GVMDiagnosticBoundary describes the first host-service boundary reached by
// the explicit GVM diagnostic profile. It is diagnostic evidence, not delivery.
type GVMDiagnosticBoundary struct {
	Kind     string
	Interval int16
	Selector uint16
}

type gvmMachine struct {
	mu                     sync.Mutex
	state                  machinecore.State
	source                 machinecore.Source
	packageSGS             []byte
	vm                     *gvm.VM
	timer                  *gvmTimerBoundary
	budget                 uint64
	lastResult             cpu.Result
	operational            bool
	eventEntry             uint32
	inputEntry             uint32
	frames                 *gvmFramePublisher
	clock                  *shared.Clock
	random                 *shared.Random
	display                *gvmhost.DisplayAdapter
	mediaServices          *gvmDecodedMediaServices
	persistent             *gvmPersistentStore
	width                  int32
	height                 int32
	originX                int
	originY                int
	inputDispatches        uint64
	lastInputCode          uint16
	lastInputResult        cpu.Result
	timerDrivenEvents      bool
	northMediaSuffix       bool
	nomMediaTail           bool
	audioGeneration        uint64
	audioGenerationPending bool
	audioEpochGuestNS      int64
	audioCursorSample      uint64
	audioCursorValid       bool
	mediaOutputRevision    uint64
	closed                 bool
}

func (f Factory) createGVMMachine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	explicit := source.ProfileID == GVMKernelProfileID || source.ProfileID == GVMOperationalProfileID
	automatic := source.ProfileID == "" &&
		(source.Format == string(loader.KindGNEX) || source.Format == string(loader.KindJava))
	if !explicit && !automatic {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if err := source.Validate(); err != nil {
		return nil, true, err
	}
	if source.Size > maxApplicationSize {
		return nil, true, fmt.Errorf("load %q: source size %d exceeds limit", source.Name, source.Size)
	}
	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil {
		return nil, true, fmt.Errorf("read GVM application: %w", err)
	}
	if int64(len(data)) != source.Size {
		return nil, true, fmt.Errorf("read GVM application: %w", io.ErrUnexpectedEOF)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, actualSHA256)
	}
	source.SHA256 = actualSHA256
	config, qualified := gvmOperationalCorpora[actualSHA256]
	if automatic && !qualified {
		return nil, false, nil
	}
	pkg, err := gnex.Inspect(data)
	if err != nil {
		return nil, true, fmt.Errorf("inspect GVM application: %w", err)
	}
	operational := source.ProfileID == GVMOperationalProfileID || automatic
	if operational && !qualified {
		return nil, true, fmt.Errorf("load %q: operational GVM profile does not support outer SHA-256 %s", source.Name, actualSHA256)
	}
	if automatic {
		source.ProfileID = GVMOperationalProfileID
	}
	source.Format = string(loader.KindGNEX)
	budget := f.FrameRunBudget
	if budget == 0 {
		if operational {
			budget = defaultGVMOperationalBudget
		} else {
			budget = f.RunBudget
			if budget == 0 {
				budget = defaultGVMBudget
			}
		}
	}
	machine := &gvmMachine{
		state:             machinecore.StateReady,
		source:            source,
		packageSGS:        bytes.Clone(pkg.SGS),
		budget:            budget,
		operational:       operational,
		width:             config.width,
		height:            config.height,
		originX:           config.originX,
		originY:           config.originY,
		timerDrivenEvents: config.timerDrivenEvents,
		northMediaSuffix:  config.northMediaSuffix,
		nomMediaTail:      config.nomMediaTail,
	}
	if operational {
		headerOffset := pkg.Header.PrefixOffset
		if headerOffset < 0 || len(pkg.SGS)-headerOffset < 0x24 {
			return nil, true, fmt.Errorf("initialize operational GVM: SGS event entries at 0x20 and 0x22 are unavailable")
		}
		machine.eventEntry = uint32(binary.LittleEndian.Uint16(pkg.SGS[headerOffset+0x20 : headerOffset+0x22]))
		machine.inputEntry = uint32(binary.LittleEndian.Uint16(pkg.SGS[headerOffset+0x22 : headerOffset+0x24]))
	}
	if err := machine.resetVMLocked(); err != nil {
		return nil, true, err
	}
	return machine, true, nil
}

func gvmAddressSpace(image gnex.ExecutionImage) gvm.AddressSpace {
	space := gvm.AddressSpace{
		FileStart:  uint32(image.SymbolFileStart),
		FileLength: uint32(image.SymbolFileEnd - image.SymbolFileStart),
		RAM:        image.SymbolRAM,
		Symbols:    make([]gvm.AddressSymbol, 0, len(image.Symbols)),
	}
	for _, symbol := range image.Symbols {
		binding := gvm.AddressSymbol{
			Region: gvm.AddressFile,
			Offset: uint32(symbol.BufferOffset - image.SymbolFileStart),
			Length: uint32(len(symbol.Data)),
		}
		if symbol.Mutable {
			binding.Region = gvm.AddressRAM
			binding.Offset = uint32(symbol.RAMOffset)
		}
		space.Symbols = append(space.Symbols, binding)
	}
	return space
}

func (m *gvmMachine) resetVMLocked() error {
	replacingVM := m.vm != nil
	image, err := gnex.DecodeExecutionImage(m.packageSGS)
	if err != nil {
		return fmt.Errorf("decode GVM execution image: %w", err)
	}
	clock := new(shared.Clock)
	if err := clock.Restore(shared.ClockState{WallEpochMillis: 0, TimezoneOffsetMins: 540, Locale: "ko-KR"}); err != nil {
		return fmt.Errorf("initialize GVM diagnostic clock: %w", err)
	}
	random := shared.NewRandom(1, 4)
	if err := random.SetLCG214013Seed("gvm", 1); err != nil {
		return fmt.Errorf("initialize GVM diagnostic random stream: %w", err)
	}
	timer := &gvmTimerBoundary{terminal: !m.operational}
	width, height := defaultGVMWidth, defaultGVMHeight
	var display *gvmhost.DisplayAdapter
	var mediaServices *gvmDecodedMediaServices
	var media []gvm.MediaResource
	if m.operational {
		width, height = m.width, m.height
		m.frames = new(gvmFramePublisher)
		textServices, textErr := shared.NewServices(shared.DefaultConfig())
		if textErr != nil {
			return fmt.Errorf("initialize operational GVM text: %w", textErr)
		}
		display, err = gvmhost.NewDisplayAdapter(gvmhost.DisplayConfig{
			Width: int(width), Height: int(height),
			OriginX: m.originX, OriginY: m.originY,
			Orientation: gvmhost.DisplayOrientationDefault,
			Palette:     gvmhost.SKTCompatibilityPalette{}, Publisher: m.frames,
			Text: textServices.Text, TextOwner: 1,
		})
		if err != nil {
			return fmt.Errorf("initialize operational GVM display: %w", err)
		}
		mediaServices, err = newGVMDecodedMediaServices(clock)
		if err != nil {
			return fmt.Errorf("initialize operational GVM audio: %w", err)
		}
		if m.persistent == nil {
			m.persistent = new(gvmPersistentStore)
		}
		media = make([]gvm.MediaResource, len(image.Media))
		if m.northMediaSuffix {
			if len(image.Media) <= 502 || !image.Media[502].Mutable || len(image.Media[502].Data) != 0 ||
				len(image.Media[79].Data) != 80 || bytes.IndexByte(image.Media[79].Data, 0) != 78 ||
				bytes.Count(image.Media[79].Data, []byte{'|'}) != 4 {
				return errors.New("north media boundary has unexpected source descriptor")
			}
		}
		if m.nomMediaTail && (len(image.Media) <= 161 || !bytes.Equal(image.Media[160].Data, []byte{12, 0}) ||
			image.Media[161].BufferOffset != image.Media[160].BufferOffset+2 ||
			uint64(image.Media[160].BufferOffset)+14 > uint64(len(image.Buffer))) {
			return errors.New("nom media boundary has unexpected source descriptor")
		}
		for i := range image.Media {
			media[i] = gvm.MediaResource{Data: image.Media[i].Data}
		}
		if m.northMediaSuffix {
			media[502].VirtualSuffix = '|'
			media[502].HasVirtualSuffix = true
		}
		if m.nomMediaTail {
			// This exact title reads 12 bytes after a two-byte immutable
			// scalar. Keep the original contiguous SGS tail available to
			// that byte-indexing path within a bounded view.
			offset := image.Media[160].BufferOffset
			media[160].Data = image.Buffer[offset : offset+14]
		}
	} else {
		m.frames = nil
	}
	services := &gvm.ServiceConfig{
		DeviceQuery:    &gvm.DeviceQueryProfile{Width: width, Height: height, AudioType: 5},
		DeviceIdentity: &gvm.DeviceIdentityProfile{MIN: "0111234567", UserID: "98B49700F"},
		Clock:          clock, ClockPolicy: gvm.FixedOffsetNoDST,
		Random: random, RandomStream: "gvm",
		Timer: timer,
	}
	if m.operational {
		services.DisplayClear = display
		services.DisplayZero = display
		services.DisplayFill = display
		services.DisplayPresent = display
		services.DisplayCopy = display
		services.MappingSelect = display
		services.ColorSelect = display
		services.RectangleDraw = display
		services.LineDraw = display
		services.PointDraw = display
		services.EllipseDraw = display
		services.RectangleFill = display
		services.SpriteDraw = display
		services.SpriteTransform = display
		services.SpritePalette = display
		services.SpriteTransformPalette = display
		services.TextDraw = display
		services.AudioReset = mediaServices
		services.Media = media
		services.MediaLoad = mediaServices
		services.DataRead = m.persistent
		services.DataWrite = m.persistent
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(
		image.Buffer,
		uint32(image.Entry),
		gvmAddressSpace(image),
		services,
	)
	if err != nil {
		return fmt.Errorf("initialize GVM kernel: %w", err)
	}
	m.vm = vm
	m.timer = timer
	m.clock = clock
	m.random = random
	m.display = display
	m.mediaServices = mediaServices
	if m.operational {
		m.mediaOutputRevision = mediaServices.revision
		if m.audioGeneration == 0 {
			m.audioGeneration = 1
		} else if replacingVM {
			m.nextGVMAudioGenerationLocked(0)
		}
	}
	m.lastResult = cpu.Result{}
	m.inputDispatches = 0
	m.lastInputCode = 0
	m.lastInputResult = cpu.Result{}
	return nil
}

func (m *gvmMachine) Load(context.Context, machinecore.Source) error {
	return fmt.Errorf("load from %s: %w", m.State(), ErrInvalidState)
}

func (m *gvmMachine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *gvmMachine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if m.operational {
		return m.runOperationalLocked(ctx, false, -1)
	}
	return m.runLocked(ctx)
}

func (m *gvmMachine) runOperationalLocked(ctx context.Context, dispatchEvent bool, timerMode int) error {
	m.state = machinecore.StateRunning
	if dispatchEvent && m.vm.Halted() {
		var started bool
		var err error
		if timerMode >= 0 {
			// The native timer wrapper publishes the timer slot in symbol 0.
			started, err = m.vm.BeginSymbolDispatch(0, uint16(timerMode), m.eventEntry)
		} else {
			started, err = m.vm.BeginDispatch(m.eventEntry)
		}
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("begin operational GVM event dispatch at offset %d: %w", m.eventEntry, err)
		}
		if !started {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, PC: uint32(m.vm.PC())}
			return nil
		}
	}
	var completed uint64
	for completed < m.budget {
		if err := ctx.Err(); err != nil {
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			return err
		}
		if err := m.vm.Step(); err != nil {
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			m.state = machinecore.StateFaulted
			return fmt.Errorf("execute operational GVM at offset %d after %d instructions: %w", m.vm.PC(), completed, err)
		}
		completed++
		if m.vm.Halted() {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, Instructions: completed, PC: uint32(m.vm.PC())}
			return nil
		}
	}
	err := fmt.Errorf("operational GVM dispatch did not halt within %d instructions", m.budget)
	m.lastResult = cpu.Result{Reason: cpu.StopBudget, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
	m.state = machinecore.StateFaulted
	return err
}

func (m *gvmMachine) runLocked(ctx context.Context) error {
	m.state = machinecore.StateRunning
	var completed uint64
	for completed < m.budget {
		if err := ctx.Err(); err != nil {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			return err
		}
		err := m.vm.Step()
		if err != nil {
			if errors.Is(err, errGVMTimerBoundary) {
				m.lastResult = cpu.Result{Reason: cpu.StopBreakpoint, Instructions: completed, PC: uint32(m.vm.PC())}
				// The diagnostic profile deliberately has no timer delivery or guest
				// redispatch. This boundary is terminal until an explicit reset.
				m.state = machinecore.StateStopped
				return nil
			}
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			m.state = machinecore.StateFaulted
			return fmt.Errorf("execute GVM at offset %d after %d instructions: %w", m.vm.PC(), completed, err)
		}
		completed++
		if m.vm.Halted() {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, Instructions: completed, PC: uint32(m.vm.PC())}
			m.state = machinecore.StateStopped
			return nil
		}
	}
	m.lastResult = cpu.Result{Reason: cpu.StopBudget, Instructions: completed, PC: uint32(m.vm.PC())}
	m.state = machinecore.StatePaused
	return nil
}

func (m *gvmMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StatePaused {
		return nil
	}
	if m.operational && m.state == machinecore.StateRunning {
		m.state = machinecore.StatePaused
		return nil
	}
	return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
}

func (m *gvmMachine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
	}
	if m.operational {
		m.state = machinecore.StateRunning
		return nil
	}
	return m.runLocked(context.Background())
}

func (m *gvmMachine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	if err := m.discardGVMAudioLocked(); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("stop GVM audio: %w", err)
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *gvmMachine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning && !(m.operational && m.vm != nil && m.vm.Halted()) {
		return fmt.Errorf("reset from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.resetVMLocked(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	m.state = machinecore.StateReady
	return nil
}

func (m *gvmMachine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.operational {
		if m.state != machinecore.StateRunning {
			return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
		}
		if m.timerDrivenEvents {
			mode, due := m.timer.advanceFrame()
			if due {
				if err := m.runOperationalLocked(ctx, true, int(mode)); err != nil {
					return err
				}
			}
			return m.advanceGVMAudioLocked(time.Second / 60)
		}
		if err := m.runOperationalLocked(ctx, true, -1); err != nil {
			return err
		}
		return m.advanceGVMAudioLocked(time.Second / 60)
	}
	if m.state != machinecore.StatePaused && m.state != machinecore.StateReady {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	return m.runLocked(ctx)
}

func (m *gvmMachine) advanceGVMAudioLocked(delta time.Duration) error {
	if m.mediaServices == nil {
		return nil
	}
	if err := m.mediaServices.advance(delta); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("advance operational GVM audio: %w", err)
	}
	return nil
}

func (m *gvmMachine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.operational || m.state != machinecore.StateRunning || m.vm == nil || !m.vm.Halted() {
		return ErrGVMInputUnavailable
	}
	key, known := guest.InputKeyCode(event.Control)
	if !known {
		return ErrGVMInputUnavailable
	}
	guestCode, supported := gvmhost.SKTGuestCode(key)
	if !supported {
		return ErrGVMInputUnavailable
	}
	// The selected native path has an authenticated press callback at H+0x22.
	// Its release path only clears host-side hit state and does not dispatch a
	// guest callback, so releases are deliberately accepted as no-ops.
	if !event.Pressed {
		return nil
	}
	started, err := m.vm.BeginSymbolDispatch(0, guestCode, m.inputEntry)
	if err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("begin operational GVM input dispatch at offset %d: %w", m.inputEntry, err)
	}
	if !started {
		m.lastResult = cpu.Result{Reason: cpu.StopExited, PC: uint32(m.vm.PC())}
		return nil
	}
	if err := m.runOperationalLocked(context.Background(), false, -1); err != nil {
		return err
	}
	m.inputDispatches++
	m.lastInputCode = guestCode
	m.lastInputResult = m.lastResult
	return nil
}

func (m *gvmMachine) Framebuffer() image.Image {
	m.mu.Lock()
	frames := m.frames
	m.mu.Unlock()
	if frames == nil {
		return nil
	}
	frame, _ := frames.snapshot()
	return frame
}

// GVMPresentDiagnostics is an optional, narrow operational-profile diagnostic.
type GVMPresentDiagnostics interface{ GVMPresentCount() uint64 }

// GVMInputDispatchDiagnostics is an optional operational-profile observation
// of authenticated guest input callbacks. It does not imply a visual change.
type GVMInputDispatchDiagnostics struct {
	DispatchCount uint64
	GuestCode     uint16
	Result        cpu.Result
}

func (m *gvmMachine) GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return GVMInputDispatchDiagnostics{
		DispatchCount: m.inputDispatches,
		GuestCode:     m.lastInputCode,
		Result:        m.lastInputResult,
	}
}

func (m *gvmMachine) GVMPresentCount() uint64 {
	m.mu.Lock()
	frames := m.frames
	m.mu.Unlock()
	if frames == nil {
		return 0
	}
	_, count := frames.snapshot()
	return count
}

func (m *gvmMachine) DrainAudio() machinecore.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mediaServices == nil {
		return machinecore.AudioChunk{}
	}
	audio, start, revision := m.mediaServices.drainTimed()
	if audio.SampleRate <= 0 || audio.Channels <= 0 || len(audio.PCM16) == 0 ||
		len(audio.PCM16)%audio.Channels != 0 {
		if revision != m.mediaOutputRevision {
			m.nextGVMAudioGenerationLocked(start)
			m.mediaOutputRevision = revision
		}
		if m.audioGenerationPending {
			m.audioGenerationPending = false
			return machinecore.AudioChunk{
				SampleRate:   audio.SampleRate,
				Channels:     audio.Channels,
				StartGuestNS: m.audioEpochGuestNS,
				Generation:   m.audioGeneration,
			}
		}
		return machinecore.AudioChunk{}
	}
	frames := len(audio.PCM16) / audio.Channels
	if revision != m.mediaOutputRevision {
		m.nextGVMAudioGenerationLocked(start)
		m.mediaOutputRevision = revision
	}
	startSample := gvmSampleCursor(time.Duration(int64(start)-m.audioEpochGuestNS), audio.SampleRate)
	if m.audioCursorValid {
		slack := gvmSampleCursor(time.Millisecond, audio.SampleRate)
		if gvmDistanceWithin(startSample, m.audioCursorSample, slack) {
			startSample = m.audioCursorSample
		}
	}
	chunk := machinecore.AudioChunk{
		SampleRate:   audio.SampleRate,
		Channels:     audio.Channels,
		PCM16:        audio.PCM16,
		StartGuestNS: int64(start),
		StartSample:  startSample,
		Generation:   m.audioGeneration,
	}
	m.audioCursorSample = startSample + uint64(frames)
	m.audioCursorValid = true
	m.audioGenerationPending = false
	return chunk
}

func gvmDistanceWithin(left, right, limit uint64) bool {
	if left >= right {
		return left-right <= limit
	}
	return right-left <= limit
}

func gvmSampleCursor(elapsed time.Duration, sampleRate int) uint64 {
	if elapsed <= 0 || sampleRate <= 0 {
		return 0
	}
	seconds := uint64(elapsed / time.Second)
	remainder := uint64(elapsed % time.Second)
	return seconds*uint64(sampleRate) + remainder*uint64(sampleRate)/uint64(time.Second)
}

func (m *gvmMachine) nextGVMAudioGenerationLocked(epoch time.Duration) {
	if m.audioGeneration == 0 || m.audioGeneration == ^uint64(0) {
		m.audioGeneration = 1
	} else {
		m.audioGeneration++
	}
	m.audioGenerationPending = true
	m.audioEpochGuestNS = int64(max(epoch, 0))
	m.audioCursorSample = 0
	m.audioCursorValid = false
}

func (m *gvmMachine) discardGVMAudioLocked() error {
	if m.mediaServices == nil {
		return nil
	}
	return m.mediaServices.ResetGVMAudio(m.mediaServices.audioType)
}

func (m *gvmMachine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	err := m.discardGVMAudioLocked()
	m.closed = true
	m.vm = nil
	m.state = machinecore.StateStopped
	if err != nil {
		return fmt.Errorf("close GVM audio: %w", err)
	}
	return nil
}

func (m *gvmMachine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}

func (m *gvmMachine) LastResult() cpu.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastResult
}

func (m *gvmMachine) GVMDiagnosticBoundary() (GVMDiagnosticBoundary, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer == nil || !m.timer.reached {
		return GVMDiagnosticBoundary{}, false
	}
	return GVMDiagnosticBoundary{
		Kind:     "timer-request",
		Interval: m.timer.interval,
		Selector: m.timer.selector,
	}, true
}
