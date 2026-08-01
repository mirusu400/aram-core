package application

import (
	"crypto/sha256"
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	DefaultDebugSnapshotEntries = 4096
	MaxDebugSnapshotEntries     = 16384
)

// DebugSnapshot is a bounded, serialization-friendly view of an application
// machine. It contains execution metadata and logs, but never source bytes,
// guest memory, framebuffer pixels, persistence, or media payloads.
type DebugSnapshot struct {
	Runtime    string                `json:"runtime"`
	State      string                `json:"state"`
	CPU        *DebugCPUSnapshot     `json:"cpu,omitempty"`
	LastResult *DebugExecutionResult `json:"last_result,omitempty"`
	GuestLog   DebugLogSnapshot      `json:"guest_log"`
	HostTrace  DebugLogSnapshot      `json:"host_trace"`
	KTF        *DebugKTFSnapshot     `json:"ktf,omitempty"`
	Raptor     *DebugRaptorSnapshot  `json:"raptor,omitempty"`
	SKVM       *DebugSKVMSnapshot    `json:"skvm,omitempty"`
}

type DebugCPUSnapshot struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Architecture string          `json:"architecture"`
	Mode         string          `json:"mode"`
	Registers    []DebugRegister `json:"registers,omitempty"`
	ReadErrors   []string        `json:"read_errors,omitempty"`
}

type DebugRegister struct {
	Name  string `json:"name"`
	Value uint32 `json:"value"`
	Hex   string `json:"hex"`
}

type DebugExecutionResult struct {
	Reason       string `json:"reason"`
	Instructions uint64 `json:"instructions"`
	PC           uint32 `json:"pc"`
	PCHex        string `json:"pc_hex"`
	Error        string `json:"error,omitempty"`
}

type DebugLogSnapshot struct {
	Total   int      `json:"total"`
	Omitted int      `json:"omitted"`
	Entries []string `json:"entries,omitempty"`
}

type DebugKTFSnapshot struct {
	PresentCount          uint32   `json:"present_count"`
	TickMS                uint64   `json:"tick_ms"`
	TaskCount             int      `json:"task_count"`
	ActiveInstructions    uint64   `json:"active_instructions"`
	LastJavaMethod        string   `json:"last_java_method,omitempty"`
	LastUnimplementedJava string   `json:"last_unimplemented_java,omitempty"`
	FirstJavaThrow        string   `json:"first_java_throw,omitempty"`
	FirstJavaThrowSP      uint32   `json:"first_java_throw_sp,omitempty"`
	LastJavaThrow         string   `json:"last_java_throw,omitempty"`
	LastJavaThrowSP       uint32   `json:"last_java_throw_sp,omitempty"`
	JavaExceptionFrames   []string `json:"java_exception_frames,omitempty"`
}

type DebugRaptorSnapshot struct {
	ModuleInitialized bool                    `json:"module_initialized"`
	Started           bool                    `json:"started"`
	ImportCalls       int                     `json:"import_calls"`
	ImportsOmitted    int                     `json:"imports_omitted"`
	Imports           []DebugRaptorImportCall `json:"imports,omitempty"`
}

type DebugRaptorImportCall struct {
	Ordinal uint32    `json:"ordinal"`
	Args    [4]uint32 `json:"args"`
	LR      uint32    `json:"lr"`
}

type DebugSKVMSnapshot struct {
	MainClass      string                    `json:"main_class"`
	Started        bool                      `json:"started"`
	MIDlet         uint32                    `json:"midlet"`
	CurrentDisplay uint32                    `json:"current_display"`
	Instructions   uint64                    `json:"instructions"`
	QueuedInput    int                       `json:"queued_input"`
	Framebuffer    *DebugFramebufferSnapshot `json:"framebuffer,omitempty"`
}

