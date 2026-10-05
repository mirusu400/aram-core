package system

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

type MemoryKind string

const (
	MemoryRAM       MemoryKind = "ram"
	MemorySparseRAM MemoryKind = "sparse-ram"
)

type MemoryRegionProfile struct {
	ID      string
	Kind    MemoryKind
	Address uint32
	Size    uint32
}

// MemoryWriteResponseProfile models an exact coprocessor-owned shared-memory
// handshake. The guest's request is committed first and then replaced by the
// response value, matching a coprocessor which consumes the request before the
// host next polls it. Rules are deliberately scoped to one access width and one
// value so ordinary RAM traffic retains normal memory semantics.
type MemoryWriteResponseProfile struct {
	MemoryID string
	Offset   uint32
	Width    Width
	Request  uint32
	Writes   []MemoryResponseWriteProfile
}

type MemoryResponseWriteProfile struct {
	Offset uint32
	Width  Width
	Value  uint32
}

type ReadOnlyRegisterProfile struct {
	ID      string
	Address uint32
	Width   Width
	Value   uint32
}

type LatchedRegisterProfile struct {
	ID                  string
	Address             uint32
	Width               Width
	AdditionalWidths    []Width
	AllowSubwordOffsets bool
	ResetValue          uint32
	WritePulses         []LatchedRegisterWritePulseProfile
}

// LatchedRegisterWritePulseProfile raises one or more interrupt-controller
// status sources when a profiled command bit is written to a simple latch.
type LatchedRegisterWritePulseProfile struct {
	Mask                  uint32
	Value                 uint32
	Sources               []uint8
	UseVectoredController bool
}

type LatchedRegisterWindowProfile struct {
	ID      string
	Address uint32
	Size    uint32
	Width   Width
}

// AddressedStorageWindowProfile maps a bounded read aperture and an exact
// command register which selects its source range in an immutable image.
type AddressedStorageWindowProfile struct {
	ID             string
	Address        uint32
	Size           uint32
	CommandID      string
	CommandAddress uint32
	CommandWidth   Width
	AddressMask    uint32
	ResetCommand   uint32
}

func (p AddressedStorageWindowProfile) validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.CommandID) || p.ID == p.CommandID ||
		p.Size == 0 || p.Size&(p.Size-1) != 0 ||
		uint64(p.Address)+uint64(p.Size) > 1<<32 ||
		(p.CommandWidth != Width8 && p.CommandWidth != Width16 && p.CommandWidth != Width32) ||
		p.CommandAddress%uint32(p.CommandWidth) != 0 ||
		uint64(p.CommandAddress)+uint64(p.CommandWidth) > 1<<32 ||
		p.AddressMask&(p.Size-1) != 0 ||
		p.CommandWidth < Width32 &&
			(p.AddressMask >= uint32(1)<<(uint32(p.CommandWidth)*8) ||
				p.ResetCommand >= uint32(1)<<(uint32(p.CommandWidth)*8)) {
		return ErrInvalidRegion
	}
	dataEnd := uint64(p.Address) + uint64(p.Size)
	commandEnd := uint64(p.CommandAddress) + uint64(p.CommandWidth)
	if uint64(p.Address) < commandEnd && uint64(p.CommandAddress) < dataEnd {
		return ErrRegionOverlap
	}
	return nil
}

// SamsungMGPProfile locates Samsung's external ARM7/MGP control aperture and
// its ready flag inside a separately mapped shared-memory region.
type SamsungMGPProfile struct {
	ID                        string
	Address                   uint32
	Size                      uint32
	ReleaseOffset             uint32
	SharedMemoryID            string
	ReadyOffset               uint32
	ReadyValue                uint8
	ResponseDelayInstructions uint64
}

func (p SamsungMGPProfile) validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.SharedMemoryID) ||
		p.Address%uint32(Width16) != 0 ||
		uint64(p.Address)+uint64(p.Size) > 1<<32 {
		return fmt.Errorf("invalid Samsung MGP profile %q", p.ID)
	}
	return SamsungMGPControlConfig{
		Size:                      p.Size,
		ReleaseOffset:             p.ReleaseOffset,
		ReadyValue:                p.ReadyValue,
		ResponseDelayInstructions: p.ResponseDelayInstructions,
	}.validate()
}

type HLEReturn string

const (
	HLEReturnLinkRegister HLEReturn = "link-register"
	// HLEReturnNextInstruction resumes immediately after an inline firmware
	// boundary whose instruction has been fully reproduced by its handler.
	HLEReturnNextInstruction HLEReturn = "next-instruction"
	// HLEReturnProgramCounter resumes at the program counter selected by the
	// handler. The execution mode remains the mode of the trapped call.
	HLEReturnProgramCounter HLEReturn = "program-counter"
	// HLEReturnPowerCycle asks the whole-machine owner to reset volatile CPU and
	// device state while preserving guest-written persistent media.
	HLEReturnPowerCycle HLEReturn = "power-cycle"

	// HLEContractQualcommPBLVerifiedLoaderState restores the success result
	// produced by an unavailable mask-ROM PBL after it authenticates the exact
	// QCSBL image selected by the host loader.
	HLEContractQualcommPBLVerifiedLoaderState = "qualcomm.pbl.verified-loader-state-v1"
	// HLEContractQualcommPBLNANDBadBlock supplies the retained PBL callback
	// which classifies one physical NAND erase block.
	HLEContractQualcommPBLNANDBadBlock = "qualcomm.pbl.nand-bad-block-v1"
	// HLEContractQualcommPBLNANDRead supplies the retained PBL callback which
	// copies a bounded run of physical NAND pages into QCSBL memory.
	HLEContractQualcommPBLNANDRead = "qualcomm.pbl.nand-read-v1"
	// HLEContractQualcommPBLFatal identifies the retained PBL assertion sink.
	// The machine handler surfaces it as a fault instead of fabricating success.
	HLEContractQualcommPBLFatal = "qualcomm.pbl.fatal-v1"
	// HLEContractQualcommBootstrapVerifiedFirmware supplies the zero success
	// result of a bootstrap verifier whose per-handset manufacturing key is not
	// distributed in a firmware package. It is valid only for a build profile
	// that has already matched every package piece by SHA-256.
	HLEContractQualcommBootstrapVerifiedFirmware = "qualcomm.bootstrap.verified-firmware-v1"
	// HLEContractQualcommResidentBootCallback supplies the return boundary of
	// a boot-resident callback for which Qualcomm's progressive ELF carries no
	// packaged implementation. It is valid only for an exact, hash-matched
	// build whose call site ignores the result.
	HLEContractQualcommResidentBootCallback = "qualcomm.boot.resident-callback-v1"
	// HLEContractSamsungAMSSFlashEnvironment publishes a handset-specific flash
	// callback registry after the progressive loader has replaced AMSS data/BSS.
	HLEContractSamsungAMSSFlashEnvironment = "samsung.amss.flash-environment-v1"
	// HLEContractSamsungAMSSBulkZero accelerates an exact-build watchdog-aware
	// zero-fill helper without changing its architectural memory result.
	HLEContractSamsungAMSSBulkZero = "samsung.amss.bulk-zero-v1"
	// HLEContractSamsungW350StaticBSSZero accelerates CK06's table-driven
	// two-range zero-fill constructor without changing its memory result.
	HLEContractSamsungW350StaticBSSZero = "samsung.w350.static-bss-zero-v1"
	// HLEContractSamsungW350OperatorProvisioning supplies the retail operator
	// flag omitted from the downloadable CK06 package. The exact UI call asks
	// for NV item 0x1301 and consumes one output byte; zero is the provisioned
	// SKT state also retained by the sibling CL10 build.
	HLEContractSamsungW350OperatorProvisioning = "samsung.w350.operator-provisioning-v1"
	// HLEContractSamsungW340DOGStartAcknowledgement supplies DC18's missing
	// watchdog-task startup acknowledgement at the exact Main Task wait call.
	// The DOG task itself remains native and continues servicing its timers.
	HLEContractSamsungW340DOGStartAcknowledgement = "samsung.w340.dog-start-ack-v1"
	// HLEContractSamsungW340BCXFirmwareIdentity supplies the fixed identity
	// returned by the handset's external Bluetooth controller. The archived
	// application processor firmware contains the BCX client but not that
	// separate controller firmware, so its synchronous 0x0226 request otherwise
	// remains queued forever during Main Task startup.
	HLEContractSamsungW340BCXFirmwareIdentity = "samsung.w340.bcx-firmware-identity-v1"
	// HLEContractSamsungW340UIMClockConfiguration supplies the success result of
	// DC18's retained UIM clock-calibration boundary. The downloadable AMSS calls
	// into a platform routine whose implementation is not present in the archived
	// image; falling through its unresolved dispatch terminates the current task.
	HLEContractSamsungW340UIMClockConfiguration = "samsung.w340.uim-clock-configuration-v1"
	// HLEContractSamsungW340ImageResourceOffset publishes the FNT offsets of
	// DC18's shared UTF, dictionary, and img_out.bin resources. A factory-
	// provisioned handset resolves the paths through EFS; a freshly formatted
	// archive image retains the payloads in the FONT partition but has no EFS
	// aliases for those lookups.
	HLEContractSamsungW340ImageResourceOffset = "samsung.w340.image-resource-offset-v1"
	// HLEContractSamsungW340DisplayColor supplies AEEDisp's retained sixteen-entry
	// system palette. The downloadable display provider leaves its GetColor slot as
	// EUNSUPPORTED, while the retail loader normally supplies the black/white theme
	// values before the first screen clear.
	HLEContractSamsungW340DisplayColor = "samsung.w340.display-color-v1"
	// HLEContractSamsungW340FontMetrics supplies the retained default-font
	// metrics used by DC18's display object. The archived downloadable image
	// contains the display client but not the retail loader's font provider.
	HLEContractSamsungW340FontMetrics = "samsung.w340.font-metrics-v1"
	// HLEContractSamsungW340FontDraw implements the retained IFont drawing ABI.
	HLEContractSamsungW340FontDraw = "samsung.w340.font-draw-v1"
	// HLEContractSamsungW340FontMeasure implements the retained IFont string
	// measurement ABI used by AEEDisp's clipping and alignment path.
	HLEContractSamsungW340FontMeasure = "samsung.w340.font-measure-v1"
	// HLEContractSamsungW340FontInfo supplies the two signed 16-bit metrics
	// expected by AEEDisp's native GetFontMetrics and DrawText implementations.
	HLEContractSamsungW340FontInfo = "samsung.w340.font-info-v1"
	// HLEContractSamsungW340MGPFrameCounter restores the retail MDSP clock
	// service behind DC18's dynamically linked frame-counter veneer. The
	// archived AP/MGP pair publishes the live counter storage but omits the
	// loader-owned service implementation that advances and returns it.
	HLEContractSamsungW340MGPFrameCounter = "samsung.w340.mgp-frame-counter-v1"
	// HLEContractSamsungW340PointerAccessPolicy restores the BREW pointer
	// validation policy installed by DC18's retail loader. The downloadable
	// image retains the validator veneer and safe-copy clients, but not the
	// loader-owned protection-domain state used to approve mapped app memory.
	HLEContractSamsungW340PointerAccessPolicy = "samsung.w340.pointer-access-policy-v1"
	// HLEContractSamsungW340ConnectionManager returns the offline connection
	// manager retained by the retail UI loader. The downloadable image contains
	// AEE_GetConMgr and all callers, but its loader-owned singleton is absent.
	HLEContractSamsungW340ConnectionManager = "samsung.w340.connection-manager-v1"
	// HLEContractSamsungW340MainAppletLifecycle supplies the retained lifecycle
	// callback that drives class 0x01007002 from its constructed state into the
	// native idle UI. The archive contains the applet and both event handlers, but
	// the retail loader's two-function callback table is not part of the dump.
	HLEContractSamsungW340MainAppletLifecycle = "samsung.w340.main-applet-lifecycle-v1"
	// HLEContractSamsungW340IdleCarouselLifecycle restores the one-shot retail
	// loader trigger which constructs IdleApp's native five-panel carousel. The
	// archived image contains both the constructor and updater, but the external
	// owner pointer which normally reaches the constructor is not packaged.
	HLEContractSamsungW340IdleCarouselLifecycle = "samsung.w340.idle-carousel-lifecycle-v1"
	// HLEContractSamsungW340IdleAppletDependency supplies the retail-loader
	// service class 0x010127d6 required by IdleApp's SECIA initialiser.
	HLEContractSamsungW340IdleAppletDependency = "samsung.w340.idle-applet-dependency-v1"
	// HLEContractSamsungW340IdleSimMainTarget supplies the loader-owned lifecycle
	// target returned to IdleApp's SIM-main constructor. The archived class manager
	// accepts the request but cannot publish the retained interface pointer.
	HLEContractSamsungW340IdleSimMainTarget = "samsung.w340.idle-sim-main-target-v1"
	// HLEContractSamsungW340IdleSimMainActivation preserves the paired SIM-main
	// interface across the retained loader's temporary-result release. The native
	// release clears IdleApp's companion word immediately before it is consumed.
	HLEContractSamsungW340IdleSimMainActivation = "samsung.w340.idle-sim-main-activation-v1"
	// HLEContractSamsungW340AnnunciatorStart supplies the retained annunciator
	// object's quiescent lifecycle method. Its object is present in the archive,
	// but the loader-owned first vtable slot is absent.
	HLEContractSamsungW340AnnunciatorStart = "samsung.w340.annunciator-start-v1"
	// HLEContractSamsungW340IdleExtendedProvider supplies the retained extended
	// provider used by class 0x0100638f during IdleApp's final initialisation.
	HLEContractSamsungW340IdleExtendedProvider = "samsung.w340.idle-extended-provider-v1"
	// HLEContractSamsungW340IdlePrimaryNotification reconnects the retained
	// foreground notification to IdleApp's primary display path. The archived
	// loader delivers the secondary notification record but omits the retail
	// bridge which classifies that record for the foreground applet.
	HLEContractSamsungW340IdlePrimaryNotification = "samsung.w340.idle-primary-notification-v1"
	// HLEContractSamsungW340StartupPrimaryInterface supplies the primary startup
	// interface returned by class 0x0100638f operation 0xb. The archived loader
	// completes the request without publishing its retained output pointer.
	HLEContractSamsungW340StartupPrimaryInterface = "samsung.w340.startup-primary-interface-v1"
	// HLEContractSamsungW340StartupSecondaryInterface supplies the paired startup
	// interface returned by class 0x0100638f operation 0x13.
	HLEContractSamsungW340StartupSecondaryInterface = "samsung.w340.startup-secondary-interface-v1"
	// HLEContractSamsungPowerCycle marks an exact firmware instruction boundary
	// which requests a hardware restart after committing persistent state.
	HLEContractSamsungPowerCycle = "samsung.power-cycle-v1"
	// HLEContractSamsungW340SBITransaction supplies the successful completion of
	// DC18's retained serial-bus dispatcher. The downloadable image contains the
	// dispatcher, but its board-driver registry is owned by the absent retail
	// loader and is cleared by the final progressive ELF BSS segment.
	HLEContractSamsungW340SBITransaction = "samsung.w340.sbi-transaction-v1"
	// HLEContractSamsungW340PMICADCConversion supplies a deterministic idle
	// sample for DC18's synchronous PMIC ADC client. The PMIC conversion
	// interrupt is generated by hardware outside the archived firmware set.
	HLEContractSamsungW340PMICADCConversion = "samsung.w340.pmic-adc-conversion-v1"
	// HLEContractSamsungW340RFSettledDeferred preserves the radio initialiser's
	// computed state while deferring its final settled marker. On retail hardware
	// the absent modem companion clears that provisional marker before the UI
	// startup state machine resumes.
	HLEContractSamsungW340RFSettledDeferred = "samsung.w340.rf-settled-deferred-v1"
	// HLEContractSamsungProgressiveAMSSLoad reproduces an exact-build OEMSBL
	// boundary which copies mapped PT_LOAD segments from NAND into EBI RAM.
	HLEContractSamsungProgressiveAMSSLoad   = "samsung.amss.progressive-load-v1"
	HLEContractSamsungSharedDirectoryInit   = "samsung.amss.shared-directory-init-v1"
	HLEContractSamsungSharedDirectoryAttach = "samsung.amss.shared-directory-attach-v1"
	// HLEContractSamsungW4200PBLFlashPrepare restores the PBL-owned flash
	// interface pointer after DC17 has cleared its second EBI bank. The matching
	// read contract copies one physical OneNAND page for QCSBL's legacy callback.
	HLEContractSamsungW4200PBLFlashPrepare = "samsung.w4200.pbl-flash-prepare-v1"
	HLEContractSamsungW4200PBLFlashRead    = "samsung.w4200.pbl-flash-read-v1"
	HLEContractSamsungW4200TSC2007Write    = "samsung.w4200.tsc2007-write-v1"
	HLEContractSamsungW4200TSC2007Read     = "samsung.w4200.tsc2007-read-v1"
	// HLEContractSamsungOptionalPreloadFile reports an absent optional factory
	// preload to exact firmware which otherwise dereferences a nil file handle.
	// It substitutes no file contents and preserves the routine's documented
	// failure return so the caller can take its ordinary empty-state path.
	HLEContractSamsungOptionalPreloadFile = "samsung.amss.optional-preload-file-v1"
)

type HLECallProfile struct {
	ID       string
	Contract string
	Address  uint32
	Mode     cpu.Mode
	Return   HLEReturn
}

type OneNANDProfile struct {
	Address                  uint32
	ManufacturerID           uint16
	DeviceID                 uint16
	VersionID                uint16
	TechnologyID             uint16
	DieBlockOffset           uint32
	Capacity                 uint64
	FlexGeometry             *OneNANDFlexGeometry
	InitialImageFromFirmware bool
	InterruptSource          uint8
}

// QualcommSFlashOneNANDProfile describes a OneNAND device reached through the
// MSM7K SFlash controller instead of a directly mapped external-bus aperture.
type QualcommSFlashOneNANDProfile struct {
	Address          uint32
	ManufacturerID   uint16
	DeviceID         uint16
	VersionID        uint16
	TechnologyID     uint16
	DieBlockOffset   uint32
	Capacity         uint64
	FlexGeometry     *OneNANDFlexGeometry
	SpareInitialData []FlashSeed
}

type ParallelPanelPortProfile struct {
	CommandAddress uint32
	DataAddress    uint32
	// AliasSpan is the number of consecutive byte addresses routed to each
	// port when low external-bus address lines are not decoded. Zero selects
	// one exact 16-bit address per port.
	AliasSpan uint32
}

// ParallelPanelSelectorPortProfile describes an indirect 16-bit panel bus:
// SelectorAddress chooses command or data, then TransferAddress emits the
// selected word to the panel controller.
type ParallelPanelSelectorPortProfile struct {
	SelectorAddress uint32
	TransferAddress uint32
	CommandSelect   uint16
	DataSelect      uint16
}

// IndexedHalfwordRegisterPortProfile maps a pair of sparse 16-bit external-bus
// ports. Writing the command port selects a register; the data port then reads
// or writes the selected register. CommandReadValue is the board-observed
// status value returned by reads from the command port.
type IndexedHalfwordRegisterPortProfile struct {
	ID               string
	CommandAddress   uint32
	DataAddress      uint32
	CommandReadValue uint16
}

func (p IndexedHalfwordRegisterPortProfile) validate() error {
	if !validProfileID(p.ID) ||
		p.CommandAddress%uint32(Width16) != 0 || p.DataAddress%uint32(Width16) != 0 ||
		p.CommandAddress == p.DataAddress ||
		uint64(p.CommandAddress)+uint64(Width16) > 1<<32 ||
		uint64(p.DataAddress)+uint64(Width16) > 1<<32 {
		return ErrInvalidRegion
	}
	return nil
}

func (p ParallelPanelPortProfile) validate() error {
	if p.CommandAddress%uint32(Width16) != 0 || p.DataAddress%uint32(Width16) != 0 ||
		p.CommandAddress == p.DataAddress || uint64(p.CommandAddress)+uint64(Width16) > 1<<32 ||
		uint64(p.DataAddress)+uint64(Width16) > 1<<32 {
		return ErrInvalidRegion
	}
	if p.AliasSpan != 0 && (p.AliasSpan < uint32(Width16) ||
		p.AliasSpan&(p.AliasSpan-1) != 0 ||
		p.CommandAddress%p.AliasSpan != 0 ||
		p.DataAddress != p.CommandAddress+p.AliasSpan ||
		uint64(p.DataAddress)+uint64(p.AliasSpan) > 1<<32) {
		return ErrInvalidRegion
	}
	return nil
}

func (p ParallelPanelSelectorPortProfile) validate() error {
	if p.SelectorAddress%uint32(Width16) != 0 || p.TransferAddress%uint32(Width16) != 0 ||
		p.SelectorAddress == p.TransferAddress || p.CommandSelect == p.DataSelect ||
		uint64(p.SelectorAddress)+uint64(Width16) > 1<<32 ||
		uint64(p.TransferAddress)+uint64(Width16) > 1<<32 {
		return ErrInvalidRegion
	}
	return nil
}

func (p OneNANDProfile) validate() error {
	geometry, geometryErr := normalizeOneNANDGeometry(p.Capacity, p.FlexGeometry)
	if p.Address%OneNANDWindowSize != 0 ||
		uint64(p.Address)+OneNANDWindowSize > 1<<32 ||
		p.ManufacturerID == 0 || p.DeviceID == 0 ||
		geometryErr != nil ||
		p.DieBlockOffset != 0 && (p.DieBlockOffset&(p.DieBlockOffset-1) != 0 ||
			p.DieBlockOffset >= geometry.blockCount) {
		return ErrInvalidOneNAND
	}
	return nil
}

func (p QualcommSFlashOneNANDProfile) validate() error {
	geometry, geometryErr := normalizeOneNANDGeometry(p.Capacity, p.FlexGeometry)
	if p.Address%QualcommSFlashWindowSize != 0 ||
		uint64(p.Address)+QualcommSFlashWindowSize > 1<<32 ||
		p.ManufacturerID == 0 || p.DeviceID == 0 ||
		geometryErr != nil ||
		p.DieBlockOffset != 0 && (p.DieBlockOffset&(p.DieBlockOffset-1) != 0 ||
			p.DieBlockOffset >= geometry.blockCount) {
		return ErrInvalidOneNAND
	}
	sparePageSize := geometry.pageSize / oneNANDSectorSize * oneNANDSpareSectorSize
	spareCapacity := p.Capacity / uint64(geometry.pageSize) * uint64(sparePageSize)
	initialData := append([]FlashSeed(nil), p.SpareInitialData...)
	sort.Slice(initialData, func(left, right int) bool {
		return initialData[left].Offset < initialData[right].Offset
	})
	for index, seed := range initialData {
		if len(seed.Data) == 0 || seed.Offset >= spareCapacity ||
			uint64(len(seed.Data)) > spareCapacity-seed.Offset {
			return ErrInvalidNANDSpare
		}
		if index > 0 {
			previous := initialData[index-1]
			if previous.Offset+uint64(len(previous.Data)) > seed.Offset {
				return ErrInvalidNANDSpare
			}
		}
	}
	return nil
}

// QualcommPrimaryClockKeyProfile maps a host control to one raw digital input
// exposed by the Qualcomm primary-clock GPIO status register.
type QualcommPrimaryClockKeyProfile struct {
	ID        string
	InputLine uint8
	ActiveLow bool
}

func (p HLECallProfile) validate() error {
	trap := cpu.ExecutionTrap{Address: p.Address, Mode: p.Mode}
	if !validProfileID(p.ID) || !validProfileID(p.Contract) || !trap.Valid() ||
		(p.Return != HLEReturnLinkRegister && p.Return != HLEReturnNextInstruction &&
			p.Return != HLEReturnProgramCounter && p.Return != HLEReturnPowerCycle) {
		return fmt.Errorf("invalid HLE call profile %q", p.ID)
	}
	return nil
}

