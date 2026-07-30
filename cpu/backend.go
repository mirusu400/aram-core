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
)

func (r StopReason) Valid() bool {
	return r >= StopRequested && r <= StopExited
}

type Result struct {
	Reason       StopReason
	Instructions uint64
	PC           uint32
	Err          error
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