// DebugFramebufferSnapshot identifies a presented frame without including its
// pixels. The canonical RGBA digest can be compared with a separately captured
// screenshot to distinguish core rendering from downstream frame corruption.
type DebugFramebufferSnapshot struct {
	Surface         string `json:"surface"`
	Sequence        uint64 `json:"sequence"`
	Width           int32  `json:"width"`
	Height          int32  `json:"height"`
	Stride          int32  `json:"stride"`
	Format          uint8  `json:"format"`
	RGBABytes       int    `json:"rgba_bytes"`
	RGBASHA256      string `json:"rgba_sha256"`
	SnapshotHashOK  bool   `json:"snapshot_hash_ok"`
	DescriptorValid bool   `json:"descriptor_valid"`
}

// DebugSnapshot returns a consistent diagnostic snapshot. Callers should
// serialize machine operations around this method if they also maintain a
// higher-level logical running state.
func (m *Machine) DebugSnapshot(maxEntries int) DebugSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := normalizeDebugSnapshotLimit(maxEntries)
	snapshot := DebugSnapshot{
		Runtime:   machineRuntimeName(m),
		State:     m.state.String(),
		CPU:       debugCPUSnapshot(m.cpu),
		GuestLog:  debugLogSnapshot(nil, 0, limit),
		HostTrace: debugLogSnapshot(nil, 0, limit),
	}
	if m.lastResult.Instructions != 0 ||
		m.lastResult.PC != 0 ||
		m.lastResult.Err != nil ||
		m.state == machinecore.StateFaulted {
		snapshot.LastResult = debugExecutionResult(m.lastResult)
	}
	if m.wipi != nil {
		snapshot.GuestLog = debugLogSnapshot(m.wipi.logs, 0, limit)
	}
	if m.ktf != nil {
		snapshot.HostTrace = debugLogSnapshot(
			m.ktf.hostTrace,
			m.ktf.hostTraceDropped,
			limit,
		)
		snapshot.KTF = &DebugKTFSnapshot{
			PresentCount:          m.ktf.presentCount,
			TickMS:                m.ktf.tickMS,
			TaskCount:             len(m.ktf.tasks),
			ActiveInstructions:    m.ktf.activeInstructions,
			LastJavaMethod:        m.ktf.lastJavaMethod,
			LastUnimplementedJava: m.ktf.lastUnimplementedJava,
			FirstJavaThrow:        m.ktf.firstJavaThrowName,
			FirstJavaThrowSP:      m.ktf.firstJavaThrowSP,
			LastJavaThrow:         m.ktf.lastJavaThrowName,
			LastJavaThrowSP:       m.ktf.lastJavaThrowSP,
			JavaExceptionFrames: append(
				[]string(nil),
				m.ktf.javaExceptionFrames...,
			),
		}
	}
	if m.raptor != nil {
		start := max(0, len(m.raptor.importTrace)-limit)
		imports := make(
			[]DebugRaptorImportCall,
			0,
			len(m.raptor.importTrace)-start,
		)
		for _, call := range m.raptor.importTrace[start:] {
			imports = append(imports, DebugRaptorImportCall{
				Ordinal: call.Ordinal,
				Args:    call.Args,
				LR:      call.LR,
			})
		}
		snapshot.Raptor = &DebugRaptorSnapshot{
			ModuleInitialized: m.raptor.moduleInitialized,
			Started:           m.raptor.started,
			ImportCalls:       len(m.raptor.importTrace),
			ImportsOmitted:    start,
			Imports:           imports,
		}
	}
	return snapshot
}