type BoardProfile struct {
	ID                            string
	PlatformID                    string
	FirmwareBuildID               string
	CPUCompatibility              ARMCPUCompatibilityProfile
	NANDReadID                    uint32
	NANDRegisterResets            []QualcommNANDRegisterReset
	NANDSize                      uint64
	NANDPageSize                  uint32
	NANDEraseBlockSize            uint32
	NANDFactoryBadBlocks          []uint32
	NANDReportsErasedECCCodewords bool
	NANDInitialData               []FlashSeed
	OneNAND                       *OneNANDProfile
	SFlashOneNAND                 *QualcommSFlashOneNANDProfile
	// PBLServiceTableAddress overrides the legacy MSM6xxx PBL-owned IRAM
	// address used for the r8 service table. Zero keeps the legacy default.
	PBLServiceTableAddress uint32
	// PBLServiceTableHeaderSize selects the first service-entry offset for the
	// profiled PBL ABI. Zero keeps the legacy 0x2c-byte header.
	PBLServiceTableHeaderSize uint32
	// PBLHeaderFeatureDataAddress supplies the separate r9 feature-slot table
	// used by newer PBL ABIs. Zero omits that table.
	PBLHeaderFeatureDataAddress uint32
	PBLHeaderFeatures           []QualcommPBLHeaderFeature
	// PBLFixedFeatureDataAddress supplies a generation-specific fixed
	// presence/value table whose selector range is profiled below.
	PBLFixedFeatureDataAddress  uint32
	PBLFixedFeatureFirst        uint32
	PBLFixedFeatureSlotCount    uint32
	PBLFixedFeatures            []QualcommPBLFixedFeature
	PBLLegacyFeatureDataAddress uint32
	PBLSharedDataAddress        uint32
	PBLSharedDataSize           uint32
	// PBLStackPointer retains the initial stack supplied by mask ROM for
	// QCSBL entrypoints which begin as ordinary functions. Zero omits it.
	PBLStackPointer     uint32
	BootClockModeStatus uint32
	// BootControlAddress overrides the MSM6xxx default 0x80000000 physical
	// base for chip families which relocate the same bounded control device.
	BootControlAddress                        uint32
	PrimaryClockStatus                        uint32
	PrimaryClockInputMask                     uint32
	PrimaryClockKeys                          []QualcommPrimaryClockKeyProfile
	BootControlWritableOffsets                []uint32
	BootControlInterruptWindowWritableOffsets []uint32
	BootControlHalfwordOffsets                []uint32
	BootControlMixedWidthOffsets              []uint32
	BootControlByteWritableOffsets            []uint32
	BootControlReadOnlyRegisters              []QualcommBootReadOnlyRegister
	BootControlRegisterResets                 []QualcommBootRegisterReset
	BootControlCompletionEvents               []QualcommCompletionEventConfig
	BootControlLegacyUARTControllers          []uint32
	BootControlLegacyUARTReceiveData          []QualcommLegacyUARTReceiveData
	BootControlSBIControllers                 []uint32
	BootControlSBIReadResponses               []QualcommSBIReadResponse
	BootControlSBICompletionStatus            uint32
	BootControlSDCCControllers                []QualcommSDCCControllerConfig
	BootControlGroupedStatusResponses         []QualcommBootGroupedStatusResponse
	BootControlWatchdogReadable               bool
	BootControlGPIOInputs                     []QualcommGPIOInputRegister
	BootControlInterruptStatusAliases         []QualcommInterruptStatusAlias
	PrimaryClockWritableOffsets               []uint32
	PrimaryClockReadOnlyRegisters             []QualcommPrimaryClockReadOnlyRegister
	PrimaryClockInterruptRegisters            []QualcommPrimaryClockInterruptRegister
	SecondaryClockWritableOffsets             []uint32
	SecondaryClockReadOnlyRegisters           []QualcommSecondaryClockReadOnlyRegister
	SparseBusRegisterOffsets                  []uint32
	SparseBusRegisterResets                   []SparseWordRegisterReset
	SparseBusRegisterReadClearOffsets         []uint32
	ClockRegimeSleepControllers               []uint32
	ClockRegimeCounters                       []QualcommClockRegimeCounterConfig
	ClockRegimeComparators                    []QualcommClockRegimeComparatorConfig
	VectoredInterrupt                         *QualcommVectoredInterruptConfig
	LegacyInterruptCascade                    *QualcommInterruptCascadeProfile
	TimeTickClock                             *QualcommTimeTickClockConfig
	Keypad                                    *QualcommGPIOKeypadProfile
	Touchscreen                               *QualcommTSC2007Profile
	Panel                                     DCSPanelConfig
	PanelPorts                                *ParallelPanelPortProfile
	PanelSelectorPorts                        *ParallelPanelSelectorPortProfile
	IndexedHalfwordRegisterPorts              []IndexedHalfwordRegisterPortProfile
	MDP                                       *QualcommMDPProfile
	LegacyTopVersion                          uint32
	LegacyTopIdentification                   uint32
	LegacyTopWritableOffsets                  []uint32
	LegacyTopVectoredInterruptOffset          uint32
	Memory                                    []MemoryRegionProfile
	MemoryWriteResponses                      []MemoryWriteResponseProfile
	ReadOnlyRegisters                         []ReadOnlyRegisterProfile
	LatchedRegisters                          []LatchedRegisterProfile
	LatchedRegisterWindows                    []LatchedRegisterWindowProfile
	AddressedStorageWindows                   []AddressedStorageWindowProfile
	SamsungMGP                                *SamsungMGPProfile
	ADSPMailbox                               *QualcommADSPMailboxProfile
	HLECalls                                  []HLECallProfile
}

// QualcommInterruptCascadeProfile routes the enabled output of the legacy QIC
// into the compact VIC. A zero GroupMask connects it directly to VectoredSource;
// otherwise GroupStatusOffset and GroupMask identify the second-level child bit
// which the first-level ISR demultiplexes.
type QualcommInterruptCascadeProfile struct {
	VectoredSource    uint8
	GroupStatusOffset uint32
	GroupMask         uint32
	// SubInterruptWindowID identifies an optional external status bank between
	// the legacy QIC and the compact VIC. The cascade drives its profiled child
	// bit together with VectoredSource.
	SubInterruptWindowID     string
	SubInterruptStatusOffset uint32
	SubInterruptMask         uint32
}

// ARMCPUCompatibilityProfile records board-selected behavior for instructions
// whose architecture defines no stable result. The zero value is precise.
type ARMCPUCompatibilityProfile struct {
	UserSystemSPSRReadAsCPSR bool
}

