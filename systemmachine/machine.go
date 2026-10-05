// Package systemmachine composes firmware loaders, board profiles, CPU
// backends, and guest-neutral system devices into headless whole-phone
// machines. It deliberately sits above package system so generic buses and
// devices never need firmware-model checks.
package systemmachine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"reflect"
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
	SnapshotSchema                       = "aram-system-machine-state-v1"
	schw830QCSBLBoundaryInstructions     = uint64(1_195_629)
	schw830QCSBLBoundaryPC               = uint32(0x000a07d8)
	samsungW320QCSBLUsedSize             = uint32(0x0000484f)
	samsungW320QCSBLLoadAddress          = uint32(0x00080000)
	samsungW320PBLVerifiedCopy           = uint32(0x01880000)
	samsungW320PBLVerifiedRecord         = uint32(0x0050aab6)
	samsungW320PBLVerifiedStatus         = uint32(0x0050aa70)
	samsungW340AMSSHeapBaseAddress       = uint32(0x02480260)
	samsungW340AMSSHeapBase              = uint32(0x06100000)
	samsungW340StaticModuleList          = uint32(0x02480274)
	samsungW340StaticModuleCount         = uint32(129)
	samsungW340IdleModuleEntry           = uint32(0x0134b361)
	samsungW340MainModuleEntry           = uint32(0x012b09a9)
	samsungW340Class7003ModuleEntry      = uint32(0x0059296b)
	samsungW340Class7017ModuleEntry      = uint32(0x01027b1f)
	samsungW340StaticClassList           = uint32(0x02480478)
	samsungW340OEMClassTable             = uint32(0x01fa7804)
	samsungW340OEMClassCount             = uint32(187)
	samsungW340StaticClassBacking        = uint32(0x07fd0000)
	samsungW340SECIAClassID              = uint32(0x010127d6)
	samsungW340SECIAFactory              = uint32(0x0003d531)
	samsungW340RetainedServiceStore      = uint32(0x027d0d54)
	samsungW340RetainedServiceObject     = uint32(0x07fd1000)
	samsungW340RetainedServiceVTable     = uint32(0x07fd1040)
	samsungW340RetainedServiceNoop       = uint32(0x07fd10c0)
	samsungW340RetainedServiceIdentity   = uint32(0x07fd10c4)
	samsungW340RetainedServiceAddRef     = uint32(0x015d7acf)
	samsungW340RetainedServiceRelease    = uint32(0x015d7ab3)
	samsungW340ConnectionManagerStore    = uint32(0x03c55990)
	samsungW340ConnectionManagerObject   = uint32(0x07fd1400)
	samsungW340ConnectionManagerVTable   = uint32(0x07fd1440)
	samsungW340IdleDependencyObject      = uint32(0x07fd1540)
	samsungW340IdleDependencyVTable      = uint32(0x07fd1580)
	samsungW340IdleSimMainObject         = uint32(0x07fd1680)
	samsungW340IdleSimMainVTable         = uint32(0x07fd16c0)
	samsungW340IdleExtendedObject        = uint32(0x07fd1880)
	samsungW340IdleExtendedVTable        = uint32(0x07fd18c0)
	samsungW340StartupPrimaryObject      = uint32(0x07fd1b00)
	samsungW340StartupPrimaryVTable      = uint32(0x07fd1b40)
	samsungW340StartupSecondaryObject    = uint32(0x07fd1d80)
	samsungW340StartupSecondaryVTable    = uint32(0x07fd1dc0)
	samsungW340MainLifecycleCallbacks    = uint32(0x07fd1f00)
	samsungW340IdleCarouselStore         = uint32(0x02def0f8)
	samsungW340IdleCarouselManagerState  = uint32(0x02def1f0)
	samsungW340IdleCarouselManager       = uint32(0x02def1f8)
	samsungW340IdleCarouselConfiguration = uint32(0x07fd1f20)
	samsungW340IdleCarouselCreator       = uint32(0x01959d72)
	samsungW340IdleCarouselUpdater       = uint32(0x00f26392)
	samsungW340IdlePrimaryNotification   = uint32(0x010060d2)
	samsungW340IdleSecondaryNotification = uint32(0x01011b98)
	samsungW340DisplayProviderStore      = uint32(0x03b6f2e0)
	samsungW340DisplayProviderObject     = uint32(0x07fd1100)
	samsungW340DisplayProviderVTable     = uint32(0x07fd1140)
	samsungW340DisplayProviderRelease    = uint32(0x07fd11c0)
	samsungW340DisplayProviderLookup     = uint32(0x07fd11d0)
	samsungW340DisplayProviderAddRef     = uint32(0x00572f35)
	samsungW340DisplayProviderCreate     = uint32(0x00572f97)
	samsungW340FontInterfaceObject       = uint32(0x07fd1300)
	samsungW340FontInterfaceVTable       = uint32(0x07fd1340)
	samsungW340FontInterfaceDraw         = uint32(0x07fd1380)
	samsungW340FontInterfaceMeasure      = uint32(0x07fd1384)
	samsungW340FontInterfaceInfo         = uint32(0x07fd1388)
	samsungW340BREWNVMFileTable          = uint32(0x0237f080)
	samsungW340BREWNVMFilename           = uint32(0x07fd1200)
	samsungW340PosDetTimerOwnerStore     = uint32(0x0405536c + 0x54)
	samsungW340PosDetTimerOwnerSlot      = uint32(0x07fd1240)
	samsungW340PosDetTimerManager        = uint32(0x07fd1280)
	samsungW340ModuleInitializerStore    = uint32(0x0245365c)
	samsungW340REXSchedulerAddress       = uint32(0x0254f028)
	samsungW340REXSchedulerEnabled       = uint32(1)
	samsungW340FlashEnvironment          = uint32(0x02399544)
	samsungW340BCXPoolPointer            = uint32(0x023936ec)
	samsungW340BCXPoolBacking            = uint32(0x0606a000)
	samsungW340BCXPoolSize               = uint32(0x00096000)
	samsungW340BCXInterfaceBase          = uint32(0x02393594)
	samsungW340BCXInterfaceStride        = uint32(0x54)
	samsungW340BCXUnavailableCallback    = uint32(0x0205c395)
	samsungW340BCIMAVSignalMaskStore     = uint32(0x02393530)
	samsungW340BCIMAVSignalMask          = uint32(0x00000007)
	samsungW340BCIMSignalMaskStore       = uint32(0x023931a4)
	samsungW340BCIMSignalMask            = uint32(0x307f00f3)
	samsungW340InterruptDispatch         = uint32(0x023936f8)
	samsungW340InterruptZeroHandler      = uint32(0x01ce2e7d)
	samsungW340EFS2DeviceCallback        = uint32(0x023997d4)
	samsungW340EFS2DeviceVTable          = uint32(0x02399828)
	samsungW340POSIXFileVTable           = uint32(0x023998c0)
	samsungW340EFS2AbsentDeviceHandler   = uint32(0x07fcff00)
	samsungW340InterruptTable            = uint32(0x02572008)
	samsungW340GroupedInterruptTable     = uint32(0x025729fc)
	samsungW340DefaultInterruptHandler   = uint32(0x00c879e5)
	samsungW340TimeTickInterruptEntry    = samsungW340InterruptTable + 21*0x34
	samsungW340IRQHandlerStore           = uint32(0x03c53370)
	samsungW340IRQHandler                = uint32(0x00c875cf)
	samsungW340FontRegistry              = uint32(0x0256ff7c)
	samsungW340FontDescriptor            = uint32(0x07fc0000)
	samsungW340FontObject                = uint32(0x07fc0020)
	samsungW340CharsetStore              = uint32(0x02571fa0)
	samsungW340CharsetDescriptor         = uint32(0x07fc0400)
	samsungW340CharsetDecodeTable        = uint32(0x07fc0440)
	samsungW340CharsetEncodeTable        = uint32(0x07fc0640)
	samsungW340CharsetEncodeOffsets      = uint32(0x07fc0740)
	samsungW340REXObjectListAnchor       = uint32(0x02571fbc)
	samsungW340REXObjectListHeadStore    = samsungW340REXObjectListAnchor + 0x10
	samsungW340CleanupListHeadStore      = uint32(0x02482c18)
	samsungW340CleanupListSentinel       = uint32(0x07fc0a00)
	samsungW340CleanupListNextStub       = uint32(0x07fc0a20)
	samsungW340SDSSScriptStore           = uint32(0x02569a24)
	samsungW340SDSSScriptBacking         = uint32(0x07fc0c00)
	samsungW340NVBufferStore             = uint32(0x02394a7c)
	samsungW340NVBufferBacking           = uint32(0x07fc0e00)
	samsungW340DeviceTable               = uint32(0x02397478)
	samsungW340DeviceObjectBacking       = uint32(0x07fc0e80)
	samsungW340DeviceObjectSize          = uint32(0x48)
	samsungW340DeviceCommonCallback      = uint32(0x012cdbe9)
	samsungW340DeviceCallback0           = uint32(0x00bdb2f1)
	samsungW340DeviceCallback1           = uint32(0x00bd8401)
	samsungW340DeviceCallback2           = uint32(0x012cc409)
	samsungW340UIInitializationFlag      = uint32(0x02575574)
	samsungW340BREWResourceOffsetsStore  = uint32(0x03fd6bf0)
	// DC18's FNT container stores these three shared resources at fixed byte
	// offsets. The native EFS lookups normally publish them, but a freshly
	// formatted handset has no directory entries from which to recover them.
	samsungW340BREWUTFOffset          = uint32(0x02dc4b30)
	samsungW340BREWDictionaryOffset   = uint32(0x0000135c)
	samsungW340BREWTableOffset        = uint32(0x00b2fcb8)
	samsungW340BREWTableOffsetStore   = samsungW340BREWResourceOffsetsStore + 8
	samsungW340LSMCallbackTableStore  = uint32(0x02455dd4)
	samsungW340LSMCallbackTable       = uint32(0x0115d828)
	samsungW340DefaultDispatchObject  = uint32(0x02573d08)
	samsungW340DefaultDispatchSlots   = uint32(8)
	samsungW340DIAGBufferStore        = uint32(0x02396498)
	samsungW340DIAGBufferBacking      = uint32(0x02ffb398)
	samsungW340RadioStateStore        = uint32(0x0261e330)
	samsungW340RadioStateBacking      = uint32(0x0422b1a4)
	samsungW340RadioWorkspace         = uint32(0x02572fd0)
	samsungW340RadioWorkspacePrimary  = uint32(0x04031fe0)
	samsungW340RadioWorkspaceBuffer   = uint32(0x040305cc)
	samsungW340RadioWorkspaceTail     = uint32(0x04032510)
	samsungW340MGPQueuePoolStore      = uint32(0x02692e60)
	samsungW340MGPQueuePoolBacking    = uint32(0x07c00000)
	samsungW340MGPQueuePoolSize       = uint32(0x00019640)
	samsungW340FontGlyphMap           = uint32(0x07fc1000)
	samsungW340BCXIdentityState       = uint32(0x03fd67cc)
	samsungW340BCXIdentityData        = samsungW340BCXIdentityState + 0x0c
	samsungW4200AMSSResetCookie       = uint32(0x12345678)
	samsungW4200AMSSResetCookieStore  = uint32(0xffffdff0)
	samsungW4200PBLFlashContextStore  = uint32(0x0800854c)
	samsungW4200PBLFlashContext       = uint32(0xffffdb00)
	samsungW4200PBLFlashReadStub      = uint32(0xffffda00)
	samsungW4200PBLFlashReadOffset    = uint32(0x34)
	samsungW4200SharedDirectory       = uint32(0x02b8e000)
	samsungW4200SharedDirectorySize   = uint32(0x0000089c)
	samsungW4200SharedDirectoryStore  = uint32(0x08008804)
	samsungW4200RuntimeDirectoryStore = uint32(0x08871000)
	// DC17's downloader-only feature table occupies the second WBT block. Flash
	// normalization replaces that block with the selected MIBIB generation, so
	// retain the table from the source piece before constructing physical NAND.
	samsungW4200FeatureConfigSource = int64(0x00020034)
	samsungW4200FeatureConfigSize   = uint32(0x000004f8)
)

// samsungW340DisplayDefaultColors is the sixteen-entry RGBVAL palette retained
// by the DC18 retail display loader. The values were recovered from the live
// AEEDisp object of the sibling SCH-W320/DC18 build; RGBVAL stores R/G/B in the
// upper three bytes, hence opaque white is 0xffffff00.
var samsungW340DisplayDefaultColors = [...]uint32{
	0x00000000, 0xffffff00, 0x00000000, 0xffffff00,
	0x00000000, 0xffffff00, 0x00000000, 0x00000000,
	0xffffff00, 0xffffff00, 0xffffff00, 0x00000000,
	0x00000000, 0x00000000, 0xffffff00, 0x00000000,
}

var (
	ErrClosed                      = errors.New("system machine is closed")
	samsungW340BCXFirmwareIdentity = [...]byte{
		0x08, 0x0a, 0x40, 0x04, 0x10, 0x25, 0x20, 0x00, 0x00,
	}
	ErrIncompatibleMedia  = errors.New("media state is incompatible with the system machine")
	ErrIncompatibleState  = errors.New("snapshot is incompatible with the system machine")
	ErrUnsupportedBackend = errors.New("CPU backend lacks required whole-system capabilities")
	ErrUnsupportedControl = errors.New("system machine has no such input control")
	ErrUnsupportedMachine = errors.New("recognized firmware has no system machine")
)

// samsungW340StaticModuleEntries reconstructs the generated BREW module table
// that the retail loader normally copies into retained RAM. The firmware image
// contains 128 module entry thunks; the table reserves one additional trailing
// slot before the static-class list.
var samsungW340StaticModuleEntries = [...]uint32{
	samsungW340IdleModuleEntry, samsungW340MainModuleEntry,
	samsungW340Class7003ModuleEntry, samsungW340Class7017ModuleEntry,
	0x0015cecb, 0x0016028d, 0x0016b039, 0x0016fed1,
	0x0017b9f5, 0x00241697, 0x00242eed, 0x002460b5,
	0x00250efd, 0x00253649, 0x00253c31, 0x004b8f41,
	0x004bb965, 0x004c430f, 0x004eac25, 0x004ed71d,
	0x004f22f9, 0x004f29d1, 0x004fb991, 0x00500fb5,
	0x00548429, 0x0056d535, 0x00571aab, 0x00594689,
	0x00825cc7, 0x00825cd3, 0x00825ce1,
	0x00825cef, 0x00833d71, 0x0083d513, 0x0084b8c3,
	0x00859219, 0x00862041, 0x0086bfa3, 0x0087506d,
	0x00876c7d, 0x0087b05d, 0x008bd077, 0x0092da61,
	0x0093c0a5, 0x00b8e4cb, 0x00b8f8b9, 0x00b98c5b,
	0x00b9b4f5, 0x00baa6a3, 0x00bae96f, 0x00bcc597,
	0x00bf30e9, 0x00c7629b, 0x00c80fdd, 0x00f04d45,
	0x00f0b28f, 0x00f0d4d9, 0x00f35f09, 0x00f3f001,
	0x00f4dc61, 0x00f69fc1, 0x00fe1871, 0x01018375,
	0x01028fa3, 0x0103e521, 0x0105591b,
	0x0125eeeb, 0x01286119, 0x0128e8c9, 0x0128fc41,
	0x012a0df5, 0x012a315d, 0x012d3b59,
	0x012f42e1, 0x01357031, 0x013665b7,
	0x0136de3d, 0x013789a7, 0x015c8da7, 0x015de357,
	0x015df0b1, 0x015f323f, 0x015fa84d, 0x01663aad,
	0x01693321, 0x0169df37, 0x016d1beb, 0x019393c7,
	0x019592e1, 0x0195c9bb, 0x01964bcd, 0x01966b61,
	0x019715d9, 0x019777a1, 0x0197d7c5, 0x019807d1,
	0x019ec953, 0x01a02bfb, 0x01a11d43, 0x01a1356b,
	0x01a13ed3, 0x01a22325, 0x01cb086b, 0x01cbba8d,
	0x01ccd447, 0x01cd452f, 0x01cf3d8f, 0x01cf6311,
	0x01cf9271, 0x01d00e7b, 0x01d72eef, 0x01d7b6c3,
	0x01d7ce47, 0x02007f7d, 0x02009503, 0x0201670b,
	0x0201e4eb, 0x02022bb7, 0x0202dda1, 0x02034dfd,
	0x0203aa2d, 0x0204459b, 0x020659d1, 0x020f379d,
	0x020f5c0f, 0x020fbcb1, 0x02108ad9, 0x02116125,
}

var samsungW340DownloadableClassRecords = [...]struct {
	address uint32
	classID uint32
}{
	{0x0237aff0, 0x0100752f},
	{0x0237b004, 0x01007537},
	{0x0237c3e8, 0x01007530},
	{0x0237ede4, 0x01007532},
	{0x0237ee68, 0x01007534},
	{0x02380744, 0x01007535},
	{0x02380768, 0x01007531},
	{0x023815f0, 0x01007538},
	{0x023867a8, 0x01007539},
	{0x0238a1f4, 0x0100752e},
	{0x023902c0, 0x01007536},
}

// qualcommInterruptCascadeSink models the MSM6280 hierarchy: the legacy QIC
// does not drive the ARM core directly. Depending on the board, its enabled IRQ
// output is either a compact-VIC source or one raw child in a second-level group.
type qualcommInterruptCascadeSink struct {
	target       *system.QualcommVectoredInterruptController
	source       uint8
	statusOffset uint32
	mask         uint32
	statusWindow *system.LatchedRegisterWindow
	windowOffset uint32
	windowMask   uint32
}

func (s qualcommInterruptCascadeSink) SetInterruptLine(
	line cpu.InterruptLine,
	asserted bool,
) error {
	if line != cpu.InterruptIRQ {
		return nil
	}
	if s.target == nil {
		return fmt.Errorf("nil Qualcomm interrupt cascade target")
	}
	if s.statusWindow != nil {
		if err := s.statusWindow.SetBits(s.windowOffset, s.windowMask, asserted); err != nil {
			return fmt.Errorf("drive Qualcomm sub-interrupt status: %w", err)
		}
	}
	if s.mask == 0 {
		return s.target.SetSource(s.source, asserted)
	}
	return s.target.SetGroupedSource(s.statusOffset, s.mask, asserted)
}

// CPUBackendMode selects one of the portable interpreter execution tiers.
// An explicitly supplied Options.Backend remains available for research and
// third-party cores.
type CPUBackendMode string

