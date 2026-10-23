package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
	"github.com/mirusu400/aram-core/profile"
)

const (
	GNEX32VirusSHA256          = "bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883"
	GNEX32VirusProfileID       = "gnex32/skt/virus"
	GNEX32UnqualifiedProfileID = "gnex32/skt/unqualified-v1"
)

var gnex32VirusProfile = gnex32.DeviceProfile{
	ID:                  GNEX32VirusProfileID,
	ScreenWidth:         120,
	ScreenHeight:        149,
	Identity:            gnex32.Identity{MIN: "0111234567", UserID: "98B4970F"},
	Platform:            "WIPI 1.2",
	KeyToneEnabled:      true,
	MMIBacklightEnabled: true,
}

type gnex32CompatibilityProfile struct {
	SHA256 string
	Title  string
	Device gnex32.DeviceProfile
}

type gnex32CompatibilityRegistry map[string]gnex32CompatibilityProfile

var gnex32CompatibilityProfiles = mustGNEX32CompatibilityRegistry([]gnex32CompatibilityProfile{
	{SHA256: GNEX32VirusSHA256, Title: "바이러스무삭제", Device: gnex32VirusProfile},
})

func newGNEX32CompatibilityRegistry(entries []gnex32CompatibilityProfile) (gnex32CompatibilityRegistry, error) {
	registry := make(gnex32CompatibilityRegistry, len(entries))
	profileIDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		digest := strings.ToLower(entry.SHA256)
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || digest != entry.SHA256 {
			return nil, fmt.Errorf("invalid GNEX32 compatibility SHA-256 %q", entry.SHA256)
		}
		if strings.TrimSpace(entry.Title) == "" || strings.IndexByte(entry.Title, 0) >= 0 {
			return nil, fmt.Errorf("GNEX32 compatibility %s has invalid title", digest)
		}
		if err := entry.Device.Validate(); err != nil {
			return nil, fmt.Errorf("GNEX32 compatibility %s: %w", digest, err)
		}
		if _, duplicate := registry[digest]; duplicate {
			return nil, fmt.Errorf("duplicate GNEX32 compatibility SHA-256 %s", digest)
		}
		if profileIDs[entry.Device.ID] {
			return nil, fmt.Errorf("duplicate GNEX32 compatibility profile ID %q", entry.Device.ID)
		}
		entry.SHA256 = digest
		registry[digest] = entry
		profileIDs[entry.Device.ID] = true
	}
	return registry, nil
}

func mustGNEX32CompatibilityRegistry(entries []gnex32CompatibilityProfile) gnex32CompatibilityRegistry {
	registry, err := newGNEX32CompatibilityRegistry(entries)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry gnex32CompatibilityRegistry) Lookup(digest string) (gnex32CompatibilityProfile, bool) {
	profile, ok := registry[strings.ToLower(digest)]
	return profile, ok
}

type gnex32Machine struct {
	mu      sync.Mutex
	state   machinecore.State
	source  machinecore.Source
	image   gnex.GNEX32Image
	profile gnex32.DeviceProfile
	runtime *gnex32.Runtime
	budget  uint64
	closed  bool

	audioGeneration        uint64
	audioGenerationPending bool
	audioEpochGuestNS      int64
	audioCursorSample      uint64
	audioCursorValid       bool
	mediaOutputRevision    uint64
	presentation           framePresentationCache
	presentationCount      uint64
}