func (p BoardProfile) Validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.PlatformID) || !validProfileID(p.FirmwareBuildID) {
		return fmt.Errorf("system board profile identity is invalid")
	}
	if p.PBLStackPointer&3 != 0 {
		return fmt.Errorf("board profile %q has unaligned PBL stack pointer 0x%x", p.ID, p.PBLStackPointer)
	}
	if p.NANDSize == 0 {
		if p.NANDPageSize != 0 || p.NANDEraseBlockSize != 0 {
			return fmt.Errorf("board profile %q has NAND geometry without capacity", p.ID)
		}
	} else if p.NANDPageSize < 0x200 || p.NANDPageSize > 16<<10 ||
		p.NANDPageSize&(p.NANDPageSize-1) != 0 ||
		p.NANDEraseBlockSize < p.NANDPageSize ||
		p.NANDEraseBlockSize%p.NANDPageSize != 0 ||
		p.NANDEraseBlockSize&(p.NANDEraseBlockSize-1) != 0 ||
		p.NANDSize%uint64(p.NANDEraseBlockSize) != 0 || p.NANDSize > uint64(1<<63-1) {
		return fmt.Errorf(
			"board profile %q has invalid NAND geometry size=0x%x page=0x%x erase=0x%x",
			p.ID, p.NANDSize, p.NANDPageSize, p.NANDEraseBlockSize,
		)
	}
	if err := validateQualcommNANDRegisterResets(p.NANDRegisterResets); err != nil {
		return fmt.Errorf("board profile %q NAND register profile: %w", p.ID, err)
	}
	if _, err := validateQualcommLegacyTopWritableOffsets(p.LegacyTopWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q legacy top page: %w", p.ID, err)
	}
	if p.LegacyTopVectoredInterruptOffset != 0 &&
		(p.VectoredInterrupt == nil ||
			p.LegacyTopVectoredInterruptOffset%4 != 0 ||
			p.LegacyTopVectoredInterruptOffset+QualcommVectoredInterruptControllerWindowSize > QualcommLegacyTopWindowSize) {
		return fmt.Errorf(
			"board profile %q has invalid legacy top compact-VIC alias offset 0x%x",
			p.ID,
			p.LegacyTopVectoredInterruptOffset,
		)
	}
	badBlocks := make(map[uint32]struct{}, len(p.NANDFactoryBadBlocks))
	for _, block := range p.NANDFactoryBadBlocks {
		if p.NANDSize == 0 || uint64(block) >= p.NANDSize/uint64(p.NANDEraseBlockSize) {
			return fmt.Errorf("board profile %q has invalid NAND factory bad block 0x%x", p.ID, block)
		}
		if _, duplicate := badBlocks[block]; duplicate {
			return fmt.Errorf("board profile %q repeats NAND factory bad block 0x%x", p.ID, block)
		}
		badBlocks[block] = struct{}{}
	}
	initialData := append([]FlashSeed(nil), p.NANDInitialData...)
	sort.Slice(initialData, func(left, right int) bool {
		return initialData[left].Offset < initialData[right].Offset
	})
	for index, seed := range initialData {
		if p.NANDSize == 0 || len(seed.Data) == 0 || seed.Offset >= p.NANDSize ||
			uint64(len(seed.Data)) > p.NANDSize-seed.Offset {
			return fmt.Errorf("board profile %q has invalid NAND initial data at 0x%x", p.ID, seed.Offset)
		}
		if index > 0 {
			previous := initialData[index-1]
			if previous.Offset+uint64(len(previous.Data)) > seed.Offset {
				return fmt.Errorf("board profile %q has overlapping NAND initial data", p.ID)
			}
		}
	}
	if p.OneNAND != nil {
		if err := p.OneNAND.validate(); err != nil {
			return fmt.Errorf("board profile %q OneNAND: %w", p.ID, err)
		}
		if p.OneNAND.InterruptSource != 0 &&
			(p.VectoredInterrupt == nil ||
				p.OneNAND.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q OneNAND interrupt source %d exceeds vectored controller",
				p.ID,
				p.OneNAND.InterruptSource,
			)
		}
	}
	if p.SFlashOneNAND != nil {
		if p.OneNAND != nil {
			return fmt.Errorf("board profile %q configures two OneNAND interfaces: %w", p.ID, ErrInvalidOneNAND)
		}
		if err := p.SFlashOneNAND.validate(); err != nil {
			return fmt.Errorf("board profile %q Qualcomm SFlash OneNAND: %w", p.ID, err)
		}
	}
	if mailbox := p.ADSPMailbox; mailbox != nil {
		if err := mailbox.validate(); err != nil {
			return fmt.Errorf("board profile %q ADSP mailbox: %w", p.ID, err)
		}
		if periodic := mailbox.PeriodicInterrupt; periodic != nil &&
			periodic.Interrupt.UseVectoredController &&
			(p.VectoredInterrupt == nil ||
				periodic.Interrupt.Source >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q ADSP periodic interrupt source %d exceeds vectored controller",
				p.ID,
				periodic.Interrupt.Source,
			)
		}
	}
	primaryClockInputMask := p.PrimaryClockInputMask
	if primaryClockInputMask == 0 {
		primaryClockInputMask = qualcommPrimaryGPIOInputMask
	}
	if p.PrimaryClockStatus&^primaryClockInputMask != 0 {
		return fmt.Errorf("board profile %q has invalid primary clock status 0x%x", p.ID, p.PrimaryClockStatus)
	}
	primaryClockKeyIDs := make(map[string]struct{}, len(p.PrimaryClockKeys))
	primaryClockKeyLines := make(map[uint8]struct{}, len(p.PrimaryClockKeys))
	for _, key := range p.PrimaryClockKeys {
		if !validProfileID(key.ID) || key.InputLine >= 32 {
			return fmt.Errorf("board profile %q has invalid primary-clock key %q", p.ID, key.ID)
		}
		lineMask := uint32(1) << key.InputLine
		idleHigh := p.PrimaryClockStatus&lineMask != 0
		if lineMask&primaryClockInputMask == 0 || idleHigh != key.ActiveLow {
			return fmt.Errorf("board profile %q has invalid primary-clock key %q", p.ID, key.ID)
		}
		if _, duplicate := primaryClockKeyIDs[key.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats primary-clock key ID %q", p.ID, key.ID)
		}
		if _, duplicate := primaryClockKeyLines[key.InputLine]; duplicate {
			return fmt.Errorf("board profile %q repeats primary-clock key input line %d", p.ID, key.InputLine)
		}
		primaryClockKeyIDs[key.ID] = struct{}{}
		primaryClockKeyLines[key.InputLine] = struct{}{}
	}
	if p.BootClockModeStatus&^uint32(0x11) != 0 {
		return fmt.Errorf("board profile %q has invalid boot clock mode status 0x%x", p.ID, p.BootClockModeStatus)
	}
	if p.BootControlAddress != 0 &&
		(p.BootControlAddress%QualcommBootControlWindowSize != 0 ||
			uint64(p.BootControlAddress)+QualcommBootControlWindowSize > 1<<32) {
		return fmt.Errorf("board profile %q has invalid boot-control address 0x%x", p.ID, p.BootControlAddress)
	}
	if err := validateQualcommBootControlConfigurationOffsets(
		p.BootControlWritableOffsets,
		p.BootControlInterruptWindowWritableOffsets,
		p.BootControlHalfwordOffsets,
		p.BootControlMixedWidthOffsets,
		p.BootControlByteWritableOffsets,
		p.BootControlReadOnlyRegisters,
		p.BootControlRegisterResets,
		p.BootControlCompletionEvents,
		p.BootControlLegacyUARTControllers,
		p.BootControlLegacyUARTReceiveData,
		p.BootControlSBIControllers,
		p.BootControlSBIReadResponses,
		p.BootControlSBICompletionStatus,
		p.BootControlSDCCControllers,
	); err != nil {
		return fmt.Errorf("board profile %q boot-control register profile: %w", p.ID, err)
	}
	if err := validateQualcommInterruptControllerConfig(QualcommInterruptControllerConfig{
		GPIOInputs:    p.BootControlGPIOInputs,
		StatusAliases: p.BootControlInterruptStatusAliases,
	}); err != nil {
		return fmt.Errorf("board profile %q boot-control interrupt inputs: %w", p.ID, err)
	}
	if err := validateQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q primary-clock writable offsets: %w", p.ID, err)
	}
	if err := validateQualcommPrimaryClockReadOnlyRegisters(
		p.PrimaryClockWritableOffsets,
		p.PrimaryClockReadOnlyRegisters,
	); err != nil {
		return fmt.Errorf("board profile %q primary-clock read-only registers: %w", p.ID, err)
	}
	if err := validateQualcommPrimaryClockInterruptRegisters(
		p.PrimaryClockWritableOffsets,
		p.PrimaryClockReadOnlyRegisters,
		p.PrimaryClockInterruptRegisters,
	); err != nil {
		return fmt.Errorf("board profile %q primary-clock interrupt registers: %w", p.ID, err)
	}
	if err := validateQualcommSecondaryClockConfig(QualcommSecondaryClockConfig{
		WritableOffsets:   p.SecondaryClockWritableOffsets,
		ReadOnlyRegisters: p.SecondaryClockReadOnlyRegisters,
	}); err != nil {
		return fmt.Errorf("board profile %q secondary-clock registers: %w", p.ID, err)
	}
	if len(p.SparseBusRegisterOffsets) != 0 || len(p.SparseBusRegisterResets) != 0 ||
		len(p.SparseBusRegisterReadClearOffsets) != 0 {
		if _, err := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
			Offsets:          p.SparseBusRegisterOffsets,
			Resets:           p.SparseBusRegisterResets,
			ReadClearOffsets: p.SparseBusRegisterReadClearOffsets,
		}); err != nil {
			return fmt.Errorf("board profile %q sparse bus registers: %w", p.ID, err)
		}
	}
	if keypad := p.Keypad; keypad != nil {
		if err := keypad.validate(); err != nil {
			return fmt.Errorf("board profile %q keypad: %w", p.ID, err)
		}
		var keypadInputMask uint32
		for _, line := range keypad.Columns {
			keypadInputMask |= uint32(1) << line
		}
		if keypadInputMask&^primaryClockInputMask != 0 {
			return fmt.Errorf("board profile %q keypad columns exceed primary-clock inputs", p.ID)
		}
		for _, key := range keypad.Keys {
			if _, duplicate := primaryClockKeyIDs[key.ID]; duplicate {
				return fmt.Errorf("board profile %q repeats control ID %q", p.ID, key.ID)
			}
		}
		for line := range primaryClockKeyLines {
			if keypadInputMask&(uint32(1)<<line) != 0 {
				return fmt.Errorf("board profile %q shares primary-clock input line %d with its keypad", p.ID, line)
			}
		}
		secondaryOffsets := make(map[uint32]struct{}, len(qualcommSecondaryClockOffsets)+len(p.SecondaryClockWritableOffsets))
		for _, offset := range qualcommSecondaryClockOffsets {
			secondaryOffsets[offset] = struct{}{}
		}
		for _, offset := range p.SecondaryClockWritableOffsets {
			secondaryOffsets[offset] = struct{}{}
		}
		for _, row := range keypad.Rows {
			if row.OutputBank == QualcommGPIOOutputSecondaryClock {
				if _, writable := secondaryOffsets[row.OutputOffset]; !writable {
					return fmt.Errorf(
						"board profile %q keypad row uses unwritable secondary-clock offset 0x%x",
						p.ID, row.OutputOffset,
					)
				}
			}
		}
		primaryWritable := make(map[uint32]struct{}, len(qualcommPrimaryClockWritableOffsets)+len(p.PrimaryClockWritableOffsets))
		for _, offset := range mergedQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets) {
			primaryWritable[offset] = struct{}{}
		}
		for index, group := range keypad.InterruptGroups {
			for _, offset := range []uint32{
				group.ClearOffset,
				group.EnableOffset,
				group.DetectOffset,
				group.PolarityOffset,
			} {
				if _, writable := primaryWritable[offset]; !writable {
					return fmt.Errorf(
						"board profile %q keypad interrupt group %d uses unwritable primary-clock offset 0x%x",
						p.ID, index, offset,
					)
				}
			}
			if _, writable := primaryWritable[group.StatusOffset]; writable ||
				group.StatusOffset == qualcommPrimaryGPIOInputOffset {
				return fmt.Errorf(
					"board profile %q keypad interrupt group %d has invalid status offset 0x%x",
					p.ID, index, group.StatusOffset,
				)
			}
			if group.UseVectoredController {
				if p.VectoredInterrupt == nil || group.InterruptSource >= p.VectoredInterrupt.SourceCount {
					return fmt.Errorf(
						"board profile %q keypad interrupt group %d source %d exceeds vectored controller",
						p.ID, index, group.InterruptSource,
					)
				}
			} else if group.InterruptSource >= 64 {
				return fmt.Errorf(
					"board profile %q keypad interrupt group %d source %d exceeds legacy controller",
					p.ID, index, group.InterruptSource,
				)
			}
		}
	}
	if touchscreen := p.Touchscreen; touchscreen != nil {
		if p.Keypad != nil {
			return fmt.Errorf("board profile %q has both keypad and touchscreen GPIO devices", p.ID)
		}
		if err := touchscreen.validate(); err != nil {
			return fmt.Errorf("board profile %q touchscreen: %w", p.ID, err)
		}
		primaryWritable := make(map[uint32]struct{}, len(qualcommPrimaryClockWritableOffsets)+len(p.PrimaryClockWritableOffsets))
		for _, offset := range mergedQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets) {
			primaryWritable[offset] = struct{}{}
		}
		group := touchscreen.InterruptGroup
		for _, offset := range []uint32{
			group.ClearOffset, group.EnableOffset, group.DetectOffset, group.PolarityOffset,
		} {
			if _, writable := primaryWritable[offset]; !writable {
				return fmt.Errorf(
					"board profile %q touchscreen uses unwritable primary-clock offset 0x%x",
					p.ID, offset,
				)
			}
		}
		if _, writable := primaryWritable[group.StatusOffset]; writable ||
			group.StatusOffset == qualcommPrimaryGPIOInputOffset {
			return fmt.Errorf(
				"board profile %q touchscreen has invalid status offset 0x%x",
				p.ID, group.StatusOffset,
			)
		}
		if group.UseVectoredController &&
			(p.VectoredInterrupt == nil || group.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q touchscreen source %d exceeds vectored controller",
				p.ID, group.InterruptSource,
			)
		}
	}
	if p.Panel.Width != 0 || p.Panel.Height != 0 {
		if _, err := validateDCSPanelConfig(p.Panel); err != nil {
			return fmt.Errorf("board profile %q panel: %w", p.ID, err)
		}
	}
	if p.PanelPorts != nil {
		if p.Panel.Width == 0 || p.Panel.Height == 0 {
			return fmt.Errorf("board profile %q sparse panel ports have no panel", p.ID)
		}
		if err := p.PanelPorts.validate(); err != nil {
			return fmt.Errorf("board profile %q sparse panel ports: %w", p.ID, err)
		}
	}
	if p.PanelSelectorPorts != nil {
		if p.PanelPorts != nil || p.Panel.Width == 0 || p.Panel.Height == 0 {
			return fmt.Errorf("board profile %q selector panel ports conflict or have no panel", p.ID)
		}
		if err := p.PanelSelectorPorts.validate(); err != nil {
			return fmt.Errorf("board profile %q selector panel ports: %w", p.ID, err)
		}
	}
	indexedPortIDs := make(map[string]struct{}, len(p.IndexedHalfwordRegisterPorts))
	indexedPortAddresses := make(map[uint32]struct{}, len(p.IndexedHalfwordRegisterPorts)*2)
	for _, ports := range p.IndexedHalfwordRegisterPorts {
		if err := ports.validate(); err != nil {
			return fmt.Errorf("board profile %q indexed halfword ports %q: %w", p.ID, ports.ID, err)
		}
		if _, duplicate := indexedPortIDs[ports.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats indexed halfword port ID %q", p.ID, ports.ID)
		}
		indexedPortIDs[ports.ID] = struct{}{}
		for _, address := range []uint32{ports.CommandAddress, ports.DataAddress} {
			if _, duplicate := indexedPortAddresses[address]; duplicate {
				return fmt.Errorf("board profile %q repeats indexed halfword port 0x%08x", p.ID, address)
			}
			indexedPortAddresses[address] = struct{}{}
		}
	}
	if mdp := p.MDP; mdp != nil {
		if err := mdp.validate(); err != nil {
			return fmt.Errorf("board profile %q MDP: %w", p.ID, err)
		}
		if p.Panel.Width == 0 || p.Panel.Height == 0 {
			return fmt.Errorf("board profile %q MDP has no panel", p.ID)
		}
		completionFound := false
		for _, event := range p.BootControlCompletionEvents {
			if event.StartOffset == mdp.CompletionStartOffset {
				completionFound = true
				break
			}
		}
		if !completionFound {
			return fmt.Errorf(
				"board profile %q MDP completion start 0x%x is not profiled",
				p.ID,
				mdp.CompletionStartOffset,
			)
		}
		scriptPointerFound := false
		for _, offset := range p.BootControlWritableOffsets {
			if offset == mdp.ScriptPointerOffset {
				scriptPointerFound = true
				break
			}
		}
		if !scriptPointerFound {
			return fmt.Errorf(
				"board profile %q MDP script-pointer register 0x%x is not writable",
				p.ID,
				mdp.ScriptPointerOffset,
			)
		}
		for _, offset := range p.BootControlHalfwordOffsets {
			if offset == mdp.ScriptPointerOffset {
				return fmt.Errorf(
					"board profile %q MDP script-pointer register 0x%x is not 32-bit",
					p.ID,
					mdp.ScriptPointerOffset,
				)
			}
		}
	}
	if p.VectoredInterrupt != nil {
		if err := p.VectoredInterrupt.validate(); err != nil {
			return fmt.Errorf("board profile %q vectored interrupt controller: %w", p.ID, err)
		}
	}
	if err := validateQualcommBootGroupedStatusResponses(
		p.BootControlGroupedStatusResponses,
		p.BootControlWritableOffsets,
	); err != nil {
		return fmt.Errorf("board profile %q boot-control grouped-status responses: %w", p.ID, err)
	}
	for _, response := range p.BootControlGroupedStatusResponses {
		if p.VectoredInterrupt == nil {
			return fmt.Errorf("board profile %q grouped-status response has no vectored controller", p.ID)
		}
		matched := false
		for index := uint8(0); index < p.VectoredInterrupt.GroupCount; index++ {
			group := p.VectoredInterrupt.Groups[index]
			if group.StatusOffset == response.GroupStatusOffset &&
				response.GroupMask&^group.ValidMask == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("board profile %q grouped-status response has no matching vectored group", p.ID)
		}
	}
	for _, controller := range p.BootControlSDCCControllers {
		if controller.GroupStatusOffset == 0 {
			continue
		}
		if p.VectoredInterrupt == nil {
			return fmt.Errorf("board profile %q SDCC absent-card interrupt has no vectored controller", p.ID)
		}
		matched := false
		for index := uint8(0); index < p.VectoredInterrupt.GroupCount; index++ {
			group := p.VectoredInterrupt.Groups[index]
			if group.StatusOffset == controller.GroupStatusOffset &&
				controller.GroupMask&^group.ValidMask == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("board profile %q SDCC absent-card interrupt has no matching vectored group", p.ID)
		}
	}
	if cascade := p.LegacyInterruptCascade; cascade != nil {
		if p.VectoredInterrupt == nil ||
			cascade.VectoredSource >= p.VectoredInterrupt.SourceCount ||
			(cascade.GroupMask == 0 && cascade.GroupStatusOffset != 0) ||
			(cascade.GroupMask != 0 && cascade.SubInterruptWindowID != "") ||
			(cascade.SubInterruptWindowID == "" &&
				(cascade.SubInterruptStatusOffset != 0 || cascade.SubInterruptMask != 0)) {
			return fmt.Errorf("board profile %q has invalid legacy interrupt cascade", p.ID)
		}
		if cascade.GroupMask != 0 {
			matched := false
			for index := uint8(0); index < p.VectoredInterrupt.GroupCount; index++ {
				group := p.VectoredInterrupt.Groups[index]
				if group.StatusOffset == cascade.GroupStatusOffset &&
					group.Source == cascade.VectoredSource &&
					cascade.GroupMask&^group.ValidMask == 0 {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("board profile %q legacy interrupt cascade has no matching vectored group", p.ID)
			}
		}
		if cascade.SubInterruptWindowID != "" {
			matched := false
			for _, window := range p.LatchedRegisterWindows {
				if window.ID != cascade.SubInterruptWindowID {
					continue
				}
				if window.Width != Width8 && window.Width != Width16 && window.Width != Width32 {
					break
				}
				bits := uint32(window.Width) * 8
				matched = cascade.SubInterruptMask != 0 &&
					cascade.SubInterruptStatusOffset%uint32(window.Width) == 0 &&
					uint64(cascade.SubInterruptStatusOffset)+uint64(window.Width) <= uint64(window.Size) &&
					(bits == 32 || cascade.SubInterruptMask < uint32(1)<<bits)
				break
			}
			if !matched {
				return fmt.Errorf("board profile %q legacy interrupt cascade has no matching sub-interrupt window", p.ID)
			}
		}
	}
	if clock := p.TimeTickClock; clock != nil {
		const maximumClockHz = uint64(1) << 48
		if clock.InstructionsPerSecond == 0 || clock.TimeTickHz == 0 ||
			clock.InstructionsPerSecond > maximumClockHz ||
			clock.TimeTickHz > clock.InstructionsPerSecond ||
			clock.PeriodicInterruptHz > clock.InstructionsPerSecond ||
			clock.InterruptSource >= 64 {
			return fmt.Errorf("board profile %q has invalid timetick clock", p.ID)
		}
		if clock.UseVectoredController &&
			(p.VectoredInterrupt == nil || clock.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q timetick interrupt source %d exceeds vectored controller",
				p.ID,
				clock.InterruptSource,
			)
		}
	}
	for _, event := range p.BootControlCompletionEvents {
		if event.UseVectoredController {
			if p.VectoredInterrupt == nil || event.InterruptSource >= p.VectoredInterrupt.SourceCount {
				return fmt.Errorf(
					"board profile %q completion interrupt source %d exceeds vectored controller",
					p.ID,
					event.InterruptSource,
				)
			}
		} else if event.InterruptSource >= 64 {
			return fmt.Errorf(
				"board profile %q has invalid completion interrupt source %d",
				p.ID,
				event.InterruptSource,
			)
		}
	}
	if err := validateQualcommClockRegimeConfig(QualcommClockRegimeConfig{
		SleepControllers: p.ClockRegimeSleepControllers,
		Counters:         p.ClockRegimeCounters,
		Comparators:      p.ClockRegimeComparators,
	}); err != nil {
		return fmt.Errorf("board profile %q clock-regime profile: %w", p.ID, err)
	}
	for _, comparator := range p.ClockRegimeComparators {
		if comparator.UseVectoredController &&
			(p.VectoredInterrupt == nil || comparator.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q clock-regime comparator interrupt source %d exceeds vectored controller",
				p.ID,
				comparator.InterruptSource,
			)
		}
	}
	memory := append([]MemoryRegionProfile(nil), p.Memory...)
	memoryByID := make(map[string]MemoryRegionProfile, len(memory))
	for _, region := range memory {
		if !validProfileID(region.ID) ||
			(region.Kind != MemoryRAM && region.Kind != MemorySparseRAM) || region.Size == 0 ||
			uint64(region.Address)+uint64(region.Size) > 1<<32 {
			return fmt.Errorf("board profile %q has invalid memory region %q", p.ID, region.ID)
		}
		if _, duplicate := memoryByID[region.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats memory region %q", p.ID, region.ID)
		}
		memoryByID[region.ID] = region
	}
	type memoryResponseKey struct {
		memoryID string
		offset   uint32
		width    Width
		request  uint32
	}
	responses := make(map[memoryResponseKey]struct{}, len(p.MemoryWriteResponses))
	for _, response := range p.MemoryWriteResponses {
		region, known := memoryByID[response.MemoryID]
		if !known || !validProfileID(response.MemoryID) ||
			(response.Width != Width8 && response.Width != Width16 && response.Width != Width32) ||
			response.Offset%uint32(response.Width) != 0 ||
			uint64(response.Offset)+uint64(response.Width) > uint64(region.Size) ||
			response.Width < Width32 && response.Request >= uint32(1)<<(uint32(response.Width)*8) ||
			len(response.Writes) == 0 {
			return fmt.Errorf("board profile %q has invalid memory response in %q", p.ID, response.MemoryID)
		}
		for _, write := range response.Writes {
			if (write.Width != Width8 && write.Width != Width16 && write.Width != Width32) ||
				write.Offset%uint32(write.Width) != 0 ||
				uint64(write.Offset)+uint64(write.Width) > uint64(region.Size) ||
				write.Width < Width32 && write.Value >= uint32(1)<<(uint32(write.Width)*8) {
				return fmt.Errorf("board profile %q has invalid memory response write in %q", p.ID, response.MemoryID)
			}
		}
		key := memoryResponseKey{response.MemoryID, response.Offset, response.Width, response.Request}
		if _, duplicate := responses[key]; duplicate {
			return fmt.Errorf("board profile %q repeats memory response in %q", p.ID, response.MemoryID)
		}
		responses[key] = struct{}{}
	}
	sort.Slice(memory, func(i, j int) bool { return memory[i].Address < memory[j].Address })
	for index := 1; index < len(memory); index++ {
		previousEnd := uint64(memory[index-1].Address) + uint64(memory[index-1].Size)
		if previousEnd > uint64(memory[index].Address) {
			return fmt.Errorf(
				"board profile %q memory regions %q and %q overlap",
				p.ID,
				memory[index-1].ID,
				memory[index].ID,
			)
		}
	}
	var mgpSharedMemory *MemoryRegionProfile
	if mgp := p.SamsungMGP; mgp != nil {
		if err := mgp.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
		for index := range memory {
			region := &memory[index]
			if region.ID == mgp.SharedMemoryID {
				if mgpSharedMemory != nil {
					return fmt.Errorf("board profile %q repeats MGP shared memory %q", p.ID, region.ID)
				}
				mgpSharedMemory = region
			}
			regionEnd := uint64(region.Address) + uint64(region.Size)
			if uint64(mgp.Address) < regionEnd && uint64(region.Address) < mgpEnd {
				return fmt.Errorf("board profile %q regions %q and %q overlap", p.ID, mgp.ID, region.ID)
			}
		}
		if mgpSharedMemory == nil || mgp.ID == mgp.SharedMemoryID ||
			mgp.ReadyOffset >= mgpSharedMemory.Size {
			return fmt.Errorf("board profile %q has invalid MGP shared-memory target", p.ID)
		}
	}
	registers := append([]ReadOnlyRegisterProfile(nil), p.ReadOnlyRegisters...)
	for _, register := range registers {
		if !validProfileID(register.ID) ||
			(register.Width != Width8 && register.Width != Width16 && register.Width != Width32) ||
			register.Address%uint32(register.Width) != 0 ||
			uint64(register.Address)+uint64(register.Width) > 1<<32 ||
			(register.Width < Width32 && register.Value >= uint32(1)<<(uint32(register.Width)*8)) {
			return fmt.Errorf("board profile %q has invalid read-only register %q", p.ID, register.ID)
		}
	}
	sort.Slice(registers, func(i, j int) bool { return registers[i].Address < registers[j].Address })
	for index := 1; index < len(registers); index++ {
		previousEnd := uint64(registers[index-1].Address) + uint64(registers[index-1].Width)
		if previousEnd > uint64(registers[index].Address) {
			return fmt.Errorf(
				"board profile %q read-only registers %q and %q overlap",
				p.ID,
				registers[index-1].ID,
				registers[index].ID,
			)
		}
	}
	latchedRegisters := append([]LatchedRegisterProfile(nil), p.LatchedRegisters...)
	for _, register := range latchedRegisters {
		if !validProfileID(register.ID) ||
			(register.Width != Width8 && register.Width != Width16 && register.Width != Width32) ||
			register.Address%uint32(register.Width) != 0 ||
			uint64(register.Address)+uint64(register.Width) > 1<<32 ||
			(register.Width < Width32 && register.ResetValue >= uint32(1)<<(uint32(register.Width)*8)) {
			return fmt.Errorf("board profile %q has invalid latched register %q", p.ID, register.ID)
		}
		additionalWidths := make(map[Width]struct{}, len(register.AdditionalWidths))
		for _, width := range register.AdditionalWidths {
			if width != Width8 && width != Width16 && width != Width32 ||
				width >= register.Width || register.Address%uint32(width) != 0 {
				return fmt.Errorf("board profile %q has invalid mixed width for latched register %q", p.ID, register.ID)
			}
			if _, duplicate := additionalWidths[width]; duplicate {
				return fmt.Errorf("board profile %q repeats mixed width for latched register %q", p.ID, register.ID)
			}
			additionalWidths[width] = struct{}{}
		}
		if len(register.AdditionalWidths) != 0 && len(register.WritePulses) != 0 {
			return fmt.Errorf("board profile %q mixes pulse and multi-width latched register %q", p.ID, register.ID)
		}
		if register.AllowSubwordOffsets && len(register.AdditionalWidths) == 0 {
			return fmt.Errorf("board profile %q enables subword offsets without mixed widths for latched register %q", p.ID, register.ID)
		}
		pulseKeys := make(map[[2]uint32]struct{}, len(register.WritePulses))
		for _, pulse := range register.WritePulses {
			if pulse.Mask == 0 || pulse.Value&^pulse.Mask != 0 || len(pulse.Sources) == 0 ||
				register.Width < Width32 && pulse.Mask >= uint32(1)<<(uint32(register.Width)*8) {
				return fmt.Errorf("board profile %q has invalid latched-register pulse %q", p.ID, register.ID)
			}
			key := [2]uint32{pulse.Mask, pulse.Value}
			if _, duplicate := pulseKeys[key]; duplicate {
				return fmt.Errorf("board profile %q repeats latched-register pulse rule %q", p.ID, register.ID)
			}
			pulseKeys[key] = struct{}{}
			seenSources := make(map[uint8]struct{}, len(pulse.Sources))
			for _, source := range pulse.Sources {
				if pulse.UseVectoredController {
					if p.VectoredInterrupt == nil || source >= p.VectoredInterrupt.SourceCount {
						return fmt.Errorf("board profile %q has invalid latched-register pulse source %d", p.ID, source)
					}
				} else if source >= 64 {
					return fmt.Errorf("board profile %q has invalid latched-register pulse source %d", p.ID, source)
				}
				if _, duplicate := seenSources[source]; duplicate {
					return fmt.Errorf("board profile %q repeats latched-register pulse source %d", p.ID, source)
				}
				seenSources[source] = struct{}{}
			}
		}
	}
	sort.Slice(latchedRegisters, func(i, j int) bool {
		return latchedRegisters[i].Address < latchedRegisters[j].Address
	})
	for index := 1; index < len(latchedRegisters); index++ {
		previousEnd := uint64(latchedRegisters[index-1].Address) + uint64(latchedRegisters[index-1].Width)
		if previousEnd > uint64(latchedRegisters[index].Address) {
			return fmt.Errorf(
				"board profile %q latched registers %q and %q overlap",
				p.ID,
				latchedRegisters[index-1].ID,
				latchedRegisters[index].ID,
			)
		}
	}
	for _, readOnly := range registers {
		readOnlyEnd := uint64(readOnly.Address) + uint64(readOnly.Width)
		for _, latched := range latchedRegisters {
			latchedEnd := uint64(latched.Address) + uint64(latched.Width)
			if uint64(readOnly.Address) < latchedEnd && uint64(latched.Address) < readOnlyEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					readOnly.ID,
					latched.ID,
				)
			}
		}
	}
	latchedWindows := append([]LatchedRegisterWindowProfile(nil), p.LatchedRegisterWindows...)
	for _, window := range latchedWindows {
		if !validProfileID(window.ID) ||
			(window.Width != Width8 && window.Width != Width16 && window.Width != Width32) ||
			window.Size == 0 || window.Address%uint32(window.Width) != 0 ||
			window.Size%uint32(window.Width) != 0 ||
			uint64(window.Address)+uint64(window.Size) > 1<<32 {
			return fmt.Errorf("board profile %q has invalid latched register window %q", p.ID, window.ID)
		}
	}
	sort.Slice(latchedWindows, func(i, j int) bool {
		return latchedWindows[i].Address < latchedWindows[j].Address
	})
	for index := 1; index < len(latchedWindows); index++ {
		previousEnd := uint64(latchedWindows[index-1].Address) + uint64(latchedWindows[index-1].Size)
		if previousEnd > uint64(latchedWindows[index].Address) {
			return fmt.Errorf(
				"board profile %q latched register windows %q and %q overlap",
				p.ID,
				latchedWindows[index-1].ID,
				latchedWindows[index].ID,
			)
		}
	}
	for _, window := range latchedWindows {
		windowEnd := uint64(window.Address) + uint64(window.Size)
		for _, readOnly := range registers {
			readOnlyEnd := uint64(readOnly.Address) + uint64(readOnly.Width)
			if uint64(window.Address) < readOnlyEnd && uint64(readOnly.Address) < windowEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					window.ID,
					readOnly.ID,
				)
			}
		}
		for _, latched := range latchedRegisters {
			latchedEnd := uint64(latched.Address) + uint64(latched.Width)
			if uint64(window.Address) < latchedEnd && uint64(latched.Address) < windowEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					window.ID,
					latched.ID,
				)
			}
		}
	}
	type addressedMapping struct {
		id      string
		address uint32
		size    uint32
	}
	addressedMappings := make([]addressedMapping, 0, len(p.AddressedStorageWindows)*2)
	addressedIDs := make(map[string]struct{}, len(p.AddressedStorageWindows)*2)
	for _, window := range p.AddressedStorageWindows {
		if err := window.validate(); err != nil {
			return fmt.Errorf("board profile %q has invalid addressed storage window %q: %w", p.ID, window.ID, err)
		}
		for _, id := range []string{window.ID, window.CommandID} {
			if _, duplicate := addressedIDs[id]; duplicate {
				return fmt.Errorf("board profile %q repeats addressed storage mapping %q", p.ID, id)
			}
			addressedIDs[id] = struct{}{}
		}
		addressedMappings = append(
			addressedMappings,
			addressedMapping{id: window.ID, address: window.Address, size: window.Size},
			addressedMapping{
				id: window.CommandID, address: window.CommandAddress, size: uint32(window.CommandWidth),
			},
		)
	}
	sort.Slice(addressedMappings, func(left, right int) bool {
		return addressedMappings[left].address < addressedMappings[right].address
	})
	for index, mapping := range addressedMappings {
		if index > 0 {
			previous := addressedMappings[index-1]
			if uint64(previous.address)+uint64(previous.size) > uint64(mapping.address) {
				return fmt.Errorf(
					"board profile %q addressed storage mappings %q and %q overlap",
					p.ID,
					previous.id,
					mapping.id,
				)
			}
		}
		mappingEnd := uint64(mapping.address) + uint64(mapping.size)
		for _, register := range registers {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mapping.id == register.ID ||
				uint64(mapping.address) < registerEnd && uint64(register.Address) < mappingEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mapping.id, register.ID)
			}
		}
		for _, register := range latchedRegisters {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mapping.id == register.ID ||
				uint64(mapping.address) < registerEnd && uint64(register.Address) < mappingEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mapping.id, register.ID)
			}
		}
		for _, window := range latchedWindows {
			windowEnd := uint64(window.Address) + uint64(window.Size)
			if mapping.id == window.ID ||
				uint64(mapping.address) < windowEnd && uint64(window.Address) < mappingEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mapping.id, window.ID)
			}
		}
	}
	if mgp := p.SamsungMGP; mgp != nil {
		mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
		for _, register := range registers {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mgp.ID == register.ID ||
				uint64(mgp.Address) < registerEnd && uint64(register.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, register.ID)
			}
		}
		for _, register := range latchedRegisters {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mgp.ID == register.ID ||
				uint64(mgp.Address) < registerEnd && uint64(register.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, register.ID)
			}
		}
		for _, window := range latchedWindows {
			windowEnd := uint64(window.Address) + uint64(window.Size)
			if mgp.ID == window.ID ||
				uint64(mgp.Address) < windowEnd && uint64(window.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, window.ID)
			}
		}
	}
	if mailbox := p.ADSPMailbox; mailbox != nil {
		if err := mailbox.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		mailboxEnd := uint64(mailbox.Address) + uint64(mailbox.Size)
		if mgp := p.SamsungMGP; mgp != nil {
			mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
			if mailbox.ID == mgp.ID ||
				uint64(mailbox.Address) < mgpEnd && uint64(mgp.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, mgp.ID)
			}
		}
		for _, region := range memory {
			regionEnd := uint64(region.Address) + uint64(region.Size)
			if uint64(mailbox.Address) < regionEnd && uint64(region.Address) < mailboxEnd {
				return fmt.Errorf(
					"board profile %q regions %q and %q overlap",
					p.ID,
					mailbox.ID,
					region.ID,
				)
			}
		}
		for _, register := range registers {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if uint64(mailbox.Address) < registerEnd && uint64(register.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, register.ID)
			}
		}
		for _, register := range latchedRegisters {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if uint64(mailbox.Address) < registerEnd && uint64(register.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, register.ID)
			}
		}
		for _, window := range latchedWindows {
			windowEnd := uint64(window.Address) + uint64(window.Size)
			if uint64(mailbox.Address) < windowEnd && uint64(window.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, window.ID)
			}
		}
		if command := mailbox.HostCommand; command != nil {
			windowsByID := make(map[string]LatchedRegisterWindowProfile, len(latchedWindows))
			for _, window := range latchedWindows {
				windowsByID[window.ID] = window
			}
			selector, ok := windowsByID[command.SelectorWindowID]
			if !ok || command.SelectorWidth != selector.Width ||
				command.SelectorOffset%uint32(command.SelectorWidth) != 0 ||
				uint64(command.SelectorOffset)+uint64(command.SelectorWidth) > uint64(selector.Size) {
				return fmt.Errorf("board profile %q has invalid ADSP host-command selector", p.ID)
			}
			commands := make(map[uint32]struct{}, len(command.Rules))
			for _, rule := range command.Rules {
				if rule.Command == 0 ||
					command.SelectorWidth < Width32 &&
						rule.Command >= uint32(1)<<(uint32(command.SelectorWidth)*8) {
					return fmt.Errorf("board profile %q has invalid ADSP host command 0x%x", p.ID, rule.Command)
				}
				if _, duplicate := commands[rule.Command]; duplicate {
					return fmt.Errorf("board profile %q repeats ADSP host command 0x%x", p.ID, rule.Command)
				}
				commands[rule.Command] = struct{}{}
				for _, operation := range rule.Copies {
					source, sourceOK := windowsByID[operation.SourceWindowID]
					destination, destinationOK := windowsByID[operation.DestinationWindowID]
					if !sourceOK || !destinationOK || operation.Width != source.Width ||
						operation.Width != destination.Width ||
						operation.SourceOffset%uint32(operation.Width) != 0 ||
						operation.DestinationOffset%uint32(operation.Width) != 0 ||
						uint64(operation.SourceOffset)+uint64(operation.Width) > uint64(source.Size) ||
						uint64(operation.DestinationOffset)+uint64(operation.Width) > uint64(destination.Size) {
						return fmt.Errorf("board profile %q has invalid ADSP host-command memory copy", p.ID)
					}
				}
			}
		}
		windowsByID := make(map[string]LatchedRegisterWindowProfile, len(latchedWindows))
		for _, window := range latchedWindows {
			windowsByID[window.ID] = window
		}
		controlRules := make(map[[2]uint32]struct{}, len(mailbox.ControlRules))
		for _, rule := range mailbox.ControlRules {
			key := [2]uint32{rule.Offset, rule.Value}
			if rule.Offset%uint32(Width32) != 0 ||
				uint64(rule.Offset)+uint64(Width32) > uint64(mailbox.Size) {
				return fmt.Errorf("board profile %q has invalid ADSP control-rule offset", p.ID)
			}
			if _, duplicate := controlRules[key]; duplicate {
				return fmt.Errorf("board profile %q repeats ADSP control rule at 0x%x value 0x%x", p.ID, rule.Offset, rule.Value)
			}
			controlRules[key] = struct{}{}
			for _, operation := range rule.Copies {
				source, sourceOK := windowsByID[operation.SourceWindowID]
				destination, destinationOK := windowsByID[operation.DestinationWindowID]
				if !sourceOK || !destinationOK || operation.Width != source.Width ||
					operation.Width != destination.Width ||
					operation.SourceOffset%uint32(operation.Width) != 0 ||
					operation.DestinationOffset%uint32(operation.Width) != 0 ||
					uint64(operation.SourceOffset)+uint64(operation.Width) > uint64(source.Size) ||
					uint64(operation.DestinationOffset)+uint64(operation.Width) > uint64(destination.Size) {
					return fmt.Errorf("board profile %q has invalid ADSP control-rule memory copy", p.ID)
				}
			}
			for _, operation := range rule.Writes {
				window, ok := windowsByID[operation.WindowID]
				if !ok || operation.Width != window.Width ||
					operation.Offset%uint32(operation.Width) != 0 ||
					uint64(operation.Offset)+uint64(operation.Width) > uint64(window.Size) ||
					operation.Width < Width32 && operation.Value >= uint32(1)<<(uint32(operation.Width)*8) {
					return fmt.Errorf("board profile %q has invalid ADSP control-rule memory write", p.ID)
				}
			}
			if interrupt := rule.Interrupt; interrupt != nil {
				if interrupt.UseVectoredController {
					if p.VectoredInterrupt == nil || interrupt.Source >= p.VectoredInterrupt.SourceCount {
						return fmt.Errorf(
							"board profile %q ADSP interrupt source %d exceeds vectored controller",
							p.ID,
							interrupt.Source,
						)
					}
				} else if interrupt.Source >= 64 {
					return fmt.Errorf(
						"board profile %q has invalid ADSP interrupt source %d",
						p.ID,
						interrupt.Source,
					)
				}
			}
		}
	}
	callIDs := make(map[string]struct{}, len(p.HLECalls))
	callTraps := make(map[cpu.ExecutionTrap]struct{}, len(p.HLECalls))
	for _, call := range p.HLECalls {
		if err := call.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		if _, duplicate := callIDs[call.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats HLE call %q", p.ID, call.ID)
		}
		trap := cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode}
		if _, duplicate := callTraps[trap]; duplicate {
			return fmt.Errorf("board profile %q repeats HLE address 0x%08x", p.ID, call.Address)
		}
		callIDs[call.ID] = struct{}{}
		callTraps[trap] = struct{}{}
	}
	return nil
}