const (
	CPUBackendPrecise  CPUBackendMode = "precise"
	CPUBackendJIT      CPUBackendMode = "jit"
	CPUBackendJITLoops CPUBackendMode = "jit-loops"
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
// supplied, is restored before the first instruction executes. Panel protocol
// probing is diagnostic-only and remains disabled unless explicitly selected.
type Options struct {
	Backend            cpu.Backend
	BackendMode        CPUBackendMode
	RunnerQuantum      uint64
	Media              *MediaState
	ProbePanelProtocol bool
	// OutputChannels overrides the presentation mixer's channel count. Zero
	// inherits the runtime default (mono); one selects mono and two stereo.
	OutputChannels uint8
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
	mdp            *system.QualcommMDPScriptEngine
	panelProbe     *system.LCDTransferProbe
	keypad         *system.QualcommGPIOKeypad
	touchscreen    *system.QualcommTSC2007
	vectoredIRQs   *system.QualcommVectoredInterruptController
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
	if options.OutputChannels != 0 && options.OutputChannels != 1 && options.OutputChannels != 2 {
		return nil, fmt.Errorf("invalid audio output channel count %d", options.OutputChannels)
	}
	pkg, err := samsung.Inspect(set)
	if err != nil {
		return nil, fmt.Errorf("inspect Samsung firmware set: %w", err)
	}
	firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		return nil, fmt.Errorf("select Samsung firmware build: %w", err)
	}
	switch firmwareProfile.Model {
	case "SCH-W210", "SCH-W240", "SCH-W270", "SCH-W290", "SCH-W300", "SCH-W320", "SCH-W330", "SCH-W340", "SCH-W350", "SCH-W390", "SCH-W410", "SCH-W420", "SCH-W450", "SCH-W460", "SCH-W599", "SCH-W850", "SPH-W4200":
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
	case samsung.SCHW210CK12ProfileID:
		board = system.SCHW210CK12BoardProfile()
	case samsung.SCHW240CL28ProfileID:
		board = system.SCHW240CL28BoardProfile()
	case samsung.SCHW270CL28ProfileID:
		board = system.SCHW270CL28BoardProfile()
	case samsung.SCHW290CK10ProfileID:
		board = system.SCHW290CK10BoardProfile()
	case samsung.SCHW300DA04ProfileID:
		board = system.SCHW300DA04BoardProfile()
	case samsung.SCHW320DC18ProfileID:
		board = system.SCHW320DC18BoardProfile()
	case samsung.SCHW330CK06ProfileID:
		board = system.SCHW330CK06BoardProfile()
	case samsung.SCHW340DC18ProfileID:
		board = system.SCHW340DC18BoardProfile()
	case samsung.SCHW350CK06ProfileID:
		board = system.SCHW350CK06BoardProfile()
	case samsung.SCHW390CK11ProfileID:
		board = system.SCHW390CK11BoardProfile()
	case samsung.SCHW410CL10ProfileID:
		board = system.SCHW410CL10BoardProfile()
	case samsung.SCHW420CD16ProfileID:
		board = system.SCHW420CD16BoardProfile()
	case samsung.SCHW460CC26ProfileID:
		board = system.SCHW460CC26BoardProfile()
	case samsung.SPHW4200DC17ProfileID:
		board = system.SPHW4200DC17BoardProfile()
	case samsung.SCHW450CK10ProfileID:
		board = system.SCHW450CK10BoardProfile()
	case samsung.SCHW599BE30ProfileID:
		board = system.SCHW599BE30BoardProfile()
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
	bootImageID := "qcsbl"
	if firmwareProfile.DirectResetImage != "" {
		bootImageID = firmwareProfile.DirectResetImage
	}
	qcsblSpec, ok := firmwareProfile.BootImage(bootImageID)
	if !ok {
		return nil, fmt.Errorf("firmware build %q has no initial boot image %q", firmwareProfile.ID, bootImageID)
	}
	qcsbl, err := samsung.ReconstructBootImage(set, pkg, qcsblSpec)
	if err != nil {
		return nil, fmt.Errorf("reconstruct initial boot image: %w", err)
	}
	var pblPreloadedBootImages []samsung.BootImage
	for _, spec := range firmwareProfile.BootImages {
		if !spec.PBLPreload || spec.ID == qcsblSpec.ID {
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
	flashImage, err := samsung.AssembleFlashForProfileWithOptions(set, pkg, firmwareProfile, samsung.FlashAssemblyOptions{
		FactoryBadBlocks: board.NANDFactoryBadBlocks,
	})
	if err != nil {
		return nil, fmt.Errorf("assemble %s NAND: %w", firmwareProfile.Model, err)
	}
	if flashImage.PageSize() != board.NANDPageSize ||
		flashImage.EraseBlockSize() != board.NANDEraseBlockSize {
		return nil, fmt.Errorf(
			"%s normalized NAND geometry %#x/%#x does not match board %#x/%#x",
			firmwareProfile.Model,
			flashImage.PageSize(), flashImage.EraseBlockSize(),
			board.NANDPageSize, board.NANDEraseBlockSize,
		)
	}
	var w4200FeatureConfig []byte
	if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID {
		w4200FeatureConfig, err = readSamsungW4200FeatureConfig(set, pkg)
		if err != nil {
			return nil, err
		}
	}
	flash, err := system.NewCOWFlashWithCapacityAndSeeds(
		flashImage,
		board.NANDSize,
		board.NANDEraseBlockSize,
		flashImage.Identity(),
		board.NANDInitialData,
	)
	if err != nil {
		return nil, fmt.Errorf("create %s writable NAND: %w", firmwareProfile.Model, err)
	}
	var secondaryFlash *system.COWFlash
	if spec := board.OneNAND; spec != nil {
		if spec.InitialImageFromFirmware {
			secondaryFlash, err = system.NewCOWFlashWithCapacityAndSeeds(
				flashImage,
				spec.Capacity,
				samsung.EraseBlockSize,
				firmwareProfile.ID+":onenand",
				nil,
			)
		} else {
			secondaryFlash, err = system.NewErasedCOWFlash(
				spec.Capacity,
				samsung.EraseBlockSize,
				firmwareProfile.ID+":onenand",
			)
		}
		if err != nil {
			return nil, fmt.Errorf("create %s OneNAND media: %w", firmwareProfile.Model, err)
		}
	}

	backend := options.Backend
	ownedBackend := false
	if backend == nil {
		var backendErr error
		backend, backendErr = newInterpreterBackend(options.BackendMode, interpreter.CompatibilityOptions{
			UserSystemSPSRReadAsCPSR: board.CPUCompatibility.UserSystemSPSRReadAsCPSR,
		})
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
	vectoredInterrupts, err := system.NewQualcommVectoredInterruptController(
		*board.VectoredInterrupt,
		interruptSink,
	)
	if err != nil {
		return fail(fmt.Errorf("create %s vectored interrupt controller: %w", firmwareProfile.Model, err))
	}
	var legacyInterruptSink system.InterruptLineSink
	if cascade := board.LegacyInterruptCascade; cascade != nil && cascade.SubInterruptWindowID == "" {
		legacyInterruptSink = qualcommInterruptCascadeSink{
			target:       vectoredInterrupts,
			source:       cascade.VectoredSource,
			statusOffset: cascade.GroupStatusOffset,
			mask:         cascade.GroupMask,
		}
	}
	legacyInterrupts, err := system.NewQualcommInterruptControllerWithConfig(
		system.QualcommInterruptControllerConfig{
			GPIOInputs:    board.BootControlGPIOInputs,
			StatusAliases: board.BootControlInterruptStatusAliases,
		},
		legacyInterruptSink,
	)
	if err != nil {
		return fail(fmt.Errorf("create %s legacy interrupt/GPIO aperture: %w", firmwareProfile.Model, err))
	}
	nandReady := system.NewStatusSignal()
	nandConfig := system.Qualcomm8BitNANDConfig(
		board.NANDPageSize,
		board.NANDEraseBlockSize,
		board.NANDReadID,
		nandReady,
	)
	nandConfig.Capacity = board.NANDSize
	nandConfig.FactoryBadBlocks = append([]uint32(nil), board.NANDFactoryBadBlocks...)
	nandConfig.ReportErasedECCCodewords = board.NANDReportsErasedECCCodewords
	nandConfig.RegisterResets = append(
		[]system.QualcommNANDRegisterReset(nil),
		board.NANDRegisterResets...,
	)
	if nandConfig.PageSize != flashImage.PageSize() ||
		nandConfig.EraseBlockSize != flashImage.EraseBlockSize() {
		return fail(fmt.Errorf("%s NAND geometry does not match normalized flash", firmwareProfile.Model))
	}
	nand, err := system.NewQualcommNAND(flash, nandConfig)
	if err != nil {
		return fail(fmt.Errorf("create %s NAND controller: %w", firmwareProfile.Model, err))
	}
	var oneNAND *system.OneNAND
	var oneNANDSpare system.StatefulNANDSpareStorage
	if spec := board.OneNAND; spec != nil {
		var oneNANDInterrupts *system.QualcommVectoredInterruptController
		if spec.InterruptSource != 0 {
			oneNANDInterrupts = vectoredInterrupts
		}
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
			ManufacturerID:      spec.ManufacturerID,
			DeviceID:            spec.DeviceID,
			VersionID:           spec.VersionID,
			TechnologyID:        spec.TechnologyID,
			DieBlockOffset:      spec.DieBlockOffset,
			Capacity:            spec.Capacity,
			FlexGeometry:        spec.FlexGeometry,
			Storage:             secondaryFlash,
			Spare:               oneNANDSpare,
			InterruptController: oneNANDInterrupts,
			InterruptSource:     spec.InterruptSource,
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
		ByteWritableOffsets:            board.BootControlByteWritableOffsets,
		ReadOnlyRegisters:              board.BootControlReadOnlyRegisters,
		RegisterResets:                 board.BootControlRegisterResets,
		CompletionEvents:               board.BootControlCompletionEvents,
		LegacyUARTControllers:          board.BootControlLegacyUARTControllers,
		LegacyUARTReceiveData:          board.BootControlLegacyUARTReceiveData,
		SBIControllers:                 board.BootControlSBIControllers,
		SBIReadResponses:               board.BootControlSBIReadResponses,
		SBICompletionStatus:            board.BootControlSBICompletionStatus,
		SDCCControllers:                board.BootControlSDCCControllers,
		GroupedStatusResponses:         board.BootControlGroupedStatusResponses,
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
		Status:              board.PrimaryClockStatus,
		InputMask:           board.PrimaryClockInputMask,
		WritableOffsets:     board.PrimaryClockWritableOffsets,
		ReadOnlyRegisters:   board.PrimaryClockReadOnlyRegisters,
		InterruptRegisters:  board.PrimaryClockInterruptRegisters,
		InterruptController: legacyInterrupts,
	})
	if err != nil {
		return fail(fmt.Errorf("create %s primary clock: %w", firmwareProfile.Model, err))
	}
	var touchscreen *system.QualcommTSC2007
	if board.Touchscreen != nil {
		touchscreen, err = system.NewQualcommTSC2007(*board.Touchscreen)
		if err != nil {
			return fail(fmt.Errorf("create %s touchscreen: %w", firmwareProfile.Model, err))
		}
		if err := touchscreen.AttachInterruptControllers(legacyInterrupts, vectoredInterrupts); err != nil {
			return fail(fmt.Errorf("attach %s touchscreen interrupt: %w", firmwareProfile.Model, err))
		}
		if err := primaryClock.AttachTouchscreen(touchscreen); err != nil {
			return fail(fmt.Errorf("attach %s touchscreen GPIO group: %w", firmwareProfile.Model, err))
		}
		if err := secondaryClock.AttachGPIOReadObserver(touchscreen); err != nil {
			return fail(fmt.Errorf("attach %s touchscreen pen input: %w", firmwareProfile.Model, err))
		}
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
	var legacyTopVectoredInterruptAperture system.Device
	if board.LegacyTopVectoredInterruptOffset != 0 {
		legacyTopVectoredInterruptAperture = bootControl
	}
	legacyTop, err := system.NewQualcommLegacyTopPageWithConfig(system.QualcommLegacyTopConfig{
		Version:                   board.LegacyTopVersion,
		Identification:            board.LegacyTopIdentification,
		WritableOffsets:           board.LegacyTopWritableOffsets,
		VectoredInterruptOffset:   board.LegacyTopVectoredInterruptOffset,
		VectoredInterruptAperture: legacyTopVectoredInterruptAperture,
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
		Offsets:          board.SparseBusRegisterOffsets,
		Resets:           board.SparseBusRegisterResets,
		ReadClearOffsets: board.SparseBusRegisterReadClearOffsets,
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
	var handoff system.BootHandoff
	if firmwareProfile.DirectResetImage != "" {
		handoff = system.BootHandoff{
			ID: "original-firmware-reset", Entry: qcsbl.EntryAddress, Mode: cpu.ModeARM,
			Registers: []system.RegisterSeed{{Register: cpu.RegisterCPSR, Value: 0x000000d3}},
		}
	} else {
		handoff, err = system.NewQualcommNANDPBLHandoff(system.QualcommNANDPBLConfig{
			Entry:                    qcsbl.EntryAddress,
			StackPointer:             board.PBLStackPointer,
			TableAddress:             pblServiceTableAddress,
			ServiceTableHeaderSize:   board.PBLServiceTableHeaderSize,
			HeaderFeatureDataAddress: board.PBLHeaderFeatureDataAddress,
			HeaderFeatures:           append([]system.QualcommPBLHeaderFeature(nil), board.PBLHeaderFeatures...),
			FixedFeatureDataAddress:  board.PBLFixedFeatureDataAddress,
			FixedFeatureFirst:        board.PBLFixedFeatureFirst,
			FixedFeatureSlotCount:    board.PBLFixedFeatureSlotCount,
			FixedFeatures:            append([]system.QualcommPBLFixedFeature(nil), board.PBLFixedFeatures...),
			LegacyFeatureDataAddress: board.PBLLegacyFeatureDataAddress,
			SharedDataAddress:        board.PBLSharedDataAddress,
			SharedDataSize:           board.PBLSharedDataSize,
			PageSize:                 board.NANDPageSize, EraseBlockSize: board.NANDEraseBlockSize,
			FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
		})
		if err != nil {
			return fail(fmt.Errorf("create %s PBL handoff: %w", firmwareProfile.Model, err))
		}
	}
	handoff.Memory = append(handoff.Memory, system.MemorySeed{
		Address: qcsbl.LoadAddress,
		Bytes:   append([]byte(nil), qcsbl.Bytes...),
	})
	for _, address := range qcsblSpec.MirrorAddresses {
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: address,
			Bytes:   append([]byte(nil), qcsbl.Bytes...),
		})
	}
	if qcsblSpec.PBLRelocationAddress != 0 {
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: qcsblSpec.PBLRelocationAddress,
			Bytes:   append([]byte(nil), qcsbl.Bytes...),
		})
	}
	if firmwareProfile.ID == samsung.SCHW320DC18ProfileID {
		if seedErr := appendSamsungW320VerifiedPBLState(&handoff, qcsbl); seedErr != nil {
			return fail(seedErr)
		}
	}
	if firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
		// DC18's AMSS startup reads the base of its 28 MiB runtime heap from a
		// retained boot-environment hole rather than an ELF file-backed segment.
		// The corresponding CL10 runtime publishes the same 0x06100000 base; the
		// downloadable DC18 chain does not carry that volatile word.
		var heapBase [4]byte
		binary.LittleEndian.PutUint32(heapBase[:], samsungW340AMSSHeapBase)
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: samsungW340AMSSHeapBaseAddress,
			Bytes:   heapBase[:],
		})
		// The retail loader also publishes the null-terminated BREW static-module
		// list in this retained block. DC18 contains the native IdleApp module
		// entry and class table, but the downloadable image leaves the list itself
		// in zero-fill memory. Without this pointer the shell cannot instantiate
		// class 0x01007001 and remains on the boot splash indefinitely.
		handoff.Memory = append(handoff.Memory, samsungW340StaticModuleListSeed())
		// Several downloadable modules publish class-list pointers from native code,
		// while their class IDs normally survive in loader-retained data. Preserve
		// those words so BREW enumeration does not mistake a zero record for the end
		// of the complete module registry.
		handoff.Memory = append(handoff.Memory, samsungW340DownloadableClassRecordSeeds()...)
		// The same retained environment enables REX task switching after the
		// kernel creates its idle, DPC, and main TCBs. The progressive image
		// references this word from its context-switch veneer but does not own
		// file-backed initialization for it. Without the PBL value, rex_wait
		// repeatedly returns to the DPC task and Main Task is never dispatched.
		var schedulerEnabled [4]byte
		binary.LittleEndian.PutUint32(schedulerEnabled[:], samsungW340REXSchedulerEnabled)
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: samsungW340REXSchedulerAddress,
			Bytes:   schedulerEnabled[:],
		})
		// DC18 consumes this retained loader flag during the first pair of UI
		// startup events, before the later AMSS flash-environment callback can
		// republish it. Seed it at the PBL boundary as well; the bulk-zero HLE
		// below restores the same value if the AMSS BSS pass covers the word.
		handoff.Memory = append(handoff.Memory, samsungW340UIInitializationSeed())
		// The retail loader publishes the FNT offsets of the shared UTF, dictionary,
		// and image resources in volatile retained state. Without them the clients
		// parse the outer FNT directory as payload data and overwrite low RAM.
		handoff.Memory = append(handoff.Memory, samsungW340BREWTableOffsetSeed())
		// The LSM task obtains its callback table through another retained-loader
		// word. The downloadable image contains the table itself, but not this
		// pointer; losing it makes the task exception-return through a zero frame.
		handoff.Memory = append(handoff.Memory, samsungW340LSMCallbackTableSeed())
		// BCX dispatch-table entries initially share a loader-owned fallback object.
		// Keep all of its method slots callable until the firmware replaces them.
		handoff.Memory = append(handoff.Memory, samsungW340DefaultDispatchObjectSeed())
		// The retail loader also retains the already-attached offline service used
		// by IdleApp. Its native class factory only rebuilds the owning IBASE shell.
		handoff.Memory = append(handoff.Memory, samsungW340RetainedServiceSeeds()...)
		// AEE_GetConMgr obtains the platform connection manager from retained UI
		// state.  The downloadable image contains its consumers and native cold
		// constructor, but the latter is normally run by the missing retail loader.
		handoff.Memory = append(handoff.Memory, samsungW340ConnectionManagerSeeds()...)
		handoff.Memory = append(handoff.Memory, samsungW340IdleDependencySeeds()...)
		handoff.Memory = append(handoff.Memory, samsungW340IdleSimMainSeeds()...)
		// AEEDisp's platform provider is another loader-retained singleton. Preserve
		// its native display allocator and restore the lookup slot that connects the
		// allocated primary device to the IDisplay object.
		handoff.Memory = append(handoff.Memory, samsungW340DisplayProviderSeeds()...)
		// BREW's first-start NVM reconciler consumes one retained path record before
		// opening brew/shared/nvm/prefs.dat.  The downloadable image contains the
		// reconciler and filename text elsewhere, but its loader-owned record is BSS.
		handoff.Memory = append(handoff.Memory, samsungW340BREWNVMSeeds()...)
		// OEMPosDet's shared timer keeps its owner chain in loader-retained RAM.
		// Reattach it to an empty manager so the native lookup can report no timer.
		handoff.Memory = append(handoff.Memory, samsungW340PosDetTimerSeeds()...)
		// Downloadable BREW modules construct one loader-backed singleton through
		// this retained initializer slot.  The retail initializer is absent from
		// the archive; its zero-state contract is a register-transparent return.
		handoff.Memory = append(handoff.Memory, samsungW340ModuleInitializerSeed())
		// MGP's AP-side task queues are allocated from a pool whose pointer is
		// retained by the retail loader.  The downloadable image supplies the
		// queue manager and its 0x6590-word size, but leaves this pointer zero.
		handoff.Memory = append(handoff.Memory, samsungW340MGPQueuePoolPointerSeed())
	}
	if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID {
		// The unavailable PBL leaves a 16-register AMSS reset template at
		// 0xffffdfc0. OEMSBL copies it before loading DC17 and the AMSS reset
		// vector authenticates the r12 slot before installing its own stacks.
		var resetCookie [4]byte
		binary.LittleEndian.PutUint32(resetCookie[:], samsungW4200AMSSResetCookie)
		handoff.Memory = append(handoff.Memory, system.MemorySeed{
			Address: samsungW4200AMSSResetCookieStore,
			Bytes:   resetCookie[:],
		})
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
	if err := board.ApplyAddressedStorageWindows(bus, flash); err != nil {
		return fail(fmt.Errorf("map %s addressed storage windows: %w", firmwareProfile.Model, err))
	}
	var audio *schw830Audio
	if firmwareProfile.ID == samsung.SCHW830DL21ProfileID {
		instructionsPerSecond := schw830AudioInstructionsPerSecond
		if board.TimeTickClock != nil && board.TimeTickClock.InstructionsPerSecond != 0 {
			instructionsPerSecond = board.TimeTickClock.InstructionsPerSecond
		}
		audioConfig := defaultSCHW830AudioConfig(instructionsPerSecond)
		audioConfig.outputChannels = options.OutputChannels
		audio, err = newSCHW830Audio(bus, audioConfig)
		if err != nil {
			return fail(err)
		}
	}
	mdp, err := board.AttachMDP(bus, dcsPanelController, bootControl)
	if err != nil {
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
		touchscreen,
	); err != nil {
		return fail(err)
	}
	var panelProbe *system.LCDTransferProbe
	if options.ProbePanelProtocol {
		panelProbe = system.NewLCDTransferProbe()
		if err := panelProbe.Attach(bus, panel); err != nil {
			return fail(fmt.Errorf("attach %s panel protocol probe: %w", firmwareProfile.Model, err))
		}
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
	quantum := samsungQualcommRunnerQuantum(firmwareProfile.ID, options.RunnerQuantum)
	clockedDevices := bus.ClockedDevices()
	if audio != nil {
		clockedDevices = append(clockedDevices, audio)
	}
	var executionRunner system.ExecutionRunner = backend
	if len(board.HLECalls) != 0 {
		handlers := samsungQualcommMachineHLEHandlers(
			flash, secondaryFlash, board, flashImage.ProgressiveELF(), flashImage.Partitions(),
			w4200FeatureConfig,
		)
		if touchscreen != nil {
			attachSamsungW4200TouchHLEHandlers(handlers, touchscreen)
		}
		hleRunner, hleErr := system.NewHLERunner(
			bus,
			backend,
			board.HLECalls,
			handlers,
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
		panel:        dcsPanelController, mdp: mdp, panelProbe: panelProbe, keypad: keypad, touchscreen: touchscreen,
		vectoredIRQs: vectoredInterrupts,
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

func samsungQualcommRunnerQuantum(firmwareBuildID string, configured uint64) uint64 {
	if configured != 0 {
		return configured
	}
	if firmwareBuildID == samsung.SCHW340DC18ProfileID {
		// DC18's compact-VIC dispatcher can drain the MGP host interrupt and the
		// 100 Hz scheduler tick in one architectural IRQ. Keep virtual-device
		// delivery within the dispatcher timing evidenced by the handset so two
		// independently clocked sources cannot accumulate for a full generic
		// 4096-instruction slice before the CPU observes either one.
		return 256
	}
	return system.DefaultClockedRunnerQuantum
}

func samsungW340FlashEnvironmentSeed() system.MemorySeed {
	const operationTableOffset = 0x1c
	data := make([]byte, operationTableOffset+0x9c)
	binary.LittleEndian.PutUint32(data[0x00:], 0x00137a2d)
	binary.LittleEndian.PutUint32(data[0x04:], 0x00137cd7)
	binary.LittleEndian.PutUint32(data[0x10:], 0x001381d1)
	binary.LittleEndian.PutUint32(data[0x14:], 0x00138181)
	operations := [...]uint32{
		0x001365c3, 0x001365c9, 0x001365cf, 0x001365d5,
		0x001365df, 0x001365e5, 0x001365eb, 0x001365f1,
		0x00136569, 0x00136415, 0x00136295, 0x00136311,
		0, 0x0013637d, 0x00136335, 0,
		0, 0, 0, 0,
		0x0013635b, 0x00136437, 0x001364ed, 0x00136535,
		0x001364c5, 0x0013640f, 0x001362c7, 0x001363a5,
		0x001363df, 0x00137ff3, 0x00138073, 0x00138079,
		0x0013649b, 0x001365f7, 0x00136605, 0x00136635,
		0x00136665, 0x0013669f, 0x001366d9,
	}
	for index, value := range operations {
		binary.LittleEndian.PutUint32(data[operationTableOffset+index*4:], value)
	}
	return system.MemorySeed{Address: samsungW340FlashEnvironment, Bytes: data}
}

func samsungW340StaticModuleListSeed() system.MemorySeed {
	data := make([]byte, int(samsungW340StaticModuleCount)*4)
	for index, entry := range samsungW340StaticModuleEntries {
		binary.LittleEndian.PutUint32(data[index*4:], entry)
	}
	return system.MemorySeed{Address: samsungW340StaticModuleList, Bytes: data}
}

func samsungW340DownloadableClassRecordSeeds() []system.MemorySeed {
	seeds := make([]system.MemorySeed, 0, len(samsungW340DownloadableClassRecords))
	for _, record := range samsungW340DownloadableClassRecords {
		data := make([]byte, 0x14)
		binary.LittleEndian.PutUint32(data, record.classID)
		seeds = append(seeds, system.MemorySeed{Address: record.address, Bytes: data})
	}
	return seeds
}

func samsungW340StaticClassSeeds(oemTable []byte) ([]system.MemorySeed, error) {
	const recordSize = 0x10
	required := int(samsungW340OEMClassCount) * recordSize
	if len(oemTable) < required {
		return nil, fmt.Errorf("Samsung W340 OEM class table has %#x bytes, need %#x", len(oemTable), required)
	}

	// BREW's supplemental static-class list uses records in the order
	// [class, flags, init, factory]. Samsung's generated OEM table already uses
	// that layout. The word immediately before the table is the final word of a
	// diagnostic string, not a factory pointer; starting there would shift every
	// factory by one record. SECIA's four-class table is resident in the BREW
	// image at 0x00074d50, but its loader-owned outer-list pointer is absent from
	// the downloader archive. IdleApp needs class 0x010127d6 from that table, so
	// append its exact native factory record before the terminator.
	classes := make([]byte, required+2*recordSize)
	copy(classes, oemTable[:required])
	secia := classes[required : required+recordSize]
	binary.LittleEndian.PutUint32(secia[0x00:], samsungW340SECIAClassID)
	binary.LittleEndian.PutUint32(secia[0x0c:], samsungW340SECIAFactory)
	list := make([]byte, 8)
	binary.LittleEndian.PutUint32(list, samsungW340StaticClassBacking)
	return []system.MemorySeed{
		{Address: samsungW340StaticClassBacking, Bytes: classes},
		{Address: samsungW340StaticClassList, Bytes: list},
	}, nil
}

func publishSamsungW340StaticClasses(backend cpu.Backend) error {
	oemTable := make([]byte, int(samsungW340OEMClassCount)*0x10)
	if err := backend.ReadMemory(samsungW340OEMClassTable, oemTable); err != nil {
		return fmt.Errorf("read Samsung W340 OEM class table: %w", err)
	}
	seeds, err := samsungW340StaticClassSeeds(oemTable)
	if err != nil {
		return err
	}
	for _, seed := range seeds {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 static classes at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340RetainedServiceSeeds() []system.MemorySeed {
	// Class 0x01006388's native factory first consults this loader-retained
	// singleton. Its cold fallback only allocates the two-entry IBASE shell used
	// to own an already-attached service; IdleApp then calls the service's attach
	// slot at vtable +0x3c. Keep native AddRef/Release and provide the quiescent
	// offline attach result used when the retail companion service is absent.
	object := make([]byte, 0x0c)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340RetainedServiceVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	vtable := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(vtable[0x00:], samsungW340RetainedServiceAddRef)
	binary.LittleEndian.PutUint32(vtable[0x04:], samsungW340RetainedServiceRelease)
	binary.LittleEndian.PutUint32(vtable[0x3c:], samsungW340RetainedServiceNoop|1)
	store := make([]byte, 4)
	binary.LittleEndian.PutUint32(store, samsungW340RetainedServiceObject)
	return []system.MemorySeed{
		{Address: samsungW340RetainedServiceObject, Bytes: object},
		{Address: samsungW340RetainedServiceVTable, Bytes: vtable},
		{Address: samsungW340RetainedServiceNoop, Bytes: []byte{0x00, 0x20, 0x70, 0x47}},
		{Address: samsungW340RetainedServiceIdentity, Bytes: []byte{0x70, 0x47}},
		{Address: samsungW340RetainedServiceStore, Bytes: store},
	}
}

func publishSamsungW340RetainedService(backend cpu.Backend) error {
	for _, seed := range samsungW340RetainedServiceSeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 retained service at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340ConnectionManagerSeeds() []system.MemorySeed {
	// The main app only requires a callable offline connection-manager surface
	// while the modem-side companion services are absent.  Preserve the object
	// identity expected by AEE_GetConMgr and let each operation complete with the
	// neutral SUCCESS result until a native provider replaces the retained slot.
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340ConnectionManagerVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	vtable := make([]byte, 0x100)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	store := make([]byte, 4)
	binary.LittleEndian.PutUint32(store, samsungW340ConnectionManagerObject)
	return []system.MemorySeed{
		{Address: samsungW340ConnectionManagerObject, Bytes: object},
		{Address: samsungW340ConnectionManagerVTable, Bytes: vtable},
		{Address: samsungW340ConnectionManagerStore, Bytes: store},
	}
}

func publishSamsungW340ConnectionManager(backend cpu.Backend) error {
	for _, seed := range samsungW340ConnectionManagerSeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 connection manager at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340IdleDependencySeeds() []system.MemorySeed {
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340IdleDependencyVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	vtable := make([]byte, 0x100)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	return []system.MemorySeed{
		{Address: samsungW340IdleDependencyObject, Bytes: object},
		{Address: samsungW340IdleDependencyVTable, Bytes: vtable},
	}
}

func publishSamsungW340IdleDependency(backend cpu.Backend) error {
	for _, seed := range samsungW340IdleDependencySeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 IdleApp dependency at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340IdleSimMainSeeds() []system.MemorySeed {
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340IdleSimMainVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	// IdleApp uses both the base interface and the extended SIM lifecycle
	// methods through slots as far as +0x1b0.  Keep every retained-loader slot
	// callable; leaving the tail zero makes the first SIM update branch to the
	// reset vector through BLX 0.
	vtable := make([]byte, 0x1c0)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	// Slot +0x08 returns the interface used as the receiver for later extended
	// lifecycle calls.  Preserve r0 instead of returning the neutral status code.
	binary.LittleEndian.PutUint32(vtable[0x08:], samsungW340RetainedServiceIdentity|1)
	return []system.MemorySeed{
		{Address: samsungW340IdleSimMainObject, Bytes: object},
		{Address: samsungW340IdleSimMainVTable, Bytes: vtable},
	}
}

func publishSamsungW340IdleSimMain(backend cpu.Backend) error {
	for _, seed := range samsungW340IdleSimMainSeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 IdleApp SIM-main target at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func ensureSamsungW340IdleSimMainCompanion(backend cpu.Backend, owner uint32) error {
	const companionOffset = uint32(0x18)
	var companion [4]byte
	if err := backend.ReadMemory(owner+companionOffset, companion[:]); err != nil {
		return fmt.Errorf("read Samsung W340 IdleApp SIM-main companion: %w", err)
	}
	if binary.LittleEndian.Uint32(companion[:]) != 0 {
		return nil
	}
	if err := publishSamsungW340IdleSimMain(backend); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(companion[:], samsungW340IdleSimMainObject)
	if err := backend.WriteMemory(owner+companionOffset, companion[:]); err != nil {
		return fmt.Errorf("publish Samsung W340 IdleApp SIM-main companion: %w", err)
	}
	return nil
}

func samsungW340IdleExtendedSeeds() []system.MemorySeed {
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340IdleExtendedVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	// The final class-0x0100638f transition uses the extended method at +0x194
	// after its base-interface calls at +0x4c and +0xc4.
	vtable := make([]byte, 0x1c0)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	binary.LittleEndian.PutUint32(vtable[0x08:], samsungW340RetainedServiceIdentity|1)
	return []system.MemorySeed{
		{Address: samsungW340IdleExtendedObject, Bytes: object},
		{Address: samsungW340IdleExtendedVTable, Bytes: vtable},
	}
}

func publishSamsungW340IdleExtended(backend cpu.Backend) error {
	for _, seed := range samsungW340IdleExtendedSeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 IdleApp extended provider at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340StartupPrimarySeeds() []system.MemorySeed {
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340StartupPrimaryVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	vtable := make([]byte, 0x1c0)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	binary.LittleEndian.PutUint32(vtable[0x08:], samsungW340RetainedServiceIdentity|1)
	return []system.MemorySeed{
		{Address: samsungW340StartupPrimaryObject, Bytes: object},
		{Address: samsungW340StartupPrimaryVTable, Bytes: vtable},
	}
}

func publishSamsungW340StartupPrimary(backend cpu.Backend) error {
	for _, seed := range samsungW340StartupPrimarySeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 startup primary interface at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340StartupSecondarySeeds() []system.MemorySeed {
	object := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340StartupSecondaryVTable)
	binary.LittleEndian.PutUint32(object[0x04:], 1)
	vtable := make([]byte, 0x1c0)
	for offset := 0; offset < len(vtable); offset += 4 {
		binary.LittleEndian.PutUint32(vtable[offset:], samsungW340RetainedServiceNoop|1)
	}
	binary.LittleEndian.PutUint32(vtable[0x08:], samsungW340RetainedServiceIdentity|1)
	return []system.MemorySeed{
		{Address: samsungW340StartupSecondaryObject, Bytes: object},
		{Address: samsungW340StartupSecondaryVTable, Bytes: vtable},
	}
}

func publishSamsungW340StartupSecondary(backend cpu.Backend) error {
	for _, seed := range samsungW340StartupSecondarySeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 startup secondary interface at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340DisplayProviderSeeds() []system.MemorySeed {
	object := make([]byte, 0x0c)
	binary.LittleEndian.PutUint32(object[0x00:], samsungW340DisplayProviderVTable)
	binary.LittleEndian.PutUint32(object[0x08:], 1)

	// Keep the native provider surface, except for Release (the loader owns this
	// fixed object) and the built-in-font lookup. The downloadable fallback
	// returns EUNSUPPORTED after its native CreateDisplay method has allocated the
	// physical device. On retail handsets selectors 0x8000..0x8002 resolve to an
	// IFont supplied by the retained loader. They are font IDs, not display IDs;
	// returning IDisplay's physical bitmap here makes text measurement call the
	// bitmap SetPixel surface and leaves every line height at zero.
	words := [...]uint32{
		samsungW340DisplayProviderAddRef,
		samsungW340DisplayProviderRelease | 1,
		samsungW340DisplayProviderCreate,
		0x00572fc1,
		0x00572fc5,
		0x00572fc9,
		0x00572fdd,
		0x00572fe1,
		0x00572fe5,
		0x00572fe9,
		0x00572fed,
		samsungW340DisplayProviderLookup | 1,
	}
	vtable := make([]byte, len(words)*4)
	for index, word := range words {
		binary.LittleEndian.PutUint32(vtable[index*4:], word)
	}
	store := make([]byte, 4)
	binary.LittleEndian.PutUint32(store, samsungW340DisplayProviderObject)
	lookup := []byte{
		0x05, 0x4b, // ldr r3, [pc, #20] ; 0x00008000
		0xc9, 0x1a, // subs r1, r1, r3
		0x02, 0x29, // cmp r1, #2
		0x03, 0xd8, // bhi unsupported
		0x04, 0x4b, // ldr r3, [pc, #16] ; retained IFont
		0x13, 0x60, // str r3, [r2]
		0x00, 0x20, // movs r0, #0
		0x70, 0x47, // bx lr
		0x14, 0x20, // unsupported: movs r0, #0x14
		0x70, 0x47, // bx lr
		0xc0, 0x46, // nop; align literals
		0xc0, 0x46,
		0x00, 0x80, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, // retained IFont, filled below
	}
	binary.LittleEndian.PutUint32(lookup[len(lookup)-4:], samsungW340FontInterfaceObject)
	fontObject := make([]byte, 8)
	binary.LittleEndian.PutUint32(fontObject[0x00:], samsungW340FontInterfaceVTable)
	binary.LittleEndian.PutUint32(fontObject[0x04:], 1)
	fontVTableWords := [...]uint32{
		samsungW340DisplayProviderRelease | 1,
		samsungW340DisplayProviderRelease | 1,
		samsungW340DisplayProviderRelease | 1,
		samsungW340FontInterfaceDraw | 1,
		samsungW340FontInterfaceMeasure | 1,
		samsungW340FontInterfaceInfo | 1,
	}
	fontVTable := make([]byte, len(fontVTableWords)*4)
	for index, word := range fontVTableWords {
		binary.LittleEndian.PutUint32(fontVTable[index*4:], word)
	}
	return []system.MemorySeed{
		{Address: samsungW340DisplayProviderObject, Bytes: object},
		{Address: samsungW340DisplayProviderVTable, Bytes: vtable},
		{Address: samsungW340DisplayProviderRelease, Bytes: []byte{0x01, 0x20, 0x70, 0x47}},
		{Address: samsungW340DisplayProviderLookup, Bytes: lookup},
		{Address: samsungW340DisplayProviderStore, Bytes: store},
		{Address: samsungW340FontInterfaceObject, Bytes: fontObject},
		{Address: samsungW340FontInterfaceVTable, Bytes: fontVTable},
		{Address: samsungW340FontInterfaceDraw, Bytes: []byte{0x00, 0x20, 0x70, 0x47}},
		{Address: samsungW340FontInterfaceMeasure, Bytes: []byte{0x00, 0x20, 0x70, 0x47}},
		{Address: samsungW340FontInterfaceInfo, Bytes: []byte{0x00, 0x20, 0x70, 0x47}},
	}
}

func publishSamsungW340DisplayProvider(backend cpu.Backend) error {
	for _, seed := range samsungW340DisplayProviderSeeds() {
		if err := backend.WriteMemory(seed.Address, seed.Bytes); err != nil {
			return fmt.Errorf("publish Samsung W340 display provider at %#x: %w", seed.Address, err)
		}
	}
	return nil
}

func samsungW340BREWNVMSeeds() []system.MemorySeed {
	// The native table contains one 12-byte record. Only the pathname pointer is
	// required on an unprovisioned handset; the zero length/flags describe the
	// empty prefs.dat file that the reconciler creates on first boot.
	record := make([]byte, 12)
	binary.LittleEndian.PutUint32(record, samsungW340BREWNVMFilename)
	return []system.MemorySeed{
		{Address: samsungW340BREWNVMFileTable, Bytes: record},
		{Address: samsungW340BREWNVMFilename, Bytes: []byte("prefs.dat\x00")},
	}
}

func samsungW340PosDetTimerSeeds() []system.MemorySeed {
	ownerStore := make([]byte, 4)
	binary.LittleEndian.PutUint32(ownerStore, samsungW340PosDetTimerOwnerSlot)
	ownerSlot := make([]byte, 4)
	binary.LittleEndian.PutUint32(ownerSlot, samsungW340PosDetTimerManager)
	return []system.MemorySeed{
		{Address: samsungW340PosDetTimerOwnerStore, Bytes: ownerStore},
		{Address: samsungW340PosDetTimerOwnerSlot, Bytes: ownerSlot},
		{Address: samsungW340PosDetTimerManager, Bytes: make([]byte, 0x18)},
	}
}

func samsungW340ModuleInitializerSeed() system.MemorySeed {
	const methodCount = 16
	data := make([]byte, methodCount*4)
	// 0x0245365c is both the initializer callback slot and the vtable installed
	// in the singleton.  The archived modules call method 0 while constructing
	// it and method 3 while applying the initial level. Preserve a callable
	// zero-state surface for every loader-owned slot until a native provider
	// replaces the table.
	for offset := 0; offset < len(data); offset += 4 {
		binary.LittleEndian.PutUint32(data[offset:], samsungW340RetainedServiceNoop|1)
	}
	return system.MemorySeed{Address: samsungW340ModuleInitializerStore, Bytes: data}
}

func samsungW340BCXPoolPointerSeed() system.MemorySeed {
	// DC18's BCX task constructs 0x4b00 32-byte records from this retained
	// backing store. The downloadable image contains the consumer and its global
	// pointer slot, but not the loader-owned pointer value. The reserved store
	// occupies the otherwise-unused range immediately below the 0x06100000 AMSS
	// heap base, so its 0x96000-byte extent ends exactly at that boundary.
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], samsungW340BCXPoolBacking)
	return system.MemorySeed{Address: samsungW340BCXPoolPointer, Bytes: data[:]}
}

func samsungW340BCIMAVSignalMaskSeed() system.MemorySeed {
	// BCIM_AV waits on this loader-retained mask before dispatching the three
	// events handled by its native loop (bits 0, 1, and 2). Leaving the slot at
	// the progressive segment's zero fill turns rex_wait into rex_wait(0), so
	// this high-priority task remains runnable and starves lower-priority NV.
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], samsungW340BCIMAVSignalMask)
	return system.MemorySeed{Address: samsungW340BCIMAVSignalMaskStore, Bytes: data[:]}
}

func samsungW340BCIMSignalMaskSeed() system.MemorySeed {
	// The companion BCIM task handles signals 0, 1, 4-7, 16-22, 28, and 29.
	// Its wait mask is another loader-owned word omitted from the downloadable
	// pieces; zero would keep this priority-100 task permanently runnable.
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], samsungW340BCIMSignalMask)
	return system.MemorySeed{Address: samsungW340BCIMSignalMaskStore, Bytes: data[:]}
}

func samsungW340BCXInterfaceSeeds() []system.MemorySeed {
	// BC_SCH asks the three BCX transports for their startup state before the
	// downloadable image has attached a Bluetooth controller. The interface
	// objects live in AMSS BSS, but their method slots are supplied by the retail
	// loader. Preserve an absent-controller state by connecting the unconditional
	// startup methods +4 and +8 at 0x005b04a8/0x005b04b4 to DC18's native
	// zero-return leaf. The remaining nullable methods are guarded by the firmware.
	const interfaceCount = 3
	seeds := make([]system.MemorySeed, 0, interfaceCount)
	for index := uint32(0); index < interfaceCount; index++ {
		callbacks := make([]byte, 8)
		binary.LittleEndian.PutUint32(callbacks[0:], samsungW340BCXUnavailableCallback)
		binary.LittleEndian.PutUint32(callbacks[4:], samsungW340BCXUnavailableCallback)
		seeds = append(seeds, system.MemorySeed{
			Address: samsungW340BCXInterfaceBase + index*samsungW340BCXInterfaceStride + 4,
			Bytes:   callbacks,
		})
	}
	return seeds
}

func samsungW340EFS2DeviceVTableSeed() system.MemorySeed {
	// The retail loader keeps this block-device interface table outside the
	// downloadable module arena. DC18 constructs the EFS2 block-device object at
	// 0x0335cf30 and publishes this retained address. Preserve the complete native
	// method and geometry table; only the loader-owned table itself is absent from
	// the four-piece archive.
	words := [...]uint32{
		0x0206f8bb, // Init(this, configuration)
		0x0206facd, // Close(this)
		0x0206fad1, // GetRoot(this, **root)
		0x0206f831, // Allocate(this, request, ...)
		0x0206fb6b, // Release(this, request)
		0x0206fadf, // Read(this, request, ...)
		0x0206fb2b, // Write(this, request, ...)
		0,
		0x0000000d,
		0x00000200,
		0x00040000,
		0x08000000,
		0,
		0x0000000d,
		0x0000000e,
		0x0000000f,
		0x00000009,
		0xffffffff,
	}
	data := make([]byte, len(words)*4)
	for index, word := range words {
		binary.LittleEndian.PutUint32(data[index*4:], word)
	}
	return system.MemorySeed{Address: samsungW340EFS2DeviceVTable, Bytes: data}
}

func samsungW340EFS2DeviceCallbackSeed() system.MemorySeed {
	// The same retained block holds the MMC1 completion callback immediately
	// before the vtable. DC18's archived 0x008ad560 handler dispatches the
	// request back through the TFS4 task itself, which is valid only when the
	// removable controller was published by the retail loader. The four-piece
	// archive has no such controller, so retain the non-null callback contract
	// while completing the probe as an absent device.
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], samsungW340EFS2AbsentDeviceHandler|1)
	return system.MemorySeed{Address: samsungW340EFS2DeviceCallback, Bytes: data[:]}
}

func samsungW340POSIXFileVTableSeed() system.MemorySeed {
	// DC18's open() implementation publishes this retained table in every file
	// descriptor. The progressive AMSS segment contains the native methods and
	// the table address literal, but zero-fills the table itself after the retail
	// loader has gone away. Restore the exact close/read/write method order used
	// by close(), read(), and write().
	words := [...]uint32{
		0x0052825d,
		0x00528133,
		0x00528171,
	}
	data := make([]byte, len(words)*4)
	for index, word := range words {
		binary.LittleEndian.PutUint32(data[index*4:], word)
	}
	return system.MemorySeed{Address: samsungW340POSIXFileVTable, Bytes: data}
}

func samsungW340EFS2AbsentDeviceHandlerSeed() system.MemorySeed {
	data := []byte{
		0x10, 0xb5, // push {r4, lr}
		0x04, 0x1c, // mov r4, r0
		0x02, 0x20, // movs r0, #2
		0x4c, 0x21, // movs r1, #0x4c
		0x08, 0x55, // strb r0, [r1, r4]
		0xa1, 0x6c, // ldr r1, [r4, #0x48]
		0x08, 0x70, // strb r0, [r1]
		0x00, 0x22, // movs r2, #0
		0x22, 0x71, // strb r2, [r4, #4]
		0x60, 0x6b, // ldr r0, [r4, #0x34]
		0xa1, 0x6b, // ldr r1, [r4, #0x38]
		0x01, 0x4b, // ldr r3, [pc, #4]
		0x98, 0x47, // blx r3
		0x10, 0xbd, // pop {r4, pc}
		0x68, 0xdd, 0x9c, 0x00, // rex_set_sigs veneer
	}
	return system.MemorySeed{Address: samsungW340EFS2AbsentDeviceHandler, Bytes: data}
}

func samsungW340IRQHandlerSeed() system.MemorySeed {
	// DC18's IRQ trampoline is resident in the fixed AMSS image, but the
	// progressive image zeroes the loader-owned dispatch pointer. The native
	// top-level Qualcomm VIC handler is byte-identical to the retained W350 and
	// W410 handlers and lives at 0x00c875ce in this build.
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], samsungW340IRQHandler)
	return system.MemorySeed{Address: samsungW340IRQHandlerStore, Bytes: data[:]}
}

func samsungW340InterruptTableSeed() system.MemorySeed {
	// DC18's downloadable image supplies the callbacks but omits the 49 native
	// Qualcomm VIC descriptors normally retained by the retail loader. CK06 and
	// CL10 contain byte-identical descriptor metadata, including the grouped
	// source layout; only their callback addresses differ. Keep DC18's exact
	// callbacks and reconstruct that retained metadata before AMSS IRQ init.
	callbacks := [...]uint32{
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x0207cd81, 0x0207cd6b, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x01d90361, 0x00c879e5, 0x00c879e5, 0x020b9989, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x01041147, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00f8939d, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
		0x00c879e5, 0x00c879e5, 0x00c879e5, 0x00c879e5,
	}
	flags := [...]uint32{
		0x10101, 0x10101, 0x10001, 0x101, 0x10001, 0x102, 0x102, 0x10004,
		0x10001, 0x10001, 0x10002, 0x10003, 0x101, 0x101, 0x10003, 0x10003,
		0x103, 0x10002, 0x102, 0x10003, 0x101, 0x101, 0x101, 0x103,
		0x103, 0x101, 0x106, 0x104, 0x103, 0x107, 0x101, 0x105,
		0x107, 0x107, 0x102, 0x101, 0x106, 0x101, 0x101, 0x101,
		0x101, 0x103, 0x102, 0x102, 0x103, 0x105, 0x107, 0x104, 0x107,
	}
	data := make([]byte, len(callbacks)*0x34)
	for source := range callbacks {
		words := [13]uint32{
			callbacks[source], flags[source], 0x49, 0, 0, 8,
			0x80000474, 0x80000430, 0x80000400, 0x80000458, 0x80000428,
			0, 0,
		}
		if source < 24 {
			words[6], words[7], words[8] = 0x80000478, 0x80000434, 0x80000404
			words[9], words[10] = 0x8000045c, 0x8000042c
		}
		switch source {
		case 2:
			words[2], words[3], words[4], words[5] = 0x3e, 3, 7, 3
		case 4:
			words[2], words[3], words[4], words[5] = 0x48, 1, 1, 7
		case 7:
			words[2], words[3], words[4], words[5] = 0x3a, 4, 0x0f, 2
		case 11:
			words[2], words[3], words[4], words[5] = 0x31, 3, 7, 0
		case 14:
			words[2], words[3], words[4], words[5] = 0x41, 2, 3, 4
		case 15:
			words[2], words[3], words[4], words[5] = 0x47, 1, 2, 6
		case 17:
			words[2], words[3], words[4], words[5] = 0x34, 6, 0x3f, 1
		case 19:
			words[2], words[3], words[4], words[5] = 0x43, 4, 0x0f, 5
		}
		for index, word := range words {
			binary.LittleEndian.PutUint32(data[source*0x34+index*4:], word)
		}
	}
	return system.MemorySeed{Address: samsungW340InterruptTable, Bytes: data}
}

func samsungW340UIMGroupedInterruptSeed() system.MemorySeed {
	// Logical IRQ 0x35 is the UART core interrupt used by DC18's UIM driver.
	// Every parent dispatcher reads shared metadata from its first child before
	// walking the group, so the complete 24-record retained table is required.
	// CL10's byte-identical interrupt manager supplies the hardware metadata;
	// DC18 starts each callback at its build-local default until native drivers
	// replace the entries they own.
	type groupedRecord struct {
		flags, mask, activeMask, auxiliaryMask  uint32
		parent                                  uint32
		enable, status, acknowledge, completion uint32
		group, tail                             uint32
	}
	records := make([]groupedRecord, 24)
	setGroup := func(
		start int,
		masks []uint32,
		flags, parent, enable, status, acknowledge, completion, group uint32,
		active, auxiliary bool,
	) {
		for index, mask := range masks {
			record := groupedRecord{
				flags: flags, mask: mask, parent: parent,
				enable: enable, status: status, acknowledge: acknowledge,
				completion: completion, group: group,
			}
			if active {
				record.activeMask = mask
			}
			if auxiliary {
				record.auxiliaryMask = mask
			}
			records[start+index] = record
		}
	}
	setGroup(0, []uint32{1, 2, 4}, 0x100, 11, 0x80000410, 0x80000484, 0, 0, 0, false, false)
	setGroup(3, []uint32{1, 2, 4}, 0x100, 17, 0x80000418, 0x8000048c, 0x80000460, 0, 1, true, false)
	setGroup(6, []uint32{8, 16, 32}, 0x10001, 17, 0x80000418, 0x8000048c, 0x80000460, 0x8000046c, 1, true, false)
	for index := 6; index < 9; index++ {
		records[index].auxiliaryMask = uint32(1) << (index - 6)
	}
	setGroup(9, []uint32{8, 4, 2, 1}, 0x100, 7, 0x80000420, 0x80000494, 0, 0, 2, false, false)
	setGroup(13, []uint32{4, 2, 1}, 0x10100, 2, 0x80000424, 0x80000498, 0x80000468, 0x80000470, 3, true, true)
	setGroup(16, []uint32{1, 2}, 0x100, 14, 0x80000414, 0x80000488, 0, 0, 4, false, false)
	setGroup(18, []uint32{8, 4, 2, 1}, 0x100, 19, 0x8000041c, 0x80000490, 0x80000464, 0, 5, true, false)
	records[22] = groupedRecord{
		flags: 0x10100, mask: 2, activeMask: 2, auxiliaryMask: 2,
		parent: 15, enable: 0x84000574, status: 0x84000584,
		acknowledge: 0x84000580, completion: 0x8400057c, group: 6, tail: 1,
	}
	records[23] = groupedRecord{
		flags: 0x10100, mask: 1, activeMask: 1, auxiliaryMask: 1,
		parent: 4, enable: 0x80005320, status: 0x80005330,
		acknowledge: 0x8000532c, completion: 0x80005328, group: 7, tail: 1,
	}
	data := make([]byte, len(records)*0x3c)
	for recordIndex, record := range records {
		words := [...]uint32{
			samsungW340DefaultInterruptHandler,
			record.flags,
			record.mask,
			record.mask,
			record.mask,
			record.activeMask,
			record.auxiliaryMask,
			record.parent,
			record.enable,
			record.status,
			record.acknowledge,
			record.completion,
			record.group,
			0,
			record.tail,
		}
		for index, word := range words {
			binary.LittleEndian.PutUint32(data[recordIndex*0x3c+index*4:], word)
		}
	}
	return system.MemorySeed{
		Address: samsungW340GroupedInterruptTable,
		Bytes:   data,
	}
}

func samsungW340FontFallbackSeeds() []system.MemorySeed {
	// DC18's retained retail loader normally publishes a 19-by-16 font registry.
	// The downloadable four-piece image has the consumers and renderer but not the
	// loader-owned registration event, leaving every lookup slot null.  Keep the
	// native lookup/getter path intact and supply a conservative fixed-width font
	// descriptor.  A zero glyph map resolves unknown characters to glyph zero while
	// the archived font partition is brought online by the normal filesystem path.
	const (
		registryRows    = 19
		registryColumns = 16
		fontWidth       = 255
		fontHeight      = 255
		fontAdvance     = 16
	)
	registry := make([]byte, registryRows*registryColumns*4)
	for offset := 0; offset < len(registry); offset += 4 {
		binary.LittleEndian.PutUint32(registry[offset:], samsungW340FontDescriptor)
	}
	descriptor := make([]byte, 0x30)
	binary.LittleEndian.PutUint32(descriptor[0x08:], samsungW340FontObject)
	binary.LittleEndian.PutUint16(descriptor[0x22:], fontWidth)
	binary.LittleEndian.PutUint16(descriptor[0x24:], fontHeight)
	binary.LittleEndian.PutUint32(descriptor[0x28:], samsungW340FontGlyphMap)
	binary.LittleEndian.PutUint16(descriptor[0x2c:], fontAdvance)
	descriptor[0x2e] = 1
	return []system.MemorySeed{
		{Address: samsungW340FontRegistry, Bytes: registry},
		{Address: samsungW340FontDescriptor, Bytes: descriptor},
	}
}

func samsungW340CharsetSeeds() []system.MemorySeed {
	// DC18's narrow/wide string veneers all dereference a retained character-set
	// descriptor through 0x02571fa0. The downloadable image contains the complete
	// conversion code but neither the loader-owned descriptor nor its pointer.
	// Retain the single-byte default used before the Korean resource filesystem is
	// mounted: bytes map directly to the low Unicode page, and the inverse tables
	// provide the same mapping for logging and early resource-name conversion.
	descriptor := make([]byte, 0x20)
	binary.LittleEndian.PutUint32(descriptor[0x04:], 1)
	binary.LittleEndian.PutUint32(descriptor[0x10:], samsungW340CharsetDecodeTable)
	binary.LittleEndian.PutUint32(descriptor[0x14:], samsungW340CharsetEncodeTable)
	binary.LittleEndian.PutUint32(descriptor[0x18:], samsungW340CharsetEncodeOffsets)
	binary.LittleEndian.PutUint32(descriptor[0x1c:], samsungW340CharsetEncodeOffsets)

	decode := make([]byte, 256*2)
	encode := make([]byte, 256)
	offsets := make([]byte, 256*2)
	for value := 0; value < 256; value++ {
		binary.LittleEndian.PutUint16(decode[value*2:], uint16(value))
		encode[value] = byte(value)
	}
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340CharsetDescriptor)
	return []system.MemorySeed{
		{Address: samsungW340CharsetStore, Bytes: pointer[:]},
		{Address: samsungW340CharsetDescriptor, Bytes: descriptor},
		{Address: samsungW340CharsetDecodeTable, Bytes: decode},
		{Address: samsungW340CharsetEncodeTable, Bytes: encode},
		{Address: samsungW340CharsetEncodeOffsets, Bytes: offsets},
	}
}

func samsungW340REXObjectListSeed() system.MemorySeed {
	// The native object constructor at 0x01040e36 treats the retained anchor as
	// a circular-list sentinel. Its first insertion copies anchor.next into the
	// new node, then publishes that node as anchor.next. The downloadable image
	// leaves the loader-owned anchor zeroed, which turns the first node into a
	// null-terminated list; a later lookup expects to return to the anchor and
	// instead loops forever after that node has been registered again. Preserve
	// the empty retail state, where anchor.next points back to the anchor.
	var head [4]byte
	binary.LittleEndian.PutUint32(head[:], samsungW340REXObjectListAnchor)
	return system.MemorySeed{Address: samsungW340REXObjectListHeadStore, Bytes: head[:]}
}

func samsungW340CleanupListSeeds() []system.MemorySeed {
	// The retained runtime owns an always-present sentinel for the cleanup list
	// walked at 0x020caa30. An empty downloadable environment leaves the head
	// null, causing the walker to treat the low exception-vector word at +8 as a
	// callback. The retail sentinel's next callback returns itself, terminating
	// the empty-list walk without bypassing the native list implementation.
	sentinel := make([]byte, 0x18)
	binary.LittleEndian.PutUint32(sentinel[0x08:], samsungW340CleanupListNextStub|1)
	stub := make([]byte, 8)
	copy(stub, []byte{0x00, 0x48, 0x70, 0x47}) // ldr r0, [pc]; bx lr
	binary.LittleEndian.PutUint32(stub[4:], samsungW340CleanupListSentinel)
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340CleanupListSentinel)
	return []system.MemorySeed{
		{Address: samsungW340CleanupListHeadStore, Bytes: pointer[:]},
		{Address: samsungW340CleanupListSentinel, Bytes: sentinel},
		{Address: samsungW340CleanupListNextStub, Bytes: stub},
	}
}

