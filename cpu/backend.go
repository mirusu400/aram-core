package cpu

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidAddress              = errors.New("invalid guest address")
	ErrInvalidMapping              = errors.New("invalid guest memory mapping")
	ErrPermissionDenied            = errors.New("guest memory permission denied")
	ErrUnsupportedInstruction      = errors.New("unsupported guest instruction")
	ErrExecutionContextUnavailable = errors.New("fast execution context unavailable")
	ErrStopped                     = errors.New("CPU execution stopped")
	ErrClosed                      = errors.New("CPU backend is closed")
)

type Permissions uint8

const (
	PermissionRead Permissions = 1 << iota
	PermissionWrite
	PermissionExecute
)

func (p Permissions) Valid() bool {
	return p != 0 && p&^(PermissionRead|PermissionWrite|PermissionExecute) == 0
}

const (
	RegisterR0 uint32 = iota
	RegisterR1
	RegisterR2
	RegisterR3
	RegisterR4
	RegisterR5
	RegisterR6
	RegisterR7
	RegisterR8
	RegisterR9
	RegisterR10
	RegisterR11
	RegisterR12
	RegisterSP
	RegisterLR
	RegisterPC
	RegisterCPSR
)

// StatusThumb is the architectural CPSR T bit.
const StatusThumb uint32 = 1 << 5

type Architecture string

const (
	ARMv4T  Architecture = "armv4t"
	ARMv5TE Architecture = "armv5te"
)

func (a Architecture) Valid() bool {
	return a == ARMv4T || a == ARMv5TE
}

type Identity struct {
	Name         string
	Version      string
	Architecture Architecture
}

func (i Identity) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return fmt.Errorf("CPU backend name is empty")
	}
	if strings.TrimSpace(i.Version) == "" {
		return fmt.Errorf("CPU backend %q version is empty", i.Name)
	}
	if !i.Architecture.Valid() {
		return fmt.Errorf("CPU backend %q has invalid architecture %q", i.Name, i.Architecture)
	}
	return nil
}

type Mode uint8

const (
	ModeARM Mode = iota
	ModeThumb
)

func (m Mode) Valid() bool {
	return m == ModeARM || m == ModeThumb
}

type StopReason uint8

const (
	StopRequested StopReason = iota
	StopBreakpoint
	StopFault
	StopBudget
	StopExited
	StopExecutionTrap
)

func (r StopReason) Valid() bool {
	return r >= StopRequested && r <= StopExecutionTrap
}

type Result struct {
	Reason       StopReason
	Instructions uint64
	PC           uint32
	Err          error
}

// MemoryBus is the physical-memory boundary used by whole-system machines.
// Application backends may continue using Backend.Map and private mappings;
// a backend advertises system support separately through SystemBusBackend.
type MemoryBus interface {
	Read(address uint32, destination []byte, permission Permissions) error
	Write(address uint32, source []byte, permission Permissions) error
}

// ExternalAbortError marks a physical-bus error which real hardware would
// deliver to the CPU as an external abort. Unsupported device semantics must
// not implement this contract: those remain explicit host implementation
// boundaries instead of being hidden behind a guest exception.
type ExternalAbortError interface {
	error
	ExternalAbort() bool
}

// MemoryAccessContext attributes a physical bus access to the guest
// instruction that caused it. Whole-system buses may use this optional context
// for diagnostics without coupling CPU backends to platform-specific devices.
type MemoryAccessContext struct {
	InstructionAddress uint32
	LinkAddress        uint32
	StackAddress       uint32
	Mode               Mode
	Attributed         bool
}

// BlockMemoryBus optionally transfers a whole span in one call. The width-typed
// access a device needs is wrong for a bulk copy: filling a thirty-two byte
// instruction-cache line as eight separate word accesses made the bus resolve
// the region and take its lock eight times for one line. A bus implements this
// for spans that resolve to plain memory and declines the rest, which keeps
// device semantics on the per-width path where they belong.
type BlockMemoryBus interface {
	MemoryBus
	ReadBlock(address uint32, destination []byte, permission Permissions) (bool, error)
	WriteBlock(address uint32, source []byte, permission Permissions) (bool, error)
}

// DirectMemoryRegion describes one stable, plain-memory mapping a CPU may
// access without re-entering the physical bus. Data is the complete region and
// Address is its guest-physical base. The bus retains ownership of Data; a CPU
// must discard it when the invalidator registered through DirectMemoryBus is
// called.
type DirectMemoryRegion struct {
	Address     uint32
	Data        []byte
	Permissions Permissions
}