func (f Factory) createGNEX32Machine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	_, hashQualified := gnex32CompatibilityProfiles.Lookup(source.SHA256)
	if source.Format != string(loader.KindGNEX) && !hashQualified {
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
		return nil, true, err
	}
	if int64(len(data)) != source.Size {
		return nil, true, io.ErrUnexpectedEOF
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actual) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, actual)
	}
	compatibility, qualified := gnex32CompatibilityProfiles.Lookup(actual)
	if !qualified {
		return nil, false, nil
	}
	pkg, err := gnex.Inspect(data)
	if err != nil {
		return nil, true, err
	}
	initialUserNV, hasInitialUserNV, err := gnex32ArchiveUserNV(pkg)
	if err != nil {
		return nil, true, err
	}
	image, err := gnex.DecodeGNEX32Image(pkg.SGS)
	if err != nil {
		return nil, true, err
	}
	if image.Header.Title != compatibility.Title {
		return nil, true, fmt.Errorf(
			"GNEX32 compatibility profile %q expects title %q, got %q",
			compatibility.Device.ID, compatibility.Title, image.Header.Title,
		)
	}
	if _, err := gnex32.Preflight(image); err != nil {
		return nil, true, fmt.Errorf("preflight GNEX32 compatibility for %q: %w", source.Name, err)
	}
	runtime, err := gnex32.NewRuntime(image, compatibility.Device)
	if err != nil {
		return nil, true, err
	}
	if hasInitialUserNV {
		runtime.RestoreUserNV(initialUserNV)
	}
	source.SHA256 = actual
	source.Format = string(loader.KindGNEX)
	source.ProfileID = compatibility.Device.ID
	budget := f.FrameRunBudget
	if budget == 0 {
		budget = 1_000_000
	}
	return &gnex32Machine{
		state:               machinecore.StateReady,
		source:              source,
		image:               image,
		profile:             compatibility.Device,
		runtime:             runtime,
		budget:              budget,
		audioGeneration:     1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}, true, nil
}

func (m *gnex32Machine) Load(context.Context, machinecore.Source) error {
	return fmt.Errorf("load from %s: %w", m.State(), ErrInvalidState)
}

func (m *gnex32Machine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *gnex32Machine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("GNEX32 machine is closed")
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.runtime.RunMain(m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("start GNEX32 image at code offset %d: %w", m.runtime.PC(), err)
	}
	if stopped, err := m.finishGNEX32CloseRequestLocked(); stopped || err != nil {
		return err
	}
	m.state = machinecore.StateRunning
	return nil
}

func (m *gnex32Machine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *gnex32Machine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateRunning
	return nil
}

func (m *gnex32Machine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	if err := m.endGNEX32Locked(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	if err := m.resetGNEX32AudioLocked(m.runtime.AudioTime()); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	if err := m.runtime.StopVibration(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	if err := m.runtime.StopNetwork(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *gnex32Machine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("GNEX32 machine is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.endGNEX32Locked(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	var userNV [gnex32.UserNVWords]uint32
	if m.runtime != nil {
		userNV = m.runtime.CaptureUserNV()
	}
	runtime, err := gnex32.NewRuntime(m.image, m.profile)
	if err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	runtime.RestoreUserNV(userNV)
	if m.runtime != nil {
		if err := m.runtime.Close(); err != nil {
			_ = runtime.Close()
			m.state = machinecore.StateFaulted
			return err
		}
	}
	m.runtime = runtime
	m.mediaOutputRevision = runtime.AudioOutputRevision()
	m.presentation = framePresentationCache{}
	m.presentationCount = 0
	m.nextGNEX32AudioGenerationLocked(0)
	m.state = machinecore.StateReady
	return nil
}

func (m *gnex32Machine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.runtime.StepFrame(m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("step GNEX32 timer at code offset %d: %w", m.runtime.PC(), err)
	}
	if _, err := m.finishGNEX32CloseRequestLocked(); err != nil {
		return err
	}
	return nil
}

func (m *gnex32Machine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("GNEX32 input from %s: %w", m.state, ErrInvalidState)
	}
	key, known := guest.InputKeyCode(event.Control)
	if !known {
		return ErrGVMInputUnavailable
	}
	code, supported := gnex32GuestCode(key)
	if !supported {
		// The backend-neutral menu control is the third WIPI soft key, but the
		// qualified Mobile C handset only exposes two soft keys. Real hardware
		// cannot deliver that key to this runtime, so ignore it instead of
		// faulting an otherwise valid input stream.
		if key == profile.KeySoft3 {
			return nil
		}
		return ErrGVMInputUnavailable
	}
	if err := m.runtime.QueueKey(code, event.Pressed, event.At, m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("deliver GNEX32 key %q at code offset %d: %w", event.Control, m.runtime.PC(), err)
	}
	if _, err := m.finishGNEX32CloseRequestLocked(); err != nil {
		return err
	}
	return nil
}

func (m *gnex32Machine) finishGNEX32CloseRequestLocked() (bool, error) {
	if !m.runtime.TakeCloseRequest() {
		return false, nil
	}
	if m.runtime.TakeExitRequest() {
		if err := m.runtime.End(m.budget); err != nil {
			m.state = machinecore.StateFaulted
			return false, fmt.Errorf("deliver GNEX32 end event at code offset %d: %w", m.runtime.PC(), err)
		}
		// EVENT_END may itself request termination. The current shutdown already
		// owns that request, so do not leave it pending in a stopped machine.
		m.runtime.TakeCloseRequest()
		m.runtime.TakeExitRequest()
	}
	if err := m.resetGNEX32AudioLocked(m.runtime.AudioTime()); err != nil {
		m.state = machinecore.StateFaulted
		return false, err
	}
	if err := m.runtime.StopVibration(); err != nil {
		m.state = machinecore.StateFaulted
		return false, err
	}
	if err := m.runtime.StopNetwork(); err != nil {
		m.state = machinecore.StateFaulted
		return false, err
	}
	m.state = machinecore.StateStopped
	return true, nil
}

func gnex32GuestCode(key profile.KeyCode) (uint16, bool) {
	switch key {
	case profile.Key1:
		return gnex32.Key1, true
	case profile.Key2:
		return gnex32.Key2, true
	case profile.Key3:
		return gnex32.Key3, true
	case profile.Key4:
		return gnex32.Key4, true
	case profile.Key5:
		return gnex32.Key5, true
	case profile.Key6:
		return gnex32.Key6, true
	case profile.Key7:
		return gnex32.Key7, true
	case profile.Key8:
		return gnex32.Key8, true
	case profile.Key9:
		return gnex32.Key9, true
	case profile.Key0:
		return gnex32.Key0, true
	case profile.KeyAsterisk:
		return gnex32.KeyAsterisk, true
	case profile.KeyPound:
		return gnex32.KeyPound, true
	case profile.KeyLeft:
		return gnex32.KeyLeft, true
	case profile.KeyRight:
		return gnex32.KeyRight, true
	case profile.KeyUp:
		return gnex32.KeyUp, true
	case profile.KeyDown:
		return gnex32.KeyDown, true
	case profile.KeySelect:
		return gnex32.KeyOK, true
	case profile.KeyClear:
		return gnex32.KeyClear, true
	case profile.KeySend:
		return gnex32.KeySend, true
	case profile.KeyEnd:
		return gnex32.KeyEnd, true
	case profile.KeySoft1:
		return gnex32.KeySoft1, true
	case profile.KeySoft2:
		return gnex32.KeySoft2, true
	default:
		return 0, false
	}
}

func (m *gnex32Machine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return nil
	}
	return m.runtime.Frame()
}

func (m *gnex32Machine) GNEX32PresentCount() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return 0
	}
	return m.runtime.PresentCount()
}

