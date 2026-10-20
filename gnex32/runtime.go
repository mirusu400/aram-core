package gnex32

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// Identity supplies the two handset strings read during GNEX startup.
type Identity struct {
	MIN    string
	UserID string
}

// DeviceProfile contains handset-owned values which are visible to GNEX
// programs. Keeping them together prevents title compatibility data from being
// scattered through the VM and presentation implementation.
type DeviceProfile struct {
	ID                  string
	ScreenWidth         int
	ScreenHeight        int
	Identity            Identity
	Platform            string
	KeyToneEnabled      bool
	MMIBacklightEnabled bool
}

const maxGNEX32ScreenPixels = 4096 * 4096

// Mobile C reports natural sound completion through EVENT_RESULT with
// swData=23.
const ResultSoundComplete uint32 = 23

const gnex32InputControlPrefix = "gnex32-key/"

func (p DeviceProfile) Validate() error {
	if strings.TrimSpace(p.ID) == "" || len(p.ID) > 255 || strings.IndexByte(p.ID, 0) >= 0 {
		return fmt.Errorf("gnex32: invalid device profile ID")
	}
	if p.ScreenWidth <= 0 || p.ScreenHeight <= 0 ||
		p.ScreenWidth > 4096 || p.ScreenHeight > 4096 ||
		p.ScreenWidth*p.ScreenHeight > maxGNEX32ScreenPixels {
		return fmt.Errorf("gnex32: invalid profile screen %dx%d", p.ScreenWidth, p.ScreenHeight)
	}
	for name, value := range map[string]string{
		"MIN": p.Identity.MIN, "user ID": p.Identity.UserID,
		"platform": p.Platform,
	} {
		if value == "" || len(value) > 255 || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("gnex32: invalid profile %s", name)
		}
	}
	return nil
}

// UserNVWords is the number of 32-bit integers in GNEX user non-volatile
// storage. The Mobile C contract fixes the storage capacity at 64 bytes.
const UserNVWords = 16

// BacklightMode is the SetBackLight handset policy documented by the Mobile C
// SDK. MMI delegates to the handset's normal UI policy; the deterministic
// headless profile resolves that policy to an enabled backlight.
type BacklightMode uint8

const (
	BacklightOff BacklightMode = iota
	BacklightOn
	BacklightMMI
)

// Runtime hosts the observed version-4 services and preserves VM and media
// state across event dispatches.
type Runtime struct {
	vm             *VM
	display        *Display
	profile        DeviceProfile
	services       *shared.Services
	owner          shared.OwnerID
	soundClip      shared.ServiceID
	keyToneClip    shared.ServiceID
	networkSocket  shared.ServiceID
	keyToneOn      bool
	backlight      BacklightMode
	tick           uint64
	timers         [3]runtimeTimer
	presents       uint64
	nv             [UserNVWords]uint32
	randomState    uint32
	paint          color.RGBA
	paintSource    color.RGBA
	paintIndex     uint8
	paintMapped    bool
	fontSize       uint32
	fontColor      color.RGBA
	fontIndex      uint8
	fontAlign      uint32
	fontBack       color.RGBA
	fontBackIdx    uint8
	text           *shared.Text
	closeRequested bool
	closed         bool
}

// CaptureUserNV returns the restart-persistent handset storage without exposing
// the runtime's mutable backing state.
func (r *Runtime) CaptureUserNV() [UserNVWords]uint32 {
	return r.nv
}

// RestoreUserNV replaces restart-persistent handset storage. Volatile VM,
// display, timer and media state are deliberately unaffected.
func (r *Runtime) RestoreUserNV(state [UserNVWords]uint32) {
	r.nv = state
}

// EnableExecutionTrace starts a fresh diagnostic opcode and service trace.
func (r *Runtime) EnableExecutionTrace() {
	if r != nil && r.vm != nil {
		r.vm.EnableExecutionTrace()
	}
}

// DisableExecutionTrace stops and discards diagnostic execution tracing.
func (r *Runtime) DisableExecutionTrace() {
	if r != nil && r.vm != nil {
		r.vm.DisableExecutionTrace()
	}
}

// ResetExecutionTrace clears an enabled diagnostic trace.
func (r *Runtime) ResetExecutionTrace() {
	if r != nil && r.vm != nil {
		r.vm.ResetExecutionTrace()
	}
}

// ExecutionTrace returns an independent snapshot of diagnostic VM coverage.
func (r *Runtime) ExecutionTrace() ExecutionTrace {
	if r == nil || r.vm == nil {
		return ExecutionTrace{}
	}
	return r.vm.ExecutionTrace()
}

// TakeCloseRequest consumes a BackToBrowser request after the current guest
// dispatch returns. The application host uses it to stop the title without
// running another guest callback.
func (r *Runtime) TakeCloseRequest() bool {
	requested := r.closeRequested
	r.closeRequested = false
	return requested
}

type runtimeTimer struct {
	due    uint64 // units of 1/60 millisecond
	period uint32
	repeat bool
	active bool
}

var gnex32FontHeights = [...]int32{6, 8, 12, 24, 16, 32}

func NewRuntime(image gnex.GNEX32Image, profile DeviceProfile) (*Runtime, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	config := shared.DefaultConfig()
	config.FrameDuration = time.Second / 60
	services, err := shared.NewServices(config)
	if err != nil {
		return nil, err
	}
	owner, err := services.Coordinator.Register("gnex32", 0)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{display: NewDisplay(profile.ScreenWidth, profile.ScreenHeight), profile: profile,
		services: services, owner: owner, text: services.Text,
		keyToneOn: profile.KeyToneEnabled, backlight: BacklightMMI,
		fontColor: monoColor(3), fontIndex: 3, fontBack: monoColor(4), fontBackIdx: 4}
	if err := services.Device.SetBacklight(profile.MMIBacklightEnabled, 0, services.Clock.Monotonic()); err != nil {
		return nil, err
	}
	vm, err := NewWithServices(image, runtime.service)
	if err != nil {
		return nil, err
	}
	runtime.vm = vm
	// GNEX reserves symbols 4 and 5 for swWidth and swHeight. The handset
	// supplies these values before main; zero leaves guest layout arithmetic
	// outside the visible LCD even when the host framebuffer is sized correctly.
	if err := vm.memory.WriteWord(4, 0, uint32(profile.ScreenWidth)); err != nil {
		return nil, err
	}
	if err := vm.memory.WriteWord(5, 0, uint32(profile.ScreenHeight)); err != nil {
		return nil, err
	}
	vm.copyImage = runtime.copyImage
	vm.copyImageDir = runtime.copyImageDir
	vm.copyImageEx = runtime.copyImageEx
	return runtime, nil
}

