package debugkit

import (
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

var ErrCPUInspectionUnsupported = errors.New("machine does not expose CPU inspection")

type registerReader interface {
	ReadRegister(id uint32) (uint32, error)
}

type resultReader interface {
	LastResult() cpu.Result
}

type CPUReport struct {
	Registers  map[string]uint32 `json:"registers"`
	LastResult *ExecutionReport  `json:"last_result,omitempty"`
}

type ExecutionReport struct {
	Reason       string `json:"reason"`
	ReasonCode   uint8  `json:"reason_code"`
	Instructions uint64 `json:"instructions"`
	PC           uint32 `json:"pc"`
	Error        string `json:"error,omitempty"`
}

var registerNames = []struct {
	name string
	id   uint32
}{
	{"r0", cpu.RegisterR0},
	{"r1", cpu.RegisterR1},
	{"r2", cpu.RegisterR2},
	{"r3", cpu.RegisterR3},
	{"r4", cpu.RegisterR4},
	{"r5", cpu.RegisterR5},
	{"r6", cpu.RegisterR6},
	{"r7", cpu.RegisterR7},
	{"r8", cpu.RegisterR8},
	{"r9", cpu.RegisterR9},
	{"r10", cpu.RegisterR10},
	{"r11", cpu.RegisterR11},
	{"r12", cpu.RegisterR12},
	{"sp", cpu.RegisterSP},
	{"lr", cpu.RegisterLR},
	{"pc", cpu.RegisterPC},
	{"cpsr", cpu.RegisterCPSR},
}

func (s *Session) CPU() (CPUReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cpuLocked()
}

func (s *Session) cpuLocked() (CPUReport, error) {
	reader, ok := s.machine.(registerReader)
	if !ok {
		return CPUReport{}, ErrCPUInspectionUnsupported
	}
	report := CPUReport{
		Registers: make(map[string]uint32, len(registerNames)),
	}
	for _, register := range registerNames {
		value, err := reader.ReadRegister(register.id)
		if err != nil {
			return CPUReport{}, fmt.Errorf("read CPU register %s: %w", register.name, err)
		}
		report.Registers[register.name] = value
	}
	if reader, ok := s.machine.(resultReader); ok {
		result := reader.LastResult()
		execution := &ExecutionReport{
			Reason:       stopReasonName(result.Reason),
			ReasonCode:   uint8(result.Reason),
			Instructions: result.Instructions,
			PC:           result.PC,
		}
		if result.Err != nil {
			execution.Error = result.Err.Error()
		}
		report.LastResult = execution
	}
	return report, nil
}

func stopReasonName(reason cpu.StopReason) string {
	switch reason {
	case cpu.StopRequested:
		return "requested"
	case cpu.StopBreakpoint:
		return "breakpoint"
	case cpu.StopFault:
		return "fault"
	case cpu.StopBudget:
		return "budget"
	case cpu.StopExited:
		return "exited"
	default:
		return fmt.Sprintf("unknown(%d)", reason)
	}
}