func (m *skvmMachine) DebugSnapshot(maxEntries int) DebugSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := DebugSnapshot{
		Runtime:   "skvm",
		State:     m.state.String(),
		GuestLog:  debugLogSnapshot(nil, 0, normalizeDebugSnapshotLimit(maxEntries)),
		HostTrace: debugLogSnapshot(nil, 0, normalizeDebugSnapshotLimit(maxEntries)),
		SKVM: &DebugSKVMSnapshot{
			MainClass:   m.mainClass,
			Started:     m.started,
			MIDlet:      m.midlet,
			QueuedInput: len(m.input),
		},
	}
	if m.vm != nil {
		snapshot.SKVM.CurrentDisplay = m.vm.CurrentDisplay()
		snapshot.SKVM.Instructions = m.vm.Instructions
	}
	if m.vm != nil && m.services != nil {
		frame := m.services.Graphics.LastFrame()
		if frame.Sequence != 0 {
			actualHash := sha256.Sum256(frame.RGBA)
			framebuffer := &DebugFramebufferSnapshot{
				Surface:        frame.SurfaceID.String(),
				Sequence:       frame.Sequence,
				Width:          frame.Width,
				Height:         frame.Height,
				RGBABytes:      len(frame.RGBA),
				RGBASHA256:     fmt.Sprintf("%x", actualHash),
				SnapshotHashOK: actualHash == frame.Hash,
			}
			if descriptor, err := m.services.Graphics.Descriptor(
				m.owner,
				m.vm.ScreenSurface(),
			); err == nil {
				framebuffer.Stride = descriptor.Stride
				framebuffer.Format = uint8(descriptor.Format)
				framebuffer.DescriptorValid =
					descriptor.Width == frame.Width &&
						descriptor.Height == frame.Height &&
						descriptor.Stride == frame.Width*4 &&
						descriptor.Format == shared.PixelRGBA8888 &&
						uint64(len(frame.RGBA)) ==
							uint64(frame.Width)*uint64(frame.Height)*4
			}
			snapshot.SKVM.Framebuffer = framebuffer
		}
	}
	return snapshot
}

func normalizeDebugSnapshotLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultDebugSnapshotEntries
	case limit > MaxDebugSnapshotEntries:
		return MaxDebugSnapshotEntries
	default:
		return limit
	}
}

func machineRuntimeName(machine *Machine) string {
	switch {
	case machine.ktf != nil:
		return "ktf"
	case machine.raptor != nil:
		return "raptor"
	case machine.minigame != nil:
		return "eads"
	case machine.wipi != nil:
		return "wipi-c"
	default:
		return "native"
	}
}

func debugCPUSnapshot(backend cpu.Backend) *DebugCPUSnapshot {
	if backend == nil {
		return nil
	}
	identity := backend.Identity()
	snapshot := &DebugCPUSnapshot{
		Name:         identity.Name,
		Version:      identity.Version,
		Architecture: string(identity.Architecture),
		Mode:         "unknown",
		Registers:    make([]DebugRegister, 0, len(debugRegisterNames)),
	}
	for id, name := range debugRegisterNames {
		value, err := backend.ReadRegister(uint32(id))
		if err != nil {
			snapshot.ReadErrors = append(
				snapshot.ReadErrors,
				fmt.Sprintf("%s: %v", name, err),
			)
			continue
		}
		snapshot.Registers = append(snapshot.Registers, DebugRegister{
			Name:  name,
			Value: value,
			Hex:   fmt.Sprintf("0x%08x", value),
		})
		if id == int(cpu.RegisterCPSR) {
			snapshot.Mode = "arm"
			if value&cpu.StatusThumb != 0 {
				snapshot.Mode = "thumb"
			}
		}
	}
	return snapshot
}

var debugRegisterNames = [...]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "r11", "r12", "sp", "lr", "pc", "cpsr",
}

func debugExecutionResult(result cpu.Result) *DebugExecutionResult {
	snapshot := &DebugExecutionResult{
		Reason:       debugStopReason(result.Reason),
		Instructions: result.Instructions,
		PC:           result.PC,
		PCHex:        fmt.Sprintf("0x%08x", result.PC),
	}
	if result.Err != nil {
		snapshot.Error = result.Err.Error()
	}
	return snapshot
}

func debugStopReason(reason cpu.StopReason) string {
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
		return "unknown"
	}
}

// debugLogSnapshot returns the newest entries within limit. dropped counts
// entries a bounded log already discarded before this call, so Total and
// Omitted stay truthful for logs that roll over during a long session.
func debugLogSnapshot(entries []string, dropped, limit int) DebugLogSnapshot {
	start := max(0, len(entries)-limit)
	return DebugLogSnapshot{
		Total:   dropped + len(entries),
		Omitted: dropped + start,
		Entries: append([]string(nil), entries[start:]...),
	}
}