func (r *Runtime) RunMain(budget uint64) error {
	if err := r.vm.Run(budget); err != nil {
		return err
	}
	if !r.vm.Halted() {
		return ErrBudget
	}
	return nil
}

// EventAvailable reports whether the image implements a named event handler.
func (r *Runtime) EventAvailable(event gnex.GNEX32Event) bool {
	if r == nil || r.vm == nil || event == gnex.GNEX32EventExtension {
		return false
	}
	pointer, valid := r.vm.image.Callbacks.Pointer(event)
	return valid && pointer != 0
}

// DispatchEvent invokes a named Mobile C event with swData..swData3. Start is
// entered through RunMain; the unnamed extension slot is deliberately not a
// public event source.
func (r *Runtime) DispatchEvent(event gnex.GNEX32Event, data EventData, budget uint64) error {
	if r == nil || r.closed {
		return fmt.Errorf("gnex32: cannot dispatch event on a closed runtime")
	}
	if event == gnex.GNEX32EventStart || event == gnex.GNEX32EventExtension ||
		event >= gnex.GNEX32EventCount {
		return ErrEventUnavailable
	}
	if err := r.vm.BeginEventData(event, data); err != nil {
		return err
	}
	return r.vm.Run(budget)
}

// End dispatches the optional EVENT_END handler. The application lifecycle
// decides when a running title ends, so an absent handler is a successful no-op.
func (r *Runtime) End(budget uint64) error {
	if !r.EventAvailable(gnex.GNEX32EventEnd) {
		return nil
	}
	return r.DispatchEvent(gnex.GNEX32EventEnd, EventData{}, budget)
}

// StepFrame advances the host's 60 Hz clock and delivers one due GNEX timer.
func (r *Runtime) StepFrame(budget uint64) error {
	r.tick++
	for slot := range r.timers {
		timer := &r.timers[slot]
		if !timer.active || r.tick*1000 < timer.due {
			continue
		}
		if timer.repeat {
			for timer.due <= r.tick*1000 {
				timer.due += uint64(timer.period) * 60
			}
		} else {
			timer.active = false
		}
		if err := r.DispatchEvent(gnex.GNEX32EventTimeout, EventData{Data: uint32(slot)}, budget); err != nil {
			return err
		}
		if r.closeRequested {
			return nil
		}
	}
	if err := r.services.AdvanceFrame(r.owner); err != nil {
		return err
	}
	return r.dispatchReadyEvents(budget)
}

func (r *Runtime) KeyPressed(code uint16, budget uint64) error {
	return r.dispatchKey(code, true, budget)
}

// QueueKey schedules a physical key transition on the shared guest clock.
// Held keys use the shared 500 ms/50 ms repeat policy; releases are delivered
// as the Mobile C SWAP_KEY_RELEASE value rather than repeating the press code.
func (r *Runtime) QueueKey(code uint16, pressed bool, at time.Duration, budget uint64) error {
	if r == nil || r.closed {
		return fmt.Errorf("gnex32: cannot queue input on a closed runtime")
	}
	if !ValidPhysicalKey(code) {
		return fmt.Errorf("gnex32: invalid physical key %#x", code)
	}
	control := gnex32InputControlPrefix + fmt.Sprintf("%04x", code)
	if err := r.services.QueueInput(r.owner, control, pressed, at); err != nil {
		return err
	}
	return r.dispatchReadyEvents(budget)
}

func (r *Runtime) dispatchKey(code uint16, keyTone bool, budget uint64) error {
	if keyTone && code != KeyRelease && r.keyToneOn {
		if err := r.playKeyTone(code); err != nil {
			return err
		}
	}
	return r.DispatchEvent(gnex.GNEX32EventKeyPress, EventData{Data: uint32(code)}, budget)
}

