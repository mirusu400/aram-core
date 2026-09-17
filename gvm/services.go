package gvm

import (
	"errors"
	"fmt"
	"math"
	"strings"

	gruntime "github.com/mirusu400/aram-core/runtime"
)

var (
	ErrInvalidServiceConfig       = errors.New("gvm: invalid service configuration")
	ErrDeviceQueryUnavailable     = errors.New("gvm: device query service unavailable")
	ErrClockUnavailable           = errors.New("gvm: clock service unavailable")
	ErrClockRange                 = errors.New("gvm: clock outside supported civil-time range")
	ErrRandomUnavailable          = errors.New("gvm: random service unavailable")
	ErrTimerUnavailable           = errors.New("gvm: timer request service unavailable")
	ErrDisplayClearUnavailable    = errors.New("gvm: display clear service unavailable")
	ErrDisplayZeroUnavailable     = errors.New("gvm: display zero-clear service unavailable")
	ErrMappingSelectUnavailable   = errors.New("gvm: mapping selection service unavailable")
	ErrAudioResetUnavailable      = errors.New("gvm: audio reset service unavailable")
	ErrMediaLoadUnavailable       = errors.New("gvm: media load service unavailable")
	ErrInvalidMediaIndex          = errors.New("gvm: invalid media index")
	ErrDisplayPresentUnavailable  = errors.New("gvm: display presentation service unavailable")
	ErrSpriteDrawUnavailable      = errors.New("gvm: sprite draw service unavailable")
	ErrSpriteTransformUnavailable = errors.New("gvm: transformed sprite draw service unavailable")
	ErrSpriteBufferUnavailable    = errors.New("gvm: sprite buffer draw service unavailable")
	ErrDisplayCopyUnavailable     = errors.New("gvm: display buffer copy service unavailable")
	ErrDisplayFillUnavailable     = errors.New("gvm: display fill service unavailable")
	ErrColorSelectUnavailable     = errors.New("gvm: drawing color selection service unavailable")
	ErrRectangleDrawUnavailable   = errors.New("gvm: rectangle outline service unavailable")
	ErrRectangleFillUnavailable   = errors.New("gvm: rectangle fill service unavailable")
	ErrTextDrawUnavailable        = errors.New("gvm: text draw service unavailable")
	ErrDataReadUnavailable        = errors.New("gvm: persistent data read service unavailable")
)

// DeviceQueryProfile is explicit GVM adapter state, not a detected handset or
// audio capability enum. Width and Height must be 1..256 as portable safety
// policy. AudioType retains all signed32 bits for the reference's exact transform.
// The constructor copies this value; there is no mid-run profile setter.
type DeviceQueryProfile struct {
	Width     int32
	Height    int32
	AudioType int32
}

// CivilTimePolicy identifies an explicitly selected portable conversion policy.
// Zero is absent, not an implicit UTC or host-timezone policy.
type CivilTimePolicy uint8

// FixedOffsetNoDST uses the borrowed clock's whole-minute fixed UTC offset.
// It does not reproduce Windows timezone discovery or DST transition behavior.
const FixedOffsetNoDST CivilTimePolicy = 1

// TimerRequestSink accepts opcode9a's two raw guest arguments after the VM has
// validated stack state. The sink is an adapter boundary, not timer delivery:
// it owns mirroring, interval<10 bypass, installation, replacement, cancellation,
// serialization and eventual guest callback policy. It must return an error
// without accepting the request when it cannot complete atomically. The owner
// must serialize calls with VM execution, and implementations must not reenter
// the VM from RequestGVMTimer.
type TimerRequestSink interface {
	RequestGVMTimer(intervalMillis int16, selector uint16) error
}

// DisplayClearSink accepts opcode55's guest-requested drawing-buffer clear.
// The authenticated native handler fills the configured one-byte-per-pixel
// drawing buffer with 0xff. This boundary does not present a frame, convert
// packed colors, select a palette, or imply that display state is initialized.
// Implementations must complete atomically and must not reenter the VM.
type DisplayClearSink interface {
	ClearGVMDisplay() error
}

// DisplayZeroSink accepts opcode56's raw zero-byte drawing-buffer clear. It is
// distinct from palette/remap fill and from opcode55's raw 0xff clear.
type DisplayZeroSink interface {
	ZeroGVMDisplay() error
}

// DisplayFillSink accepts opcode57's signed remainder modulo182. The provider
// owns the unresolved selector remap row and atomic drawing-buffer fill. This
// request neither selects a mapping row nor publishes a frame. Implementations
// must complete atomically and must not reenter the VM.
type DisplayFillSink interface {
	FillGVMDisplay(selector int16) error
}