func samsungW340SDSSScriptPointerSeed() system.MemorySeed {
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340SDSSScriptBacking)
	return system.MemorySeed{Address: samsungW340SDSSScriptStore, Bytes: pointer[:]}
}

func samsungW340NVBufferPointerSeed() system.MemorySeed {
	// The common NV client stores its 0x80-byte command staging buffer behind a
	// loader-owned pointer. DC18's downloadable image contains the pointer slot
	// but not the retained value, so its first read of item 0x01c5 otherwise asks
	// EFS to copy into address zero. Keep the buffer in the reserved retained
	// scratch page alongside the SDSS state used by the same loader contract.
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340NVBufferBacking)
	return system.MemorySeed{Address: samsungW340NVBufferStore, Bytes: pointer[:]}
}

func samsungW340DeviceTableSeeds() []system.MemorySeed {
	// DC18's loader-owned three-entry device table is consumed by the shared
	// constructor at 0x012cd3b4. Each entry names a 0x48-byte object, the common
	// constructor callback, a device-specific callback, and its native argument.
	// The downloadable AMSS clears the table but expects a retail loader to
	// republish it before the DS task initializes the objects.
	const recordSize = 0x18
	table := make([]byte, 3*recordSize)
	callbacks := [...]uint32{
		samsungW340DeviceCallback0,
		samsungW340DeviceCallback1,
		samsungW340DeviceCallback2,
	}
	arguments := [...]uint32{0, 0x2710, 5}
	for index := range callbacks {
		offset := index * recordSize
		binary.LittleEndian.PutUint32(table[offset+0x00:], uint32(index))
		binary.LittleEndian.PutUint32(
			table[offset+0x04:],
			samsungW340DeviceObjectBacking+uint32(index)*samsungW340DeviceObjectSize,
		)
		binary.LittleEndian.PutUint32(table[offset+0x08:], samsungW340DeviceCommonCallback)
		binary.LittleEndian.PutUint32(table[offset+0x0c:], callbacks[index])
		binary.LittleEndian.PutUint32(table[offset+0x10:], arguments[index])
	}
	return []system.MemorySeed{
		{Address: samsungW340DeviceTable, Bytes: table},
		{Address: samsungW340DeviceObjectBacking, Bytes: make([]byte, 3*samsungW340DeviceObjectSize)},
	}
}