func (r *Runtime) dispatchReadyEvents(budget uint64) error {
	for {
		event, ok := r.services.Events.PopReady(r.services.Clock.Monotonic())
		if !ok {
			return nil
		}
		if event.Owner != r.owner {
			return fmt.Errorf("gnex32: shared event %q belongs to owner %d", event.Kind, event.Owner)
		}
		switch event.Kind {
		case shared.EventInputPress, shared.EventInputRelease, shared.EventInputRepeat:
			code, err := parseGNEX32InputControl(event.Control)
			if err != nil {
				return err
			}
			if event.Kind == shared.EventInputRelease {
				code = KeyRelease
			}
			if err := r.dispatchKey(code, event.Kind == shared.EventInputPress, budget); err != nil {
				return err
			}
			if r.closeRequested {
				return nil
			}
		case shared.EventAudioComplete:
			switch event.ServiceID {
			case r.keyToneClip:
				if err := r.services.Media.DestroyClip(r.owner, r.keyToneClip, r.services.Events); err != nil {
					return err
				}
				r.keyToneClip = 0
			case r.soundClip:
				if err := r.services.Media.DestroyClip(r.owner, r.soundClip, r.services.Events); err != nil {
					return err
				}
				r.soundClip = 0
				if r.EventAvailable(gnex.GNEX32EventResult) {
					if err := r.DispatchEvent(
						gnex.GNEX32EventResult,
						EventData{Data: ResultSoundComplete},
						budget,
					); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("gnex32: audio completion for unknown clip %d", event.ServiceID)
			}
		case shared.EventNetworkReady:
			var result uint32
			switch event.Name {
			case "connect":
				if event.ServiceID != r.networkSocket {
					return fmt.Errorf("gnex32: network completion for unknown socket %d", event.ServiceID)
				}
				if event.Value != 0 {
					result = 1
				} else if err := r.StopNetwork(); err != nil {
					return err
				}
			case "gnex.disconnect":
				result = uint32(event.Value)
			case "read":
				if !r.EventAvailable(gnex.GNEX32EventReceive) {
					continue
				}
				return fmt.Errorf("gnex32: network receive buffer contract is unavailable")
			default:
				return fmt.Errorf("gnex32: unsupported network event %q", event.Name)
			}
			if r.EventAvailable(gnex.GNEX32EventResult) {
				if err := r.DispatchEvent(
					gnex.GNEX32EventResult, EventData{Data: result}, budget,
				); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("gnex32: unsupported shared event %q", event.Kind)
		}
	}
}

func parseGNEX32InputControl(control string) (uint16, error) {
	if len(control) != len(gnex32InputControlPrefix)+4 ||
		!strings.HasPrefix(control, gnex32InputControlPrefix) {
		return 0, fmt.Errorf("gnex32: invalid shared input control %q", control)
	}
	value, err := strconv.ParseUint(control[len(gnex32InputControlPrefix):], 16, 16)
	if err != nil {
		return 0, fmt.Errorf("gnex32: invalid shared input control %q", control)
	}
	return uint16(value), nil
}

func (r *Runtime) Frame() image.Image   { return r.display.Frame() }
func (r *Runtime) PresentCount() uint64 { return r.presents }
func (r *Runtime) PC() int              { return r.vm.PC() }

// DrainTimedAudio transfers the oldest continuous output span. GNEX media and
// display time share the Services clock advanced by StepFrame.
func (r *Runtime) DrainTimedAudio() (shared.AudioBuffer, time.Duration, uint64) {
	audio, start := r.services.Media.DrainTimed()
	if len(audio.PCM16) == 0 {
		start = r.services.Clock.Monotonic()
	}
	return audio, start, r.services.Media.OutputRevision()
}

func (r *Runtime) AudioOutputRevision() uint64 {
	return r.services.Media.OutputRevision()
}

func (r *Runtime) AudioTime() time.Duration {
	return r.services.Clock.Monotonic()
}

// Vibration reports the shared handset motor level and guest-clock deadline.
func (r *Runtime) Vibration() (uint8, time.Duration) {
	return r.services.Device.Vibration()
}

// StopVibration clears transient haptic output on a machine lifecycle boundary.
func (r *Runtime) StopVibration() error {
	return r.services.Device.Vibrate(0, 0, r.services.Clock.Monotonic())
}

// StopNetwork closes the single connection owned by the Mobile C network
// library. GNEX NetConnect exposes one process-wide connection rather than
// socket handles.
func (r *Runtime) StopNetwork() error {
	if r.networkSocket == 0 {
		return nil
	}
	socket := r.networkSocket
	if err := r.services.Network.CloseSocket(r.owner, socket, r.services.Events); err != nil {
		return err
	}
	r.networkSocket = 0
	return nil
}

// ExternalRequests returns a copy of the handset action outbox.
func (r *Runtime) ExternalRequests() []shared.ExternalRequest {
	return r.services.Device.Requests()
}

// ClearExternalRequests acknowledges handset actions through sequence.
func (r *Runtime) ClearExternalRequests(through uint64) {
	r.services.Device.ClearRequests(through)
}

func (r *Runtime) BacklightMode() BacklightMode { return r.backlight }

func (r *Runtime) BacklightEnabled() bool {
	enabled, _ := r.services.Device.Backlight()
	return enabled
}

// ResetAudioOutput stops the active GNEX sound and discards PCM that a host has
// not drained yet. The application publishes a new audio generation at the
// returned guest time so frontends also discard PCM they already buffered.
func (r *Runtime) ResetAudioOutput() (time.Duration, uint64, error) {
	if err := r.stopSound(); err != nil {
		return 0, r.services.Media.OutputRevision(), err
	}
	if err := r.stopKeyTone(); err != nil {
		return 0, r.services.Media.OutputRevision(), err
	}
	_ = r.services.Media.Drain()
	return r.services.Clock.Monotonic(), r.services.Media.OutputRevision(), nil
}

// Close releases every shared-service object owned by this runtime. Display
// pixels remain readable for post-close diagnostics.
func (r *Runtime) Close() error {
	if r.closed {
		return nil
	}
	if _, _, err := r.ResetAudioOutput(); err != nil {
		return err
	}
	if err := r.StopVibration(); err != nil {
		return err
	}
	if err := r.StopNetwork(); err != nil {
		return err
	}
	for _, font := range r.text.Snapshot().Fonts {
		if font.Owner == r.owner {
			if err := r.text.DestroyFont(r.owner, font.ID); err != nil {
				return err
			}
		}
	}
	r.services.Events.DropOwner(r.owner)
	if err := r.services.Coordinator.Transition(
		r.owner,
		shared.LifecycleDestroyed,
		r.services.Clock.Monotonic(),
		nil,
	); err != nil {
		return err
	}
	if err := r.services.Coordinator.Unregister(r.owner); err != nil {
		return err
	}
	r.closed = true
	return nil
}

func (r *Runtime) advanceSystemFrame() error {
	r.presents++
	for symbol, value := range []uint32{uint32(r.presents), uint32(r.presents % 2), uint32(r.presents % 3), uint32(r.presents % 6)} {
		if err := r.vm.memory.WriteWord(6+symbol, 0, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) copyImage(x, y int32, index int) error {
	resource, palette, err := r.vm.media.ReadImage(index)
	if err != nil {
		return err
	}
	return r.display.copyImageTransformed(x, y, resource, palette, 0, 0, 0, 0)
}

func (r *Runtime) copyImageDir(x, y int32, index int, direction uint32) error {
	resource, palette, err := r.vm.media.ReadImage(index)
	if err != nil {
		return err
	}
	return r.display.copyImageTransformed(x, y, resource, palette, 0, 0, direction, 0)
}

func (r *Runtime) copyImageEx(x, y int32, index int, alpha, zoom, mirror, rotation uint32) error {
	resource, palette, err := r.vm.media.ReadImage(index)
	if err != nil {
		return err
	}
	return r.display.copyImageTransformed(x, y, resource, palette, alpha, zoom, mirror, rotation)
}

func (r *Runtime) refreshGammaColors() error {
	if r.paintMapped {
		mapped, err := r.display.paletteColor(uint32(r.paintIndex))
		if err != nil {
			return err
		}
		r.paint = mapped
	} else {
		r.paint = r.display.gammaColor(r.paintSource)
	}
	fontColor, err := r.display.paletteColor(uint32(r.fontIndex))
	if err != nil {
		return err
	}
	fontBack, err := r.display.paletteColor(uint32(r.fontBackIdx))
	if err != nil {
		return err
	}
	r.fontColor, r.fontBack = fontColor, fontBack
	return nil
}

func terminated(value string) []byte { return append([]byte(value), 0) }

func (r *Runtime) drawStr(x, y int32, mediaIndex int, solid bool) error {
	data, _, err := r.vm.media.Read(mediaIndex)
	if err != nil {
		return err
	}
	if end := bytes.IndexByte(data, 0); end >= 0 {
		data = data[:end]
	}
	if len(data) == 0 {
		return nil
	}
	decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), data)
	if err != nil {
		return err
	}
	if r.fontSize >= uint32(len(gnex32FontHeights)) {
		return fmt.Errorf("gnex32: unsupported font size %d", r.fontSize)
	}
	fontID, err := r.text.EnsureFont(r.owner, shared.FontDescriptor{Family: "aram-fallback", Size: gnex32FontHeights[r.fontSize]})
	if err != nil {
		return err
	}
	glyphs := make([]shared.Glyph, 0, len(decoded))
	width := int32(0)
	for _, character := range string(decoded) {
		glyph, err := r.text.Glyph(r.owner, fontID, character)
		if err != nil {
			return err
		}
		glyphs = append(glyphs, glyph)
		width += glyph.Advance
	}
	if r.fontAlign == 1 {
		x -= width / 2
	} else if r.fontAlign == 2 {
		x -= width
	}
	if solid && r.fontBack.A != 0 && width > 0 {
		r.display.FillRect(x, y, x+width-1, y+gnex32FontHeights[r.fontSize]-1, r.fontBack)
	}
	cursor := x
	for _, glyph := range glyphs {
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if alpha == 0 {
					continue
				}
				px, py := int(cursor+glyph.BearingX+column), int(y+glyph.BearingY+row)
				if image.Pt(px, py).In(r.display.clip) {
					r.display.working.SetRGBA(px, py, r.fontColor)
				}
			}
		}
		cursor += glyph.Advance
	}
	return nil
}

