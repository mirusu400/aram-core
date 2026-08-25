// Package systemmachine composes firmware loaders, board profiles, CPU
// backends, and guest-neutral system devices into headless whole-phone
// machines. It deliberately sits above package system so generic buses and
// devices never need firmware-model checks.
package systemmachine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"sync"
	"sync/atomic"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
	"github.com/mirusu400/aram-core/system"
)

const (
	SnapshotSchema                   = "aram-system-machine-state-v1"
	schw830QCSBLBoundaryInstructions = uint64(1_195_629)
	schw830QCSBLBoundaryPC           = uint32(0x000a07d8)
)

var (
	ErrClosed             = errors.New("system machine is closed")
	ErrIncompatibleMedia  = errors.New("media state is incompatible with the system machine")
	ErrIncompatibleState  = errors.New("snapshot is incompatible with the system machine")
	ErrUnsupportedBackend = errors.New("CPU backend lacks required whole-system capabilities")
	ErrUnsupportedControl = errors.New("system machine has no such input control")
	ErrUnsupportedMachine = errors.New("recognized firmware has no system machine")
)

// Identity contains only stable, privacy-safe machine selection facts.
type Identity struct {
	Manufacturer    string
	Model           string
	FirmwareBuild   string
	FirmwareBuildID string
	BoardID         string
	PlatformID      string
	CPU             cpu.Identity
}

// Options customizes construction without changing board facts. A nil Backend
// selects the portable interpreter. The constructed Machine owns and closes a
// supplied backend. Media, when supplied, is restored before the first
// instruction executes.
type Options struct {
	Backend       cpu.Backend
	RunnerQuantum uint64
	Media         *MediaState
}

// MediaState is the persistent NAND state which survives a power cycle. Flash
// contains main-area copy-on-write blocks; NAND contains the physical spare/OOB
// pages. Neither field contains the immutable user-supplied firmware pieces.
type MediaState struct {
	FirmwareBuildID string
	Flash           []byte
	NAND            []byte
}

// Snapshot captures complete volatile execution state plus persistent media.
// It is intentionally an in-memory contract; product-specific serialization
// can wrap it without teaching core about paths or frontend storage.
type Snapshot struct {
	Schema          string
	FirmwareBuildID string
	BoardID         string
	PlatformID      string
	CPUIdentity     cpu.Identity
	CPU             []byte
	Bus             []byte
	Flash           []byte
	Instructions    uint64
}

// Position reports the next guest instruction and cumulative work since the
// last power cycle or loaded snapshot.
type Position struct {
	PC           uint32
	Mode         cpu.Mode
	Instructions uint64
}

// Machine is a synchronous headless whole-phone machine. Run, PowerCycle,
// input, frame, and state methods are serialized; Stop may be called from a
// different goroutine to interrupt Run.
type Machine struct {
	mu sync.Mutex

	identity Identity
	backend  cpu.Backend
	bus      *system.Bus
	runner   *system.ClockedRunner
	handoff  system.BootHandoff
	flash    *system.COWFlash
	nand     *system.QualcommNAND
	panel    *system.DCSPanelController
	keypad   *system.QualcommGPIOKeypad
	controls []string

	resetCPUState    []byte
	factoryNANDState []byte
	pc               uint32
	mode             cpu.Mode
	instructions     uint64
	bootBoundary     bootBoundary
	bootBoundaryLeft uint64
	closed           atomic.Bool
}

type bootBoundary struct {
	name         string
	instructions uint64
	pc           uint32
}