func (p BoardProfile) ApplyReadOnlyRegisters(bus *Bus) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, spec := range p.ReadOnlyRegisters {
		register, err := NewReadOnlyRegister(spec.Width, spec.Value)
		if err != nil {
			return fmt.Errorf("apply board profile %q register %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, uint32(spec.Width), register); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	return nil
}

// ApplyAddressedStorageWindows connects profile-declared read apertures to the
// supplied machine storage. The aperture itself remains read-only while other
// controllers may update the shared backing device.
func (p BoardProfile) ApplyAddressedStorageWindows(bus *Bus, storage ReadOnlyStorage) error {
	if bus == nil || storage == nil {
		return fmt.Errorf("apply board profile %q addressed storage: nil bus or storage", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, spec := range p.AddressedStorageWindows {
		window, err := NewAddressedReadOnlyStorageWindow(
			storage,
			spec.Size,
			spec.AddressMask,
			spec.ResetCommand,
		)
		if err != nil {
			return fmt.Errorf("apply board profile %q storage window %q: %w", p.ID, spec.ID, err)
		}
		command, err := NewAddressedStorageCommandRegister(window, spec.CommandWidth)
		if err != nil {
			return fmt.Errorf("apply board profile %q storage command %q: %w", p.ID, spec.CommandID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, window); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
		if err := bus.MapMMIO(
			spec.CommandID,
			spec.CommandAddress,
			uint32(spec.CommandWidth),
			command,
		); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	return nil
}

func (p BoardProfile) ApplyLatchedRegisters(bus *Bus) error {
	return p.applyLatchedRegisters(bus, nil, nil, nil)
}

// ApplyLatchedRegistersWithInterrupts wires register-window devices whose
// evidenced responses include interrupt pulses to the board's controllers.
// ApplyLatchedRegisters remains available for profiles and tests that only
// require passive register latches.
func (p BoardProfile) ApplyLatchedRegistersWithInterrupts(
	bus *Bus,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	return p.applyLatchedRegisters(bus, interruptController, vectoredInterruptController, nil)
}

// ApplyLatchedRegistersWithInterruptsAndExistingWindows is the construction
// variant for a register window that also participates in board wiring. The
// supplied devices are treated as already mapped and remain available to the
// other profiled peripherals that reference the same window IDs.
func (p BoardProfile) ApplyLatchedRegistersWithInterruptsAndExistingWindows(
	bus *Bus,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
	existing map[string]*LatchedRegisterWindow,
) error {
	return p.applyLatchedRegisters(bus, interruptController, vectoredInterruptController, existing)
}

func (p BoardProfile) applyLatchedRegisters(
	bus *Bus,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
	existing map[string]*LatchedRegisterWindow,
) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	windows := make(map[string]*LatchedRegisterWindow, len(p.LatchedRegisterWindows))
	for id, window := range existing {
		if window == nil {
			return fmt.Errorf("apply board profile %q register window %q: nil existing device", p.ID, id)
		}
		windows[id] = window
	}
	for _, spec := range p.LatchedRegisterWindows {
		if _, ok := windows[spec.ID]; ok {
			continue
		}
		window, err := NewLatchedRegisterWindow(spec.Size, spec.Width)
		if err != nil {
			return fmt.Errorf("apply board profile %q register window %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, window); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
		windows[spec.ID] = window
	}
	if spec := p.ADSPMailbox; spec != nil {
		mailbox, err := NewQualcommADSPMailbox(spec.Size, spec.WriteControlOffset)
		if err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := mailbox.configureHostCommand(spec.HostCommand, windows); err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := mailbox.configurePeriodicInterrupt(
			spec.PeriodicInterrupt,
			interruptController,
			vectoredInterruptController,
		); err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := mailbox.configureControlRulesWithInterrupts(
			spec.ControlRules,
			windows,
			interruptController,
			vectoredInterruptController,
		); err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, mailbox); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	for _, spec := range p.LatchedRegisters {
		var register Device
		if len(spec.AdditionalWidths) != 0 {
			widths := append([]Width{spec.Width}, spec.AdditionalWidths...)
			var mixed *MixedWidthLatchedRegister
			var err error
			if spec.AllowSubwordOffsets {
				mixed, err = NewMixedWidthLatchedRegisterWithSubwordOffsets(widths, spec.ResetValue)
			} else {
				mixed, err = NewMixedWidthLatchedRegister(widths, spec.ResetValue)
			}
			if err != nil {
				return fmt.Errorf("apply board profile %q register %q: %w", p.ID, spec.ID, err)
			}
			register = mixed
		} else {
			exact, err := NewLatchedRegister(spec.Width, spec.ResetValue)
			if err != nil {
				return fmt.Errorf("apply board profile %q register %q: %w", p.ID, spec.ID, err)
			}
			for _, pulse := range spec.WritePulses {
				// Assign only a non-nil controller. A typed nil pointer stored
				// in the interface would pass AttachWritePulse's nil check and
				// then panic on the first guest write that matches the rule.
				var pulser qualcommInterruptSourcePulser
				if pulse.UseVectoredController {
					if vectoredInterruptController != nil {
						pulser = vectoredInterruptController
					}
				} else if interruptController != nil {
					pulser = interruptController
				}
				if pulser == nil {
					return fmt.Errorf(
						"apply board profile %q register pulse %q: no attached interrupt controller",
						p.ID,
						spec.ID,
					)
				}
				if err := exact.AttachWritePulse(pulse.Mask, pulse.Value, pulse.Sources, pulser); err != nil {
					return fmt.Errorf("apply board profile %q register pulse %q: %w", p.ID, spec.ID, err)
				}
			}
			register = exact
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, uint32(spec.Width), register); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	return nil
}

func (p BoardProfile) ApplyMemory(bus *Bus) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, region := range p.Memory {
		switch region.Kind {
		case MemoryRAM:
			if err := bus.MapRAM(region.ID, region.Address, region.Size); err != nil {
				return fmt.Errorf("apply board profile %q: %w", p.ID, err)
			}
		case MemorySparseRAM:
			if err := bus.MapSparseRAM(region.ID, region.Address, region.Size); err != nil {
				return fmt.Errorf("apply board profile %q: %w", p.ID, err)
			}
		}
	}
	responsesByMemory := make(map[string][]MemoryWriteResponseProfile)
	for _, response := range p.MemoryWriteResponses {
		responsesByMemory[response.MemoryID] = append(responsesByMemory[response.MemoryID], response)
	}
	for memoryID, responses := range responsesByMemory {
		if err := bus.configureMemoryWriteResponses(memoryID, responses); err != nil {
			return fmt.Errorf("apply board profile %q memory region %q: %w", p.ID, memoryID, err)
		}
	}
	return nil
}

// AttachSamsungMGP maps the profiled control aperture after its shared RAM is
// present. Profiles without this companion processor require no placeholder.
func (p BoardProfile) AttachSamsungMGP(bus *Bus) (*SamsungMGPControl, error) {
	if bus == nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.SamsungMGP == nil {
		return nil, nil
	}
	var shared MemoryRegionProfile
	for _, region := range p.Memory {
		if region.ID == p.SamsungMGP.SharedMemoryID {
			shared = region
			break
		}
	}
	readyAddress := shared.Address + p.SamsungMGP.ReadyOffset
	device, err := NewSamsungMGPControl(bus, SamsungMGPControlConfig{
		Size:                      p.SamsungMGP.Size,
		ReleaseOffset:             p.SamsungMGP.ReleaseOffset,
		ReadyAddress:              readyAddress,
		ReadyValue:                p.SamsungMGP.ReadyValue,
		ResponseDelayInstructions: p.SamsungMGP.ResponseDelayInstructions,
	})
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: %w", p.ID, err)
	}
	if err := bus.MapMMIO(
		p.SamsungMGP.ID,
		p.SamsungMGP.Address,
		p.SamsungMGP.Size,
		device,
	); err != nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: %w", p.ID, err)
	}
	return device, nil
}

// AttachMDP wires the board-selected script engine to its boot-control
// completion event. Profiles without an MDP remain valid and require no
// display engine, which keeps board construction firmware-family driven.
func (p BoardProfile) AttachMDP(
	bus *Bus,
	panel *DCSPanelController,
	bootControl *QualcommBootControl,
) (*QualcommMDPScriptEngine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.MDP == nil {
		return nil, nil
	}
	if bootControl == nil {
		return nil, fmt.Errorf("attach board profile %q MDP: nil boot control", p.ID)
	}
	engine, err := NewQualcommMDPScriptEngine(bus, panel, *p.MDP)
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q MDP: %w", p.ID, err)
	}
	if err := bootControl.AttachCompletionHandler(p.MDP.CompletionStartOffset, engine); err != nil {
		return nil, fmt.Errorf("attach board profile %q MDP completion: %w", p.ID, err)
	}
	return engine, nil
}

// AttachKeypad creates the board-selected matrix and wires guest GPIO output
// writes to the primary-clock input bank sampled by firmware. Profiles without
// a keypad remain valid so non-phone boards need no placeholder device.
func (p BoardProfile) AttachKeypad(
	primaryClock *QualcommPrimaryClockControl,
	secondaryClock *QualcommSecondaryClockControl,
	interruptController *QualcommInterruptController,
) (*QualcommGPIOKeypad, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Keypad == nil {
		return nil, nil
	}
	if primaryClock == nil {
		return nil, fmt.Errorf("attach board profile %q keypad: nil GPIO device", p.ID)
	}
	usesInterrupt, usesSecondaryClock := false, false
	for _, row := range p.Keypad.Rows {
		switch row.OutputBank {
		case QualcommGPIOOutputInterrupt:
			usesInterrupt = true
		case QualcommGPIOOutputSecondaryClock:
			usesSecondaryClock = true
		}
	}
	if usesInterrupt && interruptController == nil || usesSecondaryClock && secondaryClock == nil {
		return nil, fmt.Errorf("attach board profile %q keypad: nil GPIO output device", p.ID)
	}
	if primaryClock.keypad != nil ||
		usesInterrupt && interruptController.gpioWriteObserver != nil ||
		usesSecondaryClock && secondaryClock.gpioWriteObserver != nil {
		return nil, fmt.Errorf("attach board profile %q keypad: GPIO device already attached", p.ID)
	}
	keypad, err := NewQualcommGPIOKeypad(*p.Keypad)
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q keypad: %w", p.ID, err)
	}
	if err := primaryClock.AttachGPIOKeypad(keypad); err != nil {
		return nil, fmt.Errorf("attach board profile %q keypad inputs: %w", p.ID, err)
	}
	if usesInterrupt {
		observer := qualcommGPIOKeypadBankObserver{keypad: keypad, bank: QualcommGPIOOutputInterrupt}
		if err := interruptController.AttachGPIOWriteObserver(observer); err != nil {
			return nil, fmt.Errorf("attach board profile %q interrupt keypad outputs: %w", p.ID, err)
		}
	}
	if usesSecondaryClock {
		observer := qualcommGPIOKeypadBankObserver{keypad: keypad, bank: QualcommGPIOOutputSecondaryClock}
		if err := secondaryClock.AttachGPIOWriteObserver(observer); err != nil {
			return nil, fmt.Errorf("attach board profile %q secondary-clock keypad outputs: %w", p.ID, err)
		}
	}
	return keypad, nil
}

func SCHW830DL21BoardProfile() BoardProfile {
	return BoardProfile{
		ID:              "samsung.sch-w830",
		PlatformID:      "qualcomm.arm9-sch-family",
		FirmwareBuildID: "samsung.sch-w830.dl21",
		// SCH-W830's OEMSBL and AMSS XSR tables both support EC/BA for this
		// 256 MiB, 2 KiB-page device. Do not copy the EC/AA ID from the
		// reference SPH-W8300 runtime dump: that carrier variant has a
		// different AMSS NAND table. This controller generation exposes the
		// maker in READ_ID[15:8] and the device in READ_ID[7:0].
		NANDReadID:         0x0000ecba,
		NANDSize:           0x10000000,
		NANDPageSize:       0x00000800,
		NANDEraseBlockSize: 0x00020000,
		// The downloader set has no physical OOB stream. Its WBT normalizer
		// promotes the newest MIBIB generation into QCSBL's second usable boot
		// slot; inventing a factory-bad block here would incorrectly displace
		// every logical AMSS read by one erase block.
		NANDFactoryBadBlocks: nil,
		// Samsung's downloader leaves this little-endian 0xBEAFFEFF completion
		// marker at the first byte after the packaged NAND layout. DL21 consumes
		// it to select its one-shot native BML/STL/TFS4 factory provisioning path.
		NANDInitialData: []FlashSeed{{
			Offset: 0x097c0000,
			Data:   []byte{0xff, 0xfe, 0xaf, 0xbe},
		}},
		BootClockModeStatus: 0x00000001,
		// The multiplexed keypad inputs and the boot power-key/release input
		// are pulled high while idle. Firmware waits for bit 4 before leaving
		// its late hardware-initialization loop.
		PrimaryClockStatus:    0x0000001f,
		PrimaryClockInputMask: 0x0000001f,
		PrimaryClockKeys: []QualcommPrimaryClockKeyProfile{{
			// A short active-low pulse on the boot power-key input performs the
			// handset's red END action and returns the native UI to idle.
			ID: "end", InputLine: 4, ActiveLow: true,
		}},
		BootControlWritableOffsets: []uint32{
			0x0008,
			0x00bc, 0x00c0,
			// The runtime cache-maintenance helper pulses the adjacent memory-
			// controller command strobe high and then low. No completion value is
			// read from this latch; retaining it is sufficient for the profiled
			// MSM6550 control aperture.
			0x0204,
			0x058c, 0x0590, 0x059c,
			0x05a0, 0x05a4, 0x05b0, 0x05b4, 0x05b8,
			0x05c4, 0x05c8, 0x05cc, 0x05d8,
			0x0a34,
			0x0a54, 0x0a58,
			0x0b34,
			0x0c00, 0x0c04, 0x0c08, 0x0c0c, 0x0c2c, 0x0c38, 0x0c3c, 0x0c40,
			0x0e00, 0x0e04, 0x0e08, 0x0e10, 0x0e1c, 0x0e20, 0x0e38, 0x0e3c, 0x0e40,
			0x200c,
			0x2840,
			0x4100, 0x4104, 0x4108,
			// DL21's undocumented MMCC-side command path emits a command
			// sequence through +0x4110 and writes its word payload at +0x4120.
			0x4110, 0x4114, 0x4118, 0x411c, 0x4120,
			0x4128, 0x412c, 0x4130, 0x4134, 0x4138, 0x413c,
			0x423c,
			0x4600, 0x4604, 0x4614,
			0x533c,
		},
		BootControlSBIControllers: []uint32{0x5000, 0x5100, 0x5200},
		// DL21's battery monitor reads PMIC registers 0x54, 0x53, and 0x4f.
		// Bit 0 at 0x54 completes the conversion, 0xc1 at 0x4f preserves the
		// programmed 8-bit mode, and the maximum sample at 0x53 keeps the
		// handset's battery indicator at full charge.
		BootControlSBIReadResponses: []QualcommSBIReadResponse{
			{Controller: 0x5100, Address: 0x4f, Value: 0xc1},
			{Controller: 0x5100, Address: 0x53, Value: 0xff},
			{Controller: 0x5100, Address: 0x54, Value: 0x01},
		},
		BootControlSBICompletionStatus: 0x0494,
		BootControlHalfwordOffsets: []uint32{
			0x0e0c, 0x0e28,
			0x4000, 0x4004, 0x4008,
			0x4010, 0x4014, 0x4018, 0x401c,
			0x4020, 0x4024, 0x4028, 0x402c,
			0x4030, 0x4034, 0x4038,
			0x4200, 0x4204, 0x4208,
			0x4210, 0x4214, 0x4218, 0x421c,
			0x4220, 0x4224, 0x4228, 0x422c,
			0x4230, 0x4234, 0x4238,
		},
		BootControlMixedWidthOffsets: []uint32{0x0e20},
		// MSM6550 legacy UART1/2. Their write-side CSR/CR/IMR aliases are
		// configured as halfwords above; runtime status and FIFO traffic use
		// the read-side SR/MISR/ISR and byte-wide TF/RF aliases.
		BootControlLegacyUARTControllers: []uint32{0x4000, 0x4200},
		// MSM6xxx receive-front chains are separated by 0x200 bytes. The
		// original DL21 radio initializer reads, masks, and rewrites the second
		// chain's reset and control words at CHIP_BASE+0x0c00/+0x04; both chain
		// reset registers power on asserted while the control latch starts clear.
		BootControlRegisterResets: []QualcommBootRegisterReset{
			{Offset: 0x0204, Value: 0},
			{Offset: 0x0a00, Value: 1},
			{Offset: 0x0c00, Value: 1},
		},
		// A write to RXFRONT1+0x38 is followed by polling +0x34 with mask
		// 0x008007ff. Zero is the observed idle/completed state.
		BootControlReadOnlyRegisters: []QualcommBootReadOnlyRegister{
			// The runtime polls bits 3 and 5 while evaluating external
			// wake/input state. Neither is asserted in the deterministic
			// offline board state.
			{Offset: 0x048c, Value: 0},
			{Offset: 0x0c34, Value: 0},
			// The late e00-block consumer samples the low 12 bits of the
			// response/status word before deciding whether a wrapped interval
			// contains its request. No external response is pending offline.
			{Offset: 0x0e14, Value: 0},
		},
		BootControlCompletionEvents: []QualcommCompletionEventConfig{{
			StartOffset:           0x0e04,
			StartMask:             0x00000001,
			StatusOffset:          0x0e24,
			StatusMask:            0x00000002,
			AcknowledgeOffset:     0x0e28,
			AcknowledgeWidth:      Width16,
			AcknowledgeMask:       0x0000ffff,
			InterruptSource:       13,
			UseVectoredController: true,
		}},
		PrimaryClockWritableOffsets: []uint32{
			0x0594, 0x0598, 0x05a8, 0x05ac,
			0x05bc, 0x05c0, 0x05d0, 0x05d4,
		},
		SecondaryClockWritableOffsets: []uint32{0x040c},
		SparseBusRegisterOffsets:      samsungSCHSparseBusRegisterOffsets(),
		ClockRegimeSleepControllers:   []uint32{0x5200, 0x5244},
		ClockRegimeCounters: []QualcommClockRegimeCounterConfig{{
			// DL21 samples the low 18 bits at CHIP_BASE+0x6000 when
			// measuring short radio/clock intervals. MSM6xxx documentation
			// identifies this aperture as the CDMA chip-x8 RTC domain.
			Offset:                0x6000,
			InstructionsPerSecond: 60_000_000,
			CounterHz:             9_830_400,
			Bits:                  18,
		}},
		ClockRegimeComparators: []QualcommClockRegimeComparatorConfig{{
			// STMR timer 1 exposes a 0..149 phase in bits 15:8 of the shared
			// counter. Eight independent events compare that phase through the
			// +0x48c4 table and raise the firmware's source-46 ISR. The 150 Hz
			// phase is the chip-x8 RTC divided by 65,536.
			CounterOffset:         0x480c,
			CounterMask:           0x0000ff00,
			InstructionsPerSecond: 60_000_000,
			CounterHz:             150,
			CounterModulus:        150,
			MatchBaseOffset:       0x48c4,
			MatchStride:           4,
			MatchMask:             0x0000ff00,
			EnableOffset:          0x487c,
			StatusOffset:          0x4864,
			AcknowledgeOffset:     0x4870,
			EventMask:             0x000000ff,
			InterruptSource:       46,
			UseVectoredController: true,
		}},
		VectoredInterrupt: &QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
			GroupCount:         6,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{
				{Source: 11, EnableOffset: 0x10, StatusOffset: 0x84, ValidMask: 0x07},
				{Source: 14, EnableOffset: 0x14, StatusOffset: 0x88, ValidMask: 0x03},
				{Source: 17, EnableOffset: 0x18, StatusOffset: 0x8c, ValidMask: 0x3f},
				{Source: 19, EnableOffset: 0x1c, StatusOffset: 0x90, ValidMask: 0x0f},
				{Source: 7, EnableOffset: 0x20, StatusOffset: 0x94, ValidMask: 0x0f},
				{Source: 2, EnableOffset: 0x24, StatusOffset: 0x98, ValidMask: 0x07},
			},
		},
		TimeTickClock: &QualcommTimeTickClockConfig{
			// Match deltas of 326/327 ticks implement the firmware's 10 ms
			// scheduler quantum. Sixty million retired instructions per second
			// matches the existing KTF handset execution budget at 60 Hz.
			InstructionsPerSecond: 60_000_000,
			TimeTickHz:            32_768,
			InterruptSource:       21,
			UseVectoredController: true,
		},
		Keypad: &QualcommGPIOKeypadProfile{
			Columns: []uint8{0, 1, 2, 3},
			Rows: []QualcommGPIOKeypadRowProfile{
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000400},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000800},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00001000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00002000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00004000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000040},
				{OutputOffset: 0x10, OutputMask: 0x00200000},
			},
			Keys: []QualcommGPIOKeyProfile{
				// Native idle-screen probes identify the left/menu and right/memo
				// soft buttons. This also agrees with the handset manual's B(left)
				// and B(right) behavior.
				{ID: "soft-left", Row: 0, Column: 0},
				{ID: "soft-right", Row: 1, Column: 0},
				// Native DL21 screen probes identify the four ring directions on
				// row 4: from idle they open the four documented shortcuts, while
				// within a list they move the selection or change the selected
				// value. The firmware scans this row's columns in ascending order,
				// so up/down/left/right occupy columns 0..3; the earlier reversed
				// assignment rotated on-screen navigation relative to the pressed
				// direction (pressing right moved up, and so on).
				{ID: "up", Row: 4, Column: 0},
				{ID: "down", Row: 4, Column: 1},
				{ID: "left", Row: 4, Column: 2},
				{ID: "right", Row: 4, Column: 3},
				// NATE/OK enters the highlighted menu item. The C/back key returns
				// from a nested settings list to the grid, and from the grid home.
				{ID: "ok", Row: 5, Column: 0},
				{ID: "back", Row: 3, Column: 0},
				// Dialing a number and pressing this coordinate enters the native
				// call screen.
				{ID: "send", Row: 2, Column: 0},
				// DL21's raw codes 0x54 and 0x55 and its native sound overlay
				// identify the handset's two side volume buttons.
				{ID: "volume-up", Row: 6, Column: 0},
				{ID: "volume-down", Row: 6, Column: 1},
				{ID: "digit-1", Row: 3, Column: 1},
				{ID: "digit-2", Row: 3, Column: 2},
				{ID: "digit-3", Row: 3, Column: 3},
				{ID: "digit-4", Row: 2, Column: 1},
				{ID: "digit-5", Row: 2, Column: 2},
				{ID: "digit-6", Row: 2, Column: 3},
				{ID: "digit-7", Row: 1, Column: 1},
				{ID: "digit-8", Row: 1, Column: 2},
				{ID: "digit-9", Row: 1, Column: 3},
				{ID: "star", Row: 0, Column: 1},
				{ID: "digit-0", Row: 0, Column: 2},
				{ID: "pound", Row: 0, Column: 3},
			},
		},
		Panel: DCSPanelConfig{
			Width: 240, Height: 320, NativeAddressMode: 0x48,
		},
		MDP: &QualcommMDPProfile{
			CompletionStartOffset: 0x0e04,
			ScriptPointerOffset:   0x0e08,
			RGB565SourceFormat:    0x20,
		},
		LegacyTopVersion:        0x00000000,
		LegacyTopIdentification: 0x00000000,
		Memory: []MemoryRegionProfile{
			{
				ID:      "ebi-ram",
				Kind:    MemoryRAM,
				Address: 0x00000000,
				Size:    0x08000000,
			},
			{
				// MSM6550 exposes the application DSP address space as the
				// complete 0x70000000-0x77ffffff ARM window. The firmware
				// populates it incrementally, so page-backed storage avoids a
				// 128 MiB allocation plus an equally large reset copy.
				ID:      "adsp-address-space",
				Kind:    MemorySparseRAM,
				Address: 0x70000000,
				Size:    0x08000000,
			},
			{
				ID:      "pbl-iram",
				Kind:    MemoryRAM,
				Address: 0x78000000,
				Size:    0x00010000,
			},
			{
				ID:      "high-vector-iram",
				Kind:    MemoryRAM,
				Address: 0xffff0000,
				Size:    0x0000f000,
			},
		},
		ReadOnlyRegisters: []ReadOnlyRegisterProfile{
			{
				ID:      "external-platform-status",
				Address: 0x30010004,
				Width:   Width16,
				Value:   0x0000,
			},
			{
				ID:      "external-platform-selector",
				Address: 0x30030000,
				Width:   Width16,
				Value:   0x0300,
			},
		},
		LatchedRegisterWindows: []LatchedRegisterWindowProfile{
			// Late subsystem initialization copies tables into independently
			// decoded Qualcomm bus banks. Their side effects are not yet modeled;
			// retain only the observed apertures and access widths instead of
			// treating the surrounding address space as RAM.
			{ID: "external-16bit-bank-0", Address: 0x91000000, Size: 0x00010000, Width: Width16},
			{ID: "external-16bit-bank-1", Address: 0x91200000, Size: 0x00010000, Width: Width16},
			{ID: "external-32bit-bank-2", Address: 0x91400000, Size: 0x00010000, Width: Width32},
			{ID: "external-32bit-bank-4", Address: 0x91800000, Size: 0x00014000, Width: Width32},
		},
		ADSPMailbox: &QualcommADSPMailboxProfile{
			ID:                 "external-32bit-control",
			Address:            0x91c00000,
			Size:               0x00000100,
			WriteControlOffset: 0x00000008,
			ControlRules: []QualcommADSPControlRuleProfile{
				{
					// The ARM command queue writes HOST_INT=1 after publishing a
					// command in the shared buffer. The QDSP image acknowledges it
					// through event slot 0 before raising the registered host IRQ.
					Offset: 4, Value: 1, ResponseDelayInstructions: 1,
					Writes: []QualcommADSPMemoryWriteProfile{
						{
							// The QDSP command queue reads the first halfword back as
							// its completion status. Zero acknowledges success; leaving
							// the submitted command type here makes the host retry it.
							WindowID: "external-16bit-bank-1", Offset: 0x00004d1e,
							Width: Width16, Value: 0,
						},
						{
							WindowID: "external-16bit-bank-1", Offset: 0x000051a4,
							Width: Width16, Value: 1,
						},
					},
					Interrupt: &QualcommADSPInterruptProfile{
						Source: 33, UseVectoredController: true,
					},
				},
				{
					Offset: 0, Value: 2,
					Writes: []QualcommADSPMemoryWriteProfile{{
						WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
						Width: Width16, Value: 1,
					}},
				},
				{
					Offset: 0, Value: 3,
					Writes: []QualcommADSPMemoryWriteProfile{{
						WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
						Width: Width16, Value: 0,
					}},
				},
			},
			HostCommand: &QualcommADSPHostCommandProfile{
				SelectorWindowID: "external-16bit-bank-1",
				SelectorOffset:   0x00000bc8,
				SelectorWidth:    Width16,
				Rules: []QualcommADSPHostCommandRuleProfile{
					{
						Command: 1,
						Copies: []QualcommADSPMemoryCopyProfile{{
							SourceWindowID:      "external-32bit-bank-2",
							SourceOffset:        0x00000570,
							DestinationWindowID: "external-32bit-bank-2",
							DestinationOffset:   0x0000056c,
							Width:               Width32,
						}},
					},
					// Command 4 releases the temporary DSP lock after a module-set
					// transition. It has no payload; clearing the selector is the
					// complete DSP-side acknowledgement.
					{Command: 4},
				},
			},
		},
	}
}