func (r *Runtime) service(id uint16, stack []uint32) (ServiceResult, error) {
	result, err := r.executeService(id, stack)
	if err != nil {
		return ServiceResult{}, err
	}
	contract, ok := ServiceContractFor(id)
	if !ok {
		return ServiceResult{}, &UnsupportedServiceError{ID: id}
	}
	if result.Pop != contract.Pop || len(result.Push) != contract.Push {
		return ServiceResult{}, fmt.Errorf(
			"%w: service %#x %s returned pop=%d push=%d, want pop=%d push=%d",
			ErrServiceStackEffect, id, contract.Name, result.Pop, len(result.Push), contract.Pop, contract.Push,
		)
	}
	return result, nil
}

func (r *Runtime) executeService(id uint16, stack []uint32) (ServiceResult, error) {
	last := len(stack) - 1
	switch id {
	case 0x07: // BackToBrowser(url)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		target, err := r.readString(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		// GNEX/GVM2 titles may pass at most 255 URL bytes.
		if len(target) == 0 || len(target) > 255 {
			return ServiceResult{}, fmt.Errorf("gnex32: BackToBrowser URL length %d is outside 1..255", len(target))
		}
		if _, err := r.services.Device.Request(
			r.owner, shared.RequestBrowser, string(target), nil,
			r.services.Clock.Monotonic(),
		); err != nil {
			return ServiceResult{}, err
		}
		r.closeRequested = true
		return ServiceResult{Pop: 1, Halt: true}, nil
	case 0x101: // NetConnect(IP, Port)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		host, err := r.readString(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		port := int32(stack[last])
		if len(host) == 0 || port <= 0 || port > 65535 {
			return ServiceResult{}, fmt.Errorf("gnex32: invalid NetConnect endpoint %q:%d", host, port)
		}
		if err := r.StopNetwork(); err != nil {
			return ServiceResult{}, err
		}
		socket, err := r.services.Network.OpenSocket(r.owner, 2, 1)
		if err != nil {
			return ServiceResult{}, err
		}
		cleanup := func() { _ = r.services.Network.CloseSocket(r.owner, socket, r.services.Events) }
		if err := r.services.Network.ConnectSocket(r.owner, socket, string(host), uint16(port)); err != nil {
			cleanup()
			return ServiceResult{}, err
		}
		r.networkSocket = socket
		if err := r.services.CompleteSocketResponse(
			r.owner, socket, r.services.Device.NetworkAvailable(), r.services.Clock.Monotonic(),
		); err != nil {
			r.networkSocket = 0
			cleanup()
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0x102: // NetDisconnect()
		result := int64(10)
		connected := r.networkSocket != 0
		if connected {
			result = 9
		}
		if _, err := r.services.Events.Enqueue(shared.Event{
			At: r.services.Clock.Monotonic(), Kind: shared.EventNetworkReady,
			Owner: r.owner, Name: "gnex.disconnect", Value: result,
		}); err != nil {
			return ServiceResult{}, err
		}
		if connected {
			if err := r.StopNetwork(); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{}, nil
	case 0x104: // NetSend(int[], size)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok || ref.element != 0 {
			return ServiceResult{}, ErrInvalidLValue
		}
		size := int64(int32(stack[last]))
		if size < 0 || size > 1024 {
			return ServiceResult{}, fmt.Errorf("gnex32: NetSend size %d is outside 0..1024", size)
		}
		data, err := r.vm.memory.ReadBytes(ref.symbol, 0, int(size))
		if err != nil {
			return ServiceResult{}, err
		}
		if _, err := r.vm.memory.ReadWord(0, 0); err != nil {
			return ServiceResult{}, err
		}
		status := uint32(0)
		if r.networkSocket != 0 {
			info, infoErr := r.services.Network.SocketInfo(r.owner, r.networkSocket)
			if infoErr != nil {
				return ServiceResult{}, infoErr
			}
			if info.State == shared.ConnectionConnected {
				written, writeErr := r.services.WriteSocketRequest(r.owner, r.networkSocket, data)
				if writeErr != nil {
					return ServiceResult{}, writeErr
				}
				if written == len(data) {
					status = 1
				}
			}
		}
		if err := r.vm.memory.WriteWord(0, 0, status); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 1: // GetSysMin(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.vm.media.Replace(int(stack[last]), terminated(r.profile.Identity.MIN)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1}, nil
	case 2: // GetSysUserID(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.vm.media.Replace(int(stack[last]), terminated(r.profile.Identity.UserID)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1, Push: []uint32{0}}, nil
	case 0x1d8: // GetSysInfo(keyword, info)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		keyword, err := r.readString(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		var value string
		switch string(keyword) {
		case "PLATFORM":
			value = r.profile.Platform
		case "VODEX":
			value = "Not Support"
		default:
			return ServiceResult{Pop: 2, Push: []uint32{0}}, nil
		}
		if err := r.vm.media.Replace(int(stack[last]), terminated(value)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2, Push: []uint32{1}}, nil
	case 4, 5: // GetDate/GetTime(int[4])
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last]
		if !ok || ref.element != 0 {
			return ServiceResult{}, ErrInvalidLValue
		}
		// LocalMillis already applies the fixed handset timezone. Interpret it
		// in UTC to obtain those local civil fields without consulting the host
		// clock or its timezone database.
		now := time.UnixMilli(r.services.Clock.LocalMillis()).UTC()
		values := []uint32{uint32(now.Year()), uint32(now.Month()), uint32(now.Day()), uint32(now.Weekday())}
		if id == 5 {
			values = []uint32{uint32(now.Hour()), uint32(now.Minute()), uint32(now.Second()), uint32(now.Nanosecond() / 1_000_000)}
		}
		for i, value := range values {
			if err := r.vm.memory.WriteWord(ref.symbol, i, value); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 1}, nil
	case 0x9d: // GetMediaSize(media)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(len(data))}}, nil
	case 0x9e: // SetMediaSize(media, size)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		size := int64(int32(stack[last]))
		status := uint32(0)
		if size >= 0 {
			if size&1 != 0 {
				size++
			}
			if int64(int(size)) == size && r.vm.media.Resize(int(stack[last-1]), int(size)) == nil {
				status = 1
			}
		}
		return ServiceResult{Pop: 2, Push: []uint32{status}}, nil
	case 0x9f: // StrInit(string, size)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if stack[last] > 1<<20 {
			return ServiceResult{}, fmt.Errorf("gnex32: StrInit size exceeds limit")
		}
		if err := r.vm.media.Replace(int(stack[last-1]), make([]byte, int(stack[last])+1)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa0: // StrLen(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		length := bytes.IndexByte(data, 0)
		if length < 0 {
			length = len(data)
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(length)}}, nil
	case 0xa1: // StrCpy(destination, source)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		if end := bytes.IndexByte(data, 0); end >= 0 {
			data = data[:end+1]
		} else {
			data = append(data, 0)
		}
		if err := r.vm.media.Replace(int(stack[last-1]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa2: // StrSub(destination, source, start, length)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last-2]))
		if err != nil {
			return ServiceResult{}, err
		}
		if end := bytes.IndexByte(data, 0); end >= 0 {
			data = data[:end]
		}
		start, length := int32(stack[last-1]), int32(stack[last])
		if start < 0 || length < 0 {
			return ServiceResult{}, fmt.Errorf("gnex32: negative StrSub range %d:%d", start, length)
		}
		begin := min(int(start), len(data))
		end := begin + min(int(length), len(data)-begin)
		if err := r.vm.media.Replace(int(stack[last-3]), terminated(string(data[begin:end]))); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 4}, nil
	case 0xa3: // StrCat(destination, source)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		destination, err := r.readString(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		source, err := r.readString(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		combined := make([]byte, 0, len(destination)+len(source)+1)
		combined = append(combined, destination...)
		combined = append(combined, source...)
		combined = append(combined, 0)
		if err := r.vm.media.Replace(int(stack[last-1]), combined); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa4: // StrCmp(string, string)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		left, _, err := r.vm.media.Read(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		right, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		if end := bytes.IndexByte(left, 0); end >= 0 {
			left = left[:end]
		}
		if end := bytes.IndexByte(right, 0); end >= 0 {
			right = right[:end]
		}
		return ServiceResult{Pop: 2, Push: []uint32{uint32(int32(bytes.Compare(left, right)))}}, nil
	case 0xa5: // GetChar(media, index)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		// The virus menu reads its zero-initialized sentinel at an index
		// below the start of a lookup table. Native GNEX leaves this read
		// unspecified; return zero while keeping the media store bounds-safe.
		if int32(stack[last]) < 0 {
			return ServiceResult{Pop: 2, Push: []uint32{0}}, nil
		}
		if resource, flags, err := r.vm.media.Read(int(stack[last-1])); err == nil &&
			flags&1 != 0 && int(stack[last]) == len(resource) {
			return ServiceResult{Pop: 2, Push: []uint32{0}}, nil
		}
		value, err := r.vm.media.MediaByte(int(stack[last-1]), int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2, Push: []uint32{uint32(value)}}, nil
	case 0xa6: // PutChar(media, index, value)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if int32(stack[last-1]) < 0 {
			return ServiceResult{Pop: 3}, nil
		}
		if err := r.vm.media.SetMediaByte(int(stack[last-2]), int(stack[last-1]), byte(stack[last])); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0xa7: // AsciiToInt(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, err := r.readString(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(parseGNEX32Decimal(data))}}, nil
	case 0xa8: // IntToAscii(destination, value)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data := terminated(strconv.FormatInt(int64(int32(stack[last])), 10))
		if err := r.vm.media.Replace(int(stack[last-1]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa9: // PutByte(int[], index, value)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-2]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		offset, err := gnex32ByteOffset(ref, stack[last-1])
		if err != nil {
			return ServiceResult{}, err
		}
		if err := r.vm.memory.WriteBytes(ref.symbol, offset, []byte{byte(stack[last])}); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0xaa: // GetByte(int[], index)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		offset, err := gnex32ByteOffset(ref, stack[last])
		if err != nil {
			return ServiceResult{}, err
		}
		data, err := r.vm.memory.ReadBytes(ref.symbol, offset, 1)
		if err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2, Push: []uint32{uint32(data[0])}}, nil
	case 0xab: // PutBytes(int[], index, media, size)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-3]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		offset, err := gnex32ByteOffset(ref, stack[last-2])
		if err != nil {
			return ServiceResult{}, err
		}
		source, _, err := r.vm.media.Read(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		size := int64(int32(stack[last]))
		if size < 0 || size > int64(len(source)) || int64(int(size)) != size {
			return ServiceResult{}, fmt.Errorf("gnex32: PutBytes size %d is out of range", size)
		}
		if err := r.vm.memory.WriteBytes(ref.symbol, offset, source[:int(size)]); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 4}, nil
	case 0xac: // GetBytes(int[], index, destination, size)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-3]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		offset, err := gnex32ByteOffset(ref, stack[last-2])
		if err != nil {
			return ServiceResult{}, err
		}
		size := int64(int32(stack[last]))
		if size < 0 || int64(int(size)) != size {
			return ServiceResult{}, fmt.Errorf("gnex32: GetBytes size %d is out of range", size)
		}
		data, err := r.vm.memory.ReadBytes(ref.symbol, offset, int(size))
		if err != nil {
			return ServiceResult{}, err
		}
		if err := r.vm.media.Replace(int(stack[last-1]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 4}, nil
	case 0xad: // MakeStrStr(destination, format, source)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		format, err := r.readString(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		value, err := r.readString(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		data, err := formatGNEX32String(format, value)
		if err != nil {
			return ServiceResult{}, err
		}
		if err := r.vm.media.Replace(int(stack[last-2]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0xae, 0xaf, 0xb0, 0xb1, 0xb2: // MakeStr1..MakeStr5(destination, format, values...)
		count := int(id-0xae) + 1
		if len(stack) < count+2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		first := len(stack) - count - 2
		format, err := r.readString(int(stack[first+1]))
		if err != nil {
			return ServiceResult{}, err
		}
		data, err := formatGNEX32Integers(format, stack[first+2:])
		if err != nil {
			return ServiceResult{}, err
		}
		if err := r.vm.media.Replace(int(stack[first]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: count + 2}, nil
	case 0xdd: // ArrayToVar(int[], value, count, operation)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-3]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		value, count, operation := stack[last-2], stack[last-1], stack[last]
		if count > 4096 || operation > 11 {
			return ServiceResult{}, fmt.Errorf("gnex32: unsupported ArrayToVar count or operation")
		}
		if (operation == 4 || operation == 5) && value == 0 {
			return ServiceResult{}, ErrDivisionByZero
		}
		oldValues := make([]uint32, count)
		for i := uint32(0); i < count; i++ {
			old, err := r.vm.memory.ReadWord(ref.symbol, ref.element+int(i))
			if err != nil {
				return ServiceResult{}, err
			}
			oldValues[i] = old
		}
		nextValues := make([]uint32, count)
		for i, old := range oldValues {
			next := value
			switch operation {
			case 1:
				next = old + value
			case 2:
				next = old - value
			case 3:
				next = old * value
			case 4:
				next = uint32(int32(old) / int32(value))
			case 5:
				next = uint32(int32(old) % int32(value))
			case 6:
				next = old & value
			case 7:
				next = old | value
			case 8:
				next = 0
				if value == 0 {
					next = 1
				}
			case 9:
				next = old ^ value
			case 10:
				next = uint32(int32(old) >> value)
			case 11:
				next = old << value
			}
			nextValues[i] = next
		}
		for i, next := range nextValues {
			if err := r.vm.memory.WriteWord(ref.symbol, ref.element+int(i), next); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 4}, nil
	case 0x93: // GetUserNV(int[], count)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		if stack[last] > uint32(len(r.nv)) {
			return ServiceResult{}, fmt.Errorf("gnex32: user NV read exceeds 16 words")
		}
		if stack[last] != 0 {
			if _, err := r.vm.memory.ReadWord(ref.symbol, ref.element+int(stack[last])-1); err != nil {
				return ServiceResult{}, err
			}
		}
		for i := uint32(0); i < stack[last]; i++ {
			if err := r.vm.memory.WriteWord(ref.symbol, ref.element+int(i), r.nv[i]); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 2}, nil
	case 0x94: // PutUserNV(int[], count)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		if stack[last] > uint32(len(r.nv)) {
			return ServiceResult{}, fmt.Errorf("gnex32: user NV write exceeds 16 words")
		}
		next := r.nv
		for i := uint32(0); i < stack[last]; i++ {
			value, err := r.vm.memory.ReadWord(ref.symbol, ref.element+int(i))
			if err != nil {
				return ServiceResult{}, err
			}
			next[i] = value
		}
		r.nv = next
		return ServiceResult{Pop: 2}, nil
	case 0x3a: // ClearWhite
		fill, _ := r.display.paletteColor(0)
		r.display.Clear(fill)
		return ServiceResult{}, nil
	case 0x3b: // ClearBlack
		fill, _ := r.display.paletteColor(3)
		r.display.Clear(fill)
		return ServiceResult{}, nil
	case 0x38: // ClearRGB(red, green, blue)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.Clear(r.display.gammaColor(color.RGBA{R: byte(stack[last-2]), G: byte(stack[last-1]), B: byte(stack[last]), A: 255}))
		return ServiceResult{Pop: 3}, nil
	case 0x26: // SetColor(palette index)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		mapped, err := r.display.paletteColor(stack[last])
		if err != nil {
			return ServiceResult{}, err
		}
		r.paint, r.paintIndex, r.paintMapped = mapped, uint8(stack[last]), true
		return ServiceResult{Pop: 1}, nil
	case 0x27: // SetColorRGB(red, green, blue)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.paintSource = color.RGBA{R: byte(stack[last-2]), G: byte(stack[last-1]), B: byte(stack[last]), A: 255}
		r.paint, r.paintMapped = r.display.gammaColor(r.paintSource), false
		return ServiceResult{Pop: 3}, nil
	case 0x28: // SetFontType(size, foreground, background, alignment)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if stack[last-3] >= uint32(len(gnex32FontHeights)) {
			return ServiceResult{}, fmt.Errorf("gnex32: unsupported font size %d", stack[last-3])
		}
		fontColor, err := r.display.paletteColor(stack[last-2])
		if err != nil {
			return ServiceResult{}, err
		}
		fontBack, err := r.display.paletteColor(stack[last-1])
		if err != nil {
			return ServiceResult{}, err
		}
		r.fontSize = stack[last-3]
		r.fontColor, r.fontIndex = fontColor, uint8(stack[last-2])
		r.fontBack, r.fontBackIdx = fontBack, uint8(stack[last-1])
		r.fontAlign = stack[last]
		return ServiceResult{Pop: 4}, nil
	case 0x2f: // SetPaletteColorRGB(image, index, red, green, blue)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		status := uint32(0)
		if !r.vm.media.SetPaletteColorRGB(int(stack[last-4]), int(int32(stack[last-3])),
			byte(stack[last-2]), byte(stack[last-1]), byte(stack[last])) {
			status = ^uint32(0)
		}
		return ServiceResult{Pop: 5, Push: []uint32{status}}, nil
	case 0x46: // FillRect(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.FillRect(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]), r.paint)
		return ServiceResult{Pop: 4}, nil
	case 0x42: // DrawLine(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.DrawLine(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]), r.paint)
		return ServiceResult{Pop: 4}, nil
	case 0x43: // DrawHLine(x1, x2, y)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		y := int32(stack[last])
		r.display.DrawLine(int32(stack[last-2]), y, int32(stack[last-1]), y, r.paint)
		return ServiceResult{Pop: 3}, nil
	case 0x44: // DrawVLine(x, y1, y2)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		x := int32(stack[last-2])
		r.display.DrawLine(x, int32(stack[last-1]), x, int32(stack[last]), r.paint)
		return ServiceResult{Pop: 3}, nil
	case 0x45: // DrawRect(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.DrawRect(
			int32(stack[last-3]), int32(stack[last-2]),
			int32(stack[last-1]), int32(stack[last]), r.paint,
		)
		return ServiceResult{Pop: 4}, nil
	case 0x47: // FillRectEx(x1, y1, x2, y2, alpha)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.FillRectAlpha(int32(stack[last-4]), int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), r.paint, stack[last])
		return ServiceResult{Pop: 5}, nil
	case 0x48, 0x49: // DrawRectRound / FillRectRound(x1, y1, x2, y2, radius)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		x1, y1, x2, y2 := int32(stack[last-4]), int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1])
		if id == 0x48 {
			r.display.DrawRectRound(x1, y1, x2, y2, int32(stack[last]), r.paint)
		} else {
			r.display.FillRectRound(x1, y1, x2, y2, int32(stack[last]), r.paint)
		}
		return ServiceResult{Pop: 5}, nil
	case 0x51: // SaveLCD()
		r.display.SaveLCD()
		return ServiceResult{}, nil
	case 0x52: // RestoreLCD()
		r.display.RestoreLCD()
		return ServiceResult{}, nil
	case 0x36: // Clear(palette index)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		fill, err := r.display.paletteColor(stack[last])
		if err != nil {
			return ServiceResult{}, err
		}
		r.display.Clear(fill)
		return ServiceResult{Pop: 1}, nil
	case 0x22: // SetClip(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.SetClip(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]))
		return ServiceResult{Pop: 4}, nil
	case 0x23: // ResetClip()
		r.display.ResetClip()
		return ServiceResult{}, nil
	case 0x55, 0x56: // DrawStr/DrawStrSolid(x, y, string)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.drawStr(int32(stack[last-2]), int32(stack[last-1]), int(stack[last]), id == 0x56); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0x5f: // Flush
		r.display.Flush()
		return ServiceResult{}, r.advanceSystemFrame()
	case 0x60: // FlushPartial(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.FlushPartial(
			int32(stack[last-3]), int32(stack[last-2]),
			int32(stack[last-1]), int32(stack[last]),
		)
		return ServiceResult{Pop: 4}, r.advanceSystemFrame()
	case 0xc8: // RandSeed(seed)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.randomState = stack[last]
		return ServiceResult{Pop: 1}, nil
	case 0xc9: // Rand(min, max), max excluded
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		minimum, maximum := int32(stack[last-1]), int32(stack[last])
		if maximum == minimum {
			return ServiceResult{Pop: 2, Push: []uint32{uint32(minimum)}}, nil
		}
		if maximum < minimum {
			minimum, maximum = maximum, minimum
		}
		// GNEX preserves the GVM Mobile C random service. The reference GVM2X
		// runtime forwards RandSeed/Rand to the Microsoft-compatible C rand
		// sequence and maps its 15-bit result into the requested signed range.
		r.randomState = r.randomState*214013 + 2531011
		draw := (r.randomState >> 16) & 0x7fff
		value := minimum + int32(draw%uint32(int64(maximum)-int64(minimum)))
		return ServiceResult{Pop: 2, Push: []uint32{uint32(value)}}, nil
	case 0xcb: // Abs(value)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := int32(stack[last])
		if value < 0 {
			value = -value
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(value)}}, nil
	case 0xcd, 0xce: // Sin100/Cos100(degrees)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		// Mobile C defines these functions as the nearest integer to the
		// trigonometric result scaled by 100. Reduce the angle first so the
		// result stays stable for the entire signed 32-bit input domain.
		degrees := int32(stack[last]) % 360
		radians := float64(degrees) * math.Pi / 180
		value := math.Sin(radians)
		if id == 0xce {
			value = math.Cos(radians)
		}
		return ServiceResult{
			Pop:  1,
			Push: []uint32{uint32(int32(math.Round(value * 100)))},
		}, nil
	case 0xd6: // Max3(int, int, int)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := int32(stack[last-2])
		if candidate := int32(stack[last-1]); candidate > value {
			value = candidate
		}
		if candidate := int32(stack[last]); candidate > value {
			value = candidate
		}
		return ServiceResult{Pop: 3, Push: []uint32{uint32(value)}}, nil
	case 0xe1: // Sqrt(value)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := int32(stack[last])
		if value < 0 || value > 65535 {
			return ServiceResult{}, fmt.Errorf("gnex32: Sqrt value %d is outside 0..65535", value)
		}
		return ServiceResult{
			Pop: 1, Push: []uint32{uint32(math.Sqrt(float64(value)))},
		}, nil
	case 0x25: // SetGamma(gamma)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		previous := r.display.Gamma()
		if err := r.display.SetGamma(stack[last]); err != nil {
			return ServiceResult{}, err
		}
		if err := r.refreshGammaColors(); err != nil {
			_ = r.display.SetGamma(uint32(previous))
			_ = r.refreshGammaColors()
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1}, nil
	case 0x29: // SetFont(size)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if stack[last] >= uint32(len(gnex32FontHeights)) {
			return ServiceResult{}, fmt.Errorf("gnex32: unsupported font size %d", stack[last])
		}
		r.fontSize = stack[last]
		return ServiceResult{Pop: 1}, nil
	case 0x90: // SetKeyTone(on)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		switch stack[last] {
		case 0:
			if err := r.stopKeyTone(); err != nil {
				return ServiceResult{}, err
			}
			r.keyToneOn = false
		case 1:
			r.keyToneOn = true
		default:
			return ServiceResult{}, fmt.Errorf("gnex32: key tone switch %d is outside 0..1", stack[last])
		}
		return ServiceResult{Pop: 1}, nil
	case 0x91: // SetBackLight(mode)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if stack[last] > uint32(BacklightMMI) {
			return ServiceResult{}, fmt.Errorf("gnex32: backlight mode %d is outside 0..2", stack[last])
		}
		if err := r.setBacklight(BacklightMode(stack[last])); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1}, nil
	case 0x82, 0x83: // PlaySound(media), StopSound()
		if id == 0x82 {
			if last < 0 {
				return ServiceResult{}, ErrStackUnderflow
			}
			if err := r.playSound(int(stack[last])); err != nil {
				return ServiceResult{}, err
			}
			return ServiceResult{Pop: 1}, nil
		}
		return ServiceResult{}, r.stopSound()
	case 0x8e: // StartVib(seconds)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		seconds := int32(stack[last])
		if seconds < 0 {
			return ServiceResult{}, fmt.Errorf("gnex32: negative vibration duration %d", seconds)
		}
		if err := r.services.Device.Vibrate(
			100, time.Duration(seconds)*time.Second, r.services.Clock.Monotonic(),
		); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1}, nil
	case 0x8f: // StopVib()
		if err := r.StopVibration(); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{}, nil
	case 0x95, 0x96, 0x97: // SetTimer0/1/2(periodMs, onceOrRepeat)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		period := stack[last-1]
		if period > 60000 {
			return ServiceResult{}, fmt.Errorf("gnex32: timer period exceeds limit")
		}
		if stack[last] > 1 {
			return ServiceResult{}, fmt.Errorf("gnex32: timer flag %d is outside 0..1", stack[last])
		}
		timer := &r.timers[id-0x95]
		timer.period = period
		timer.repeat = stack[last] != 0
		timer.active = period != 0
		timer.due = r.tick*1000 + uint64(period)*60
		return ServiceResult{Pop: 2}, nil
	case 0xd8: // Min(int, int)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := stack[last-1]
		if int32(stack[last]) < int32(value) {
			value = stack[last]
		}
		return ServiceResult{Pop: 2, Push: []uint32{value}}, nil
	case 0x98, 0x99, 0x9a: // ResetTimer0/1/2()
		r.timers[id-0x98].active = false
		return ServiceResult{}, nil
	default:
		return ServiceResult{}, &UnsupportedServiceError{ID: id}
	}
}

