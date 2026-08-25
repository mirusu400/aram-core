package cpu

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidAddress         = errors.New("invalid guest address")
	ErrInvalidMapping         = errors.New("invalid guest memory mapping")
	ErrPermissionDenied       = errors.New("guest memory permission denied")
	ErrUnsupportedInstruction = errors.New("unsupported guest instruction")
	ErrStopped                = errors.New("CPU execution stopped")
	ErrClosed                 = errors.New("CPU backend is closed")
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
