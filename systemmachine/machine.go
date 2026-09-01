// Package systemmachine composes firmware loaders, board profiles, CPU
// backends, and guest-neutral system devices into headless whole-phone
// machines. It deliberately sits above package system so generic buses and
// devices never need firmware-model checks.
package systemmachine

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"strings"
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
	samsungW320QCSBLUsedSize         = uint32(0x0000484f)
	samsungW320QCSBLLoadAddress      = uint32(0x00080000)
	samsungW320PBLVerifiedCopy       = uint32(0x01880000)
	samsungW320PBLVerifiedRecord     = uint32(0x0050aab6)
	samsungW320PBLVerifiedStatus     = uint32(0x0050aa70)
)

var (
	ErrClosed             = errors.New("system machine is closed")
	ErrIncompatibleMedia  = errors.New("media state is incompatible with the system machine")
	ErrIncompatibleState  = errors.New("snapshot is incompatible with the system machine")
	ErrUnsupportedBackend = errors.New("CPU backend lacks required whole-system capabilities")
	ErrUnsupportedControl = errors.New("system machine has no such input control")
	ErrUnsupportedMachine = errors.New("recognized firmware has no system machine")
)

// CPUBackendMode selects one of the portable interpreter execution tiers.
// An explicitly supplied Options.Backend remains available for research and
// third-party cores.
type CPUBackendMode string

const (
	CPUBackendPrecise CPUBackendMode = "precise"
	CPUBackendJIT     CPUBackendMode = "jit"
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
// selects BackendMode, with an empty mode retaining the precise interpreter.
// The constructed Machine owns and closes a supplied backend. Media, when
// supplied, is restored before the first instruction executes.
type Options struct {
	Backend       cpu.Backend
	BackendMode   CPUBackendMode
	RunnerQuantum uint64
	Media         *MediaState
}

// MediaState is the persistent NAND state which survives a power cycle. Flash
// and NAND retain the primary main/OOB state used by existing single-chip
// boards. SecondaryFlash and OneNANDSpare are populated by dual-flash boards.
// None of the fields contains the immutable user-supplied firmware pieces.
type MediaState struct {
	FirmwareBuildID string
	Flash           []byte
	NAND            []byte
	SecondaryFlash  []byte
	OneNANDSpare    []byte
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
	SecondaryFlash  []byte
	OneNANDSpare    []byte
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

	identity       Identity
	backend        cpu.Backend
	bus            *system.Bus
	runner         *system.ClockedRunner
	handoff        system.BootHandoff
	flash          *system.COWFlash
	secondaryFlash *system.COWFlash
	nand           *system.QualcommNAND
	oneNANDSpare   system.StatefulNANDSpareStorage
	panel          *system.DCSPanelController
	keypad         *system.QualcommGPIOKeypad
	primaryClock   *system.QualcommPrimaryClockControl
	primaryKeys    map[string]system.QualcommPrimaryClockKeyProfile
	audio          *schw830Audio
	controls       []string

	resetCPUState            []byte
	factoryNANDState         []byte
	factoryOneNANDSpareState []byte
	pc                       uint32
	mode                     cpu.Mode
	instructions             uint64
	bootBoundary             bootBoundary
	bootBoundaryLeft         uint64
	closed                   atomic.Bool
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
	case "SCH-W320", "SCH-W340", "SCH-W350", "SCH-W410", "SCH-W850":
		return newSamsungRawDownloadMachine(set, pkg, firmwareProfile, options)
	case "SCH-W770":
		return newSCHW770(set, pkg, firmwareProfile, options)
	case "SCH-W830":
		return newSCHW830(set, pkg, firmwareProfile, options)
	case "SCH-W860":
		return newSCHW860(set, pkg, firmwareProfile, options)
	default:
		return nil, fmt.Errorf("%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build)
	}
}

func newSamsungRawDownloadMachine(
	set firmwareset.Set,
	pkg samsung.Package,
	firmwareProfile samsung.BuildProfile,
	options Options,
) (*Machine, error) {
	var board system.BoardProfile
	switch firmwareProfile.ID {
	case samsung.SCHW320DC18ProfileID:
		board = system.SCHW320DC18BoardProfile()
	case samsung.SCHW340DC18ProfileID:
		board = system.SCHW340DC18BoardProfile()
	case samsung.SCHW350CK06ProfileID:
		board = system.SCHW350CK06BoardProfile()
	case samsung.SCHW410CL10ProfileID:
		board = system.SCHW410CL10BoardProfile()
	case samsung.SCHW850CF11ProfileID:
		board = system.SCHW850CF11BoardProfile()
	default:
		return nil, fmt.Errorf(
			"%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build,
		)
	}
	return newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{}, options)
}

// NewSCHW770 constructs the DA05 version-one MIBIB handset from its traced
// model-specific storage, display, GPIO, and keypad wiring.
func NewSCHW770(set firmwareset.Set, options Options) (*Machine, error) {
	pkg, err := samsung.Inspect(set)
	if err != nil {
		return nil, fmt.Errorf("inspect Samsung firmware set: %w", err)
	}
	firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		return nil, fmt.Errorf("select Samsung firmware build: %w", err)
	}
	if firmwareProfile.Model != "SCH-W770" {
		return nil, fmt.Errorf("%w: Samsung %s build %s", ErrUnsupportedMachine, firmwareProfile.Model, firmwareProfile.Build)
	}
	return newSCHW770(set, pkg, firmwareProfile, options)
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
	board := schw830BoardProfile(firmwareProfile.ID)
	return newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{
		name:         "SCH-W830 QCSBL callback",
		instructions: schw830QCSBLBoundaryInstructions,
		pc:           schw830QCSBLBoundaryPC,
	}, options)
}