func (r *Runtime) playSound(index int) error {
	resource, _, err := r.vm.media.Read(index)
	if err != nil {
		return err
	}
	mediaType, payload, err := gnex32SoundPayload(resource)
	if err != nil {
		return fmt.Errorf("gnex32: play sound media %d: %w", index, err)
	}
	clip, err := r.services.Media.CreateClip(r.owner, mediaType, uint64(len(payload)))
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = r.services.Media.DestroyClip(r.owner, clip, r.services.Events)
	}
	if _, err := r.services.Media.Append(r.owner, clip, payload); err != nil {
		cleanup()
		return err
	}
	if err := r.services.Media.Prepare(r.owner, clip); err != nil {
		cleanup()
		return err
	}
	if err := r.stopSound(); err != nil {
		cleanup()
		return err
	}
	if err := r.services.Media.Play(r.owner, clip, 1); err != nil {
		cleanup()
		return err
	}
	r.soundClip = clip
	return nil
}

func (r *Runtime) stopSound() error {
	if r.soundClip == 0 {
		return nil
	}
	clip := r.soundClip
	info, err := r.services.Media.Info(r.owner, clip)
	if err != nil {
		return err
	}
	if info.State == shared.ClipPlaying || info.State == shared.ClipPaused {
		// Preserve PCM already rendered before the guest's stop time. Removing it
		// would also erase short sounds before a host prebuffer can play them.
		if err := r.services.Media.StopOnTimeline(r.owner, clip); err != nil {
			return err
		}
	}
	if err := r.services.Media.DestroyClip(r.owner, clip, r.services.Events); err != nil {
		return err
	}
	r.soundClip = 0
	return nil
}