func samsungW340UIInitializationSeed() system.MemorySeed {
	// The UI task consumes this retained-loader byte as a one-shot request. Its
	// native initializer publishes the radio/UI state and clears the byte again.
	return system.MemorySeed{Address: samsungW340UIInitializationFlag, Bytes: []byte{1}}
}

func samsungW340BREWTableOffsetSeed() system.MemorySeed {
	var offsets [16]byte
	binary.LittleEndian.PutUint32(offsets[0:], samsungW340BREWUTFOffset)
	binary.LittleEndian.PutUint32(offsets[4:], samsungW340BREWDictionaryOffset)
	binary.LittleEndian.PutUint32(offsets[8:], samsungW340BREWTableOffset)
	// The optional /brew/shared/eo/content.bun lookup remains zero because that
	// file is not present in DC18's archived FNT container.
	return system.MemorySeed{Address: samsungW340BREWResourceOffsetsStore, Bytes: offsets[:]}
}

func samsungW340LSMCallbackTableSeed() system.MemorySeed {
	// DC18's loader publishes the address of the file-backed LSM callback table
	// in a retained word which the downloadable AMSS otherwise leaves zero.
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340LSMCallbackTable)
	return system.MemorySeed{Address: samsungW340LSMCallbackTableStore, Bytes: pointer[:]}
}