// SCHW770DA05BoardProfile starts the earlier version-one MIBIB handset from
// its own board identity. DA05 has a 512 MiB EC/DC raw NAND containing the
// downloader image and a separate 384 MiB EC/5C OneNAND data device.
func SCHW770DA05BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w770"
	profile.FirmwareBuildID = "samsung.sch-w770.da05"
	profile.Memory = append([]MemoryRegionProfile(nil), profile.Memory...)
	for index := range profile.Memory {
		if profile.Memory[index].ID == "ebi-ram" {
			// DA05 places its late OEMSBL work area at 0x0bfff000 and
			// explicitly clears words immediately below 0x0c000000.
			profile.Memory[index].Size = 0x0c000000
			break
		}
	}
	profile.NANDSize = 0x20000000
	profile.NANDReadID = 0x0000ecdc
	// DA05 derives this location from the MIBIB packaged end (0x11200000)
	// and checks the downloader's little-endian 0xBEAFFEFF completion marker
	// before choosing its one-shot native BML/STL/TFS4 provisioning path. The
	// following words are the preload-table entry count and version. The
	// four-piece archive omits this downloader-generated footer, so model a
	// completed download with an empty preload table without inventing payload
	// records that are not present in the archive.
	profile.NANDInitialData = []FlashSeed{{
		Offset: 0x11200000,
		Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.OneNAND = &OneNANDProfile{
		Address: 0x40000000, ManufacturerID: 0x00ec, DeviceID: 0x005c,
		DieBlockOffset: 0x0800, Capacity: 0x18000000,
	}
	// DA05 predates the service-ID table used by later QCSBLs. Its ROM PBL
	// publishes equivalent NAND geometry through boot_feature_cfg at this
	// fixed high-IRAM structure address.
	profile.PBLLegacyFeatureDataAddress = 0xffff6044
	// The boot power-key input line that publishes W830's red END action is not
	// evidenced on DA05, whose scanner samples input bits 0..2 only. Drop the
	// inherited host control rather than exporting an unproven W770 key.
	profile.PrimaryClockKeys = nil
	// DA05's hsdevice_W770 key scanner selects three GPIO rows through bits
	// 10..12 of GPIO_OE_1 and samples input bits 0..2. The HOLD switch is the
	// otherwise-unmapped matrix position stored at scan-buffer index 6
	// (column 2, row 0); firmware handles that position separately to emit its
	// short- and long-hold events. Keep the other coordinates unnamed until
	// their handset-facing meanings are established independently.
	profile.Keypad = &QualcommGPIOKeypadProfile{
		Columns: []uint8{0, 1, 2},
		Rows: []QualcommGPIOKeypadRowProfile{
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000400},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000800},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00001000},
		},
		Keys: []QualcommGPIOKeyProfile{{ID: "hold", Row: 0, Column: 2}},
		// GPIOs 46, 62, and 63 are the three matrix columns. The shared GPIO
		// dispatcher services hardware groups 2 and 3 from VIC source 5, reads
		// their pending words at +0x05e4/+0x05e8, and acknowledges each bit via
		// +0x0594/+0x0598 after invoking the registered key callback.
		InterruptGroups: []QualcommGPIOInterruptGroupProfile{
			{
				ClearOffset: 0x0594, EnableOffset: 0x05a8,
				DetectOffset: 0x05bc, PolarityOffset: 0x05d0, StatusOffset: 0x05e4,
				InterruptSource: 5, UseVectoredController: true,
			},
			{
				ClearOffset: 0x0598, EnableOffset: 0x05ac,
				DetectOffset: 0x05c0, PolarityOffset: 0x05d4, StatusOffset: 0x05e8,
				InterruptSource: 5, UseVectoredController: true,
			},
		},
		ColumnInterrupts: []QualcommGPIOKeypadColumnInterruptProfile{
			{Column: 0, Group: 1, Mask: 1 << 7}, // GPIO 62
			{Column: 1, Group: 1, Mask: 1 << 8}, // GPIO 63
			{Column: 2, Group: 0, Mask: 1 << 7}, // GPIO 46 / HOLD column
		},
	}
	// DA05 selects the legacy high-page path and exposes paired getter/setter
	// routines for the boot scratch words at 0xffffff04 and 0xffffff08.
	// Both words reset clear and retain values written during the handoff.
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	// DA05's portrait boot surface is scanned from the opposite mounted edge
	// to W830's 240x320 panel. Page-reverse plus BGR maps its native 0x88 mode
	// to the upright framebuffer orientation exposed to frontends.
	profile.Panel = DCSPanelConfig{Width: 240, Height: 400, NativeAddressMode: 0x88}
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20020000,
	}
	// OEMSBL streams a fixed 16-bit register table through the sparse
	// 0x30000000/0x30020000 command/data pair before it initializes the primary
	// panel. No status read or framebuffer transfer has been observed on this
	// auxiliary path, so retain only the two evidenced halfword latches.
	// DA05 also drives a byte-wide external command/data port at offsets 0 and 2.
	// Its low-level helper writes command values to the first byte and streams
	// payload bytes through the second; retaining those two latches is enough
	// to preserve the guest-visible bus contract while the attached peripheral
	// remains offline.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID:      "auxiliary-16bit-panel-command",
			Address: 0x30000000,
			Size:    uint32(Width16),
			Width:   Width16,
		},
		LatchedRegisterWindowProfile{
			ID:      "auxiliary-16bit-panel-data",
			Address: 0x30020000,
			Size:    uint32(Width16),
			Width:   Width16,
		},
		LatchedRegisterWindowProfile{
			ID:      "external-8bit-command-data",
			Address: 0x38000000,
			Size:    4,
			Width:   Width8,
		},
	)
	// DA05's GPIO helper resolves group 4 input through CHIP_BASE+0x0940.
	// That word is reserved in the older INTCTL layout inherited by W830, so
	// expose the W770 low-idle input only through this exact board contract.
	profile.BootControlGPIOInputs = []QualcommGPIOInputRegister{{Offset: 0x40, Value: 0}}
	// DA05 samples bit 10 of GPIO_IN_1 at this address when publishing the
	// initial slider state. No external transition is asserted at reset.
	profile.SecondaryClockReadOnlyRegisters = []QualcommSecondaryClockReadOnlyRegister{{
		Offset: 0x0444,
		Value:  0,
	}}
	// DA05 restores its saved chip configuration through +0x00a0 immediately
	// before the common +0x00a4 latch. AMSS samples and then replaces the clock
	// plan word at +0x010c, programs +0x0120 before the common +0x0124 control,
	// updates the paired clock-source controls at +0x0130/+0x0134, and writes
	// companion plan words at +0x0148/+0x0248. The clock-vote transition copies
	// its calibration pair into the common +0x0080/+0x0084 bank. AMSS peripheral
	// setup writes the +0x0a44 command word; its polling code treats bit 1 at
	// +0x0a4c as successful synchronous completion and reads a zero result from
	// +0x0a50. The QCSBL stack-exit path clears the adjacent watchdog/control
	// latches below.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x0a4c, Value: 0x00000002},
		QualcommBootReadOnlyRegister{Offset: 0x0a50, Value: 0x00000000},
		// A periodic AMSS worker debounces bit 23 of this raw input word. The
		// deterministic board starts with that external signal deasserted.
		QualcommBootReadOnlyRegister{Offset: 0x05ec, Value: 0x00000000},
	)
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x0080,
		0x00a0,
		0x010c,
		0x0120,
		0x0130,
		0x0134,
		0x0148,
		0x0248,
		0x0a44,
		0x53a8,
		0x53e0,
	)
	return profile
}

// SCHW320DC18BoardProfile describes the shared early raw-downloader platform
// with SCH-W320's exact firmware and NAND-layout identity.
func SCHW320DC18BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w320", "samsung.sch-w320.dc18", 0x0a760000,
	)
	// DC18's OEMSBL samples bit 1 of the primary input word after its board
	// setup and takes a dedicated boot path only while that active-low line is
	// asserted.
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{
		{ID: "download", InputLine: 1, ActiveLow: true},
	}
	// DC18's AMSS clears runtime arenas at 0x09800000 and 0x0a000000 through
	// its ordinary word-copy loop. Its identity-mapped MMU table covers the
	// complete second EBI RAM bank; leave adjacent builds on their evidenced
	// 128 MiB map and expose the additional 64 MiB only for SCH-W320.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "w320-ebi1-ram", Kind: MemorySparseRAM,
		Address: 0x08000000, Size: 0x04000000,
	})
	// DC18 programs the second UART controller with 32-bit STR operations,
	// while earlier boot stages retain their narrower accesses.
	promoteQualcommLegacyUARTToMixedWidth(&profile, 0x4200)
	// DC18's UIM task programs the intervening controller at +0x4100 with the
	// same word-wide MSM6280 UART protocol used by DC17. Supply a minimal T=0
	// card transport: ATR after reset, deterministic successful APDU status,
	// and the compact-VIC child used by the registered UIM ISR.
	configureQualcommLegacyUARTWordController(&profile, 0x4100)
	profile.BootControlLegacyUARTReceiveData = []QualcommLegacyUARTReceiveData{{
		Controller:      0x4100,
		InterruptSource: 55,
		// DC18 polls the UIM status again before the next clocked-device slice.
		// Publish the ATR in the same RX-enable transaction.
		DelayInstructions: 0,
		EchoTransmit:      true,
		T0Card:            true,
		Data:              []byte{0x3b, 0x00},
	}}
	profile.LegacyInterruptCascade = &QualcommInterruptCascadeProfile{
		// DC18 registers the UIM transport on child bit 1 in source 17's
		// second-level group.  The adjacent child bit 3 belongs to a different
		// handler and must not receive the legacy QIC output.
		VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
	}
	// DC18's late GPIO setup resolves its fourth input group through
	// CHIP_BASE+0x0940. The word is reserved in this INTCTL generation and no
	// external line is asserted on the deterministic board at reset.
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
	)
	// DC18's raw-NAND probe samples compact-VIC group 14 at CHIP_BASE+0x488.
	// Wire the EBI request and NAND completion lines into that second-level
	// status aperture; the older flat NAND-ready alias is hidden by the VIC.
	profile.BootControlGroupedStatusResponses = []QualcommBootGroupedStatusResponse{
		{
			Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
			GroupStatusOffset: 0x88, GroupMask: 0x02,
		},
		{
			Offset: 0x0380, NANDReadyMask: 0x01,
			GroupStatusOffset: 0x88, GroupMask: 0x01,
		},
	}
	// Unlike the adjacent raw builds, DC18 rechecks a signed loader-state
	// record that the missing mask-ROM PBL normally leaves behind after QCSBL
	// authentication. The package registry has already selected the exact
	// QCSBL by SHA-256, so restore only that PBL ABI result at the original
	// helper boundary; no guest instruction or firmware byte is patched.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w320-pbl-verified-loader-state", Contract: HLEContractQualcommPBLVerifiedLoaderState,
		Address: 0x0010214e, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// DC18's OEMSBL cache-maintenance wrapper calls a second routine supplied by
	// the preceding boot environment at 0x00102fb2. The archived PBL fragment
	// contains only the adjacent C++ runtime strings/table at that address, so
	// executing it as Thumb code falls through into data. The wrapper ignores the
	// result and performs its own barriers; preserve the resident routine's
	// returning ABI without patching either firmware image.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-pbl-cache-maintenance-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x00102fb2, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// DC18's ARM veneer at 0x00e00fb0 calls 0x001138c8, inside the
	// progressive ELF's entirely zero-filled 0x00100000 program segment. The
	// handset's preceding boot environment supplies that resident callback;
	// its sole AMSS call site continues unconditionally and ignores the result.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-resident-boot-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x001138c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// A late AMSS caller at 0x02300f36 reaches a second entry in the same
	// zero-filled resident segment. Its interworking link returns to Thumb code,
	// and the caller consumes no result, matching the preserved-register ABI.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-late-resident-boot-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x00113ea8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// The downloader archive contains an empty preload table, so it does not
	// carry mmda/brew/shared/pbook/pbdeleted. DC18's optional phonebook restore
	// helper assumes that factory file exists and dereferences IFILE_Open's nil
	// result before it can report failure. Preserve the helper's normal false
	// result at its ABI boundary; no proprietary phonebook data is fabricated.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-optional-phonebook-preload",
		Contract: HLEContractSamsungOptionalPreloadFile,
		Address:  0x01402864, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The same empty downloader preload table omits the indexed multimedia
	// defaults consumed by this sibling reader. Its callers already handle a
	// false result by retaining compiled-in defaults; avoid its identical nil
	// IFILE dereference while preserving that result.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-optional-multimedia-preload",
		Contract: HLEContractSamsungOptionalPreloadFile,
		Address:  0x014082ba, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// DC18 uploads its ARM7/MGP image into the 32 KiB companion code bank. Its
	// image header publishes the polled ready byte at +0x29e0.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
		Address: 0x90108000, Size: 0x00008000,
	}, MemoryRegionProfile{
		// The late AMSS MGP client reads the companion data and scratch banks
		// from +0x10000 through the halfword interface at +0x1f140.
		ID: "w320-mgp-data-ram", Kind: MemorySparseRAM,
		Address: 0x90110000, Size: 0x0000f140,
	})
	profile.SamsungMGP = &SamsungMGPProfile{
		// The active release register is followed by the tail of a 0x240-byte
		// table copied from 0x9011f000, so retain the final 0xa0-byte block.
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0xa0,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		// The host and companion also exchange a halfword at +0x20 while the
		// late AMSS services start, and the adjacent table reaches +0x40, so
		// retain the complete aperture up to the MGP control block.
		LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x60, Width: Width16,
		},
		// DC18's late peripheral initialiser publishes byte commands at offsets
		// zero and two of this second external chip-select aperture. The values
		// are retained control latches; no asynchronous device response is
		// required by the observed boot path.
		LatchedRegisterWindowProfile{
			ID: "w320-external-8bit-command-data", Address: 0x38000000,
			Size: 4, Width: Width8,
		},
	)
	// DC18 programs cursor/window bytes as packed 0x42xx..0x4axx command words,
	// then streams RGB565 pixels through the A7-set FIFO.
	profile.Panel.Protocol = ParallelPanelProtocolPackedRGB565Window424A
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
		AliasSpan:      0x80,
	}
	return profile
}

// SCHW340DC18BoardProfile describes the shared early raw-downloader platform
// with SCH-W340's exact firmware and NAND-layout identity.
func SCHW340DC18BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w340", "samsung.sch-w340.dc18", 0x08800000,
	)
	// DC18 maps the compact VIC's 0x80000400 virtual window onto the
	// 0xfffff544 physical top-page aperture after enabling its AMSS MMU table.
	// Keep that aperture connected to the same controller used before the MMU
	// transition so grouped enable/status state remains coherent.
	profile.LegacyTopVectoredInterruptOffset = 0x0544
	// The MGP host interrupt dispatcher reads the external interrupt-status
	// word at BUS_BASE+0x380 before walking its registered callbacks.
	profile.SparseBusRegisterOffsets = append(
		profile.SparseBusRegisterOffsets,
		0x0380, 0x0780, 0x0b80, 0x0f80,
	)
	profile.SparseBusRegisterReadClearOffsets = []uint32{0x0380, 0x0780, 0x0b80, 0x0f80}
	// DC18 reads the downloader-owned preload footer as one fixed 0x13ecc-byte
	// object. The archive stops immediately before it, but a completed handset
	// download has programmed the whole object (including ECC for otherwise empty
	// codewords), not merely its header. Materialise the programmed zero padding
	// so the NAND controller does not report the omitted tail as erased-codeword
	// failures.
	//
	// An entirely empty table cannot finish DC18's native provisioning: its final
	// step writes the four-byte table version to nvm/preload_ver, but the parent
	// directory is normally made while walking table entries. Reconstruct the one
	// generated metadata entry needed for that invariant. Its payload is the
	// footer's own zero version word, so this does not fabricate an archived
	// handset asset.
	preloadFooter := make([]byte, 0x00013ecc)
	copy(preloadFooter, profile.NANDInitialData[0].Data)
	preloadFooter[4] = 1 // entry count
	const preloadRecord = 12
	copy(preloadFooter[preloadRecord:], "nvm/preload_ver")
	preloadFooter[preloadRecord+0x80] = 8 // source offset: header version word
	preloadFooter[preloadRecord+0x84] = 4 // source length
	profile.NANDInitialData[0].Data = preloadFooter
	// DC18 opens its internal MMC volume as "mmc1" before TFS4 reports task
	// startup to Main Task.  The inherited W830 idle-status latch leaves that
	// asynchronous discovery permanently asleep; publish the MSM6280 SDCC
	// command completions through logical interrupt 70 instead.
	for i, register := range profile.BootControlReadOnlyRegisters {
		if register.Offset == 0x0c34 {
			profile.BootControlReadOnlyRegisters = append(
				profile.BootControlReadOnlyRegisters[:i],
				profile.BootControlReadOnlyRegisters[i+1:]...,
			)
			break
		}
	}
	profile.BootControlSDCCControllers = []QualcommSDCCControllerConfig{{
		Base: 0x0c00, CardPresent: true,
		GroupStatusOffset: 0x90,
		GroupMask:         0x01,
	}}
	// The provisioned DC18 UI updates the MSM6280 GPIO/output latch at +0x480
	// with halfword stores while bringing up its late handset peripherals.
	profile.BootControlHalfwordOffsets = append(profile.BootControlHalfwordOffsets, 0x0480)
	// DC18's UIM task programs +0x4100 with the same word-wide MSM6280 reset
	// sequence as SCH-W320 and CK06. Its UART core ISR is registered on logical
	// IRQ 0x35, which the retained second-level table routes through the compact
	// VIC rather than exposing as a first-level source.
	configureQualcommLegacyUARTWordController(&profile, 0x4100)
	profile.BootControlLegacyUARTReceiveData = []QualcommLegacyUARTReceiveData{{
		Controller:                0x4100,
		InterruptSource:           17,
		UseVectoredController:     true,
		VectoredGroupStatusOffset: 0x8c,
		VectoredGroupMask:         0x02,
		DelayInstructions:         4_000_000,
		// DC18 asserts BREAK while the UIM reset line is low, waits twenty
		// REX ticks, and releases it with STOP_BREAK (0x60). Delay the ATR until
		// that stabilization timer has moved the driver into its TS receive state.
		// Reads at +0x10 expose interrupt status; the receive FIFO remains at +0x0c.
		ReceiveCommand:        0x04,
		ActivationCommand:     0x60,
		PulseReceiveInterrupt: true,
		EchoTransmit:          true,
		T0Card:                true,
		Data:                  []byte{0x3b, 0x00},
	}}
	if profile.TimeTickClock != nil {
		// DC18 never programs the sleep-clock match register inherited from the
		// wrapped W830 platform. Its REX idle task instead relies on the legacy
		// free-running 100 Hz TIME_TICK_INT to dispatch delayed filesystem work.
		clock := *profile.TimeTickClock
		clock.PeriodicInterruptHz = 100
		profile.TimeTickClock = &clock
	}
	if profile.VectoredInterrupt != nil {
		// The raw retail loader leaves TIMETICK_INT (logical source 21) enabled.
		// With the MSM6280 reverse source packing it occupies second-bank bit 2.
		interrupts := *profile.VectoredInterrupt
		// Source 13 is the sleep-clock/alarm event that releases the EFS
		// startup task; source 21 is the free-running scheduler tick.
		interrupts.ResetEnabledSources[1] = (1 << 10) | (1 << 2)
		profile.VectoredInterrupt = &interrupts
	}
	// DC18's raw-NAND probe toggles EBI2_CFG0 bit 3 and samples compact-VIC
	// group 14 child bit 1 at CHIP_BASE+0x488. The status aperture takes
	// precedence over the legacy flat NAND-ready alias, so connect the exact
	// request bit to the grouped source used by this OEMSBL.
	profile.BootControlGroupedStatusResponses = []QualcommBootGroupedStatusResponse{{
		Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
		GroupStatusOffset: 0x88, GroupMask: 0x02,
	}, {
		// Erase and final program completion use the sibling child bit without
		// an EBI request-bit handshake.
		Offset: 0x0380, NANDReadyMask: 0x01,
		GroupStatusOffset: 0x88, GroupMask: 0x01,
	}}
	// DC18's retained PBL verifier reports fatal configuration errors through a
	// mask-ROM routine just below the archived 0x00101000 PBL fragment. Bind the
	// non-returning ABI so an invalid host handoff fails at its actual boundary
	// instead of executing the zero-filled gap up to the next image.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-pbl-fatal",
		Contract: HLEContractQualcommPBLFatal,
		Address:  0x000fff84, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// The downloadable DC18 OEMSBL expands the final AMSS data segment over the
	// flash-driver registry. A retail boot chain republishes that retained
	// environment at the loader epilogue; model the same exact-build boundary.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-amss-flash-environment", Contract: HLEContractSamsungAMSSFlashEnvironment,
		Address: 0x000a1514, Mode: cpu.ModeARM, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-amss-bulk-zero", Contract: HLEContractSamsungAMSSBulkZero,
		Address: 0x0142c3d0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-sbi-transaction", Contract: HLEContractSamsungW340SBITransaction,
		Address: 0x005d39aa, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-sbi-transaction-buffered", Contract: HLEContractSamsungW340SBITransaction,
		Address: 0x005d3940, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The late AMSS radio initialiser uses the relocated synchronous SBI read
	// wrapper. Its native path waits for a PMIC-controller completion signal;
	// route that exact-build entry through the same idle-peripheral contract.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-sbi-transaction-late", Contract: HLEContractSamsungW340SBITransaction,
		Address: 0x01d900c0, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-pmic-adc-conversion", Contract: HLEContractSamsungW340PMICADCConversion,
		Address: 0x00505a7a, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The archived AP and MGP images do not include the retail modem companion
	// which clears the provisional RF-settled byte before the startup UI checks
	// it. Intercept only the exact store instruction, retaining all native RF
	// sampling and its surrounding initialisation side effects.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w340-rf-settled-deferred", Contract: HLEContractSamsungW340RFSettledDeferred,
		Address: 0x01a2e598, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	// DOG has already entered its native monitor loop when Main Task publishes
	// TASK_START_SIG, so its one-shot MC_ACK_SIG never reaches Main. Skip only
	// that exact wait instruction; all watchdog code and subsequent task-start
	// handshakes continue to execute natively.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-dog-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d70e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// GSDI consumes operator/UIM provisioning supplied by the retail modem
	// environment before it acknowledges TASK_START_SIG.  The archived AP/MGP
	// pair has no producer for that state, so skip only Main Task's corresponding
	// acknowledgement wait and continue starting the remaining native tasks.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-gsdi-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d1d8, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// GSTK is the SIM Toolkit peer of GSDI and depends on the same unavailable
	// operator provisioning.  Preserve the subsequent startup sequence by
	// bypassing only its matching Main Task acknowledgement wait.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-gstk-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d1f2, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// The retained callback service has no retail companion provider and cannot
	// publish its one-shot startup acknowledgement.  Its task remains available
	// for later native callbacks; only Main Task's initial wait is absent here.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-callback-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d4c0, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// QVP APP is present in the archive but its retail multimedia peer is not;
	// let Main Task continue after the native start signal without manufacturing
	// any QVP state or disabling the task itself.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-qvp-app-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d65e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// QVPPL is the corresponding playback pipeline and lacks the same retail
	// multimedia provider.  Its neighbouring QVPIO task acknowledges natively,
	// so keep this compatibility boundary specific to QVPPL's wait.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-qvppl-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d6a8, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// QTV's video renderer is another optional multimedia client whose DSP-side
	// provider is outside the archived pair.  Leave its task intact and bypass
	// only the one-shot boot acknowledgement wait.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-qtv-render-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d780, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// QTV audio is the final sibling using that unavailable DSP provider.  Keep
	// its task and all audio device setup native, skipping only the boot-time ACK
	// that the missing companion would otherwise complete.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-qtv-audio-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d7a4, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// QTV_TASK10 is the last optional QTV worker and shares the same absent DSP
	// service.  The following BCX tasks acknowledge natively through their
	// retained interface table, so this remains the final multimedia-only gate.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-qtv-worker-start-ack",
		Contract: HLEContractSamsungW340DOGStartAcknowledgement,
		Address:  0x01d8d84e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	// The empty DC18 preload footer does not contain the optional phonebook
	// deletion bitmap.  Its helper assumes the file exists and dereferences the
	// nil IFILE returned by the native manager. Preserve the helper's documented
	// false result at its own ABI boundary, as on the sibling W320 build.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-optional-phonebook-preload",
		Contract: HLEContractSamsungOptionalPreloadFile,
		Address:  0x00fa88e8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The same empty preload footer omits the indexed multimedia defaults read
	// by this sibling helper.  Native callers already treat a false result as an
	// absent optional asset; avoid the otherwise unconditional nil IFILE read.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-optional-multimedia-preload",
		Contract: HLEContractSamsungOptionalPreloadFile,
		Address:  0x00fa9b82, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// A later-loaded BREW module contains the same optional indexed-file reader
	// with its own file-manager singleton.  Its empty-media failure path has the
	// same unchecked IFILE dereference, and its callers likewise accept false.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-optional-module-preload",
		Contract: HLEContractSamsungOptionalPreloadFile,
		Address:  0x01d291c4, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The BCX client issues synchronous command 0x0226 and validates the fixed
	// nine-byte controller identity before allowing Main Task to continue. The
	// controller image is not part of the archived handset firmware set, so
	// provide only that hardware-owned identity at the native query boundary.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-bcx-firmware-identity",
		Contract: HLEContractSamsungW340BCXFirmwareIdentity,
		Address:  0x01d8de4a, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w340-uim-clock-configuration",
		Contract: HLEContractSamsungW340UIMClockConfiguration,
		Address:  0x010427ba, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// This is the exact `str r0, [r4, #0x18]` publication after the final
		// native shared-resource lookup. It is also the boundary at which the
		// earlier UTF and dictionary lookup results are available.
		ID:       "w340-image-resource-offset",
		Contract: HLEContractSamsungW340ImageResourceOffset,
		Address:  0x01041ae8, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The downloader provider's GetColor fallback returns EUNSUPPORTED for
		// selectors 1..16, leaving every AEEDisp system-colour slot black. The
		// sibling DC18 handset retains the loader-provided palette shown here.
		ID:       "w340-display-color",
		Contract: HLEContractSamsungW340DisplayColor,
		Address:  0x00572fdc, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// AEEDisp resolves built-in font IDs 0x8000..0x8002 through a provider
		// retained by the retail loader. Without it, DC18 installs the physical
		// framebuffer in the font slots and reports a zero line height.
		ID:       "w340-font-metrics",
		Contract: HLEContractSamsungW340FontMetrics,
		Address:  0x00058e34, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls,
		HLECallProfile{
			ID: "w340-font-draw", Contract: HLEContractSamsungW340FontDraw,
			Address: 0x07fd1380, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		HLECallProfile{
			ID: "w340-font-measure", Contract: HLEContractSamsungW340FontMeasure,
			Address: 0x07fd1384, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		HLECallProfile{
			ID: "w340-font-info", Contract: HLEContractSamsungW340FontInfo,
			Address: 0x07fd1388, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
	)
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// BREW's class loader copies registered class records through a checked
		// memcpy wrapper. In a downloader-only boot the retained access-policy
		// state is absent, so every mapped source and destination is rejected.
		ID:       "w340-pointer-access-policy",
		Contract: HLEContractSamsungW340PointerAccessPolicy,
		Address:  0x00042b8c, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// AEE_GetConMgr fatals when the loader-retained singleton is null. Publish
		// the quiescent offline provider at this exact accessor boundary.
		ID:       "w340-connection-manager",
		Contract: HLEContractSamsungW340ConnectionManager,
		Address:  0x0054b0d8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	profile.HLECalls = append(profile.HLECalls,
		HLECallProfile{
			// EVT_APP_START reaches this instruction immediately after MainApp has
			// been selected from the native event table.  The retail loader retains
			// the lifecycle callback at object+0x28; the downloader image leaves it
			// zero, which makes the native start handler tear the applet down before
			// it can create IdleApp.  Publish it before preserving the original
			// `movs r6, #0` instruction semantics.
			ID:       "w340-main-applet-lifecycle-prestart",
			Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address:  0x012b3692, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		HLECallProfile{
			// Event 0x7000 reaches this exact state load after constructing MainApp.
			// Supply the absent retained lifecycle callback while preserving the
			// rest of the native event handler.
			ID:       "w340-main-applet-lifecycle-start",
			Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address:  0x012b7cd6, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		HLECallProfile{
			// Event 0x800c repeats the same readiness gate in its update path.
			ID:       "w340-main-applet-lifecycle-update",
			Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address:  0x012b7e7c, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
	)
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// IdleApp calls this native wrapper for every event-0x7000 refresh. On
		// the first call, enter the otherwise-unreachable native carousel
		// initializer; subsequent calls enter its native updater directly.
		ID:       "w340-idle-carousel-lifecycle",
		Contract: HLEContractSamsungW340IdleCarouselLifecycle,
		Address:  0x01959ec4, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// IdleApp requests SECIA class 0x010127d6 here. The native static-table
		// fallback is present in the archived image, but its index-four provider is
		// deliberately unsupported by both platform variants; the retail loader
		// normally supplies the missing implementation. Publish its quiescent
		// interface at the CreateInstance boundary so the all-or-nothing IdleApp
		// constructor can finish.
		ID:       "w340-idle-applet-dependency",
		Contract: HLEContractSamsungW340IdleAppletDependency,
		Address:  0x01349f68, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The native class-0x0100638f request completes, but the missing retail
		// lifecycle provider leaves its output word at sp+0x28 null. Emulate the
		// following load while supplying the quiescent retained interface.
		ID:       "w340-idle-sim-main-target",
		Contract: HLEContractSamsungW340IdleSimMainTarget,
		Address:  0x01351376, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The retained loader releases the temporary class-manager result after the
		// handoff, clearing IdleApp's paired interface at r4+0x18. Restore that
		// interface while emulating the synthetic target's quiescent activation.
		ID:       "w340-idle-sim-main-activation",
		Contract: HLEContractSamsungW340IdleSimMainActivation,
		Address:  0x01351670, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// IdleApp has constructed the OEM annunciator object by this point, but
		// its retained-loader vtable has a null lifecycle entry. The retail
		// implementation only activates the already-created status-bar object.
		ID:       "w340-annunciator-start",
		Contract: HLEContractSamsungW340AnnunciatorStart,
		Address:  0x004d4b18, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The archived class constructor leaves its retained provider at r4+0x28
		// null. Emulate the following load while publishing the quiescent extended
		// interface expected through vtable slots +0x4c and +0xc4.
		ID:       "w340-idle-extended-provider",
		Contract: HLEContractSamsungW340IdleExtendedProvider,
		Address:  0x0134edac, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The downloader-only loader publishes foreground records as the retained
		// secondary type 0x01011b98. The retail bridge aliases those records to the
		// primary 0x010060d2 path before IdleApp evaluates the payload.
		ID:       "w340-idle-primary-notification",
		Contract: HLEContractSamsungW340IdlePrimaryNotification,
		Address:  0x00c67ae2, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// Class 0x0100638f operation 0xb returns successfully without filling its
		// output word at sp+0x28. Emulate the following load while publishing the
		// retained startup interface consumed through vtable slot +0xc4.
		ID:       "w340-startup-primary-interface",
		Contract: HLEContractSamsungW340StartupPrimaryInterface,
		Address:  0x01a22064, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The paired operation 0x13 has the same retained-output dependency at
		// sp+0x24. Supply its quiescent interface while emulating the load.
		ID:       "w340-startup-secondary-interface",
		Contract: HLEContractSamsungW340StartupSecondaryInterface,
		Address:  0x01a220ba, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// MGPCC samples this dynamically linked MDSP frame counter before and
		// after programming the host interface. A downloader-only boot resolves
		// the veneer to an inert retained symbol, leaving the highest-priority
		// task in a tight equality loop and starving the UI task.
		ID:       "w340-mgp-frame-counter",
		Contract: HLEContractSamsungW340MGPFrameCounter,
		Address:  0x00d58ed0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// W340 decodes the LCD D/C line at address bit 7: OEMSBL writes 16-bit
	// register indexes at chip-select +0 and their values at +0x80.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
		AliasSpan:      0x80,
	}
	profile.Panel.Protocol = ParallelPanelProtocolIndexedRGB565Window454647
	// DC18 gives BREW applets the identity-mapped high arena below the MSM6280
	// clock block. The first 64 KiB remain the boot-control register aperture;
	// executable heaps and applet stacks occupy the remainder up to 0x84000000.
	// The shell allocates 32 KiB slots and probes their 64-byte headers (the
	// first observed probes are 0x807fffc0 and 0x80807fc0).
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "w340-brew-high-ram", Kind: MemorySparseRAM,
		Address: 0x80010000, Size: 0x03ff0000,
	})
	// The MGP loader copies its ARM7 image into the 32 KiB code/shared-RAM
	// aperture and exchanges boot pointers in the image header.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
		Address: 0x90108000, Size: 0x00008000,
	}, MemoryRegionProfile{
		// The ARM7's local data/scratch range 0x10000..0x1f140 is visible to
		// the application processor immediately above the code bank.
		ID: "w340-mgp-data-ram", Kind: MemorySparseRAM,
		Address: 0x90110000, Size: 0x0000f140,
	})
	// DC18 asserts +0x0c while uploading the companion image, clears it to
	// release the ARM7, and waits for the image's shared ready byte at +0x29e0.
	profile.SamsungMGP = &SamsungMGPProfile{
		// AMSS snapshots the complete 0x240-byte MGP table beginning at
		// 0x9011f000, so the control tail extends through 0x9011f240.
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0xa0,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	// The host-side MGP service initialises the halfword interface at +0x00/+0x0c
	// before it publishes its command descriptor. Late AMSS startup also reads
	// the status banks at +0x10/+0x20/+0x40; retain the complete aperture up to
	// the active MGP control block, as on the sibling DC18 board.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x60, Width: Width16,
		},
	)
	return profile
}