func (r *Runtime) playKeyTone(code uint16) error {
	payload := gnex32KeyToneWave(code)
	clip, err := r.services.Media.CreateClip(r.owner, "audio/wav", uint64(len(payload)))
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = r.services.Media.DestroyClip(r.owner, clip, r.services.Events)
	}
	if _, err := r.services.Media.Append(r.owner, clip, payload); err != nil {
		cleanup()
		return err
	}
	if err := r.services.Media.Prepare(r.owner, clip); err != nil {
		cleanup()
		return err
	}
	if err := r.stopKeyTone(); err != nil {
		cleanup()
		return err
	}
	if err := r.services.Media.Play(r.owner, clip, 1); err != nil {
		cleanup()
		return err
	}
	r.keyToneClip = clip
	return nil
}

func (r *Runtime) stopKeyTone() error {
	if r.keyToneClip == 0 {
		return nil
	}
	clip := r.keyToneClip
	info, err := r.services.Media.Info(r.owner, clip)
	if err != nil {
		return err
	}
	if info.State == shared.ClipPlaying || info.State == shared.ClipPaused {
		if err := r.services.Media.StopOnTimeline(r.owner, clip); err != nil {
			return err
		}
	}
	if err := r.services.Media.DestroyClip(r.owner, clip, r.services.Events); err != nil {
		return err
	}
	r.keyToneClip = 0
	return nil
}