func samsungW340DefaultDispatchObjectSeed() system.MemorySeed {
	// The live DC18 image rewrites these slots as individual BCX services attach.
	// Before that point every slot uses the native unavailable-service callback.
	data := make([]byte, samsungW340DefaultDispatchSlots*4)
	for offset := range samsungW340DefaultDispatchSlots {
		binary.LittleEndian.PutUint32(data[offset*4:], samsungW340BCXUnavailableCallback)
	}
	return system.MemorySeed{Address: samsungW340DefaultDispatchObject, Bytes: data}
}

func samsungW340DIAGBufferPointerSeed() system.MemorySeed {
	// DC18 uses the same 64 KiB Qualcomm DIAG ring layout as CK06. The retained
	// pointer targets byte 0x38 of the static DIAG state block; without it,
	// diagbuf_init writes its initial state byte through address zero and corrupts
	// the exception vectors.
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340DIAGBufferBacking)
	return system.MemorySeed{Address: samsungW340DIAGBufferStore, Bytes: pointer[:]}
}

func samsungW340RadioStatePointerSeed() system.MemorySeed {
	// The modem receiver library keeps its mutable state immediately after the
	// DC18 radio-status byte at 0x0422b1a1. The downloadable AMSS image leaves
	// the cross-image pointer in BSS, while a retail loader republishes it after
	// clearing that BSS. Preserve that retained-loader contract here as well.
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340RadioStateBacking)
	return system.MemorySeed{Address: samsungW340RadioStateStore, Bytes: pointer[:]}
}

func samsungW340RadioWorkspaceSeed() system.MemorySeed {
	// This 0x40-byte descriptor follows the retained grouped-interrupt table.
	// The same receiver implementation in CK06 and CL10 keeps an identical
	// layout; their live descriptors locate each DC18 backing object by stable
	// offsets from the build-local literals used by the shared code.
	data := make([]byte, 0x40)
	for offset, value := range map[int]uint32{
		0x00: 0x00010101,
		0x04: samsungW340RadioWorkspacePrimary,
		0x08: samsungW340RadioWorkspaceBuffer,
		0x0c: samsungW340RadioWorkspacePrimary + 8,
		0x28: samsungW340RadioWorkspaceBuffer,
		0x2c: 1,
		0x30: samsungW340RadioWorkspaceBuffer,
		0x34: 1,
		0x38: samsungW340RadioWorkspaceTail,
		0x3c: samsungW340RadioWorkspaceBuffer,
	} {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	return system.MemorySeed{Address: samsungW340RadioWorkspace, Bytes: data}
}

func samsungW340MGPQueuePoolPointerSeed() system.MemorySeed {
	// q_init at 0x009857b8 consumes 0x6590 words from this retained pool.
	// Keep it in the otherwise-unused EBI tail below the loader scratch area;
	// it must not overlap the BCX pool immediately below the AMSS heap.
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], samsungW340MGPQueuePoolBacking)
	return system.MemorySeed{Address: samsungW340MGPQueuePoolStore, Bytes: pointer[:]}
}

func samsungW4200SharedDirectorySeed() system.MemorySeed {
	const (
		recordStateMutable   = uint32(0xabcdef00)
		recordStateDirectory = uint32(0xabcdef01)
		recordStateSealed    = uint32(0xabcdef02)
		directoryEnd         = uint32(0x884)
		bootRecordOffset     = uint32(0x34)
		bootRecordSize       = uint32(0x1f8)
		configRecordOffset   = bootRecordOffset + bootRecordSize
		configRecordSize     = uint32(0x61c)
	)
	data := make([]byte, samsungW4200SharedDirectorySize)
	put := func(offset, value uint32) {
		binary.LittleEndian.PutUint32(data[offset:offset+4], value)
	}
	record := func(offset, magic, size, state uint32) {
		put(offset+0x00, magic)
		put(offset+0x04, size)
		put(offset+0x08, state)
		put(offset+0x0c, size)
	}

	// Qualcomm's progressive loader publishes a small record directory in the
	// first zero-fill segment. DC17 reserves exactly the newer 0x89c-byte shape
	// also observed on CL10: descriptor, reset handoff, boot metadata, common
	// configuration, two terminal records, and 0x18 bytes of allocator slack.
	record(0x000, 0xa1b2c3d5, 0x18, recordStateDirectory)
	put(0x010, samsungW4200SharedDirectory+0x18)
	put(0x014, samsungW4200SharedDirectory+directoryEnd)
	record(0x018, 0xa1b2c3da, 0x1c, recordStateMutable)
	put(0x028, samsungW4200AMSSResetCookie)
	put(0x030, 0x1a2b3c4d)
	record(bootRecordOffset, 0xa1b2c3d8, bootRecordSize, recordStateSealed)
	record(configRecordOffset, 0xa1b2c3dc, configRecordSize, recordStateMutable)
	record(0x848, 0xa1b2c3d7, 0x14, recordStateSealed)
	put(0x858, 0x0000ffff)
	record(0x85c, 0xa1b2c3d6, 0x10, recordStateMutable)

	// The inline OEMSBL structure starts with a successful-read flag. Its
	// remaining fields are populated from the WBT item/value table by the HLE
	// handlers after each AMSS zero-fill pass.
	config := configRecordOffset + 0x10
	put(config+0x00, 0x103b5d7f)
	put(config+0x04, 1)
	return system.MemorySeed{Address: samsungW4200SharedDirectory, Bytes: data}
}

func readSamsungW4200FeatureConfig(set firmwareset.Set, pkg samsung.Package) ([]byte, error) {
	metadata, ok := pkg.Pieces[samsung.RoleWBT]
	if !ok {
		return nil, fmt.Errorf("read Samsung W4200 feature config: WBT piece is unavailable")
	}
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return nil, fmt.Errorf("read Samsung W4200 feature config WBT piece: %w", err)
	}
	data := make([]byte, samsungW4200FeatureConfigSize)
	read, err := piece.ReadAt(data, samsungW4200FeatureConfigSource)
	if err != nil || read != len(data) {
		return nil, fmt.Errorf(
			"read Samsung W4200 source feature config at %#x: read %d: %w",
			samsungW4200FeatureConfigSource, read, err,
		)
	}
	return data, nil
}

func populateSamsungW4200FeatureConfig(directory, raw []byte) error {
	const (
		configOutputOffset = 0x240
		configOutputSize   = 0x608
		maximumClockItems  = 0xaa
	)
	if len(directory) < configOutputOffset+configOutputSize || len(raw)%8 != 0 {
		return fmt.Errorf("Samsung W4200 feature config: invalid output or input size")
	}
	output := directory[configOutputOffset : configOutputOffset+configOutputSize]
	clear(output)
	binary.LittleEndian.PutUint32(output[0x00:], 1)
	binary.LittleEndian.PutUint32(output[0xb4:], maximumClockItems)
	clockItems := uint32(0)
	for offset := 0; offset+8 <= len(raw); offset += 8 {
		item := binary.LittleEndian.Uint32(raw[offset:])
		value := binary.LittleEndian.Uint32(raw[offset+4:])
		var destination uint32
		switch {
		case item >= 0x108 && item <= 0x10d:
			destination = 0x04 + (item-0x108)*8
		case item >= 0x10e && item <= 0x11f:
			destination = 0x2c + (item-0x10e)*8
		case item >= 0x120 && item <= 0x130:
			if clockItems >= maximumClockItems {
				return fmt.Errorf("Samsung W4200 feature config: too many clock items")
			}
			destination = 0xb8 + clockItems*8
			clockItems++
		case item >= 0x131:
			if item != 0x131 {
				return fmt.Errorf("Samsung W4200 feature config: invalid terminator %#x", item)
			}
			return nil
		default:
			continue
		}
		if destination+8 > uint32(len(output)) {
			return fmt.Errorf("Samsung W4200 feature config: output range %#x", destination)
		}
		binary.LittleEndian.PutUint32(output[destination:], item)
		binary.LittleEndian.PutUint32(output[destination+4:], value)
	}
	return fmt.Errorf("Samsung W4200 feature config: terminator is missing")
}