// New recognizes an exact supported firmware set and dispatches it to a named
// platform/board constructor. Recognizing a container or build never silently
// substitutes SCH-W830 hardware for a different phone.
func New(set firmwareset.Set, options Options) (*Machine, error) {
	pkg, err := samsung.Inspect(set)
	if err != nil {
		return nil, fmt.Errorf("inspect Samsung firmware set: %w", err)
	}
	firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		return nil, fmt.Errorf("select Samsung firmware build: %w", err)
	}
	switch firmwareProfile.Model {
	case "SCH-W830":
		return newSCHW830(set, pkg, firmwareProfile, options)
	case "SCH-W860":
		return newSCHW860(set, pkg, firmwareProfile, options)
	default:
		return nil, fmt.Errorf("%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build)
	}
}

// NewSCHW860 constructs the currently evidenced DA06 adjacent-board machine.
// Its boot path is usable for compatibility research; no keypad wiring or
// complete user-interface milestone is claimed yet.
func NewSCHW860(set firmwareset.Set, options Options) (*Machine, error) {
	pkg, err := samsung.Inspect(set)
	if err != nil {
		return nil, fmt.Errorf("inspect Samsung firmware set: %w", err)
	}
	firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		return nil, fmt.Errorf("select Samsung firmware build: %w", err)
	}
	if firmwareProfile.Model != "SCH-W860" {
		return nil, fmt.Errorf("%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build)
	}
	return newSCHW860(set, pkg, firmwareProfile, options)
}

// NewSCHW830 is the board-specific equivalent of New. Piece order and host
// filenames are irrelevant; a recognized non-W830 build is rejected.
func NewSCHW830(set firmwareset.Set, options Options) (*Machine, error) {
	pkg, err := samsung.Inspect(set)
	if err != nil {
		return nil, fmt.Errorf("inspect Samsung firmware set: %w", err)
	}
	firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		return nil, fmt.Errorf("select Samsung firmware build: %w", err)
	}
	if firmwareProfile.Model != "SCH-W830" {
		return nil, fmt.Errorf("%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build)
	}
	return newSCHW830(set, pkg, firmwareProfile, options)
}

func newSCHW830(
	set firmwareset.Set,
	pkg samsung.Package,
	firmwareProfile samsung.BuildProfile,
	options Options,
) (*Machine, error) {
	board := system.SCHW830DL21BoardProfile()
	board.FirmwareBuildID = firmwareProfile.ID
	return newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{
		name:         "SCH-W830 QCSBL callback",
		instructions: schw830QCSBLBoundaryInstructions,
		pc:           schw830QCSBLBoundaryPC,
	}, options)
}

func newSCHW860(
	set firmwareset.Set,
	pkg samsung.Package,
	firmwareProfile samsung.BuildProfile,
	options Options,
) (*Machine, error) {
	board := system.SCHW860DA06BoardProfile()
	board.FirmwareBuildID = firmwareProfile.ID
	return newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{}, options)
}