// DisplayPresentSink accepts an unsuppressed opcode78 guest submission. The
// provider owns initialized geometry, drawing/display buffers, packed-color
// conversion, immutable publication and frame sequencing. This interface does
// not make native repaint events or UI clears into guest frames. Implementations
// must complete atomically and must not reenter the VM.
type DisplayPresentSink interface {
	PresentGVMDisplay() error
}

// DisplayBuffer names the two private one-byte-per-pixel buffers copied by
// opcodes76 and77. Drawing is opcode78's guest presentation source; Auxiliary
// is the second initialized buffer. These names do not imply publication.
type DisplayBuffer uint8

const (
	DisplayBufferDrawing DisplayBuffer = iota + 1
	DisplayBufferAuxiliary
)

// DisplayCopySink accepts an atomic full-extent copy between initialized GVM
// buffers. It must preserve overlap-safe provider policy and must not publish a
// frame. Geometry, allocation and extent validation belong to the adapter.
// Implementations must complete atomically and must not reenter the VM.
type DisplayCopySink interface {
	CopyGVMDisplay(source, destination DisplayBuffer) error
}

// MappingSelectSink accepts opcode59's clamped selector in the range 0..6.
// The exact-build native target updates private drawing-remap state. The row
// contents and a portable color policy are not established, so this interface
// exposes selection only and must not be treated as presentation. Implementations
// must complete atomically and must not reenter the VM.
type MappingSelectSink interface {
	SelectGVMMapping(selector uint8) error
}

// ColorSelectSink accepts opcode5e's normalized low-byte selector. The exact-build
// native handler reduces only the low byte modulo182, records that selector, and
// derives an active packed drawing byte from the current remap row. Selector4 is
// transparent in the native pixel writer. The provider owns remap state and must
// apply the update atomically; this request neither draws nor presents a frame.
// Implementations must not reenter the VM.
type ColorSelectSink interface {
	SelectGVMColor(selector uint8) error
}

// RectangleFillSink accepts opcode63's four signed guest coordinates in stack
// order. The exact-build native callee sorts each axis, clips to its private
// inclusive bounds, and invokes the active drawing-color writer for every pixel.
// The provider owns those bounds, active color/remap state, transparency and the
// drawing buffer. Implementations must complete atomically and must not reenter
// the VM; this request does not publish a frame.
type RectangleFillSink interface {
	FillGVMRectangle(x1, y1, x2, y2 int16) error
}

// RectangleDrawSink accepts opcode62's four signed guest coordinates in stack
// order. The exact-build handler at 0x418e10 forwards them to 0x40e600, which
// draws the two clipped horizontal and two clipped vertical inclusive edges.
// The provider owns active color/remap state and the drawing buffer. It must
// complete atomically, must not reenter the VM, and does not publish a frame.
type RectangleDrawSink interface {
	DrawGVMRectangle(x1, y1, x2, y2 int16) error
}

// SpriteDrawSink accepts opcode6f's validated media payload and signed guest
// coordinates. The exact-build native callee recognizes private sprite types
// and applies resource-local anchors before rasterizing through the active remap
// state. This boundary deliberately does not guess those formats or mutate a
// display buffer. Resource is an independent copy owned by the callee. The
// implementation must complete atomically and must not reenter the VM.
type SpriteDrawSink interface {
	DrawGVMSprite(resource []byte, x, y int16) error
}

// SpriteTransformSink accepts opcode70's validated media payload, signed guest
// coordinates, and raw nonzero horizontal-mirror selection. The exact-build
// handler at 0x4194b0 calls 0x4107b0, whose zero path matches normal sprite
// placement and whose nonzero path selects the mirrored type rasterizers.
// Resource is an independent copy. Implementations must complete atomically and
// must not reenter the VM.
type SpriteTransformSink interface {
	DrawGVMTransformedSprite(resource []byte, x, y int16, mirrorHorizontal bool) error
}

// SpriteBufferSink accepts opcode71's sprite payload and a private full-screen
// indexed buffer snapshot. It must rasterize atomically without retaining the
// slices. The VM publishes the returned buffer only after successful completion.
type SpriteBufferSink interface {
	DrawGVMSpriteBuffer(resource, buffer []byte, x, y int16) error
}

// TextDrawStyle is the normalized private drawing state consumed by opcodes
// 6a..6d. Mode is 0..3, primary and secondary are 0..181 palette selectors,
// and alignment is 0..2. Opcode6a uses primary only and passes background=false.
type TextDrawStyle struct {
	Mode       uint8
	Primary    uint8
	Secondary  uint8
	Alignment  uint8
	Background bool
}