func samsungQualcommHLEHandlers() map[string]system.HLECallHandler {
	return map[string]system.HLECallHandler{
		system.HLEContractSamsungPowerCycle: system.HLECallHandlerFunc(
			func(system.HLECallContext) error { return nil },
		),
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
		system.HLEContractSamsungW340DOGStartAcknowledgement: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				// The intercepted Thumb BL is 32 bits wide. Select its actual
				// fallthrough explicitly rather than the generic 16-bit inline
				// instruction return.
				return call.CPU.WriteRegister(cpu.RegisterPC, call.Call.Address+4)
			},
		),
		system.HLEContractSamsungW340BCXFirmwareIdentity: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("supply Samsung W340 BCX firmware identity: unavailable CPU")
				}
				if err := call.CPU.WriteMemory(
					samsungW340BCXIdentityData, samsungW340BCXFirmwareIdentity[:],
				); err != nil {
					return fmt.Errorf("publish Samsung W340 BCX firmware identity: %w", err)
				}
				// The native routine caches both "queried" and "matched" at +6/+7.
				if err := call.CPU.WriteMemory(
					samsungW340BCXIdentityState+6, []byte{1, 1},
				); err != nil {
					return fmt.Errorf("publish Samsung W340 BCX identity state: %w", err)
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 1)
			},
		),
		system.HLEContractSamsungW340UIMClockConfiguration: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				return call.CPU.WriteRegister(cpu.RegisterR0, 1)
			},
		),
		system.HLEContractSamsungW340ImageResourceOffset: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 image-resource offset: unavailable CPU")
				}
				offset, err := call.CPU.ReadRegister(cpu.RegisterR0)
				if err != nil {
					return err
				}
				if offset == 0 {
					offset = samsungW340BREWTableOffset
				}
				seed := samsungW340BREWTableOffsetSeed()
				current := append([]byte(nil), seed.Bytes...)
				if err := call.CPU.ReadMemory(seed.Address, current); err != nil {
					return fmt.Errorf("read Samsung W340 shared-resource offsets: %w", err)
				}
				for index, fallback := range []uint32{
					samsungW340BREWUTFOffset,
					samsungW340BREWDictionaryOffset,
				} {
					word := current[index*4 : index*4+4]
					if binary.LittleEndian.Uint32(word) == 0 {
						binary.LittleEndian.PutUint32(word, fallback)
					}
				}
				binary.LittleEndian.PutUint32(current[8:], offset)
				if err := call.CPU.WriteMemory(seed.Address, current); err != nil {
					return fmt.Errorf("publish Samsung W340 shared-resource offsets: %w", err)
				}
				return nil
			},
		),
		system.HLEContractSamsungW340DisplayColor: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("supply Samsung W340 display color: unavailable CPU")
				}
				selector, err := call.CPU.ReadRegister(cpu.RegisterR1)
				if err != nil {
					return err
				}
				output, err := call.CPU.ReadRegister(cpu.RegisterR2)
				if err != nil {
					return err
				}
				if selector == 0 || selector > uint32(len(samsungW340DisplayDefaultColors)) {
					return call.CPU.WriteRegister(cpu.RegisterR0, 0x14) // EUNSUPPORTED
				}
				if output == 0 {
					return call.CPU.WriteRegister(cpu.RegisterR0, 0x0e) // EBADPARM
				}
				var encoded [4]byte
				binary.LittleEndian.PutUint32(encoded[:], samsungW340DisplayDefaultColors[selector-1])
				if err := call.CPU.WriteMemory(output, encoded[:]); err != nil {
					return fmt.Errorf("publish Samsung W340 display color %d: %w", selector, err)
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340FontMetrics: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("supply Samsung W340 font metrics: unavailable CPU")
				}
				const (
					ascent  = uint32(12)
					descent = uint32(4)
				)
				for register, value := range map[uint32]uint32{
					cpu.RegisterR2: ascent,
					cpu.RegisterR3: descent,
				} {
					output, err := call.CPU.ReadRegister(register)
					if err != nil {
						return err
					}
					if output == 0 {
						continue
					}
					var encoded [4]byte
					binary.LittleEndian.PutUint32(encoded[:], value)
					if err := call.CPU.WriteMemory(output, encoded[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 font metric: %w", err)
					}
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, ascent+descent)
			},
		),
		system.HLEContractSamsungW340FontDraw: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("draw Samsung W340 retained font: unavailable CPU")
				}
				// AEEDisp owns clipping and copies the target bitmap to the panel. A
				// retained font with no resident glyph payload still reports success;
				// icons and other bitmap layers can then complete normally.
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340FontMeasure: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("measure Samsung W340 retained font: unavailable CPU")
				}
				text, err := call.CPU.ReadRegister(cpu.RegisterR1)
				if err != nil {
					return err
				}
				characters, err := call.CPU.ReadRegister(cpu.RegisterR2)
				if err != nil {
					return err
				}
				limit, err := call.CPU.ReadRegister(cpu.RegisterR3)
				if err != nil {
					return err
				}
				if characters == ^uint32(0) {
					characters = 0
					for characters < 4096 {
						var encoded [2]byte
						if err := call.CPU.ReadMemory(text+characters*2, encoded[:]); err != nil {
							return fmt.Errorf("read Samsung W340 retained-font string: %w", err)
						}
						if binary.LittleEndian.Uint16(encoded[:]) == 0 {
							break
						}
						characters++
					}
				}
				const advance = uint32(8)
				measuredCharacters := characters
				if limit != 0 && measuredCharacters > limit/advance {
					measuredCharacters = limit / advance
				}
				measuredWidth := measuredCharacters * advance
				stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
				if err != nil {
					return err
				}
				var outputs [8]byte
				if err := call.CPU.ReadMemory(stack, outputs[:]); err != nil {
					return fmt.Errorf("read Samsung W340 retained-font outputs: %w", err)
				}
				for _, output := range []struct {
					address uint32
					value   uint32
				}{
					{binary.LittleEndian.Uint32(outputs[0:]), measuredCharacters},
					{binary.LittleEndian.Uint32(outputs[4:]), measuredWidth},
				} {
					if output.address == 0 {
						continue
					}
					var encoded [4]byte
					binary.LittleEndian.PutUint32(encoded[:], output.value)
					if err := call.CPU.WriteMemory(output.address, encoded[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 retained-font extent: %w", err)
					}
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340FontInfo: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("describe Samsung W340 retained font: unavailable CPU")
				}
				output, err := call.CPU.ReadRegister(cpu.RegisterR1)
				if err != nil {
					return err
				}
				if output != 0 {
					var metrics [4]byte
					binary.LittleEndian.PutUint16(metrics[0:], 12)
					binary.LittleEndian.PutUint16(metrics[2:], 4)
					if err := call.CPU.WriteMemory(output, metrics[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 retained-font info: %w", err)
					}
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340PointerAccessPolicy: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("approve Samsung W340 pointer access: unavailable CPU")
				}
				// The emulator bus still enforces mapped-region permissions. This
				// restores only the missing BREW protection-domain approval bit.
				return call.CPU.WriteRegister(cpu.RegisterR0, 1)
			},
		),
		system.HLEContractSamsungW340ConnectionManager: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 connection manager: unavailable CPU")
				}
				if err := publishSamsungW340ConnectionManager(call.CPU); err != nil {
					return err
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, samsungW340ConnectionManagerObject)
			},
		),
		system.HLEContractSamsungW340MainAppletLifecycle: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 MainApp lifecycle: unavailable CPU")
				}
				owner, err := call.CPU.ReadRegister(cpu.RegisterR4)
				if err != nil {
					return err
				}
				object := make([]byte, 0x2c)
				if err := call.CPU.ReadMemory(owner, object); err != nil {
					return fmt.Errorf("read Samsung W340 MainApp object: %w", err)
				}
				if classID := binary.LittleEndian.Uint32(object[0x04:]); classID != 0x01007002 ||
					binary.LittleEndian.Uint32(object[0x18:]) != 0x012bac79 ||
					binary.LittleEndian.Uint32(object[0x1c:]) != 0x012bac15 {
					return fmt.Errorf("Samsung W340 MainApp signature at %#08x is invalid: %x", owner, object[:0x20])
				}

				state := binary.LittleEndian.Uint32(object[0x20:])
				callback := binary.LittleEndian.Uint32(object[0x28:])
				if state == 0 {
					state = 7
					var encoded [4]byte
					binary.LittleEndian.PutUint32(encoded[:], state)
					if err := call.CPU.WriteMemory(owner+0x20, encoded[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 MainApp ready state: %w", err)
					}
				}
				if callback == 0 {
					var table [8]byte
					// slot zero attaches/detaches the owner; slot one handles the
					// already-native event payload and reports it consumed.
					binary.LittleEndian.PutUint32(table[0:], samsungW340RetainedServiceNoop|1)
					binary.LittleEndian.PutUint32(table[4:], samsungW340DisplayProviderRelease|1)
					if err := call.CPU.WriteMemory(samsungW340MainLifecycleCallbacks, table[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 MainApp lifecycle callbacks: %w", err)
					}
					callback = samsungW340MainLifecycleCallbacks
					var encoded [4]byte
					binary.LittleEndian.PutUint32(encoded[:], callback)
					if err := call.CPU.WriteMemory(owner+0x28, encoded[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 MainApp lifecycle pointer: %w", err)
					}
				}
				if call.Call.Address == 0x012b3692 {
					// The trapped instruction is `movs r6, #0` at the beginning of
					// EVT_APP_START.  Keep its register side effect after publishing
					// the retained lifecycle callback needed later in this handler.
					return call.CPU.WriteRegister(cpu.RegisterR6, 0)
				}
				// Both traps replace `ldr r0, [r4, #0x20]`. Home-screen pixels must
				// come from native drawing; this boundary never composes output.
				return call.CPU.WriteRegister(cpu.RegisterR0, state)
			},
		),
		system.HLEContractSamsungW340IdleCarouselLifecycle: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("route Samsung W340 IdleApp carousel lifecycle: unavailable CPU")
				}
				var encoded [4]byte
				if err := call.CPU.ReadMemory(samsungW340IdleCarouselStore, encoded[:]); err != nil {
					return fmt.Errorf("read Samsung W340 IdleApp carousel store: %w", err)
				}
				carousel := binary.LittleEndian.Uint32(encoded[:])
				if carousel == 0 {
					// The retail initializer normally reconstructs this manager from
					// /brew/stri_data/pst.dat. The downloader archive has neither that
					// provisioned file nor the factory path which creates its first
					// record, so publish the constructor-equivalent empty manager and
					// route through the native add/persist path once.
					managerState := make([]byte, 0xce8)
					binary.LittleEndian.PutUint32(managerState[0x00:], 1)
					binary.LittleEndian.PutUint32(managerState[0x04:], 1)
					binary.LittleEndian.PutUint16(managerState[0x12:], 0xffff)
					for index := uint32(0); index < 5; index++ {
						object := uint32(0x18) + index*0x290
						binary.LittleEndian.PutUint16(managerState[object+0x244:], 0xffff)
					}
					if err := call.CPU.WriteMemory(samsungW340IdleCarouselManagerState, managerState); err != nil {
						return fmt.Errorf("initialize Samsung W340 IdleApp carousel manager: %w", err)
					}

					configuration := make([]byte, 0x28)
					// The native record validator requires a non-zero first duration.
					// All other zero values are valid defaults, including carousel
					// kind zero and the first of its eighteen presentation slots.
					binary.LittleEndian.PutUint16(configuration[0x06:], 1)
					if err := call.CPU.WriteMemory(samsungW340IdleCarouselConfiguration, configuration); err != nil {
						return fmt.Errorf("publish Samsung W340 IdleApp default carousel record: %w", err)
					}
					for register, value := range map[uint32]uint32{
						cpu.RegisterR0: samsungW340IdleCarouselManager,
						cpu.RegisterR1: samsungW340IdleCarouselConfiguration,
						cpu.RegisterR2: 0,
						cpu.RegisterR3: 0,
					} {
						if err := call.CPU.WriteRegister(register, value); err != nil {
							return err
						}
					}
					// The native creator preserves LR, validates the record, writes
					// pst.dat, selects it, and returns to the original IdleApp caller.
					return call.CPU.WriteRegister(cpu.RegisterPC, samsungW340IdleCarouselCreator)
				}
				if err := call.CPU.WriteRegister(cpu.RegisterR0, carousel); err != nil {
					return err
				}
				return call.CPU.WriteRegister(cpu.RegisterPC, samsungW340IdleCarouselUpdater)
			},
		),
		system.HLEContractSamsungW340IdleAppletDependency: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 IdleApp dependency: unavailable CPU")
				}
				output, err := call.CPU.ReadRegister(cpu.RegisterR2)
				if err != nil {
					return err
				}
				if output == 0 {
					return fmt.Errorf("publish Samsung W340 IdleApp dependency: null output")
				}
				if err := publishSamsungW340IdleDependency(call.CPU); err != nil {
					return err
				}
				var pointer [4]byte
				binary.LittleEndian.PutUint32(pointer[:], samsungW340IdleDependencyObject)
				if err := call.CPU.WriteMemory(output, pointer[:]); err != nil {
					return fmt.Errorf("publish Samsung W340 IdleApp dependency result: %w", err)
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340IdleSimMainTarget: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 IdleApp SIM-main target: unavailable CPU")
				}
				stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
				if err != nil {
					return err
				}
				var encoded [4]byte
				if err := call.CPU.ReadMemory(stack+0x28, encoded[:]); err != nil {
					return fmt.Errorf("read Samsung W340 IdleApp SIM-main target result: %w", err)
				}
				target := binary.LittleEndian.Uint32(encoded[:])
				if target == 0 {
					if err := publishSamsungW340IdleSimMain(call.CPU); err != nil {
						return err
					}
					target = samsungW340IdleSimMainObject
					binary.LittleEndian.PutUint32(encoded[:], target)
					if err := call.CPU.WriteMemory(stack+0x28, encoded[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 IdleApp SIM-main target result: %w", err)
					}
				}
				if target == samsungW340IdleSimMainObject {
					// The retail lifecycle provider publishes a paired interface in
					// IdleApp's retained SIM state. Reinitialisation releases only the
					// companion and then reuses the primary target, so restore the pair
					// on every synthetic-target handoff.
					owner, err := call.CPU.ReadRegister(cpu.RegisterR4)
					if err != nil {
						return err
					}
					if err := ensureSamsungW340IdleSimMainCompanion(call.CPU, owner+0x2180); err != nil {
						return err
					}
				}
				// This trap replaces the Thumb LDR at 0x01351376.
				return call.CPU.WriteRegister(cpu.RegisterR0, target)
			},
		),
		system.HLEContractSamsungW340IdleSimMainActivation: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("activate Samsung W340 IdleApp SIM-main target: unavailable CPU")
				}
				owner, err := call.CPU.ReadRegister(cpu.RegisterR4)
				if err != nil {
					return err
				}
				return ensureSamsungW340IdleSimMainCompanion(call.CPU, owner)
			},
		),
		system.HLEContractSamsungW340AnnunciatorStart: system.HLECallHandlerFunc(
			func(system.HLECallContext) error { return nil },
		),
		system.HLEContractSamsungW340IdleExtendedProvider: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 IdleApp extended provider: unavailable CPU")
				}
				owner, err := call.CPU.ReadRegister(cpu.RegisterR4)
				if err != nil {
					return err
				}
				stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
				if err != nil {
					return err
				}
				var pointer [4]byte
				if err := call.CPU.ReadMemory(owner+0x28, pointer[:]); err != nil {
					return fmt.Errorf("read Samsung W340 IdleApp extended provider: %w", err)
				}
				target := binary.LittleEndian.Uint32(pointer[:])
				if target == 0 {
					if err := publishSamsungW340IdleExtended(call.CPU); err != nil {
						return err
					}
					target = samsungW340IdleExtendedObject
					binary.LittleEndian.PutUint32(pointer[:], target)
					if err := call.CPU.WriteMemory(owner+0x28, pointer[:]); err != nil {
						return fmt.Errorf("store Samsung W340 IdleApp extended provider: %w", err)
					}
				}
				if target == samsungW340IdleExtendedObject {
					// The next native call is GetCurrentNetworkInfo(provider,
					// sp+0x2c).  The retained retail provider returned a
					// zero-filled four-word record while the modem was offline.
					// Leaving it untouched feeds stale stack words into the
					// immediately following operation-0x13 constructor.
					if err := call.CPU.WriteMemory(stack+0x2c, make([]byte, 0x10)); err != nil {
						return fmt.Errorf("clear Samsung W340 IdleApp extended-provider result: %w", err)
					}
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, target)
			},
		),
		system.HLEContractSamsungW340IdlePrimaryNotification: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("bridge Samsung W340 IdleApp primary notification: unavailable CPU")
				}
				payload, err := call.CPU.ReadRegister(cpu.RegisterR0)
				if err != nil {
					return err
				}
				var encoded [4]byte
				if err := call.CPU.ReadMemory(payload, encoded[:]); err != nil {
					return fmt.Errorf("read Samsung W340 IdleApp notification type: %w", err)
				}
				kind := binary.LittleEndian.Uint32(encoded[:])
				if kind == samsungW340IdleSecondaryNotification {
					// This trap replaces the literal load at 0x00c67ae2. Matching
					// the retained secondary kind routes its payload through the
					// primary foreground path without modifying caller memory.
					return call.CPU.WriteRegister(cpu.RegisterR3, kind)
				}
				return call.CPU.WriteRegister(cpu.RegisterR3, samsungW340IdlePrimaryNotification)
			},
		),
		system.HLEContractSamsungW340StartupPrimaryInterface: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 startup primary interface: unavailable CPU")
				}
				stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
				if err != nil {
					return err
				}
				var pointer [4]byte
				if err := call.CPU.ReadMemory(stack+0x28, pointer[:]); err != nil {
					return fmt.Errorf("read Samsung W340 startup primary result: %w", err)
				}
				target := binary.LittleEndian.Uint32(pointer[:])
				if target == 0 {
					if err := publishSamsungW340StartupPrimary(call.CPU); err != nil {
						return err
					}
					target = samsungW340StartupPrimaryObject
					binary.LittleEndian.PutUint32(pointer[:], target)
					if err := call.CPU.WriteMemory(stack+0x28, pointer[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 startup primary result: %w", err)
					}
				}
				// This trap replaces the Thumb LDR at 0x01a22064.
				return call.CPU.WriteRegister(cpu.RegisterR0, target)
			},
		),
		system.HLEContractSamsungW340StartupSecondaryInterface: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("publish Samsung W340 startup secondary interface: unavailable CPU")
				}
				stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
				if err != nil {
					return err
				}
				var pointer [4]byte
				if err := call.CPU.ReadMemory(stack+0x24, pointer[:]); err != nil {
					return fmt.Errorf("read Samsung W340 startup secondary result: %w", err)
				}
				target := binary.LittleEndian.Uint32(pointer[:])
				if target == 0 {
					if err := publishSamsungW340StartupSecondary(call.CPU); err != nil {
						return err
					}
					target = samsungW340StartupSecondaryObject
					binary.LittleEndian.PutUint32(pointer[:], target)
					if err := call.CPU.WriteMemory(stack+0x24, pointer[:]); err != nil {
						return fmt.Errorf("publish Samsung W340 startup secondary result: %w", err)
					}
				}
				// This trap replaces the Thumb LDR at 0x01a220ba.
				return call.CPU.WriteRegister(cpu.RegisterR0, target)
			},
		),
		system.HLEContractSamsungW340MGPFrameCounter: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("advance Samsung W340 MGP frame counter: unavailable CPU")
				}
				readWord := func(address uint32) (uint32, error) {
					var encoded [4]byte
					if err := call.CPU.ReadMemory(address, encoded[:]); err != nil {
						return 0, err
					}
					return binary.LittleEndian.Uint32(encoded[:]), nil
				}

				root, err := readWord(0x00ed703c)
				if err != nil {
					return fmt.Errorf("read Samsung W340 MGP clock root: %w", err)
				}
				if root < 0x00080000 || root >= 0x03000000 {
					return fmt.Errorf("invalid Samsung W340 MGP clock root %#08x", root)
				}
				owner, err := readWord(root + 0x10)
				if err != nil {
					return fmt.Errorf("read Samsung W340 MGP clock owner: %w", err)
				}
				if owner < 0x90108000 || owner >= 0x90120000 {
					return fmt.Errorf("invalid Samsung W340 MGP clock owner %#08x", owner)
				}
				clock, err := readWord(owner)
				if err != nil {
					return fmt.Errorf("read Samsung W340 MGP clock object: %w", err)
				}
				if clock < 0x90108000 || clock >= 0x90120000 {
					return fmt.Errorf("invalid Samsung W340 MGP clock object %#08x", clock)
				}
				var selector [1]byte
				if err := call.CPU.ReadMemory(clock+3, selector[:]); err != nil {
					return fmt.Errorf("read Samsung W340 MGP clock selector: %w", err)
				}
				if selector[0] >= 4 {
					return fmt.Errorf("invalid Samsung W340 MGP clock selector %d", selector[0])
				}
				counterAddress := clock + 4 + uint32(selector[0])*4
				counter, err := readWord(counterAddress)
				if err != nil {
					return fmt.Errorf("read Samsung W340 MGP frame counter: %w", err)
				}
				counter++
				var encoded [4]byte
				binary.LittleEndian.PutUint32(encoded[:], counter)
				if err := call.CPU.WriteMemory(counterAddress, encoded[:]); err != nil {
					return fmt.Errorf("advance Samsung W340 MGP frame counter: %w", err)
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, counter)
			},
		),
		system.HLEContractSamsungW340PMICADCConversion: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("convert Samsung W340 PMIC ADC sample: unavailable CPU")
				}
				output, err := call.CPU.ReadRegister(cpu.RegisterR1)
				if err != nil {
					return err
				}
				if err := call.CPU.WriteMemory(output, []byte{0, 0}); err != nil {
					return fmt.Errorf("publish Samsung W340 PMIC ADC sample: %w", err)
				}
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
		system.HLEContractSamsungW340RFSettledDeferred: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				if call.CPU == nil {
					return fmt.Errorf("defer Samsung W340 RF settled state: unavailable CPU")
				}
				// The retail modem companion completes these asynchronous startup
				// publications while the AP-side RF initialiser is sampling the
				// PMIC. The archived AP/MGP pair has no producer for them, so
				// publish the single completed snapshot at the exact final-store
				// boundary instead of repeatedly forcing UI state from the runner.
				for address, value := range map[uint32][]byte{
					0x0403281e: {1, 7}, // startup callback count: next native tick is eight
					0x040350e8: {1},    // initial render request pending
					0x04035108: {1},    // UI update source available
					0x04039606: {1},    // modem boot-ready publication
					0x040ecd1f: {0},    // provisional RF-settled marker cleared
					0x040ecd25: {0},    // idle radio level
				} {
					if err := call.CPU.WriteMemory(address, value); err != nil {
						return fmt.Errorf("publish Samsung W340 modem startup state at %#08x: %w", address, err)
					}
				}
				return nil
			},
		),
		system.HLEContractSamsungOptionalPreloadFile: system.HLECallHandlerFunc(
			func(call system.HLECallContext) error {
				return call.CPU.WriteRegister(cpu.RegisterR0, 0)
			},
		),
	}
}