func newSamsungQualcommMachine(
	set firmwareset.Set,
	pkg samsung.Package,
	firmwareProfile samsung.BuildProfile,
	board system.BoardProfile,
	boundary bootBoundary,
	options Options,
) (*Machine, error) {
	qcsblSpec, ok := firmwareProfile.BootImage("qcsbl")
	if !ok {
		return nil, fmt.Errorf("firmware build %q has no QCSBL image", firmwareProfile.ID)
	}
	qcsbl, err := samsung.ReconstructBootImage(set, pkg, qcsblSpec)
	if err != nil {
		return nil, fmt.Errorf("reconstruct QCSBL: %w", err)
	}

	if err := board.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s board: %w", firmwareProfile.Model, err)
	}
	flashImage, err := samsung.AssembleFlashWithOptions(set, pkg, samsung.FlashAssemblyOptions{
		FactoryBadBlocks: board.NANDFactoryBadBlocks,
	})
	if err != nil {
		return nil, fmt.Errorf("assemble %s NAND: %w", firmwareProfile.Model, err)
	}
	flash, err := system.NewCOWFlashWithCapacityAndSeeds(
		flashImage,
		board.NANDSize,
		samsung.EraseBlockSize,
		flashImage.Identity(),
		board.NANDInitialData,
	)
	if err != nil {
		return nil, fmt.Errorf("create %s writable NAND: %w", firmwareProfile.Model, err)
	}

	backend := options.Backend
	ownedBackend := false
	if backend == nil {
		backend = interpreter.New()
		ownedBackend = true
	}
	fail := func(constructionErr error) (*Machine, error) {
		if ownedBackend {
			_ = backend.Close()
		}
		return nil, constructionErr
	}
	if err := requireSystemBackend(backend); err != nil {
		return fail(err)
	}
	interruptSink, ok := backend.(cpu.InterruptLineBackend)
	if !ok {
		return fail(fmt.Errorf("%w: %s has no interrupt-line sink", ErrUnsupportedBackend, backend.Identity().Name))
	}

	legacyInterrupts := system.NewQualcommInterruptController(nil)
	vectoredInterrupts, err := system.NewQualcommVectoredInterruptController(
		*board.VectoredInterrupt,
		interruptSink,
	)
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 vectored interrupt controller: %w", err))
	}
	nandReady := system.NewStatusSignal()
	nandConfig := system.Qualcomm2K8BitNANDConfig(board.NANDReadID, nandReady)
	nandConfig.Capacity = board.NANDSize
	nandConfig.FactoryBadBlocks = append([]uint32(nil), board.NANDFactoryBadBlocks...)
	if nandConfig.PageSize != samsung.PageSize {
		return fail(fmt.Errorf("SCH-W830 NAND page size does not match normalized flash"))
	}
	nand, err := system.NewQualcommNAND(flash, nandConfig)
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 NAND controller: %w", err))
	}
	factoryNANDState, err := nand.SaveState()
	if err != nil {
		return fail(fmt.Errorf("capture SCH-W830 factory NAND state: %w", err))
	}

	bootControl, err := system.NewQualcommBootControl(system.QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: board.BootClockModeStatus,
		WritableOffsets:             board.BootControlWritableOffsets,
		HalfwordOffsets:             board.BootControlHalfwordOffsets,
		MixedWidthOffsets:           board.BootControlMixedWidthOffsets,
		ReadOnlyRegisters:           board.BootControlReadOnlyRegisters,
		RegisterResets:              board.BootControlRegisterResets,
		CompletionEvents:            board.BootControlCompletionEvents,
		LegacyUARTControllers:       board.BootControlLegacyUARTControllers,
		SBIControllers:              board.BootControlSBIControllers,
		SBICompletionStatus:         board.BootControlSBICompletionStatus,
		NANDReady:                   nandReady,
		InterruptController:         legacyInterrupts,
		VectoredInterruptController: vectoredInterrupts,
		TimeTickClock:               cloneTimeTickClock(board.TimeTickClock),
	})
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 boot control: %w", err))
	}
	secondaryClock, err := system.NewQualcommSecondaryClockControlWithWritableOffsets(
		board.SecondaryClockWritableOffsets,
	)
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 secondary clock: %w", err))
	}
	primaryClock, err := system.NewQualcommPrimaryClockControl(system.QualcommPrimaryClockConfig{
		Status:          board.PrimaryClockStatus,
		InputMask:       board.PrimaryClockInputMask,
		WritableOffsets: board.PrimaryClockWritableOffsets,
	})
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 primary clock: %w", err))
	}
	keypad, err := board.AttachKeypad(primaryClock, secondaryClock, legacyInterrupts)
	if err != nil {
		return fail(err)
	}
	legacyTop, err := system.NewQualcommLegacyTopPageWithConfig(system.QualcommLegacyTopConfig{
		Version:         board.LegacyTopVersion,
		Identification:  board.LegacyTopIdentification,
		WritableOffsets: board.LegacyTopWritableOffsets,
	})
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 legacy top page: %w", err))
	}
	clockRegime, err := system.NewQualcommClockRegimeWithConfig(system.QualcommClockRegimeConfig{
		SleepControllers:            board.ClockRegimeSleepControllers,
		Counters:                    board.ClockRegimeCounters,
		Comparators:                 board.ClockRegimeComparators,
		InterruptController:         legacyInterrupts,
		VectoredInterruptController: vectoredInterrupts,
	})
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 clock regime: %w", err))
	}
	busRegisters, err := system.NewSparseWordRegisters(schw830BusRegisterOffsets())
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 sparse bus registers: %w", err))
	}
	panelController, err := system.NewDCSPanelController(board.Panel)
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 panel controller: %w", err))
	}
	panel, err := system.NewParallelPanelInterfaceWithController(panelController)
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 panel transport: %w", err))
	}
	handoff, err := system.NewQualcommNANDPBLHandoff(system.QualcommNANDPBLConfig{
		Entry: qcsbl.EntryAddress, TableAddress: 0x78001000,
		PageSize: samsung.PageSize, EraseBlockSize: samsung.EraseBlockSize,
		FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
	})
	if err != nil {
		return fail(fmt.Errorf("create SCH-W830 PBL handoff: %w", err))
	}
	handoff.Memory = append(handoff.Memory, system.MemorySeed{
		Address: qcsbl.LoadAddress,
		Bytes:   append([]byte(nil), qcsbl.Bytes...),
	})

	bus := system.NewBus()
	if _, err := board.AttachMDP(bus, panelController, bootControl); err != nil {
		return fail(err)
	}
	if err := mapSCHW830Board(
		bus,
		board,
		legacyInterrupts,
		vectoredInterrupts,
		bootControl,
		nand,
		primaryClock,
		secondaryClock,
		panel,
		clockRegime,
		busRegisters,
		legacyTop,
	); err != nil {
		return fail(err)
	}
	if err := backend.(cpu.SystemBusBackend).AttachSystemBus(bus); err != nil {
		return fail(fmt.Errorf("attach SCH-W830 physical bus: %w", err))
	}
	if err := handoff.Apply(bus, backend); err != nil {
		return fail(fmt.Errorf("apply SCH-W830 PBL handoff: %w", err))
	}
	resetCPUState, err := backend.SaveContext()
	if err != nil {
		return fail(fmt.Errorf("capture SCH-W830 reset CPU state: %w", err))
	}
	quantum := options.RunnerQuantum
	if quantum == 0 {
		quantum = system.DefaultClockedRunnerQuantum
	}
	runner, err := system.NewClockedRunner(
		backend,
		backend,
		quantum,
		bus.ClockedDevices()...,
	)
	if err != nil {
		return fail(err)
	}
	machine := &Machine{
		identity: Identity{
			Manufacturer:    firmwareProfile.Manufacturer,
			Model:           firmwareProfile.Model,
			FirmwareBuild:   firmwareProfile.Build,
			FirmwareBuildID: firmwareProfile.ID,
			BoardID:         board.ID,
			PlatformID:      board.PlatformID,
			CPU:             backend.Identity(),
		},
		backend: backend, bus: bus, runner: runner, handoff: handoff,
		flash: flash, nand: nand, panel: panelController, keypad: keypad,
		controls:         boardControls(board),
		resetCPUState:    append([]byte(nil), resetCPUState...),
		factoryNANDState: append([]byte(nil), factoryNANDState...),
		pc:               handoff.Entry, mode: handoff.Mode,
		bootBoundary: boundary, bootBoundaryLeft: boundary.instructions,
	}
	if options.Media != nil {
		if err := machine.loadMediaLocked(*options.Media); err != nil {
			_ = backend.Close()
			return nil, err
		}
		if err := machine.powerCycleLocked(); err != nil {
			_ = backend.Close()
			return nil, err
		}
	}
	return machine, nil
}