// TextDrawSink accepts opcode6a's validated NUL-terminated text resource,
// signed coordinates, and a snapshot of the VM-owned text drawing state. The
// exact-build native renderer decodes its legacy Korean byte stream and clips
// glyph pixels into the current drawing buffer. Resource is an independent
// copy. Implementations must complete atomically and must not reenter the VM.
type TextDrawSink interface {
	DrawGVMText(resource []byte, x, y int16, style TextDrawStyle) error
}

// AudioResetSink accepts opcode91's provider-selected audio type. The exact-build
// handler initializes or resets type-specific native audio objects. This request
// boundary does not model playback, media decoding, device ownership or teardown.
// Implementations must complete atomically and must not reenter the VM.
type AudioResetSink interface {
	ResetGVMAudio(audioType int32) error
}

// MediaResource is owned immutable input for opcode90. Construction copies Data.
// The first payload byte is the exact-build format discriminator consumed by the
// native audio-type adapter; GVM does not assign it a portable codec name.
type MediaResource struct {
	Data []byte
}

// MediaLoadSink accepts an independent copy of one validated media payload.
// It owns codec/device policy. Mutating data cannot change VM-owned resources.
// Implementations must complete atomically and must not reenter the VM.
type MediaLoadSink interface {
	LoadGVMMedia(index uint16, data []byte) error
}

// DataReadSink supplies opcode98's persistent byte payload. The VM validates the
// tagged destination and signed word extent before calling it, requires exactly
// size bytes, then commits the copy and two-word pop atomically. Providers must
// not retain or mutate the returned slice after the call.
type DataReadSink interface {
	ReadGVMData(size uint32) ([]byte, error)
}

// ServiceConfig opts independently into device query (51), display clear (55),
// remapped display fill (57), mapping selection (59), drawing-color selection
// (5e), rectangle outline/fill (62/63), sprite drawing (6f/70/71), display copies
// (76/77), presentation (78), media load (90), audio reset (91), persistent
// data read (98), civil clock (b9), random range (a1), and timer
// requests (9a).
// Clock and ClockPolicy must be supplied together. The Clock pointer is borrowed,
// not copied: the owner must serialize VM execution and clock Advance/Restore.
// GVM never advances, restores or replaces that clock. A nonnil clock alone does
// not establish an actual device's time or profile. Callers must select both.
// No VM save-state or factory/startup integration is supplied by this API.
type ServiceConfig struct {
	DeviceQuery *DeviceQueryProfile
	Clock       *gruntime.Clock
	ClockPolicy CivilTimePolicy
	// Random is borrowed, and RandomStream is copied. Supply both or neither.
	// The owner must serialize execution with Random draws, seeding and Restore.
	// Construction validates only the name, never looking up or creating a stream.
	// Unequal A1 operands require an explicitly seeded LCG214013Output15 stream,
	// revalidated on every draw. Equal operands do not consult Random.
	Random       *gruntime.Random
	RandomStream string
	// Timer is borrowed. GVM only forwards authenticated opcode9a requests; it
	// does not advance time, install shared runtime timers or deliver callbacks.
	Timer TimerRequestSink
	// DisplayClear is borrowed. Opcode55 requests only a drawing-buffer fill;
	// it does not publish or validate a frame.
	DisplayClear DisplayClearSink
	DisplayZero  DisplayZeroSink
	// DisplayFill is borrowed. Opcode57 forwards only the normalized selector;
	// the provider owns remapping and drawing-buffer mutation.
	DisplayFill DisplayFillSink
	// DisplayPresent is borrowed. Opcode78 forwarding assumes the adapter has
	// already established that the native suppression gate is clear.
	DisplayPresent DisplayPresentSink
	// DisplayCopy is borrowed. Opcodes76/77 copy the configured full display
	// extent between drawing and auxiliary buffers without presentation.
	DisplayCopy DisplayCopySink
	// MappingSelect is borrowed. Opcode59 forwards only the authenticated clamped
	// selector. The provider owns any remap-row policy and atomic mutation.
	MappingSelect MappingSelectSink
	// ColorSelect is borrowed. Opcode5e forwards only low-byte modulo182. The
	// provider owns the active remap row, packed byte and transparent-color policy.
	ColorSelect ColorSelectSink
	// RectangleDraw is borrowed. Opcode62 forwards four signed coordinates; the
	// provider owns clipped inclusive edge drawing and active color state.
	RectangleDraw RectangleDrawSink
	// RectangleFill is borrowed. Opcode63 forwards four signed coordinates; the
	// provider owns sorting, clipping, active color and drawing-buffer mutation.
	RectangleFill RectangleFillSink
	// SpriteDraw is borrowed. Opcode6f forwards a copied media payload and the
	// two signed coordinates. The provider owns format validation and raster state.
	SpriteDraw SpriteDrawSink
	// SpriteTransform is borrowed. Opcode70 forwards a copied media payload,
	// signed coordinates and a raw-zero/nonzero horizontal mirror selection.
	SpriteTransform SpriteTransformSink
	SpriteBuffer    SpriteBufferSink
	// TextDraw is borrowed. Opcode6a forwards a copied text resource and the
	// current normalized text style without exposing mutable VM state.
	TextDraw TextDrawSink
	// AudioReset is borrowed. Opcode91 also requires DeviceQuery so the adapter
	// receives the same explicit AudioType selected for opcode51.
	AudioReset AudioResetSink
	// Media is copied at construction. MediaLoad is borrowed and receives a new
	// independent payload copy for every opcode90 request.
	Media     []MediaResource
	MediaLoad MediaLoadSink
	DataRead  DataReadSink
}