// DirectMemoryBus optionally exposes ordinary RAM to a CPU backend. It is a
// cache-fill contract, not a replacement for MemoryBus: MMIO, sparse memory,
// ROM, observed accesses, region-boundary accesses, and permission failures are
// declined and continue through the ordinary bus path.
//
// SetDirectMemoryInvalidator installs the callback the bus invokes whenever a
// mapping or observer change could make a previously returned region unsafe to
// access directly. A system bus has one attached CPU, so replacing the callback
// replaces the previous attachment.
type DirectMemoryBus interface {
	MemoryBus
	DirectMemoryRegion(address uint32, size int, permission Permissions) (DirectMemoryRegion, bool)
	SetDirectMemoryInvalidator(func())
}

// ContextMemoryBus optionally receives the guest instruction context for each
// physical access. Backends fall back to MemoryBus when it is not implemented.
type ContextMemoryBus interface {
	MemoryBus
	ReadContext(MemoryAccessContext, uint32, []byte, Permissions) error
	WriteContext(MemoryAccessContext, uint32, []byte, Permissions) error
}

type SystemBusBackend interface {
	Backend
	AttachSystemBus(MemoryBus) error
}

// ExecutionTrap identifies an instruction address that whole-system code owns
// as an explicit host boundary. The trapped instruction is not retired and no
// guest bytes are changed. Application-mode backends need not implement this
// optional contract.
type ExecutionTrap struct {
	Address uint32
	Mode    Mode
}

func (t ExecutionTrap) Valid() bool {
	return t.Mode.Valid() &&
		(t.Mode != ModeARM || t.Address&3 == 0) &&
		(t.Mode != ModeThumb || t.Address&1 == 0)
}

type ExecutionTrapBackend interface {
	Backend
	SetExecutionTraps([]ExecutionTrap) error
}

// InterruptLine names the two asynchronous exception inputs exposed by
// classic ARM cores. Platform interrupt controllers drive these level inputs;
// the CPU applies CPSR masking and exception priority at instruction
// boundaries.
type InterruptLine uint8

const (
	InterruptIRQ InterruptLine = iota
	InterruptFIQ
)

func (l InterruptLine) Valid() bool {
	return l == InterruptIRQ || l == InterruptFIQ
}

type InterruptLineBackend interface {
	Backend
	SetInterruptLine(InterruptLine, bool) error
}

type SystemCapability uint64

const (
	CapabilityPhysicalBus SystemCapability = 1 << iota
	CapabilityPrivilegedModes
	CapabilityCP15Control
	CapabilityMMU
	CapabilityExceptions
	CapabilityInterruptLines
	CapabilityExecutionTraps
)

type SystemCapabilities uint64

func (c SystemCapabilities) Has(capability SystemCapability) bool {
	return uint64(c)&uint64(capability) != 0
}

type SystemBackend interface {
	SystemBusBackend
	SystemCapabilities() SystemCapabilities
}

// ExecutionContext is a backend-owned, reusable architectural register
// snapshot. It is intentionally not a portable save-state: cooperative
// application schedulers use it to switch tasks without discarding mappings or
// translated code shared by every task in the same guest address space.
type ExecutionContext interface {
	CPUExecutionContext()
}

// ExecutionContextBackend is an optional fast context-switch capability.
// SaveExecutionContext reuses destination when it belongs to this backend;
// callers pass nil for the first capture. MarshalExecutionContext emits the
// ordinary portable SaveContext representation only when persistence needs it.
// A backend must reject this capability whenever retaining mapping or
// translation caches would be unsafe.
type ExecutionContextBackend interface {
	Backend
	SaveExecutionContext(destination ExecutionContext) (ExecutionContext, error)
	RestoreExecutionContext(ExecutionContext) error
	MarshalExecutionContext(ExecutionContext, []byte) ([]byte, error)
}

// ExecutionStatistics are cumulative counters for scheduler and translation
// behavior. Deltas around a frame expose cache-reset and translation costs
// without enabling per-instruction tracing.
type ExecutionStatistics struct {
	SerializedContextSaves    uint64 `json:"serialized_context_saves"`
	SerializedContextRestores uint64 `json:"serialized_context_restores"`
	FastContextSaves          uint64 `json:"fast_context_saves"`
	FastContextRestores       uint64 `json:"fast_context_restores"`
	TranslationInvalidations  uint64 `json:"translation_invalidations"`
	TranslatedBlocks          uint64 `json:"translated_blocks"`
	TranslatedGuestBytes      uint64 `json:"translated_guest_bytes"`
	TranslatedHostBytes       uint64 `json:"translated_host_bytes"`
	NativeArenaResets         uint64 `json:"native_arena_resets"`
	HostFrameCaptures         uint64 `json:"host_frame_captures"`
	HostRegisterCommits       uint64 `json:"host_register_commits"`
}