func requireSystemBackend(backend cpu.Backend) error {
	if backend == nil {
		return fmt.Errorf("%w: nil backend", ErrUnsupportedBackend)
	}
	systemBackend, ok := backend.(cpu.SystemBackend)
	if !ok || backend.Architecture() != cpu.ARMv5TE {
		return fmt.Errorf("%w: %s", ErrUnsupportedBackend, backend.Identity().Name)
	}
	required := []cpu.SystemCapability{
		cpu.CapabilityPhysicalBus,
		cpu.CapabilityPrivilegedModes,
		cpu.CapabilityCP15Control,
		cpu.CapabilityMMU,
		cpu.CapabilityInterruptLines,
	}
	for _, capability := range required {
		if !systemBackend.SystemCapabilities().Has(capability) {
			return fmt.Errorf("%w: %s lacks capability 0x%x", ErrUnsupportedBackend, backend.Identity().Name, capability)
		}
	}
	return nil
}

func cloneTimeTickClock(source *system.QualcommTimeTickClockConfig) *system.QualcommTimeTickClockConfig {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func mapSCHW830Board(
	bus *system.Bus,
	board system.BoardProfile,
	legacyInterrupts *system.QualcommInterruptController,
	vectoredInterrupts *system.QualcommVectoredInterruptController,
	bootControl *system.QualcommBootControl,
	nand *system.QualcommNAND,
	primaryClock *system.QualcommPrimaryClockControl,
	secondaryClock *system.QualcommSecondaryClockControl,
	panel *system.ParallelPanelInterface,
	clockRegime *system.QualcommClockRegime,
	busRegisters *system.SparseWordRegisters,
	legacyTop *system.QualcommLegacyTopPage,
) error {
	if err := board.ApplyMemory(bus); err != nil {
		return err
	}
	if err := board.ApplyReadOnlyRegisters(bus); err != nil {
		return err
	}
	if err := board.ApplyLatchedRegistersWithInterrupts(bus, legacyInterrupts, vectoredInterrupts); err != nil {
		return err
	}
	mappings := []struct {
		name    string
		address uint32
		size    uint32
		device  system.Device
	}{
		{"qualcomm-boot-control", 0x80000000, system.QualcommBootControlWindowSize, bootControl},
		{"qualcomm-nand", 0x60000000, system.QualcommNANDWindowSize, nand},
		{"qualcomm-primary-clock", 0x84000000, system.QualcommPrimaryClockWindowSize, primaryClock},
		{"qualcomm-secondary-clock", 0x84004000, system.QualcommSecondaryClockWindowSize, secondaryClock},
		{"parallel-panel", 0x20000000, system.ParallelPanelWindowSize, panel},
		{"qualcomm-clock-regime", 0x90000000, system.QualcommClockRegimeWindowSize, clockRegime},
		{"qualcomm-sparse-bus-registers", 0x90400000, 0x1000, busRegisters},
		{"qualcomm-legacy-top-page", 0xfffff000, system.QualcommLegacyTopWindowSize, legacyTop},
	}
	for _, mapping := range mappings {
		if err := bus.MapMMIO(mapping.name, mapping.address, mapping.size, mapping.device); err != nil {
			return fmt.Errorf("map SCH-W830 device %q: %w", mapping.name, err)
		}
	}
	return nil
}

func schw830BusRegisterOffsets() []uint32 {
	offsets := make([]uint32, 0, 128)
	for _, span := range [][2]uint32{{0x240, 0x27c}, {0x280, 0x29c}, {0x2c0, 0x2dc}} {
		for offset := span[0]; offset <= span[1]; offset += 4 {
			offsets = append(offsets, offset)
		}
	}
	offsets = append(offsets,
		0x3a0, 0x3a4, 0x3a8, 0x3ac, 0x3b0, 0x3b4, 0x3b8, 0x3bc,
		0x3c0, 0x3c4, 0x3c8, 0x3cc, 0x3d0,
		0x3e0, 0x3e4, 0x3e8, 0x3ec, 0x3f0,
	)
	for column := uint32(0); column <= 0x200; column += 0x40 {
		offsets = append(offsets, column+0x10)
	}
	for column := uint32(0x400); column <= 0x600; column += 0x40 {
		for _, lane := range []uint32{0, 4, 8, 0x0c, 0x14} {
			offsets = append(offsets, column+lane)
		}
	}
	for column := uint32(0xc00); column <= 0xe00; column += 0x40 {
		offsets = append(offsets, column+0x18, column+0x1c)
	}
	return offsets
}

func boardControls(board system.BoardProfile) []string {
	if board.Keypad == nil {
		return nil
	}
	controls := make([]string, len(board.Keypad.Keys))
	for index, key := range board.Keypad.Keys {
		controls[index] = key.ID
	}
	return controls
}

func (m *Machine) Identity() Identity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.identity
}