type serviceState struct {
	deviceQuery     *DeviceQueryProfile
	clock           *gruntime.Clock
	clockPolicy     CivilTimePolicy
	random          *gruntime.Random
	randomStream    string
	timer           TimerRequestSink
	displayClear    DisplayClearSink
	displayZero     DisplayZeroSink
	displayFill     DisplayFillSink
	displayPresent  DisplayPresentSink
	displayCopy     DisplayCopySink
	mappingSelect   MappingSelectSink
	colorSelect     ColorSelectSink
	rectangleDraw   RectangleDrawSink
	rectangleFill   RectangleFillSink
	spriteDraw      SpriteDrawSink
	spriteTransform SpriteTransformSink
	spriteBuffer    SpriteBufferSink
	textDraw        TextDrawSink
	audioReset      AudioResetSink
	media           [][]byte
	mediaLoad       MediaLoadSink
	dataRead        DataReadSink
	textStyle       textStyleState
}

// textStyleState mirrors the four normalized bytes consumed by the exact-build
// text-resource opcodes. It is VM-owned state: 66 replaces all four fields,
// while 67..69 replace the corresponding subsets.
type textStyleState struct {
	mode      uint8
	primary   uint8
	secondary uint8
	variant   uint8
}

// NewWithAddressSpaceAndServices explicitly enables service-aware dispatch.
// Nil config enables no provider and supplies no defaults: valid query operands
// then report a named unavailable cause inside ExecutionError. Old constructors
// remain distinguishable and return UnsupportedOpcodeError for
// 51/55/57/59/5e/62/63/6f/70/76/77/78/90/91/b9/a1 instead.
// Configuration is validated before arena construction, with no clock mutation.
// Current clock conversion range is checked per query, since its owner can advance
// or restore the shared clock after construction. Epoch zero can be selected via
// runtime.Clock.Restore; runtime.NewClock(0,...) instead normalizes to its default.
func NewWithAddressSpaceAndServices(program []byte, entry uint32, space AddressSpace, config *ServiceConfig) (*VM, error) {
	// The exact-build display initializer publishes this text state before guest
	// execution. Opcodes66..69 then replace all or selected fields.
	state := &serviceState{textStyle: textStyleState{mode: 2, primary: 3}}
	if config != nil {
		if p := config.DeviceQuery; p != nil {
			if p.Width < 1 || p.Width > 256 || p.Height < 1 || p.Height > 256 {
				return nil, fmt.Errorf("%w: device dimensions must be 1..256", ErrInvalidServiceConfig)
			}
			copy := *p
			state.deviceQuery = &copy
		}
		if (config.Clock == nil && config.ClockPolicy != 0) || (config.Clock != nil && config.ClockPolicy != FixedOffsetNoDST) {
			return nil, fmt.Errorf("%w: clock requires explicit FixedOffsetNoDST policy", ErrInvalidServiceConfig)
		}
		state.clock, state.clockPolicy = config.Clock, config.ClockPolicy
		if (config.Random == nil) != (config.RandomStream == "") {
			return nil, fmt.Errorf("%w: random requires a provider and stream name together", ErrInvalidServiceConfig)
		}
		if config.Random != nil {
			name := config.RandomStream
			if strings.TrimSpace(name) == "" || len(name) > 64 || strings.IndexByte(name, 0) >= 0 {
				return nil, fmt.Errorf("%w: invalid random stream name", ErrInvalidServiceConfig)
			}
			state.random, state.randomStream = config.Random, strings.Clone(name)
		}
		state.timer = config.Timer
		state.displayClear = config.DisplayClear
		state.displayZero = config.DisplayZero
		state.displayFill = config.DisplayFill
		state.displayPresent = config.DisplayPresent
		state.displayCopy = config.DisplayCopy
		state.mappingSelect = config.MappingSelect
		state.colorSelect = config.ColorSelect
		state.rectangleDraw = config.RectangleDraw
		state.rectangleFill = config.RectangleFill
		state.spriteDraw = config.SpriteDraw
		state.spriteTransform = config.SpriteTransform
		state.spriteBuffer = config.SpriteBuffer
		state.textDraw = config.TextDraw
		state.audioReset = config.AudioReset
		state.mediaLoad = config.MediaLoad
		state.dataRead = config.DataRead
		if len(config.Media) > math.MaxUint16 {
			return nil, fmt.Errorf("%w: too many media records", ErrInvalidServiceConfig)
		}
		state.media = make([][]byte, len(config.Media))
		for i, media := range config.Media {
			if len(media.Data) > math.MaxUint16 {
				return nil, fmt.Errorf("%w: media %d exceeds 16-bit length", ErrInvalidServiceConfig, i)
			}
			state.media[i] = append([]byte(nil), media.Data...)
		}
	}
	v, err := NewWithAddressSpace(program, entry, space)
	if err != nil {
		return nil, err
	}
	v.services = state
	return v, nil
}