// SCHW350CK06BoardProfile describes the shared early raw-downloader platform
// with SCH-W350's exact firmware and NAND-layout identity.
func SCHW350CK06BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w350", "samsung.sch-w350.ck06", 0x08f80000,
	)
	// CK06's AMSS clears the second UART configuration words with 32-bit STR
	// operations. OEMSBL still shares the same controller with narrower
	// accesses, so expose the evidenced mixed-width aperture.
	promoteQualcommLegacyUARTToMixedWidth(&profile, 0x4200)
	// CK06's UIM task uses the intervening MSM6280 controller at +0x4100.
	// Its traced reset/configuration sequence and word-wide FIFO accesses match
	// DC18 exactly, including compact-VIC source 17 child bit 1. Attach the
	// stateful T=0 transport so AMSS can complete ATR and USIM APDU discovery.
	configureQualcommLegacyUARTWordController(&profile, 0x4100)
	profile.BootControlLegacyUARTReceiveData = []QualcommLegacyUARTReceiveData{{
		Controller:        0x4100,
		InterruptSource:   55,
		DelayInstructions: 0,
		EchoTransmit:      true,
		T0Card:            true,
		Data:              []byte{0x3b, 0x00},
	}}
	profile.LegacyInterruptCascade = &QualcommInterruptCascadeProfile{
		VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
	}
	// Late AMSS hardware setup writes its 0x00100203 configuration word to
	// CHIP_BASE +0x039c. The value is not polled as a completion signal, so a
	// board-specific latch is sufficient.
	profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, 0x039c)
	// CK06 selects PMIC slave 0x38 through the controller configuration word and
	// reads that slave's identification register at address 1. The returned low
	// six bits must equal 0x38; zero or an address echo restarts hardware setup.
	profile.BootControlSBIReadResponses = append(
		profile.BootControlSBIReadResponses,
		QualcommSBIReadResponse{Controller: 0x5000, Address: 0x01, Value: 0x38},
	)
	// CK06 masks the five raw primary inputs during startup and enters its
	// on-screen UCDMA download mode when the idle 0x1f value becomes 0x1b.
	// Expose that evidenced active-low boot input without assigning it the
	// unrelated END/power-key meaning used by adjacent handsets.
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{{
		ID: "download", InputLine: 2, ActiveLow: true,
	}}
	profile.NANDReportsErasedECCCodewords = false
	// The downloader deliberately omits the handset-specific 0x019a0000
	// manufacturing block. Its bootstrap state byte is nevertheless present
	// on a device and accepts only zero (unprovisioned) or one (provisioned).
	// Seed the privacy-safe unprovisioned state and let CK06 create the rest.
	// Claiming the provisioned value without the handset's complete signed
	// manufacturing record fails its bootstrap validation.
	profile.NANDInitialData = append(profile.NANDInitialData, FlashSeed{
		Offset: 0x019a0004,
		Data:   []byte{0},
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-static-bss-zero",
		Contract: HLEContractSamsungW350StaticBSSZero,
		Address:  0x000a0040, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// The archived downloader lacks the factory EFS/NV record which identifies
	// the handset's home operator. Trap only the CK06 UI's NV 0x1301 dispatch;
	// every other NV request and the complete UIM transaction path stay native.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-operator-provisioning",
		Contract: HLEContractSamsungW350OperatorProvisioning,
		Address:  0x00578d0e, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-bootstrap-verified-firmware",
		Contract: HLEContractQualcommBootstrapVerifiedFirmware,
		Address:  0x00113d30, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// CK06's OEMSBL calls 0x001129a8 from three hardware-transition wrappers
	// (including the unconditional wrapper at 0x000a092c). The address lies in
	// the progressive image's zero-filled resident segment, while every caller
	// ignores its result and continues through LR. Preserve that missing PBL ABI
	// boundary instead of letting the CPU fall through zeroes into an unrelated
	// resident helper.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-resident-thumb-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x001129a8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// CK06 imports one ARM callback from 0x001478c8, inside its zero-filled
	// BOOT/NOTUSED program segment. The handset's preceding boot environment
	// supplies that resident ABI; the downloader package intentionally does
	// not. Its sole call site consumes no return value, so preserve registers
	// and return through LR without patching guest bytes.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-resident-boot-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x001478c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// CK06's late AMSS hardware-registration sequence imports two more ARM
	// callbacks from the same erased BOOT/NOTUSED resident segment. The first is
	// passed the resident descriptor at 0x00147898 and the second receives zero;
	// both callers discard the return value before continuing device setup. Keep
	// the unavailable boot-environment ABI register-transparent, just like the
	// earlier callback above, instead of executing erased 0xff words as an SVC.
	profile.HLECalls = append(profile.HLECalls,
		HLECallProfile{
			ID:       "w350-resident-registration-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x00147968, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		HLECallProfile{
			ID:       "w350-resident-registration-finalize",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x00147970, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	)
	// A new CK06 filesystem completes with an NV rebuild and calls the reboot
	// veneer at 0x013b0078 from this one instruction. Trap the exact call site,
	// rather than the shared fatal/restart implementation, so genuine AMSS fatal
	// paths remain visible while the successful provisioning path cold-boots with
	// its newly committed NAND contents.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-nv-rebuild-power-cycle",
		Contract: HLEContractSamsungPowerCycle,
		Address:  0x0131cb38, Mode: cpu.ModeThumb, Return: HLEReturnPowerCycle,
	})
	// CK06's OEMSBL samples bit 2 at secondary input +0x440 before it
	// releases the startup timer path. The adjacent W830 board instead wires
	// its evidenced idle input to bit 4, so keep this value board-specific.
	profile.SecondaryClockReadOnlyRegisters = append(
		profile.SecondaryClockReadOnlyRegisters,
		QualcommSecondaryClockReadOnlyRegister{
			Offset: qualcommSecondaryClockDisabledStatusOffset,
			Value:  0x00000004,
		},
	)
	// The same startup routine reads back the watchdog service latch after
	// writing one and treats bit 0 as completion.
	profile.BootControlWatchdogReadable = true
	// CK06's late GPIO helper resolves its fourth input group through
	// CHIP_BASE+0x0940. That word is reserved in the older INTCTL layout, just
	// as it is on DA05, and no external line is asserted on the deterministic
	// board at reset.
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
	)
	// CK06's raw-NAND factory first verifies the reset interface selector in
	// EBI2_CFG0, then sets bit 3 and requires compact-VIC group 14 child bit 1
	// at CHIP_BASE+0x488 to follow it. Program/erase completion uses the sibling
	// child bit, matching the same MSM6280 controller on the adjacent raw builds.
	profile.BootControlGroupedStatusResponses = []QualcommBootGroupedStatusResponse{
		{
			Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
			GroupStatusOffset: 0x88, GroupMask: 0x02,
		},
		{
			Offset: 0x0380, NANDReadyMask: 0x01,
			GroupStatusOffset: 0x88, GroupMask: 0x01,
		},
	}
	// CK06 drives its 16-bit LCD command FIFO at external chip-select
	// 0x38000000 and streams RGB565 payload words through the adjacent +4
	// data aperture.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x38000000,
		DataAddress:    0x38000004,
	}
	profile.Panel.Protocol = ParallelPanelProtocolPackedRGB565Window424A
	// Late AMSS uses address bit 18 to select the value port of a separate
	// indexed external device. It writes controller values such as
	// register 0x21 = 0x2010, reads selected registers back, and polls the
	// command port's clear ready status. These are not coordinates or pixels
	// for the packed LCD FIFO above.
	profile.IndexedHalfwordRegisterPorts = []IndexedHalfwordRegisterPortProfile{{
		ID:             "w350-indexed-external-registers",
		CommandAddress: 0x20000000,
		DataAddress:    0x20040000,
	}}
	// The handset also initializes a small secondary display through a
	// write-only 16-bit command/data pair at +0/+4. It has no observed status
	// or framebuffer feedback into the boot chain, so retain only the bounded
	// output aperture instead of aliasing it to the primary 240x320 surface.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID: "w350-secondary-panel-output", Address: 0x40000000,
			Size: 6, Width: Width16,
		},
	)
	return profile
}

// SCHW410CL10BoardProfile describes the shared early raw-downloader platform
// with SCH-W410's exact firmware and NAND-layout identity.
func SCHW410CL10BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w410", "samsung.sch-w410.cl10", 0x09100000,
	)
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{{
		ID: "end", InputLine: 4, ActiveLow: true,
	}}
	return profile
}

// SCHW300DA04BoardProfile retains only the shared raw-download board facts
// needed to start DA04. Model-specific peripherals are added when execution
// reaches and evidences them.
func SCHW300DA04BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w300", "samsung.sch-w300.da04", 0x085c0000,
	)
	// DA04 writes 16-bit LCD register indexes at chip-select +0 and their
	// values at address-bit-7 +0x80 during its first hardware pass.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
		AliasSpan:      0x80,
	}
	profile.Panel.Protocol = ParallelPanelProtocolIndexedRGB565Window454647
	return profile
}

// SCHW420CD16BoardProfile retains only the shared raw-download board facts
// needed to start CD16. Model-specific peripherals are added when execution
// reaches and evidences them.
func SCHW420CD16BoardProfile() BoardProfile {
	return samsungRawDownloadBoardProfile(
		"samsung.sch-w420", "samsung.sch-w420.cd16", 0x0d5c0000,
	)
}