func (m *Machine) Position() Position {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Position{PC: m.pc, Mode: m.mode, Instructions: m.instructions}
}

// Controls returns the stable input IDs accepted by SetKey.
func (m *Machine) Controls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.controls...)
}

// Run executes at most budget guest instructions. A zero budget runs until a
// stop, context cancellation, execution fault, or guest exit.
func (m *Machine) Run(ctx context.Context, budget uint64) cpu.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return cpu.Result{Reason: cpu.StopFault, PC: m.pc, Err: ErrClosed}
	}
	var retired uint64
	for budget == 0 || retired < budget {
		slice := uint64(0)
		if budget != 0 {
			slice = budget - retired
		}
		if m.bootBoundaryLeft != 0 && (slice == 0 || slice > m.bootBoundaryLeft) {
			slice = m.bootBoundaryLeft
		}
		result := m.runner.Run(ctx, m.pc, m.mode, slice)
		retired += result.Instructions
		m.instructions += result.Instructions
		m.pc = result.PC
		if status, err := m.backend.ReadRegister(cpu.RegisterCPSR); err == nil {
			m.mode = cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				m.mode = cpu.ModeThumb
			}
		}
		if m.bootBoundaryLeft != 0 {
			if result.Instructions > m.bootBoundaryLeft {
				return cpu.Result{
					Reason: cpu.StopFault, Instructions: retired, PC: m.pc,
					Err: fmt.Errorf(
						"%s boundary overrun by %d instructions",
						m.bootBoundary.name,
						result.Instructions-m.bootBoundaryLeft,
					),
				}
			}
			m.bootBoundaryLeft -= result.Instructions
			if m.bootBoundaryLeft == 0 && result.Err == nil &&
				result.Reason == cpu.StopBudget && m.pc != m.bootBoundary.pc {
				return cpu.Result{
					Reason: cpu.StopFault, Instructions: retired, PC: m.pc,
					Err: fmt.Errorf(
						"%s boundary PC 0x%08x, want 0x%08x",
						m.bootBoundary.name,
						m.pc,
						m.bootBoundary.pc,
					),
				}
			}
		}
		result.Instructions = retired
		if result.Err != nil || result.Reason != cpu.StopBudget ||
			budget != 0 && retired == budget {
			return result
		}
	}
	return cpu.Result{Reason: cpu.StopBudget, Instructions: retired, PC: m.pc}
}