func (r *Runtime) setBacklight(mode BacklightMode) error {
	if mode > BacklightMMI {
		return fmt.Errorf("gnex32: backlight mode %d is outside 0..2", mode)
	}
	if err := r.services.Device.SetBacklight(
		r.backlightEnabled(mode),
		0,
		r.services.Clock.Monotonic(),
	); err != nil {
		return err
	}
	r.backlight = mode
	return nil
}

func (r *Runtime) backlightEnabled(mode BacklightMode) bool {
	return mode == BacklightOn || mode == BacklightMMI && r.profile.MMIBacklightEnabled
}

// Version-4 flag-0x200 resources normally contain encoded media directly. Some
// GNEX resource exporters retain the older little-endian sound-kind prefix (2),
// so accept both layouts as well as mutable resources assembled by the guest.
func gnex32SoundPayload(resource []byte) (string, []byte, error) {
	payload := resource
	if len(payload) >= 2 && payload[0] == 2 && payload[1] == 0 {
		payload = payload[2:]
	}
	if len(payload) < 4 {
		return "", nil, fmt.Errorf("%w: truncated sound resource", shared.ErrMediaUnsupported)
	}
	switch string(payload[:4]) {
	case "MMMD":
		return "audio/x-smaf", payload, nil
	case "MThd":
		return "audio/midi", payload, nil
	case "RIFF":
		return "audio/wav", payload, nil
	default:
		return "", nil, fmt.Errorf("%w: sound resource has signature %x", shared.ErrMediaUnsupported, payload[:4])
	}
}