// serviceSpan keeps ReadWord's tag/signed-index rules, with full output preflight
// against the selected global arena, not descriptor lengths. It is private so
// callers cannot obtain a mutable view of VM storage.
func (v *VM) serviceSpan(ref uint16, extent uint64) ([]byte, error) {
	if v.address == nil {
		return nil, ErrInvalidAddress
	}
	region, index := v.address.ram, ref
	if index&0x4000 != 0 {
		region = v.address.file
		index &^= 0x4000
	}
	start := uint64(index) * 2
	if int16(index) < 0 || start >= uint64(len(region)) || extent > uint64(len(region))-start {
		return nil, ErrInvalidAddress
	}
	return region[start : start+extent], nil
}

func (v *VM) serviceTail(ref uint16) ([]byte, error) {
	if v.address == nil {
		return nil, ErrInvalidAddress
	}
	region, index := v.address.ram, ref
	if index&0x4000 != 0 {
		region = v.address.file
		index &^= 0x4000
	}
	start := uint64(index) * 2
	if int16(index) < 0 || start >= uint64(len(region)) {
		return nil, ErrInvalidAddress
	}
	return region[start:], nil
}

func (s *serviceState) deviceQueryWords() ([4]uint16, error) {
	if s.deviceQuery == nil {
		return [4]uint16{}, ErrDeviceQueryUnavailable
	}
	p := s.deviceQuery
	k := uint16(8)
	switch {
	case p.Width < 120 || p.Height < 80:
		k = 1
	case p.Width < 128 || p.Height < 128:
		k = 2
	case p.Width < 176 || p.Height < 176:
		k = 4
	}
	mask := uint16(uint32(1) << uint32(p.AudioType&31))
	if p.AudioType == 5 {
		mask = 0x24
	} else if p.AudioType == 6 {
		mask = 0x64
	}
	return [4]uint16{k, 4, mask, 1}, nil
}

func checkedServiceMillis(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

func (s *serviceState) clockWords() ([4]uint16, error) {
	if s.clock == nil || s.clockPolicy != FixedOffsetNoDST {
		return [4]uint16{}, ErrClockUnavailable
	}
	// One authoritative sample, under the caller's serialization discipline.
	// No WallMillis/LocalMillis resampling, host time, time advancement or RNG.
	state := s.clock.Snapshot()
	if state.MonotonicNanos < 0 || state.TimezoneOffsetMins < -1440 || state.TimezoneOffsetMins > 1440 {
		return [4]uint16{}, ErrClockRange
	}
	utc, ok := checkedServiceMillis(state.WallEpochMillis, state.MonotonicNanos/1_000_000)
	const maxMillis = int64(2147483647999)
	if !ok || utc < 0 || utc > maxMillis {
		return [4]uint16{}, ErrClockRange
	}
	local, ok := checkedServiceMillis(utc, int64(state.TimezoneOffsetMins)*60_000)
	// Validate milliseconds before division: -1ms must not truncate into epoch0.
	// This conservative range is portable policy, not full native CRT equivalence.
	if !ok || local < 0 || local > maxMillis {
		return [4]uint16{}, ErrClockRange
	}
	seconds := local / 1000
	return [4]uint16{uint16(seconds / 3600 % 24), uint16(seconds / 60 % 60), uint16(seconds % 60), uint16(utc % 1000)}, nil
}