// Stop interrupts an in-progress Run. A later Run resumes from the returned
// execution position.
func (m *Machine) Stop() error {
	if m.closed.Load() {
		return ErrClosed
	}
	return m.backend.Stop()
}

// SetKey changes one stable board-profile key ID.
func (m *Machine) SetKey(id string, pressed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	if m.keypad == nil {
		return ErrUnsupportedControl
	}
	return m.keypad.SetKey(id, pressed)
}

// Framebuffer returns a detached copy of the current guest-produced panel.
func (m *Machine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return nil
	}
	return m.panel.FrameRGBA()
}

// FrameRGB565 returns the panel's native pixel surface in row-major order.
func (m *Machine) FrameRGB565() []uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return nil
	}
	return m.panel.FrameRGB565()
}

// FrameSHA256 hashes the little-endian native RGB565 surface. It is useful for
// deterministic milestone reports without retaining proprietary screen bytes.
func (m *Machine) FrameSHA256() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ""
	}
	pixels := m.panel.FrameRGB565()
	encoded := make([]byte, len(pixels)*2)
	for index, pixel := range pixels {
		binary.LittleEndian.PutUint16(encoded[index*2:], pixel)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// PowerCycle resets CPU, RAM, and devices while preserving NAND media.
func (m *Machine) PowerCycle() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	return m.powerCycleLocked()
}