type ExecutionStatisticsBackend interface {
	Backend
	ExecutionStatistics() ExecutionStatistics
}

const MaxHostCallWords = 64

// HostCallFrame is one reentrant host-boundary snapshot. Registers holds
// r0-r15 and CPSR. Stack and Parameters are optional contiguous word ranges
// requested by the caller and captured under the same backend synchronization
// point as the registers.
type HostCallFrame struct {
	Registers      [17]uint32
	Stack          [MaxHostCallWords]uint32
	Parameters     [MaxHostCallWords]uint32
	StackWords     uint32
	ParameterWords uint32
}

type HostCallFrameRequest struct {
	StackWords       uint32
	ParameterAddress uint32
	ParameterWords   uint32
}

// RegisterCommit batches host-return register updates. Mask bit n selects
// Values[n]. Register IDs remain the public architectural IDs above.
type RegisterCommit struct {
	Values [17]uint32
	Mask   uint32
}

func (commit *RegisterCommit) Set(register, value uint32) error {
	if register >= uint32(len(commit.Values)) {
		return fmt.Errorf("register %d: %w", register, ErrInvalidAddress)
	}
	commit.Values[register] = value
	commit.Mask |= 1 << register
	return nil
}

type HostCallFrameBackend interface {
	Backend
	CaptureHostCallFrame(*HostCallFrame, HostCallFrameRequest) error
	CommitHostCallRegisters(RegisterCommit) error
}

// CaptureHostCallFrame uses the backend bulk capability when available and a
// scalar portable fallback otherwise.
func CaptureHostCallFrame(
	backend Backend,
	destination *HostCallFrame,
	request HostCallFrameRequest,
) error {
	if backend == nil || destination == nil {
		return fmt.Errorf("host-call frame: %w", ErrInvalidAddress)
	}
	if request.StackWords > MaxHostCallWords ||
		request.ParameterWords > MaxHostCallWords ||
		request.ParameterWords != 0 && request.ParameterAddress == 0 {
		return fmt.Errorf("host-call frame request: %w", ErrInvalidAddress)
	}
	if bulk, ok := backend.(HostCallFrameBackend); ok {
		return bulk.CaptureHostCallFrame(destination, request)
	}
	for register := range destination.Registers {
		value, err := backend.ReadRegister(uint32(register))
		if err != nil {
			return err
		}
		destination.Registers[register] = value
	}
	destination.StackWords = request.StackWords
	destination.ParameterWords = request.ParameterWords
	var encoded [4]byte
	stack := destination.Registers[RegisterSP]
	for index := uint32(0); index < request.StackWords; index++ {
		if err := backend.ReadMemory(stack+index*4, encoded[:]); err != nil {
			return err
		}
		destination.Stack[index] = binary.LittleEndian.Uint32(encoded[:])
	}
	for index := uint32(0); index < request.ParameterWords; index++ {
		if err := backend.ReadMemory(
			request.ParameterAddress+index*4,
			encoded[:],
		); err != nil {
			return err
		}
		destination.Parameters[index] = binary.LittleEndian.Uint32(encoded[:])
	}
	return nil
}

func CommitHostCallRegisters(backend Backend, commit RegisterCommit) error {
	if backend == nil || commit.Mask>>17 != 0 {
		return fmt.Errorf("host-call register commit: %w", ErrInvalidAddress)
	}
	if bulk, ok := backend.(HostCallFrameBackend); ok {
		return bulk.CommitHostCallRegisters(commit)
	}
	for register := uint32(0); register < 17; register++ {
		if commit.Mask&(1<<register) == 0 {
			continue
		}
		if err := backend.WriteRegister(register, commit.Values[register]); err != nil {
			return err
		}
	}
	return nil
}

type Backend interface {
	Identity() Identity
	Architecture() Architecture
	Map(address, size uint32, permissions Permissions) error
	ReadMemory(address uint32, destination []byte) error
	WriteMemory(address uint32, source []byte) error
	ReadRegister(id uint32) (uint32, error)
	WriteRegister(id, value uint32) error
	Run(context.Context, uint32, Mode, uint64) Result
	Stop() error
	SaveContext() ([]byte, error)
	RestoreContext([]byte) error
	Close() error
}