func samsungQualcommMachineHLEHandlers(
	flash *system.COWFlash,
	secondaryFlash *system.COWFlash,
	board system.BoardProfile,
	progressive samsung.ProgressiveELF,
	partitions []samsung.Partition,
	w4200FeatureConfig []byte,
) map[string]system.HLECallHandler {
	handlers := samsungQualcommHLEHandlers()
	handlers[system.HLEContractSamsungW4200PBLFlashPrepare] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || call.Bus == nil || board.ID != "samsung.sph-w4200" {
				return fmt.Errorf("prepare Samsung W4200 PBL flash interface: unavailable machine")
			}
			var pointer [4]byte
			binary.LittleEndian.PutUint32(pointer[:], samsungW4200PBLFlashContext)
			if err := call.Bus.WriteMemory(
				samsungW4200PBLFlashContextStore, pointer[:], cpu.PermissionWrite,
			); err != nil {
				return fmt.Errorf("publish Samsung W4200 PBL flash context: %w", err)
			}
			context := make([]byte, samsungW4200PBLFlashReadOffset+4)
			binary.LittleEndian.PutUint32(
				context[samsungW4200PBLFlashReadOffset:], samsungW4200PBLFlashReadStub,
			)
			if err := call.Bus.WriteMemory(
				samsungW4200PBLFlashContext, context, cpu.PermissionWrite,
			); err != nil {
				return fmt.Errorf("publish Samsung W4200 PBL flash callbacks: %w", err)
			}
			// The original routine returns one after completing its bounded scan.
			return call.CPU.WriteRegister(cpu.RegisterR0, 1)
		},
	)
	handlers[system.HLEContractSamsungW4200PBLFlashRead] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || secondaryFlash == nil || board.ID != "samsung.sph-w4200" ||
				board.OneNAND == nil {
				return fmt.Errorf("read Samsung W4200 PBL flash: unavailable machine")
			}
			context, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			page, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			destination, err := call.CPU.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return err
			}
			const pageSize = uint64(0x800)
			offset := uint64(page) * pageSize
			if context != samsungW4200PBLFlashContext ||
				offset > uint64(secondaryFlash.Size()) ||
				pageSize > uint64(secondaryFlash.Size())-offset ||
				uint64(destination)+pageSize > 1<<32 {
				return fmt.Errorf(
					"Samsung W4200 PBL flash read range %#x:%#x -> %#x is invalid",
					context, page, destination,
				)
			}
			data := make([]byte, pageSize)
			read, readErr := secondaryFlash.ReadAt(data, int64(offset))
			if readErr != nil || read != len(data) {
				if readErr == nil {
					readErr = fmt.Errorf("short OneNAND read: %d of %d bytes", read, len(data))
				}
				return readErr
			}
			if err := call.CPU.WriteMemory(destination, data); err != nil {
				return err
			}
			// DC17's retained callback uses zero for success.
			return call.CPU.WriteRegister(cpu.RegisterR0, 0)
		},
	)
	handlers[system.HLEContractSamsungProgressiveAMSSLoad] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || call.Bus == nil {
				return fmt.Errorf("load Samsung progressive AMSS: nil CPU or bus")
			}
			if flash == nil || len(progressive.ProgramHeaders) == 0 {
				return fmt.Errorf("load Samsung progressive AMSS: image metadata is unavailable")
			}
			var amssStart uint64
			foundAMSS := false
			for _, partition := range partitions {
				if partition.Name == "0:AMSS" {
					amssStart = partition.Start
					foundAMSS = true
					break
				}
			}
			if !foundAMSS {
				return fmt.Errorf("load Samsung progressive AMSS: 0:AMSS partition is unavailable")
			}

			const (
				elfProgramLoad = uint32(1)
				transferSize   = uint32(0x10000)
			)
			buffer := make([]byte, transferSize)
			zeroes := make([]byte, transferSize)
			loaded := 0
			for index, header := range progressive.ProgramHeaders {
				if header.Type != elfProgramLoad {
					continue
				}
				memoryEnd := uint64(header.PhysicalAddress) + uint64(header.MemorySize)
				fileOffset := header.Offset
				if board.ID == "samsung.sph-w4200" {
					// DC17's progressive image keeps Qualcomm HASH (2) and
					// BOOT (5) segments at their physical-address offsets.
					// Their ELF p_offset values describe the logical signed
					// stream and are lower by the 0x12e000 loader window.
					// The first header segment and the high shared-config
					// segments retain ordinary p_offset placement.
					switch header.Flags >> 24 {
					case 2, 5:
						fileOffset = header.PhysicalAddress
					}
				}
				fileStart := amssStart + uint64(fileOffset)
				fileEnd := fileStart + uint64(header.FileSize)
				if memoryEnd > 1<<32 || fileEnd < fileStart || fileEnd > uint64(flash.Size()) ||
					header.FileSize > header.MemorySize {
					return fmt.Errorf("load Samsung progressive AMSS segment %d: invalid range", index)
				}
				// Qualcomm packages also describe non-EBI auxiliary segments in the
				// same ELF. Only materialise ranges backed by this board's RAM map.
				if header.MemorySize != 0 {
					if err := call.Bus.ReadMemory(
						header.PhysicalAddress, buffer[:1], cpu.PermissionRead,
					); err != nil {
						continue
					}
				}
				for offset := uint32(0); offset < header.FileSize; {
					count := min(transferSize, header.FileSize-offset)
					read, err := flash.ReadAt(buffer[:count], int64(fileStart+uint64(offset)))
					if err != nil || read != int(count) {
						return fmt.Errorf("load Samsung progressive AMSS segment %d at %#x: read %d: %w", index, fileStart+uint64(offset), read, err)
					}
					if err := call.Bus.WriteMemory(header.PhysicalAddress+offset, buffer[:count], cpu.PermissionWrite); err != nil {
						return fmt.Errorf("load Samsung progressive AMSS segment %d at %#x: %w", index, header.PhysicalAddress+offset, err)
					}
					offset += count
				}
				for offset := header.FileSize; offset < header.MemorySize; {
					count := min(transferSize, header.MemorySize-offset)
					if err := call.Bus.WriteMemory(header.PhysicalAddress+offset, zeroes[:count], cpu.PermissionWrite); err != nil {
						return fmt.Errorf("zero Samsung progressive AMSS segment %d at %#x: %w", index, header.PhysicalAddress+offset, err)
					}
					offset += count
				}
				loaded++
			}
			if loaded == 0 {
				return fmt.Errorf("load Samsung progressive AMSS: no mapped load segments")
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, progressive.Entry)
		},
	)
	handlers[system.HLEContractSamsungSharedDirectoryInit] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || call.Bus == nil {
				return fmt.Errorf("initialise Samsung shared directory: nil CPU or bus")
			}
			address, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			length, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			if board.ID != "samsung.sph-w4200" || address != samsungW4200SharedDirectory ||
				length < samsungW4200SharedDirectorySize {
				return fmt.Errorf("initialise Samsung W4200 shared directory: unexpected range %#x+%#x", address, length)
			}
			seed := samsungW4200SharedDirectorySeed()
			if err := populateSamsungW4200FeatureConfig(seed.Bytes, w4200FeatureConfig); err != nil {
				return err
			}
			if err := call.Bus.WriteMemory(seed.Address, seed.Bytes, cpu.PermissionWrite); err != nil {
				return fmt.Errorf("publish Samsung W4200 shared directory: %w", err)
			}
			var pointer [4]byte
			binary.LittleEndian.PutUint32(pointer[:], seed.Address)
			if err := call.Bus.WriteMemory(
				samsungW4200SharedDirectoryStore, pointer[:], cpu.PermissionWrite,
			); err != nil {
				return fmt.Errorf("publish Samsung W4200 shared-directory pointer: %w", err)
			}
			return nil
		},
	)
	handlers[system.HLEContractSamsungSharedDirectoryAttach] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil {
				return fmt.Errorf("attach Samsung shared directory: nil CPU")
			}
			if board.ID != "samsung.sph-w4200" {
				return fmt.Errorf("attach Samsung W4200 shared directory: unexpected board %q", board.ID)
			}
			seed := samsungW4200SharedDirectorySeed()
			if err := populateSamsungW4200FeatureConfig(seed.Bytes, w4200FeatureConfig); err != nil {
				return err
			}
			if err := call.CPU.WriteMemory(seed.Address, seed.Bytes); err != nil {
				return fmt.Errorf("attach Samsung W4200 shared directory: %w", err)
			}
			var pointer [4]byte
			binary.LittleEndian.PutUint32(pointer[:], seed.Address)
			if err := call.CPU.WriteMemory(samsungW4200RuntimeDirectoryStore, pointer[:]); err != nil {
				return fmt.Errorf("attach Samsung W4200 runtime directory pointer: %w", err)
			}
			return nil
		},
	)
	handlers[system.HLEContractSamsungAMSSFlashEnvironment] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil {
				return fmt.Errorf("publish Samsung AMSS flash environment: nil CPU")
			}
			stack, err := call.CPU.ReadRegister(cpu.RegisterSP)
			if err != nil {
				return err
			}
			seed := samsungW340FlashEnvironmentSeed()
			if err := call.CPU.WriteMemory(seed.Address, seed.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 flash environment: %w", err)
			}
			staticModules := samsungW340StaticModuleListSeed()
			if err := call.CPU.WriteMemory(staticModules.Address, staticModules.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 static module list: %w", err)
			}
			for _, classRecord := range samsungW340DownloadableClassRecordSeeds() {
				if err := call.CPU.WriteMemory(classRecord.Address, classRecord.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 downloadable class record: %w", err)
				}
			}
			if err := publishSamsungW340StaticClasses(call.CPU); err != nil {
				return err
			}
			if err := publishSamsungW340RetainedService(call.CPU); err != nil {
				return err
			}
			if err := publishSamsungW340ConnectionManager(call.CPU); err != nil {
				return err
			}
			if err := publishSamsungW340DisplayProvider(call.CPU); err != nil {
				return err
			}
			for _, nvmSeed := range samsungW340BREWNVMSeeds() {
				if err := call.CPU.WriteMemory(nvmSeed.Address, nvmSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 BREW NVM record: %w", err)
				}
			}
			for _, timerSeed := range samsungW340PosDetTimerSeeds() {
				if err := call.CPU.WriteMemory(timerSeed.Address, timerSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 position timer owner: %w", err)
				}
			}
			moduleInitializer := samsungW340ModuleInitializerSeed()
			if err := call.CPU.WriteMemory(moduleInitializer.Address, moduleInitializer.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 module initializer: %w", err)
			}
			mgpQueuePool := samsungW340MGPQueuePoolPointerSeed()
			if err := call.CPU.WriteMemory(mgpQueuePool.Address, mgpQueuePool.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 MGP queue-pool pointer: %w", err)
			}
			bcxPool := samsungW340BCXPoolPointerSeed()
			if err := call.CPU.WriteMemory(bcxPool.Address, bcxPool.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 BCX pool pointer: %w", err)
			}
			bcimAVSignalMask := samsungW340BCIMAVSignalMaskSeed()
			if err := call.CPU.WriteMemory(bcimAVSignalMask.Address, bcimAVSignalMask.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 BCIM_AV signal mask: %w", err)
			}
			bcimSignalMask := samsungW340BCIMSignalMaskSeed()
			if err := call.CPU.WriteMemory(bcimSignalMask.Address, bcimSignalMask.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 BCIM signal mask: %w", err)
			}
			for _, interfaceSeed := range samsungW340BCXInterfaceSeeds() {
				if err := call.CPU.WriteMemory(interfaceSeed.Address, interfaceSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 BCX interface: %w", err)
				}
			}
			deviceVTable := samsungW340EFS2DeviceVTableSeed()
			if err := call.CPU.WriteMemory(deviceVTable.Address, deviceVTable.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 EFS2 device vtable: %w", err)
			}
			fileVTable := samsungW340POSIXFileVTableSeed()
			if err := call.CPU.WriteMemory(fileVTable.Address, fileVTable.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 POSIX file vtable: %w", err)
			}
			deviceCallback := samsungW340EFS2DeviceCallbackSeed()
			if err := call.CPU.WriteMemory(deviceCallback.Address, deviceCallback.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 EFS2 device callback: %w", err)
			}
			absentDeviceHandler := samsungW340EFS2AbsentDeviceHandlerSeed()
			if err := call.CPU.WriteMemory(absentDeviceHandler.Address, absentDeviceHandler.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 absent EFS2 device handler: %w", err)
			}
			irqHandler := samsungW340IRQHandlerSeed()
			if err := call.CPU.WriteMemory(irqHandler.Address, irqHandler.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 IRQ handler: %w", err)
			}
			interruptTable := samsungW340InterruptTableSeed()
			if err := call.CPU.WriteMemory(interruptTable.Address, interruptTable.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 interrupt table: %w", err)
			}
			groupedInterrupt := samsungW340UIMGroupedInterruptSeed()
			if err := call.CPU.WriteMemory(groupedInterrupt.Address, groupedInterrupt.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 UIM grouped interrupt: %w", err)
			}
			// The same retained loader environment owns the first interrupt-service
			// dispatch slot. DC18 resolves service 0x10e to index zero while bringing
			// up RF backup storage. Leaving this word at the progressive segment's
			// zero fill turns the indirect call into a branch to the reset vector.
			var interruptHandler [4]byte
			binary.LittleEndian.PutUint32(interruptHandler[:], samsungW340InterruptZeroHandler)
			if err := call.CPU.WriteMemory(samsungW340InterruptDispatch, interruptHandler[:]); err != nil {
				return fmt.Errorf("publish Samsung W340 interrupt dispatch: %w", err)
			}
			for _, fontSeed := range samsungW340FontFallbackSeeds() {
				if err := call.CPU.WriteMemory(fontSeed.Address, fontSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 font registry: %w", err)
				}
			}
			for _, charsetSeed := range samsungW340CharsetSeeds() {
				if err := call.CPU.WriteMemory(charsetSeed.Address, charsetSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 character set: %w", err)
				}
			}
			rexObjectList := samsungW340REXObjectListSeed()
			if err := call.CPU.WriteMemory(rexObjectList.Address, rexObjectList.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 REX object-list anchor: %w", err)
			}
			for _, listSeed := range samsungW340CleanupListSeeds() {
				if err := call.CPU.WriteMemory(listSeed.Address, listSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 cleanup-list sentinel: %w", err)
				}
			}
			scriptPointer := samsungW340SDSSScriptPointerSeed()
			if err := call.CPU.WriteMemory(scriptPointer.Address, scriptPointer.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 SD script state: %w", err)
			}
			nvBuffer := samsungW340NVBufferPointerSeed()
			if err := call.CPU.WriteMemory(nvBuffer.Address, nvBuffer.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 NV buffer pointer: %w", err)
			}
			for _, deviceSeed := range samsungW340DeviceTableSeeds() {
				if err := call.CPU.WriteMemory(deviceSeed.Address, deviceSeed.Bytes); err != nil {
					return fmt.Errorf("publish Samsung W340 device table: %w", err)
				}
			}
			uiInitialization := samsungW340UIInitializationSeed()
			if err := call.CPU.WriteMemory(uiInitialization.Address, uiInitialization.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 UI initialization flag: %w", err)
			}
			brewTableOffset := samsungW340BREWTableOffsetSeed()
			if err := call.CPU.WriteMemory(brewTableOffset.Address, brewTableOffset.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 BREW resource-table offset: %w", err)
			}
			lsmCallbacks := samsungW340LSMCallbackTableSeed()
			if err := call.CPU.WriteMemory(lsmCallbacks.Address, lsmCallbacks.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 LSM callback table: %w", err)
			}
			dispatchObject := samsungW340DefaultDispatchObjectSeed()
			if err := call.CPU.WriteMemory(dispatchObject.Address, dispatchObject.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 default dispatch object: %w", err)
			}
			diagBuffer := samsungW340DIAGBufferPointerSeed()
			if err := call.CPU.WriteMemory(diagBuffer.Address, diagBuffer.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 DIAG buffer pointer: %w", err)
			}
			radioState := samsungW340RadioStatePointerSeed()
			if err := call.CPU.WriteMemory(radioState.Address, radioState.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 radio-state pointer: %w", err)
			}
			radioWorkspace := samsungW340RadioWorkspaceSeed()
			if err := call.CPU.WriteMemory(radioWorkspace.Address, radioWorkspace.Bytes); err != nil {
				return fmt.Errorf("publish Samsung W340 radio workspace: %w", err)
			}
			// Reproduce the intercepted ARM `add sp, sp, #0x28` epilogue instruction.
			return call.CPU.WriteRegister(cpu.RegisterSP, stack+0x28)
		},
	)
	handlers[system.HLEContractSamsungAMSSBulkZero] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil {
				return fmt.Errorf("Samsung AMSS bulk zero: nil CPU")
			}
			address, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			length, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			if length&3 != 0 || uint64(address)+uint64(length) > 1<<32 || length > 0x08000000 {
				return fmt.Errorf("Samsung AMSS bulk-zero range %#x+%#x is invalid", address, length)
			}
			zeroes := make([]byte, 0x10000)
			for offset := uint32(0); offset < length; {
				count := min(uint32(len(zeroes)), length-offset)
				if err := call.CPU.WriteMemory(address+offset, zeroes[:count]); err != nil {
					return fmt.Errorf("Samsung AMSS bulk zero at %#x: %w", address+offset, err)
				}
				offset += count
			}
			// DC18's AMSS BSS includes the loader-owned top-level IRQ dispatch
			// pointer. Restore it after the accelerated zero pass, at the same
			// boundary where the retained retail loader republishes its state.
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340StaticModuleList) >= uint64(address) &&
				uint64(samsungW340StaticModuleList)+uint64(samsungW340StaticModuleCount)*4 <= uint64(address)+uint64(length) {
				staticModules := samsungW340StaticModuleListSeed()
				if err := call.CPU.WriteMemory(staticModules.Address, staticModules.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 static module list after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" {
				for _, classRecord := range samsungW340DownloadableClassRecordSeeds() {
					if uint64(classRecord.Address) < uint64(address) ||
						uint64(classRecord.Address)+uint64(len(classRecord.Bytes)) > uint64(address)+uint64(length) {
						continue
					}
					if err := call.CPU.WriteMemory(classRecord.Address, classRecord.Bytes); err != nil {
						return fmt.Errorf("restore Samsung W340 downloadable class record after bulk zero: %w", err)
					}
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340BREWResourceOffsetsStore) >= uint64(address) &&
				uint64(samsungW340BREWResourceOffsetsStore)+16 <= uint64(address)+uint64(length) {
				brewTableOffset := samsungW340BREWTableOffsetSeed()
				if err := call.CPU.WriteMemory(brewTableOffset.Address, brewTableOffset.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 BREW resource-table offset after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340StaticClassList) >= uint64(address) &&
				uint64(samsungW340StaticClassList)+8 <= uint64(address)+uint64(length) {
				if err := publishSamsungW340StaticClasses(call.CPU); err != nil {
					return err
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340RetainedServiceStore) >= uint64(address) &&
				uint64(samsungW340RetainedServiceStore)+4 <= uint64(address)+uint64(length) {
				if err := publishSamsungW340RetainedService(call.CPU); err != nil {
					return err
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340ConnectionManagerStore) >= uint64(address) &&
				uint64(samsungW340ConnectionManagerStore)+4 <= uint64(address)+uint64(length) {
				if err := publishSamsungW340ConnectionManager(call.CPU); err != nil {
					return err
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340DisplayProviderStore) >= uint64(address) &&
				uint64(samsungW340DisplayProviderStore)+4 <= uint64(address)+uint64(length) {
				if err := publishSamsungW340DisplayProvider(call.CPU); err != nil {
					return err
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340BREWNVMFileTable) >= uint64(address) &&
				uint64(samsungW340BREWNVMFileTable)+12 <= uint64(address)+uint64(length) {
				for _, nvmSeed := range samsungW340BREWNVMSeeds() {
					if err := call.CPU.WriteMemory(nvmSeed.Address, nvmSeed.Bytes); err != nil {
						return fmt.Errorf("restore Samsung W340 BREW NVM record after bulk zero: %w", err)
					}
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340PosDetTimerOwnerStore) >= uint64(address) &&
				uint64(samsungW340PosDetTimerOwnerStore)+4 <= uint64(address)+uint64(length) {
				for _, timerSeed := range samsungW340PosDetTimerSeeds() {
					if err := call.CPU.WriteMemory(timerSeed.Address, timerSeed.Bytes); err != nil {
						return fmt.Errorf("restore Samsung W340 position timer owner after bulk zero: %w", err)
					}
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340ModuleInitializerStore) >= uint64(address) &&
				uint64(samsungW340ModuleInitializerStore)+uint64(len(samsungW340ModuleInitializerSeed().Bytes)) <= uint64(address)+uint64(length) {
				moduleInitializer := samsungW340ModuleInitializerSeed()
				if err := call.CPU.WriteMemory(moduleInitializer.Address, moduleInitializer.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 module initializer after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340IRQHandlerStore) >= uint64(address) &&
				uint64(samsungW340IRQHandlerStore)+4 <= uint64(address)+uint64(length) {
				irqHandler := samsungW340IRQHandlerSeed()
				if err := call.CPU.WriteMemory(irqHandler.Address, irqHandler.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 IRQ handler after bulk zero: %w", err)
				}
				interruptTable := samsungW340InterruptTableSeed()
				if err := call.CPU.WriteMemory(interruptTable.Address, interruptTable.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 interrupt table after bulk zero: %w", err)
				}
				groupedInterrupt := samsungW340UIMGroupedInterruptSeed()
				if err := call.CPU.WriteMemory(groupedInterrupt.Address, groupedInterrupt.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 UIM grouped interrupt after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340SDSSScriptStore) >= uint64(address) &&
				uint64(samsungW340SDSSScriptStore)+4 <= uint64(address)+uint64(length) {
				scriptPointer := samsungW340SDSSScriptPointerSeed()
				if err := call.CPU.WriteMemory(scriptPointer.Address, scriptPointer.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 SD script state after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340NVBufferStore) >= uint64(address) &&
				uint64(samsungW340NVBufferStore)+4 <= uint64(address)+uint64(length) {
				nvBuffer := samsungW340NVBufferPointerSeed()
				if err := call.CPU.WriteMemory(nvBuffer.Address, nvBuffer.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 NV buffer pointer after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340DeviceTable) >= uint64(address) &&
				uint64(samsungW340DeviceTable)+3*0x18 <= uint64(address)+uint64(length) {
				for _, deviceSeed := range samsungW340DeviceTableSeeds() {
					if err := call.CPU.WriteMemory(deviceSeed.Address, deviceSeed.Bytes); err != nil {
						return fmt.Errorf("restore Samsung W340 device table after bulk zero: %w", err)
					}
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340UIInitializationFlag) >= uint64(address) &&
				uint64(samsungW340UIInitializationFlag)+1 <= uint64(address)+uint64(length) {
				uiInitialization := samsungW340UIInitializationSeed()
				if err := call.CPU.WriteMemory(uiInitialization.Address, uiInitialization.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 UI initialization flag after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340LSMCallbackTableStore) >= uint64(address) &&
				uint64(samsungW340LSMCallbackTableStore)+4 <= uint64(address)+uint64(length) {
				lsmCallbacks := samsungW340LSMCallbackTableSeed()
				if err := call.CPU.WriteMemory(lsmCallbacks.Address, lsmCallbacks.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 LSM callback table after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340DefaultDispatchObject) >= uint64(address) &&
				uint64(samsungW340DefaultDispatchObject)+uint64(samsungW340DefaultDispatchSlots*4) <= uint64(address)+uint64(length) {
				dispatchObject := samsungW340DefaultDispatchObjectSeed()
				if err := call.CPU.WriteMemory(dispatchObject.Address, dispatchObject.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 default dispatch object after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340DIAGBufferStore) >= uint64(address) &&
				uint64(samsungW340DIAGBufferStore)+4 <= uint64(address)+uint64(length) {
				diagBuffer := samsungW340DIAGBufferPointerSeed()
				if err := call.CPU.WriteMemory(diagBuffer.Address, diagBuffer.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 DIAG buffer pointer after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340RadioStateStore) >= uint64(address) &&
				uint64(samsungW340RadioStateStore)+4 <= uint64(address)+uint64(length) {
				radioState := samsungW340RadioStatePointerSeed()
				if err := call.CPU.WriteMemory(radioState.Address, radioState.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 radio-state pointer after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340MGPQueuePoolStore) >= uint64(address) &&
				uint64(samsungW340MGPQueuePoolStore)+4 <= uint64(address)+uint64(length) {
				mgpQueuePool := samsungW340MGPQueuePoolPointerSeed()
				if err := call.CPU.WriteMemory(mgpQueuePool.Address, mgpQueuePool.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 MGP queue-pool pointer after bulk zero: %w", err)
				}
			}
			if board.ID == "samsung.sch-w340" &&
				uint64(samsungW340RadioWorkspace) >= uint64(address) &&
				uint64(samsungW340RadioWorkspace)+0x40 <= uint64(address)+uint64(length) {
				radioWorkspace := samsungW340RadioWorkspaceSeed()
				if err := call.CPU.WriteMemory(radioWorkspace.Address, radioWorkspace.Bytes); err != nil {
					return fmt.Errorf("restore Samsung W340 radio workspace after bulk zero: %w", err)
				}
			}
			blocks := length >> 5
			if blocks >= 0x1fff {
				if err := call.CPU.WriteMemory(0x8000540c, []byte{1}); err != nil {
					return fmt.Errorf("service Samsung AMSS bulk-zero watchdog: %w", err)
				}
			}
			for _, register := range []uint32{cpu.RegisterR8, cpu.RegisterR9, cpu.RegisterR10} {
				if err := call.CPU.WriteRegister(register, 0); err != nil {
					return err
				}
			}
			if err := call.CPU.WriteRegister(cpu.RegisterR11, blocks%0x1fff); err != nil {
				return err
			}
			if err := call.CPU.WriteRegister(cpu.RegisterR12, 0); err != nil {
				return err
			}
			if err := call.CPU.WriteRegister(cpu.RegisterR2, 0x1fff); err != nil {
				return err
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, address+length)
		},
	)
	handlers[system.HLEContractSamsungW350StaticBSSZero] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || board.ID != "samsung.sch-w350" {
				return fmt.Errorf("service Samsung W350 static BSS zero: unavailable machine")
			}
			const descriptorAddress = uint32(0x000e2d0c)
			descriptors := make([]byte, 16)
			if err := call.CPU.ReadMemory(descriptorAddress, descriptors); err != nil {
				return fmt.Errorf("read Samsung W350 BSS descriptors: %w", err)
			}
			var finalAddress uint32
			zeroes := make([]byte, 0x10000)
			for index := 0; index < 2; index++ {
				address := binary.LittleEndian.Uint32(descriptors[index*8:])
				length := binary.LittleEndian.Uint32(descriptors[index*8+4:])
				if address&3 != 0 || length&3 != 0 || length == 0 ||
					uint64(address)+uint64(length) > 1<<32 || length > 0x08000000 {
					return fmt.Errorf(
						"Samsung W350 BSS descriptor %d range %#x+%#x is invalid",
						index, address, length,
					)
				}
				for offset := uint32(0); offset < length; {
					count := min(uint32(len(zeroes)), length-offset)
					if err := call.CPU.WriteMemory(address+offset, zeroes[:count]); err != nil {
						return fmt.Errorf("zero Samsung W350 BSS at %#x: %w", address+offset, err)
					}
					offset += count
				}
				finalAddress = address + length
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: finalAddress,
				cpu.RegisterR1: finalAddress,
				cpu.RegisterR2: 0,
			} {
				if err := call.CPU.WriteRegister(register, value); err != nil {
					return err
				}
			}
			return nil
		},
	)
	handlers[system.HLEContractSamsungW350OperatorProvisioning] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || board.ID != "samsung.sch-w350" {
				return fmt.Errorf("supply Samsung W350 operator provisioning: unavailable machine")
			}
			item, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			if item != 0x1301 {
				return fmt.Errorf("Samsung W350 operator provisioning item = %#x, want 0x1301", item)
			}
			output, err := call.CPU.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return err
			}
			if err := call.CPU.WriteMemory(output, []byte{0}); err != nil {
				return fmt.Errorf("write Samsung W350 operator provisioning result: %w", err)
			}
			// CK06 rebuilds this compact NV cache after clearing AMSS BSS. The
			// downloadable image supplies the default value one, while a retail SKT
			// handset carries zero in EFS. Normalize the verified record as well as
			// this call's output so later native readers observe the same state.
			const recordAddress = uint32(0x03962652)
			record := make([]byte, 8)
			if err := call.CPU.ReadMemory(recordAddress, record); err != nil {
				return fmt.Errorf("read Samsung W350 operator NV cache: %w", err)
			}
			if !bytes.Equal(record[:7], []byte{'4', '8', '6', '5', 0x6e, 0xff, 0x01}) {
				return fmt.Errorf("Samsung W350 operator NV cache signature = %x", record)
			}
			if err := call.CPU.WriteMemory(recordAddress+7, []byte{0}); err != nil {
				return fmt.Errorf("write Samsung W350 operator NV cache: %w", err)
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, 0)
		},
	)
	handlers[system.HLEContractSamsungW340SBITransaction] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || board.ID != "samsung.sch-w340" {
				return fmt.Errorf("service Samsung W340 SBI transaction: unavailable machine")
			}
			// DC18 uses zero as the successful SBI result. Read transactions retain
			// the caller-provided receive buffer, which models an idle peripheral.
			return call.CPU.WriteRegister(cpu.RegisterR0, 0)
		},
	)
	badBlocks := make(map[uint32]struct{}, len(board.NANDFactoryBadBlocks))
	for _, block := range board.NANDFactoryBadBlocks {
		badBlocks[block] = struct{}{}
	}
	handlers[system.HLEContractQualcommPBLNANDBadBlock] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || flash == nil || board.NANDEraseBlockSize == 0 {
				return fmt.Errorf("Qualcomm PBL NAND bad-block callback is unavailable")
			}
			block, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			blockCount := uint64(flash.Size()) / uint64(board.NANDEraseBlockSize)
			if uint64(block) >= blockCount {
				return fmt.Errorf("Qualcomm PBL NAND block 0x%x is out of range", block)
			}
			result := uint32(0)
			if _, bad := badBlocks[block]; bad {
				result = 1
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, result)
		},
	)
	handlers[system.HLEContractQualcommPBLNANDRead] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || flash == nil || board.NANDPageSize == 0 {
				return fmt.Errorf("Qualcomm PBL NAND read callback is unavailable")
			}
			registers := make([]uint32, 5)
			for index, register := range []uint32{
				cpu.RegisterR0, cpu.RegisterR1, cpu.RegisterR2, cpu.RegisterR3, cpu.RegisterSP,
			} {
				value, err := call.CPU.ReadRegister(register)
				if err != nil {
					return err
				}
				registers[index] = value
			}
			if registers[0] != 2 || registers[1] != board.NANDPageSize {
				return fmt.Errorf(
					"Qualcomm PBL NAND read ABI type/page-size %#x/%#x is unsupported",
					registers[0], registers[1],
				)
			}
			countBytes := make([]byte, 4)
			if err := call.CPU.ReadMemory(registers[4], countBytes); err != nil {
				return err
			}
			pageCount := binary.LittleEndian.Uint32(countBytes)
			if pageCount == 0 || uint64(pageCount)*uint64(board.NANDPageSize) > system.MaxHandoffSeedBytes {
				return fmt.Errorf("Qualcomm PBL NAND page count 0x%x is invalid", pageCount)
			}
			byteCount := uint64(pageCount) * uint64(board.NANDPageSize)
			flashOffset := uint64(registers[2]) * uint64(board.NANDPageSize)
			flashSize := uint64(flash.Size())
			if flashOffset > flashSize || byteCount > flashSize-flashOffset ||
				uint64(registers[3])+byteCount > 1<<32 {
				return fmt.Errorf("Qualcomm PBL NAND read range is invalid")
			}
			data := make([]byte, int(byteCount))
			read, err := flash.ReadAt(data, int64(flashOffset))
			if err != nil || read != len(data) {
				if err == nil {
					err = fmt.Errorf("short NAND read: %d of %d bytes", read, len(data))
				}
				return err
			}
			if err := call.CPU.WriteMemory(registers[3], data); err != nil {
				return err
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, 1)
		},
	)
	handlers[system.HLEContractQualcommPBLFatal] = system.HLECallHandlerFunc(
		func(system.HLECallContext) error {
			return fmt.Errorf("retained Qualcomm PBL reported a fatal error")
		},
	)
	return handlers
}

func attachSamsungW4200TouchHLEHandlers(
	handlers map[string]system.HLECallHandler,
	touchscreen *system.QualcommTSC2007,
) {
	handlers[system.HLEContractSamsungW4200TSC2007Write] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || touchscreen == nil {
				return fmt.Errorf("write Samsung W4200 TSC2007: unavailable machine")
			}
			bus, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			command, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			length, err := call.CPU.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return err
			}
			if bus != 0 || command > 0xff || length != 1 {
				return fmt.Errorf("Samsung W4200 TSC2007 write ABI %#x/%#x/%#x is unsupported", bus, command, length)
			}
			touchscreen.WriteCommand(uint8(command))
			return call.CPU.WriteRegister(cpu.RegisterR0, 0)
		},
	)
	handlers[system.HLEContractSamsungW4200TSC2007Read] = system.HLECallHandlerFunc(
		func(call system.HLECallContext) error {
			if call.CPU == nil || touchscreen == nil {
				return fmt.Errorf("read Samsung W4200 TSC2007: unavailable machine")
			}
			bus, err := call.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return err
			}
			selector, err := call.CPU.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return err
			}
			length, err := call.CPU.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return err
			}
			if bus != 0 || selector != 9 || length != 2 {
				return fmt.Errorf("Samsung W4200 TSC2007 read ABI %#x/%#x/%#x is unsupported", bus, selector, length)
			}
			return call.CPU.WriteRegister(cpu.RegisterR0, uint32(touchscreen.ReadSample()))
		},
	)
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