func (m *Machine) powerCycleLocked() error {
	if err := m.bus.Reset(); err != nil {
		return fmt.Errorf("reset system bus: %w", err)
	}
	if err := m.backend.RestoreContext(m.resetCPUState); err != nil {
		return fmt.Errorf("restore reset CPU state: %w", err)
	}
	if err := m.handoff.Apply(m.bus, m.backend); err != nil {
		return fmt.Errorf("reapply PBL handoff: %w", err)
	}
	m.pc = m.handoff.Entry
	m.mode = m.handoff.Mode
	m.instructions = 0
	m.bootBoundaryLeft = m.bootBoundary.instructions
	return nil
}

// SaveMedia returns a detached persistent-media snapshot suitable for a later
// constructor or LoadMedia call with the exact same firmware build.
func (m *Machine) SaveMedia() (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return MediaState{}, ErrClosed
	}
	return m.saveMediaLocked()
}

func (m *Machine) saveMediaLocked() (MediaState, error) {
	flashState, err := m.flash.SaveState()
	if err != nil {
		return MediaState{}, fmt.Errorf("save NAND main area: %w", err)
	}
	nandState, err := m.nand.SaveState()
	if err != nil {
		return MediaState{}, fmt.Errorf("save NAND spare area: %w", err)
	}
	return MediaState{
		FirmwareBuildID: m.identity.FirmwareBuildID,
		Flash:           append([]byte(nil), flashState...),
		NAND:            append([]byte(nil), nandState...),
	}, nil
}

// LoadMedia atomically replaces persistent NAND media and performs a power
// cycle so volatile guest state cannot disagree with the restored filesystem.
func (m *Machine) LoadMedia(media MediaState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	oldMedia, err := m.saveMediaLocked()
	if err != nil {
		return err
	}
	if err := m.loadMediaLocked(media); err != nil {
		return err
	}
	if err := m.powerCycleLocked(); err != nil {
		_ = m.loadMediaLocked(oldMedia)
		return err
	}
	return nil
}

func (m *Machine) loadMediaLocked(media MediaState) error {
	if media.FirmwareBuildID != m.identity.FirmwareBuildID || len(media.Flash) == 0 || len(media.NAND) == 0 {
		return ErrIncompatibleMedia
	}
	oldFlash, flashSaveErr := m.flash.SaveState()
	oldNAND, nandSaveErr := m.nand.SaveState()
	if flashSaveErr != nil || nandSaveErr != nil {
		return fmt.Errorf("capture media rollback state: %v %v", flashSaveErr, nandSaveErr)
	}
	if err := m.flash.LoadState(media.Flash); err != nil {
		return fmt.Errorf("load NAND main area: %w", err)
	}
	if err := m.nand.LoadState(media.NAND); err != nil {
		_ = m.flash.LoadState(oldFlash)
		_ = m.nand.LoadState(oldNAND)
		return fmt.Errorf("load NAND spare area: %w", err)
	}
	if err := m.nand.Reset(); err != nil {
		_ = m.flash.LoadState(oldFlash)
		_ = m.nand.LoadState(oldNAND)
		return fmt.Errorf("reset restored NAND controller: %w", err)
	}
	return nil
}