// EnableGNEX32ExecutionTrace starts a fresh bounded diagnostic trace.
func (m *gnex32Machine) EnableGNEX32ExecutionTrace() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime != nil {
		m.runtime.EnableExecutionTrace()
	}
}

// GNEX32ExecutionTrace returns a detached snapshot of dynamic VM coverage.
func (m *gnex32Machine) GNEX32ExecutionTrace() gnex32.ExecutionTrace {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return gnex32.ExecutionTrace{}
	}
	return m.runtime.ExecutionTrace()
}

func (m *gnex32Machine) GNEX32BacklightMode() gnex32.BacklightMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return gnex32.BacklightMMI
	}
	return m.runtime.BacklightMode()
}

// ReceiveHostEvent translates the backend-neutral handset ingress contract to
// the Mobile C callbacks used by GNEX32 titles.
func (m *gnex32Machine) ReceiveHostEvent(event machinecore.HostEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	data := gnex32.EventData{
		Data:  event.Words[0],
		Data1: event.Words[1],
		Data2: event.Words[2],
		Data3: event.Words[3],
	}
	switch event.Kind {
	case machinecore.HostEventNetworkReceive:
		return m.InjectGNEX32NetworkReceive(event.Data)
	case machinecore.HostEventSerialReceive:
		return m.InjectGNEX32SerialReceive(data)
	case machinecore.HostEventSMSReceive:
		return m.InjectGNEX32SMSReceive(data)
	default:
		panic("validated host event kind is unhandled")
	}
}