func newInterpreterBackend(
	mode CPUBackendMode,
	compatibility interpreter.CompatibilityOptions,
) (cpu.Backend, error) {
	switch mode {
	case "", CPUBackendPrecise:
		return interpreter.NewWithCompatibility(compatibility), nil
	case CPUBackendJIT:
		return interpreter.NewJITWithOptions(interpreter.JITOptions{Compatibility: compatibility}), nil
	case CPUBackendJITLoops:
		return interpreter.NewJITWithOptions(interpreter.JITOptions{
			LoopAcceleration: true,
			Compatibility:    compatibility,
		}), nil
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
	touchscreen *system.QualcommTSC2007,
) error {
	existingWindows := make(map[string]*system.LatchedRegisterWindow)
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
	if board.ID == "samsung.sph-w4200.dc17" {
		const indirectBase = uint32(0x30006000)
		expected := map[string]system.LatchedRegisterProfile{
			"w4200-indirect-bus-control": {
				ID: "w4200-indirect-bus-control", Address: indirectBase,
				Width: system.Width16,
			},
			"w4200-indirect-bus-address-low": {
				ID: "w4200-indirect-bus-address-low", Address: indirectBase + 8,
				Width: system.Width16,
			},
			"w4200-indirect-bus-address-high": {
				ID: "w4200-indirect-bus-address-high", Address: indirectBase + 10,
				Width: system.Width16,
			},
			"w4200-indirect-bus-data": {
				ID: "w4200-indirect-bus-data", Address: indirectBase + 12,
				Width: system.Width32, AdditionalWidths: []system.Width{system.Width16},
				AllowSubwordOffsets: true,
			},
		}
		remaining := make([]system.LatchedRegisterProfile, 0, len(board.LatchedRegisters)-len(expected))
		for _, spec := range board.LatchedRegisters {
			want, replace := expected[spec.ID]
			if !replace {
				remaining = append(remaining, spec)
				continue
			}
			if !reflect.DeepEqual(spec, want) {
				return fmt.Errorf("SPH-W4200 indirect register profile %q changed: got %+v", spec.ID, spec)
			}
			delete(expected, spec.ID)
		}
		if len(expected) != 0 {
			return fmt.Errorf("SPH-W4200 indirect register profile is incomplete: %v", expected)
		}
		indirectBus, err := system.NewQualcommIndirectRegisterBus(0x30000608)
		if err != nil {
			return fmt.Errorf("create SPH-W4200 indirect register bus: %w", err)
		}
		if err := bus.MapMMIO(
			"w4200-indirect-register-bus",
			indirectBase,
			system.QualcommIndirectRegisterBusWindowSize,
			indirectBus,
		); err != nil {
			return fmt.Errorf("map SPH-W4200 indirect register bus: %w", err)
		}
		board.LatchedRegisters = remaining
		_ = touchscreen // The external sub-interrupt route is attached after firmware registration is identified.
	}
	if cascade := board.LegacyInterruptCascade; cascade != nil && cascade.SubInterruptWindowID != "" {
		var cascadeWindow *system.LatchedRegisterWindow
		for _, spec := range board.LatchedRegisterWindows {
			if spec.ID != cascade.SubInterruptWindowID {
				continue
			}
			if cascadeWindow != nil {
				return fmt.Errorf("duplicate Qualcomm sub-interrupt window %q", spec.ID)
			}
			window, err := system.NewLatchedRegisterWindow(spec.Size, spec.Width)
			if err != nil {
				return fmt.Errorf("create Qualcomm sub-interrupt window %q: %w", spec.ID, err)
			}
			if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, window); err != nil {
				return fmt.Errorf("map Qualcomm sub-interrupt window %q: %w", spec.ID, err)
			}
			cascadeWindow = window
			existingWindows[spec.ID] = window
		}
		if cascadeWindow == nil {
			return fmt.Errorf("missing Qualcomm sub-interrupt window %q", cascade.SubInterruptWindowID)
		}
		if err := legacyInterrupts.AttachInterruptSink(&qualcommInterruptCascadeSink{
			target:       vectoredInterrupts,
			source:       cascade.VectoredSource,
			statusWindow: cascadeWindow,
			windowOffset: cascade.SubInterruptStatusOffset,
			windowMask:   cascade.SubInterruptMask,
		}); err != nil {
			return fmt.Errorf("attach Qualcomm sub-interrupt cascade: %w", err)
		}
	}
	if err := board.ApplyLatchedRegistersWithInterruptsAndExistingWindows(
		bus, legacyInterrupts, vectoredInterrupts, existingWindows,
	); err != nil {
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
	if board.PanelSelectorPorts != nil {
		if err := mapSelectorPanelPorts(bus, "parallel-panel", *board.PanelSelectorPorts, panel); err != nil {
			return fmt.Errorf("map %s selector panel ports: %w", board.ID, err)
		}
	} else if board.PanelPorts == nil {
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
	commandDevice := system.Device(commandPort)
	dataDevice := system.Device(dataPort)
	portSize := uint32(system.Width16)
	if ports.AliasSpan != 0 {
		portSize = ports.AliasSpan
		commandDevice = &parallelPanelAddressAlias{ParallelPanelPort: commandPort}
		dataDevice = &parallelPanelAddressAlias{ParallelPanelPort: dataPort}
	}
	if err := bus.MapMMIO(
		name+"-command",
		ports.CommandAddress,
		portSize,
		commandDevice,
	); err != nil {
		return err
	}
	return bus.MapMMIO(
		name+"-data",
		ports.DataAddress,
		portSize,
		dataDevice,
	)
}

// parallelPanelAddressAlias collapses low, physically undecoded external-bus
// address bits while retaining the panel port's reset and snapshot behavior.
type parallelPanelAddressAlias struct {
	*system.ParallelPanelPort
}

func (p *parallelPanelAddressAlias) Read(_ uint32, width system.Width) (uint32, error) {
	return p.ParallelPanelPort.Read(0, width)
}

func (p *parallelPanelAddressAlias) Write(_ uint32, width system.Width, value uint32) error {
	return p.ParallelPanelPort.Write(0, width, value)
}

func mapSelectorPanelPorts(
	bus *system.Bus,
	name string,
	ports system.ParallelPanelSelectorPortProfile,
	panel *system.ParallelPanelInterface,
) error {
	selectorPort, transferPort, err := system.NewParallelPanelSelectorPorts(
		panel,
		ports.CommandSelect,
		ports.DataSelect,
	)
	if err != nil {
		return fmt.Errorf("create selector transport: %w", err)
	}
	if err := bus.MapMMIO(
		name+"-selector",
		ports.SelectorAddress,
		uint32(system.Width16),
		selectorPort,
	); err != nil {
		return err
	}
	return bus.MapMMIO(
		name+"-transfer",
		ports.TransferAddress,
		uint32(system.Width16),
		transferPort,
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
		if errors.Is(result.Err, system.ErrHLEPowerCycle) {
			if err := m.powerCycleLocked(); err != nil {
				return cpu.Result{
					Reason: cpu.StopFault, Instructions: retired, PC: m.pc,
					Err: fmt.Errorf("automatic guest power cycle: %w", err),
				}
			}
			if budget != 0 && retired == budget {
				return cpu.Result{Reason: cpu.StopBudget, Instructions: retired, PC: m.pc}
			}
			continue
		}
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

// SetTouch changes the panel-space contact reported by a profiled touchscreen.
func (m *Machine) SetTouch(x, y int, pressed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	if m.touchscreen == nil {
		return ErrUnsupportedControl
	}
	return m.touchscreen.SetTouch(x, y, pressed)
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

// PanelProtocolReport returns the cumulative diagnostic transfer inference
// when the machine was constructed with Options.ProbePanelProtocol. The report
// contains only addresses, standardized protocol facts, and bounded counters;
// it does not retain pixel or firmware data.
func (m *Machine) PanelProtocolReport() (system.LCDTransferReport, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.panelProbe == nil {
		return system.LCDTransferReport{}, false
	}
	return m.panelProbe.Report(), true
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