// FactoryReset discards guest NAND main/OOB writes and returns to the board's
// generated new-media baseline before applying a power cycle.
func (m *Machine) FactoryReset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	m.flash.FactoryReset()
	if err := m.nand.LoadState(m.factoryNANDState); err != nil {
		return fmt.Errorf("restore factory NAND spare state: %w", err)
	}
	return m.powerCycleLocked()
}

func (m *Machine) SaveSnapshot() (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return Snapshot{}, ErrClosed
	}
	cpuState, err := m.backend.SaveContext()
	if err != nil {
		return Snapshot{}, err
	}
	busState, err := m.bus.SaveState()
	if err != nil {
		return Snapshot{}, err
	}
	flashState, err := m.flash.SaveState()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Schema:          SnapshotSchema,
		FirmwareBuildID: m.identity.FirmwareBuildID,
		BoardID:         m.identity.BoardID,
		PlatformID:      m.identity.PlatformID,
		CPUIdentity:     m.identity.CPU,
		CPU:             append([]byte(nil), cpuState...),
		Bus:             append([]byte(nil), busState...),
		Flash:           append([]byte(nil), flashState...),
		Instructions:    m.instructions,
	}, nil
}

// LoadSnapshot atomically restores CPU, bus, devices, and persistent main-area
// flash from an exact-profile snapshot.
func (m *Machine) LoadSnapshot(snapshot Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	if snapshot.Schema != SnapshotSchema ||
		snapshot.FirmwareBuildID != m.identity.FirmwareBuildID ||
		snapshot.BoardID != m.identity.BoardID ||
		snapshot.PlatformID != m.identity.PlatformID ||
		snapshot.CPUIdentity != m.identity.CPU ||
		len(snapshot.CPU) == 0 || len(snapshot.Bus) == 0 || len(snapshot.Flash) == 0 {
		return ErrIncompatibleState
	}
	oldCPU, cpuSaveErr := m.backend.SaveContext()
	oldBus, busSaveErr := m.bus.SaveState()
	oldFlash, flashSaveErr := m.flash.SaveState()
	if cpuSaveErr != nil || busSaveErr != nil || flashSaveErr != nil {
		return fmt.Errorf("capture snapshot rollback state: %v %v %v", cpuSaveErr, busSaveErr, flashSaveErr)
	}
	oldPosition := Position{PC: m.pc, Mode: m.mode, Instructions: m.instructions}
	rollback := func() {
		_ = m.flash.LoadState(oldFlash)
		_ = m.bus.LoadState(oldBus)
		_ = m.backend.RestoreContext(oldCPU)
		m.pc, m.mode, m.instructions = oldPosition.PC, oldPosition.Mode, oldPosition.Instructions
	}
	if err := m.flash.LoadState(snapshot.Flash); err != nil {
		return fmt.Errorf("load snapshot flash: %w", err)
	}
	if err := m.bus.LoadState(snapshot.Bus); err != nil {
		rollback()
		return fmt.Errorf("load snapshot bus: %w", err)
	}
	if err := m.backend.RestoreContext(snapshot.CPU); err != nil {
		rollback()
		return fmt.Errorf("load snapshot CPU: %w", err)
	}
	pc, err := m.backend.ReadRegister(cpu.RegisterPC)
	if err != nil {
		rollback()
		return err
	}
	status, err := m.backend.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		rollback()
		return err
	}
	m.pc = pc
	m.mode = cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		m.mode = cpu.ModeThumb
	}
	m.instructions = snapshot.Instructions
	m.bootBoundaryLeft = 0
	if snapshot.Instructions < m.bootBoundary.instructions {
		m.bootBoundaryLeft = m.bootBoundary.instructions - snapshot.Instructions
	}
	return nil
}

func (m *Machine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return nil
	}
	m.closed.Store(true)
	return m.backend.Close()
}