func newSCHW770(
	set firmwareset.Set,
	pkg samsung.Package,
	firmwareProfile samsung.BuildProfile,
	options Options,
) (*Machine, error) {
	board := system.SCHW770DA05BoardProfile()
	board.FirmwareBuildID = firmwareProfile.ID
	return newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{}, options)
}

func schw830BoardProfile(firmwareBuildID string) system.BoardProfile {
	board := system.SCHW830DL21BoardProfile()
	board.FirmwareBuildID = firmwareBuildID
	if firmwareBuildID != samsung.SCHW830DL21ProfileID {
		board.BootControlSBIReadResponses = nil
	}
	return board
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
	var pblPreloadedBootImages []samsung.BootImage
	for _, spec := range firmwareProfile.BootImages {
		if !spec.PBLPreload {
			continue
		}
		image, reconstructErr := samsung.ReconstructBootImage(set, pkg, spec)
		if reconstructErr != nil {
			return nil, fmt.Errorf("reconstruct PBL-preloaded image %q: %w", spec.ID, reconstructErr)
		}
		pblPreloadedBootImages = append(pblPreloadedBootImages, image)
	}
	var pblROM samsung.MemoryImage
	if pblSpec, ok := firmwareProfile.MemoryImage("pbl-rom"); ok {
		pblROM, err = samsung.ReconstructMemoryImage(set, pkg, pblSpec)
		if err != nil {
			return nil, fmt.Errorf("reconstruct PBL ROM: %w", err)
		}
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
	var secondaryFlash *system.COWFlash
	if spec := board.OneNAND; spec != nil {
		secondaryFlash, err = system.NewErasedCOWFlash(
			spec.Capacity,
			samsung.EraseBlockSize,
			firmwareProfile.ID+":onenand",
		)
		if err != nil {
			return nil, fmt.Errorf("create %s OneNAND media: %w", firmwareProfile.Model, err)
		}
	}

	backend := options.Backend
	ownedBackend := false
	if backend == nil {
		var backendErr error
		backend, backendErr = newInterpreterBackend(options.BackendMode)
		if backendErr != nil {
			return nil, backendErr
		}
		ownedBackend = true
	} else if options.BackendMode != "" {
		return nil, errors.New("system machine options cannot select both Backend and BackendMode")
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

	legacyInterrupts, err := system.NewQualcommInterruptControllerWithConfig(
		system.QualcommInterruptControllerConfig{GPIOInputs: board.BootControlGPIOInputs},
		nil,
	)
	if err != nil {
		return fail(fmt.Errorf("create %s legacy interrupt/GPIO aperture: %w", firmwareProfile.Model, err))
	}
	vectoredInterrupts, err := system.NewQualcommVectoredInterruptController(
		*board.VectoredInterrupt,
		interruptSink,
	)
	if err != nil {
		return fail(fmt.Errorf("create %s vectored interrupt controller: %w", firmwareProfile.Model, err))
	}
	nandReady := system.NewStatusSignal()
	nandConfig := system.Qualcomm2K8BitNANDConfig(board.NANDReadID, nandReady)
	nandConfig.Capacity = board.NANDSize
	nandConfig.FactoryBadBlocks = append([]uint32(nil), board.NANDFactoryBadBlocks...)
	nandConfig.ReportErasedECCCodewords = board.NANDReportsErasedECCCodewords
	if nandConfig.PageSize != samsung.PageSize {
		return fail(fmt.Errorf("%s NAND page size does not match normalized flash", firmwareProfile.Model))
	}
	nand, err := system.NewQualcommNAND(flash, nandConfig)
	if err != nil {
		return fail(fmt.Errorf("create %s NAND controller: %w", firmwareProfile.Model, err))
	}
	var oneNAND *system.OneNAND
	var oneNANDSpare system.StatefulNANDSpareStorage
	if spec := board.OneNAND; spec != nil {
		spareConfig := system.Qualcomm2K8BitNANDConfig(
			uint32(spec.ManufacturerID)<<8|uint32(spec.DeviceID&0xff),
			system.NewStatusSignal(),
		)
		spareConfig.Capacity = spec.Capacity
		oneNANDSpare, err = system.NewQualcommNAND(secondaryFlash, spareConfig)
		if err != nil {
			return fail(fmt.Errorf("create %s OneNAND spare media: %w", firmwareProfile.Model, err))
		}
		oneNAND, err = system.NewOneNAND(system.OneNANDConfig{
			ManufacturerID: spec.ManufacturerID,
			DeviceID:       spec.DeviceID,
			VersionID:      spec.VersionID,
			TechnologyID:   spec.TechnologyID,
			DieBlockOffset: spec.DieBlockOffset,
			Capacity:       spec.Capacity,
			FlexGeometry:   spec.FlexGeometry,
			Storage:        secondaryFlash,
			Spare:          oneNANDSpare,
		})
		if err != nil {
			return fail(fmt.Errorf("create %s OneNAND: %w", firmwareProfile.Model, err))
		}
	}
	var sflashOneNAND *system.QualcommSFlashController
	if spec := board.SFlashOneNAND; spec != nil {
		if len(spec.SpareInitialData) != 0 {
			pageSize := uint32(0x0800)
			pagesPerEraseBlock := uint64(samsung.EraseBlockSize / pageSize)
			if spec.FlexGeometry != nil {
				pageSize = spec.FlexGeometry.PageSize
				pagesPerEraseBlock = uint64(spec.FlexGeometry.MLCBlockSize / pageSize)
			}
			sparePageSize := pageSize / 0x0200 * 0x0010
			oneNANDSpare, err = system.NewSparseNANDSpare(system.SparseNANDSpareConfig{
				PageSize:           sparePageSize,
				PageCount:          spec.Capacity / uint64(pageSize),
				PagesPerEraseBlock: pagesPerEraseBlock,
				Identity:           firmwareProfile.ID + ":sflash-onenand-spare",
				InitialData:        spec.SpareInitialData,
			})
			if err != nil {
				return fail(fmt.Errorf("create %s SFlash OneNAND spare media: %w", firmwareProfile.Model, err))
			}
		}
		target, targetErr := system.NewOneNAND(system.OneNANDConfig{
			ManufacturerID: spec.ManufacturerID,
			DeviceID:       spec.DeviceID,
			VersionID:      spec.VersionID,
			TechnologyID:   spec.TechnologyID,
			DieBlockOffset: spec.DieBlockOffset,
			Capacity:       spec.Capacity,
			FlexGeometry:   spec.FlexGeometry,
			Storage:        flash,
			Spare:          oneNANDSpare,
		})
		if targetErr != nil {
			return fail(fmt.Errorf("create %s SFlash OneNAND target: %w", firmwareProfile.Model, targetErr))
		}
		sflashOneNAND, err = system.NewQualcommSFlashController(target)
		if err != nil {
			return fail(fmt.Errorf("create %s SFlash OneNAND controller: %w", firmwareProfile.Model, err))
		}
	}
	factoryNANDState, err := nand.SaveState()
	if err != nil {
		return fail(fmt.Errorf("capture %s factory NAND state: %w", firmwareProfile.Model, err))
	}
	var factoryOneNANDSpareState []byte
	if oneNANDSpare != nil {
		factoryOneNANDSpareState, err = oneNANDSpare.SaveState()
		if err != nil {
			return fail(fmt.Errorf("capture %s factory OneNAND spare state: %w", firmwareProfile.Model, err))
		}
	}

	bootControl, err := system.NewQualcommBootControl(system.QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: board.BootClockModeStatus,
		WritableOffsets:                board.BootControlWritableOffsets,
		InterruptWindowWritableOffsets: board.BootControlInterruptWindowWritableOffsets,
		HalfwordOffsets:                board.BootControlHalfwordOffsets,
		MixedWidthOffsets:              board.BootControlMixedWidthOffsets,
		ReadOnlyRegisters:              board.BootControlReadOnlyRegisters,
		RegisterResets:                 board.BootControlRegisterResets,
		CompletionEvents:               board.BootControlCompletionEvents,
		LegacyUARTControllers:          board.BootControlLegacyUARTControllers,
		SBIControllers:                 board.BootControlSBIControllers,
		SBIReadResponses:               board.BootControlSBIReadResponses,
		SBICompletionStatus:            board.BootControlSBICompletionStatus,
		WatchdogServiceReadable:        board.BootControlWatchdogReadable,
		NANDReady:                      nandReady,
		InterruptController:            legacyInterrupts,
		VectoredInterruptController:    vectoredInterrupts,
		TimeTickClock:                  cloneTimeTickClock(board.TimeTickClock),
	})
	if err != nil {
		return fail(fmt.Errorf("create %s boot control: %w", firmwareProfile.Model, err))
	}
	secondaryClock, err := system.NewQualcommSecondaryClockControlWithConfig(
		system.QualcommSecondaryClockConfig{
			WritableOffsets:   board.SecondaryClockWritableOffsets,
			ReadOnlyRegisters: board.SecondaryClockReadOnlyRegisters,
		},
	)
	if err != nil {
		return fail(fmt.Errorf("create %s secondary clock: %w", firmwareProfile.Model, err))
	}
	primaryClock, err := system.NewQualcommPrimaryClockControl(system.QualcommPrimaryClockConfig{
		Status:          board.PrimaryClockStatus,
		InputMask:       board.PrimaryClockInputMask,
		WritableOffsets: board.PrimaryClockWritableOffsets,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s primary clock: %w", firmwareProfile.Model, err))
	}
	keypad, err := board.AttachKeypad(primaryClock, secondaryClock, legacyInterrupts)
	if err != nil {
		return fail(err)
	}
	if keypad != nil {
		if err := keypad.AttachInterruptControllers(legacyInterrupts, vectoredInterrupts); err != nil {
			return fail(fmt.Errorf("attach %s keypad interrupts: %w", firmwareProfile.Model, err))
		}
	}
	legacyTop, err := system.NewQualcommLegacyTopPageWithConfig(system.QualcommLegacyTopConfig{
		Version:         board.LegacyTopVersion,
		Identification:  board.LegacyTopIdentification,
		WritableOffsets: board.LegacyTopWritableOffsets,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s legacy top page: %w", firmwareProfile.Model, err))
	}
	clockRegime, err := system.NewQualcommClockRegimeWithConfig(system.QualcommClockRegimeConfig{
		SleepControllers:            board.ClockRegimeSleepControllers,
		Counters:                    board.ClockRegimeCounters,
		Comparators:                 board.ClockRegimeComparators,
		InterruptController:         legacyInterrupts,
		VectoredInterruptController: vectoredInterrupts,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s clock regime: %w", firmwareProfile.Model, err))
	}
	busRegisters, err := system.NewSparseWordRegistersWithConfig(system.SparseWordRegistersConfig{
		Offsets: board.SparseBusRegisterOffsets,
		Resets:  board.SparseBusRegisterResets,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s sparse bus registers: %w", firmwareProfile.Model, err))
	}
	dcsPanelController, err := system.NewDCSPanelController(board.Panel)
	if err != nil {
		return fail(fmt.Errorf("create %s panel controller: %w", firmwareProfile.Model, err))
	}
	panel, err := system.NewParallelPanelInterfaceWithController(dcsPanelController)
	if err != nil {
		return fail(fmt.Errorf("create %s panel transport: %w", firmwareProfile.Model, err))
	}
	pblServiceTableAddress := board.PBLServiceTableAddress
	if pblServiceTableAddress == 0 {
		pblServiceTableAddress = 0x78001000
	}
	handoff, err := system.NewQualcommNANDPBLHandoff(system.QualcommNANDPBLConfig{
		Entry: qcsbl.EntryAddress, TableAddress: pblServiceTableAddress,
		ServiceTableHeaderSize:   board.PBLServiceTableHeaderSize,
		HeaderFeatureDataAddress: board.PBLHeaderFeatureDataAddress,
		HeaderFeatures:           append([]system.QualcommPBLHeaderFeature(nil), board.PBLHeaderFeatures...),
		LegacyFeatureDataAddress: board.PBLLegacyFeatureDataAddress,
		SharedDataAddress:        board.PBLSharedDataAddress,
		SharedDataSize:           board.PBLSharedDataSize,
		PageSize:                 samsung.PageSize, EraseBlockSize: samsung.EraseBlockSize,
		FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s PBL handoff: %w", firmwareProfile.Model, err))
	}
	handoff.Memory = append(handoff.Memory, system.MemorySeed{
		Address: qcsbl.LoadAddress,
		Bytes:   append([]byte(nil), qcsbl.Bytes...),
	})
	if firmwareProfile.ID == samsung.SCHW320DC18ProfileID {
		if seedErr := appendSamsungW320VerifiedPBLState(&handoff, qcsbl); seedErr != nil {
			return fail(seedErr)
		}
	}
	for _, image := range pblPreloadedBootImages {
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: image.LoadAddress,
			Bytes:   append([]byte(nil), image.Bytes...),
		})
	}
	if len(pblROM.Bytes) != 0 {
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: pblROM.LoadAddress,
			Bytes:   append([]byte(nil), pblROM.Bytes...),
		})
	}

	bus := system.NewBus()
	var audio *schw830Audio
	if firmwareProfile.ID == samsung.SCHW830DL21ProfileID {
		instructionsPerSecond := schw830AudioInstructionsPerSecond
		if board.TimeTickClock != nil && board.TimeTickClock.InstructionsPerSecond != 0 {
			instructionsPerSecond = board.TimeTickClock.InstructionsPerSecond
		}
		audio, err = newSCHW830Audio(bus, defaultSCHW830AudioConfig(instructionsPerSecond))
		if err != nil {
			return fail(err)
		}
	}
	if _, err := board.AttachMDP(bus, dcsPanelController, bootControl); err != nil {
		return fail(err)
	}
	if err := mapSamsungQualcommBoard(
		bus,
		board,
		legacyInterrupts,
		vectoredInterrupts,
		bootControl,
		nand,
		oneNAND,
		sflashOneNAND,
		primaryClock,
		secondaryClock,
		panel,
		clockRegime,
		busRegisters,
		legacyTop,
		audio,
	); err != nil {
		return fail(err)
	}
	if err := backend.(cpu.SystemBusBackend).AttachSystemBus(bus); err != nil {
		return fail(fmt.Errorf("attach %s physical bus: %w", firmwareProfile.Model, err))
	}
	if err := handoff.Apply(bus, backend); err != nil {
		return fail(fmt.Errorf("apply %s PBL handoff: %w", firmwareProfile.Model, err))
	}
	resetCPUState, err := backend.SaveContext()
	if err != nil {
		return fail(fmt.Errorf("capture %s reset CPU state: %w", firmwareProfile.Model, err))
	}
	quantum := options.RunnerQuantum
	if quantum == 0 {
		quantum = system.DefaultClockedRunnerQuantum
	}
	clockedDevices := bus.ClockedDevices()
	if audio != nil {
		clockedDevices = append(clockedDevices, audio)
	}
	var executionRunner system.ExecutionRunner = backend
	if len(board.HLECalls) != 0 {
		hleRunner, hleErr := system.NewHLERunner(
			bus,
			backend,
			board.HLECalls,
			samsungQualcommHLEHandlers(),
		)
		if hleErr != nil {
			return fail(fmt.Errorf("configure %s HLE calls: %w", firmwareProfile.Model, hleErr))
		}
		executionRunner = hleRunner
	}
	runner, err := system.NewClockedRunner(
		backend,
		executionRunner,
		quantum,
		clockedDevices...,
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
		flash: flash, secondaryFlash: secondaryFlash, nand: nand,
		oneNANDSpare: oneNANDSpare,
		panel:        dcsPanelController, keypad: keypad,
		primaryClock: primaryClock, primaryKeys: boardPrimaryClockKeys(board), audio: audio,
		controls:                 boardControls(board),
		resetCPUState:            append([]byte(nil), resetCPUState...),
		factoryNANDState:         append([]byte(nil), factoryNANDState...),
		factoryOneNANDSpareState: append([]byte(nil), factoryOneNANDSpareState...),
		pc:                       handoff.Entry, mode: handoff.Mode,
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

func samsungQualcommHLEHandlers() map[string]system.HLECallHandler {
	return map[string]system.HLECallHandler{
		system.HLEContractQualcommPBLVerifiedLoaderState: system.HLECallHandlerFunc(
			restoreSamsungW320VerifiedPBLLoaderState,
		),
		system.HLEContractQualcommBootstrapVerifiedFirmware: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractQualcommResidentBootCallback: system.HLECallHandlerFunc(
			func(system.HLECallContext) error {
				return nil
			},
		),
	}
}

func restoreSamsungW320VerifiedPBLLoaderState(call system.HLECallContext) error {
	if call.CPU == nil {
		return fmt.Errorf("restore Samsung W320 verified PBL loader state: nil CPU")
	}
	qcsbl := make([]byte, samsungW320QCSBLUsedSize)
	if err := call.CPU.ReadMemory(samsungW320QCSBLLoadAddress, qcsbl); err != nil {
		return fmt.Errorf("read verified Samsung W320 QCSBL: %w", err)
	}
	if err := call.CPU.WriteMemory(samsungW320PBLVerifiedCopy, qcsbl); err != nil {
		return fmt.Errorf("restore Samsung W320 PBL QCSBL copy: %w", err)
	}
	record, err := samsungW320VerifiedPBLRecord(qcsbl)
	if err != nil {
		return err
	}
	if err := call.CPU.WriteMemory(samsungW320PBLVerifiedRecord, record); err != nil {
		return fmt.Errorf("restore Samsung W320 PBL verification record: %w", err)
	}
	if err := call.CPU.WriteMemory(samsungW320PBLVerifiedStatus, []byte{0}); err != nil {
		return fmt.Errorf("restore Samsung W320 PBL verification status: %w", err)
	}
	if err := call.CPU.WriteRegister(cpu.RegisterR0, 0x10); err != nil {
		return fmt.Errorf("restore Samsung W320 PBL loader result: %w", err)
	}
	return nil
}

func appendSamsungW320VerifiedPBLState(handoff *system.BootHandoff, qcsbl samsung.BootImage) error {
	if handoff == nil {
		return fmt.Errorf("restore Samsung W320 PBL state: nil handoff")
	}
	if qcsbl.LoadAddress != samsungW320QCSBLLoadAddress ||
		qcsbl.UsedSize != samsungW320QCSBLUsedSize ||
		uint64(qcsbl.UsedSize) > uint64(len(qcsbl.Bytes)) {
		return fmt.Errorf("restore Samsung W320 PBL state: invalid QCSBL geometry")
	}
	verifiedQCSBL := append([]byte(nil), qcsbl.Bytes[:qcsbl.UsedSize]...)
	record, err := samsungW320VerifiedPBLRecord(verifiedQCSBL)
	if err != nil {
		return err
	}
	handoff.Memory = append(
		handoff.Memory,
		system.MemorySeed{Address: samsungW320PBLVerifiedCopy, Bytes: verifiedQCSBL},
		system.MemorySeed{Address: samsungW320PBLVerifiedRecord, Bytes: record},
		system.MemorySeed{Address: samsungW320PBLVerifiedStatus, Bytes: []byte{0}},
	)
	return nil
}

func samsungW320VerifiedPBLRecord(qcsbl []byte) ([]byte, error) {
	if len(qcsbl) != int(samsungW320QCSBLUsedSize) {
		return nil, fmt.Errorf(
			"restore Samsung W320 verified PBL state: QCSBL size 0x%x, want 0x%x",
			len(qcsbl),
			samsungW320QCSBLUsedSize,
		)
	}
	digest := sha512.Sum512(qcsbl)
	record := make([]byte, 6+len(digest))
	binary.BigEndian.PutUint32(record, samsungW320QCSBLUsedSize)
	copy(record[6:], digest[:])
	return record, nil
}

func newInterpreterBackend(mode CPUBackendMode) (cpu.Backend, error) {
	switch mode {
	case "", CPUBackendPrecise:
		return interpreter.New(), nil
	case CPUBackendJIT:
		return interpreter.NewJIT(), nil
	default:
		return nil, fmt.Errorf("%w: CPU backend mode %q", ErrUnsupportedBackend, mode)
	}
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

func mapSamsungQualcommBoard(
	bus *system.Bus,
	board system.BoardProfile,
	legacyInterrupts *system.QualcommInterruptController,
	vectoredInterrupts *system.QualcommVectoredInterruptController,
	bootControl *system.QualcommBootControl,
	nand system.Device,
	oneNAND *system.OneNAND,
	sflashOneNAND *system.QualcommSFlashController,
	primaryClock *system.QualcommPrimaryClockControl,
	secondaryClock *system.QualcommSecondaryClockControl,
	panel *system.ParallelPanelInterface,
	clockRegime *system.QualcommClockRegime,
	busRegisters *system.SparseWordRegisters,
	legacyTop *system.QualcommLegacyTopPage,
	audio *schw830Audio,
) error {
	if err := board.ApplyMemory(bus); err != nil {
		return err
	}
	if _, err := board.AttachSamsungMGP(bus); err != nil {
		return err
	}
	if err := board.ApplyReadOnlyRegisters(bus); err != nil {
		return err
	}
	if audio != nil {
		var commandWindow *system.LatchedRegisterWindowProfile
		remaining := make([]system.LatchedRegisterWindowProfile, 0, len(board.LatchedRegisterWindows))
		for index := range board.LatchedRegisterWindows {
			spec := board.LatchedRegisterWindows[index]
			if spec.ID == schw830AudioCommandWindowID {
				copy := spec
				commandWindow = &copy
				continue
			}
			remaining = append(remaining, spec)
		}
		if commandWindow == nil {
			return fmt.Errorf("SCH-W830 board has no audio command window %q", schw830AudioCommandWindowID)
		}
		device, err := newSCHW830AudioCommandWindow(commandWindow.Size, commandWindow.Width, audio)
		if err != nil {
			return fmt.Errorf("create SCH-W830 audio command window: %w", err)
		}
		if err := bus.MapMMIO(commandWindow.ID, commandWindow.Address, commandWindow.Size, device); err != nil {
			return fmt.Errorf("map SCH-W830 audio command window: %w", err)
		}
		board.LatchedRegisterWindows = remaining
	}
	if err := board.ApplyLatchedRegistersWithInterrupts(bus, legacyInterrupts, vectoredInterrupts); err != nil {
		return err
	}
	bootControlAddress := board.BootControlAddress
	if bootControlAddress == 0 {
		bootControlAddress = 0x80000000
	}
	mappings := []struct {
		name    string
		address uint32
		size    uint32
		device  system.Device
	}{
		{"qualcomm-boot-control", bootControlAddress, system.QualcommBootControlWindowSize, bootControl},
		{"qualcomm-nand", 0x60000000, system.QualcommNANDWindowSize, nand},
		{"qualcomm-primary-clock", 0x84000000, system.QualcommPrimaryClockWindowSize, primaryClock},
		{"qualcomm-secondary-clock", 0x84004000, system.QualcommSecondaryClockWindowSize, secondaryClock},
		{"qualcomm-clock-regime", 0x90000000, system.QualcommClockRegimeWindowSize, clockRegime},
		{"qualcomm-sparse-bus-registers", 0x90400000, 0x1000, busRegisters},
		{"qualcomm-legacy-top-page", 0xfffff000, system.QualcommLegacyTopWindowSize, legacyTop},
	}
	for _, mapping := range mappings {
		if err := bus.MapMMIO(mapping.name, mapping.address, mapping.size, mapping.device); err != nil {
			return fmt.Errorf("map %s device %q: %w", board.ID, mapping.name, err)
		}
	}
	if board.PanelPorts == nil {
		if err := bus.MapMMIO(
			"parallel-panel",
			0x20000000,
			system.ParallelPanelWindowSize,
			panel,
		); err != nil {
			return fmt.Errorf("map %s parallel panel: %w", board.ID, err)
		}
	} else {
		if err := mapSparsePanelPorts(bus, "parallel-panel", *board.PanelPorts, panel); err != nil {
			return fmt.Errorf("map %s panel ports: %w", board.ID, err)
		}
	}
	for _, spec := range board.IndexedHalfwordRegisterPorts {
		if err := mapIndexedHalfwordRegisterPorts(bus, spec); err != nil {
			return fmt.Errorf("map %s indexed halfword ports: %w", board.ID, err)
		}
	}
	if oneNAND != nil {
		if err := bus.MapMMIO(
			"samsung-onenand",
			board.OneNAND.Address,
			system.OneNANDWindowSize,
			oneNAND,
		); err != nil {
			return fmt.Errorf("map %s OneNAND: %w", board.ID, err)
		}
	}
	if sflashOneNAND != nil {
		if err := bus.MapMMIO(
			"qualcomm-sflash-onenand",
			board.SFlashOneNAND.Address,
			system.QualcommSFlashWindowSize,
			sflashOneNAND,
		); err != nil {
			return fmt.Errorf("map %s Qualcomm SFlash OneNAND: %w", board.ID, err)
		}
	}
	return nil
}

func mapSparsePanelPorts(
	bus *system.Bus,
	name string,
	ports system.ParallelPanelPortProfile,
	panel *system.ParallelPanelInterface,
) error {
	commandPort, err := system.NewParallelPanelCommandPort(panel)
	if err != nil {
		return fmt.Errorf("create command port: %w", err)
	}
	dataPort, err := system.NewParallelPanelDataPort(panel)
	if err != nil {
		return fmt.Errorf("create data port: %w", err)
	}
	if err := bus.MapMMIO(
		name+"-command",
		ports.CommandAddress,
		uint32(system.Width16),
		commandPort,
	); err != nil {
		return err
	}
	return bus.MapMMIO(
		name+"-data",
		ports.DataAddress,
		uint32(system.Width16),
		dataPort,
	)
}

func mapIndexedHalfwordRegisterPorts(
	bus *system.Bus,
	ports system.IndexedHalfwordRegisterPortProfile,
) error {
	registers := system.NewIndexedHalfwordRegisters(ports.CommandReadValue)
	commandPort, err := system.NewIndexedHalfwordCommandPort(registers)
	if err != nil {
		return fmt.Errorf("create command port: %w", err)
	}
	dataPort, err := system.NewIndexedHalfwordDataPort(registers)
	if err != nil {
		return fmt.Errorf("create data port: %w", err)
	}
	if err := bus.MapMMIO(
		ports.ID+"-command",
		ports.CommandAddress,
		uint32(system.Width16),
		commandPort,
	); err != nil {
		return err
	}
	return bus.MapMMIO(
		ports.ID+"-data",
		ports.DataAddress,
		uint32(system.Width16),
		dataPort,
	)
}

func boardControls(board system.BoardProfile) []string {
	keypadCount := 0
	if board.Keypad != nil {
		keypadCount = len(board.Keypad.Keys)
	}
	controls := make([]string, 0, keypadCount+len(board.PrimaryClockKeys))
	if board.Keypad != nil {
		for _, key := range board.Keypad.Keys {
			controls = append(controls, key.ID)
		}
	}
	for _, key := range board.PrimaryClockKeys {
		controls = append(controls, key.ID)
	}
	return controls
}

func boardPrimaryClockKeys(board system.BoardProfile) map[string]system.QualcommPrimaryClockKeyProfile {
	if len(board.PrimaryClockKeys) == 0 {
		return nil
	}
	keys := make(map[string]system.QualcommPrimaryClockKeyProfile, len(board.PrimaryClockKeys))
	for _, key := range board.PrimaryClockKeys {
		keys[key.ID] = key
	}
	return keys
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
	if key, ok := m.primaryKeys[id]; ok {
		if m.primaryClock == nil {
			return ErrUnsupportedControl
		}
		high := pressed
		if key.ActiveLow {
			high = !pressed
		}
		return m.primaryClock.SetInputLine(key.InputLine, high)
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
	if m.oneNANDSpare != nil {
		if err := m.oneNANDSpare.Reset(); err != nil {
			return fmt.Errorf("reset OneNAND spare media: %w", err)
		}
	}
	if err := m.bus.Reset(); err != nil {
		return fmt.Errorf("reset system bus: %w", err)
	}
	if m.audio != nil {
		if err := m.audio.resetAtInstructions(0); err != nil {
			return err
		}
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
	var secondaryFlashState []byte
	if m.secondaryFlash != nil {
		secondaryFlashState, err = m.secondaryFlash.SaveState()
		if err != nil {
			return MediaState{}, fmt.Errorf("save secondary NAND main area: %w", err)
		}
	}
	var oneNANDSpareState []byte
	if m.oneNANDSpare != nil {
		oneNANDSpareState, err = m.oneNANDSpare.SaveState()
		if err != nil {
			return MediaState{}, fmt.Errorf("save OneNAND spare area: %w", err)
		}
	}
	return MediaState{
		FirmwareBuildID: m.identity.FirmwareBuildID,
		Flash:           append([]byte(nil), flashState...),
		NAND:            append([]byte(nil), nandState...),
		SecondaryFlash:  append([]byte(nil), secondaryFlashState...),
		OneNANDSpare:    append([]byte(nil), oneNANDSpareState...),
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
	if media.FirmwareBuildID != m.identity.FirmwareBuildID || len(media.Flash) == 0 || len(media.NAND) == 0 ||
		(m.secondaryFlash != nil) != (len(media.SecondaryFlash) != 0) ||
		(m.oneNANDSpare != nil) != (len(media.OneNANDSpare) != 0) {
		return ErrIncompatibleMedia
	}
	oldFlash, flashSaveErr := m.flash.SaveState()
	oldNAND, nandSaveErr := m.nand.SaveState()
	var oldSecondaryFlash, oldOneNANDSpare []byte
	var secondarySaveErr, oneNANDSpareSaveErr error
	if m.secondaryFlash != nil {
		oldSecondaryFlash, secondarySaveErr = m.secondaryFlash.SaveState()
	}
	if m.oneNANDSpare != nil {
		oldOneNANDSpare, oneNANDSpareSaveErr = m.oneNANDSpare.SaveState()
	}
	if flashSaveErr != nil || nandSaveErr != nil || secondarySaveErr != nil || oneNANDSpareSaveErr != nil {
		return fmt.Errorf("capture media rollback state: %v %v %v %v",
			flashSaveErr, nandSaveErr, secondarySaveErr, oneNANDSpareSaveErr)
	}
	rollback := func() {
		_ = m.flash.LoadState(oldFlash)
		_ = m.nand.LoadState(oldNAND)
		if m.secondaryFlash != nil {
			_ = m.secondaryFlash.LoadState(oldSecondaryFlash)
		}
		if m.oneNANDSpare != nil {
			_ = m.oneNANDSpare.LoadState(oldOneNANDSpare)
		}
	}
	if err := m.flash.LoadState(media.Flash); err != nil {
		return fmt.Errorf("load NAND main area: %w", err)
	}
	if m.secondaryFlash != nil {
		if err := m.secondaryFlash.LoadState(media.SecondaryFlash); err != nil {
			rollback()
			return fmt.Errorf("load secondary NAND main area: %w", err)
		}
	}
	if err := m.nand.LoadState(media.NAND); err != nil {
		rollback()
		return fmt.Errorf("load NAND spare area: %w", err)
	}
	if m.oneNANDSpare != nil {
		if err := m.oneNANDSpare.LoadState(media.OneNANDSpare); err != nil {
			rollback()
			return fmt.Errorf("load OneNAND spare area: %w", err)
		}
	}
	if err := m.nand.Reset(); err != nil {
		rollback()
		return fmt.Errorf("reset restored NAND controller: %w", err)
	}
	if m.oneNANDSpare != nil {
		if err := m.oneNANDSpare.Reset(); err != nil {
			rollback()
			return fmt.Errorf("reset restored OneNAND spare media: %w", err)
		}
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
	if m.secondaryFlash != nil {
		m.secondaryFlash.FactoryReset()
	}
	if err := m.nand.LoadState(m.factoryNANDState); err != nil {
		return fmt.Errorf("restore factory NAND spare state: %w", err)
	}
	if m.oneNANDSpare != nil {
		if err := m.oneNANDSpare.LoadState(m.factoryOneNANDSpareState); err != nil {
			return fmt.Errorf("restore factory OneNAND spare state: %w", err)
		}
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
	var secondaryFlashState, oneNANDSpareState []byte
	if m.secondaryFlash != nil {
		secondaryFlashState, err = m.secondaryFlash.SaveState()
		if err != nil {
			return Snapshot{}, err
		}
	}
	if m.oneNANDSpare != nil {
		oneNANDSpareState, err = m.oneNANDSpare.SaveState()
		if err != nil {
			return Snapshot{}, err
		}
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
		SecondaryFlash:  append([]byte(nil), secondaryFlashState...),
		OneNANDSpare:    append([]byte(nil), oneNANDSpareState...),
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
		!compatibleCPUContextIdentity(snapshot.CPUIdentity, m.identity.CPU) ||
		len(snapshot.CPU) == 0 || len(snapshot.Bus) == 0 || len(snapshot.Flash) == 0 ||
		(m.secondaryFlash != nil) != (len(snapshot.SecondaryFlash) != 0) ||
		(m.oneNANDSpare != nil) != (len(snapshot.OneNANDSpare) != 0) {
		return ErrIncompatibleState
	}
	oldCPU, cpuSaveErr := m.backend.SaveContext()
	oldBus, busSaveErr := m.bus.SaveState()
	oldFlash, flashSaveErr := m.flash.SaveState()
	var oldSecondaryFlash, oldOneNANDSpare []byte
	var secondarySaveErr, oneNANDSpareSaveErr error
	if m.secondaryFlash != nil {
		oldSecondaryFlash, secondarySaveErr = m.secondaryFlash.SaveState()
	}
	if m.oneNANDSpare != nil {
		oldOneNANDSpare, oneNANDSpareSaveErr = m.oneNANDSpare.SaveState()
	}
	if cpuSaveErr != nil || busSaveErr != nil || flashSaveErr != nil ||
		secondarySaveErr != nil || oneNANDSpareSaveErr != nil {
		return fmt.Errorf("capture snapshot rollback state: %v %v %v %v %v",
			cpuSaveErr, busSaveErr, flashSaveErr, secondarySaveErr, oneNANDSpareSaveErr)
	}
	oldPosition := Position{PC: m.pc, Mode: m.mode, Instructions: m.instructions}
	rollback := func() {
		_ = m.flash.LoadState(oldFlash)
		if m.secondaryFlash != nil {
			_ = m.secondaryFlash.LoadState(oldSecondaryFlash)
		}
		if m.oneNANDSpare != nil {
			_ = m.oneNANDSpare.LoadState(oldOneNANDSpare)
		}
		_ = m.bus.LoadState(oldBus)
		_ = m.backend.RestoreContext(oldCPU)
		m.pc, m.mode, m.instructions = oldPosition.PC, oldPosition.Mode, oldPosition.Instructions
	}
	if err := m.flash.LoadState(snapshot.Flash); err != nil {
		return fmt.Errorf("load snapshot flash: %w", err)
	}
	if m.secondaryFlash != nil {
		if err := m.secondaryFlash.LoadState(snapshot.SecondaryFlash); err != nil {
			rollback()
			return fmt.Errorf("load snapshot secondary flash: %w", err)
		}
	}
	if m.oneNANDSpare != nil {
		if err := m.oneNANDSpare.LoadState(snapshot.OneNANDSpare); err != nil {
			rollback()
			return fmt.Errorf("load snapshot OneNAND spare: %w", err)
		}
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
	if m.audio != nil {
		if err := m.audio.resetAtInstructions(snapshot.Instructions); err != nil {
			rollback()
			return err
		}
	}
	m.bootBoundaryLeft = 0
	if snapshot.Instructions < m.bootBoundary.instructions {
		m.bootBoundaryLeft = m.bootBoundary.instructions - snapshot.Instructions
	}
	return nil
}

func compatibleCPUContextIdentity(saved, active cpu.Identity) bool {
	if saved == active {
		return true
	}
	return saved.Architecture == active.Architecture &&
		saved.Version == active.Version &&
		strings.HasPrefix(saved.Name, interpreter.BackendName) &&
		strings.HasPrefix(active.Name, interpreter.BackendName)
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