// InjectGNEX32NetworkReceive is the optional host ingress boundary for a
// connected Mobile C title. It preserves the machine's execution serialization.
func (m *gnex32Machine) InjectGNEX32NetworkReceive(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("receive GNEX32 network data from %s: %w", m.state, ErrInvalidState)
	}
	if err := m.runtime.InjectNetworkReceive(data, m.budget); err != nil {
		if errors.Is(err, gnex32.ErrNetworkReceiveUnavailable) ||
			errors.Is(err, gnex32.ErrNetworkReceiveTooLarge) {
			return err
		}
		m.state = machinecore.StateFaulted
		return fmt.Errorf("deliver GNEX32 network data at code offset %d: %w", m.runtime.PC(), err)
	}
	_, err := m.finishGNEX32CloseRequestLocked()
	return err
}

func (m *gnex32Machine) InjectGNEX32SerialReceive(data gnex32.EventData) error {
	return m.injectGNEX32ExternalEvent("serial", func() error {
		return m.runtime.InjectSerialReceive(data, m.budget)
	})
}

func (m *gnex32Machine) InjectGNEX32SMSReceive(data gnex32.EventData) error {
	return m.injectGNEX32ExternalEvent("SMS", func() error {
		return m.runtime.InjectSMSReceive(data, m.budget)
	})
}

func (m *gnex32Machine) injectGNEX32ExternalEvent(kind string, dispatch func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("receive GNEX32 %s event from %s: %w", kind, m.state, ErrInvalidState)
	}
	if err := dispatch(); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("deliver GNEX32 %s event at code offset %d: %w", kind, m.runtime.PC(), err)
	}
	_, err := m.finishGNEX32CloseRequestLocked()
	return err
}

var _ machinecore.HostEventReceiver = (*gnex32Machine)(nil)

func (m *gnex32Machine) DrainAudio() machinecore.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return machinecore.AudioChunk{}
	}
	audio, start, revision := m.runtime.DrainTimedAudio()
	if audio.SampleRate <= 0 || audio.Channels <= 0 || len(audio.PCM16) == 0 ||
		len(audio.PCM16)%audio.Channels != 0 {
		if revision != m.mediaOutputRevision {
			m.nextGNEX32AudioGenerationLocked(start)
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
		m.nextGNEX32AudioGenerationLocked(start)
		m.mediaOutputRevision = revision
	}
	startSample := gnex32SampleCursor(time.Duration(int64(start)-m.audioEpochGuestNS), audio.SampleRate)
	if m.audioCursorValid {
		slack := gnex32SampleCursor(time.Millisecond, audio.SampleRate)
		if gnex32DistanceWithin(startSample, m.audioCursorSample, slack) {
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

func gnex32DistanceWithin(left, right, limit uint64) bool {
	if left >= right {
		return left-right <= limit
	}
	return right-left <= limit
}

func gnex32SampleCursor(elapsed time.Duration, sampleRate int) uint64 {
	if elapsed <= 0 || sampleRate <= 0 {
		return 0
	}
	seconds := uint64(elapsed / time.Second)
	remainder := uint64(elapsed % time.Second)
	return seconds*uint64(sampleRate) + remainder*uint64(sampleRate)/uint64(time.Second)
}

func (m *gnex32Machine) nextGNEX32AudioGenerationLocked(epoch time.Duration) {
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

func (m *gnex32Machine) resetGNEX32AudioLocked(epoch time.Duration) error {
	if m.runtime != nil {
		var err error
		epoch, m.mediaOutputRevision, err = m.runtime.ResetAudioOutput()
		if err != nil {
			return err
		}
	}
	m.nextGNEX32AudioGenerationLocked(epoch)
	return nil
}

func (m *gnex32Machine) endGNEX32Locked() error {
	if m.runtime == nil ||
		m.state != machinecore.StateRunning && m.state != machinecore.StatePaused {
		return nil
	}
	if err := m.runtime.End(m.budget); err != nil {
		return fmt.Errorf("deliver GNEX32 end event at code offset %d: %w", m.runtime.PC(), err)
	}
	return nil
}

func (m *gnex32Machine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	var result error
	if m.runtime != nil {
		if err := m.endGNEX32Locked(); err != nil {
			result = errors.Join(result, err)
		}
		if err := m.resetGNEX32AudioLocked(m.runtime.AudioTime()); err != nil {
			result = errors.Join(result, err)
		}
		if err := m.runtime.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}
	m.closed = true
	m.state = machinecore.StateStopped
	return result
}

func (m *gnex32Machine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}