// SPHW4200DC17BoardProfile selects the KTF sibling's exact firmware and NAND
// identity while retaining the shared MSM6280 raw-download device contracts.
func SPHW4200DC17BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sph-w4200", "samsung.sph-w4200.dc17", 0x0e600000,
	)
	// DC17 probes its removable-card SDCC at CHIP_BASE+0x0c00 while TFS4 is
	// starting. Its native filesystem startup requires a memory card to finish
	// CMD8/CMD55/ACMD41 discovery; the related W830
	// profile uses +0x0c34 for a different fixed idle-status contract, so
	// replace that inherited register only on this board.
	for i, register := range profile.BootControlReadOnlyRegisters {
		if register.Offset == 0x0c34 {
			profile.BootControlReadOnlyRegisters = append(
				profile.BootControlReadOnlyRegisters[:i],
				profile.BootControlReadOnlyRegisters[i+1:]...,
			)
			break
		}
	}
	profile.BootControlSDCCControllers = []QualcommSDCCControllerConfig{{
		Base: 0x0c00, CardPresent: true,
		// DC17 registers SDCC_INT as logical interrupt 70. The MSM6280
		// dispatcher maps IDs 67..70 to compact-VIC source 19's group at
		// +0x90 in descending bit order, making ID 70 child bit 0.
		GroupStatusOffset: 0x90,
		GroupMask:         0x01,
	}}
	// DC17 is a full-touch handset. Its TSC2007 module samples the pen level on
	// GPIO 39 (0x27), bit 0 of MSM6280 GPIO group 2. GPIO 43, bit 4 of the same
	// group, is a separate board-level external-interrupt aggregate; the pen
	// callback is registered independently and must therefore publish bit 0.
	// The panel's bit-banged I2C conversion wrappers are exact-build HLE gates
	// below; pen level itself remains visible through GPIO_IN_2 bit 0.
	profile.Touchscreen = &QualcommTSC2007Profile{
		Width: 240, Height: 432,
		PenInputOffset: 0x0440, PenInputMask: 0x00000001,
		InterruptGroup: QualcommGPIOInterruptGroupProfile{
			ClearOffset: 0x0594, EnableOffset: 0x05a8,
			DetectOffset: 0x05bc, PolarityOffset: 0x05d0,
			StatusOffset:    0x05e4,
			InterruptSource: 5, UseVectoredController: true,
		},
		InterruptMask: 0x00000001,
	}
	// The shared GPIO ISR scans both hardware groups serviced by VIC source 5.
	// Touch is on group 2 (+0x5e4); group 3 (+0x5e8) has no asserted external
	// input on this board but must remain readable while the dispatcher walks it.
	profile.PrimaryClockReadOnlyRegisters = append(
		profile.PrimaryClockReadOnlyRegisters,
		QualcommPrimaryClockReadOnlyRegister{Offset: 0x05e8, Value: 0},
	)
	// DC17's UIM transport is the third MSM6280 legacy UART at
	// CHIP_BASE+0x4100. Unlike the adjacent modem UARTs, its driver uses
	// word-wide accesses for the configuration registers and FIFO. Preserve
	// those accesses while exposing the read-side SR/MISR/ISR aliases; in
	// particular, +0x08 must report transmitter state rather than echoing the
	// 0xff clock-select value written to the same address.
	configureQualcommLegacyUARTWordController(&profile, 0x4100)
	profile.BootControlLegacyUARTReceiveData = []QualcommLegacyUARTReceiveData{{
		Controller:         0x4100,
		InterruptSource:    55,
		DelayInstructions:  65_536,
		EchoTransmit:       true,
		TransmitFrameBytes: 5,
		TransmitResponse:   []byte{0x6d, 0x00},
		// Minimal direct-convention T=0 ATR. It is delivered after the UIM
		// transport has completed its reset sequence and entered the scheduler.
		Data: []byte{0x3b, 0x00},
	}}
	// MSM6280 presents the legacy QIC output through compact VIC source 17's
	// six-way second-level group. UIM is child bit 1; its registered handler is
	// the 0x2e6d61 transport ISR in DC17.
	profile.LegacyInterruptCascade = &QualcommInterruptCascadeProfile{
		VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
	}
	// The raw-NAND backend probes its EBI chip-select line by toggling bit 3
	// in EBI2_CFG0. MSM6280 reflects that line in compact-VIC group 14 child
	// bit 1, at CHIP_BASE+0x488, even while the aggregate interrupt is masked.
	profile.BootControlGroupedStatusResponses = []QualcommBootGroupedStatusResponse{
		{
			Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
			GroupStatusOffset: 0x88, GroupMask: 0x02,
		},
		{
			// Raw-NAND erase and final program completion use the sibling
			// write/error-complete child without an EBI control-bit probe.
			Offset: 0x0380, NANDReadyMask: 0x01,
			GroupStatusOffset: 0x88, GroupMask: 0x01,
		},
	}
	// DC17's RF backup manager reserves the final five blocks by scanning down
	// from block 0xfff. The AMSS raw-NAND factory and BML device table identify
	// that 4-Gbit device as Samsung EC/DC. This controller is separate from the
	// EC/5C OneNAND which OEMSBL uses to load the progressive image.
	profile.NANDReadID = 0x0000ecdc
	profile.NANDSize = 0x20000000
	// DC17 tests bit 0 of CHIP_BASE+0x274 before choosing its boot path. A set
	// bit enters the OEMSBL packet downloader at 0x000af9c0; the retail cold
	// boot path requires the strap clear so control continues into AMSS.
	profile.BootClockModeStatus = 0
	// DC17 uses the same PMIC ADC contract as the related MSM6280 handset:
	// register 0x54 publishes conversion complete, 0x4f retains the selected
	// 8-bit mode, and 0x53 supplies the completed battery sample.
	profile.BootControlSBIReadResponses = []QualcommSBIReadResponse{
		{Controller: 0x5100, Address: 0x4f, Value: 0xc1},
		{Controller: 0x5100, Address: 0x53, Value: 0xff},
		{Controller: 0x5100, Address: 0x54, Value: 0x01},
	}
	// AMSS samples the PBL-retained word at 0xfffff3a8 during its reset-vector
	// hardware pass. A cold handset starts with no retained request asserted;
	// keep the word writable because the same top-page slot is boot scratch RAM.
	profile.LegacyTopWritableOffsets = append(profile.LegacyTopWritableOffsets, 0x03a8)
	// qdspmem copies the downloader image into the shared ADSP address space with
	// its ready-state halfword initialised to 2. On hardware the newly released
	// DSP consumes that state and publishes zero before qdsptask's first poll.
	// Model only this exact image-loader handshake; the rest of the 128 MiB DSP
	// window remains ordinary sparse RAM.
	profile.MemoryWriteResponses = append(
		append([]MemoryWriteResponseProfile(nil), profile.MemoryWriteResponses...),
		MemoryWriteResponseProfile{
			MemoryID: "adsp-address-space", Offset: 0x00202f3a,
			Width: Width16, Request: 2,
			Writes: []MemoryResponseWriteProfile{{
				Offset: 0x00202f3a, Width: Width16, Value: 0,
			}},
		},
		MemoryWriteResponseProfile{
			MemoryID: "adsp-address-space", Offset: 0x00202f30,
			Width: Width16, Request: 0x0100,
			Writes: []MemoryResponseWriteProfile{
				{Offset: 0x00202f30, Width: Width16, Value: 0},
				{Offset: 0x00202f3a, Width: Width16, Value: 1},
			},
		},
	)
	// DC17's QDSP command queue submits its shared-buffer address through the
	// mailbox write-control word itself. The mailbox clears the hardware mutex
	// bit before matching control rules, so 0x80020000 is observed here as
	// 0x00020000. Complete the event-slot response, publish the module-loader
	// ready flag, and pulse the registered DSP interrupt after both shared-memory
	// updates have become visible to the ARM ISR. gl1_hw polls the ready halfword
	// through its runtime descriptor after the loader callback returns.
	profile.ADSPMailbox.ControlRules = append(
		profile.ADSPMailbox.ControlRules,
		QualcommADSPControlRuleProfile{
			Offset: 0x08, Value: 0x00020000, ResponseDelayInstructions: 1,
			Copies: []QualcommADSPMemoryCopyProfile{{
				SourceWindowID:      "external-32bit-bank-2",
				SourceOffset:        0x00000570,
				DestinationWindowID: "external-32bit-bank-2",
				DestinationOffset:   0x0000056c,
				Width:               Width32,
			}},
			Writes: []QualcommADSPMemoryWriteProfile{
				{
					WindowID: "external-16bit-bank-1", Offset: 0x00000bfc,
					Width: Width16, Value: 0,
				},
				{
					WindowID: "external-16bit-bank-1", Offset: 0x00003e4a,
					Width: Width16, Value: 1,
				},
			},
			Interrupt: &QualcommADSPInterruptProfile{
				Source: 33, UseVectoredController: true,
			},
			StartPeriodicInterrupt: true,
		},
	)
	// The GSM layer registers GSTMR_INT (compact-VIC source 29) as the DSP
	// frame-tick dispatcher. Once the module image is ready, hardware raises it
	// once per 4.615 ms GSM frame; its callback advances gl1_hw's frame counter.
	profile.ADSPMailbox.PeriodicInterrupt = &QualcommADSPPeriodicInterruptProfile{
		InstructionsPerSecond: 60_000_000,
		InterruptHz:           217,
		Interrupt: QualcommADSPInterruptProfile{
			Source: 29, UseVectoredController: true,
		},
	}
	// OEMSBL resets and probes the companion OneNAND through the interrupt and
	// command halfwords at 0x4001e482/0x4001e440 before loading AMSS.
	profile.OneNAND = &OneNANDProfile{
		Address: 0x40000000, ManufacturerID: 0x00ec, DeviceID: 0x005c,
		DieBlockOffset: 0x0800, Capacity: 0x20000000, InitialImageFromFirmware: true,
	}
	// DC17's packaged loader descriptor is normally completed by an unavailable
	// downloader stage. The OEMSBL calls this exact boundary to materialise the
	// progressive ELF before restoring the AMSS entry context at address zero.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		// The downloadable QCSBL/OEMSBL pair starts after mask-ROM PBL. OEMSBL's
		// bad-block scan is the first consumer of the missing PBL flash interface;
		// the host has already applied the selected MIBIB and factory-bad-block
		// layout, so this boundary publishes an empty scan and the retained reader.
		ID: "w4200-pbl-flash-prepare", Contract: HLEContractSamsungW4200PBLFlashPrepare,
		Address: 0x000a0b58, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		ID: "w4200-pbl-flash-read", Contract: HLEContractSamsungW4200PBLFlashRead,
		Address: 0xffffda00, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		ID: "w4200-progressive-amss-loader", Contract: HLEContractSamsungProgressiveAMSSLoad,
		Address: 0x00081b9c, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		// AMSS clears the progressive image's zero-fill segments before this
		// routine constructs the record directory consumed by boot_cfg_table.
		ID: "w4200-shared-directory-init", Contract: HLEContractSamsungSharedDirectoryInit,
		Address: 0x000a3f4c, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		// The relocated runtime performs one final BSS pass before attaching the
		// fixed descriptor to its Thumb-side global registry.
		ID: "w4200-shared-directory-attach", Contract: HLEContractSamsungSharedDirectoryAttach,
		Address: 0x00d60f84, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		ID: "w4200-tsc2007-write", Contract: HLEContractSamsungW4200TSC2007Write,
		Address: 0x01078d44, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	}, HLECallProfile{
		ID: "w4200-tsc2007-read", Contract: HLEContractSamsungW4200TSC2007Read,
		Address: 0x01078ccc, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// DC17 clears its second static-data segment at 0x08000000 before the
	// AMSS handoff. Expose the MSM6280's adjacent EBI chip-select aperture as
	// page-backed RAM so the complete address window is available without a
	// second eager 128 MiB host allocation.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "w4200-ebi-ram-bank-1", Kind: MemorySparseRAM,
		Address: 0x08000000, Size: 0x08000000,
	}, MemoryRegionProfile{
		// DC17's final two progressive-ELF records form the Qualcomm shared
		// configuration segment at 0x18000000..0x1a5e9900. Keep the bounded,
		// page-aligned aperture sparse because AMSS reads only selected tables.
		ID: "w4200-shared-config-segment", Kind: MemorySparseRAM,
		Address: 0x18000000, Size: 0x02600000,
	})
	// AMSS polls bit 1 of this external-bus status word until the controller is
	// ready. No write to the status address occurs during initialization.
	profile.ReadOnlyRegisters = append(profile.ReadOnlyRegisters, ReadOnlyRegisterProfile{
		ID: "w4200-external-bus-ready", Address: 0x30002000,
		Width: Width32, Value: 0x00000002,
	})
	// A late AMSS timing helper exchanges one byte through this independently
	// decoded external port. The firmware uses paired STRB/LDRB accessors and
	// retains the most recently published value.
	profile.LatchedRegisters = append(profile.LatchedRegisters, LatchedRegisterProfile{
		ID: "w4200-external-byte-port", Address: 0x38000000,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-byte-control", Address: 0x38010000,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The cold-start GPIO table contains eight byte-wide output ports at
		// consecutive halfword addresses. The common setter accesses each with
		// STRB and may sample the retained route bit before a secondary write.
		ID: "w4200-external-gpio-group-0", Address: 0x38020000,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-1", Address: 0x38020002,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-2", Address: 0x38020004,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-3", Address: 0x38020006,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-4", Address: 0x38020008,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-5", Address: 0x3802000a,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-6", Address: 0x3802000c,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w4200-external-gpio-group-7", Address: 0x3802000e,
		Width: Width8, ResetValue: 0,
	})
	// The cold-start hardware table publishes external-bus control words through
	// these dedicated registers before entering the remaining peripheral
	// initialisers. Relocated AMSS later writes the 32-bit control words directly.
	profile.LatchedRegisters = append(profile.LatchedRegisters,
		LatchedRegisterProfile{
			ID: "w4200-external-bus-control", Address: 0x30002004,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-timing", Address: 0x30002008,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-chip-select", Address: 0x30002010,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-control-14", Address: 0x30002014,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-clock", Address: 0x3000201c,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-mask", Address: 0x30002020,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-control-28", Address: 0x30002028,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-mode", Address: 0x3000202c,
			Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-external-bus-control-30", Address: 0x30002030,
			Width: Width32, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-control", Address: 0x30006000,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-address-low", Address: 0x30006008,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-address-high", Address: 0x3000600a,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-data", Address: 0x3000600c,
			Width: Width32, AdditionalWidths: []Width{Width16}, AllowSubwordOffsets: true, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-2-control", Address: 0x30007000,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-2-address-low", Address: 0x30007008,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-2-address-high", Address: 0x3000700a,
			Width: Width16, ResetValue: 0,
		},
		LatchedRegisterProfile{
			ID: "w4200-indirect-bus-2-data", Address: 0x30007800,
			Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
		},
	)
	// DC17's relocated AMSS installs its interrupt dispatch callbacks in the
	// three-word CHIP_BASE+0x0d00 control record. The values are retained and
	// consumed by software; no autonomous side effect is required here.
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x0d04, 0x0d10, 0x0d14, 0x0d18, 0x0d1c,
	)
	// DC17 drives the main indexed RGB565 controller through a halfword
	// command/data pair in the same external chip-select aperture.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x30005000,
		DataAddress:    0x30005004,
	}
	profile.Panel = DCSPanelConfig{
		Width: 240, Height: 432, Protocol: ParallelPanelProtocolIndexedRGB565Window210213,
	}
	return profile
}

// SCHW450CK10BoardProfile starts the original MSM6250-era flat boot image at
// reset while retaining only shared Samsung/Qualcomm MMIO primitives.
func SCHW450CK10BoardProfile() BoardProfile {
	profile := samsungLegacyFlatBoardProfile(
		"samsung.sch-w450", "samsung.sch-w450.ck10",
	)
	// CK10's reset-resident flash table contains one 128 MiB small-page entry,
	// manufacturer/device 0x20/0x79.
	profile.NANDReadID = 0x00002079
	// The main QCIF panel exposes packed 0..175 and 0..219 address bounds in
	// registers 0x44/0x45 and a packed cursor in register 0x21.
	profile.Panel = DCSPanelConfig{
		Width: 176, Height: 220, Protocol: ParallelPanelProtocolIndexedRGB565Window4445,
	}
	// The direct reset stub owns the full 32-bit external platform word. Drop
	// the two inherited read-only halfword straps used by later raw QCSBLs.
	readOnly := profile.ReadOnlyRegisters[:0]
	for _, register := range profile.ReadOnlyRegisters {
		if register.ID != "external-platform-feature-low" &&
			register.ID != "external-platform-feature-high" {
			readOnly = append(readOnly, register)
		}
	}
	profile.ReadOnlyRegisters = readOnly
	// The reset prologue snapshots this cold-reset status word into its IRAM
	// handoff record before configuring memory. It is not a writable latch.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x3400, Value: 0},
	)
	// Reset publishes a word and then toggles its low byte at +0x3404.
	profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, 0x3404)
	profile.BootControlMixedWidthOffsets = append(profile.BootControlMixedWidthOffsets, 0x3404)
	profile.BootControlByteWritableOffsets = append(profile.BootControlByteWritableOffsets, 0x3404)
	profile.PrimaryClockWritableOffsets = append(
		profile.PrimaryClockWritableOffsets,
		0x0108,
		0x0120, 0x0124, 0x0128, 0x0138, 0x013c, 0x0140,
		0x0150, 0x0154, 0x0158, 0x015c, 0x0160, 0x0164, 0x016c, 0x0170,
		0x0174, 0x0178, 0x017c, 0x0180,
		0x01ec, 0x01f0, 0x01f4, 0x01f8, 0x01fc,
		0x0204, 0x0208, 0x020c, 0x0210, 0x0214, 0x0218,
		0x0248, 0x024c, 0x0250, 0x0254, 0x0270, 0x0274,
		0x02e4, 0x02e8,
	)
	profile.PrimaryClockReadOnlyRegisters = append(
		profile.PrimaryClockReadOnlyRegisters,
		QualcommPrimaryClockReadOnlyRegister{Offset: 0x0104, Value: 0},
		// OS memory bring-up requires the ready line at bit 24 while treating
		// bit 28 as an error indication.
		QualcommPrimaryClockReadOnlyRegister{Offset: 0x0168, Value: 0x01000000},
	)
	profile.PrimaryClockInterruptRegisters = append(
		profile.PrimaryClockInterruptRegisters,
		QualcommPrimaryClockInterruptRegister{
			StatusOffset: 0x0244,
			ClearOffset:  0x024c,
			Bits: []QualcommPrimaryClockInterruptBit{
				{Bit: 1, Source: 45},
				{Bit: 2, Source: 46},
			},
		},
	)
	profile.ReadOnlyRegisters = append(profile.ReadOnlyRegisters, ReadOnlyRegisterProfile{
		// The reset tail samples the low five external-memory status bits before
		// entering the two bounded RAM-initialisation helpers.
		ID: "w450-external-memory-status", Address: 0x48000070,
		Width: Width32, Value: 0,
	}, ReadOnlyRegisterProfile{
		// Encoded 0x20/0x79 manufacturer and device selectors used to
		// choose the matching small-page flash reader from the reset table.
		ID: "w450-bootstrap-selectors", Address: 0x64000308,
		Width: Width32, Value: 0x103c8000,
	}, ReadOnlyRegisterProfile{
		ID: "w450-bootstrap-result", Address: 0x64000320,
		Width: Width32, Value: 0x000000ff,
	})
	profile.AddressedStorageWindows = append(
		profile.AddressedStorageWindows,
		AddressedStorageWindowProfile{
			ID: "w450-bootstrap-page", Address: 0x64000000, Size: 0x200,
			CommandID: "w450-bootstrap-page-command", CommandAddress: 0x64000304,
			CommandWidth: Width32, AddressMask: 0xfffffe00,
		},
	)
	// The reset sequence masks this one clock-domain register before touching
	// external-memory timing. It is outside the inherited primary clock
	// controller aperture and is not read back by the handoff path.
	profile.LatchedRegisters = append(profile.LatchedRegisters, LatchedRegisterProfile{
		ID: "w450-clock-domain-mask", Address: 0x84001400,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-switch-control", Address: 0x84001304,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-hardware-control", Address: 0x84001330,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-hardware-divider", Address: 0x84001334,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-hardware-source", Address: 0x84001338,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-hardware-mode", Address: 0x8400133c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-plan-parameter-0", Address: 0x84001380,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-plan-parameter-1", Address: 0x84001384,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-plan-commit", Address: 0x84001394,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-clock-plan-limit", Address: 0x84001398,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The post-memory reset tail publishes its masked bootstrap word through
		// this exact companion-controller register.
		ID: "w450-bootstrap-word", Address: 0x6400031c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-bootstrap-control", Address: 0x64000300,
		Width: Width32, ResetValue: 0,
		WritePulses: []LatchedRegisterWritePulseProfile{
			{Mask: 0xffffffff, Value: 1, Sources: []uint8{45}},
			{Mask: 0xffffffff, Value: 7, Sources: []uint8{45, 46}},
			{Mask: 0xffffffff, Value: 5, Sources: []uint8{45, 46}},
			{Mask: 0xffffffff, Value: 6, Sources: []uint8{45}},
		},
	}, LatchedRegisterProfile{
		// Reset reads the full external-memory configuration word, then sets
		// its low-half enable bit with STRH.
		ID: "w450-external-memory-configuration", Address: 0x48000000,
		Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-timing", Address: 0x48000004,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-static-memory-configuration", Address: 0x48000008,
		Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-mode", Address: 0x4800000c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-timing-0", Address: 0x48000020,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-timing-1", Address: 0x48000024,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-timing-2", Address: 0x48000028,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-timing-3", Address: 0x4800002c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-control-0", Address: 0x48000030,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-control-1", Address: 0x48000034,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-control-2", Address: 0x48000038,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-control-3", Address: 0x4800003c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-bank-control", Address: 0x48000060,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-memory-bank-timing", Address: 0x48000064,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-platform-word", Address: 0x30010000,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// Early OS bring-up streams one-byte commands through this external
		// peripheral port before enabling the corresponding control line.
		ID: "w450-external-serial-command", Address: 0x38000000,
		Width: Width8, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w450-external-serial-data", Address: 0x38100000,
		Width: Width8, ResetValue: 0,
	})
	profile.LatchedRegisterWindows = append(profile.LatchedRegisterWindows, LatchedRegisterWindowProfile{
		// Reset configures the external-bus control words in the first half,
		// then programs sixteen 0x20-byte timing slots in the second half.
		ID: "w450-external-bus-control", Address: 0x63800000,
		Size: 0x400, Width: Width32,
	})
	return profile
}

// SCHW599BE30BoardProfile uses the same direct-reset flat-flash contract for
// the monolithic ARM7 firmware and its adjacent preload region.
func SCHW599BE30BoardProfile() BoardProfile {
	profile := samsungLegacyFlatBoardProfile(
		"samsung.sch-w599", "samsung.sch-w599.be30",
	)
	profile.PlatformID = "intel.pxa27x-samsung-flat-v1"
	profile.NANDReadID = 0x00009879
	// The low-vector display sequence selects commands at chip-select +0 and
	// streams their 16-bit values through the adjacent +2 halfword. A separate
	// +6 control line is cleared before programming the panel.
	profile.PanelSelectorPorts = &ParallelPanelSelectorPortProfile{
		SelectorAddress: 0x20000000,
		TransferAddress: 0x20000002,
		CommandSelect:   0,
		DataSelect:      1,
	}
	profile.Panel.Protocol = ParallelPanelProtocolIndexedRGB565Window36373839
	profile.NANDRegisterResets = append(
		profile.NANDRegisterResets,
		QualcommNANDRegisterReset{Offset: 0x0240, Value: 0},
		QualcommNANDRegisterReset{Offset: 0x0260, Value: 0},
	)
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x0734, 0x0738, 0x073c,
		0x2200, 0x2208, 0x220c, 0x2214,
	)
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x075c, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x2234, Value: 0},
	)
	// The high-vector reset stub reads physical flash through its addressed
	// page aperture and copies the selected payload into the inherited 128 MiB
	// low-address EBI RAM before transferring control there.
	// The high-vector tail samples two encoded bootstrap selector fields from
	// this read-only external status word after the command handshakes finish.
	profile.ReadOnlyRegisters = append(profile.ReadOnlyRegisters, ReadOnlyRegisterProfile{
		ID: "w599-bootstrap-selectors", Address: 0x64000308,
		// The selector fields encode Toshiba manufacturer 0x98 and the 128 MiB
		// small-page device 0x79, the only table entry large enough for this
		// build's firmware and preload regions.
		Width: Width32, Value: 0x4c3c8000,
	}, ReadOnlyRegisterProfile{
		// A separate result byte returns 0xff when the bootstrap read completes
		// without the status error bit. Other values make the reset scanner skip
		// the page and continue toward its bounded device-size limit.
		ID: "w599-bootstrap-result", Address: 0x64000320,
		Width: Width32, Value: 0x000000ff,
	}, ReadOnlyRegisterProfile{
		// Runtime memory initialisation requires the controller-ready bit before
		// it accepts the configured bank table. A clear bit enters the handset's
		// explicit 0x12340000 terminal-error path.
		ID: "pxa27x-memory-controller-status", Address: 0x48000040,
		Width: Width32, Value: 0x00004000,
	})
	// Bootstrap read commands encode a byte-aligned flash page plus low flag
	// bits. The controller exposes the selected 512-byte page through a fixed
	// read-only data aperture.
	profile.AddressedStorageWindows = append(
		profile.AddressedStorageWindows,
		AddressedStorageWindowProfile{
			ID: "w599-bootstrap-page", Address: 0x64000000, Size: 0x200,
			CommandID: "w599-bootstrap-page-command", CommandAddress: 0x64000304,
			CommandWidth: Width32, AddressMask: 0xfffffe00,
		},
	)
	// The reset stub samples the low half of PXA27x BSCNTR1 before applying its
	// board drive-strength setting.
	profile.LatchedRegisters = append(profile.LatchedRegisters, LatchedRegisterProfile{
		ID: "pxa27x-memory-strength-1", Address: 0x48000050,
		Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-panel-control", Address: 0x20000006,
		Width: Width16, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The low-vector clock bootstrap programs the same source/divider word
		// into both companion clock domains before peripheral initialisation.
		ID: "w599-clock-domain-0", Address: 0x50c0000c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-clock-domain-1", Address: 0x58c0000c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// A high-vector helper publishes its masked bootstrap word here before
		// returning to the reset sequencer.
		ID: "w599-bootstrap-word", Address: 0x6400031c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-bootstrap-control", Address: 0x64000300,
		Width: Width32, ResetValue: 0,
		WritePulses: []LatchedRegisterWritePulseProfile{
			{Mask: 0xffffffff, Value: 1, Sources: []uint8{45}},
			{Mask: 0xffffffff, Value: 7, Sources: []uint8{45, 46}},
			{Mask: 0xffffffff, Value: 5, Sources: []uint8{45, 46}},
			{Mask: 0xffffffff, Value: 6, Sources: []uint8{45}},
		},
	}, LatchedRegisterProfile{
		ID: "pxa27x-static-memory-control-0", Address: 0x48000008,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-static-memory-control-1", Address: 0x4800000c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-static-memory-control-2", Address: 0x48000010,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-dynamic-memory-configuration", Address: 0x48000000,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-dynamic-memory-control", Address: 0x48000004,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The memory timing table writes its high-numbered entries through this
		// paired configuration aperture.
		ID: "pxa27x-memory-timing-index", Address: 0x48000030,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-memory-timing-value", Address: 0x48000034,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The runtime's width-aware register writer programs this table-selected
		// control word one accumulated byte at a time through 32-bit stores.
		ID: "pxa27x-memory-runtime-control", Address: 0x48000044,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-memory-bank-control-0", Address: 0x48000020,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-memory-bank-control-1", Address: 0x48000024,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-memory-bank-control-2", Address: 0x48000028,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "pxa27x-memory-bank-control-3", Address: 0x4800002c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		// The Thumb reset tail asserts this external bootstrap latch before
		// transferring into the ordinary low-vector image.
		ID: "w599-external-bootstrap", Address: 0x63800000,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-clear", Address: 0x63800008,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-mode", Address: 0x63800020,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-status", Address: 0x63800024,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-control", Address: 0x63800028,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-mask", Address: 0x63800030,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-mask-high", Address: 0x63800034,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-mask-final", Address: 0x63800038,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-mask-limit", Address: 0x6380003c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data", Address: 0x63800040,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-high", Address: 0x63800044,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-final", Address: 0x63800048,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-limit", Address: 0x6380004c,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-end", Address: 0x63800050,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-tail", Address: 0x63800054,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-data-terminal", Address: 0x63800058,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-command", Address: 0x63800100,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-command-high", Address: 0x63800120,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-command-final", Address: 0x63800140,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-command-limit", Address: 0x63800160,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-status-low", Address: 0x63800104,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-status-high", Address: 0x63800124,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-status-final", Address: 0x63800144,
		Width: Width32, ResetValue: 0,
	}, LatchedRegisterProfile{
		ID: "w599-external-bootstrap-status-limit", Address: 0x63800164,
		Width: Width32, ResetValue: 0,
	})
	// The first low-vector routine transmits through the byte-wide FIFO at
	// CHIP_BASE+0x1b3c, identifying the surrounding MSM6xxx legacy UART bank.
	const legacyUARTBase = uint32(0x1b30)
	// The same reset sequence enables the bounded Qualcomm companion control
	// latch at CHIP_BASE+0x2204 before leaving high-vector startup.
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x05f0, 0x05f4, 0x060c, 0x0630,
		legacyUARTBase+qualcommLegacyUARTFIFOOffset,
		0x0710, 0x072c, 0x0730, 0x2204, 0x2210, 0x2220, 0x2224,
	)
	profile.BootControlMixedWidthOffsets = append(
		profile.BootControlMixedWidthOffsets,
		legacyUARTBase+qualcommLegacyUARTFIFOOffset,
	)
	// This companion generation exposes its bootstrap pin state where the
	// older INTCTL layout placed GPIO interrupt-detect group four. Both sampled
	// pins are asserted once the deterministic external endpoint is ready.
	profile.BootControlInterruptStatusAliases = append(
		profile.BootControlInterruptStatusAliases,
		QualcommInterruptStatusAlias{Offset: 0x50, Bank: 1},
	)
	profile.BootControlLegacyUARTControllers = append(
		profile.BootControlLegacyUARTControllers, legacyUARTBase,
	)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		profile.BootControlHalfwordOffsets = append(
			profile.BootControlHalfwordOffsets, legacyUARTBase+relative,
		)
	}
	return profile
}

func samsungLegacyFlatBoardProfile(id, firmwareBuildID string) BoardProfile {
	profile := samsungSmallPageRawDownloadBoardProfile(id, firmwareBuildID, 0)
	profile.PlatformID = "qualcomm.arm7-samsung-flat-v1"
	profile.CPUCompatibility.UserSystemSPSRReadAsCPSR = true
	// These archives already contain the downloader-produced physical bytes.
	// Do not invent the completion footer used by later MIBIB packages.
	profile.NANDInitialData = nil
	profile.NANDReportsErasedECCCodewords = false
	// Original reset code runs instead of an unavailable-mask-ROM HLE, so no
	// retained PBL tables or entry register contract is supplied by the host.
	profile.PBLServiceTableAddress = 0
	profile.PBLServiceTableHeaderSize = 0
	profile.PBLHeaderFeatureDataAddress = 0
	profile.PBLHeaderFeatures = nil
	profile.PBLFixedFeatureDataAddress = 0
	profile.PBLFixedFeatureFirst = 0
	profile.PBLFixedFeatureSlotCount = 0
	profile.PBLFixedFeatures = nil
	profile.PBLLegacyFeatureDataAddress = 0
	profile.PBLSharedDataAddress = 0
	profile.PBLSharedDataSize = 0
	profile.PBLStackPointer = 0
	return profile
}

// SCHW210CK12BoardProfile selects CK12's exact small-page raw-download board.
func SCHW210CK12BoardProfile() BoardProfile {
	return samsungW270CompatibleBoardProfile(
		"samsung.sch-w210", "samsung.sch-w210.ck12", 0x093c0000,
	)
}

// SCHW240CL28BoardProfile selects CL28's exact small-page raw-download board.
func SCHW240CL28BoardProfile() BoardProfile {
	profile := samsungW240W290BoardProfile(
		"samsung.sch-w240", "samsung.sch-w240.cl28", 0x0bac0000,
	)
	// CL28's partition extent exceeds 128 MiB and its PBL publishes the
	// corresponding 256 MiB small-page device geometry.
	profile.NANDSize = 0x10000000
	return profile
}

// SCHW290CK10BoardProfile selects CK10's exact small-page raw-download board.
func SCHW290CK10BoardProfile() BoardProfile {
	return samsungW240W290BoardProfile(
		"samsung.sch-w290", "samsung.sch-w290.ck10", 0x05d00000,
	)
}

func samsungW240W290BoardProfile(id, firmwareBuildID string, packagedEnd uint64) BoardProfile {
	profile := samsungSmallPageRawDownloadBoardProfile(id, firmwareBuildID, packagedEnd)
	// This QCSBL generation relocates into the retained 0x78010000 PBL RAM
	// bank and uses its upper half for working data and the initial stack.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "small-page-pbl-runtime", Kind: MemoryRAM,
		Address: 0x78010000, Size: 0x00020000,
	})
	// The relocated QCSBL reset stub publishes 0x78028000 as its full-
	// descending stack top. The missing mask ROM uses the same retained RAM
	// bank when it calls the profiled QCSBL entry routine.
	profile.PBLStackPointer = 0x78028000
	// This PBL generation expands the NAND feature records retained in its
	// source image into a fixed selector table consumed directly by QCSBL.
	profile.PBLLegacyFeatureDataAddress = 0
	profile.PBLFixedFeatureDataAddress = 0xffff601c
	profile.PBLFixedFeatureFirst = 0x000000ff
	profile.PBLFixedFeatureSlotCount = 0x0000013f
	profile.PBLFixedFeatures = []QualcommPBLFixedFeature{
		{Selector: 0x0ff, Value: 0x0020},
		{Selector: 0x100, Value: 0x4000},
		{Selector: 0x102, Value: 0x0200},
		{Selector: 0x103, Value: 0x004a},
		{Selector: 0x119, Value: 0x0014},
	}
	// Revision one selects the retained 0x78010000 QCSBL relocation and its
	// matching internal-RAM stack layout.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x0270, Value: 1},
	)
	return profile
}

// SCHW330CK06BoardProfile selects CK06's exact small-page raw-download board.
func SCHW330CK06BoardProfile() BoardProfile {
	return samsungSmallPageRawDownloadBoardProfile(
		"samsung.sch-w330", "samsung.sch-w330.ck06", 0x05700000,
	)
}

// SCHW390CK11BoardProfile selects CK11's exact small-page raw-download board.
func SCHW390CK11BoardProfile() BoardProfile {
	return samsungSmallPageRawDownloadBoardProfile(
		"samsung.sch-w390", "samsung.sch-w390.ck11", 0x05700000,
	)
}

// SCHW460CC26BoardProfile selects CC26's exact small-page raw-download board.
func SCHW460CC26BoardProfile() BoardProfile {
	return samsungSmallPageRawDownloadBoardProfile(
		"samsung.sch-w460", "samsung.sch-w460.cc26", 0x05870000,
	)
}

func samsungSmallPageRawDownloadBoardProfile(id, firmwareBuildID string, packagedEnd uint64) BoardProfile {
	profile := samsungRawDownloadBoardProfile(id, firmwareBuildID, packagedEnd)
	profile.NANDPageSize = 0x00000200
	profile.NANDEraseBlockSize = 0x00004000
	// This small-page generation publishes 0x2000 physical erase blocks in
	// its retained PBL feature data: 128 MiB independent of the smaller
	// packaged partition extent.
	profile.NANDSize = 0x08000000
	return profile
}

// SCHW270CL28BoardProfile uses the older small-page NAND geometry published
// by CL28's MIBIB and retained PBL feature table.
func SCHW270CL28BoardProfile() BoardProfile {
	return samsungW270CompatibleBoardProfile(
		"samsung.sch-w270", "samsung.sch-w270.cl28", 0x08c80000,
	)
}

func samsungW270CompatibleBoardProfile(id, firmwareBuildID string, packagedEnd uint64) BoardProfile {
	profile := samsungRawDownloadBoardProfile(id, firmwareBuildID, packagedEnd)
	profile.NANDPageSize = 0x00000200
	profile.NANDEraseBlockSize = 0x00004000
	profile.PBLStackPointer = 0x03f40000
	profile.PBLFixedFeatureDataAddress = 0x78002000
	profile.PBLFixedFeatureFirst = 0x000000f9
	profile.PBLFixedFeatureSlotCount = 5
	profile.PBLFixedFeatures = []QualcommPBLFixedFeature{
		{Selector: 0xf9, Value: 0x20},
		{Selector: 0xfa, Value: 0x200},
		{Selector: 0xfc, Value: 0x4000},
		{Selector: 0xfd, Value: 0x14},
	}
	// CL28's QCSBL uses three callbacks from a retained PBL interface table.
	// Their original low-vector entries are populated by unavailable mask-ROM
	// state, so bind only the exact bad-block, page-read, and fatal ABIs here.
	profile.HLECalls = append(profile.HLECalls,
		HLECallProfile{
			ID: "w270-pbl-nand-read", Contract: HLEContractQualcommPBLNANDRead,
			Address: 0x03d4b000, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		HLECallProfile{
			ID: "w270-pbl-fatal", Contract: HLEContractQualcommPBLFatal,
			Address: 0x03d4b004, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		HLECallProfile{
			ID: "w270-pbl-nand-bad-block", Contract: HLEContractQualcommPBLNANDBadBlock,
			Address: 0x03d4b008, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	)
	// CL28's older QCSBL clears the bounded CHIP_BASE +0x18..+0x24 latch group
	// while installing its exception-mode stacks.
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x0018, 0x001c, 0x0020, 0x0024,
		// The retained PBL's clock bootstrap publishes the decoded PLL fields
		// through this paired control latch before returning to QCSBL.
		0x0300, 0x0304, 0x032c,
		0x0a1c, 0x0a2c,
	)
	profile.BootControlInterruptWindowWritableOffsets = append(
		profile.BootControlInterruptWindowWritableOffsets,
		0x0920, 0x0924,
	)
	// The retained PBL publishes its runtime data in this one-page RAM window
	// before entering QCSBL. CL28's entry initializes its first 0x18 bytes.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "w270-pbl-runtime-data", Kind: MemoryRAM,
		Address: 0x7f000000, Size: 0x00001000,
	})
	// CL28 tests bit 0 of CHIP_BASE+0x270 before selecting its exception-mode
	// stacks. Revision one selects the evidenced 0x03f40000 stack inside EBI
	// RAM; the clear path selects 0x080b0000 beyond this board's RAM window.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x0270, Value: 1},
		// The retained PBL requires low selector two before decoding the
		// cold-reset PLL fields and publishing them through +0x0a1c/+0x0a2c.
		QualcommBootReadOnlyRegister{Offset: 0x53b8, Value: 2},
		QualcommBootReadOnlyRegister{Offset: 0x53bc, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x53c0, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x53c4, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x53c8, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x53cc, Value: 0},
		QualcommBootReadOnlyRegister{Offset: 0x53d0, Value: 0},
	)
	return profile
}

func samsungRawDownloadBoardProfile(id, firmwareBuildID string, packagedEnd uint64) BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = id
	profile.PlatformID = "qualcomm.arm9-sch-raw-v1"
	profile.FirmwareBuildID = firmwareBuildID
	// These MSM6280 handsets use Toshiba 98/CA NAND: a 2 KiB-page,
	// 64-page/block, 256 MiB device accepted by both the boot chain and AMSS's
	// NFA device table.
	profile.NANDReadID = 0x000098ca
	profile.NANDSize = 0x10000000
	// The MSM6280 ECC path completes an erased-codeword transfer with its
	// operation-error bit set. AMSS then sets NAND config bit 0 and rereads the
	// 528-byte raw codeword to distinguish erased data from an actual failure.
	profile.NANDReportsErasedECCCodewords = true
	profile.Panel = DCSPanelConfig{
		Width: 240, Height: 320, Protocol: ParallelPanelProtocolIndexedRGB565,
	}
	profile.NANDInitialData = []FlashSeed{{
		Offset: packagedEnd,
		Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.OneNAND = nil
	// The raw QCSBL generation predates the service-ID table used by the
	// wrapped W830 downloads and consumes the ROM feature structure directly.
	profile.PBLLegacyFeatureDataAddress = 0xffff6044
	profile.BootControlSBIReadResponses = nil
	// The shared raw QCSBL clears this watchdog/control latch before entering
	// its NAND geometry path.
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		// The raw AMSS clock-vote dispatcher updates both halves of the
		// CHIP_BASE +0x08/+0x0c control pair. W830 already supplies +0x08;
		// the raw platform additionally exercises the adjacent word.
		0x000c,
		// AMSS samples and updates the active clock-plan word during late
		// hardware initialisation, then publishes the companion plans at +0x148
		// and +0x248. Its clock-vote transition copies the calibration pair into
		// +0x80 and the inherited +0x84 latch.
		0x0080,
		0x00a0,
		0x010c,
		0x0120,
		0x0130,
		0x0134,
		0x0148,
		0x0248,
		// The post-QDSP hardware pass publishes its 0x1001 enable word in the
		// companion control slot before updating the already observed +0xa44
		// latch.
		0x0a38,
		0x0a44,
		0x0d60,
		0x0d70,
		0x2000,
		0x2008,
		0x2010,
		0x2028,
		// AMSS initialises this peripheral control group as one bounded
		// sequence, including the adjacent +0x34/+0x38 control pair.
		0x2040,
		0x2044,
		0x2048,
		0x204c,
		0x2064,
		0x2068,
		0x206c,
		0x2074,
		0x2078,
		0x207c,
		0x2804,
		0x2808,
		0x2810,
		// The same initialiser clears the companion interrupt/status mask at
		// +0x284c before publishing the peripheral table below.
		0x284c,
		0x53a8,
		// PBL publishes its cold-boot stack transition through this scratch
		// latch before switching exception modes.
		0x5410,
	)
	// AMSS builds 32 four-word peripheral descriptors in the bounded
	// 0x2400..0x25ff hardware table.
	for offset := uint32(0x2400); offset < 0x2600; offset += 4 {
		profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
	}
	// The companion 16-entry status table has an eight-byte stride and clears
	// the second word of each entry.
	for offset := uint32(0x2984); offset < 0x2a00; offset += 8 {
		profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
	}
	// The retained PBL ROM samples bit 4 of this reset-status word before it
	// decides between cold and warm boot. A newly constructed handset is cold.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x5314, Value: 0},
		// PBL tests bit 0 before programming the adjacent cold-start clock
		// sequence. The uninitialised clock domain reports clear after reset.
		QualcommBootReadOnlyRegister{Offset: 0x5418, Value: 0},
		// AMSS treats a zero optional peripheral source pointer as absent before
		// initialising the adjacent 0x2400 control block.
		QualcommBootReadOnlyRegister{Offset: 0x2824, Value: 0},
		// The raw OEMSBL samples bit 0 of the +0x0d64 hardware-status word
		// before selecting its optional warm/peripheral startup path. No such
		// external state is asserted on a deterministic cold boot.
		QualcommBootReadOnlyRegister{Offset: 0x0d64, Value: 0},
		// The synchronous peripheral command issued through +0x0a44 completes
		// with bit 1 set in its status word.
		QualcommBootReadOnlyRegister{Offset: 0x0a4c, Value: 0x00000002},
	)
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	// The W350 OEMSBL feature enumerator samples the two halfwords preceding
	// the inherited +4 platform-status register. No optional external feature
	// line is asserted in the deterministic offline board state.
	profile.ReadOnlyRegisters = append(
		profile.ReadOnlyRegisters,
		ReadOnlyRegisterProfile{
			ID: "external-platform-feature-low", Address: 0x30010000,
			Width: Width16, Value: 0,
		},
		ReadOnlyRegisterProfile{
			ID: "external-platform-feature-high", Address: 0x30010002,
			Width: Width16, Value: 0,
		},
	)
	// After OEMSBL creates the NAND bad-block table and restarts, QCSBL
	// programs the external-memory timing bank at +0x20..+0x74 before loading
	// AMSS. These are retained control latches, not general-purpose RAM.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			// AMSS's external-bus helper publishes byte-wide command and data
			// values through offsets 0 and 2 of this chip-select aperture. Leaving
			// either address unmapped turns the valid write into a data abort.
			ID: "external-8bit-command-data", Address: 0x30000000,
			Size: 4, Width: Width8,
		},
		LatchedRegisterWindowProfile{
			ID: "qcsbl-external-memory-control", Address: 0xa0000000,
			Size: 0x00000078, Width: Width32,
		},
	)
	profile.SparseBusRegisterOffsets = append(profile.SparseBusRegisterOffsets, 0x0000, 0x0040)
	profile.SparseBusRegisterResets = []SparseWordRegisterReset{{
		Offset: 0x0040, Value: 0x80000000,
	}}
	// The raw handset samples bit 10 of this GPIO input word while publishing
	// its initial slider state. No external transition is asserted at reset.
	profile.SecondaryClockReadOnlyRegisters = append(
		profile.SecondaryClockReadOnlyRegisters,
		QualcommSecondaryClockReadOnlyRegister{Offset: 0x0444, Value: 0},
	)
	return profile
}

func promoteQualcommLegacyUARTToMixedWidth(profile *BoardProfile, base uint32) {
	wordOffsets := make(map[uint32]struct{}, len(qualcommLegacyUARTHalfwordRegisterOffsets))
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets[base+relative] = struct{}{}
	}
	halfwordOffsets := make([]uint32, 0, len(profile.BootControlHalfwordOffsets))
	for _, offset := range profile.BootControlHalfwordOffsets {
		if _, promote := wordOffsets[offset]; promote {
			profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
			profile.BootControlMixedWidthOffsets = append(profile.BootControlMixedWidthOffsets, offset)
			continue
		}
		halfwordOffsets = append(halfwordOffsets, offset)
	}
	profile.BootControlHalfwordOffsets = halfwordOffsets
}

func configureQualcommLegacyUARTWordController(profile *BoardProfile, base uint32) {
	profile.BootControlLegacyUARTControllers = append(
		profile.BootControlLegacyUARTControllers, base,
	)
	writable := make(map[uint32]struct{}, len(profile.BootControlWritableOffsets))
	for _, offset := range profile.BootControlWritableOffsets {
		writable[offset] = struct{}{}
	}
	mixed := make(map[uint32]struct{}, len(profile.BootControlMixedWidthOffsets))
	for _, offset := range profile.BootControlMixedWidthOffsets {
		mixed[offset] = struct{}{}
	}
	registers := append(
		append([]uint32(nil), qualcommLegacyUARTHalfwordRegisterOffsets[:]...),
		qualcommLegacyUARTFIFOOffset,
	)
	for _, relative := range registers {
		offset := base + relative
		if _, ok := writable[offset]; !ok {
			profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
			writable[offset] = struct{}{}
		}
		if _, ok := mixed[offset]; !ok {
			profile.BootControlMixedWidthOffsets = append(profile.BootControlMixedWidthOffsets, offset)
			mixed[offset] = struct{}{}
		}
	}
}

func samsungSCHSparseBusRegisterOffsets() []uint32 {
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

// SCHW860DA06BoardProfile is the adjacent SCH-family board contract currently
// evidenced by DA06's original QCSBL/OEMSBL. It deliberately clears the W830
// keypad map: sharing a Qualcomm platform does not prove identical handset
// matrix wiring.
func SCHW860DA06BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w860"
	profile.FirmwareBuildID = "samsung.sch-w860.da06"
	// DA06's MIBIB layout ends at 0x0a1c0000, but OEMSBL selects the EC/BA
	// 256 MiB NAND geometry. Partition extent and physical device capacity
	// are separate contracts.
	profile.NANDSize = 0x10000000
	// DA06's storage helper subtracts 0x06400000 from its 0x0fbc0000 preload-
	// table address, so the table begins at physical NAND 0x097c0000. That is
	// the same packaged-end offset at which W830 expects a different downloader
	// completion marker; inheriting the W830 bytes makes DA06 interpret the
	// following erased word as a 0xffffffff entry count. The four-piece W860
	// archive has no previous-user-media backup, so its board baseline carries
	// an empty table header (zero entry count and version) instead.
	profile.NANDInitialData = []FlashSeed{{
		Offset: 0x097c0000,
		Data:   []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.BootControlSBIReadResponses = nil
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	return profile
}

// SCHW850CF11BoardProfile starts the modem ARM9 of the MSM7600 dual-processor
// handset. The application processor remains outside this machine boundary;
// the profiled boot path is the OEMSBL maintenance/download environment.
func SCHW850CF11BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w850"
	profile.PlatformID = "qualcomm.msm7600-modem-arm9"
	profile.FirmwareBuildID = "samsung.sch-w850.cf11"
	profile.NANDSize = 0x20000000
	// The downloader package leaves the TFS4 control area erased. A newly
	// provisioned handset carries an LPCH control marker there so QCSBL can
	// derive the usable/reserved-block split before it loads OEMSBL. The marker
	// is generated metadata; no firmware or user filesystem bytes are embedded.
	profile.NANDInitialData = []FlashSeed{
		{
			Offset: 0x18e80000,
			Data:   newQualcommFlexOneNANDControlHeader("UPCH"),
		},
		{
			// A control header is active only when its following payload page is
			// programmed. Zero denotes the generated empty bad-block map.
			Offset: 0x18e81000,
			Data:   []byte{0x00},
		},
		{
			// Page 60's second 512-byte chunk is the FBA enumeration table. One
			// unflagged record scans 0x400 blocks starting at FBA 0. An erased
			// count retries past FBA 0x3ff, while an empty table suppresses the
			// partition index construction entirely.
			Offset: 0x18ebc20c,
			Data: []byte{
				0x01, 0x00, 0x00, 0x00, // record count
				0x00, 0x00, 0x00, 0x00, // record tag and flags
				0x00, 0x00, 0x00, 0x04, // start FBA and block count
			},
		},
		{
			// The terminal page of the control group is an all-zero sentinel.
			// QCSBL locates it through the programmed OOB marker and requires
			// the complete main page to compare equal to zero.
			Offset: 0x18ebf000,
			Data:   make([]byte, 0x1000),
		},
		{
			Offset: 0x18f00000,
			Data:   newQualcommFlexOneNANDControlHeader("LPCH"),
		},
		{
			Offset: 0x18f01000,
			Data:   []byte{0x00},
		},
		{
			Offset: 0x18f3c20c,
			Data: []byte{
				0x01, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x04,
			},
		},
		{
			Offset: 0x18f3f000,
			Data:   make([]byte, 0x1000),
		},
	}
	// MSM7600 enables an identity mapping for its terminal high-IRAM section,
	// while the inherited 0x78001000 MSM6xxx service-table address is absent
	// from QCSBL's first-level page table. Keep the table adjacent to, but
	// disjoint from, the PBL shared record in that mapped section.
	profile.PBLServiceTableAddress = 0xffffe100
	profile.PBLServiceTableHeaderSize = 0x30
	profile.PBLHeaderFeatureDataAddress = 0xffffe0a0
	profile.PBLHeaderFeatures = []QualcommPBLHeaderFeature{
		{Selector: qualcommPBLHeaderFlashBlockCount, Value: 0x0400},
		{Selector: qualcommPBLHeaderSLCBlockCount, Value: 0x0010},
		{Selector: qualcommPBLHeaderBadBlockLimit, Value: 0x0014},
	}
	// QCSBL preserves a 0x68-byte record supplied by the missing PBL. Its
	// entry stub receives r11 as the exclusive end of this bounded IRAM data.
	profile.PBLSharedDataAddress = 0xffffe000
	profile.PBLSharedDataSize = 0x68
	profile.OneNAND = nil
	profile.SFlashOneNAND = &QualcommSFlashOneNANDProfile{
		Address:        0xa0a00000,
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       0x20000000,
		FlexGeometry: &OneNANDFlexGeometry{
			PageSize: 0x1000, BlockCount: 0x400, SLCBoundary: 0x0f,
			SLCBlockSize: 0x40000, MLCBlockSize: 0x80000,
		},
		SpareInitialData: []FlashSeed{
			{
				// Raw FBA 0x325 page 0 (UPCH).
				Offset: 0x00c74000,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// The programmed control payload on page 1 carries the same
				// active-page marker while preserving the erased bad-block word.
				Offset: 0x00c74080,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// Page 63 pairs the programmed OOB marker with the all-zero main
				// sentinel seeded above.
				Offset: 0x00c75f80,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// Raw FBA 0x326 page 0 (LPCH). The first word is the erased
				// bad-block marker; 0xa5a5 identifies an active control page.
				Offset: 0x00c78000,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				Offset: 0x00c78080,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				Offset: 0x00c79f80,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
		},
	}
	profile.BootControlAddress = 0xb8000000
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	profile.BootControlSBIReadResponses = nil
	// MSM7600 retains this clock/interrupt control word as ordinary writable
	// state during QCSBL startup.
	profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, 0x0840)
	// At legacy INTCTL +0x04, MSM7600 exposes a readable control latch rather
	// than the MSM6xxx write-only INT_CLEAR_1 register. QCSBL performs an
	// explicit read/modify/write sequence, so override only this profiled word.
	profile.BootControlInterruptWindowWritableOffsets = append(
		profile.BootControlInterruptWindowWritableOffsets,
		0x0904,
	)
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		// MSM7600 reserves the legacy polarity-3 word as a fuse/status input.
		// Reset-zero selects the unprovisioned modem configuration fallback.
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
		// MSM7600 places an additional input/status word at legacy INTCTL
		// +0x44. QCSBL tests bit 17 before consulting IRQ status bank 1; no
		// external source is asserted during deterministic cold boot.
		QualcommGPIOInputRegister{Offset: 0x44, Value: 0},
	)
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		// The OEMSBL control sequence polls this completion word after
		// programming +0x200/+0x204. Reset-zero reports an idle controller.
		QualcommBootReadOnlyRegister{Offset: 0x0218, Value: 0},
	)
	// This QCSBL keeps its fatal-record header at 0xfffef000 and copies the
	// final 32-byte scatter record to 0xfffeffe0. Both fall inside the final,
	// bounded 4 KiB page of the MSM7600 modem IRAM bank.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "msm7600-qcsbl-iram-page", Kind: MemoryRAM,
		Address: 0xfffef000, Size: 0x00001000,
	})
	// Retain the two bounded MSM7600 clock banks discovered during QCSBL
	// initialization; unrelated addresses remain unowned.
	profile.LatchedRegisterWindows = append(profile.LatchedRegisterWindows, LatchedRegisterWindowProfile{
		// The QCSBL clock bootstrap writes the +0x0c/+0x10/+0x14 control
		// triplet and +0x84 selector, then updates the base and +0x210 mode
		// words in the same compact register bank.
		ID: "msm7600-clock-bootstrap", Address: 0xa8600000,
		Size: 0x00000214, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// QCSBL's static clock table builds four runtime banks from these exact
		// 1 KiB apertures before enabling the modem clock domains.
		ID: "msm7600-modem-clock-bank-0", Address: 0xa9400000,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-1", Address: 0xa9500400,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-2", Address: 0xa9600800,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-3", Address: 0xa9700c00,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// The clock-control path updates base/+0x04/+0x0c and publishes its
		// completion/mode words at +0x834/+0x838 in this bank.
		ID: "msm7600-clock-control", Address: 0xb0007000,
		Size: 0x0000083c, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// The application-side branches of the same clock table build aligned
		// addresses from +0x000 through the observed +0xf08 words.
		ID: "msm7600-app-clock-bank", Address: 0xb0400000,
		Size: 0x00001000, Width: Width32,
	})
	profile.LatchedRegisters = append(profile.LatchedRegisters, LatchedRegisterProfile{
		ID: "msm7600-delay-progress", Address: 0xb8200000,
		Width: Width32,
	}, LatchedRegisterProfile{
		// Revision 1 selects the PBL service-table ABI carried in r7/r8. A
		// reset-zero value selects the older link-time feature pointer instead,
		// which this MSM7600 QCSBL does not receive from its mask ROM handoff.
		ID: "msm7600-hardware-revision", Address: 0xa9000270,
		Width: Width32, ResetValue: 0x10000000,
	}, LatchedRegisterProfile{
		// The QCSBL clock initialiser publishes selector 9 through this single
		// MSM7600 control word before it starts the remaining clock sequence.
		ID: "msm7600-clock-selector", Address: 0xa8500004,
		Width: Width32,
	})
	return profile
}

func newQualcommFlexOneNANDControlHeader(magic string) []byte {
	// This is the first-generation header emitted by the Qualcomm flash
	// manager for an empty control map: no previous control blocks, generation
	// one for the selected half and globally, and the detected 1x1x0x400
	// Flex-OneNAND geometry. The halfword following the reserved-block count is
	// left erased, matching a freshly programmed header page.
	header := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0xff, 0xff,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x00,
	}
	copy(header, magic)
	return header
}

func validProfileID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 255 && strings.IndexByte(value, 0) < 0
}
